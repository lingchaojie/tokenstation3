package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

const expectedInsertQuery = "INSERT INTO llm_archive.model_call_archive (captured_at, capture_id, ingest_batch_id, request_id, session_id, platform, requested_model, upstream_model, upstream_endpoint, stream, http_status, stop_reason, thinking_effort, thinking_type, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, signature_present, is_truncated, request_truncated, response_truncated, request_observed_bytes, request_stored_bytes, response_observed_bytes, response_stored_bytes, request_sha256, response_sha256, spool_version, capture_version, raw_request, raw_response, request_headers, response_headers) FORMAT RowBinary"

func TestUploadUsesZstdRowBinaryAndStableDedupToken(t *testing.T) {
	batch := goldenFixtureBatch(t)
	type receivedRequest struct {
		method      string
		username    string
		password    string
		authOK      bool
		encoding    string
		contentType string
		query       string
		dedup       string
		body        []byte
		err         error
	}
	received := make(chan receivedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		decoder, err := zstd.NewReader(r.Body, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		var body []byte
		if err == nil {
			body, err = io.ReadAll(decoder)
			decoder.Close()
		}
		received <- receivedRequest{
			method:      r.Method,
			username:    username,
			password:    password,
			authOK:      ok,
			encoding:    r.Header.Get("Content-Encoding"),
			contentType: r.Header.Get("Content-Type"),
			query:       r.URL.Query().Get("query"),
			dedup:       r.URL.Query().Get("insert_deduplication_token"),
			body:        body,
			err:         err,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	uploader := newTestUploader(t, srv.URL, nil)
	require.NoError(t, uploader.Upload(context.Background(), batch))
	got := <-received
	require.NoError(t, got.err)
	require.Equal(t, http.MethodPost, got.method)
	require.True(t, got.authOK)
	require.Equal(t, "capture_ingest", got.username)
	require.Equal(t, "secret", got.password)
	require.Equal(t, "zstd", got.encoding)
	require.Equal(t, "application/octet-stream", got.contentType)
	require.Equal(t, expectedInsertQuery, got.query)
	require.Equal(t, batch.ID.String(), got.dedup)
	require.Equal(t, goldenRowHex, fmt.Sprintf("%x", got.body))
}

func TestUploadRejectsMalformedHashBeforeStartingHTTPRequest(t *testing.T) {
	batch := goldenFixtureBatch(t)
	batch.Records[0].Manifest.Request.SHA256 = "not-a-fixed-string-64-hash"
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := newTestUploader(t, srv.URL, nil).Upload(context.Background(), batch)
	require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
	require.Zero(t, requests.Load())
}

func TestUploadRejectsValidButInconsistentFullHashBeforeStartingHTTPRequest(t *testing.T) {
	batch := goldenFixtureBatch(t)
	batch.Records[0].Manifest.Request.SHA256 = strings.Repeat("0", 64)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := newTestUploader(t, srv.URL, nil).Upload(context.Background(), batch)
	require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
	require.Zero(t, requests.Load())
}

func TestUploadPreflightsImmutableFilesBeforeStartingRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		batch  func(*testing.T) *spool.Batch
		mutate func(*testing.T, *spool.Batch)
	}{
		{
			name:  "corrupt compressed file",
			batch: goldenFixtureBatch,
			mutate: func(t *testing.T, batch *spool.Batch) {
				path := filepath.Join(batch.Records[0].Path, "request.zst")
				payload, err := os.ReadFile(path)
				require.NoError(t, err)
				payload[len(payload)/2] ^= 0xff
				require.NoError(t, os.WriteFile(path, payload, 0o600))
			},
		},
		{
			name:  "missing declared file",
			batch: goldenFixtureBatch,
			mutate: func(t *testing.T, batch *spool.Batch) {
				require.NoError(t, os.Remove(filepath.Join(batch.Records[0].Path, "request.zst")))
			},
		},
		{
			name: "self-consistent invalid zstd for declared empty file",
			batch: func(t *testing.T) *spool.Batch {
				return fixtureBatch(t, fixtureOptions{
					Policy: model.ContentPolicy{StoreRequestBody: true},
					Final:  model.Final{ResponseComplete: true},
				})
			},
			mutate: func(t *testing.T, batch *spool.Batch) {
				rewriteFixtureFile(t, batch, "request.zst", []byte("not-zstd"), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := tt.batch(t)
			tt.mutate(t, batch)
			var roundTrips atomic.Int32
			uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
			uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				roundTrips.Add(1)
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					return nil, err
				}
				return responseFor(req, http.StatusOK, ""), nil
			})

			err := uploader.Upload(context.Background(), batch)
			require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
			require.Zero(t, roundTrips.Load(), "authenticated RoundTrip started before local integrity was proven")
		})
	}
}

func TestUploadRejectsCanonicalManifestAndReferenceMismatchesBeforeRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*spool.RecordRef)
	}{
		{name: "spool version", mutate: func(ref *spool.RecordRef) { ref.Manifest.SpoolVersion++ }},
		{name: "capture version", mutate: func(ref *spool.RecordRef) { ref.Manifest.CaptureVersion++ }},
		{name: "request completeness", mutate: func(ref *spool.RecordRef) { ref.Manifest.Request.Complete = false }},
		{name: "response completeness", mutate: func(ref *spool.RecordRef) { ref.Manifest.Response.Complete = false }},
		{name: "truncation consistency", mutate: func(ref *spool.RecordRef) { ref.Manifest.Request.Truncated = true }},
		{name: "record stored bytes", mutate: func(ref *spool.RecordRef) { ref.StoredBytes++ }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := goldenFixtureBatch(t)
			tt.mutate(&batch.Records[0])
			var roundTrips atomic.Int32
			uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
			uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				roundTrips.Add(1)
				return responseFor(req, http.StatusOK, ""), nil
			})

			err := uploader.Upload(context.Background(), batch)
			require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
			require.Zero(t, roundTrips.Load())
		})
	}
}

func TestUploadPreservesCanceledPreflightContextWithoutRoundTrip(t *testing.T) {
	batch := goldenFixtureBatch(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var roundTrips atomic.Int32
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		roundTrips.Add(1)
		return responseFor(req, http.StatusOK, ""), nil
	})

	err := uploader.Upload(ctx, batch)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, spool.ErrSpoolCorrupt)
	require.Zero(t, roundTrips.Load())
}

func TestUploadClassifiesRemoteAndNetworkFailures(t *testing.T) {
	batch := goldenFixtureBatch(t)
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "request timeout", status: http.StatusRequestTimeout, want: ErrRetryable},
		{name: "too early", status: http.StatusTooEarly, want: ErrRetryable},
		{name: "too many requests", status: http.StatusTooManyRequests, want: ErrRetryable},
		{name: "start of server error range", status: http.StatusInternalServerError, want: ErrRetryable},
		{name: "server error", status: http.StatusServiceUnavailable, want: ErrRetryable},
		{name: "end of server error range", status: 599, want: ErrRetryable},
		{name: "outside server error range", status: 600, want: ErrSchema},
		{name: "unauthenticated", status: http.StatusUnauthorized, want: ErrUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, want: ErrUnauthorized},
		{name: "schema or data rejection", status: http.StatusBadRequest, want: ErrSchema},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tt.status)
				_, _ = io.CopyN(w, bytes.NewReader(bytes.Repeat([]byte("remote error"), 1<<16)), 1<<19)
			}))
			defer srv.Close()

			err := newTestUploader(t, srv.URL, nil).Upload(context.Background(), batch)
			require.ErrorIs(t, err, tt.want)
		})
	}

	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("offline")}
	})
	err := uploader.Upload(context.Background(), batch)
	require.ErrorIs(t, err, ErrRetryable)
}

func TestSchemaRejectionPreservesImmutableBatchForExactRetry(t *testing.T) {
	batch := goldenFixtureBatch(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	uploader := newTestUploader(t, srv.URL, nil)

	err := uploader.Upload(context.Background(), batch)
	require.ErrorIs(t, err, ErrSchema)
	for _, ref := range batch.Records {
		require.DirExists(t, ref.Path)
	}
	require.NoError(t, uploader.Upload(context.Background(), batch))
	require.EqualValues(t, 2, calls.Load())
}

func TestUploadBoundsAndClosesHTTPResponseBody(t *testing.T) {
	batch := goldenFixtureBatch(t)
	body := &endlessResponseBody{}
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, err
		}
		resp := responseFor(req, http.StatusBadRequest, "")
		resp.Body = body
		return resp, nil
	})

	err := uploader.Upload(context.Background(), batch)
	require.ErrorIs(t, err, ErrSchema)
	require.True(t, body.closed.Load())
	require.Equal(t, maxHTTPResponseBytes+1, body.bytesRead.Load())
}

func TestUploadCancelsRequestBeforeDrainingStalledResponseBody(t *testing.T) {
	batch := goldenFixtureBatch(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &contextStalledBody{readStarted: make(chan struct{})}
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body.ctx = req.Context()
		resp := responseFor(req, http.StatusServiceUnavailable, "")
		resp.Body = body
		return resp, nil
	})

	done := make(chan error, 1)
	go func() { done <- uploader.Upload(ctx, batch) }()
	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Upload never began bounded response draining")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrRetryable)
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("Upload remained blocked after response headers stalled")
	}
	require.True(t, body.closed.Load())
}

func TestUploadEarlyHTTPFailureAndContextCancelDoNotDeadlockEncoder(t *testing.T) {
	batch := goldenFixtureBatch(t)
	tests := []struct {
		name      string
		transport roundTripperFunc
		want      error
	}{
		{
			name: "early response",
			transport: func(req *http.Request) (*http.Response, error) {
				return responseFor(req, http.StatusServiceUnavailable, "busy"), nil
			},
			want: ErrRetryable,
		},
		{
			name: "transport failure",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection lost before body read")
			},
			want: ErrRetryable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
			uploader.client.Transport = tt.transport
			done := make(chan error, 1)
			go func() { done <- uploader.Upload(context.Background(), batch) }()
			select {
			case err := <-done:
				require.ErrorIs(t, err, tt.want)
			case <-time.After(time.Second):
				t.Fatal("Upload leaked or deadlocked its encoder goroutine")
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	started := make(chan struct{})
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	done := make(chan error, 1)
	go func() { done <- uploader.Upload(ctx, batch) }()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("context cancellation leaked or deadlocked the encoder goroutine")
	}
}

func TestEncodeCompressedReportsOriginalErrorBeforeFlushingBackpressuredPipe(t *testing.T) {
	uploader := &HTTPUploader{}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- uploader.encodeCompressed(context.Background(), writer, nil)
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
	case <-time.After(250 * time.Millisecond):
		_ = reader.Close()
		err := <-done
		require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
		t.Fatal("original encoder error was trapped behind a flushing zstd Close")
	}
	_ = reader.Close()
}

func TestUploadLargeEarlyHTTPFailureIsRemoteRetryableNotLocalCorruption(t *testing.T) {
	payload := make([]byte, 4<<20)
	for i := range payload {
		payload[i] = byte(rand.Uint32())
	}
	batch := fixtureBatch(t, fixtureOptions{
		Policy:  model.ContentPolicy{StoreRequestBody: true},
		Request: payload,
		Final:   model.Final{HTTPStatus: 503, ResponseComplete: true},
	})
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return responseFor(req, http.StatusServiceUnavailable, "busy"), nil
	})

	err := uploader.Upload(context.Background(), batch)
	require.ErrorIs(t, err, ErrRetryable)
	require.NotErrorIs(t, err, spool.ErrSpoolCorrupt)
}

func TestUploadEncoderCorruptionWinsOverRemoteSchemaClassification(t *testing.T) {
	batch := goldenFixtureBatch(t)
	path := batch.Records[0].Path + "/request.zst"
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data[0] ^= 0xff
	require.NoError(t, os.WriteFile(path, data, 0o600))
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, readErr := io.Copy(io.Discard, req.Body)
		if readErr != nil {
			return nil, readErr
		}
		return responseFor(req, http.StatusBadRequest, "bad row"), nil
	})

	done := make(chan error, 1)
	go func() { done <- uploader.Upload(context.Background(), batch) }()
	select {
	case err = <-done:
		require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
		require.NotErrorIs(t, err, ErrSchema)
	case <-time.After(time.Second):
		t.Fatal("encoder error leaked or deadlocked the upload goroutine")
	}
}

func TestProbeUsesSameDialerAndExactAuthenticatedPing(t *testing.T) {
	type probeRequest struct {
		method   string
		path     string
		rawQuery string
		user     string
		pass     string
		ok       bool
	}
	received := make(chan probeRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		received <- probeRequest{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery, user: user, pass: pass, ok: ok}
		_, _ = io.WriteString(w, "Ok.\n")
	}))
	defer srv.Close()
	serverAddress := srv.Listener.Addr().String()
	var dialCalls atomic.Int32
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialCalls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
	}
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123/base?query=must-not-leak", dial)

	require.NoError(t, uploader.Probe(context.Background()))
	got := <-received
	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, "/ping", got.path)
	require.Empty(t, got.rawQuery)
	require.True(t, got.ok)
	require.Equal(t, "capture_ingest", got.user)
	require.Equal(t, "secret", got.pass)
	require.EqualValues(t, 1, dialCalls.Load())
}

func TestProbeRejectsAnythingExceptExactClickHousePing(t *testing.T) {
	for _, body := range []string{"Ok.", "Ok.\nextra", strings.Repeat("x", 1<<20)} {
		t.Run(fmt.Sprintf("length_%d", len(body)), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			err := newTestUploader(t, srv.URL, nil).Probe(context.Background())
			require.ErrorIs(t, err, ErrRetryable)
		})
	}
}

func TestProbeClassifiesStatusWhenResponseBodyStalls(t *testing.T) {
	tests := []struct {
		name   string
		status int
		prefix string
		want   error
	}{
		{name: "success without EOF is retryable", status: http.StatusOK, prefix: "Ok.\n", want: ErrRetryable},
		{name: "unauthenticated", status: http.StatusUnauthorized, prefix: "x", want: ErrUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, prefix: "x", want: ErrUnauthorized},
		{name: "schema rejection", status: http.StatusBadRequest, prefix: "x", want: ErrSchema},
		{name: "server failure", status: http.StatusServiceUnavailable, prefix: "x", want: ErrRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &prefixThenStalledBody{
				prefix:      []byte(tt.prefix),
				readStarted: make(chan struct{}),
				closedCh:    make(chan struct{}),
			}
			uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
			uploader.responseBodyTimeout = 25 * time.Millisecond
			uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				resp := responseFor(req, tt.status, "")
				resp.Body = body
				return resp, nil
			})

			done := make(chan error, 1)
			go func() { done <- uploader.Probe(context.Background()) }()
			select {
			case <-body.readStarted:
			case <-time.After(time.Second):
				_ = body.Close()
				t.Fatal("Probe did not reach the stalled response body")
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, tt.want)
				switch tt.want {
				case ErrUnauthorized:
					var typed *UnauthorizedError
					require.ErrorAs(t, err, &typed)
					require.Equal(t, tt.status, typed.StatusCode)
				case ErrSchema:
					var typed *SchemaError
					require.ErrorAs(t, err, &typed)
					require.Equal(t, tt.status, typed.StatusCode)
				case ErrRetryable:
					var typed *RetryableError
					require.ErrorAs(t, err, &typed)
					if tt.status == http.StatusOK {
						require.Zero(t, typed.StatusCode)
						require.Error(t, typed.Cause)
					} else {
						require.Equal(t, tt.status, typed.StatusCode)
					}
				}
			case <-time.After(250 * time.Millisecond):
				_ = body.Close()
				<-done
				t.Fatal("Probe remained blocked after its response body deadline")
			}
			require.True(t, body.closed.Load())
		})
	}
}

func TestUploadLargeFieldMakesHTTPProgressBeforeReadingWholeCompressedFile(t *testing.T) {
	const (
		rawBytes           = 32 << 20
		compressedReadGate = 4 << 20
		httpBodyProgress   = 256 << 10
	)
	batch := fixtureBatch(t, fixtureOptions{
		Policy: model.ContentPolicy{StoreRequestBody: true},
		Final:  model.Final{HTTPStatus: 200, ResponseComplete: true},
		WriteFixture: func(t *testing.T, sink interface {
			WriteRequestHeaders([]byte) error
			WriteResponseHeaders([]byte) error
			WriteRequest([]byte) error
			WriteResponse([]byte) error
		}) {
			chunk := make([]byte, 64<<10)
			state := uint64(0x9e3779b97f4a7c15)
			for range rawBytes / len(chunk) {
				for offset := 0; offset < len(chunk); offset += 8 {
					state ^= state << 13
					state ^= state >> 7
					state ^= state << 17
					binary.LittleEndian.PutUint64(chunk[offset:], state)
				}
				require.NoError(t, sink.WriteRequest(chunk))
			}
		},
	})
	inputBlocked := make(chan struct{})
	releaseInput := make(chan struct{})
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.encoder = RowBinaryEncoder{openRawFile: func(path string) (io.ReadCloser, error) {
		file, err := os.Open(path)
		if err != nil || filepath.Base(path) != "request.zst" {
			return file, err
		}
		return &gatedReadCloser{
			ReadCloser: file,
			remaining:  compressedReadGate,
			blocked:    inputBlocked,
			release:    releaseInput,
		}, nil
	}}
	outputProgress := make(chan struct{})
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		buffer := make([]byte, 32<<10)
		var read int
		for read < httpBodyProgress {
			n, err := req.Body.Read(buffer)
			read += n
			if err != nil {
				return nil, err
			}
		}
		close(outputProgress)
		return responseFor(req, http.StatusServiceUnavailable, "busy"), nil
	})
	done := make(chan error, 1)
	go func() {
		done <- uploader.Upload(context.Background(), batch)
	}()
	waitDone := func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("streaming upload did not terminate")
			return nil
		}
	}

	select {
	case <-outputProgress:
		close(releaseInput)
		require.ErrorIs(t, waitDone(), ErrRetryable)
	case <-inputBlocked:
		close(releaseInput)
		_ = waitDone()
		t.Fatal("encoder read more than 4 MiB of compressed input before producing 256 KiB of the HTTP body")
	case <-time.After(3 * time.Second):
		close(releaseInput)
		_ = waitDone()
		t.Fatal("encoder made no progressive-flow decision")
	}
}

func newTestUploader(t *testing.T, endpoint string, dial DialContextFunc) *HTTPUploader {
	t.Helper()
	uploader, err := NewHTTPUploader(HTTPConfig{
		URL:         endpoint,
		Database:    "llm_archive",
		Table:       "model_call_archive",
		Username:    "capture_ingest",
		Password:    "secret",
		DialContext: dial,
	})
	require.NoError(t, err)
	return uploader
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

type endlessResponseBody struct {
	bytesRead atomic.Int64
	closed    atomic.Bool
}

type contextStalledBody struct {
	ctx         context.Context
	readStarted chan struct{}
	started     atomic.Bool
	closed      atomic.Bool
}

type prefixThenStalledBody struct {
	prefix      []byte
	readStarted chan struct{}
	closedCh    chan struct{}
	started     atomic.Bool
	closed      atomic.Bool
}

type gatedReadCloser struct {
	io.ReadCloser
	remaining int64
	blocked   chan struct{}
	release   chan struct{}
	signaled  atomic.Bool
}

type fixtureDiskManifest struct {
	model.Manifest
	BodyLimitBytes   uint64 `json:"body_limit_bytes"`
	HeaderLimitBytes uint64 `json:"header_limit_bytes"`
}

func rewriteFixtureFile(t *testing.T, batch *spool.Batch, name string, compressed, decoded []byte) {
	t.Helper()
	ref := &batch.Records[0]
	require.NoError(t, os.WriteFile(filepath.Join(ref.Path, name), compressed, 0o600))
	found := false
	for i := range ref.Manifest.Files {
		if ref.Manifest.Files[i].Name != name {
			continue
		}
		ref.Manifest.Files[i].CompressedBytes = uint64(len(compressed))
		ref.Manifest.Files[i].UncompressedBytes = uint64(len(decoded))
		ref.Manifest.Files[i].CompressedSHA256 = sha256HexForUploadTest(compressed)
		ref.Manifest.Files[i].UncompressedSHA256 = sha256HexForUploadTest(decoded)
		found = true
		break
	}
	require.True(t, found, "fixture manifest has no declared file %q", name)
	rewriteFixtureManifest(t, batch)
}

func rewriteFixtureManifest(t *testing.T, batch *spool.Batch) {
	t.Helper()
	ref := &batch.Records[0]
	manifestPath := filepath.Join(ref.Path, "manifest.json")
	encoded, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var disk fixtureDiskManifest
	require.NoError(t, json.Unmarshal(encoded, &disk))
	disk.Manifest = ref.Manifest
	encoded, err = json.Marshal(disk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, encoded, 0o600))
}

func sha256HexForUploadTest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (b *endlessResponseBody) Read(payload []byte) (int, error) {
	b.bytesRead.Add(int64(len(payload)))
	return len(payload), nil
}

func (b *endlessResponseBody) Close() error {
	b.closed.Store(true)
	return nil
}

func (b *contextStalledBody) Read([]byte) (int, error) {
	if b.started.CompareAndSwap(false, true) {
		close(b.readStarted)
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextStalledBody) Close() error {
	b.closed.Store(true)
	return nil
}

func (b *prefixThenStalledBody) Read(payload []byte) (int, error) {
	if len(b.prefix) > 0 {
		n := copy(payload, b.prefix)
		b.prefix = b.prefix[n:]
		return n, nil
	}
	if b.started.CompareAndSwap(false, true) {
		close(b.readStarted)
	}
	<-b.closedCh
	return 0, io.ErrClosedPipe
}

func (b *prefixThenStalledBody) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		close(b.closedCh)
	}
	return nil
}

func (r *gatedReadCloser) Read(payload []byte) (int, error) {
	if r.remaining <= 0 {
		if r.signaled.CompareAndSwap(false, true) {
			close(r.blocked)
		}
		<-r.release
	} else if int64(len(payload)) > r.remaining {
		payload = payload[:r.remaining]
	}
	n, err := r.ReadCloser.Read(payload)
	r.remaining -= int64(n)
	return n, err
}

func responseFor(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

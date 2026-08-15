package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
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

func TestEncodeLargeBatchDoesNotMaterializeBodyStringsOrByteSlices(t *testing.T) {
	batch := fixtureBatch(t, fixtureOptions{
		Policy: model.ContentPolicy{StoreRequestBody: true, StoreResponseBody: true},
		Final:  model.Final{HTTPStatus: 200, ResponseComplete: true},
		WriteFixture: func(t *testing.T, sink interface {
			WriteRequestHeaders([]byte) error
			WriteResponseHeaders([]byte) error
			WriteRequest([]byte) error
			WriteResponse([]byte) error
		}) {
			chunk := bytes.Repeat([]byte{0}, 64<<10)
			for range (32 << 20) / len(chunk) {
				require.NoError(t, sink.WriteRequest(chunk))
			}
			for range (32 << 20) / len(chunk) {
				require.NoError(t, sink.WriteResponse(chunk))
			}
		},
	})
	uploader := newTestUploader(t, "http://clickhouse.invalid:8123", nil)
	uploader.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, err := io.Copy(io.Discard, req.Body)
		if err != nil {
			return nil, err
		}
		return responseFor(req, http.StatusOK, ""), nil
	})

	peak := peakHeapGrowth(t, func() error { return uploader.Upload(context.Background(), batch) })
	require.Less(t, peak, uint64(48<<20), "a whole 32 MiB field allocation plus streaming codec memory exceeds this bound")
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

func (b *endlessResponseBody) Read(payload []byte) (int, error) {
	b.bytesRead.Add(int64(len(payload)))
	return len(payload), nil
}

func (b *endlessResponseBody) Close() error {
	b.closed.Store(true)
	return nil
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

func peakHeapGrowth(t *testing.T, run func() error) uint64 {
	t.Helper()
	runtime.GC()
	previousGC := debug.SetGCPercent(25)
	defer debug.SetGCPercent(previousGC)
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	peak := baseline.HeapAlloc
	done := make(chan error, 1)
	go func() { done <- run() }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case err := <-done:
			require.NoError(t, err)
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if stats.HeapAlloc > peak {
				peak = stats.HeapAlloc
			}
			if peak <= baseline.HeapAlloc {
				return 0
			}
			return peak - baseline.HeapAlloc
		case <-ticker.C:
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if stats.HeapAlloc > peak {
				peak = stats.HeapAlloc
			}
		case <-timeout.C:
			t.Fatal("large streaming upload timed out")
		}
	}
}

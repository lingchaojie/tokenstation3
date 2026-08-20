package spool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestAttemptPreservesBinaryPayloadsInIndependentZstdStreams(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, policyAll())
	request := []byte{0xff, 0x00, 0x61, 0xc3, 0x28}
	response := []byte{0x00, 0xfe, 0xfd, 0x7f}
	require.NoError(t, a.WriteRequest(request[:2]))
	require.NoError(t, a.WriteRequest(request[2:]))
	require.NoError(t, a.WriteResponse(response))
	require.NoError(t, a.Commit())

	require.Equal(t, request, readZstdFile(t, readyPath(s, a.ID(), "request.zst")))
	require.Equal(t, response, readZstdFile(t, readyPath(s, a.ID(), "response.zst")))
	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, len(request), manifest.Request.StoredBytes)
	require.EqualValues(t, len(response), manifest.Response.StoredBytes)
	require.Equal(t, hashHex(request), manifest.Request.SHA256)
	require.Equal(t, hashHex(response), manifest.Response.SHA256)
}

func TestAttemptTruncatesPersistedPrefixButObservesAndHashesEveryByte(t *testing.T) {
	s := openTestStore(t, nil)
	s.config.MaxBodyBytes = 5
	a := beginAttempt(t, s, policyAll())
	payload := []byte{0xff, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	require.NoError(t, a.WriteRequest(payload[:3]))
	require.NoError(t, a.WriteRequest(payload[3:]))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, len(payload), manifest.Request.ObservedBytes)
	require.EqualValues(t, 5, manifest.Request.StoredBytes)
	require.Equal(t, hashHex(payload), manifest.Request.SHA256)
	require.True(t, manifest.Request.Truncated)
	require.Equal(t, payload[:5], readZstdFile(t, readyPath(s, a.ID(), "request.zst")))
}

func TestAttemptWithZeroLimitsPersistsCompleteBodyAndHeaders(t *testing.T) {
	s := openTestStore(t, nil)
	s.config.MaxBodyBytes = 0
	s.config.MaxHeaderBytes = 0
	a := beginAttempt(t, s, policyAll())
	body := bytes.Repeat([]byte("b"), 64)
	header := bytes.Repeat([]byte("h"), 32)

	require.NoError(t, a.WriteRequest(body))
	require.NoError(t, a.WriteRequestHeaders(header))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, len(body), manifest.Request.ObservedBytes)
	require.EqualValues(t, len(body), manifest.Request.StoredBytes)
	require.False(t, manifest.Request.Truncated)
	require.EqualValues(t, len(header), manifest.RequestHeaders.ObservedBytes)
	require.EqualValues(t, len(header), manifest.RequestHeaders.StoredBytes)
	require.False(t, manifest.RequestHeaders.Truncated)
	require.Equal(t, body, readZstdFile(t, readyPath(s, a.ID(), "request.zst")))
	require.Equal(t, header, readZstdFile(t, readyPath(s, a.ID(), "request_headers.zst")))
}

func TestHeaderPoliciesAndLimitsAreIndependent(t *testing.T) {
	s := openTestStore(t, nil)
	s.config.MaxHeaderBytes = 3
	a := beginAttempt(t, s, model.ContentPolicy{
		StoreRequestHeaders: true,
		StoreResponseBody:   true,
	})
	require.NoError(t, a.WriteRequestHeaders([]byte("abcdef")))
	require.NoError(t, a.WriteResponseHeaders([]byte("discarded")))
	require.NoError(t, a.WriteResponse([]byte("body")))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, 6, manifest.RequestHeaders.ObservedBytes)
	require.EqualValues(t, 3, manifest.RequestHeaders.StoredBytes)
	require.True(t, manifest.RequestHeaders.Truncated)
	require.EqualValues(t, 9, manifest.ResponseHeaders.ObservedBytes)
	require.Zero(t, manifest.ResponseHeaders.StoredBytes)
	require.Equal(t, []byte("abc"), readZstdFile(t, readyPath(s, a.ID(), "request_headers.zst")))
	require.NoFileExists(t, readyPath(s, a.ID(), "response_headers.zst"))
}

func TestDisabledRawStorageStillExtractsWithoutCreatingContentFile(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		Format:    model.PayloadSSE,
		Policy:    model.ContentPolicy{StoreResponseBody: false},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)

	require.NoError(t, a.WriteResponse([]byte("data: {\"usage\":{\"output_tokens\":9}}\n\n")))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, 9, manifest.Extracted.OutputTokens)
	require.NoFileExists(t, readyPath(s, a.ID(), "response.zst"))
}

func TestAttemptFallsBackToBeginSessionIDWhenWireBodyHasNoSession(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		SessionID: "capture-session",
		Format:    model.PayloadJSON,
		Policy:    model.ContentPolicy{StoreRequestBody: true},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	require.NoError(t, a.WriteRequest([]byte(`{"model":"claude-opus-5"}`)))
	a.mu.Lock()
	a.finishExtractionLocked()
	extracted := a.extracted
	a.mu.Unlock()
	a.Abort(errors.New("test cleanup"))

	require.Equal(t, "capture-session", extracted.SessionID)
}

func TestAttemptPrefersWireBodySessionOverBeginFallback(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		SessionID: "capture-session",
		Format:    model.PayloadJSON,
		Policy:    model.ContentPolicy{StoreRequestBody: true},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	require.NoError(t, a.WriteRequest([]byte(`{"conversation_id":"wire-session"}`)))
	a.mu.Lock()
	a.finishExtractionLocked()
	extracted := a.extracted
	a.mu.Unlock()
	a.Abort(errors.New("test cleanup"))

	require.Equal(t, "wire-session", extracted.SessionID)
}

func TestMalformedExtractionDoesNotPreventCommitOrLeakPayloadInWarning(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		Format:    model.PayloadJSON,
		Policy:    model.ContentPolicy{StoreResponseBody: true},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	body := []byte("{\"secret\":\"do-not-log\"")

	require.NoError(t, a.WriteResponse(body))
	require.NoError(t, a.WriteResponse([]byte("still-do-not-log")))
	require.NoError(t, a.Finalize(model.Final{HTTPStatus: 200, OutputTokens: 4, StopReason: "terminal"}))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, 4, manifest.Extracted.OutputTokens)
	require.Equal(t, "terminal", manifest.Extracted.StopReason)
	require.Equal(t, append(body, []byte("still-do-not-log")...), readZstdFile(t, readyPath(s, a.ID(), "response.zst")))
	logOutput := logs.String()
	require.Equal(t, 1, strings.Count(logOutput, `"msg":"capture metadata extraction failed"`))
	require.Contains(t, logOutput, `"capture_id":"`+a.ID().String()+`"`)
	require.Contains(t, logOutput, `"error_category":"metadata_extraction_failed"`)
	require.NotContains(t, logOutput, "do-not-log")
	require.NotContains(t, logOutput, "still-do-not-log")
}

func TestNonUTF8RawContentRemainsCommitableWhenExtractionFails(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		Format:    model.PayloadJSON,
		Policy:    model.ContentPolicy{StoreResponseBody: true},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	body := []byte{0xff, 0x00, 0xfe, 0x7f}

	require.NoError(t, a.WriteResponse(body))
	require.NoError(t, a.Commit())

	require.Equal(t, body, readZstdFile(t, readyPath(s, a.ID(), "response.zst")))
}

func TestExtractionWarningIsLoggedOnlyOnceAfterRepeatedFailures(t *testing.T) {
	s := openTestStore(t, nil)
	sink, err := s.Open(model.Begin{
		CaptureID: uuid.New(),
		Format:    model.PayloadSSE,
		Policy:    model.ContentPolicy{StoreResponseBody: false},
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	secret := []byte("do-not-log-repeated-secret")
	oversized := append([]byte("data: "), bytes.Repeat(secret, 48<<10)...)

	require.NoError(t, a.WriteResponse(oversized))
	require.NoError(t, a.WriteResponse(secret))
	require.NoError(t, a.Commit())

	logOutput := logs.String()
	require.Equal(t, 1, strings.Count(logOutput, `"msg":"capture metadata extraction failed"`))
	require.NotContains(t, logOutput, string(secret))
}

func TestResponseFramingClosesRequestEncoder(t *testing.T) {
	recorder := &eventRecorder{}
	s := openTestStore(t, recorder)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("request")))
	require.NoError(t, a.WriteResponse([]byte("response")))

	require.Contains(t, recorder.allEvents(), "close-writer:request.zst")
	a.Abort(errors.New("test cleanup"))
}

func TestResponseFirstLeavesARecoverableEmptyRequestStream(t *testing.T) {
	root := t.TempDir()
	s := openTestStoreAt(t, root, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteResponse([]byte("response")))
	require.NoError(t, a.Commit())

	reopened := openTestStoreAt(t, root, nil)
	report, err := reopened.Recover(t.Context())

	require.NoError(t, err)
	require.Len(t, report.Ready, 1)
	require.Zero(t, report.CorruptDeleted)
}

func TestAbortDeletesPartialAndReleasesAttemptSlot(t *testing.T) {
	s := openTestStoreWithMaxAttempts(t, 1)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("partial")))
	a.Abort(errors.New("disconnect"))

	require.NoDirExists(t, filepathForAttempt(s, a.ID()))
	replacement := beginAttempt(t, s, policyAll())
	replacement.Abort(errors.New("test cleanup"))
}

func TestCapacityFailureAbortsNewPartialWithoutDeletingReadyRecords(t *testing.T) {
	root := t.TempDir()
	s := openTestStoreAt(t, root, nil)
	ready := beginAttempt(t, s, policyAll())
	require.NoError(t, ready.WriteRequest([]byte("older")))
	require.NoError(t, ready.Commit())

	setCapacityUsage(s.capacity, usage{Allocated: s.config.MaxBytes, Free: 1 << 40})
	newer := beginAttemptWithoutFailure(t, s, model.ContentPolicy{})
	require.Nil(t, newer.attempt)
	require.ErrorIs(t, newer.err, ErrSpoolCap)
	require.DirExists(t, readyPath(s, ready.ID(), "."))
}

func TestFrameCapacityFailureAbortsPartialReleasesSlotAndKeepsOlderReady(t *testing.T) {
	root := t.TempDir()
	s := openTestStoreAt(t, root, nil)
	older := beginAttempt(t, s, policyAll())
	require.NoError(t, older.WriteRequest([]byte("older")))
	require.NoError(t, older.Commit())

	newer := beginAttempt(t, s, policyAll())
	contentLimit := s.config.MaxBytes - s.config.OperationalHeadroomBytes
	setCapacityUsage(s.capacity, usage{
		Allocated: contentLimit - attemptOverheadBytes - 32<<10,
		Free:      1 << 40,
	})

	err := newer.WriteRequest(bytes.Repeat([]byte{0xff}, 64<<10))

	require.ErrorIs(t, err, ErrSpoolCap)
	require.NoDirExists(t, filepathForAttempt(s, newer.ID()))
	require.DirExists(t, readyPath(s, older.ID(), "."))
	setCapacityUsage(s.capacity, usage{Free: 1 << 40})
	replacement := beginAttempt(t, s, policyAll())
	replacement.Abort(errors.New("test cleanup"))
}

func TestManifestFileStatsMatchCompressedAndUncompressedStreams(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, policyAll())
	payload := bytes.Repeat([]byte{0xff, 0x00, 0x61}, 1024)
	require.NoError(t, a.WriteRequest(payload))
	require.NoError(t, a.Commit())

	manifest := readManifest(t, s, a.ID())
	var requestFile *model.FileStat
	for i := range manifest.Files {
		if manifest.Files[i].Name == "request.zst" {
			requestFile = &manifest.Files[i]
			break
		}
	}
	require.NotNil(t, requestFile)
	compressed, err := os.ReadFile(readyPath(s, a.ID(), "request.zst"))
	require.NoError(t, err)
	require.EqualValues(t, len(compressed), requestFile.CompressedBytes)
	require.EqualValues(t, len(payload), requestFile.UncompressedBytes)
	require.Equal(t, hashHex(compressed), requestFile.CompressedSHA256)
	require.Equal(t, hashHex(payload), requestFile.UncompressedSHA256)
}

func readZstdFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	decoder, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	require.NoError(t, err)
	defer decoder.Close()
	b, err := io.ReadAll(decoder)
	require.NoError(t, err)
	return b
}

func hashHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func filepathForAttempt(s *Store, id uuid.UUID) string {
	return s.partialDir + string(os.PathSeparator) + id.String()
}

type attemptResult struct {
	attempt *Attempt
	err     error
}

func beginAttemptWithoutFailure(t *testing.T, s *Store, policy model.ContentPolicy) attemptResult {
	t.Helper()
	sink, err := s.Open(model.Begin{CaptureID: uuid.New(), Policy: policy})
	if err != nil {
		return attemptResult{err: err}
	}
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	return attemptResult{attempt: a}
}

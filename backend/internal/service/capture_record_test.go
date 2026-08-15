package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type repeatedCaptureByteReader struct {
	remaining int64
	value     byte
}

type repeatedCapturePatternReader struct {
	remaining int64
	pattern   []byte
	offset    int
}

func (r *repeatedCapturePatternReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.pattern[r.offset]
		r.offset = (r.offset + 1) % len(r.pattern)
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func (r *repeatedCaptureByteReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.value
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func largeCaptureMetadataBody(size int64) io.Reader {
	return io.MultiReader(
		strings.NewReader(`{"padding":"`),
		&repeatedCaptureByteReader{remaining: size, value: 'x'},
		strings.NewReader(`","model":"mapped-tail-model","stream":true}`),
	)
}

func denseCaptureMetadataBody(size int64) io.Reader {
	return io.MultiReader(
		strings.NewReader(`{"junk":[`),
		&repeatedCapturePatternReader{remaining: size, pattern: []byte("0,")},
		strings.NewReader(`0],"model":"mapped-tail-model","stream":true}`),
	)
}

type delayedTranslatorSource struct {
	readStarted  chan struct{}
	closeSignal  chan struct{}
	readReturned chan struct{}
	startOnce    sync.Once
	closeOnce    sync.Once
	returnOnce   sync.Once
}

type closeReleasedCaptureReader struct {
	closed chan struct{}
	once   sync.Once
}

type dataAndErrorCaptureBody struct {
	payload []byte
	err     error
	done    bool
}

func (r *dataAndErrorCaptureBody) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.payload), r.err
}

func (*dataAndErrorCaptureBody) Close() error { return nil }

func TestSnapshotHTTPRequestBodyForCaptureRestoresPrefixWhenReadReturnsDataAndError(t *testing.T) {
	payload := []byte(`{"model":"provider-final","input":"must-not-disappear"}`)
	sentinel := errors.New("forced request body read failure")
	req := httptest.NewRequest(http.MethodPost, "https://provider.example/v1/responses", nil)
	req.Body = &dataAndErrorCaptureBody{payload: payload, err: sentinel}
	req.GetBody = nil

	captured, truncated, hash, _, _, _ := snapshotHTTPRequestBodyForCapture(req, 1<<20)
	require.Nil(t, captured, "failed snapshots must not be archived as complete requests")
	require.False(t, truncated)
	require.Empty(t, hash)

	replayed, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, payload, replayed, "capture observation must not consume bytes before the real provider transport")
}

func (r *closeReleasedCaptureReader) Read(p []byte) (int, error) {
	<-r.closed
	return copy(p, []byte("tail-before-scanner-exit\n")), io.EOF
}

func (r *closeReleasedCaptureReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func newDelayedTranslatorSource() *delayedTranslatorSource {
	return &delayedTranslatorSource{
		readStarted:  make(chan struct{}),
		closeSignal:  make(chan struct{}),
		readReturned: make(chan struct{}),
	}
}

func (r *delayedTranslatorSource) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.readStarted) })
	<-r.closeSignal
	time.Sleep(50 * time.Millisecond)
	r.returnOnce.Do(func() { close(r.readReturned) })
	return 0, io.EOF
}

func (r *delayedTranslatorSource) Close() error {
	r.closeOnce.Do(func() { close(r.closeSignal) })
	return nil
}

func TestBuildErrorCaptureRecord(t *testing.T) {
	// both empty -> nil (nothing to archive)
	if buildErrorCaptureRecord(nil, "anthropic", "m", "m", "", false, nil, nil, 1024) != nil {
		t.Fatal("empty req+resp must return nil")
	}

	resp := &http.Response{
		StatusCode: 429,
		Header:     http.Header{"X-Request-Id": {"req-err-1"}, "Authorization": {"Bearer secret"}},
		Request:    &http.Request{Header: http.Header{"X-Api-Key": {"sk-xxx"}, "Anthropic-Version": {"2023-06-01"}}},
	}
	errBody := []byte(`{"type":"error","error":{"type":"rate_limit_error"}}`)
	rec := buildErrorCaptureRecord(resp, "anthropic", "claude-x", "claude-x", "", true, []byte(`{"model":"claude-x"}`), errBody, 1024)
	if rec == nil {
		t.Fatal("expected a record")
	}
	if rec.HTTPStatus != 429 || rec.RequestID != "req-err-1" || rec.Platform != "anthropic" || !rec.Stream {
		t.Fatalf("bad envelope: %+v", rec)
	}
	if string(rec.RawResponse) != string(errBody) {
		t.Fatalf("raw response mismatch: %q", rec.RawResponse)
	}
	// credentials stripped, upstream diagnostic headers kept
	if strings.Contains(string(rec.RequestHeaders), "sk-xxx") || strings.Contains(string(rec.ResponseHeaders), "secret") {
		t.Fatal("credentials must be stripped from captured headers")
	}
	if !strings.Contains(string(rec.RequestHeaders), "2023-06-01") {
		t.Fatalf("upstream request headers must be kept: %s", rec.RequestHeaders)
	}

	// truncation applies to error bodies too
	rec2 := buildErrorCaptureRecord(nil, "openai", "m", "m", "", false, nil, []byte("0123456789"), 4)
	if string(rec2.RawResponse) != "0123" || !rec2.Truncated {
		t.Fatalf("truncation failed: %q trunc=%v", rec2.RawResponse, rec2.Truncated)
	}

	// A request-only overflow is still a truncated capture even if the error
	// response itself fits in the configured bound.
	rec3 := buildErrorCaptureRecord(nil, "openai", "m", "m", "", false, []byte("abcdefghij"), []byte("err"), 4)
	if string(rec3.RawRequest) != "abcd" || string(rec3.RawResponse) != "err" || !rec3.Truncated {
		t.Fatalf("request-only truncation failed: req=%q resp=%q trunc=%v", rec3.RawRequest, rec3.RawResponse, rec3.Truncated)
	}
}

func TestCaptureBridgeIsTakenOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCaptureResult(c, nil, []byte("attempt-one"), false)

	first := attachCaptureToForwardResult(c, &ForwardResult{})
	second := attachCaptureToForwardResult(c, &ForwardResult{})
	if string(first.CaptureResponse) != "attempt-one" {
		t.Fatalf("first result capture = %q", first.CaptureResponse)
	}
	if len(second.CaptureResponse) != 0 {
		t.Fatalf("bridge was reused by a second result: %q", second.CaptureResponse)
	}
}

func TestFailedForwardCaptureUsesTerminalOutcomePolicy(t *testing.T) {
	tests := []struct {
		name            string
		failed          bool
		terminalCapture bool
		wantPolicy      bool
	}{
		{name: "success excluded", failed: false, wantPolicy: false},
		{name: "terminal included", failed: true, wantPolicy: true},
		{name: "billable partial uses terminal outcome", terminalCapture: true, wantPolicy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			policy := DefaultCaptureRuntimePolicy()
			policy.Enabled = true
			policy.Platforms.Anthropic = true
			policy.Outcomes.Success = false
			policy.Outcomes.TerminalError = true
			compiled, err := CompileCaptureRuntimePolicy(policy)
			require.NoError(t, err)
			setCompiledCaptureScopeForTest(c, compiled, 9, nil)
			setCapturePlatform(c, PlatformAnthropic)
			setCaptureResult(c, &http.Response{StatusCode: http.StatusOK}, []byte(`{"provider":"body"}`), false)

			result := attachCaptureToForwardResult(c, &ForwardResult{UpstreamFailed: tt.failed, CaptureTerminalError: tt.terminalCapture})
			require.NotNil(t, result.CaptureResponse)
			require.Equal(t, tt.wantPolicy, result.CaptureContentPolicy != nil)
		})
	}
}

func TestFailedOpenAIHTTPCaptureUsesTerminalOutcomePolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	setCapturePlatform(c, PlatformOpenAI)
	setCaptureResult(c, &http.Response{StatusCode: http.StatusOK}, []byte(`{"provider":"body"}`), false)

	result := attachCaptureToOpenAIForwardResult(c, &OpenAIForwardResult{UpstreamFailed: true})
	require.NotNil(t, result.CaptureResponse)
	require.NotNil(t, result.CaptureContentPolicy)
}

func TestCaptureBridgeAttemptIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	beginCaptureAttempt(c)
	setCaptureResult(c, nil, []byte("failed-attempt"), false)
	if got := attachCaptureToForwardResult(c, nil); got != nil {
		t.Fatal("nil failover result must stay nil")
	}

	// The next attempt has capture disabled, so beginning it must discard the
	// unclaimed bridge from the failed attempt.
	beginCaptureAttempt(c)
	second := attachCaptureToForwardResult(c, &ForwardResult{})
	if len(second.CaptureResponse) != 0 {
		t.Fatalf("stale bridge leaked into disabled attempt: %q", second.CaptureResponse)
	}

	// Reusing a Gin context for another completed request is isolated too.
	beginCaptureAttempt(c)
	setCaptureResult(c, nil, []byte("captured"), false)
	beginCaptureAttempt(c)
	reused := attachCaptureToOpenAIForwardResult(c, &OpenAIForwardResult{})
	if len(reused.CaptureResponse) != 0 {
		t.Fatalf("reused context leaked capture: %q", reused.CaptureResponse)
	}
}

func TestCaptureResponseRequestMetadataOverridesPreTransportFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	enableCaptureForTest(t, c)
	initial := httptest.NewRequest(http.MethodPost, "https://cli-chat-proxy.example/v1/responses", nil)
	initial.Header.Set("X-XAI-Token-Auth", "cli-secret")
	setCapturePlatform(c, PlatformOpenAI)
	SetCaptureOutboundRequest(c, initial, []byte(`{"model":"initial-model","stream":true}`), 1024)
	finalBody := []byte(`{"model":"final-model","stream":false}`)
	final, err := http.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", bytes.NewReader(finalBody))
	require.NoError(t, err)
	final.Header.Set("Accept", "application/json")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": {"official-response"}},
		Request:    final,
	}
	setCaptureResult(c, resp, []byte(`{"ok":true}`), false)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, final.URL.String(), bridge.UpstreamEndpoint)
	require.Contains(t, string(bridge.RequestHeaders), "Accept")
	require.NotContains(t, string(bridge.RequestHeaders), "X-Xai-Token-Auth")
	require.Equal(t, finalBody, bridge.UpstreamRequest)
	require.Equal(t, "final-model", bridge.UpstreamModel)
	require.True(t, bridge.UpstreamStreamKnown)
	require.False(t, bridge.UpstreamStream)
	require.Equal(t, []byte(`{"ok":true}`), bridge.Response)
}

func TestCaptureResponseReplacementKeepsTailMetadataBeyondRawLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	enableCaptureForTest(t, c)
	setCapturePlatform(c, PlatformOpenAI)
	initial := httptest.NewRequest(http.MethodPost, "https://provider.example/initial", nil)
	SetCaptureOutboundRequest(c, initial, []byte(`{"model":"initial-model","stream":true}`), captureHardMaxBodyBytes)

	endpoint, err := url.Parse("https://provider.example/final")
	require.NoError(t, err)
	final := &http.Request{Method: http.MethodPost, URL: endpoint, Header: make(http.Header), Body: http.NoBody}
	final.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(largeCaptureMetadataBody(16 << 20)), nil }
	setCaptureResult(c, &http.Response{StatusCode: http.StatusOK, Request: final}, []byte(`{"ok":true}`), false)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, bridge.UpstreamRequest, captureHardMaxBodyBytes)
	require.True(t, bridge.RequestTruncated)
	require.Equal(t, "mapped-tail-model", bridge.UpstreamModel)
	require.True(t, bridge.UpstreamStreamKnown)
	require.True(t, bridge.UpstreamStream)
}

func TestCaptureResponseRedirectReplacesInitialPOSTBodyWithFinalEmptyGET(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	enableCaptureForTest(t, c)
	setCapturePlatform(c, PlatformOpenAI)
	initialBody := []byte(`{"model":"initial-model","stream":true}`)
	initial := httptest.NewRequest(http.MethodPost, "https://provider.example/start", bytes.NewReader(initialBody))
	SetCaptureOutboundRequest(c, initial, initialBody, 1024)
	final := httptest.NewRequest(http.MethodGet, "https://provider.example/final", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Request: final}

	setCaptureResult(c, resp, []byte(`{"ok":true}`), false)
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, final.URL.String(), bridge.UpstreamEndpoint)
	require.NotNil(t, bridge.UpstreamRequest)
	require.Empty(t, bridge.UpstreamRequest)
	require.Equal(t, HashUsageRequestPayload(nil), bridge.UpstreamRequestHash)
	require.Empty(t, bridge.UpstreamModel)
	require.False(t, bridge.UpstreamStreamKnown)
}

func TestSetCaptureUpstreamRequestRedirectReplacesInitialPOSTBodyWithFinalEmptyGET(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	initialBody := []byte(`{"initial":"post"}`)
	initial := httptest.NewRequest(http.MethodPost, "https://provider.example/start", bytes.NewReader(initialBody))
	setCaptureUpstreamRequest(c, initial, 1024)
	final := httptest.NewRequest(http.MethodGet, "https://provider.example/final", nil)

	setCaptureResult(c, &http.Response{StatusCode: http.StatusOK, Request: final}, []byte(`{"ok":true}`), false)
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.NotNil(t, bridge.UpstreamRequest)
	require.Empty(t, bridge.UpstreamRequest)
	require.Equal(t, final.URL.String(), bridge.UpstreamEndpoint)
}

func TestCompletedRequestSnapshotDoesNotInventEmptyBodyForUnknownLengthReader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://provider.example/final", nil)
	req.Body = io.NopCloser(strings.NewReader("real-final-body"))
	req.GetBody = nil
	req.ContentLength = 0

	body, truncated, hash, observed, _, _, _ := snapshotCompletedHTTPRequestBodyForCapture(req, 1024)
	require.False(t, observed)
	require.Nil(t, body)
	require.False(t, truncated)
	require.Empty(t, hash)
}

func TestUnobservableReplacementRequestFailsCaptureClosed(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	initialBody := []byte(`{"initial":"post"}`)
	initial := httptest.NewRequest(http.MethodPost, "https://provider.example/start", bytes.NewReader(initialBody))
	setCaptureUpstreamRequest(c, initial, 1024)
	final := httptest.NewRequest(http.MethodPost, "https://provider.example/final", nil)
	final.Body = io.NopCloser(strings.NewReader("unknown-final-body"))
	final.GetBody = nil
	final.ContentLength = 0
	setCaptureResult(c, &http.Response{StatusCode: http.StatusOK, Request: final}, []byte(`{"ok":true}`), false)

	result := attachCaptureToForwardResult(c, &ForwardResult{})
	require.Nil(t, result.CaptureResponse, "an unobservable replacement request must not be paired with stale pre-redirect bytes")
	require.Nil(t, result.CaptureRequest)
}

func TestCapturePolicyMissDoesNotCreateAttemptSlot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)

	beginCaptureAttempt(c)
	ResetCaptureExchange(c)
	_, captured := takeCaptureResult(c)
	require.False(t, captured)
	_, exists := c.Get(captureResultContextKey)
	require.False(t, exists, "a policy miss must not allocate or mutate a capture slot")
}

func TestLateCaptureFinalizerCannotOverwriteNewAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	firstReq := httptest.NewRequest(http.MethodPost, "https://first.example/v1/messages", nil)
	SetCaptureOutboundRequest(c, firstReq, []byte(`{"attempt":1}`), 1024)
	firstResp := &http.Response{
		StatusCode: http.StatusOK,
		Request:    firstReq,
		Body:       io.NopCloser(strings.NewReader("first-response")),
	}
	finishFirst := beginCaptureResponse(c, firstResp, true, 1024)
	_, err := io.ReadAll(firstResp.Body)
	require.NoError(t, err)

	secondReq := httptest.NewRequest(http.MethodPost, "https://second.example/v1/messages", nil)
	SetCaptureOutboundRequest(c, secondReq, []byte(`{"attempt":2}`), 1024)
	secondResp := &http.Response{
		StatusCode: http.StatusOK,
		Request:    secondReq,
		Body:       io.NopCloser(strings.NewReader("second-response")),
	}
	finishSecond := beginCaptureResponse(c, secondResp, true, 1024)
	_, err = io.ReadAll(secondResp.Body)
	require.NoError(t, err)
	finishSecond()
	finishFirst() // stale translator/finalizer publishes after the new attempt

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.JSONEq(t, `{"attempt":2}`, string(bridge.UpstreamRequest))
	require.Equal(t, "second-response", string(bridge.Response))
	require.Equal(t, "https://second.example/v1/messages", bridge.UpstreamEndpoint)
}

func TestBuildTerminalErrorCaptureRecordUsesFinalExchangeAndPolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.TerminalError = true
	policy.Content.RawResponse = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req, err := http.NewRequest(http.MethodPost, "https://api.example.test/v1/messages", bytes.NewReader(nil))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"final-model"}`), 1024)

	rec := BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, &UpstreamFailoverError{
		StatusCode:              http.StatusTooManyRequests,
		ResponseBody:            []byte(`{"error":{"message":"busy"}}`),
		ResponseHeaders:         http.Header{"X-Request-Id": []string{"req-final"}},
		HasUpstreamHTTPResponse: true,
	}, 1024)
	require.NotNil(t, rec)
	require.Equal(t, http.StatusTooManyRequests, rec.HTTPStatus)
	require.Equal(t, "https://api.example.test/v1/messages", rec.UpstreamEndpoint)
	require.JSONEq(t, `{"model":"final-model"}`, string(rec.RawRequest))
	require.NotContains(t, string(rec.RequestHeaders), "secret")
	require.NotNil(t, rec.ContentPolicy)
	require.False(t, rec.ContentPolicy.RawResponse)
}

func TestBuildTerminalErrorCaptureRecordSkipsLocalOrDisabledOutcomes(t *testing.T) {
	failure := &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, ResponseBody: []byte("busy")}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	require.Nil(t, BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, failure, 1024), "local error has no exchange evidence")

	policy.Outcomes.TerminalError = false
	compiled, err = CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	failure.UpstreamEndpoint = "https://api.example.test/v1/messages"
	require.Nil(t, BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, failure, 1024))
}

func TestBuildTerminalErrorCaptureRecordRequiresObservedProviderRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	c.Set("parsed_request", &ParsedRequest{Model: "client-model", Body: NewRequestBodyRef([]byte(`{"model":"inbound-compat"}`))})

	require.Nil(t, BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, &UpstreamFailoverError{
		StatusCode:              http.StatusBadGateway,
		ResponseBody:            []byte(`{"error":"provider"}`),
		UpstreamEndpoint:        "https://provider.example/v1/messages",
		HasUpstreamHTTPResponse: true,
	}, 1024), "an inbound compatibility body must never be labeled as the exact provider request")
}

func TestBuildTerminalErrorCaptureRecordRejectsSyntheticTransportFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"gpt-5"}`), 1024)

	svc := &OpenAIGatewayService{}
	failure := svc.handleOpenAIUpstreamTransportError(context.Background(), c, &Account{ID: 1, Platform: PlatformOpenAI}, errors.New("dial tcp: connection refused"), false)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, failure, &failoverErr)
	require.False(t, failoverErr.HasUpstreamHTTPResponse)
	require.Nil(t, BuildTerminalErrorCaptureRecord(c, PlatformOpenAI, failoverErr, 1024))
}

func TestBuildTerminalErrorCaptureRecordRejectsErrorEmbeddedInHTTP200Stream(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"claude"}`), 1024)

	failure := &UpstreamFailoverError{
		StatusCode:       http.StatusForbidden,
		ResponseBody:     []byte(`{"type":"error"}`),
		UpstreamEndpoint: req.URL.String(),
		// An HTTP 200 SSE event:error may use an HTTP-like status for failover,
		// but it is not an upstream error-status response.
		HasUpstreamHTTPResponse: false,
	}
	require.Nil(t, BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, failure, 1024))
}

func TestBuildTerminalErrorCaptureRecordKeepsRequestedAndMappedModels(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	SetCaptureRequestedModel(c, "client-model")
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"mapped-provider-model","stream":false}`), 1024)

	rec := BuildTerminalErrorCaptureRecord(c, PlatformOpenAI, &UpstreamFailoverError{
		StatusCode:              http.StatusBadGateway,
		ResponseBody:            []byte(`{"error":"upstream"}`),
		UpstreamEndpoint:        req.URL.String(),
		HasUpstreamHTTPResponse: true,
	}, 1024)
	require.NotNil(t, rec)
	require.Equal(t, "client-model", rec.RequestedModel)
	require.Equal(t, "mapped-provider-model", rec.UpstreamModel)
}

func TestExtractCaptureProviderRequestMetaUsesFinalProviderShapes(t *testing.T) {
	tests := []struct {
		name       string
		platform   string
		body       string
		endpoint   string
		wantModel  string
		wantStream bool
	}{
		{
			name:       "kiro nested runtime envelope",
			platform:   PlatformKiro,
			body:       `{"conversationState":{"currentMessage":{"userInputMessage":{"modelId":"kiro-mapped-model"}}}}`,
			endpoint:   "https://q.example.amazonaws.com/generateAssistantResponse",
			wantModel:  "kiro-mapped-model",
			wantStream: true,
		},
		{
			name:       "gemini model and action in endpoint",
			platform:   PlatformGemini,
			body:       `{"contents":[]}`,
			endpoint:   "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
			wantModel:  "gemini-2.5-pro",
			wantStream: true,
		},
		{
			name:       "antigravity wrapper forces provider stream",
			platform:   PlatformAntigravity,
			body:       `{"model":"claude-sonnet-4-5","request":{"contents":[]}}`,
			endpoint:   "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent",
			wantModel:  "claude-sonnet-4-5",
			wantStream: true,
		},
		{
			name:       "bedrock model and action in endpoint",
			platform:   PlatformAnthropic,
			body:       `{"anthropic_version":"bedrock-2023-05-31"}`,
			endpoint:   "https://bedrock-runtime.example/model/anthropic.claude-3-7-sonnet/invoke-with-response-stream",
			wantModel:  "anthropic.claude-3-7-sonnet",
			wantStream: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, stream, streamKnown := extractCaptureProviderRequestMeta(tt.platform, []byte(tt.body), tt.endpoint)
			require.Equal(t, tt.wantModel, model)
			require.True(t, streamKnown)
			require.Equal(t, tt.wantStream, stream)
		})
	}
}

func TestTerminalCaptureKeepsOutboundMetaBeyondRawRequestLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	setCapturePlatform(c, PlatformAnthropic)
	SetCaptureRequestedModel(c, "client-model")

	body := []byte(`{"padding":"` + strings.Repeat("x", captureHardMaxBodyBytes) + `","model":"mapped-tail-model","stream":true}`)
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", bytes.NewReader(body))
	require.NoError(t, err)
	SetCaptureOutboundRequest(c, req, body, captureHardMaxBodyBytes)
	rec := BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, &UpstreamFailoverError{
		StatusCode:              http.StatusBadGateway,
		ResponseBody:            []byte(`{"error":"provider"}`),
		UpstreamEndpoint:        req.URL.String(),
		HasUpstreamHTTPResponse: true,
	}, captureHardMaxBodyBytes)
	require.NotNil(t, rec)
	require.Len(t, rec.RawRequest, captureHardMaxBodyBytes)
	require.True(t, rec.Truncated)
	require.Equal(t, "mapped-tail-model", rec.UpstreamModel)
	require.True(t, rec.Stream)
}

func TestSetCaptureUpstreamRequestKeepsMetaBeyondRawRequestLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	setCapturePlatform(c, PlatformAnthropic)
	const paddingSize = 16 << 20
	endpoint, err := url.Parse("https://api.anthropic.test/v1/messages")
	require.NoError(t, err)
	req := &http.Request{Method: http.MethodPost, URL: endpoint, Header: make(http.Header), Body: http.NoBody}
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(largeCaptureMetadataBody(paddingSize)), nil }
	hasher := sha256.New()
	_, err = io.Copy(hasher, largeCaptureMetadataBody(paddingSize))
	require.NoError(t, err)
	wantHash := hex.EncodeToString(hasher.Sum(nil))

	setCaptureUpstreamRequest(c, req, captureHardMaxBodyBytes)
	bridge, ok := takeCaptureResult(c)

	require.True(t, ok)
	require.Len(t, bridge.UpstreamRequest, captureHardMaxBodyBytes)
	require.True(t, bridge.RequestTruncated)
	require.Equal(t, wantHash, bridge.UpstreamRequestHash)
	require.Equal(t, "mapped-tail-model", bridge.UpstreamModel)
	require.True(t, bridge.UpstreamStreamKnown)
	require.True(t, bridge.UpstreamStream)
}

func TestCaptureMetadataReaderSkipsHugeUnrelatedStringWithoutBodySizedAllocation(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	model, stream, known, err := extractCaptureProviderRequestMetaFromReader(largeCaptureMetadataBody(16 << 20))
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.NoError(t, err)
	require.Equal(t, "mapped-tail-model", model)
	require.True(t, known)
	require.True(t, stream)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestCaptureMetadataReaderSkipsDenseArrayWithoutPerElementAllocation(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	model, stream, known, err := extractCaptureProviderRequestMetaFromReader(denseCaptureMetadataBody(16 << 20))
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.NoError(t, err)
	require.Equal(t, "mapped-tail-model", model)
	require.True(t, known)
	require.True(t, stream)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestCaptureResponseSignatureScanSkipsDenseArrayWithoutPerElementAllocation(t *testing.T) {
	prefix := []byte(`{"signature":null,"dense":[`)
	suffix := []byte(`0]}`)
	repeat := (captureHardMaxBodyBytes - len(prefix) - len(suffix)) / 2
	body := make([]byte, 0, captureHardMaxBodyBytes)
	body = append(body, prefix...)
	body = append(body, strings.Repeat("0,", repeat)...)
	body = append(body, suffix...)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	cols := extractResponseColumns(body, false)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.False(t, cols.SignaturePresent)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestCaptureResponseColumnsExtractExactHardLimitSSELine(t *testing.T) {
	prefix := []byte(`data: {"response":{"status":"completed","usage":{"input_tokens":3,"output_tokens":2}},"padding":"`)
	suffix := []byte(`"}`)
	body := make([]byte, 0, captureHardMaxBodyBytes)
	body = append(body, prefix...)
	body = append(body, strings.Repeat("x", captureHardMaxBodyBytes-len(prefix)-len(suffix))...)
	body = append(body, suffix...)
	require.Len(t, body, captureHardMaxBodyBytes)

	cols := extractResponseColumnsForPlatform(body, true, PlatformOpenAI)
	require.True(t, cols.stopReasonPresent)
	require.Equal(t, "completed", cols.StopReason)
	require.True(t, cols.inputTokensPresent)
	require.Equal(t, 3, cols.InputTokens)
	require.True(t, cols.outputTokensPresent)
	require.Equal(t, 2, cols.OutputTokens)
}

func TestCaptureResponseColumnsExtractNestedOpenAIUsageAliases(t *testing.T) {
	payload := []byte(`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"prompt_tokens":13,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4,"cache_creation_tokens":3}}}}`)
	cols := extractResponseColumnsForPlatform(payload, true, PlatformOpenAI)

	require.True(t, cols.stopReasonPresent)
	require.Equal(t, "completed", cols.StopReason)
	require.True(t, cols.inputTokensPresent)
	require.Equal(t, 13, cols.InputTokens)
	require.True(t, cols.outputTokensPresent)
	require.Equal(t, 7, cols.OutputTokens)
	require.True(t, cols.cacheReadPresent)
	require.Equal(t, 4, cols.CacheReadTokens)
	require.True(t, cols.cacheCreatePresent)
	require.Equal(t, 3, cols.CacheCreationTokens)
}

func TestCaptureResponseColumnsExtractGeminiOutputIncludingThoughtTokens(t *testing.T) {
	for name, payload := range map[string][]byte{
		"root":   []byte(`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":50}}`),
		"nested": []byte(`{"response":{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":50}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			cols := extractResponseColumnsForPlatform(payload, false, PlatformGemini)
			require.True(t, cols.outputTokensPresent)
			require.Equal(t, 70, cols.OutputTokens)
		})
	}
}

func TestCaptureResponseStopReasonScanSkipsDenseArraysWithoutPerElementAllocation(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		item   string
		suffix string
	}{
		{
			name:   "openai choices",
			prefix: `{"choices":[`,
			item:   `{"finish_reason":"stop"},`,
			suffix: `{"finish_reason":"final"}]}`,
		},
		{
			name:   "gemini candidates",
			prefix: `{"candidates":[`,
			item:   `{"finishReason":"STOP"},`,
			suffix: `{"finishReason":"FINAL"}]}`,
		},
		{
			name:   "nested gemini candidates",
			prefix: `{"response":{"candidates":[`,
			item:   `{"finishReason":"STOP"},`,
			suffix: `{"finishReason":"FINAL"}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repeat := (captureHardMaxBodyBytes - len(tt.prefix) - len(tt.suffix)) / len(tt.item)
			body := []byte(tt.prefix + strings.Repeat(tt.item, repeat) + tt.suffix)

			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			cols := extractResponseColumns(body, false)
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			require.True(t, cols.stopReasonPresent)
			require.Equal(t, "final", strings.ToLower(cols.StopReason))
			// gjson.GetBytes makes at most one body-sized copy for array
			// iteration. The guard is deliberately above that bounded copy but
			// far below the former per-element projection (180+ MiB here).
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
		})
	}
}

func TestBuildTerminalErrorCaptureRecordAllowsEmptyHTTPErrorBody(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"claude"}`), 1024)

	rec := BuildTerminalErrorCaptureRecord(c, PlatformAnthropic, &UpstreamFailoverError{
		StatusCode:              http.StatusBadGateway,
		UpstreamEndpoint:        req.URL.String(),
		HasUpstreamHTTPResponse: true,
	}, 1024)
	require.NotNil(t, rec)
	require.Empty(t, rec.RawResponse)
	require.Equal(t, http.StatusBadGateway, rec.HTTPStatus)
}

func TestBuildTerminalErrorCaptureRecordUsesObservedEmptyProviderResponse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req := httptest.NewRequest(http.MethodPost, "https://api.antigravity.test/v1/messages", nil)
	SetCaptureOutboundRequest(c, req, []byte(`{"model":"claude"}`), 1024)
	setCaptureResult(c, &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Header:     http.Header{"X-Request-Id": []string{"provider-empty"}},
		Request:    req,
	}, []byte{}, false)

	rec := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, &UpstreamFailoverError{
		StatusCode:              http.StatusInternalServerError,
		ResponseHeaders:         http.Header{"X-Request-Id": []string{"mapped-client-status"}},
		UpstreamEndpoint:        req.URL.String(),
		HasUpstreamHTTPResponse: true,
	}, 1024)
	require.NotNil(t, rec)
	require.Equal(t, http.StatusUnprocessableEntity, rec.HTTPStatus)
	require.Equal(t, "provider-empty", rec.RequestID)
	require.Empty(t, rec.RawResponse)
}

func TestSnapshotBytesCopiesInput(t *testing.T) {
	src := []byte(`{"a":1}`)
	got := snapshotBytes(src)
	if string(got) != string(src) {
		t.Fatalf("copy mismatch: %q", got)
	}
	src[0] = 'X' // 篡改源
	if got[0] == 'X' {
		t.Fatal("snapshot must be an independent copy")
	}
	if snapshotBytes(nil) != nil {
		t.Fatal("nil in -> nil out")
	}
}

func TestCaptureRequestSnapshotsUseEightMiBHardCeiling(t *testing.T) {
	const hardLimit = 8 << 20
	body := bytes.Repeat([]byte("x"), hardLimit+1024)

	direct, truncated := captureWithLimit(body, hardLimit*2)
	require.Len(t, direct, hardLimit)
	require.True(t, truncated)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "https://api.example/v1/messages", bytes.NewReader(body))
	setCaptureUpstreamRequest(c, req, hardLimit*2)
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, bridge.UpstreamRequest, hardLimit)
	require.True(t, bridge.RequestTruncated)
}

func TestKiroTranslatedStreamCloseJoinsTranslator(t *testing.T) {
	raw := newDelayedTranslatorSource()
	pipeReader, pipeWriter := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(pipeWriter, raw)
		_ = pipeWriter.Close()
	}()
	select {
	case <-raw.readStarted:
	case <-time.After(time.Second):
		t.Fatal("translator did not start reading")
	}

	body := &kiroTranslatedStreamBody{PipeReader: pipeReader, raw: raw, done: done}
	require.NoError(t, body.Close())
	returnedBeforeTranslator := false
	select {
	case <-done:
	default:
		returnedBeforeTranslator = true
		<-done
	}
	require.False(t, returnedBeforeTranslator, "Close returned while the translator could still publish stale capture state")
}

func TestKiroTranslatedStreamClosePublishesAfterRawReaderExit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	SetCaptureOutboundRequest(c, c.Request, []byte(`{"model":"kiro"}`), 1024)

	raw := &closeReleasedCaptureReader{closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: c.Request}
	_ = beginCaptureResponse(c, resp, true, 1024)
	pipeReader, pipeWriter := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(pipeWriter, resp.Body)
		_ = resp.Body.Close()
		_ = pipeWriter.Close()
	}()

	body := &kiroTranslatedStreamBody{PipeReader: pipeReader, raw: resp.Body, done: done}
	require.NoError(t, body.Close())
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("tail-before-scanner-exit\n"), bridge.Response)
}

func TestCloseCaptureResponseJoinsScannerBeforePublishing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	SetCaptureOutboundRequest(c, c.Request, []byte(`{"model":"test"}`), 1024)

	raw := &closeReleasedCaptureReader{closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: c.Request}
	_ = beginCaptureResponse(c, resp, true, 1024)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		_, _ = io.ReadAll(resp.Body)
	}()

	closeCaptureResponseAndJoinScanner(resp, scanDone)
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("tail-before-scanner-exit\n"), bridge.Response)
	require.NoError(t, resp.Body.Close(), "the public close remains idempotent after scanner ownership releases it")
}

func TestGrokNestedTranslatorsJoinBeforePublishingRawCapture(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
	account := &Account{Platform: PlatformGrok}
	// The runtime policy helper above enables OpenAI; use the provider platform
	// that the real Grok route supplies as well.
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.Grok = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	req := httptest.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, req, []byte(`{"model":"grok"}`)))

	raw := &closeReleasedCaptureReader{closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: req}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
	resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, defaultMaxLineSize)
	resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, apicompat.ResponsesClientToolMapping{
		CustomTools: map[string]bool{"apply_patch": true},
	}, defaultMaxLineSize)

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		_, _ = io.ReadAll(resp.Body)
	}()
	closeCaptureResponseAndJoinScanner(resp, scanDone)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("tail-before-scanner-exit\n"), bridge.Response)
}

func TestExtractSessionIDFromMetadataUserID(t *testing.T) {
	body := []byte(`{"model":"claude","metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"11111111-1111-1111-1111-111111111111\"}"}}`)
	if got := extractCaptureSessionID(body); got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("session_id from metadata.user_id, got %q", got)
	}
	body2 := []byte(`{"conversation_id":"conv-42"}`)
	if got := extractCaptureSessionID(body2); got != "conv-42" {
		t.Fatalf("fallback session hint, got %q", got)
	}
}

func TestExtractResponseColumnsNonStream(t *testing.T) {
	resp := []byte(`{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2},"content":[{"type":"thinking","signature":"sig"}]}`)
	cols := extractResponseColumns(resp, false)
	if cols.StopReason != "end_turn" || cols.InputTokens != 10 || cols.OutputTokens != 5 || cols.CacheReadTokens != 2 {
		t.Fatalf("bad cols: %+v", cols)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature must be detected")
	}
}

func TestNonStreamingCaptureRespectsFlag(t *testing.T) {
	body := []byte(`{"stop_reason":"end_turn"}`)
	if got := captureResponseIfEnabled(false, body, 1024); got != nil {
		t.Fatal("must be nil when disabled")
	}
	got := captureResponseIfEnabled(true, body, 1024)
	if string(got) != string(body) {
		t.Fatalf("copy mismatch: %q", got)
	}
	body[0] = 'X'
	if got[0] == 'X' {
		t.Fatal("must be independent copy")
	}
}

func TestCaptureEndpointRedactsURLCredentialsAndSensitiveQuery(t *testing.T) {
	raw := "https://user:password@provider.example/v1/models/model:stream?key=api-secret&access_token=token-secret&proxy=http%3A%2F%2Fproxy-user%3Aproxy-pass%40proxy.example%3A8080&region=us-east-1#private"
	redacted := redactCaptureEndpoint(raw)

	require.Contains(t, redacted, "provider.example/v1/models/model:stream")
	require.Contains(t, redacted, "region=us-east-1")
	require.NotContains(t, redacted, "user")
	require.NotContains(t, redacted, "password")
	require.NotContains(t, redacted, "api-secret")
	require.NotContains(t, redacted, "token-secret")
	require.NotContains(t, redacted, "proxy-user")
	require.NotContains(t, redacted, "proxy-pass")
	require.NotContains(t, redacted, "private")
	require.Contains(t, redacted, "key=%5BREDACTED%5D")
	require.Contains(t, redacted, "proxy=%5BREDACTED%5D")
}

func TestCaptureEndpointRedactsCommonCompoundCredentialKeys(t *testing.T) {
	raw := "https://provider.example/v1?x-api-key=one&api_token=two&refresh_token=three&id_token=four&x-auth-token=five&client-signature=six&serviceCredential=seven&webhookSecret=eight&hmacSignature=nine&bearerToken=ten&authentication=eleven&code=twelve&authorization_code=thirteen&sig=fourteen&sas=fifteen&request-id=keep-request&status_code=keep-status&monkey=keep-monkey"
	redacted := redactCaptureEndpoint(raw)
	for _, secret := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen"} {
		require.NotContains(t, redacted, secret)
	}
	require.Contains(t, redacted, "request-id=keep-request")
	require.Contains(t, redacted, "status_code=keep-status")
	require.Contains(t, redacted, "monkey=keep-monkey")
}

func TestCaptureEndpointMalformedURLStillDropsUserInfoAndQuery(t *testing.T) {
	redacted := redactCaptureEndpoint("https://user:password@provider.example/%zz?x-api-key=secret#fragment")
	require.Equal(t, "https://provider.example/%zz", redacted)
	require.NotContains(t, redacted, "user")
	require.NotContains(t, redacted, "password")
	require.NotContains(t, redacted, "secret")
}

func TestCaptureHeadersRedactCustomRelayCredentials(t *testing.T) {
	raw := redactHTTPHeader(http.Header{
		"X-Relay-Key":                  {"relay-secret"},
		"X-Auth-Token":                 {"auth-secret"},
		"X-Custom-Secret":              {"custom-secret"},
		"X-ApiKey":                     {"api-key-secret"},
		"ApiToken":                     {"api-token-secret"},
		"AccessToken":                  {"access-token-secret"},
		"ClientSecret":                 {"client-secret"},
		"ClientSignature":              {"client-signature-secret"},
		"X-CredentialSignature":        {"credential-signature-secret"},
		"X-ServiceCredential":          {"service-credential-secret"},
		"X-WebhookSecret":              {"webhook-secret"},
		"X-HmacSignature":              {"hmac-signature-secret"},
		"X-BearerToken":                {"bearer-token-secret"},
		"X-Authentication":             {"authentication-secret"},
		"Authentication-Info":          {"authentication-info-secret"},
		"X-Request-Id-Secret":          {"mixed-request-secret"},
		"X-RateLimit-AuthToken":        {"mixed-ratelimit-token"},
		"Anthropic-Version-Credential": {"mixed-version-credential"},
		"X-Request-Id":                 {"request-kept"},
		"X-Ratelimit-Limit":            {"100"},
		"Anthropic-Version":            {"2023-06-01"},
	})
	text := string(raw)
	for _, secret := range []string{
		"relay-secret", "auth-secret", "custom-secret", "api-key-secret", "api-token-secret",
		"access-token-secret", "client-secret", "client-signature-secret", "credential-signature-secret",
		"service-credential-secret", "webhook-secret", "hmac-signature-secret", "bearer-token-secret",
		"authentication-secret", "authentication-info-secret",
		"mixed-request-secret", "mixed-ratelimit-token", "mixed-version-credential",
	} {
		require.NotContains(t, text, secret)
	}
	require.Contains(t, text, "request-kept")
	require.Contains(t, text, "100")
	require.Contains(t, text, "2023-06-01")
}

func TestCaptureBridgeAlwaysStoresRedactedEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	enableCaptureForTest(t, c)

	req := httptest.NewRequest(http.MethodPost, "https://u:p@provider.example/v1/messages?api_key=secret&diagnostic=kept", strings.NewReader(`{}`))
	SetCaptureOutboundRequest(c, req, []byte(`{}`), 1024)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}
	setCaptureResult(c, resp, nil, false)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, "https://provider.example/v1/messages?api_key=%5BREDACTED%5D&diagnostic=kept", bridge.UpstreamEndpoint)
}

func TestAttachCaptureResultUsesFinalProviderRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		header string
		value  string
		openAI bool
	}{
		{name: "gemini", header: "X-Goog-Request-Id", value: "goog-final"},
		{name: "grok", header: "Xai-Request-Id", value: "xai-final", openAI: true},
		{name: "kiro", header: "X-Amzn-Requestid", value: "aws-final"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			enableCaptureForTest(t, c)
			setCapturePlatform(c, PlatformAnthropic)
			requestBody := []byte(`{"model":"provider-final","stream":true}`)
			req := httptest.NewRequest(http.MethodPost, "https://provider.example/v1/messages", bytes.NewReader(requestBody))
			SetCaptureOutboundRequest(c, req, requestBody, 1024)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{tc.header: []string{tc.value}}, Body: http.NoBody, Request: req}
			setCaptureResult(c, resp, []byte(`{}`), false)

			if tc.openAI {
				result := attachCaptureToOpenAIForwardResult(c, &OpenAIForwardResult{RequestID: "stale", UpstreamModel: "business-initial", Stream: false})
				require.Equal(t, tc.value, result.RequestID)
				require.Equal(t, "provider-final", result.UpstreamModelForCapture())
				require.True(t, result.StreamForCapture())
			} else {
				result := attachCaptureToForwardResult(c, &ForwardResult{RequestID: "stale", UpstreamModel: "business-initial", Stream: false})
				require.Equal(t, tc.value, result.RequestID)
				require.Equal(t, "provider-final", result.UpstreamModelForCapture())
				require.True(t, result.StreamForCapture())
			}
		})
	}
}

func TestCaptureTruncation(t *testing.T) {
	got, truncated := captureWithLimit([]byte("0123456789"), 4)
	if string(got) != "0123" || !truncated {
		t.Fatalf("got %q truncated=%v", got, truncated)
	}
	got2, truncated2 := captureWithLimit([]byte("ab"), 4)
	if string(got2) != "ab" || truncated2 {
		t.Fatalf("got %q truncated=%v", got2, truncated2)
	}
	if got3, tr := captureWithLimit(nil, 4); got3 != nil || tr {
		t.Fatal("nil in -> nil, false")
	}
	if got4, tr := captureWithLimit([]byte("x"), 0); got4 != nil || tr {
		t.Fatal("limit<=0 -> nil, false")
	}
}

func TestSSETeeAppendsRawLinesWithFraming(t *testing.T) {
	acc := newSSETee(1024)
	acc.appendLine("event: message_start")
	acc.appendLine(`data: {"type":"message_start"}`)
	acc.appendLine("")
	out, truncated := acc.bytes()
	want := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	if string(out) != want || truncated {
		t.Fatalf("got %q truncated=%v", out, truncated)
	}
}

func TestSSETeeTruncates(t *testing.T) {
	acc := newSSETee(5)
	acc.appendLine("0123456789")
	out, truncated := acc.bytes()
	if len(out) > 5 || !truncated {
		t.Fatalf("expected truncation, got %q trunc=%v", out, truncated)
	}
}

func TestSSETeeNilAndDisabled(t *testing.T) {
	var acc *sseTee
	acc.appendLine("x") // no panic
	if b, tr := acc.bytes(); b != nil || tr {
		t.Fatal("nil tee -> nil,false")
	}
}

func TestSSETeeConcurrentAppendAndRead(t *testing.T) {
	acc := newSSETee(1 << 20)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			acc.appendLine("data: {}")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_, _ = acc.bytes() // must be race-free under -race
	}
	<-done
}

func TestRedactHeadersStripsCredentials(t *testing.T) {
	h := map[string][]string{
		"Authorization":     {"Bearer secret"},
		"X-Api-Key":         {"sk-xxx"},
		"Cookie":            {"a=b"},
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"tools-2024"},
		"X-Request-Id":      {"req-1"},
	}
	out := redactHeadersJSON(h)
	s := string(out)
	for _, secret := range []string{"secret", "sk-xxx", "a=b"} {
		if strings.Contains(s, secret) {
			t.Fatalf("credential leaked: %q in %s", secret, s)
		}
	}
	for _, keep := range []string{"2023-06-01", "tools-2024", "req-1"} {
		if !strings.Contains(s, keep) {
			t.Fatalf("must keep %q; got %s", keep, s)
		}
	}
	if redactHeadersJSON(nil) != nil {
		t.Fatal("nil headers -> nil")
	}
}

func TestExtractResponseColumnsStreamSSE(t *testing.T) {
	sse := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":50}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"s\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n")
	cols := extractResponseColumns(sse, true)
	if cols.StopReason != "tool_use" || cols.InputTokens != 7 || cols.OutputTokens != 3 {
		t.Fatalf("bad stream cols: %+v", cols)
	}
	if cols.CacheReadTokens != 100 || cols.CacheCreationTokens != 50 {
		t.Fatalf("cache tokens must come from message.usage, got read=%d creation=%d", cols.CacheReadTokens, cols.CacheCreationTokens)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature_delta must set SignaturePresent")
	}
}

func TestExtractResponseColumnsProviderNativeFormats(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		stream bool
		want   responseColumns
	}{
		{
			name: "openai chat completions json",
			body: `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":21,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":5}}}`,
			want: responseColumns{StopReason: "stop", InputTokens: 21, OutputTokens: 8, CacheReadTokens: 5},
		},
		{
			name:   "openai responses sse",
			body:   "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":31,\"output_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":7}}}}\n\n",
			stream: true,
			want:   responseColumns{StopReason: "completed", InputTokens: 31, OutputTokens: 9, CacheReadTokens: 7},
		},
		{
			name: "gemini json",
			body: `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"thoughtSignature":"sig"}]}}],"usageMetadata":{"promptTokenCount":41,"candidatesTokenCount":10,"cachedContentTokenCount":6}}`,
			want: responseColumns{StopReason: "STOP", InputTokens: 41, OutputTokens: 10, CacheReadTokens: 6, SignaturePresent: true},
		},
		{
			name:   "antigravity wrapped sse for non-stream client",
			body:   "data: {\"response\":{\"candidates\":[{\"finishReason\":\"MAX_TOKENS\"}],\"usageMetadata\":{\"promptTokenCount\":51,\"candidatesTokenCount\":11,\"cachedContentTokenCount\":4}}}\n\n",
			stream: false,
			want:   responseColumns{StopReason: "MAX_TOKENS", InputTokens: 51, OutputTokens: 11, CacheReadTokens: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractResponseColumns([]byte(tt.body), tt.stream)
			require.Equal(t, tt.want.StopReason, got.StopReason)
			require.Equal(t, tt.want.InputTokens, got.InputTokens)
			require.Equal(t, tt.want.OutputTokens, got.OutputTokens)
			require.Equal(t, tt.want.CacheReadTokens, got.CacheReadTokens)
			require.Equal(t, tt.want.SignaturePresent, got.SignaturePresent)
		})
	}
}

func TestExtractCaptureColumnsPreservesTrustedPrefilledUsageWhenRawFormatHasNoUsage(t *testing.T) {
	rec := &CaptureRecord{
		RawResponse:         []byte(`{"provider":"opaque"}`),
		StopReason:          "prefilled",
		InputTokens:         61,
		OutputTokens:        12,
		CacheReadTokens:     3,
		CacheCreationTokens: 2,
		SignaturePresent:    true,
	}
	extractCaptureColumns(rec)
	require.Equal(t, "prefilled", rec.StopReason)
	require.Equal(t, 61, rec.InputTokens)
	require.Equal(t, 12, rec.OutputTokens)
	require.Equal(t, 3, rec.CacheReadTokens)
	require.Equal(t, 2, rec.CacheCreationTokens)
	require.True(t, rec.SignaturePresent)
}

func TestExtractCaptureColumnsIgnoresMalformedUsageAndStopFields(t *testing.T) {
	rec := &CaptureRecord{
		Stream:       true,
		StopReason:   "prefilled",
		InputTokens:  5,
		OutputTokens: 7,
		RawResponse: []byte(strings.Join([]string{
			`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":5,"output_tokens":7}}}`,
			``,
			`data: {"type":"response.completed","response":{"status":123,"usage":{"input_tokens":-1,"output_tokens":"bad"}}}`,
			``,
		}, "\n")),
	}
	extractCaptureColumns(rec)
	require.Equal(t, "completed", rec.StopReason)
	require.Equal(t, 5, rec.InputTokens)
	require.Equal(t, 7, rec.OutputTokens)

	rawError := &CaptureRecord{StopReason: "terminal_error", RawResponse: []byte(`{"stop_reason":123,"usage":{"input_tokens":-1,"output_tokens":4294967296}}`)}
	extractCaptureColumns(rawError)
	require.Equal(t, "terminal_error", rawError.StopReason)
	require.Zero(t, rawError.InputTokens)
	require.Zero(t, rawError.OutputTokens)
	require.Zero(t, captureUInt32(-1))
	require.Zero(t, captureUInt32(int(^uint32(0))+1))
}

func TestExtractCaptureColumnsIgnoresMalformedSignatures(t *testing.T) {
	rec := &CaptureRecord{Stream: true, RawResponse: []byte(strings.Join([]string{
		`data: {"signature":null}`,
		``,
		`data: {"content":[{"thoughtSignature":123},{"signature":{}}]}`,
		``,
		`data: {"delta":{"type":"signature_delta","signature":[]}}`,
		``,
	}, "\n"))}
	extractCaptureColumns(rec)
	require.False(t, rec.SignaturePresent)

	var kiroRaw bytes.Buffer
	_, _ = kiroRaw.Write(buildCaptureKiroFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "ok", "signature": nil, "thoughtSignature": 123},
	}))
	_, _ = kiroRaw.Write(buildCaptureKiroFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))
	kiroRec := &CaptureRecord{Platform: PlatformKiro, Stream: true, RawResponse: kiroRaw.Bytes()}
	extractCaptureColumns(kiroRec)
	require.False(t, kiroRec.SignaturePresent)
}

func TestExtractCaptureColumnsKiroProviderNativeEventStream(t *testing.T) {
	var raw bytes.Buffer
	_, _ = raw.Write(buildCaptureKiroFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "data: example"},
	}))
	_, _ = raw.Write(buildCaptureKiroFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 71, "outputTokens": 13,
			"cacheReadInputTokens": 8, "cacheWriteInputTokens": 4,
		}},
	}))
	_, _ = raw.Write(buildCaptureKiroFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))
	rec := &CaptureRecord{Platform: PlatformKiro, Stream: true, UpstreamModel: "claude-sonnet-4-6", RawResponse: raw.Bytes()}
	extractCaptureColumns(rec)
	require.Equal(t, "end_turn", rec.StopReason)
	require.Equal(t, 71, rec.InputTokens)
	require.Equal(t, 13, rec.OutputTokens)
	require.Equal(t, 8, rec.CacheReadTokens)
	require.Equal(t, 4, rec.CacheCreationTokens)
}

func TestExtractCaptureColumnsKiroProviderNativeUsagePresenceAndRange(t *testing.T) {
	buildResponse := func(tokenUsage map[string]any) []byte {
		t.Helper()
		var raw bytes.Buffer
		if tokenUsage != nil {
			_, _ = raw.Write(buildCaptureKiroFrame(t, "messageMetadataEvent", map[string]any{
				"messageMetadataEvent": map[string]any{"tokenUsage": tokenUsage},
			}))
		}
		_, _ = raw.Write(buildCaptureKiroFrame(t, "messageStopEvent", map[string]any{
			"messageStopEvent": map[string]any{"stopReason": "end_turn"},
		}))
		return raw.Bytes()
	}

	const beyondUInt32 = uint64(^uint32(0)) + 1
	tests := []struct {
		name       string
		tokenUsage map[string]any
		wantInput  int
		wantOutput int
		wantRead   int
		wantCreate int
	}{
		{
			name:       "stop only preserves trusted prefill",
			wantInput:  61,
			wantOutput: 12,
			wantRead:   3,
			wantCreate: 2,
		},
		{
			name: "explicit zero overwrites trusted prefill",
			tokenUsage: map[string]any{
				"uncachedInputTokens":   0,
				"outputTokens":          0,
				"cacheReadInputTokens":  0,
				"cacheWriteInputTokens": 0,
			},
		},
		{
			name: "values beyond archive UInt32 range preserve trusted prefill",
			tokenUsage: map[string]any{
				"uncachedInputTokens":   beyondUInt32,
				"outputTokens":          beyondUInt32,
				"cacheReadInputTokens":  beyondUInt32,
				"cacheWriteInputTokens": beyondUInt32,
			},
			wantInput:  61,
			wantOutput: 12,
			wantRead:   3,
			wantCreate: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &CaptureRecord{
				Platform:            PlatformKiro,
				Stream:              true,
				RawResponse:         buildResponse(tt.tokenUsage),
				InputTokens:         61,
				OutputTokens:        12,
				CacheReadTokens:     3,
				CacheCreationTokens: 2,
			}
			extractCaptureColumns(rec)
			require.Equal(t, "end_turn", rec.StopReason)
			require.Equal(t, tt.wantInput, rec.InputTokens)
			require.Equal(t, tt.wantOutput, rec.OutputTokens)
			require.Equal(t, tt.wantRead, rec.CacheReadTokens)
			require.Equal(t, tt.wantCreate, rec.CacheCreationTokens)
		})
	}
}

func TestExtractCaptureColumnsProviderNativeRequestMetadata(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		wantID     string
		wantEffort string
		wantType   string
	}{
		{
			name:    "antigravity nested metadata user id",
			request: `{"request":{"sessionId":"{\"device_id\":\"ag-device\",\"session_id\":\"ag-session\"}"}}`,
			wantID:  "ag-session",
		},
		{
			name:    "gemini nested metadata user id",
			request: `{"request":{"metadata":{"user_id":"{\"device_id\":\"gemini-device\",\"session_id\":\"gemini-session\"}"}}}`,
			wantID:  "gemini-session",
		},
		{
			name:       "kiro nested thinking",
			request:    `{"conversationState":{"conversationId":"kiro-session"},"additionalModelRequestFields":{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}}`,
			wantID:     "kiro-session",
			wantEffort: "high",
			wantType:   "adaptive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &CaptureRecord{RawRequest: []byte(tt.request)}
			extractCaptureColumns(rec)
			require.Equal(t, tt.wantID, rec.SessionID)
			require.Equal(t, tt.wantEffort, rec.ThinkingEffort)
			require.Equal(t, tt.wantType, rec.ThinkingType)
		})
	}
}

func buildCaptureKiroFrame(t *testing.T, eventType string, payload map[string]any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	headerName := []byte(":event-type")
	headerValue := []byte(eventType)
	headersLen := 1 + len(headerName) + 1 + 2 + len(headerValue)
	totalLen := 12 + headersLen + len(payloadBytes) + 4
	frame := make([]byte, totalLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(frame[4:8], uint32(headersLen))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	offset := 12
	frame[offset] = byte(len(headerName))
	offset++
	copy(frame[offset:], headerName)
	offset += len(headerName)
	frame[offset] = 7
	offset++
	binary.BigEndian.PutUint16(frame[offset:offset+2], uint16(len(headerValue)))
	offset += 2
	copy(frame[offset:], headerValue)
	offset += len(headerValue)
	copy(frame[offset:], payloadBytes)
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
	return frame
}

func TestSnapshotForCaptureWithFlag(t *testing.T) {
	b, tr := SnapshotForCaptureWithFlag([]byte("abc"), 8)
	if string(b) != "abc" || tr {
		t.Fatalf("got %q trunc=%v, want abc false", b, tr)
	}
	b2, tr2 := SnapshotForCaptureWithFlag([]byte("0123456789"), 4)
	if string(b2) != "0123" || !tr2 {
		t.Fatalf("got %q trunc=%v, want 0123 true", b2, tr2)
	}
	if b3, tr3 := SnapshotForCaptureWithFlag(nil, 4); b3 != nil || tr3 {
		t.Fatalf("nil in -> nil,false; got %q %v", b3, tr3)
	}
}

func TestCaptureRequestID(t *testing.T) {
	if got := CaptureRequestID("req_real"); got != "req_real" {
		t.Fatalf("passthrough failed: %q", got)
	}
	if got := CaptureRequestID("   "); len(got) < 8 {
		t.Fatalf("empty upstream should fallback to uuid, got %q", got)
	}
	if a, b := CaptureRequestID(""), CaptureRequestID(""); a == b {
		t.Fatalf("two fallbacks must differ: %q == %q", a, b)
	}
}

func TestExtractCaptureColumnsKeepsPrefilledEffort(t *testing.T) {
	// raw_request 无 output_config（模拟 Bedrock 剥离），但已预填 effort
	rec := &CaptureRecord{
		RawRequest:     []byte(`{"model":"claude","messages":[]}`),
		ThinkingEffort: "high",
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "high" {
		t.Fatalf("prefilled effort overwritten: %q", rec.ThinkingEffort)
	}
}

func TestExtractCaptureColumnsFallsBackToRawEffort(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest: []byte(`{"output_config":{"effort":"xhigh"}}`),
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "xhigh" {
		t.Fatalf("raw fallback failed: %q", rec.ThinkingEffort)
	}
}

func TestCaptureAwareErrorReadDoesNotChangeFunctionalBodyLimit(t *testing.T) {
	const fallbackLimit = int64(512 << 10)
	providerBody := bytes.Repeat([]byte("z"), 768<<10)

	readWithoutCapture := func() []byte {
		resp := &http.Response{Body: io.NopCloser(bytes.NewReader(providerBody))}
		got, err := readCaptureAwareUpstreamErrorBody(resp, fallbackLimit)
		require.NoError(t, err)
		return got
	}
	readWithCapture := func() ([]byte, *captureResultBridge) {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		enableCaptureForTest(t, c)
		beginCaptureAttempt(c)
		resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewReader(providerBody))}
		finish := beginCaptureResponse(c, resp, true, 1<<20)
		got, err := readCaptureAwareUpstreamErrorBody(resp, fallbackLimit)
		require.NoError(t, err)
		finish()
		bridge, ok := takeCaptureResult(c)
		require.True(t, ok)
		return got, bridge
	}

	withoutCapture := readWithoutCapture()
	withCapture, bridge := readWithCapture()
	require.Equal(t, withoutCapture, withCapture)
	require.Len(t, withCapture, int(fallbackLimit))
	require.Equal(t, providerBody, bridge.Response, "capture may retain bytes beyond the stable functional limit")
	require.False(t, bridge.Truncated)
}

func TestGeminiNonStreamingCaptureProbesPastHardLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	setCapturePlatform(c, PlatformGemini)
	SetCaptureOutboundRequest(c, c.Request, []byte(`{"model":"gemini-test"}`), captureHardMaxBodyBytes)

	providerBody := bytes.Repeat([]byte("g"), captureHardMaxBodyBytes+1)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
		Request:    c.Request,
	}
	finish := beginCaptureResponse(c, resp, true, captureHardMaxBodyBytes)
	_, err := (&GeminiMessagesCompatService{}).handleNonStreamingResponse(c, resp, "gemini-test")
	finish()
	require.Error(t, err, "the intentionally truncated synthetic JSON is not a valid provider response")

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, bridge.Response, captureHardMaxBodyBytes)
	require.True(t, bridge.ResponseTruncated)
	require.True(t, bridge.Truncated)
}

// TestKiroCaptureRecordExtraction 覆盖历史兼容记录中“Anthropic 请求 +
// 翻译后 Anthropic SSE”的 worker 抽取回退。当前生产 Kiro 路径归档的是
// 原生 AWS EventStream，由上方 provider-native 用例覆盖。
func TestKiroCaptureRecordExtraction(t *testing.T) {
	rawReq := []byte(`{"model":"CLAUDE_SONNET_4_20250514_V1_0",` +
		`"metadata":{"user_id":"{\"device_id\":\"d1\",\"account_uuid\":\"a1\",\"session_id\":\"sess-kiro-9\"}"}}`)
	rawSSE := []byte("event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":18452,\"cache_read_input_tokens\":16384}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"EqwDCkY\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1203}}\n\n")
	rec := &CaptureRecord{
		Platform:       "kiro",
		Stream:         true,
		HTTPStatus:     200,
		RawRequest:     rawReq,
		RawResponse:    rawSSE,
		ThinkingEffort: "high", // 由 submit 侧从 ParsedRequest.OutputEffort 预填
	}
	extractCaptureColumns(rec)

	if rec.SessionID != "sess-kiro-9" {
		t.Fatalf("session_id from metadata.user_id: got %q", rec.SessionID)
	}
	if rec.ThinkingEffort != "high" {
		t.Fatalf("prefilled effort must survive: %q", rec.ThinkingEffort)
	}
	if rec.StopReason != "end_turn" {
		t.Fatalf("stop_reason: got %q", rec.StopReason)
	}
	if rec.InputTokens != 18452 || rec.OutputTokens != 1203 || rec.CacheReadTokens != 16384 {
		t.Fatalf("usage cols: in=%d out=%d cacheRead=%d", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens)
	}
	if !rec.SignaturePresent {
		t.Fatal("signature_delta in translated SSE must set SignaturePresent (PDF >65% coverage)")
	}
}

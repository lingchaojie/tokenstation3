//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAITypedCaptureTestHarness struct {
	records   chan *CaptureRecord
	terminals chan string
	pool      *ConversationCapturePool
}

func newOpenAITypedCaptureTestHarness(t *testing.T) *openAITypedCaptureTestHarness {
	t.Helper()
	harness := &openAITypedCaptureTestHarness{
		records:   make(chan *CaptureRecord, 1),
		terminals: make(chan string, 1),
	}
	harness.pool = NewConversationCapturePoolWithTerminalEventsForUnitTest(harness.records, harness.terminals)
	t.Cleanup(harness.pool.Stop)
	return harness
}

func (h *openAITypedCaptureTestHarness) commit(
	t *testing.T,
	c *gin.Context,
	result *OpenAIForwardResult,
	wantRequest []byte,
	wantResponse []byte,
	wantTruncated bool,
) *CaptureRecord {
	t.Helper()
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result), "handler-owned terminal must commit the typed attempt")
	require.Equal(t, "commit", h.requireTerminal(t))
	record := h.requireRecord(t)
	require.Equal(t, wantRequest, record.RawRequest)
	require.Equal(t, wantResponse, record.RawResponse)
	require.Equal(t, wantTruncated, record.Truncated)
	h.requireNoExtraTerminal(t)
	return record
}

func (h *openAITypedCaptureTestHarness) abort(t *testing.T, c *gin.Context) {
	t.Helper()
	AbortCaptureAttempt(c)
	require.Equal(t, "abort", h.requireTerminal(t))
	select {
	case record := <-h.records:
		t.Fatalf("aborted typed attempt unexpectedly published a record: %+v", record)
	default:
	}
	h.requireNoExtraTerminal(t)
}

func (h *openAITypedCaptureTestHarness) requireTerminal(t *testing.T) string {
	t.Helper()
	select {
	case terminal := <-h.terminals:
		return terminal
	default:
		t.Fatal("typed attempt did not publish a terminal event synchronously")
		return ""
	}
}

func (h *openAITypedCaptureTestHarness) requireRecord(t *testing.T) *CaptureRecord {
	t.Helper()
	select {
	case record := <-h.records:
		return record
	default:
		t.Fatal("committed typed attempt did not publish a record synchronously")
		return nil
	}
}

func (h *openAITypedCaptureTestHarness) requireNoExtraTerminal(t *testing.T) {
	t.Helper()
	select {
	case terminal := <-h.terminals:
		t.Fatalf("typed attempt published duplicate terminal %q", terminal)
	default:
	}
}

func TestOpenAICompatBoundedTerminalErrorMarksTypedCaptureIncomplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := newOpenAITypedCaptureTestHarness(t)
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	svc := &OpenAIGatewayService{cfg: cfg, capturePool: harness.pool}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	enableCaptureForTest(t, c)
	account := &Account{ID: 9751, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	wireRequest := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`)
	upstreamRequest := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/chat/completions", strings.NewReader(string(wireRequest)))
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, upstreamRequest, wireRequest))

	providerBody := strings.Repeat("x", int(openAIUpstreamErrorBodyReadLimit)+1)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"bounded-error"}},
		Body:       io.NopCloser(strings.NewReader(providerBody)),
		Request:    upstreamRequest,
	}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
	result, err := svc.handleCompatErrorResponse(resp, c, account, func(*gin.Context, int, string, string) {}, "gpt-5.4")
	require.Error(t, err)
	require.NotNil(t, result)
	harness.commit(t, c, result, wireRequest, []byte(providerBody[:openAIUpstreamErrorBodyReadLimit]), true)
}

func TestOpenAITypedCapturePreservesSuccessfulUpstreamHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := newOpenAITypedCaptureTestHarness(t)
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	svc := &OpenAIGatewayService{cfg: cfg, capturePool: harness.pool}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	enableCaptureForTest(t, c)
	account := &Account{ID: 9752, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	wireRequest := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`)
	upstreamRequest := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/chat/completions", strings.NewReader(string(wireRequest)))
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, upstreamRequest, wireRequest))

	providerBody := []byte(`{"id":"chatcmpl-status","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"created"}},
		Body:       io.NopCloser(strings.NewReader(string(providerBody))),
		Request:    upstreamRequest,
	}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
	readBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, providerBody, readBody)

	record := harness.commit(t, c, &OpenAIForwardResult{}, wireRequest, providerBody, false)
	require.Equal(t, http.StatusCreated, record.HTTPStatus)
}

func TestOpenAICompatFailoverPreservesIncompleteTypedErrorResponse(t *testing.T) {
	tests := []struct {
		name         string
		body         io.ReadCloser
		wantResponse []byte
	}{
		{
			name:         "bounded_prefix",
			body:         io.NopCloser(strings.NewReader(strings.Repeat("x", int(openAIUpstreamErrorBodyReadLimit)+1))),
			wantResponse: []byte(strings.Repeat("x", int(openAIUpstreamErrorBodyReadLimit))),
		},
		{
			name:         "short_io_error",
			body:         &errTailReader{data: []byte(`{"error":{"message":"short"}}`), err: io.ErrUnexpectedEOF},
			wantResponse: []byte(`{"error":{"message":"short"}}`),
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			harness := newOpenAITypedCaptureTestHarness(t)
			cfg := &config.Config{}
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			svc := &OpenAIGatewayService{cfg: cfg, capturePool: harness.pool}

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
			enableCaptureForTest(t, c)
			account := &Account{ID: int64(9760 + index), Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			wireRequest := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`)
			upstreamRequest := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/chat/completions", strings.NewReader(string(wireRequest)))
			require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, upstreamRequest, wireRequest))

			resp := &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {test.name}},
				Body:       test.body,
				Request:    upstreamRequest,
			}
			svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
			respBody, upstreamMsg := svc.readOpenAIUpstreamError(resp)
			failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, resp, respBody, upstreamMsg, "gpt-5.4")
			require.NotNil(t, failure)
			require.True(t, failure.CaptureResponseIncomplete, "bounded or failed reads must remain incomplete through failover")
			require.True(t, CommitTerminalErrorCaptureAttemptWithCompleteness(c, PlatformOpenAI, failure.HTTPStatusForCapture(), !failure.CaptureResponseIncomplete))
			require.Equal(t, "commit", harness.requireTerminal(t))
			record := harness.requireRecord(t)
			require.Equal(t, test.wantResponse, record.RawResponse)
			require.True(t, record.Truncated)
			harness.requireNoExtraTerminal(t)
		})
	}
}

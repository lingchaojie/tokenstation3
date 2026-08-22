//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type orderedOpenAIDeliveryFailureWriter struct {
	gin.ResponseWriter
	failureSignal       chan struct{}
	failSemantic        bool
	mu                  sync.Mutex
	failed              bool
	postFailureAttempts int
	postFailureBytes    int
}

func (w *orderedOpenAIDeliveryFailureWriter) Write(p []byte) (int, error) {
	payload := string(p)
	w.mu.Lock()
	if w.failed {
		w.postFailureAttempts++
		w.postFailureBytes += len(p)
		w.mu.Unlock()
		return 0, errors.New("ordered downstream write after delivery failure")
	}
	shouldFail := payload == ":\n\n" || strings.HasPrefix(payload, "event: ping\n")
	if w.failSemantic {
		shouldFail = strings.Contains(payload, "visible before idle")
	}
	if shouldFail {
		w.failed = true
		close(w.failureSignal)
		w.mu.Unlock()
		return 0, errors.New("ordered downstream delivery failure")
	}
	w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

func (w *orderedOpenAIDeliveryFailureWriter) postFailureWrites() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.postFailureAttempts, w.postFailureBytes
}

type orderedOpenAIProviderTail struct {
	prefix        []byte
	tail          []byte
	failureSignal <-chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once
	step          int
}

func (r *orderedOpenAIProviderTail) Read(p []byte) (int, error) {
	switch r.step {
	case 0:
		r.step++
		return copy(p, r.prefix), nil
	case 1:
		select {
		case <-r.failureSignal:
			r.step++
			return copy(p, r.tail), nil
		case <-r.closed:
			return 0, io.EOF
		}
	default:
		return 0, io.EOF
	}
}

func (r *orderedOpenAIProviderTail) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

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

func TestOpenAIConvertedDeliveryFailureDefersTypedCaptureToLateProviderTruth(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		requestBody      []byte
		forward          func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
		lateProviderTail string
		wantProviderErr  bool
		failSemantic     bool
	}{
		{
			name:        "chat_late_official_terminal_commits_disconnect",
			path:        "/v1/chat/completions",
			requestBody: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\ndata: [DONE]\n\n",
		},
		{
			name:        "chat_semantic_failure_late_official_terminal_commits_disconnect",
			path:        "/v1/chat/completions",
			requestBody: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\ndata: [DONE]\n\n",
			failSemantic:     true,
		},
		{
			name:        "messages_late_official_terminal_commits_disconnect",
			path:        "/v1/messages",
			requestBody: []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\ndata: [DONE]\n\n",
		},
		{
			name:        "messages_semantic_failure_late_official_terminal_commits_disconnect",
			path:        "/v1/messages",
			requestBody: []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\ndata: [DONE]\n\n",
			failSemantic:     true,
		},
		{
			name:        "chat_late_provider_error_aborts_terminal_disabled",
			path:        "/v1/chat/completions",
			requestBody: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"late ordered provider failure\"}}}\n\n",
			wantProviderErr:  true,
		},
		{
			name:        "chat_semantic_failure_late_provider_error_aborts_terminal_disabled",
			path:        "/v1/chat/completions",
			requestBody: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"late ordered provider failure\"}}}\n\n",
			wantProviderErr:  true,
			failSemantic:     true,
		},
		{
			name:        "messages_late_provider_error_aborts_terminal_disabled",
			path:        "/v1/messages",
			requestBody: []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"late ordered provider failure\"}}}\n\n",
			wantProviderErr:  true,
		},
		{
			name:        "messages_semantic_failure_late_provider_error_aborts_terminal_disabled",
			path:        "/v1/messages",
			requestBody: []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-5.4")
			},
			lateProviderTail: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_ordered\",\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"late ordered provider failure\"}}}\n\n",
			wantProviderErr:  true,
			failSemantic:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			harness := newOpenAITypedCaptureTestHarness(t)
			policy := DefaultCaptureRuntimePolicy()
			policy.Enabled = true
			policy.Platforms.OpenAI = true
			policy.Outcomes.Success = false
			policy.Outcomes.TerminalError = false

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			require.NoError(t, InstallCaptureRuntimePolicyForUnitTest(c, policy, 9, nil))
			failureSignal := make(chan struct{})
			failureWriter := &orderedOpenAIDeliveryFailureWriter{
				ResponseWriter: c.Writer, failureSignal: failureSignal, failSemantic: test.failSemantic,
			}
			c.Writer = failureWriter

			providerPrefix := []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ordered\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\",\"output\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible before idle\"}\n\n")
			providerTail := []byte(test.lateProviderTail)
			upstreamBody := &orderedOpenAIProviderTail{
				prefix: providerPrefix, tail: providerTail, failureSignal: failureSignal, closed: make(chan struct{}),
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"ordered-keepalive"}},
				Body:       upstreamBody,
			}}
			cfg := &config.Config{Gateway: config.GatewayConfig{
				StreamKeepaliveInterval: 1,
				Capture: config.GatewayCaptureConfig{
					Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
				},
			}}
			svc := &OpenAIGatewayService{cfg: cfg, capturePool: harness.pool, httpUpstream: upstream}
			account := &Account{
				ID: 9770, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "account-id"},
			}

			started := time.Now()
			result, err := test.forward(svc, context.Background(), c, account, test.requestBody)
			require.Less(t, time.Since(started), 4*time.Second, "ordered drain must stay bounded")
			require.NotNil(t, result)
			require.True(t, result.ClientDisconnect)
			postFailureAttempts, postFailureBytes := failureWriter.postFailureWrites()
			require.Zero(t, postFailureAttempts, "no client write may be attempted after the first typed delivery failure")
			require.Zero(t, postFailureBytes, "no client bytes may be attempted after the first typed delivery failure")
			if test.wantProviderErr {
				require.ErrorContains(t, err, "late ordered provider failure")
				require.True(t, result.CaptureTerminalError, "late provider failure must outrank disconnect")
				require.False(t, result.CaptureResponseComplete)
				require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
				require.Equal(t, "abort", harness.requireTerminal(t))
				select {
				case record := <-harness.records:
					t.Fatalf("terminal-disabled late provider error unexpectedly committed: %+v", record)
				default:
				}
				harness.requireNoExtraTerminal(t)
				return
			}

			require.NoError(t, err)
			require.False(t, result.CaptureTerminalError, "typed delivery failure must remain disconnect-causal")
			require.True(t, result.CaptureResponseComplete)
			record := harness.commit(t, c, result, upstream.lastBody, append(append([]byte(nil), providerPrefix...), providerTail...), false)
			require.Contains(t, string(record.RawResponse), `"type":"response.completed"`)
		})
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

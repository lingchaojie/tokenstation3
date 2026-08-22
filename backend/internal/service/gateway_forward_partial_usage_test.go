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

type gatewayForwardErrorPolicyRepoStub struct {
	AccountRepository
	tempCalls           int
	modelRateLimitCalls []gatewayForwardModelRateLimitCall
}

type gatewayForwardModelRateLimitCall struct {
	accountID int64
	scope     string
}

type cancelOnSemanticWriteCloser struct {
	gin.ResponseWriter
	cancel context.CancelFunc
	once   sync.Once
}

func (w *cancelOnSemanticWriteCloser) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"text":"client canceled"`)) {
		w.once.Do(w.cancel)
	}
	return w.ResponseWriter.Write(p)
}

type blockingCaptureBody struct {
	prefix []byte
	sent   bool
	closed chan struct{}
	once   sync.Once
}

func newBlockingCaptureBody(prefix []byte) *blockingCaptureBody {
	return &blockingCaptureBody{prefix: prefix, closed: make(chan struct{})}
}

func (b *blockingCaptureBody) Read(p []byte) (int, error) {
	if !b.sent && len(b.prefix) > 0 {
		b.sent = true
		return copy(p, b.prefix), nil
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingCaptureBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func installDisconnectOnlyCapturePolicy(t *testing.T, c *gin.Context) {
	t.Helper()
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.Anthropic = true
	policy.Platforms.Kiro = true
	policy.Platforms.Antigravity = true
	policy.Platforms.OpenAI = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = false
	policy.ModelAllowlists.Anthropic = []string{}
	policy.ModelAllowlists.Kiro = []string{}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
}

func TestGatewayForward_ClientCausalCancellationCommitsDisconnectCapture(t *testing.T) {
	for _, family := range []struct {
		name    string
		account func() *Account
	}{
		{name: "shared_anthropic", account: newAnthropicOAuthAccountForPartialUsageTest},
		{name: "apikey_passthrough", account: newAnthropicAPIKeyAccountForTest},
	} {
		for _, stage := range []struct {
			name   string
			prefix string
		}{
			{name: "before_semantic_output"},
			{name: "after_semantic_output", prefix: anthropicAPIKeyPassthroughTestSemanticPrefix(4, "client canceled")},
		} {
			t.Run(family.name+"/"+stage.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformAnthropic)
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody)).WithContext(ctx)
				installDisconnectOnlyCapturePolicy(t, c)
				if stage.prefix == "" {
					cancel()
				} else {
					c.Writer = &cancelOnSemanticWriteCloser{ResponseWriter: c.Writer, cancel: cancel}
				}
				body := newBlockingCaptureBody([]byte(stage.prefix))
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"client-cancel"}},
					Body:       body,
				}}
				svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)

				result, forwardErr := svc.Forward(ctx, c, family.account(), parsed)

				require.ErrorIs(t, forwardErr, context.Canceled)
				require.NotNil(t, result, "the public forward boundary must return an archivable disconnect result")
				require.True(t, result.ClientDisconnect)
				require.False(t, result.CaptureTerminalError)
				require.False(t, result.CaptureResponseComplete)
				require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
				attempts := transport.Attempts()
				require.Len(t, attempts, 1)
				if stage.prefix == "" {
					require.Empty(t, attempts[0].ResponseBytes())
				} else {
					require.Equal(t, []byte(stage.prefix), attempts[0].ResponseBytes())
				}
				require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
				finals := attempts[0].Finals()
				require.Len(t, finals, 1)
				require.False(t, finals[0].ResponseComplete)
				require.Empty(t, finals[0].StopReason)
			})
		}
	}
}

func TestGatewayForward_ProviderStreamFailuresRemainTerminal(t *testing.T) {
	providerErrors := []struct {
		name string
		err  error
	}{
		{name: "provider_canceled", err: context.Canceled},
		{name: "provider_deadline", err: context.DeadlineExceeded},
		{name: "provider_read_error", err: io.ErrUnexpectedEOF},
	}
	families := []struct {
		name    string
		account func() *Account
	}{
		{name: "shared_anthropic", account: newAnthropicOAuthAccountForPartialUsageTest},
		{name: "apikey_passthrough", account: newAnthropicAPIKeyAccountForTest},
	}
	for _, family := range families {
		for _, provider := range providerErrors {
			t.Run(family.name+"/"+provider.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformAnthropic)
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
				installDisconnectOnlyCapturePolicy(t, c)
				prefix := []byte(anthropicAPIKeyPassthroughTestSemanticPrefix(4, "provider failure"))
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"provider-failure"}},
					Body:       &streamReadCloser{payload: prefix, err: provider.err},
				}}
				svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)

				result, forwardErr := svc.Forward(context.Background(), c, family.account(), parsed)

				require.ErrorIs(t, forwardErr, provider.err)
				require.NotNil(t, result)
				require.False(t, result.ClientDisconnect)
				require.True(t, result.CaptureTerminalError)
				require.False(t, result.CaptureResponseComplete)
				require.False(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
				attempts := transport.Attempts()
				require.Len(t, attempts, 1)
				require.Equal(t, prefix, attempts[0].ResponseBytes())
				require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
				require.Empty(t, attempts[0].Finals())
			})
		}
	}
}

func TestGatewayForward_PreSemanticClientCancellationWithoutLiveAttemptPreservesFailover(t *testing.T) {
	families := []struct {
		name    string
		account func() *Account
	}{
		{name: "shared_anthropic", account: newAnthropicOAuthAccountForPartialUsageTest},
		{name: "apikey_passthrough", account: newAnthropicAPIKeyAccountForTest},
	}
	admissions := []struct {
		name string
		set  func(*GatewayService)
	}{
		{name: "pool_nil", set: func(svc *GatewayService) { svc.capturePool = nil }},
		{name: "capture_disabled", set: func(svc *GatewayService) { svc.cfg.Gateway.Capture.Enabled = false }},
		{name: "runtime_disabled", set: func(svc *GatewayService) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{}, func() bool { return false })
		}},
		{name: "ipc_begin_failed", set: func(svc *GatewayService) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{beginErr: errors.New("capture IPC unavailable")}, func() bool { return true })
		}},
	}
	for _, family := range families {
		for _, admission := range admissions {
			t.Run(family.name+"/"+admission.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformAnthropic)
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody)).WithContext(ctx)
				installDisconnectOnlyCapturePolicy(t, c)
				body := newBlockingCaptureBody(nil)
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       body,
				}}
				svc := newForwardPartialUsageServiceForTest(upstream)
				admission.set(svc)
				if svc.capturePool != nil {
					t.Cleanup(svc.capturePool.Stop)
				}

				result, forwardErr := svc.Forward(ctx, c, family.account(), parsed)

				require.Nil(t, result, "capture unavailability must not create a capture-only proxy result")
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, forwardErr, &failoverErr)
				require.False(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
			})
		}
	}
}

func TestGatewayForward_PostSemanticClientCancellationWithoutLiveAttemptRemainsPartial(t *testing.T) {
	for _, family := range []struct {
		name    string
		account func() *Account
	}{
		{name: "shared_anthropic", account: newAnthropicOAuthAccountForPartialUsageTest},
		{name: "apikey_passthrough", account: newAnthropicAPIKeyAccountForTest},
	} {
		t.Run(family.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformAnthropic)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody)).WithContext(ctx)
			installDisconnectOnlyCapturePolicy(t, c)
			c.Writer = &cancelOnSemanticWriteCloser{ResponseWriter: c.Writer, cancel: cancel}
			prefix := []byte(anthropicAPIKeyPassthroughTestSemanticPrefix(4, "client canceled"))
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       newBlockingCaptureBody(prefix),
			}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			svc.capturePool = nil

			result, forwardErr := svc.Forward(ctx, c, family.account(), parsed)

			require.ErrorIs(t, forwardErr, context.Canceled)
			require.NotNil(t, result, "semantic output remains a non-failover partial result even without capture")
			require.True(t, result.ClientDisconnect)
			require.False(t, result.CaptureTerminalError)
			require.False(t, result.CaptureResponseComplete)
			require.False(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
		})
	}
}

func (r *gatewayForwardErrorPolicyRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *gatewayForwardErrorPolicyRepoStub) SetModelRateLimit(_ context.Context, id int64, scope string, _ time.Time, _ ...string) error {
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, gatewayForwardModelRateLimitCall{
		accountID: id,
		scope:     scope,
	})
	return nil
}

// 本文件覆盖 issue #5148：流式转发中途出错（缺失 terminal 事件、读错误等）时，
// 已观测到的上游 usage 不得随错误一起被丢弃，Forward 必须把部分结果与错误一同
// 返回，供 handler 照常提交 usage 记录。

func newForwardPartialUsageServiceForTest(upstream *anthropicHTTPUpstreamRecorder) *GatewayService {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
			Capture: config.GatewayCaptureConfig{
				Enabled:        true,
				MaxBodyBytes:   64 * 1024,
				MaxHeaderBytes: 1 << 20,
			},
		},
	}
	return &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
}

func newForwardPartialUsageCaptureServiceForTest(upstream *anthropicHTTPUpstreamRecorder) (*GatewayService, *recordingCaptureTransport) {
	transport := &recordingCaptureTransport{}
	svc := newForwardPartialUsageServiceForTest(upstream)
	svc.capturePool = newConversationCapturePoolForTransport(transport, func() bool { return true })
	return svc, transport
}

func newAnthropicOAuthAccountForPartialUsageTest() *Account {
	return &Account{
		ID:          501,
		Name:        "anthropic-oauth-partial-usage",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestGatewayService_Forward_StreamMissingTerminalWithUsageSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	// newapi 等聚合上游的典型失败形态：message_start/message_delta 携带 usage，
	// 但流在 message_stop 前直接结束。
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet-latest","content":[],"usage":{"input_tokens":11,"cache_read_input_tokens":7}}}`,
		"",
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		"",
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
		"",
	}, "\n") + "\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-partial"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result, "上游已返回 usage 时，缺少 terminal 事件不应阻断转发或计费")
	require.True(t, result.Stream)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.CacheReadInputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "rid-partial", result.RequestID)
	require.NotNil(t, result.FirstTokenMs)
	require.Nil(t, result.CaptureResponse)
	require.False(t, result.CaptureTruncated)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(upstreamSSE), attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates(), "the handler-side partial-result sink owns commit")
	AbortCaptureAttempt(c)
}

func TestGatewayService_Forward_SemanticOutputWithoutUsagePreservesPartialAndCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstreamSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_semantic\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamSSE))}}
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)

	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	require.ErrorContains(t, err, "upstream response missing billable usage")
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.False(t, result.CaptureResponseComplete, "clean EOF and missing usage must not synthesize provider completion")
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Nil(t, result.CaptureResponse)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(upstreamSSE), attempts[0].ResponseBytes())
	require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
	require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
	require.Len(t, attempts[0].Finals(), 1)
	require.False(t, attempts[0].Finals()[0].ResponseComplete, "the public sink must preserve streaming clean-EOF incompleteness")
	require.Contains(t, rec.Body.String(), `"text":"hello"`)
}

func TestGatewayService_Forward_NonStreamingMissingUsageCommitsVerifiedFullBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	providerBody := []byte(`{"id":"msg_no_usage","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
	}}
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)

	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "successful full-body read must survive missing-usage terminalization")
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
	require.Len(t, attempts[0].Finals(), 1)
	require.True(t, attempts[0].Finals()[0].ResponseComplete, "the public sink must receive verified non-stream completion")
	require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
}

func TestGatewayService_Forward_PreambleUsageOnlyMissingTerminalStillBills(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstreamSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_preamble\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamSSE))}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.False(t, result.UpstreamFailed)
	require.Empty(t, rec.Body.String(), "metadata-only preamble remains staged when the stream ends before semantic output")
}

func TestGatewayService_Forward_SSEErrorUsesSemanticCommitBoundary(t *testing.T) {
	for _, tt := range []struct {
		name      string
		upstream  string
		committed bool
	}{
		{
			name: "preamble only",
			upstream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_error_pre\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"boom\"}}\n\n",
		},
		{
			name: "semantic text with zero usage",
			upstream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_error_post\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"visible\"}}\n\n" +
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"boom\"}}\n\n",
			committed: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			enableCaptureForTest(t, c)
			body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.upstream)),
			}}

			svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
			result, err := svc.Forward(
				context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed,
			)
			require.Error(t, err)
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
			require.Equal(t, []byte(tt.upstream), attempts[0].ResponseBytes())
			require.Empty(t, attempts[0].TerminalStates())
			defer AbortCaptureAttempt(c)
			var failoverErr *UpstreamFailoverError
			if !tt.committed {
				require.Nil(t, result)
				require.ErrorAs(t, err, &failoverErr)
				require.Greater(t, c.Writer.Size(), -1, "latest upstream commits protocol preamble before event:error")
				require.Contains(t, rec.Body.String(), "message_start")
				require.False(t, failoverErr.SafeToFailoverAfterWrite, "committed preamble must not authorize replay after write")
				return
			}

			require.NotNil(t, result, "semantic output must retain its result on an SSE error event")
			require.False(t, errors.As(err, &failoverErr), "committed output must not be replayed")
			var streamErr *sseStreamErrorEventError
			require.ErrorAs(t, err, &streamErr)
			require.Zero(t, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			require.Nil(t, result.CaptureResponse)
			require.Contains(t, rec.Body.String(), `"text":"visible"`)
		})
	}
}

func TestGatewayService_Forward_StreamReadErrorAfterOutputPreservesPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	// message_start 后已有真实文本输出（含 usage），随后上游连接异常中断。
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			payload: []byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_read_error\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"cache_creation_input_tokens\":4}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"visible\"}}\n\n"),
			err: io.ErrUnexpectedEOF,
		},
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream read error")
	require.NotNil(t, result, "已写出内容后的读错误必须保留部分 usage")
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
}

func TestGatewayService_Forward_StreamWithoutUsageReturnsBillingErrorResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	// 只有 ping、没有任何 usage 的流中断：不应产生零 usage 的幽灵账单记录。
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: ping\ndata: {\"type\": \"ping\"}\n\n")),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result, "不可计费响应需要保留错误请求/capture 元数据")
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.Equal(t, -1, c.Writer.Size(), "纯前导帧不得污染可重试响应")
}

func TestGatewayService_Forward_FailoverErrorKeepsNilResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	// 未向客户端写出任何字节前的读错误会包成 UpstreamFailoverError 走换号重试。
	// 该路径必须保持 result=nil：failover 成功后按成功请求计费，双份结果会重复计费。
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			err: errors.New("connection reset by peer"),
		},
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Nil(t, result, "failover 错误必须保持 result=nil，防止重试成功后双重计费")
}

func TestGatewayService_Forward_PreOutputSSEOverloadedErrorUsesSemantic529(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	const errorJSON = `{"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_01"}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: error\ndata: " + errorJSON + "\n\n")),
	}}
	repo := &gatewayForwardErrorPolicyRepoStub{}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     NewRateLimitService(repo, nil, cfg, nil, nil),
		deferredService:      &DeferredService{},
	}
	account := newAnthropicOAuthAccountForPartialUsageTest()
	account.Credentials["temp_unschedulable_enabled"] = true
	account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
		"error_code":       float64(529),
		"keywords":         []any{"Overloaded"},
		"duration_minutes": float64(10),
	}}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Nil(t, result)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, 529, failoverErr.StatusCode)
	require.JSONEq(t, errorJSON, string(failoverErr.ResponseBody))
	require.Len(t, repo.modelRateLimitCalls, 1, "synthetic 529 must participate in temp-unschedulable rules")
	require.Equal(t, account.ID, repo.modelRateLimitCalls[0].accountID)
	require.Equal(t, parsed.Model, repo.modelRateLimitCalls[0].scope)
	require.Empty(t, rec.Body.String(), "pre-output overload must remain eligible for account failover")
}

func TestGatewayService_Forward_PostOutputSSEOverloadedErrorKeepsExistingStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	const errorJSON = `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	fixture := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: error\ndata: " + errorJSON + "\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(fixture)),
	}}
	repo := &gatewayForwardErrorPolicyRepoStub{}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     NewRateLimitService(repo, nil, cfg, nil, nil),
		deferredService:      &DeferredService{},
	}

	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	require.Error(t, err)
	require.Nil(t, result)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.JSONEq(t, errorJSON, string(failoverErr.ResponseBody))
	require.Zero(t, repo.tempCalls)
	require.Contains(t, rec.Body.String(), "message_start")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamMissingTerminalWithUsageSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "claude-3-7-sonnet-20250219",
		Stream: true,
	}

	upstreamSSE := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_partial","type":"message","role":"assistant","content":[],"usage":{"input_tokens":9,"cache_read_input_tokens":2}}}`,
		"",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		"",
	}, "\n") + "\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-pass-partial"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	account := newAnthropicAPIKeyAccountForTest()

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result, "透传流缺少 terminal 但已观测到 usage 时应正常计费")
	require.True(t, result.Stream)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, "claude-3-7-sonnet-20250219", result.Model)
	require.Equal(t, upstreamSSE, rec.Body.String(), "committed passthrough bytes must remain unchanged")
	require.Nil(t, result.CaptureResponse)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(upstreamSSE), attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardPreambleOnlyMissingTerminalStillBills(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	requestBody := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(requestBody), Model: "claude-3-7-sonnet-20250219", Stream: true}
	upstreamSSE := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_preamble","type":"message","role":"assistant","content":[],"usage":{"input_tokens":9}}}`,
		"",
		`event: ping`,
		`data: {"type":"ping"}`,
		"",
	}, "\n") + "\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}

	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.False(t, result.UpstreamFailed)
	require.Empty(t, recorder.Body.String(), "metadata-only preamble remains staged when the stream ends before semantic output")
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(upstreamSSE), attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates(), "the final-account handler sink owns terminal classification")
	AbortCaptureAttempt(c)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardPostSemanticZeroUsageErrorPreservesPartialAndCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	requestBody := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(requestBody), Model: "claude-3-7-sonnet-20250219", Stream: true}
	upstreamSSE := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_zero","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0}}}`,
		"",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`,
		"",
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"boom"}}`,
		"",
	}, "\n") + "\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}

	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed)
	require.Error(t, err)
	var streamErr *sseStreamErrorEventError
	require.ErrorAs(t, err, &streamErr)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, upstreamSSE, recorder.Body.String(), "committed raw Anthropic SSE, including event:error, must pass through unchanged")
	require.Nil(t, result.CaptureResponse)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(upstreamSSE), attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestGatewayService_Forward_PreSemanticReadErrorReturnsTerminalCaptureOnlyResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(requestBody), Model: "claude-3-5-sonnet-latest", Stream: true}
	providerPrefix := []byte(": provider-preamble\n\n")
	forcedErr := errors.New("forced provider read failure before semantic output")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"presemantic-read-error"}},
		Body:       &streamReadCloser{payload: providerPrefix, err: forcedErr},
	}}

	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)

	require.Nil(t, result, "pre-semantic read failures remain eligible for account failover")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, providerPrefix, attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates(), "the final-account handler sink owns terminal classification")
	AbortCaptureAttempt(c)
	require.Empty(t, recorder.Body.String())
}

func TestGatewayCompatibility_PreSemanticAndBufferedReadErrorsUseTypedAttemptAtWireBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	forcedErr := errors.New("forced compatibility provider read failure")
	tests := []struct {
		name      string
		responses bool
		stream    bool
		body      []byte
	}{
		{name: "chat_stream_presemantic", stream: true, body: []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)},
		{name: "responses_stream_presemantic", responses: true, stream: true, body: []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"input":"hello"}`)},
		{name: "chat_buffered", body: []byte(`{"model":"claude-3-5-sonnet-latest","stream":false,"messages":[{"role":"user","content":"hello"}]}`)},
		{name: "responses_buffered", responses: true, body: []byte(`{"model":"claude-3-5-sonnet-latest","stream":false,"input":"hello"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/compat", bytes.NewReader(tt.body))
			enableCaptureForTest(t, c)
			providerPrefix := []byte(": provider-preamble\n\n")
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"compat-read-error"}},
				Body:       &streamReadCloser{payload: providerPrefix, err: forcedErr},
			}}
			svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
			account := newAnthropicOAuthAccountForPartialUsageTest()
			parsed := &ParsedRequest{Body: NewRequestBodyRef(tt.body), Model: "claude-3-5-sonnet-latest", Stream: tt.stream}

			var result *ForwardResult
			var err error
			if tt.responses {
				result, err = svc.ForwardAsResponses(context.Background(), c, account, tt.body, parsed)
			} else {
				result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, tt.body, parsed)
			}

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Nil(t, result, "pre-semantic failures must not submit or bill an intermediate account")
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, upstream.lastBody, attempts[0].RequestBytes(), "capture must use the final transformed wire request")
			require.Equal(t, providerPrefix, attempts[0].ResponseBytes(), "capture must contain only bytes naturally returned to the parser")
			require.Empty(t, attempts[0].TerminalStates(), "the final-account handler sink owns terminal classification")
			_, legacy := takeCaptureResult(c)
			require.False(t, legacy, "typed paths must not publish the retired whole-body bridge")
			AbortCaptureAttempt(c)
		})
	}
}

func TestGatewayForwardAsResponses_MissingUsageIsProviderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":false,"input":"hello"}`)
	providerBody := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_no_usage","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-latest","stop_reason":null,"usage":{}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"responses-no-usage"}},
		Body:       io.NopCloser(strings.NewReader(providerBody)),
	}}
	enableCaptureForTest(t, c)
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-3-5-sonnet-latest", Stream: false}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "native message_stop must prove the stream-to-buffer response complete")
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
	require.Len(t, attempts[0].Finals(), 1)
	require.True(t, attempts[0].Finals()[0].ResponseComplete)
	marked, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "upstream_usage_missing", marked.Code)
}

func TestGatewayForwardAsChatCompletions_MissingUsageCommitsOfficialTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	providerBody := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_no_usage","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-latest","stop_reason":null,"usage":{}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"chat-no-usage"}},
		Body:       io.NopCloser(strings.NewReader(providerBody)),
	}}
	svc, transport := newForwardPartialUsageCaptureServiceForTest(upstream)
	account := newAnthropicOAuthAccountForPartialUsageTest()
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-3-5-sonnet-latest", Stream: false}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "native message_stop must prove the Chat stream-to-buffer response complete")
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, result))
	require.Len(t, attempts[0].Finals(), 1)
	require.True(t, attempts[0].Finals()[0].ResponseComplete)
}

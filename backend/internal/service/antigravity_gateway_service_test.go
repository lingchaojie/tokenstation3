package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// antigravityFailingWriter 模拟客户端断开连接的 gin.ResponseWriter
type antigravityFailingWriter struct {
	gin.ResponseWriter
	failAfter int // 允许成功写入的次数，之后所有写入返回错误
	writes    int
}

type antigravityClientCancelBody struct {
	ctx    context.Context
	cancel context.CancelFunc
	prefix []byte
	sent   bool
}

func (b *antigravityClientCancelBody) Read(p []byte) (int, error) {
	if !b.sent && len(b.prefix) > 0 {
		b.sent = true
		return copy(p, b.prefix), nil
	}
	b.cancel()
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *antigravityClientCancelBody) Close() error { return nil }

func TestAntigravityForwardGemini_ClientCausalCancellationCommitsDisconnectCapture(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix []byte
	}{
		{name: "before_semantic_output"},
		{name: "after_semantic_output", prefix: []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"client canceled\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4}}}\n\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(requestBody)).WithContext(ctx)
			installDisconnectOnlyCapturePolicy(t, c)
			if len(test.prefix) > 0 {
				c.Writer = &cancelOnSemanticWriteCloser{ResponseWriter: c.Writer, cancel: cancel}
			}

			providerBody := &antigravityClientCancelBody{ctx: ctx, cancel: cancel, prefix: test.prefix}
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"antigravity-client-cancel"}},
				Body:       providerBody,
			}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{
				Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
			}}}
			transport := &recordingCaptureTransport{}
			svc := &AntigravityGatewayService{
				settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
				tokenProvider:  &AntigravityTokenProvider{},
				httpUpstream:   upstream,
				capturePool:    newConversationCapturePoolForTransport(transport, func() bool { return true }),
			}
			t.Cleanup(svc.capturePool.Stop)
			account := &Account{
				ID: 107, Name: "native-gemini-cancel", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"access_token": "token", "project_id": "project-107", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
			}

			result, forwardErr := svc.ForwardGemini(ctx, c, account, "gemini-2.5-flash", "streamGenerateContent", true, requestBody, false)

			require.ErrorIs(t, forwardErr, context.Canceled)
			require.NotNil(t, result)
			require.True(t, result.ClientDisconnect)
			require.False(t, result.UpstreamFailed)
			require.False(t, result.CaptureTerminalError)
			require.False(t, result.CaptureResponseComplete)
			require.True(t, CommitForwardCaptureAttempt(c, PlatformAntigravity, result))
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			if len(test.prefix) == 0 {
				require.Empty(t, attempts[0].ResponseBytes())
			} else {
				require.Equal(t, test.prefix, attempts[0].ResponseBytes())
			}
			require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
			finals := attempts[0].Finals()
			require.Len(t, finals, 1)
			require.False(t, finals[0].ResponseComplete)
			require.Empty(t, finals[0].StopReason)
		})
	}
}

func TestAntigravityForwardGemini_ProviderStreamFailuresRemainTerminal(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "provider_canceled", err: context.Canceled},
		{name: "provider_deadline", err: context.DeadlineExceeded},
		{name: "provider_read_error", err: io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			providerPrefix := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"provider failure\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4}}}\n\n")
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(requestBody))
			installDisconnectOnlyCapturePolicy(t, c)
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"antigravity-provider-failure"}},
				Body:       &streamReadCloser{payload: providerPrefix, err: test.err},
			}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{
				Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
			}}}
			transport := &recordingCaptureTransport{}
			svc := &AntigravityGatewayService{
				settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
				tokenProvider:  &AntigravityTokenProvider{},
				httpUpstream:   upstream,
				capturePool:    newConversationCapturePoolForTransport(transport, func() bool { return true }),
			}
			t.Cleanup(svc.capturePool.Stop)
			account := &Account{
				ID: 108, Name: "native-gemini-provider-error", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"access_token": "token", "project_id": "project-108", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
			}

			result, forwardErr := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, requestBody, false)

			require.ErrorIs(t, forwardErr, test.err)
			require.NotNil(t, result)
			require.False(t, result.ClientDisconnect)
			require.True(t, result.CaptureTerminalError)
			require.False(t, result.CaptureResponseComplete)
			require.False(t, CommitForwardCaptureAttempt(c, PlatformAntigravity, result))
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, providerPrefix, attempts[0].ResponseBytes())
			require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
			require.Empty(t, attempts[0].Finals())
		})
	}
}

func TestAntigravityForwardGemini_PreSemanticCancellationWithoutLiveAttemptPreservesProxyFailure(t *testing.T) {
	for _, admission := range []struct {
		name string
		set  func(*AntigravityGatewayService, *config.Config)
	}{
		{name: "pool_nil", set: func(svc *AntigravityGatewayService, _ *config.Config) { svc.capturePool = nil }},
		{name: "capture_disabled", set: func(_ *AntigravityGatewayService, cfg *config.Config) { cfg.Gateway.Capture.Enabled = false }},
		{name: "runtime_disabled", set: func(svc *AntigravityGatewayService, _ *config.Config) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{}, func() bool { return false })
		}},
		{name: "ipc_begin_failed", set: func(svc *AntigravityGatewayService, _ *config.Config) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{beginErr: errors.New("capture IPC unavailable")}, func() bool { return true })
		}},
		{name: "request_headers_write_failed", set: func(svc *AntigravityGatewayService, _ *config.Config) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{failWriteAt: 1}, func() bool { return true })
		}},
		{name: "request_body_write_failed", set: func(svc *AntigravityGatewayService, _ *config.Config) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{failWriteAt: 2}, func() bool { return true })
		}},
		{name: "response_headers_write_failed", set: func(svc *AntigravityGatewayService, _ *config.Config) {
			svc.capturePool = newConversationCapturePoolForTransport(&recordingCaptureTransport{failWriteAt: 3}, func() bool { return true })
		}},
	} {
		t.Run(admission.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(requestBody)).WithContext(ctx)
			installDisconnectOnlyCapturePolicy(t, c)
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       &antigravityClientCancelBody{ctx: ctx, cancel: cancel},
			}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{
				Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
			}}}
			svc := &AntigravityGatewayService{
				settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
				tokenProvider:  &AntigravityTokenProvider{},
				httpUpstream:   upstream,
				capturePool:    newConversationCapturePoolForTransport(&recordingCaptureTransport{}, func() bool { return true }),
			}
			initialPool := svc.capturePool
			admission.set(svc, cfg)
			if initialPool != nil && initialPool != svc.capturePool {
				initialPool.Stop()
			}
			if svc.capturePool != nil {
				t.Cleanup(svc.capturePool.Stop)
			}
			account := &Account{
				ID: 109, Name: "native-gemini-no-attempt", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"access_token": "token", "project_id": "project-109", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
			}

			result, forwardErr := svc.ForwardGemini(ctx, c, account, "gemini-2.5-flash", "streamGenerateContent", true, requestBody, false)

			require.ErrorIs(t, forwardErr, context.Canceled)
			require.NotNil(t, result)
			require.False(t, result.ClientDisconnect, "without a live attempt presemantic proxy behavior must remain the existing failure path")
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			require.False(t, CommitForwardCaptureAttempt(c, PlatformAntigravity, result))
		})
	}
}

func TestAntigravityForwardGemini_PostSemanticCancellationWithoutLiveAttemptRemainsPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(requestBody)).WithContext(ctx)
	installDisconnectOnlyCapturePolicy(t, c)
	c.Writer = &cancelOnSemanticWriteCloser{ResponseWriter: c.Writer, cancel: cancel}
	prefix := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"client canceled\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4}}}\n\n")
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       &antigravityClientCancelBody{ctx: ctx, cancel: cancel, prefix: prefix},
	}}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{
		Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
	}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
		capturePool:    newConversationCapturePoolForTransport(&recordingCaptureTransport{failWriteAt: 1}, func() bool { return true }),
	}
	t.Cleanup(svc.capturePool.Stop)
	account := &Account{
		ID: 110, Name: "native-gemini-postsemantic-no-attempt", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "project-110", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
	}

	result, forwardErr := svc.ForwardGemini(ctx, c, account, "gemini-2.5-flash", "streamGenerateContent", true, requestBody, false)

	require.ErrorIs(t, forwardErr, context.Canceled)
	require.NotNil(t, result, "semantic output remains a non-failover partial result even without capture")
	require.True(t, result.ClientDisconnect)
	require.False(t, result.UpstreamFailed)
	require.False(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
	require.False(t, CommitForwardCaptureAttempt(c, PlatformAntigravity, result))
}

func (w *antigravityFailingWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed: client disconnected")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

// newAntigravityTestService 创建用于流式测试的 AntigravityGatewayService
func newAntigravityTestService(cfg *config.Config) *AntigravityGatewayService {
	return &AntigravityGatewayService{
		settingService: &SettingService{cfg: cfg},
	}
}

func TestAntigravityUpstreamErrorBodyReadLimit_RespectsDiagnosticLimit(t *testing.T) {
	svc := newAntigravityTestService(&config.Config{Gateway: config.GatewayConfig{
		LogUpstreamErrorBody:         true,
		LogUpstreamErrorBodyMaxBytes: int(gatewayUpstreamErrorBodyReadLimit) + 1024,
	}})

	require.Equal(t, int64(svc.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes), svc.upstreamErrorBodyReadLimit())
}

func TestStripSignatureSensitiveBlocksFromClaudeRequest(t *testing.T) {
	req := &antigravity.ClaudeRequest{
		Model: "claude-sonnet-4-5",
		Thinking: &antigravity.ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: 1024,
		},
		Messages: []antigravity.ClaudeMessage{
			{
				Role: "assistant",
				Content: json.RawMessage(`[
					{"type":"thinking","thinking":"secret plan","signature":""},
					{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}
				]`),
			},
			{
				Role: "user",
				Content: json.RawMessage(`[
					{"type":"tool_result","tool_use_id":"t1","content":"ok","is_error":false},
					{"type":"redacted_thinking","data":"..."}
				]`),
			},
		},
	}

	changed, err := stripSignatureSensitiveBlocksFromClaudeRequest(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, req.Thinking)

	require.Len(t, req.Messages, 2)

	var blocks0 []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks0))
	require.Len(t, blocks0, 2)
	require.Equal(t, "text", blocks0[0]["type"])
	require.Equal(t, "secret plan", blocks0[0]["text"])
	require.Equal(t, "text", blocks0[1]["type"])

	var blocks1 []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[1].Content, &blocks1))
	require.Len(t, blocks1, 1)
	require.Equal(t, "text", blocks1[0]["type"])
	require.NotEmpty(t, blocks1[0]["text"])
}

func TestStripThinkingFromClaudeRequest_DoesNotDowngradeTools(t *testing.T) {
	req := &antigravity.ClaudeRequest{
		Model: "claude-sonnet-4-5",
		Thinking: &antigravity.ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: 1024,
		},
		Messages: []antigravity.ClaudeMessage{
			{
				Role:    "assistant",
				Content: json.RawMessage(`[{"type":"thinking","thinking":"secret plan"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]`),
			},
		},
	}

	changed, err := stripThinkingFromClaudeRequest(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, req.Thinking)

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0]["type"])
	require.Equal(t, "secret plan", blocks[0]["text"])
	require.Equal(t, "tool_use", blocks[1]["type"])
}

func TestIsPromptTooLongError(t *testing.T) {
	require.True(t, isPromptTooLongError([]byte(`{"error":{"message":"Prompt is too long"}}`)))
	require.True(t, isPromptTooLongError([]byte(`{"message":"Prompt is too long"}`)))
	require.False(t, isPromptTooLongError([]byte(`{"error":{"message":"other"}}`)))
}

type httpUpstreamStub struct {
	resp *http.Response
	err  error
}

func (s *httpUpstreamStub) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.resp, s.err
}

func (s *httpUpstreamStub) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.resp, s.err
}

type queuedHTTPUpstreamStub struct {
	responses     []*http.Response
	errors        []error
	requestBodies [][]byte
	callCount     int
	onCall        func(*http.Request, *queuedHTTPUpstreamStub)
	allowNilReply bool
}

func (s *queuedHTTPUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.requestBodies = append(s.requestBodies, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		s.requestBodies = append(s.requestBodies, nil)
	}

	idx := s.callCount
	s.callCount++
	if s.onCall != nil {
		s.onCall(req, s)
	}

	var resp *http.Response
	if idx < len(s.responses) {
		resp = s.responses[idx]
	}
	var err error
	if idx < len(s.errors) {
		err = s.errors[idx]
	}
	if resp == nil && err == nil && !s.allowNilReply {
		return nil, errors.New("unexpected upstream call")
	}
	return resp, err
}

func (s *queuedHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, concurrency)
}

type recordingInternal500CounterCache struct {
	incrementCalls []int64
	resetCalls     []int64
}

func (c *recordingInternal500CounterCache) IncrementInternal500Count(_ context.Context, accountID int64) (int64, error) {
	c.incrementCalls = append(c.incrementCalls, accountID)
	return int64(len(c.incrementCalls)), nil
}

func (c *recordingInternal500CounterCache) ResetInternal500Count(_ context.Context, accountID int64) error {
	c.resetCalls = append(c.resetCalls, accountID)
	return nil
}

type antigravitySettingRepoStub struct {
	values map[string]string
}

func (s *antigravitySettingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *antigravitySettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (s *antigravitySettingRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *antigravitySettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *antigravitySettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *antigravitySettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *antigravitySettingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestResolveAntigravityProjectID(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    string
		wantErr bool
	}{
		{
			name: "uses onboard project_id first",
			account: &Account{Credentials: map[string]any{
				"project_id": " onboard-project ",
				antigravityProjectIDFallbackCredentialKey: " configured-project ",
			}},
			want: "onboard-project",
		},
		{
			name: "uses configured credentials fallback",
			account: &Account{Credentials: map[string]any{
				antigravityProjectIDFallbackCredentialKey: " configured-project ",
			}},
			want: "configured-project",
		},
		{
			name: "uses configured extra fallback",
			account: &Account{Extra: map[string]any{
				antigravityProjectIDFallbackCredentialKey: " extra-project ",
			}},
			want: "extra-project",
		},
		{
			name:    "missing project",
			account: &Account{Credentials: map[string]any{}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAntigravityProjectID(tc.account)
			if tc.wantErr {
				require.ErrorIs(t, err, errAntigravityProjectIDRequired)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAntigravityGatewayService_ForwardGemini_UsesConfiguredProjectFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
		},
	})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/antigravity/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(body))

	upstreamBody := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1}}}\n\n")
	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
			},
		},
	}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}

	account := &Account{
		ID:          101,
		Name:        "acc-configured-project",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			antigravityProjectIDFallbackCredentialKey: "configured-project",
			"model_mapping": map[string]any{
				"gemini-2.5-flash": "gemini-2.5-flash",
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requestBodies, 1)

	var wrapped map[string]any
	require.NoError(t, json.Unmarshal(upstream.requestBodies[0], &wrapped))
	require.Equal(t, "configured-project", wrapped["project"])
}

func TestAntigravityGatewayService_ForwardGemini_ImageUsesDefaultMappingAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw a cat"}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"],"imageConfig":{"aspectRatio":"1:1"}}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.1-flash-image:generateContent", bytes.NewReader(body))

	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"inlineData\":{\"mimeType\":\"image/png\",\"data\":\"aGVsbG8=\"}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1}}}\n\n",
			)),
		}},
		onCall: func(req *http.Request, _ *queuedHTTPUpstreamStub) {
			require.Equal(t, "Bearer test-access-token", req.Header.Get("Authorization"))
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.Contains(t, req.URL.String(), "/v1internal:streamGenerateContent?alt=sse")
		},
	}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}
	account := &Account{
		ID:          104,
		Name:        "antigravity-image",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-access-token",
			"project_id":   "test-project",
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-3.1-flash-image", "generateContent", true, body, false)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemini-3.1-flash-image", result.Model)
	require.Equal(t, "gemini-3.1-flash-image", result.UpstreamModel)
	require.Equal(t, 1, result.ImageCount)
	require.Len(t, upstream.requestBodies, 1)

	var wrapped map[string]any
	require.NoError(t, json.Unmarshal(upstream.requestBodies[0], &wrapped))
	require.Equal(t, "test-project", wrapped["project"])
	require.Equal(t, "gemini-3.1-flash-image", wrapped["model"])
	request, ok := wrapped["request"].(map[string]any)
	require.True(t, ok)
	generationConfig, ok := request["generationConfig"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"TEXT", "IMAGE"}, generationConfig["responseModalities"])
}

func TestAntigravityGatewayService_ForwardGemini_PreservesServerSideToolInvocationConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object","additionalProperties":false}}]},{"googleSearch":{}}],"toolConfig":{"includeServerSideToolInvocations":true}}`)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	body = bytes.ReplaceAll(body, []byte{92}, nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewReader(body))

	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{}}}\n\n")),
	}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}
	account := &Account{
		ID: 103, Name: "native-gemini", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "project-103", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "generateContent", false, body, false)
	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "provider finishReason must prove the stream-to-buffer response complete")
	require.Len(t, upstream.requestBodies, 1)

	var wrapped map[string]any
	require.NoError(t, json.Unmarshal(upstream.requestBodies[0], &wrapped))
	request, ok := wrapped["request"].(map[string]any)
	require.True(t, ok)
	toolConfig, ok := request["toolConfig"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, toolConfig["includeServerSideToolInvocations"])
	require.NotContains(t, toolConfig, "include_server_side_tool_invocations")
}

func TestAntigravityGatewayService_ForwardGemini_StreamingMissingUsageIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	providerBody := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{}}}\n\n")
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(requestBody))
	enableCaptureForTest(t, c)

	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"antigravity-missing-usage"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
	}}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
	}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}
	account := &Account{
		ID: 104, Name: "native-gemini-stream", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "project-104", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, requestBody, false)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete)
	require.Equal(t, providerBody, result.CaptureResponse)
	marked, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.True(t, marked.Stream)
	_, bridgeRemains := takeCaptureResult(c)
	require.False(t, bridgeRemains, "the public boundary must consume the capture bridge exactly once")
}

func TestAntigravityGatewayService_ForwardGemini_MissingProjectReturnsLocalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
		},
	})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/antigravity/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewReader(body))

	upstream := &queuedHTTPUpstreamStub{}
	internal500Cache := &recordingInternal500CounterCache{}
	svc := &AntigravityGatewayService{
		tokenProvider:    &AntigravityTokenProvider{},
		httpUpstream:     upstream,
		internal500Cache: internal500Cache,
	}

	account := &Account{
		ID:          102,
		Name:        "acc-missing-project",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"model_mapping": map[string]any{
				"gemini-2.5-flash": "gemini-2.5-flash",
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, body, false)
	require.Nil(t, result)
	require.ErrorIs(t, err, errAntigravityProjectIDRequired)
	require.Equal(t, http.StatusBadRequest, writer.Code)
	require.Empty(t, upstream.requestBodies)
	require.Empty(t, internal500Cache.incrementCalls)
	require.Contains(t, writer.Body.String(), "project_id")
	require.NotContains(t, writer.Body.String(), `"project":""`)
}

func TestAntigravityGatewayService_Forward_PromptTooLong(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"model": "claude-opus-4-6",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 1,
		"stream":     false,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request = req

	respBody := []byte(`{"error":{"message":"Prompt is too long"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Request-Id": []string{"req-1"}},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}

	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   &httpUpstreamStub{resp: resp},
	}

	account := &Account{
		ID:          1,
		Name:        "acc-1",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result)

	var promptErr *PromptTooLongError
	require.ErrorAs(t, err, &promptErr)
	require.Equal(t, http.StatusBadRequest, promptErr.StatusCode)
	require.Equal(t, "req-1", promptErr.RequestID)
	require.NotEmpty(t, promptErr.Body)

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "prompt_too_long", events[0].Kind)
}

func TestAntigravityGatewayService_Forward_SignatureRetryCaptureUsesOnlyFinalHTTPAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"bad"}]},{"role":"user","content":"hi"}],"max_tokens":32}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)

	initialBody := []byte(`{"error":{"message":"Corrupted thought signature."}}`)
	finalBody := []byte(`{"error":{"message":"` + strings.Repeat("z", 64<<10) + `"}}`)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": {"signature-initial-400"}}, Body: io.NopCloser(bytes.NewReader(initialBody))},
		{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"X-Request-Id": {"signature-final-422"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{ID: 12, Name: "signature-retry-capture", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "proj"}}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	record := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, failure, 1<<20)
	require.NotNil(t, record)
	require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.Equal(t, "signature-final-422", record.RequestID)
	require.Len(t, upstream.requestBodies, 2)
	require.Equal(t, upstream.requestBodies[1], record.RawRequest)
}

func TestAntigravityGatewayService_Forward_SignatureRetryReadErrorKeepsFinalHTTPAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"bad"}]},{"role":"user","content":"hi"}],"max_tokens":32}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)

	initialBody := []byte(`{"error":{"message":"Corrupted thought signature."}}`)
	finalBody := []byte(`{"error":{"message":"final retry body prefix"}}`)
	readErr := errors.New("forced signature retry response read failure")
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": {"signature-initial-400"}}, Body: io.NopCloser(bytes.NewReader(initialBody))},
		{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"X-Request-Id": {"signature-final-422"}}, Body: &antigravityCompatErrorReader{data: finalBody, err: readErr}},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{ID: 120, Name: "signature-retry-read-error", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "proj"}}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	record := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, failure, 1<<20)
	require.NotNil(t, record)
	require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.Len(t, upstream.requestBodies, 2)
	require.Equal(t, upstream.requestBodies[1], record.RawRequest)
}

func TestAntigravityGatewayService_Forward_BudgetRetryReadErrorKeepsFinalHTTPAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)

	initialBody := []byte(`{"error":{"message":"thinking budget_tokens input should be greater than or equal to 1024"}}`)
	finalBody := []byte(`{"error":{"message":"budget retry body prefix"}}`)
	readErr := errors.New("forced budget retry response read failure")
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": {"budget-initial-400"}}, Body: io.NopCloser(bytes.NewReader(initialBody))},
		{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"X-Request-Id": {"budget-final-422"}}, Body: &antigravityCompatErrorReader{data: finalBody, err: readErr}},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{ID: 121, Name: "budget-retry-read-error", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "proj"}}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	record := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, failure, 1<<20)
	require.NotNil(t, record)
	require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.Len(t, upstream.requestBodies, 2)
	require.Equal(t, upstream.requestBodies[1], record.RawRequest)
}

func TestAntigravityGatewayService_Forward_SignatureRetryTransportDoesNotPairInitialHTTPResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"bad"}]},{"role":"user","content":"hi"}],"max_tokens":32}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)

	initialBody := []byte(`{"error":{"message":"Corrupted thought signature."}}`)
	transportErr := errors.New("signature retry transport failed")
	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": {"signature-initial-400"}}, Body: io.NopCloser(bytes.NewReader(initialBody))}},
		errors:    []error{nil, transportErr, transportErr, transportErr},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{ID: 13, Name: "signature-retry-transport", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "proj"}}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), transportErr.Error())
	_, captured := takeCaptureResult(c)
	require.False(t, captured, "a request-only signature retry must not be paired with the initial 400")
}

func TestAntigravityForwardUpstreamTerminalOnlyPolicyArchivesHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)

	providerBody := []byte(`{"error":{"message":"provider rejected request"}}`)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusUnprocessableEntity,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"antigravity-upstream-422"},
		},
		Body: io.NopCloser(bytes.NewReader(providerBody)),
	}}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		httpUpstream:   upstream,
	}
	account := &Account{
		ID:          14,
		Name:        "compatible-upstream-terminal",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://compatible.example", "api_key": "secret"},
	}

	result, err := svc.ForwardUpstream(context.Background(), c, account, body)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, http.StatusUnprocessableEntity, writer.Code)
	require.Equal(t, providerBody, writer.Body.Bytes())

	record := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, failure, 1<<20)
	require.NotNil(t, record, "terminal-only policy must archive an upstream-compatible HTTP failure")
	require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
	require.Equal(t, providerBody, record.RawResponse)
	require.Equal(t, upstream.requestBodies[0], record.RawRequest)
	require.Equal(t, "antigravity-upstream-422", record.RequestID)
}

func TestAntigravityForwardUpstreamNonStreamingUsesFunctionalResponseLimitBeyondCapturePrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	requestBody := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
	enableCaptureForTest(t, c)

	providerBody := []byte(`{"id":"msg_large","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0},"padding":"` + strings.Repeat("x", captureHardMaxBodyBytes) + `"}`)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
	}}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: captureHardMaxBodyBytes}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		httpUpstream:   upstream,
	}
	account := &Account{ID: 15, Name: "large-compatible-response", Platform: PlatformAntigravity, Type: AccountTypeUpstream, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://compatible.example", "api_key": "secret"}}

	result, err := svc.ForwardUpstream(context.Background(), c, account, requestBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, providerBody, writer.Body.Bytes(), "business forwarding must not inherit the smaller capture retention limit")
	require.Len(t, result.CaptureResponse, captureHardMaxBodyBytes)
	require.True(t, result.CaptureTruncated)
}

// TestAntigravityGatewayService_Forward_ModelRateLimitTriggersFailover
// 验证：当账号存在模型限流且剩余时间 >= antigravityRateLimitThreshold 时，
// Forward 方法应返回 UpstreamFailoverError，触发 Handler 切换账号
func TestAntigravityGatewayService_Forward_ModelRateLimitTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"model": "claude-opus-4-6",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 1,
		"stream":     false,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request = req

	// 不需要真正调用上游，因为预检查会直接返回切换信号
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  &httpUpstreamStub{resp: nil, err: nil},
	}

	// 设置模型限流：剩余时间 30 秒（> antigravityRateLimitThreshold 7s）
	futureResetAt := time.Now().Add(30 * time.Second).Format(time.RFC3339)
	account := &Account{
		ID:          1,
		Name:        "acc-rate-limited",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"claude-opus-4-6-thinking": map[string]any{
					"rate_limit_reset_at": futureResetAt,
				},
			},
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.Nil(t, result, "Forward should not return result when model rate limited")
	require.NotNil(t, err, "Forward should return error")

	// 核心验证：错误应该是 UpstreamFailoverError，而不是普通 502 错误
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "error should be UpstreamFailoverError to trigger account switch")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	// 非粘性会话请求，ForceCacheBilling 应为 false
	require.False(t, failoverErr.ForceCacheBilling, "ForceCacheBilling should be false for non-sticky session")
}

// TestAntigravityGatewayService_ForwardGemini_ModelRateLimitTriggersFailover
// 验证：ForwardGemini 方法同样能正确将 AntigravityAccountSwitchError 转换为 UpstreamFailoverError
func TestAntigravityGatewayService_ForwardGemini_ModelRateLimitTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hi"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewReader(body))
	c.Request = req

	// 不需要真正调用上游，因为预检查会直接返回切换信号
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  &httpUpstreamStub{resp: nil, err: nil},
	}

	// 设置模型限流：剩余时间 30 秒（> antigravityRateLimitThreshold 7s）
	futureResetAt := time.Now().Add(30 * time.Second).Format(time.RFC3339)
	account := &Account{
		ID:          2,
		Name:        "acc-gemini-rate-limited",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"gemini-2.5-flash": map[string]any{
					"rate_limit_reset_at": futureResetAt,
				},
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "generateContent", false, body, false)
	require.Nil(t, result, "ForwardGemini should not return result when model rate limited")
	require.NotNil(t, err, "ForwardGemini should return error")

	// 核心验证：错误应该是 UpstreamFailoverError，而不是普通 502 错误
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "error should be UpstreamFailoverError to trigger account switch")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	// 非粘性会话请求，ForceCacheBilling 应为 false
	require.False(t, failoverErr.ForceCacheBilling, "ForceCacheBilling should be false for non-sticky session")
}

// TestAntigravityGatewayService_Forward_StickySessionForceCacheBilling
// 验证：粘性会话切换时，UpstreamFailoverError.ForceCacheBilling 应为 true
func TestAntigravityGatewayService_Forward_StickySessionForceCacheBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request = req

	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  &httpUpstreamStub{resp: nil, err: nil},
	}

	// 设置模型限流：剩余时间 30 秒（> antigravityRateLimitThreshold 7s）
	futureResetAt := time.Now().Add(30 * time.Second).Format(time.RFC3339)
	account := &Account{
		ID:          3,
		Name:        "acc-sticky-rate-limited",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"claude-opus-4-6-thinking": map[string]any{
					"rate_limit_reset_at": futureResetAt,
				},
			},
		},
	}

	// 传入 isStickySession = true
	result, err := svc.Forward(context.Background(), c, account, body, true)
	require.Nil(t, result, "Forward should not return result when model rate limited")
	require.NotNil(t, err, "Forward should return error")

	// 核心验证：粘性会话切换时，ForceCacheBilling 应为 true
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "error should be UpstreamFailoverError to trigger account switch")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.True(t, failoverErr.ForceCacheBilling, "ForceCacheBilling should be true for sticky session switch")
}

// TestAntigravityGatewayService_ForwardGemini_StickySessionForceCacheBilling verifies
// that ForwardGemini sets ForceCacheBilling=true for sticky session switch.
func TestAntigravityGatewayService_ForwardGemini_StickySessionForceCacheBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hi"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewReader(body))
	c.Request = req

	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  &httpUpstreamStub{resp: nil, err: nil},
	}

	// 设置模型限流：剩余时间 30 秒（> antigravityRateLimitThreshold 7s）
	futureResetAt := time.Now().Add(30 * time.Second).Format(time.RFC3339)
	account := &Account{
		ID:          4,
		Name:        "acc-gemini-sticky-rate-limited",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"gemini-2.5-flash": map[string]any{
					"rate_limit_reset_at": futureResetAt,
				},
			},
		},
	}

	// 传入 isStickySession = true
	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "generateContent", false, body, true)
	require.Nil(t, result, "ForwardGemini should not return result when model rate limited")
	require.NotNil(t, err, "ForwardGemini should return error")

	// 核心验证：粘性会话切换时，ForceCacheBilling 应为 true
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "error should be UpstreamFailoverError to trigger account switch")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.True(t, failoverErr.ForceCacheBilling, "ForceCacheBilling should be true for sticky session switch")
}

func TestAntigravityGatewayService_ForwardGemini_ClearsStickySessionOnGeminiRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hi"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-flash-preview:generateContent", bytes.NewReader(body))
	c.Request = req

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	upstream := &httpUpstreamStub{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}}
	repo := &stubAntigravityAccountRepo{}
	cache := &stubSmartRetryCache{}
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
		accountRepo:   repo,
		cache:         cache,
	}

	account := &Account{
		ID:          44,
		Name:        "acc-gemini-runtime-rate-limited",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"expires_at":   time.Now().Add(time.Hour).Format(time.RFC3339),
			"project_id":   "proj",
		},
		Extra: map[string]any{
			"mixed_scheduling": true,
		},
	}

	result, err := svc.ForwardGemini(
		context.Background(),
		c,
		account,
		"gemini-3-flash-preview",
		"generateContent",
		false,
		body,
		true,
		WithForwardGeminiSession(77, "gemini:sticky-runtime"),
	)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-flash", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, antigravityGeminiModelRateLimitKey, repo.modelRateLimitCalls[1].modelKey)
	require.Len(t, cache.deleteCalls, 1)
	require.Equal(t, int64(77), cache.deleteCalls[0].groupID)
	require.Equal(t, "gemini:sticky-runtime", cache.deleteCalls[0].sessionHash)
}

// TestAntigravityGatewayService_Forward_BillsWithMappedModel
// 验证：Antigravity Claude 转发返回的计费模型使用映射后的模型
func TestAntigravityGatewayService_Forward_BillsWithMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
		"max_tokens": 16,
		"stream":     true,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request = req
	enableCaptureForTest(t, c)

	upstreamBody := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}}\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"req-bill-1"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}

	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   &httpUpstreamStub{resp: resp},
	}

	const mappedModel = "gemini-3-pro-high"
	account := &Account{
		ID:          5,
		Name:        "acc-forward-billing",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
			"model_mapping": map[string]any{
				"claude-sonnet-4-5": mappedModel,
			},
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4-5", result.Model)
	require.Equal(t, mappedModel, result.UpstreamModel)
	require.Equal(t, upstreamBody, result.CaptureResponse)
	require.NotEmpty(t, result.CaptureRequest)
	require.NotNil(t, result.CaptureContentPolicy)
}

func TestAntigravityGatewayService_Forward_MissingUsageIsProviderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"antigravity-claude-no-usage"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n")),
	}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}
	account := &Account{ID: 16, Name: "antigravity-no-usage", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "project-16", "model_mapping": map[string]any{"claude-sonnet-4-5": "gemini-3-pro-high"}}}

	result, err := svc.Forward(context.Background(), c, account, body, false)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "provider finishReason must prove the Claude stream-to-buffer response complete")
	marked, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "upstream_usage_missing", marked.Code)
}

func TestAntigravityForwardUpstream_MissingUsageIsProviderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	providerBody := []byte(`{"id":"msg_no_usage","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(providerBody))}}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{}), httpUpstream: upstream}
	account := &Account{ID: 17, Name: "compatible-no-usage", Platform: PlatformAntigravity, Type: AccountTypeUpstream, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://compatible.example", "api_key": "secret"}}

	result, err := svc.ForwardUpstream(context.Background(), c, account, body)

	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete, "successful full-body read must survive missing-usage terminalization")
	marked, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "upstream_usage_missing", marked.Code)
}

// TestAntigravityGatewayService_ForwardGemini_BillsWithMappedModel
// 验证：Antigravity Gemini 转发返回的计费模型使用映射后的模型
func TestAntigravityGatewayService_ForwardGemini_BillsWithMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewReader(body))
	c.Request = req
	enableCaptureForTest(t, c)

	upstreamBody := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}}\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"req-bill-2"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}

	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   &httpUpstreamStub{resp: resp},
	}

	const mappedModel = "gemini-3-pro-high"
	account := &Account{
		ID:          6,
		Name:        "acc-gemini-billing",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
			"model_mapping": map[string]any{
				"gemini-2.5-flash": mappedModel,
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash", "generateContent", true, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemini-2.5-flash", result.Model)
	require.Equal(t, mappedModel, result.UpstreamModel)
	require.Equal(t, upstreamBody, result.CaptureResponse)
	require.NotEmpty(t, result.CaptureRequest)
	require.NotNil(t, result.CaptureContentPolicy)
}

func TestAntigravityGatewayService_ForwardGemini_FallbackReportsActualUpstreamModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-primary:generateContent", bytes.NewReader(body))

	const (
		originalModel = "gemini-primary"
		mappedModel   = "gemini-primary-upstream"
		fallbackModel = "gemini-fallback-upstream"
	)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":404,"message":"model not found"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"response":{"modelVersion":"gemini-fallback-upstream","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}}` + "\n\n",
			)),
		},
	}}
	settings := &antigravitySettingRepoStub{values: map[string]string{
		SettingKeyEnableModelFallback:      "true",
		SettingKeyFallbackModelAntigravity: fallbackModel,
	}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(settings, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}
	account := &Account{
		ID:          9,
		Name:        "acc-gemini-fallback",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
			"model_mapping": map[string]any{
				originalModel: mappedModel,
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, originalModel, "generateContent", true, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, originalModel, result.Model)
	require.Equal(t, fallbackModel, result.UpstreamModel)
	require.Equal(t, fallbackModel, result.UpstreamResponseModel)
	require.False(t, result.UpstreamResponseModelConflict)
	mismatch := upstreamModelMismatch(result.UpstreamModel, result.UpstreamResponseModel)
	require.NotNil(t, mismatch)
	require.False(t, *mismatch)
	require.Len(t, upstream.requestBodies, 2)
	require.Contains(t, string(upstream.requestBodies[0]), `"model":"`+mappedModel+`"`)
	require.Contains(t, string(upstream.requestBodies[1]), `"model":"`+fallbackModel+`"`)
}

func TestAntigravityGatewayService_ForwardGemini_FallbackFailureCaptureUsesOnlyFinalAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-primary:generateContent", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	c.Set("parsed_request", &ParsedRequest{Body: NewRequestBodyRef(body), Model: "gemini-primary"})

	const mappedModel, fallbackModel = "gemini-primary-upstream", "gemini-fallback-upstream"
	initialBody := []byte(`{"error":{"code":404,"message":"model not found"}}`)
	finalBody := []byte(`{"response":{"error":{"code":422,"message":"fallback rejected"}}}`)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"initial-404"}}, Body: io.NopCloser(bytes.NewReader(initialBody))},
		{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"fallback-422"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	settings := &antigravitySettingRepoStub{values: map[string]string{
		SettingKeyEnableModelFallback: "true", SettingKeyFallbackModelAntigravity: fallbackModel,
	}}
	svc := &AntigravityGatewayService{settingService: NewSettingService(settings, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{
		ID: 91, Name: "fallback-final-attempt", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "proj", "model_mapping": map[string]any{"gemini-primary": mappedModel}},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-primary", "generateContent", false, body, false)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	record := BuildTerminalErrorCaptureRecord(c, PlatformAntigravity, failure, 1<<20)
	require.NotNil(t, record)
	require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.NotEqual(t, initialBody, record.RawResponse)
	require.Contains(t, string(record.RawRequest), `"model":"`+fallbackModel+`"`)
	require.Equal(t, "fallback-422", record.RequestID)
	require.Len(t, upstream.requestBodies, 2)
}

func TestAntigravityGatewayService_ForwardGemini_FallbackTransportOrNilResponseAbortsFinalAttempt(t *testing.T) {
	transportErr := errors.New("fallback transport failed")
	for _, tt := range []struct {
		name          string
		fallbackError error
		allowNilReply bool
	}{
		{name: "transport_error", fallbackError: transportErr},
		{name: "nil_response", allowNilReply: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-primary:generateContent", bytes.NewReader(body))
			enableCaptureForTest(t, c)

			initialBody := []byte(`{"error":{"code":404,"message":"model not found"}}`)
			upstream := &queuedHTTPUpstreamStub{
				responses:     []*http.Response{{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(initialBody))}},
				errors:        []error{nil, tt.fallbackError},
				allowNilReply: tt.allowNilReply,
			}
			cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 16 << 10}}}
			settings := &antigravitySettingRepoStub{values: map[string]string{SettingKeyEnableModelFallback: "true", SettingKeyFallbackModelAntigravity: "gemini-fallback"}}
			captureTransport := &recordingCaptureTransport{}
			svc := &AntigravityGatewayService{
				settingService: NewSettingService(settings, cfg), tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream,
				capturePool: newConversationCapturePoolForTransport(captureTransport, func() bool { return true }),
			}
			account := &Account{ID: 92, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"access_token": "token", "project_id": "proj", "model_mapping": map[string]any{"gemini-primary": "gemini-primary-upstream"}}}

			result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-primary", "generateContent", false, body, false)
			require.Nil(t, result)
			if tt.fallbackError != nil {
				require.ErrorIs(t, err, tt.fallbackError)
			} else {
				require.ErrorContains(t, err, "upstream returned nil response")
			}
			require.Len(t, captureTransport.Attempts(), 2)
			require.Equal(t, []captureTerminalState{captureAborted}, captureTransport.Attempts()[0].TerminalStates(), "starting the fallback must abort the initial 404 attempt")
			finalAttempt := captureTransport.Attempts()[1]
			require.Equal(t, []captureTerminalState{captureAborted}, finalAttempt.TerminalStates(), "the request-only fallback attempt must terminate exactly once")
			require.NotContains(t, finalAttempt.TerminalStates(), captureCommitted)
		})
	}
}

func TestAntigravityGatewayService_ForwardGemini_RetriesCorruptedThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
			{"role": "model", "parts": []map[string]any{{"text": "thinking", "thought": true, "thoughtSignature": "sig_bad_1"}}},
			{"role": "model", "parts": []map[string]any{{"functionCall": map[string]any{"name": "toolA", "args": map[string]any{"x": 1}}, "thoughtSignature": "sig_bad_2"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/antigravity/v1beta/models/gemini-3.1-pro-preview:streamGenerateContent", bytes.NewReader(body))
	c.Request = req

	firstRespBody := []byte(`{"response":{"error":{"code":400,"message":"Corrupted thought signature.","status":"INVALID_ARGUMENT"}}}`)
	secondRespBody := []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}}\n\n")

	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req-sig-1"},
				},
				Body: io.NopCloser(bytes.NewReader(firstRespBody)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req-sig-2"},
				},
				Body: io.NopCloser(bytes.NewReader(secondRespBody)),
			},
		},
	}

	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}

	const originalModel = "gemini-3.1-pro-preview"
	const mappedModel = "gemini-3.1-pro-high"
	account := &Account{
		ID:          7,
		Name:        "acc-gemini-signature",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
			"model_mapping": map[string]any{
				originalModel: mappedModel,
			},
		},
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, originalModel, "streamGenerateContent", true, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, originalModel, result.Model)
	require.Equal(t, mappedModel, result.UpstreamModel)
	require.Len(t, upstream.requestBodies, 2, "signature error should trigger exactly one retry")

	firstReq := string(upstream.requestBodies[0])
	secondReq := string(upstream.requestBodies[1])
	require.Contains(t, firstReq, `"thoughtSignature":"sig_bad_1"`)
	require.Contains(t, firstReq, `"thoughtSignature":"sig_bad_2"`)
	require.Contains(t, secondReq, `"thoughtSignature":"skip_thought_signature_validator"`)
	require.NotContains(t, secondReq, `"thoughtSignature":"sig_bad_1"`)
	require.NotContains(t, secondReq, `"thoughtSignature":"sig_bad_2"`)

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, events)
	require.Equal(t, "signature_error", events[0].Kind)
}

func TestAntigravityGatewayService_ForwardGemini_SignatureRetryPropagatesFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
			{"role": "model", "parts": []map[string]any{{"text": "thinking", "thought": true, "thoughtSignature": "sig_bad_1"}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/antigravity/v1beta/models/gemini-3.1-pro-preview:streamGenerateContent", bytes.NewReader(body))
	c.Request = req

	firstRespBody := []byte(`{"response":{"error":{"code":400,"message":"Corrupted thought signature.","status":"INVALID_ARGUMENT"}}}`)

	const originalModel = "gemini-3.1-pro-preview"
	const mappedModel = "gemini-3.1-pro-high"
	account := &Account{
		ID:          8,
		Name:        "acc-gemini-signature-failover",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "proj",
			"model_mapping": map[string]any{
				originalModel: mappedModel,
			},
		},
	}

	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"req-sig-failover-1"},
				},
				Body: io.NopCloser(bytes.NewReader(firstRespBody)),
			},
		},
		onCall: func(_ *http.Request, stub *queuedHTTPUpstreamStub) {
			if stub.callCount != 1 {
				return
			}
			futureResetAt := time.Now().Add(30 * time.Second).Format(time.RFC3339)
			account.Extra = map[string]any{
				modelRateLimitsKey: map[string]any{
					mappedModel: map[string]any{
						"rate_limit_reset_at": futureResetAt,
					},
				},
			}
		},
	}

	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
	}

	result, err := svc.ForwardGemini(context.Background(), c, account, originalModel, "streamGenerateContent", true, body, true)
	require.Nil(t, result)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "signature retry should propagate failover instead of falling back to the original 400")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.True(t, failoverErr.ForceCacheBilling)
	require.Len(t, upstream.requestBodies, 1, "retry should stop at preflight failover and not issue a second upstream request")

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 2)
	require.Equal(t, "signature_error", events[0].Kind)
	require.Equal(t, "failover", events[1].Kind)
}

// TestStreamUpstreamResponse_UsageAndFirstToken
// 验证：usage 字段可被累积/覆盖更新，并且能记录首 token 时间
func TestStreamUpstreamResponse_UsageAndFirstToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `event: message_start`)
		fmt.Fprintln(pw, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`)
		fmt.Fprintln(pw, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		fmt.Fprintln(pw, `event: message_stop`)
		fmt.Fprintln(pw, `data: {"type":"message_stop"}`)
	}()

	start := time.Now().Add(-10 * time.Millisecond)
	result, err := svc.streamUpstreamResponse(c, resp, start)
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.usage)
	require.Equal(t, 1, result.usage.InputTokens)
	// 第二次事件覆盖 output_tokens
	require.Equal(t, 5, result.usage.OutputTokens)
	require.Equal(t, 3, result.usage.CacheReadInputTokens)
	require.Equal(t, 4, result.usage.CacheCreationInputTokens)
	require.NotNil(t, result.firstTokenMs)

	// 确保有透传输出
	require.Contains(t, rec.Body.String(), "data:")
}

// --- 流式 happy path 测试 ---

// TestStreamUpstreamResponse_NormalComplete
// 验证：正常流式转发完成时，数据正确透传、usage 正确收集、clientDisconnect=false
func TestStreamUpstreamResponse_NormalComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `event: message_start`)
		fmt.Fprintln(pw, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10}}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: content_block_start`)
		fmt.Fprintln(pw, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: content_block_delta`)
		fmt.Fprintln(pw, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: content_block_stop`)
		fmt.Fprintln(pw, `data: {"type":"content_block_stop","index":0}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: message_delta`)
		fmt.Fprintln(pw, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: message_stop`)
		fmt.Fprintln(pw, `data: {"type":"message_stop"}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.clientDisconnect, "normal completion should not set clientDisconnect")
	require.NotNil(t, result.usage)
	require.Equal(t, 5, result.usage.OutputTokens, "should collect output_tokens from message_delta")
	require.NotNil(t, result.firstTokenMs, "should record first token time")

	// 验证数据被透传到客户端
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "content_block_delta")
	require.Contains(t, body, "message_delta")
}

// TestHandleGeminiStreamingResponse_NormalComplete
// 验证：正常 Gemini 流式转发，数据正确透传、usage 正确收集
func TestHandleGeminiStreamingResponse_NormalComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		// 第一个 chunk（部分内容）
		fmt.Fprintln(pw, `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}`)
		fmt.Fprintln(pw, "")
		// 第二个 chunk（最终内容+完整 usage）
		fmt.Fprintln(pw, `data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":8,"cachedContentTokenCount":2}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.clientDisconnect, "normal completion should not set clientDisconnect")
	require.NotNil(t, result.usage)
	// Gemini usage: promptTokenCount=10, candidatesTokenCount=8, cachedContentTokenCount=2
	// → InputTokens=10-2=8, OutputTokens=8, CacheReadInputTokens=2
	require.Equal(t, 8, result.usage.InputTokens)
	require.Equal(t, 8, result.usage.OutputTokens)
	require.Equal(t, 2, result.usage.CacheReadInputTokens)
	require.NotNil(t, result.firstTokenMs, "should record first token time")

	// 验证数据被透传到客户端
	body := rec.Body.String()
	require.Contains(t, body, "Hello")
	require.Contains(t, body, "world")
	// 不应包含错误事件
	require.NotContains(t, body, "event: error")
}

func TestHandleGeminiStreamingResponseDenseUnknownPayloadAllocatesLinearly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dense := strings.TrimSuffix(strings.Repeat("0,", (8<<20)/2), ",")
	payload := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"opaque":[` + dense + `]}`
	body := "data: " + payload + "\n\n"
	svc := newAntigravityTestService(&config.Config{Gateway: config.GatewayConfig{MaxLineSize: 9 << 20}})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}

	runtime.GC()
	var before runtime.MemStats
	var after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
	runtime.ReadMemStats(&after)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.semanticOutput)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(96<<20))
}

func TestAntigravityForcedBufferedDenseUnknownPayloadAllocatesLinearly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dense := strings.TrimSuffix(strings.Repeat("0,", (4<<20)/2), ",")
	payload := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1},"opaque":[` + dense + `]}`
	body := "data: " + payload + "\n\n"
	svc := newAntigravityTestService(&config.Config{Gateway: config.GatewayConfig{MaxLineSize: 5 << 20}})

	for _, tc := range []struct {
		name string
		run  func(*gin.Context, *http.Response) error
	}{
		{
			name: "gemini",
			run: func(c *gin.Context, resp *http.Response) error {
				_, err := svc.handleGeminiStreamToNonStreaming(c, resp, time.Now())
				return err
			},
		},
		{
			name: "claude",
			run: func(c *gin.Context, resp *http.Response) error {
				_, _, err := svc.collectClaudeStreamResponse(c, resp, time.Now(), "claude-test")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}

			runtime.GC()
			var before runtime.MemStats
			var after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := tc.run(c, resp)
			runtime.ReadMemStats(&after)

			require.NoError(t, err)
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(128<<20))
		})
	}
}

func TestAntigravityDirectStreamReadErrorAfterSemanticOutputPreservesPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	forcedErr := errors.New("forced antigravity stream read failure")
	tests := []struct {
		name   string
		body   []byte
		invoke func(*AntigravityGatewayService, *gin.Context, *http.Response) (*antigravityStreamResult, error)
	}{
		{
			name: "gemini_native",
			body: []byte(`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}` + "\n\n"),
			invoke: func(s *AntigravityGatewayService, c *gin.Context, resp *http.Response) (*antigravityStreamResult, error) {
				return s.handleGeminiStreamingResponse(c, resp, time.Now())
			},
		},
		{
			name: "claude_native",
			body: []byte(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}}` + "\n\n"),
			invoke: func(s *AntigravityGatewayService, c *gin.Context, resp *http.Response) (*antigravityStreamResult, error) {
				return s.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newAntigravityTestService(&config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &antigravityCompatErrorReader{data: tt.body, err: forcedErr}}

			result, err := tt.invoke(svc, c, resp)

			require.ErrorIs(t, err, forcedErr)
			require.NotNil(t, result)
			require.True(t, result.semanticOutput)
			require.NotNil(t, result.usage)
			require.Equal(t, 8, result.usage.InputTokens)
			require.Equal(t, 3, result.usage.OutputTokens)
			require.Contains(t, recorder.Body.String(), "partial")
		})
	}
}

func TestAntigravityBaseURLStreamReadErrorAfterSemanticOutputReturnsBillableTerminalPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	forcedErr := errors.New("forced antigravity relay read failure")
	providerBody := []byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":8}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		``,
	}, "\n") + "\n")
	upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"ag-relay-partial"}},
		Body:       &antigravityCompatErrorReader{data: providerBody, err: forcedErr},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	svc := newAntigravityTestService(cfg)
	svc.httpUpstream = upstream
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	account := &Account{ID: 130, Platform: PlatformAntigravity, Type: AccountTypeUpstream, Concurrency: 1, Credentials: map[string]any{"base_url": "https://relay.example", "api_key": "relay-secret"}}

	result, err := svc.ForwardUpstream(context.Background(), c, account, body)

	require.ErrorIs(t, err, forcedErr)
	require.NotNil(t, result)
	require.False(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.Equal(t, "claude-sonnet-4-5", result.UpstreamModel)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, providerBody, result.CaptureResponse)
	require.Contains(t, recorder.Body.String(), "partial")
}

// TestHandleClaudeStreamingResponse_NormalComplete
// 验证：正常 Claude 流式转发（Gemini→Claude 转换），数据正确转换并输出
func TestHandleClaudeStreamingResponse_NormalComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		// v1internal 包装格式：Gemini 数据嵌套在 "response" 字段下
		// ProcessLine 先尝试反序列化为 V1InternalResponse，裸格式会导致 Response.UsageMetadata 为空
		fmt.Fprintln(pw, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"Hi there"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3}}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.clientDisconnect, "normal completion should not set clientDisconnect")
	require.NotNil(t, result.usage)
	// Gemini→Claude 转换的 usage：promptTokenCount=5→InputTokens=5, candidatesTokenCount=3→OutputTokens=3
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.NotNil(t, result.firstTokenMs, "should record first token time")

	// 验证输出是 Claude SSE 格式（processor 会转换）
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start", "should contain Claude message_start event")
	require.Contains(t, body, "event: message_stop", "should contain Claude message_stop event")
	// 不应包含错误事件
	require.NotContains(t, body, "event: error")
}

// TestHandleGeminiStreamingResponse_ThoughtsTokenCount
// 验证：Gemini 流式转发时 thoughtsTokenCount 被计入 OutputTokens
func TestHandleGeminiStreamingResponse_ThoughtsTokenCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":50}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30,"thoughtsTokenCount":80,"cachedContentTokenCount":10}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.usage)
	// promptTokenCount=100, cachedContentTokenCount=10 → InputTokens=90
	require.Equal(t, 90, result.usage.InputTokens)
	// candidatesTokenCount=30 + thoughtsTokenCount=80 → OutputTokens=110
	require.Equal(t, 110, result.usage.OutputTokens)
	require.Equal(t, 10, result.usage.CacheReadInputTokens)
}

// TestHandleClaudeStreamingResponse_ThoughtsTokenCount
// 验证：Gemini→Claude 流式转换时 thoughtsTokenCount 被计入 OutputTokens
func TestHandleClaudeStreamingResponse_ThoughtsTokenCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":10,"thoughtsTokenCount":25}}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "gemini-2.5-pro")
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.usage)
	// promptTokenCount=50 → InputTokens=50
	require.Equal(t, 50, result.usage.InputTokens)
	// candidatesTokenCount=10 + thoughtsTokenCount=25 → OutputTokens=35
	require.Equal(t, 35, result.usage.OutputTokens)
}

// --- 流式客户端断开检测测试 ---

// TestStreamUpstreamResponse_ClientDisconnectDrainsUsage
// 验证：客户端写入失败后，streamUpstreamResponse 继续读取上游以收集 usage
func TestStreamUpstreamResponse_ClientDisconnectDrainsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `event: message_start`)
		fmt.Fprintln(pw, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10}}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: message_delta`)
		fmt.Fprintln(pw, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `event: message_stop`)
		fmt.Fprintln(pw, `data: {"type":"message_stop"}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.True(t, result.terminalObserved)
	require.NotNil(t, result.usage)
	require.Equal(t, 20, result.usage.OutputTokens)
}

// TestStreamUpstreamResponse_ContextCanceled
// 验证：context 取消时保留 usage/clientDisconnect，但不能伪装成 provider success。
func TestStreamUpstreamResponse_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	resp := &http.Response{StatusCode: http.StatusOK, Body: cancelReadCloser{}, Header: http.Header{}}

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.NotContains(t, rec.Body.String(), "event: error")
}

// TestStreamUpstreamResponse_Timeout
// 验证：上游超时时返回已收集的 usage
func TestStreamUpstreamResponse_Timeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1, MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())
	_ = pw.Close()
	_ = pr.Close()

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result, "pre-semantic timeout is replay-safe and must not create a partial result")
	require.True(t, failoverErr.HasUpstreamHTTPResponse)
}

// TestStreamUpstreamResponse_TimeoutAfterClientDisconnect
// 验证：客户端断开后上游超时，返回 usage 并标记 clientDisconnect
func TestStreamUpstreamResponse_TimeoutAfterClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1, MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		fmt.Fprintln(pw, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5}}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`)
		fmt.Fprintln(pw, "")
		// 不关闭 pw → 等待超时
	}()

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())
	_ = pw.Close()
	_ = pr.Close()

	require.ErrorContains(t, err, "stream data interval timeout")
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.False(t, result.terminalObserved)
}

func TestStreamUpstreamResponse_ReadErrorAfterClientDisconnectIsTerminalPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body: &streamReadCloser{
			payload: []byte(strings.Join([]string{
				`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5}}}`,
				"",
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				"",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
				"",
			}, "\n")),
			err: io.ErrUnexpectedEOF,
		},
	}

	result, err := svc.streamUpstreamResponse(c, resp, time.Now())
	require.ErrorContains(t, err, "stream read error")
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.False(t, result.terminalObserved)
}

// TestHandleGeminiStreamingResponse_ClientDisconnect
// 验证：Gemini 流式转发中客户端断开后继续 drain 上游
func TestHandleGeminiStreamingResponse_ClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `data: {"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.NotContains(t, rec.Body.String(), "write_failed")
}

// TestHandleGeminiStreamingResponse_ContextCanceled
// 验证：context 取消时不注入错误事件，但返回 terminal partial error。
func TestHandleGeminiStreamingResponse_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	resp := &http.Response{StatusCode: http.StatusOK, Body: cancelReadCloser{}, Header: http.Header{}}

	result, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.NotContains(t, rec.Body.String(), "event: error")
}

// TestHandleClaudeStreamingResponse_ClientDisconnect
// 验证：Claude 流式转发中客户端断开后继续 drain 上游
func TestHandleClaudeStreamingResponse_ClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		// v1internal 包装格式
		fmt.Fprintln(pw, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":15}}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
}

func TestAntigravityNativeClientDisconnectDoesNotHideProviderReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name    string
		payload string
		run     func(*AntigravityGatewayService, *gin.Context, *http.Response) (*antigravityStreamResult, error)
	}{
		{
			name:    "gemini native",
			payload: `data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"partial"}]}}]}` + "\n\n",
			run: func(svc *AntigravityGatewayService, c *gin.Context, resp *http.Response) (*antigravityStreamResult, error) {
				return svc.handleGeminiStreamingResponse(c, resp, time.Now())
			},
		},
		{
			name:    "claude conversion",
			payload: `data: {"response":{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"partial"}]}}]}}` + "\n\n",
			run: func(svc *AntigravityGatewayService, c *gin.Context, resp *http.Response) (*antigravityStreamResult, error) {
				return svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newAntigravityTestService(&config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       &streamReadCloser{payload: []byte(tc.payload), err: io.ErrUnexpectedEOF},
			}

			result, err := tc.run(svc, c, resp)
			require.NotNil(t, result)
			require.True(t, result.clientDisconnect)
			require.False(t, result.terminalObserved)
			require.ErrorContains(t, err, "stream read error")
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		})
	}
}

// TestHandleClaudeStreamingResponse_EmptyStream
// 验证：上游只返回无法解析的 SSE 行时，触发 UpstreamFailoverError 而不是向客户端发出残缺流
func TestHandleClaudeStreamingResponse_EmptyStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		// 所有行均为无法 JSON 解析的内容，ProcessLine 全部返回 nil
		fmt.Fprintln(pw, "data: not-valid-json")
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, "data: also-invalid")
		fmt.Fprintln(pw, "")
	}()

	_, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
	_ = pr.Close()

	// 应当返回 UpstreamFailoverError 而非 nil，以便上层触发 failover
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)

	// 客户端不应收到任何 SSE 事件（既无 message_start 也无 message_stop）
	body := rec.Body.String()
	require.NotContains(t, body, "event: message_start")
	require.NotContains(t, body, "event: message_stop")
	require.NotContains(t, body, "event: message_delta")
}

// TestHandleClaudeStreamingResponse_ContextCanceled
// 验证：context 取消时不注入错误事件，但返回 terminal partial error。
func TestHandleClaudeStreamingResponse_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	resp := &http.Response{StatusCode: http.StatusOK, Body: cancelReadCloser{}, Header: http.Header{}}

	result, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.NotContains(t, rec.Body.String(), "event: error")
}

// TestExtractSSEUsage 验证 extractSSEUsage 从 SSE data 行正确提取 usage
func TestExtractSSEUsage(t *testing.T) {
	svc := &AntigravityGatewayService{}
	tests := []struct {
		name     string
		line     string
		expected ClaudeUsage
	}{
		{
			name:     "message_delta with output_tokens",
			line:     `data: {"type":"message_delta","usage":{"output_tokens":42}}`,
			expected: ClaudeUsage{OutputTokens: 42},
		},
		{
			name:     "compact data field without space",
			line:     `data:{"type":"message_delta","usage":{"output_tokens":43}}`,
			expected: ClaudeUsage{OutputTokens: 43},
		},
		{
			name:     "data field with tab",
			line:     "data:\t{\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":44}}}",
			expected: ClaudeUsage{InputTokens: 44},
		},
		{
			name:     "non-data line ignored",
			line:     `event: message_start`,
			expected: ClaudeUsage{},
		},
		{
			name:     "top-level usage with all fields",
			line:     `data: {"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}`,
			expected: ClaudeUsage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 5, CacheCreationInputTokens: 3},
		},
		{
			// Anthropic message_start 把 usage 嵌套在 message.usage 下，
			// 必须从这里提取输入侧字段（含 cache_read/cache_creation_input_tokens）。
			name:     "message_start nested usage with input/cache tokens",
			line:     `data: {"type":"message_start","message":{"id":"msg_01","usage":{"input_tokens":35576,"cache_creation_input_tokens":0,"cache_read_input_tokens":12000,"output_tokens":1}}}`,
			expected: ClaudeUsage{InputTokens: 35576, OutputTokens: 1, CacheReadInputTokens: 12000},
		},
		{
			// message_start.message.usage.cache_creation 内的 5m/1h 明细也要解析。
			name:     "message_start nested usage with cache_creation breakdown",
			line:     `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":70}}}}`,
			expected: ClaudeUsage{InputTokens: 100, CacheCreation5mTokens: 30, CacheCreation1hTokens: 70},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &ClaudeUsage{}
			svc.extractSSEUsage(tt.line, usage)
			require.Equal(t, tt.expected, *usage)
		})
	}
}

// TestExtractSSEUsage_StreamingSequence 复现 issue #2332：完整的 Anthropic streaming
// 序列（message_start → message_delta）必须把两类事件中的 usage 字段都汇入同一份累计值，
// 否则透传账号产出的 usage_logs 会出现 input_tokens=0、仅有 output_tokens 的"残缺"记录。
func TestExtractSSEUsage_StreamingSequence(t *testing.T) {
	svc := &AntigravityGatewayService{}
	usage := &ClaudeUsage{}

	// 1) message_start：携带完整输入侧 usage（input_tokens + cache_read）
	svc.extractSSEUsage(
		`data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":35576,"cache_creation_input_tokens":0,"cache_read_input_tokens":12000,"output_tokens":1}}}`,
		usage,
	)
	// 2) message_delta：流结束时只带 output_tokens（无 input_tokens 字段）
	svc.extractSSEUsage(
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":816}}`,
		usage,
	)

	require.Equal(t, 35576, usage.InputTokens, "message_start 的 input_tokens 必须被记录，否则记账会缺失输入侧 token (#2332)")
	require.Equal(t, 12000, usage.CacheReadInputTokens, "message_start 的 cache_read_input_tokens 必须被记录")
	require.Equal(t, 816, usage.OutputTokens, "message_delta 的最终 output_tokens 必须被记录")
}

// TestAntigravityClientWriter 验证 antigravityClientWriter 的断开检测
func TestAntigravityClientWriter(t *testing.T) {
	t.Run("normal write succeeds", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		flusher, _ := c.Writer.(http.Flusher)
		cw := newAntigravityClientWriter(c.Writer, flusher, "test")

		ok := cw.Write([]byte("hello"))
		require.True(t, ok)
		require.False(t, cw.Disconnected())
		require.Contains(t, rec.Body.String(), "hello")
	})

	t.Run("write failure marks disconnected", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		fw := &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
		flusher, _ := c.Writer.(http.Flusher)
		cw := newAntigravityClientWriter(fw, flusher, "test")

		ok := cw.Write([]byte("hello"))
		require.False(t, ok)
		require.True(t, cw.Disconnected())
	})

	t.Run("subsequent writes are no-op", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		fw := &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
		flusher, _ := c.Writer.(http.Flusher)
		cw := newAntigravityClientWriter(fw, flusher, "test")

		cw.Write([]byte("first"))
		ok := cw.Fprintf("second %d", 2)
		require.False(t, ok)
		require.True(t, cw.Disconnected())
	})
}

// TestUnwrapV1InternalResponse 测试 unwrapV1InternalResponse 的各种输入场景
func TestUnwrapV1InternalResponse(t *testing.T) {
	svc := &AntigravityGatewayService{}

	// 构造 >50KB 的大型 JSON
	largePadding := strings.Repeat("x", 50*1024)
	largeInput := []byte(fmt.Sprintf(`{"response":{"id":"big","pad":"%s"}}`, largePadding))
	largeExpected := fmt.Sprintf(`{"id":"big","pad":"%s"}`, largePadding)

	tests := []struct {
		name     string
		input    []byte
		expected string
		wantErr  bool
	}{
		{
			name:     "正常 response 包装",
			input:    []byte(`{"response":{"id":"123","content":"hello"}}`),
			expected: `{"id":"123","content":"hello"}`,
		},
		{
			name:     "无 response 透传",
			input:    []byte(`{"id":"456"}`),
			expected: `{"id":"456"}`,
		},
		{
			name:     "空 JSON",
			input:    []byte(`{}`),
			expected: `{}`,
		},
		{
			name:     "response 为 null",
			input:    []byte(`{"response":null}`),
			expected: `null`,
		},
		{
			name:     "response 为基础类型 string",
			input:    []byte(`{"response":"hello"}`),
			expected: `"hello"`,
		},
		{
			name:     "非法 JSON",
			input:    []byte(`not json`),
			expected: `not json`,
		},
		{
			name:     "嵌套 response 只解一层",
			input:    []byte(`{"response":{"response":{"inner":true}}}`),
			expected: `{"response":{"inner":true}}`,
		},
		{
			name:     "大型 JSON >50KB",
			input:    largeInput,
			expected: largeExpected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.unwrapV1InternalResponse(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, strings.TrimSpace(string(got)))
		})
	}
}

// --- unwrapV1InternalResponse benchmark 对照组 ---

// unwrapV1InternalResponseOld 旧实现：Unmarshal+Marshal 双重开销（仅用于 benchmark 对照）
func unwrapV1InternalResponseOld(body []byte) ([]byte, error) {
	var outer map[string]any
	if err := json.Unmarshal(body, &outer); err != nil {
		return nil, err
	}
	if resp, ok := outer["response"]; ok {
		return json.Marshal(resp)
	}
	return body, nil
}

func BenchmarkUnwrapV1Internal_Old_Small(b *testing.B) {
	body := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"hello world"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = unwrapV1InternalResponseOld(body)
	}
}

func BenchmarkUnwrapV1Internal_New_Small(b *testing.B) {
	body := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"hello world"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}}`)
	svc := &AntigravityGatewayService{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.unwrapV1InternalResponse(body)
	}
}

func BenchmarkUnwrapV1Internal_Old_Large(b *testing.B) {
	body := generateLargeUnwrapJSON(10 * 1024) // ~10KB
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = unwrapV1InternalResponseOld(body)
	}
}

func BenchmarkUnwrapV1Internal_New_Large(b *testing.B) {
	body := generateLargeUnwrapJSON(10 * 1024) // ~10KB
	svc := &AntigravityGatewayService{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.unwrapV1InternalResponse(body)
	}
}

// generateLargeUnwrapJSON 生成指定最小大小的包含 response 包装的 JSON
func generateLargeUnwrapJSON(minSize int) []byte {
	parts := make([]map[string]string, 0)
	current := 0
	for current < minSize {
		text := fmt.Sprintf("这是第 %d 段内容，用于填充 JSON 到目标大小。", len(parts)+1)
		parts = append(parts, map[string]string{"text": text})
		current += len(text) + 20 // 估算 JSON 编码开销
	}
	inner := map[string]any{
		"candidates": []map[string]any{
			{"content": map[string]any{"parts": parts}},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     100,
			"candidatesTokenCount": 50,
		},
	}
	outer := map[string]any{"response": inner}
	b, _ := json.Marshal(outer)
	return b
}

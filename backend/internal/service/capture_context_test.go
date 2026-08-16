package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCaptureDecisionShortCircuitsOpenAIBeforeBufferAllocation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)

	_, ok := CaptureDecisionFor(c, "openai", CaptureOutcomeSuccess)
	require.False(t, ok)
	require.False(t, CaptureMayApplyFor(c, "openai"))
	_, exists := c.Get(captureResultContextKey)
	require.False(t, exists)
}

func TestCaptureDecisionRequiresBothRequestScopeFilters(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.GroupIDs = []int64{7}
	policy.UserIDs = []int64{9}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	group := int64(7)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCompiledCaptureScopeForTest(c, compiled, 9, &group)
	content, ok := CaptureDecisionFor(c, "openai", CaptureOutcomeTerminalError)
	require.True(t, ok)
	require.Equal(t, policy.Content, content)

	otherUser, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCompiledCaptureScopeForTest(otherUser, compiled, 8, &group)
	_, ok = CaptureDecisionFor(otherUser, "openai", CaptureOutcomeSuccess)
	require.False(t, ok)
}

func TestPrepareCaptureScopeFailsClosedForNilOrFailedSettingService(t *testing.T) {
	for _, settingService := range []*SettingService{
		nil,
		NewSettingService(&capturePolicyRepoStub{getErr: context.DeadlineExceeded}, nil),
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		PrepareCapturePolicyScope(context.Background(), c, settingService, 9, nil)
		_, ok := CaptureDecisionFor(c, "anthropic", CaptureOutcomeSuccess)
		require.False(t, ok)
	}
}

func TestPrepareCaptureScopeStaticDisabledDoesNotReadRepository(t *testing.T) {
	repo := &capturePolicyRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	start := time.Now()
	PrepareCapturePolicyScope(context.Background(), c, svc, 9, nil)
	require.Less(t, time.Since(start), 50*time.Millisecond)
	gets, _ := repo.calls()
	require.Zero(t, gets)
	require.False(t, CaptureMayApplyFor(c, PlatformAnthropic))
}

func TestPrepareCaptureScopeColdCacheRefreshesWithoutBlockingForwarding(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	encoded, err := json.Marshal(policy)
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	repo := &capturePolicyRepoStub{value: string(encoded), getStarted: started, getRelease: release}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	svc := NewSettingService(repo, cfg)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	start := time.Now()
	PrepareCapturePolicyScope(context.Background(), c, svc, 9, nil)
	require.Less(t, time.Since(start), 50*time.Millisecond)
	require.False(t, CaptureMayApplyFor(c, PlatformAnthropic), "cold cache must fail closed")

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background policy refresh did not start")
	}
	for i := 0; i < 100; i++ {
		repeated, _ := gin.CreateTestContext(httptest.NewRecorder())
		PrepareCapturePolicyScope(context.Background(), repeated, svc, 9, nil)
		require.False(t, CaptureMayApplyFor(repeated, PlatformAnthropic))
	}
	gets, _ := repo.calls()
	require.Equal(t, 1, gets, "cold-cache requests must share one background refresh")
	close(release)
	require.Eventually(t, func() bool {
		entry, _ := svc.captureRuntimePolicyCache.Load().(*cachedCaptureRuntimePolicy)
		return entry != nil && entry.compiled.Enabled()
	}, time.Second, 10*time.Millisecond)

	warm, _ := gin.CreateTestContext(httptest.NewRecorder())
	PrepareCapturePolicyScope(context.Background(), warm, svc, 9, nil)
	require.True(t, CaptureMayApplyFor(warm, PlatformAnthropic))
}

func TestPrepareCaptureScopeExpiredCacheServesStaleWithoutBlocking(t *testing.T) {
	oldPolicy := DefaultCaptureRuntimePolicy()
	oldPolicy.Enabled = true
	oldEntry := newCaptureRuntimePolicySuccessEntry(oldPolicy)
	oldEntry.expiresAt = time.Now().Add(-time.Second).UnixNano()

	newPolicy := oldPolicy
	newPolicy.Enabled = false
	encoded, err := json.Marshal(newPolicy)
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	repo := &capturePolicyRepoStub{value: string(encoded), getStarted: started, getRelease: release}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	svc := NewSettingService(repo, cfg)
	svc.captureRuntimePolicyCache.Store(oldEntry)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	start := time.Now()
	PrepareCapturePolicyScope(context.Background(), c, svc, 9, nil)
	require.Less(t, time.Since(start), 50*time.Millisecond)
	require.True(t, CaptureMayApplyFor(c, PlatformAnthropic), "expired cache must serve stale while refreshing")

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background policy refresh did not start")
	}
	close(release)
}

func TestApplyCaptureContentPolicyKeepsMetadataAndClearsDisabledFields(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest:      []byte(`{"metadata":{"user_id":"{\"device_id\":\"device-a\",\"session_id\":\"session-a\"}"}}`),
		RawResponse:     []byte(`{"stop_reason":"end_turn","usage":{"output_tokens":4}}`),
		RequestHeaders:  []byte(`{"Anthropic-Version":["2023-06-01"]}`),
		ResponseHeaders: []byte(`{"X-Request-Id":["req-a"]}`),
	}
	extractCaptureColumns(rec)
	ApplyCaptureContentPolicy(rec, CaptureContentPolicy{})

	require.Equal(t, "session-a", rec.SessionID)
	require.Equal(t, "end_turn", rec.StopReason)
	require.Equal(t, 4, rec.OutputTokens)
	require.Empty(t, rec.RawRequest)
	require.Empty(t, rec.RawResponse)
	require.Empty(t, rec.RequestHeaders)
	require.Empty(t, rec.ResponseHeaders)
}

func TestCaptureExchangeBridgeKeepsFinalOutboundAndRawUpstreamSnapshots(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, err := http.NewRequest(http.MethodPost, "https://api.example.test/v1/responses", bytes.NewReader(nil))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("OpenAI-Beta", "responses=v1")
	body := []byte(`{"model":"mapped-model"}`)

	SetCaptureOutboundRequest(c, req, body, 1024)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Request-Id": []string{"upstream-a"}},
		Request:    req,
	}
	setCaptureResult(c, resp, []byte(`{"id":"resp-a"}`), false)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, body, bridge.UpstreamRequest)
	require.Equal(t, "https://api.example.test/v1/responses", bridge.UpstreamEndpoint)
	require.Equal(t, 200, bridge.HTTPStatus)
	require.JSONEq(t, `{"Openai-Beta":["responses=v1"]}`, string(bridge.RequestHeaders))
	require.NotContains(t, string(bridge.RequestHeaders), "secret")
	require.JSONEq(t, `{"id":"resp-a"}`, string(bridge.Response))
}

func TestPrepareCaptureScopeBeginsAndStoresTypedAttemptSynchronously(t *testing.T) {
	transport := &recordingCaptureTransport{}
	begin := testCaptureBegin()

	ctx, attempt := PrepareCaptureScope(context.Background(), transport, begin)
	require.NotNil(t, attempt)
	require.Equal(t, 1, transport.Begins())
	require.Same(t, attempt, captureAttemptFromContext(ctx))
	require.Equal(t, begin.CaptureID, attempt.ID())
}

func TestPrepareCaptureScopeReturnsOriginalContextWhenAdmissionFails(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: context.DeadlineExceeded}
	begin := model.Begin{CaptureID: uuid.New()}
	parent := context.Background()

	ctx, attempt := PrepareCaptureScope(parent, transport, begin)
	require.Nil(t, attempt)
	require.Equal(t, parent, ctx)
	require.Equal(t, 1, transport.Begins())
}

func TestCaptureOutboundRequestStreamsExistingWireSliceAndSanitizedHeaders(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	SetCaptureRequestedModel(c, "client-model")
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	body := []byte(`{"model":"mapped-model","stream":true}`)
	svc := &GatewayService{cfg: captureEnabledConfigForTest(32 << 20), capturePool: pool}

	svc.captureOutboundRequest(c, &Account{Platform: PlatformAnthropic}, req, body)

	require.Equal(t, 1, transport.Begins())
	recording := transport.Attempts()[0]
	require.Equal(t, body, recording.RequestBytes())
	require.Len(t, recording.RequestInputs(), 1)
	require.Equal(t, &body[0], &recording.RequestInputs()[0][0], "capture must frame the existing wire slice without cloning it")
	require.NotContains(t, string(recording.requestHeaders), "secret")
	require.Contains(t, string(recording.requestHeaders), "Anthropic-Version")
	require.Equal(t, "client-model", recording.begin.RequestedModel)
	require.Equal(t, "mapped-model", recording.begin.UpstreamModel)
	require.True(t, recording.begin.Stream)
	require.Same(t, captureAttemptForRequest(c), captureAttemptForRequest(c))
}

func TestCaptureOutboundRequestAbortsPriorRetryAttempt(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{cfg: captureEnabledConfigForTest(32 << 20), capturePool: pool}
	account := &Account{Platform: PlatformAnthropic}

	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://first.test/v1/messages", nil), []byte(`{"attempt":1}`))
	first := transport.Attempts()[0]
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://second.test/v1/messages", nil), []byte(`{"attempt":2}`))

	require.Equal(t, 2, transport.Begins())
	require.Equal(t, []captureTerminalState{captureAborted}, first.TerminalStates())
	require.Empty(t, transport.Attempts()[1].TerminalStates())
	require.Equal(t, []byte(`{"attempt":2}`), transport.Attempts()[1].RequestBytes())
}

func TestCaptureOutboundRequestPolicyOffRetryAbortsPriorTypedAttempt(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.Anthropic = true
	policy.Platforms.Gemini = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{cfg: captureEnabledConfigForTest(32 << 20), capturePool: pool}

	svc.captureOutboundRequest(c, &Account{Platform: PlatformAnthropic}, httptest.NewRequest(http.MethodPost, "https://first.test/v1/messages", nil), []byte(`{"attempt":1}`))
	require.Equal(t, 1, transport.Begins())
	first := transport.Attempts()[0]
	require.True(t, CaptureUsesStreamingAttempt(c))

	svc.captureOutboundRequest(c, &Account{Platform: PlatformGemini}, httptest.NewRequest(http.MethodPost, "https://second.test/v1/messages", nil), []byte(`{"attempt":2}`))

	require.Equal(t, 1, transport.Begins(), "a policy-off retry must not begin another capture")
	require.Equal(t, []captureTerminalState{captureAborted}, first.TerminalStates())
	require.False(t, CaptureUsesStreamingAttempt(c), "a policy-off retry must clear the stale typed owner")
}

func TestGatewayResponseTeeStreamsOnlyConsumedBytesIntoCurrentAttempt(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	cfg := captureEnabledConfigForTest(32 << 20)
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
	svc := &GatewayService{cfg: cfg, capturePool: pool}
	account := &Account{Platform: PlatformAnthropic}
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	svc.captureOutboundRequest(c, account, req, []byte(`{"model":"mapped"}`))
	source := &trackingCaptureReadCloser{reader: strings.NewReader("abcdef")}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"upstream-id"}, "Set-Cookie": []string{"secret"}},
		Body:       source,
		Request:    req,
	}

	finish := svc.beginGatewayCaptureResponse(c, account, resp)
	buf := make([]byte, 3)
	_, err = io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	finish()

	recording := transport.Attempts()[0]
	require.Equal(t, []byte("abc"), recording.ResponseBytes())
	require.Equal(t, 3, source.bytesRead)
	require.Contains(t, string(recording.responseHeaders), "X-Request-Id")
	require.NotContains(t, string(recording.responseHeaders), "secret")
	require.Empty(t, recording.TerminalStates(), "response finalization must not own commit")
}

func TestTypedBeginCaptureResponsePreservesSuccessfulUpstreamHTTPStatus(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{cfg: captureEnabledConfigForTest(32 << 20), capturePool: pool}
	account := &Account{Platform: PlatformAnthropic}
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	svc.captureOutboundRequest(c, account, req, []byte(`{"model":"mapped"}`))
	resp := &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: req}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	finish()

	require.True(t, CommitForwardCaptureAttempt(c, PlatformAnthropic, &ForwardResult{}))
	require.Equal(t, []model.Final{{HTTPStatus: http.StatusCreated, ResponseComplete: true}}, transport.Attempts()[0].Finals())
}

func TestGatewayStreamingAttemptDoesNotPublishLegacyWholeBodyBridge(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{
		cfg:              captureEnabledConfigForTest(32 << 20),
		capturePool:      pool,
		rateLimitService: &RateLimitService{},
	}
	account := &Account{ID: 1, Platform: PlatformAnthropic}
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	svc.captureOutboundRequest(c, account, req, []byte(`{"model":"mapped","stream":true}`))
	raw := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":3}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw)), Request: req}
	svc.beginGatewayCaptureResponse(c, account, resp)

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "mapped", "mapped", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge, "streaming attempt must not publish a second whole-response bridge")
	require.Equal(t, []byte(raw), transport.Attempts()[0].ResponseBytes())
	AbortCaptureAttempt(c)
}

func TestGatewayFailedSynchronousAdmissionDoesNotFallbackToLegacyBridge(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: context.DeadlineExceeded}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{
		cfg:              captureEnabledConfigForTest(32 << 20),
		capturePool:      pool,
		rateLimitService: &RateLimitService{},
	}
	account := &Account{ID: 1, Platform: PlatformAnthropic}
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	svc.captureOutboundRequest(c, account, req, []byte(`{"model":"mapped","stream":true}`))
	require.Equal(t, 1, transport.Begins())
	require.Nil(t, captureAttemptForRequest(c))
	raw := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":3}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw)), Request: req}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "mapped", "mapped", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge, "failed typed admission must fail open without allocating the retired whole-body bridge")
}

func TestTypedAdmissionFailureCanTransitionToLaterLegacyOwner(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: context.DeadlineExceeded}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	typedReq := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	_, ok := beginCaptureAttemptForWireRequest(c.Request.Context(), c, pool, PlatformOpenAI, typedReq, []byte(`{"model":"typed"}`), 1<<20)
	require.False(t, ok)
	require.True(t, captureStreamingAttemptPath(c), "the failed typed owner must not fall back within the same attempt")

	legacyReq := httptest.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", nil)
	setCapturePlatform(c, PlatformGrok)
	SetCaptureOutboundRequest(c, legacyReq, []byte(`{"model":"grok"}`), 1<<20)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("legacy-response")), Request: legacyReq}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	finish()

	bridge, exists := takeCaptureResult(c)
	require.True(t, exists)
	require.Equal(t, []byte("legacy-response"), bridge.Response)
	require.False(t, captureStreamingAttemptPath(c), "a later explicit legacy owner must replace the failed typed owner")
}

func TestTypedRetryCanTransitionToLaterLegacyOwnerAndAbortsTypedAttempt(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	typedReq := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	_, ok := beginCaptureAttemptForWireRequest(c.Request.Context(), c, pool, PlatformOpenAI, typedReq, []byte(`{"model":"typed"}`), 1<<20)
	require.True(t, ok)
	typed := transport.Attempts()[0]

	legacyReq := httptest.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", nil)
	setCapturePlatform(c, PlatformGrok)
	SetCaptureOutboundRequest(c, legacyReq, []byte(`{"model":"grok"}`), 1<<20)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("legacy-final")), Request: legacyReq}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	finish()

	require.Equal(t, []captureTerminalState{captureAborted}, typed.TerminalStates())
	bridge, exists := takeCaptureResult(c)
	require.True(t, exists)
	require.Equal(t, []byte("legacy-final"), bridge.Response)
	require.False(t, captureStreamingAttemptPath(c))
}

func TestGatewayNonStreamingAttemptDoesNotPublishLegacyWholeBodyBridge(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &GatewayService{
		cfg:              captureEnabledConfigForTest(32 << 20),
		capturePool:      pool,
		rateLimitService: &RateLimitService{},
	}
	account := &Account{ID: 1, Platform: PlatformAnthropic}
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.test/v1/messages", nil)
	svc.captureOutboundRequest(c, account, req, []byte(`{"model":"mapped","stream":false}`))
	raw := []byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"mapped","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":7}}`)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw)), Request: req}
	svc.beginGatewayCaptureResponse(c, account, resp)

	result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, account, "mapped", "mapped")

	require.NoError(t, err)
	require.NotNil(t, result)
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge, "non-streaming attempt must not publish a second whole-response bridge")
	require.Equal(t, raw, transport.Attempts()[0].ResponseBytes())
	AbortCaptureAttempt(c)
}

func newFinalAttemptFixture(t *testing.T, policy CaptureRuntimePolicy) (*gin.Context, *GatewayService, *recordingCaptureTransport, *Account) {
	t.Helper()
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	cfg := captureEnabledConfigForTest(32 << 20)
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
	return c, &GatewayService{cfg: cfg, capturePool: pool}, transport, &Account{Platform: PlatformAnthropic}
}

func TestFinalAttemptRetryAbortsOldAndCommitsClientVisibleAttempt(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	firstBody := []byte(`{"attempt":1}`)
	secondBody := []byte(`{"attempt":2}`)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://first.test/v1/messages", nil), firstBody)
	first := transport.Attempts()[0]
	require.True(t, captureAttemptForRequest(c).WriteResponse([]byte("first response")))
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://second.test/v1/messages", nil), secondBody)
	second := transport.Attempts()[1]
	require.True(t, captureAttemptForRequest(c).WriteResponse([]byte("client-visible response")))

	require.True(t, CommitCaptureAttempt(c, PlatformAnthropic, CaptureOutcomeSuccess, model.Final{HTTPStatus: 200, ResponseComplete: true}))

	require.Equal(t, []captureTerminalState{captureAborted}, first.TerminalStates())
	require.Equal(t, []captureTerminalState{captureCommitted}, second.TerminalStates())
	require.Equal(t, secondBody, second.RequestBytes())
	require.Equal(t, []byte("client-visible response"), second.ResponseBytes())
}

func TestFinalAttemptRealUpstreamErrorCommits(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil), []byte(`{"model":"mapped"}`))
	require.True(t, captureAttemptForRequest(c).WriteResponse([]byte(`{"error":"unavailable"}`)))

	require.True(t, CommitCaptureAttempt(c, PlatformAnthropic, CaptureOutcomeTerminalError, model.Final{HTTPStatus: 503, ResponseComplete: true}))

	recording := transport.Attempts()[0]
	require.Equal(t, []captureTerminalState{captureCommitted}, recording.TerminalStates())
	require.Equal(t, []model.Final{{HTTPStatus: 503, ResponseComplete: true}}, recording.Finals())
}

func TestFinalAttemptLocalSyntheticErrorAborts(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil), []byte(`{"model":"mapped"}`))

	AbortCaptureAttempt(c)
	AbortCaptureAttempt(c)

	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[0].TerminalStates())
}

func TestPreCommitDisconnectCommitsIncompleteAttemptWithReason(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil), []byte(`{"model":"mapped","stream":true}`))
	require.True(t, captureAttemptForRequest(c).WriteResponse([]byte("partial")))

	require.True(t, CommitCapturePreCommitDisconnect(c, PlatformAnthropic, model.Final{HTTPStatus: 200}))

	recording := transport.Attempts()[0]
	require.Equal(t, []captureTerminalState{captureCommitted}, recording.TerminalStates())
	require.Equal(t, []model.Final{{HTTPStatus: 200, StopReason: "pre_commit_disconnect", ResponseComplete: false}}, recording.Finals())
}

func TestFinalAttemptOutcomePolicyOffAbortsWithoutCommit(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil), []byte(`{"model":"mapped"}`))

	require.False(t, CommitCaptureAttempt(c, PlatformAnthropic, CaptureOutcomeSuccess, model.Final{HTTPStatus: 200, ResponseComplete: true}))

	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[0].TerminalStates())
	require.Empty(t, transport.Attempts()[0].Finals())
}

func TestFinalAttemptGatewaySideEffectSinkCommitsUsageMetadata(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	c, svc, transport, account := newFinalAttemptFixture(t, policy)
	svc.captureOutboundRequest(c, account, httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil), []byte(`{"model":"mapped"}`))
	result := &ForwardResult{
		Usage: ClaudeUsage{InputTokens: 11, OutputTokens: 7, CacheReadInputTokens: 3, CacheCreationInputTokens: 2},
	}

	require.True(t, CommitForwardCaptureAttempt(c, string(account.Platform), result))

	require.Equal(t, []model.Final{{
		HTTPStatus:          200,
		InputTokens:         11,
		OutputTokens:        7,
		CacheReadTokens:     3,
		CacheCreationTokens: 2,
		ResponseComplete:    true,
	}}, transport.Attempts()[0].Finals())
	require.Equal(t, []captureTerminalState{captureCommitted}, transport.Attempts()[0].TerminalStates())
}

func TestPreCommitOpenAISideEffectSinkClassifiesDisconnect(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	c, _, transport, _ := newFinalAttemptFixture(t, policy)
	account := &Account{Platform: PlatformOpenAI}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	beginCaptureAttemptForWireRequest(c.Request.Context(), c, pool, PlatformOpenAI, httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil), []byte(`{"model":"gpt-5","stream":true}`), 1<<20)
	result := &OpenAIForwardResult{
		Usage:              OpenAIUsage{InputTokens: 13, OutputTokens: 5, CacheReadInputTokens: 4, CacheCreationInputTokens: 1},
		ClientDisconnect:   true,
		UpstreamHTTPStatus: 200,
	}

	require.True(t, CommitOpenAIForwardCaptureAttempt(c, string(account.Platform), result))

	require.Equal(t, []model.Final{{
		HTTPStatus:          200,
		InputTokens:         13,
		OutputTokens:        5,
		CacheReadTokens:     4,
		CacheCreationTokens: 1,
		StopReason:          "pre_commit_disconnect",
		ResponseComplete:    false,
	}}, transport.Attempts()[0].Finals())
	require.Equal(t, []captureTerminalState{captureCommitted}, transport.Attempts()[0].TerminalStates())
}

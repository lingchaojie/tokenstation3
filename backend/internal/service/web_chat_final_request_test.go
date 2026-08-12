//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type webChatGeminiFinalRequestRecorder struct {
	body     []byte
	response *http.Response
}

type webChatGeminiSequenceRecorder struct {
	responses []*http.Response
	bodies    [][]byte
}

func (r *webChatGeminiSequenceRecorder) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body := snapshotHTTPRequestBody(req)
	r.bodies = append(r.bodies, body)
	if len(r.responses) == 0 {
		return nil, io.EOF
	}
	resp := r.responses[0]
	r.responses = r.responses[1:]
	return resp, nil
}

func (r *webChatGeminiSequenceRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return r.Do(req, proxyURL, accountID, concurrency)
}

func (r *webChatGeminiFinalRequestRecorder) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req != nil && req.Body != nil {
		r.body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(r.body))
	}
	if r.response != nil {
		return r.response, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(bytes.NewBufferString(
			`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1}}`,
		)),
	}, nil
}

func TestWebChatGeminiFinalHTTPErrorArchivesProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorBody := []byte(`{"error":{"code":400,"message":"gemini rejected final request","status":"INVALID_ARGUMENT"}}`)
	recorder := &webChatGeminiFinalRequestRecorder{response: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-gemini-final-error"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	gateway := &GatewayService{cfg: cfg, capturePool: pool}
	gemini := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: cfg}
	account := &Account{
		ID: 703, Name: "webchat-gemini-final-error", Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-provider-secret"},
	}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformGemini, Status: StatusActive}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}
	double.geminiCompatService = gemini
	c := newTestGinContext(context.Background())

	result, err := double.dispatchChatCompletions(c, webChatDispatchInput{
		User:           &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
		ConversationID: 7, AssistantMessageID: 101, Model: "gemini-2.5-flash", Provider: "gemini",
		Capabilities: WebChatModelCapability{Provider: "gemini", Platform: PlatformGemini, Model: "gemini-2.5-flash", SupportsText: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
	})
	pool.Stop()

	require.Error(t, err)
	require.Nil(t, result, "terminal HTTP errors remain non-billable")
	require.Len(t, writer.records, 1, "one final provider attempt must be archived")
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, recorder.body, record.RawRequest)
	require.NotContains(t, string(record.RequestHeaders), "gemini-provider-secret")
}

func TestWebChatGeminiRetryArchivesOnlyFinalProviderHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	intermediateBody := []byte(`{"error":{"code":503,"message":"retry me","status":"UNAVAILABLE"}}`)
	finalBody := []byte(`{"error":{"code":400,"message":"final rejection","status":"INVALID_ARGUMENT"}}`)
	recorder := &webChatGeminiSequenceRecorder{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(intermediateBody))},
		{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}, "X-Goog-Request-Id": {"rid-gemini-final-after-retry"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 3)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	gateway := &GatewayService{cfg: cfg, capturePool: pool}
	gemini := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: cfg}
	account := &Account{ID: 707, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "secret"}}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformGemini, Status: StatusActive}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}
	double.geminiCompatService = gemini

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User:           &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
		ConversationID: 7, AssistantMessageID: 101, Model: "gemini-2.5-flash", Provider: "gemini",
		Capabilities: WebChatModelCapability{Provider: "gemini", Platform: PlatformGemini, Model: "gemini-2.5-flash", SupportsText: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
	})
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()

	require.Len(t, recorder.bodies, 2)
	require.Len(t, writer.records, 1, "intermediate retry must never be archived")
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.NotEqual(t, intermediateBody, record.RawResponse)
	require.Equal(t, recorder.bodies[1], record.RawRequest)
}

func TestWebChatGeminiSuccessArchivesProviderNativeResponseBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	providerBody := []byte(`{"candidates":[{"content":{"parts":[{"text":"hello from raw gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":4}}`)
	recorder := &webChatGeminiFinalRequestRecorder{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Goog-Request-Id": {"rid-gemini-success"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	gateway := &GatewayService{cfg: cfg, capturePool: pool}
	gemini := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: cfg}
	account := &Account{
		ID: 705, Name: "webchat-gemini-success", Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-provider-secret"},
	}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformGemini, Status: StatusActive}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}
	double.geminiCompatService = gemini

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User:           &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
		ConversationID: 7, AssistantMessageID: 101, Model: "gemini-2.5-flash", Provider: "gemini",
		Capabilities: WebChatModelCapability{Provider: "gemini", Platform: PlatformGemini, Model: "gemini-2.5-flash", SupportsText: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	pool.Stop()

	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, providerBody, record.RawResponse, "archive must retain Gemini-native JSON rather than converted Chat Completions JSON")
	require.Equal(t, recorder.body, record.RawRequest)
}

func (r *webChatGeminiFinalRequestRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return r.Do(req, proxyURL, accountID, concurrency)
}

func TestGeminiChatCompletionsCompatCarriesFinalProviderRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &webChatGeminiFinalRequestRecorder{}
	svc := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: &config.Config{Gateway: config.GatewayConfig{
		Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
	}}}
	account := &Account{
		ID: 601, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-key"},
	}
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	enableCaptureForTest(t, c)

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, recorder.body)
	require.Equal(t, recorder.body, result.UpstreamRequest)
	require.NotEqual(t, body, result.UpstreamRequest, "Gemini compat converts Chat Completions into a provider-native request")
}

func TestCaptureUpstreamRequestKeepsImmutableFinalAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)

	first := []byte(`{"attempt":1}`)
	firstReq, err := http.NewRequest(http.MethodPost, "https://provider.invalid", bytes.NewReader(first))
	require.NoError(t, err)
	setCaptureUpstreamRequest(c, firstReq, 1024)

	finalBody := []byte(`{"attempt":2}`)
	finalReq, err := http.NewRequest(http.MethodPost, "https://provider.invalid", bytes.NewReader(finalBody))
	require.NoError(t, err)
	setCaptureUpstreamRequest(c, finalReq, 1024)
	finalBody[0] = 'x'

	result := finalizeForwardResult(c, &ForwardResult{})
	require.Equal(t, `{"attempt":2}`, string(result.UpstreamRequest))
	require.NotEqual(t, first, result.UpstreamRequest)
}

func TestWebChatSingleAccountFinalFailoverableHTTPErrorArchivesExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("anthropic chat completions", func(t *testing.T) {
		errorBody := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"final overload"}}`)
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-anthropic-final-503"}},
			Body:       io.NopCloser(bytes.NewReader(errorBody)),
		}}
		writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
		pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
		cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
		gateway := &GatewayService{cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, capturePool: pool}
		account := &Account{ID: 711, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
			"api_key": "secret", "base_url": "https://api.anthropic.com",
		}}
		double := newWebChatServiceWithStubs(t)
		double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive}}
		double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

		result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
			User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
			AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
			Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true},
			Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
		})
		require.Error(t, err)
		require.Nil(t, result, "final failoverable HTTP errors must remain non-billable")
		pool.Stop()

		require.Len(t, writer.records, 1, "the one-account WebChat boundary owns the final failoverable response")
		record := <-writer.records
		require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
		require.Equal(t, errorBody, record.RawResponse)
		require.Equal(t, upstream.lastBody, record.RawRequest)
	})

	t.Run("anthropic responses", func(t *testing.T) {
		errorBody := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"final responses rate limit"}}`)
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-responses-final-429"}},
			Body:       io.NopCloser(bytes.NewReader(errorBody)),
		}}
		writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
		pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
		cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
		gateway := &GatewayService{cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, capturePool: pool}
		account := &Account{ID: 712, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
			"api_key": "secret", "base_url": "https://api.anthropic.com",
		}}
		double := newWebChatServiceWithStubs(t)
		double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive}}
		double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

		result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
			User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
			AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
			Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true, SupportsWebSearch: true},
			Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
			WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: false},
		})
		require.Error(t, err)
		require.Nil(t, result)
		pool.Stop()

		require.Len(t, writer.records, 1)
		record := <-writer.records
		require.Equal(t, http.StatusTooManyRequests, record.HTTPStatus)
		require.Equal(t, errorBody, record.RawResponse)
		require.Equal(t, upstream.lastBody, record.RawRequest)
	})

	t.Run("gemini compat", func(t *testing.T) {
		errorBody := []byte(`{"error":{"code":503,"message":"final gemini overload","status":"UNAVAILABLE"}}`)
		recorder := &webChatGeminiFinalRequestRecorder{response: &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": {"application/json"}, "X-Goog-Request-Id": {"rid-gemini-final-503"}},
			Body:       io.NopCloser(bytes.NewReader(errorBody)),
		}}
		writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
		pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
		cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
		gateway := &GatewayService{cfg: cfg, capturePool: pool}
		gemini := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: cfg, rateLimitService: &RateLimitService{}}
		account := &Account{ID: 713, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
			"api_key": "secret", "pool_mode": true,
		}}
		double := newWebChatServiceWithStubs(t)
		double.availableGroups = []Group{{ID: 11, Platform: PlatformGemini, Status: StatusActive}}
		double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}
		double.geminiCompatService = gemini

		result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
			User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
			AssistantMessageID: 101, Model: "gemini-2.5-flash", Provider: "gemini",
			Capabilities: WebChatModelCapability{Provider: "gemini", Platform: PlatformGemini, Model: "gemini-2.5-flash", SupportsText: true},
			Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
		})
		require.Error(t, err)
		require.Nil(t, result)
		pool.Stop()

		require.Len(t, writer.records, 1)
		record := <-writer.records
		require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
		require.Equal(t, errorBody, record.RawResponse)
		require.Equal(t, recorder.body, record.RawRequest)
	})

	t.Run("kiro", func(t *testing.T) {
		originalRetrySleep := kiroRetrySleep
		kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
		t.Cleanup(func() { kiroRetrySleep = originalRetrySleep })
		errorBody := []byte(`{"message":"final kiro overload"}`)
		responses := make([]*http.Response, 3)
		for i := range responses {
			responses[i] = &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(errorBody))}
		}
		recorder := &webChatGeminiSequenceRecorder{responses: responses}
		writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
		pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
		cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
		gateway := &GatewayService{cfg: cfg, httpUpstream: recorder, tlsFPProfileService: &TLSFingerprintProfileService{}, kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool}
		account := &Account{ID: 714, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
			"access_token": "secret", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		}}
		double := newWebChatServiceWithStubs(t)
		double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, KiroEndpointMode: KiroEndpointModeKRS}}
		double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

		result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
			User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
			AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
			Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true},
			Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
		})
		require.Error(t, err)
		require.Nil(t, result)
		pool.Stop()

		require.Len(t, recorder.bodies, 3, "KIRO retries remain inside the one selected WebChat account")
		require.Len(t, writer.records, 1, "only the terminal KIRO attempt is archived")
		record := <-writer.records
		require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
		require.Equal(t, errorBody, record.RawResponse)
		require.Equal(t, recorder.bodies[2], record.RawRequest)
	})
}

func TestWebChatFinalHTTPErrorCaptureUsesConfiguredCeiling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name      string
		bodySize  int
		wantSize  int
		truncated bool
	}{
		{name: "retains response above safe logging limit", bodySize: 600 << 10, wantSize: 600 << 10},
		{name: "marks response above archive ceiling", bodySize: (8 << 20) + 1024, wantSize: 8 << 20, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := `{"type":"error","error":{"message":"reject"},"padding":"`
			suffix := `"}`
			body := []byte(prefix + strings.Repeat("x", tc.bodySize-len(prefix)-len(suffix)) + suffix)
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}}
			writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
			pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
			gateway := &GatewayService{cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, capturePool: pool}
			account := &Account{ID: 715, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
				"api_key": "secret", "base_url": "https://api.anthropic.com",
			}}
			double := newWebChatServiceWithStubs(t)
			double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive}}
			double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

			result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
				User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
				AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
				Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true},
				Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
			})
			require.Error(t, err)
			require.Nil(t, result)
			pool.Stop()

			require.Len(t, writer.records, 1)
			record := <-writer.records
			require.Len(t, record.RawResponse, tc.wantSize)
			require.Equal(t, tc.truncated, record.Truncated)
			if !tc.truncated {
				require.Equal(t, body[len(body)-32:], record.RawResponse[len(record.RawResponse)-32:])
			}
		})
	}
}

func TestWebChatGeminiFinalHTTPErrorRetainsBodyAboveSafeLoggingLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prefix := `{"error":{"code":400,"message":"reject"},"padding":"`
	suffix := `"}`
	body := []byte(prefix + strings.Repeat("g", (600<<10)-len(prefix)-len(suffix)) + suffix)
	recorder := &webChatGeminiFinalRequestRecorder{response: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	gateway := &GatewayService{cfg: cfg, capturePool: pool}
	gemini := &GeminiMessagesCompatService{httpUpstream: recorder, cfg: cfg}
	account := &Account{ID: 717, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "secret"}}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformGemini, Status: StatusActive}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}
	double.geminiCompatService = gemini

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
		AssistantMessageID: 101, Model: "gemini-2.5-flash", Provider: "gemini",
		Capabilities: WebChatModelCapability{Provider: "gemini", Platform: PlatformGemini, Model: "gemini-2.5-flash", SupportsText: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: false,
	})
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()

	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Len(t, record.RawResponse, len(body))
	require.Equal(t, body[len(body)-32:], record.RawResponse[len(record.RawResponse)-32:])
	require.False(t, record.Truncated)
}

func TestNormalGatewayFailoverableHTTPErrorDoesNotUseWebChatTerminalSink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorBody := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"handler failover"}}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	svc := &GatewayService{cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, capturePool: pool}
	account := &Account{ID: 718, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
		"api_key": "secret", "base_url": "https://api.anthropic.com",
	}}
	inbound := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(inbound))

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, inbound, &ParsedRequest{
		Body: NewRequestBodyRef(inbound), Model: "claude-sonnet-4", Stream: false,
	})
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()

	require.Len(t, writer.records, 0, "normal handler failover attempts remain owned by the handler loop")
}

func TestWebChatKiroOnlyWebSearchArchivesFinalAWSRequestAndResponsePair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })

	mcpBody := []byte(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`)
	var providerBody bytes.Buffer
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "answer from kiro web search"},
	}))
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 8, "outputTokens": 5}},
	}))
	rawProviderBody := snapshotBytes(providerBody.Bytes())
	recorder := &webChatGeminiSequenceRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(mcpBody))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": {"rid-kiro-websearch"}}, Body: io.NopCloser(bytes.NewReader(rawProviderBody))},
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	gateway := &GatewayService{cfg: cfg, httpUpstream: recorder, tlsFPProfileService: &TLSFingerprintProfileService{}, kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool}
	account := &Account{ID: 716, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
		"access_token": "secret", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST", "region": "us-east-1",
	}}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, KiroEndpointMode: KiroEndpointModeKRS}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
		AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
		Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true, SupportsWebSearch: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "current events"}}, Stream: true,
		WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: true},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	pool.Stop()

	require.Len(t, recorder.bodies, 2, "MCP request then final AWS runtime request")
	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, recorder.bodies[1], record.RawRequest, "archive must pair the final runtime request, not the intermediate MCP request")
	require.NotEqual(t, recorder.bodies[0], record.RawRequest)
	require.Equal(t, rawProviderBody, record.RawResponse, "archive must tee provider-native AWS event-stream bytes")
}

func TestWebChatKiroOnlyWebSearchFinalHTTPErrorArchivesNativeTerminalAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })
	originalRetrySleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { kiroRetrySleep = originalRetrySleep })

	mcpBody := []byte(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`)
	intermediateBody := []byte(`{"message":"temporary Kiro web-search overload"}`)
	finalBody := []byte(`{"message":"final Kiro web-search overload"}`)
	recorder := &webChatGeminiSequenceRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(mcpBody))},
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(intermediateBody))},
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(intermediateBody))},
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Requestid": {"rid-kiro-websearch-final-503"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 3)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	gateway := &GatewayService{cfg: cfg, httpUpstream: recorder, tlsFPProfileService: &TLSFingerprintProfileService{}, kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool}
	account := &Account{ID: 719, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
		"access_token": "secret", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST", "region": "us-east-1",
	}}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, KiroEndpointMode: KiroEndpointModeKRS}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
		AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
		Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true, SupportsWebSearch: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "current events"}}, Stream: true,
		WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: true},
	})
	require.Error(t, err)
	require.Nil(t, result, "a final native AWS HTTP error remains non-billable")
	pool.Stop()

	require.Len(t, recorder.bodies, 4, "one MCP request and three AWS runtime attempts")
	require.Len(t, writer.records, 1, "only the final native AWS attempt may be archived")
	record := <-writer.records
	require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
	require.Equal(t, recorder.bodies[3], record.RawRequest)
	require.NotEqual(t, recorder.bodies[0], record.RawRequest)
	require.Equal(t, finalBody, record.RawResponse)
	require.NotEqual(t, intermediateBody, record.RawResponse)
}

func TestWebChatKiroOnlyWebSearchFinal429RetainsNativeBodyAboveSafeLogLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })

	for _, tc := range []struct {
		name          string
		bodySize      int
		wantSize      int
		wantTruncated bool
	}{
		{name: "retains 600 KiB exactly", bodySize: 600 << 10, wantSize: 600 << 10},
		{name: "bounds body above 8 MiB", bodySize: (8 << 20) + 1024, wantSize: 8 << 20, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpBody := []byte(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`)
			prefix := `{"message":"final Kiro web-search rate limit","padding":"`
			suffix := `"}`
			finalBody := []byte(prefix + strings.Repeat("r", tc.bodySize-len(prefix)-len(suffix)) + suffix)
			recorder := &webChatGeminiSequenceRecorder{responses: []*http.Response{
				{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(mcpBody))},
				{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Requestid": {"rid-kiro-websearch-final-429"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
			}}
			writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
			pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
			gateway := &GatewayService{cfg: cfg, httpUpstream: recorder, tlsFPProfileService: &TLSFingerprintProfileService{}, kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool}
			account := &Account{ID: 720, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
				"access_token": "secret", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST", "region": "us-east-1",
			}}
			double := newWebChatServiceWithStubs(t)
			double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, KiroEndpointMode: KiroEndpointModeKRS}}
			double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

			result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
				User: &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}, ConversationID: 7,
				AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
				Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true, SupportsWebSearch: true},
				Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "current events"}}, Stream: true,
				WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: true},
			})
			require.Error(t, err)
			require.Nil(t, result)
			pool.Stop()

			require.Len(t, recorder.bodies, 2, "one MCP request and one terminal AWS runtime request")
			require.Len(t, writer.records, 1)
			record := <-writer.records
			require.Equal(t, http.StatusTooManyRequests, record.HTTPStatus)
			require.Equal(t, recorder.bodies[1], record.RawRequest)
			require.Equal(t, finalBody[:tc.wantSize], record.RawResponse, "429 restoration must preserve bytes up to the WebChat ceiling")
			require.Equal(t, tc.wantTruncated, record.Truncated)
		})
	}
}

func TestWebChatAnthropicChatFinalHTTPErrorArchivesProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorBody := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"provider rejected final request"}}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-anthropic-final-error"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	svc := &GatewayService{
		cfg:                 cfg,
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
		capturePool:         pool,
	}
	account := &Account{
		ID: 701, Name: "webchat-anthropic-final-error", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "provider-secret", "base_url": "https://api.anthropic.com",
			"model_mapping": map[string]any{"claude-sonnet-4": "claude-sonnet-4-provider"},
		},
	}
	inbound := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/messages", bytes.NewReader(inbound))
	enableCaptureForTest(t, c)
	webChatCtx := withWebChatFinalGatewayErrorCaptureSubmitter(
		withWebChatStreamCapture(c.Request.Context(), newWebChatStreamCapture(8<<20)), svc,
	)

	result, err := svc.ForwardAsChatCompletions(webChatCtx, c, account, inbound, &ParsedRequest{
		Body: NewRequestBodyRef(inbound), Model: "claude-sonnet-4", Stream: false,
	})
	pool.Stop()

	require.Error(t, err)
	require.Nil(t, result, "terminal HTTP errors remain non-billable")
	require.Len(t, writer.records, 1, "one final provider attempt must be archived")
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, upstream.lastBody, record.RawRequest)
	require.NotEqual(t, inbound, record.RawRequest)
	require.Equal(t, "claude-sonnet-4-provider", gjson.GetBytes(record.RawRequest, "model").String())
	require.NotContains(t, string(record.RequestHeaders), "provider-secret")
}

func TestWebChatAnthropicResponsesFinalHTTPErrorArchivesProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorBody := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"responses provider rejected final request"}}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-responses-final-error"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	svc := &GatewayService{
		cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, capturePool: pool,
	}
	account := &Account{
		ID: 702, Name: "webchat-responses-final-error", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "provider-secret", "base_url": "https://api.anthropic.com",
			"model_mapping": map[string]any{"claude-sonnet-4": "claude-sonnet-4-provider"},
		},
	}
	inbound := []byte(`{"model":"claude-sonnet-4","input":"hello","stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/messages", bytes.NewReader(inbound))
	enableCaptureForTest(t, c)
	webChatCtx := withWebChatFinalGatewayErrorCaptureSubmitter(
		withWebChatStreamCapture(c.Request.Context(), newWebChatStreamCapture(8<<20)), svc,
	)

	result, err := svc.ForwardAsResponses(webChatCtx, c, account, inbound, &ParsedRequest{
		Body: NewRequestBodyRef(inbound), Model: "claude-sonnet-4", Stream: false,
	})
	pool.Stop()

	require.Error(t, err)
	require.Nil(t, result, "terminal HTTP errors remain non-billable")
	require.Len(t, writer.records, 1, "one final provider attempt must be archived")
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, upstream.lastBody, record.RawRequest)
	require.NotEqual(t, inbound, record.RawRequest)
	require.Equal(t, "claude-sonnet-4-provider", gjson.GetBytes(record.RawRequest, "model").String())
}

func TestWebChatKiroFinalHTTPErrorArchivesFinalProviderPayloadExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorBody := []byte(`{"message":"kiro rejected final payload","reason":"invalid_request"}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Amzn-Requestid": {"rid-kiro-final-error"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	svc := &GatewayService{
		cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{},
		kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool,
	}
	account := &Account{
		ID: 704, Name: "webchat-kiro-final-error", Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-provider-secret",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
			"model_mapping": map[string]any{
				"claude-sonnet-4": "claude-sonnet-4",
			},
		},
	}
	inbound := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/messages", bytes.NewReader(inbound))
	enableCaptureForTest(t, c)
	webChatCtx := withWebChatFinalGatewayErrorCaptureSubmitter(
		withWebChatStreamCapture(c.Request.Context(), newWebChatStreamCapture(8<<20)), svc,
	)

	result, err := svc.ForwardAsChatCompletions(webChatCtx, c, account, inbound, &ParsedRequest{
		Body: NewRequestBodyRef(inbound), Model: "claude-sonnet-4", Stream: false,
		Group: &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS},
	})
	pool.Stop()

	require.Error(t, err)
	require.Nil(t, result, "terminal HTTP errors remain non-billable")
	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, upstream.lastBody, record.RawRequest)
	require.NotEqual(t, inbound, record.RawRequest)
	require.True(t, gjson.GetBytes(record.RawRequest, "conversationState").Exists(), "archive must contain the final Kiro provider envelope")
	require.NotContains(t, string(record.RequestHeaders), "kiro-provider-secret")
}

func TestWebChatKiroRetryArchivesOnlyFinalProviderHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalRetrySleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { kiroRetrySleep = originalRetrySleep })
	intermediateBody := []byte(`{"message":"temporary kiro failure"}`)
	finalBody := []byte(`{"message":"final kiro rejection"}`)
	recorder := &webChatGeminiSequenceRecorder{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(intermediateBody))},
		{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Requestid": {"rid-kiro-final-after-retry"}}, Body: io.NopCloser(bytes.NewReader(finalBody))},
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 3)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20}}}
	svc := &GatewayService{
		cfg: cfg, httpUpstream: recorder, tlsFPProfileService: &TLSFingerprintProfileService{},
		kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool,
	}
	account := &Account{ID: 708, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
		"access_token": "secret", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		"model_mapping": map[string]any{"claude-sonnet-4": "claude-sonnet-4"},
	}}
	inbound := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/messages", bytes.NewReader(inbound))
	enableCaptureForTest(t, c)
	webChatCtx := withWebChatFinalGatewayErrorCaptureSubmitter(
		withWebChatStreamCapture(c.Request.Context(), newWebChatStreamCapture(8<<20)), svc,
	)

	result, err := svc.ForwardAsChatCompletions(webChatCtx, c, account, inbound, &ParsedRequest{
		Body: NewRequestBodyRef(inbound), Model: "claude-sonnet-4", Stream: false,
		Group: &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS},
	})
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()

	require.Len(t, recorder.bodies, 2)
	require.Len(t, writer.records, 1, "intermediate Kiro retry must never be archived")
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, finalBody, record.RawResponse)
	require.NotEqual(t, intermediateBody, record.RawResponse)
	require.Equal(t, recorder.bodies[1], record.RawRequest)
}

func TestWebChatKiroSuccessArchivesProviderNativeEventStreamBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var providerBody bytes.Buffer
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello from raw kiro"},
	}))
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 7,
			"outputTokens":        4,
		}},
	}))
	rawProviderBody := snapshotBytes(providerBody.Bytes())
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": {"rid-kiro-success"}},
		Body:       io.NopCloser(bytes.NewReader(rawProviderBody)),
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 8 << 20},
	}}
	gateway := &GatewayService{
		cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{},
		kiroCooldownStore: &stubKiroCooldownStore{}, capturePool: pool,
	}
	account := &Account{
		ID: 706, Name: "webchat-kiro-success", Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-provider-secret",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
			"model_mapping": map[string]any{
				"claude-sonnet-4": "claude-sonnet-4",
			},
		},
	}
	double := newWebChatServiceWithStubs(t)
	double.availableGroups = []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, KiroEndpointMode: KiroEndpointModeKRS}}
	double.gatewayService = &webChatRealGatewayHarness{GatewayService: gateway, account: account}

	result, err := double.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User:           &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
		ConversationID: 7, AssistantMessageID: 101, Model: "claude-sonnet-4", Provider: "anthropic",
		Capabilities: WebChatModelCapability{Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4", SupportsText: true},
		Messages:     []WebChatMessage{{Role: WebChatRoleUser, ContentText: "hello"}}, Stream: true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	pool.Stop()

	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, rawProviderBody, record.RawResponse, "archive must retain AWS event-stream bytes rather than translated Anthropic/WebChat SSE")
	require.Equal(t, upstream.lastBody, record.RawRequest)
}

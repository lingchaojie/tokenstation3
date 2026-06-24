package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountKiroCredentialsPreferCliproxyPlusKeys(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"accessToken":        "fallback-access",
			"profileArn":         "fallback-profile",
			"access_token":       "plus-access",
			"refresh_token":      "plus-refresh",
			"profile_arn":        "plus-profile",
			"client_id":          "kiro-client",
			"client_secret":      "kiro-secret",
			"preferred_endpoint": "amazonq",
		},
	}

	require.True(t, account.IsKiro())
	require.True(t, account.IsOpenAICompatible())
	require.Equal(t, "plus-access", account.GetKiroAccessToken())
	require.Equal(t, "plus-refresh", account.GetKiroRefreshToken())
	require.Equal(t, "plus-profile", account.GetKiroProfileARN())
	require.Equal(t, "kiro-client", account.GetKiroClientID())
	require.Equal(t, "kiro-secret", account.GetKiroClientSecret())
	require.Equal(t, "amazonq", account.GetKiroPreferredEndpoint())
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
	require.True(t, IsOpenAICompatiblePlatform(PlatformKiro))
	require.Equal(t, PlatformKiro, NormalizeOpenAICompatiblePlatform(PlatformKiro))
	require.True(t, IsAllowedQuotaPlatform(PlatformKiro))
}

func TestKiroCredentialKeysAreSensitive(t *testing.T) {
	t.Parallel()

	require.True(t, IsSensitiveCredentialKey("access_token"))
	require.True(t, IsSensitiveCredentialKey("refresh_token"))
	require.True(t, IsSensitiveCredentialKey("client_secret"))
}

func TestResolveKiroModelID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"kiro-auto":                       "auto",
		"kiro-claude-sonnet-4-5":          "claude-sonnet-4.5",
		"kiro-claude-sonnet-4-5-20250929": "claude-sonnet-4.5",
		"kiro-claude-opus-4-5":            "claude-opus-4.5",
		"kiro-claude-haiku-4-5":           "claude-haiku-4.5",
		"kiro-claude-sonnet-4-5-agentic":  "claude-sonnet-4.5",
		"claude-sonnet-4-5":               "claude-sonnet-4.5",
		"anthropic/claude-sonnet-4-5":     "claude-sonnet-4.5",
		"unknown-but-contains-haiku":      "claude-haiku-4.5",
		"unknown-but-contains-opus":       "claude-opus-4.5",
		"unknown":                         "unknown",
	}

	for input, want := range tests {
		input := input
		want := want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, ResolveKiroModelID(input))
		})
	}
}

func TestParseKiroEventStream(t *testing.T) {
	t.Parallel()

	stream := bytes.Join([][]byte{
		buildKiroEventStreamMessageForTest("assistantResponseEvent", `{"assistantResponseEvent":{"content":"Hello ","toolUses":[{"toolUseId":"toolu_1","name":"TodoWrite","input":{"todos":[]}}]}}`),
		buildKiroEventStreamMessageForTest("assistantResponseEvent", `{"assistantResponseEvent":{"content":"world"}}`),
		buildKiroEventStreamMessageForTest("messageMetadataEvent", `{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":7,"cacheReadInputTokens":2,"outputTokens":3,"totalTokens":12}}}`),
		buildKiroEventStreamMessageForTest("messageStopEvent", `{"stopReason":"tool_use"}`),
	}, nil)

	parsed, err := parseKiroEventStream(bytes.NewReader(stream))
	require.NoError(t, err)
	require.Equal(t, "Hello world", parsed.Content)
	require.Equal(t, OpenAIUsage{InputTokens: 9, OutputTokens: 3, CacheReadInputTokens: 2}, parsed.Usage)
	require.Equal(t, "tool_use", parsed.StopReason)
	require.Len(t, parsed.ToolUses, 1)
	require.Equal(t, "toolu_1", parsed.ToolUses[0].ID)
	require.Equal(t, "TodoWrite", parsed.ToolUses[0].Name)
	require.JSONEq(t, `{"todos":[]}`, string(parsed.ToolUses[0].Input))
}

func TestForwardAsAnthropic_KiroUsesCodeWhispererEndpointAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"kiro-claude-sonnet-4-5","max_tokens":1024,"stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := bytes.Join([][]byte{
		buildKiroEventStreamMessageForTest("assistantResponseEvent", `{"assistantResponseEvent":{"content":"ok"}}`),
		buildKiroEventStreamMessageForTest("messageMetadataEvent", `{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":7,"outputTokens":3,"totalTokens":10}}}`),
		buildKiroEventStreamMessageForTest("messageStopEvent", `{"stopReason":"end_turn"}`),
	}, nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-amzn-requestid": []string{"rid_kiro"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          303,
		Name:        "kiro-pass",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "kiro-access",
			"refresh_token":      "kiro-refresh",
			"auth_method":        "kiro-cli",
			"profile_arn":        "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
			"base_url":           "https://codewhisperer.test.local",
			"preferred_endpoint": "codewhisperer",
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "kiro-claude-sonnet-4-5", result.Model)
	require.Equal(t, "kiro-claude-sonnet-4-5", result.BillingModel)
	require.Equal(t, "claude-sonnet-4.5", result.UpstreamModel)
	require.Equal(t, OpenAIUsage{InputTokens: 7, OutputTokens: 3}, result.Usage)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://codewhisperer.test.local/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer kiro-access", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, KiroContentType, upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, KiroAcceptStream, upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, KiroCodeWhispererTarget, upstream.lastReq.Header.Get("X-Amz-Target"))
	require.Equal(t, "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererruntime/0.1.14474 os/linux lang/rust/1.92.0 md/appVersion-2.0.0 app/AmazonQ-For-CLI", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererruntime/0.1.14474 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI", upstream.lastReq.Header.Get("X-Amz-User-Agent"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Amz-Sdk-Invocation-Id"))
	require.Equal(t, "attempt=1; max=1", upstream.lastReq.Header.Get("Amz-Sdk-Request"))
	require.Equal(t, 0, upstream.plainCallCount)
	require.Equal(t, 1, upstream.tlsCallCount)
	require.Nil(t, upstream.lastTLSProfile)
	require.Equal(t, "vibe", gjson.GetBytes(upstream.lastBody, "conversationState.agentTaskType").String())
	require.Equal(t, "claude-sonnet-4.5", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "CLI", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.content").String())
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/test", gjson.GetBytes(upstream.lastBody, "profileArn").String())
	require.JSONEq(t, `{"id":"","type":"message","role":"assistant","model":"kiro-claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`, rec.Body.String())
}

func TestForwardResponses_KiroConvertsResponsesInputToCodeWhisperer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"kiro-claude-sonnet-4-5","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := bytes.Join([][]byte{
		buildKiroEventStreamMessageForTest("assistantResponseEvent", `{"assistantResponseEvent":{"content":"ok"}}`),
		buildKiroEventStreamMessageForTest("messageMetadataEvent", `{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":4,"outputTokens":2,"totalTokens":6}}}`),
		buildKiroEventStreamMessageForTest("messageStopEvent", `{"stopReason":"end_turn"}`),
	}, nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-amzn-requestid": []string{"rid_kiro_resp"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          304,
		Name:        "kiro-pass",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "kiro-access",
			"refresh_token":      "kiro-refresh",
			"auth_method":        "kiro-cli",
			"profile_arn":        "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
			"base_url":           "https://codewhisperer.test.local",
			"preferred_endpoint": "codewhisperer",
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://codewhisperer.test.local/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.content").String())
	require.Equal(t, "claude-sonnet-4.5", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, OpenAIUsage{InputTokens: 4, OutputTokens: 2}, result.Usage)
	require.Equal(t, "kiro-claude-sonnet-4-5", result.Model)
	require.Equal(t, "kiro-claude-sonnet-4-5", result.BillingModel)
	require.Equal(t, "claude-sonnet-4.5", result.UpstreamModel)
}

func TestApplyKiroRuntimeHeadersUsesKiroIDEFingerprintForNonCLIAuth(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse", nil)
	account := &Account{
		ID:       305,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "kiro-access",
			"refresh_token": "stable-refresh",
			"client_id":     "stable-client",
			"auth_method":   "social",
		},
	}

	applyKiroRuntimeHeaders(req, account, "kiro-access")

	require.Equal(t, "Bearer kiro-access", req.Header.Get("Authorization"))
	require.Contains(t, req.Header.Get("X-Amz-User-Agent"), "aws-sdk-js/1.0.0 KiroIDE-")
	require.Contains(t, req.Header.Get("User-Agent"), "api/codewhispererruntime#1.0.0")
	require.Contains(t, req.Header.Get("User-Agent"), " KiroIDE-")
	require.NotEmpty(t, req.Header.Get("Amz-Sdk-Invocation-Id"))
	require.Equal(t, "attempt=1; max=1", req.Header.Get("Amz-Sdk-Request"))
}

func TestKiroRequestOriginMatchesPlusCLIResolution(t *testing.T) {
	t.Parallel()

	cliAccount := &Account{
		Platform:    PlatformKiro,
		Credentials: map[string]any{"auth_method": "kiro-cli"},
	}
	require.Equal(t, "CLI", kiroRequestOriginForAccount(cliAccount, "AI_EDITOR"))
	require.Equal(t, "CLI", kiroRequestOriginForAccount(cliAccount, "CLI"))

	socialAccount := &Account{
		Platform:    PlatformKiro,
		Credentials: map[string]any{"auth_method": "social"},
	}
	require.Equal(t, "AI_EDITOR", kiroRequestOriginForAccount(socialAccount, "AI_EDITOR"))
	require.Equal(t, "CLI", kiroRequestOriginForAccount(socialAccount, "AMAZON_Q"))
}

func TestBuildKiroUsageLimitsURLMatchesPlusProfileAndAuthMethod(t *testing.T) {
	t.Parallel()

	profileARN := "arn:aws:codewhisperer:ap-southeast-1:123456789012:profile/ABCDEF"
	account := &Account{
		ID:       306,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "kiro-access",
			"refresh_token": "kiro-refresh",
			"profile_arn":   profileARN,
			"auth_method":   "kiro-cli",
		},
	}

	targetURL, err := buildKiroUsageLimitsURL(account)
	require.NoError(t, err)
	parsed, err := url.Parse(targetURL)
	require.NoError(t, err)
	require.Equal(t, "https://codewhisperer.ap-southeast-1.amazonaws.com/getUsageLimits", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	require.Equal(t, profileARN, parsed.Query().Get("profileArn"))
	require.Equal(t, "KIRO_CLI", parsed.Query().Get("origin"))
	require.Equal(t, "AGENTIC_REQUEST", parsed.Query().Get("resourceType"))
	require.Empty(t, parsed.Query().Get("isEmailRequired"))
}

func TestParseKiroUsageLimitsPayloadPrefersPrecisionFields(t *testing.T) {
	t.Parallel()

	info, err := parseKiroUsageLimitsPayload([]byte(`{
		"daysUntilReset": 3,
		"usageBreakdownList": [
			{
				"resourceType": "AGENTIC_REQUEST",
				"usageLimit": 100,
				"currentUsage": 10,
				"usageLimitWithPrecision": 100.5,
				"currentUsageWithPrecision": 12.25
			}
		]
	}`))

	require.NoError(t, err)
	require.Equal(t, 100.5, info.UsageLimit)
	require.Equal(t, 12.25, info.CurrentUsage)
	require.InDelta(t, 12.189, info.Utilization, 0.001)
}

func buildKiroEventStreamMessageForTest(eventType string, payload string) []byte {
	var headers bytes.Buffer
	_ = headers.WriteByte(byte(len(":event-type")))
	_, _ = headers.WriteString(":event-type")
	_ = headers.WriteByte(7)
	_ = binary.Write(&headers, binary.BigEndian, uint16(len(eventType)))
	_, _ = headers.WriteString(eventType)

	payloadBytes := []byte(payload)
	totalLen := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	var msg bytes.Buffer
	_ = binary.Write(&msg, binary.BigEndian, totalLen)
	_ = binary.Write(&msg, binary.BigEndian, uint32(headers.Len()))
	prelude := msg.Bytes()
	_ = binary.Write(&msg, binary.BigEndian, crc32.ChecksumIEEE(prelude))
	_, _ = msg.Write(headers.Bytes())
	_, _ = msg.Write(payloadBytes)
	_ = binary.Write(&msg, binary.BigEndian, crc32.ChecksumIEEE(msg.Bytes()))
	return msg.Bytes()
}

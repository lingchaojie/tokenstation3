package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
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
		"unknown":                         "claude-sonnet-4.5",
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
	require.Equal(t, KiroUserAgent, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, KiroFullUserAgent, upstream.lastReq.Header.Get("X-Amz-User-Agent"))
	require.Equal(t, "claude-sonnet-4.5", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "AI_EDITOR", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.origin").String())
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

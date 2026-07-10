//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExtractResponsesReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	got := ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"claude-sonnet-4.5","reasoning":{"effort":"HIGH"}}`))
	require.NotNil(t, got)
	require.Equal(t, "high", *got)

	maxGot := ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"deepseek-v4-pro","reasoning":{"effort":"max"}}`))
	require.NotNil(t, maxGot)
	require.Equal(t, "xhigh", *maxGot)

	require.Nil(t, ExtractResponsesReasoningEffortFromBody([]byte(`{"model":"claude-sonnet-4.5"}`)))
}

func TestHandleResponsesBufferedStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7,"_sub2api_kiro_credits":0.17}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.Contains(t, rec.Body.String(), `"cached_tokens":9`)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
}

func TestHandleResponsesStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8,"_sub2api_kiro_credits":0.23}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.InDelta(t, 0.23, result.Usage.KiroCredits, 0.000001)
	require.Contains(t, rec.Body.String(), `response.completed`)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
}

func TestHandleResponsesBufferedStreamingResponse_KiroMarkedFinalUsageClearsProvisionalTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := markedKiroFinalUsageAnthropicResponse("msg_responses_buffered_final")

	result, err := (&GatewayService{}).handleResponsesBufferedStreamingResponse(
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(120), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

func TestHandleResponsesStreamingResponse_KiroMarkedFinalUsageClearsProvisionalTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := markedKiroFinalUsageAnthropicResponse("msg_responses_stream_final")

	result, err := (&GatewayService{}).handleResponsesStreamingResponse(
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Contains(t, rec.Body.String(), `"input_tokens":120`)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

func TestForwardAsResponsesKiroDirectUsesKiroEndpointMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusForbidden, `{"message":"blocked"}`),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
		kiroCooldownStore:   &stubKiroCooldownStore{},
	}
	account := &Account{
		ID:          102,
		Name:        "kiro direct",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	parsed := &ParsedRequest{
		Model:  "claude-sonnet-4-6",
		Stream: false,
		Group:  &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","input":"hello","stream":false}`)

	_, _ = svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer kiro-access-token", upstream.requests[0].Header.Get("Authorization"))
}

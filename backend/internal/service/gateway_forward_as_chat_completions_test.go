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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExtractCCReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	t.Run("nested reasoning.effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning":{"effort":"HIGH"}}`))
		require.NotNil(t, got)
		require.Equal(t, "high", *got)
	})

	t.Run("flat reasoning_effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning_effort":"x-high"}`))
		require.NotNil(t, got)
		require.Equal(t, "xhigh", *got)
	})

	t.Run("DeepSeek max", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning_effort":"Max"}`))
		require.NotNil(t, got)
		require.Equal(t, "xhigh", *got)
	})

	t.Run("missing effort", func(t *testing.T) {
		require.Nil(t, extractCCReasoningEffortFromBody([]byte(`{"model":"gpt-5"}`)))
	})
}

func TestHandleCCBufferedFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "high"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"_sub2api_kiro_final_usage":true,"_sub2api_kiro_credits":0.17}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

// Kimi 等 Anthropic 兼容上游返回 SSE 紧凑格式（冒号后无空格），CC 桥此前按
// "event: " / "data: " 严格匹配会丢弃全部事件，最终报 "Upstream stream ended
// without a response"（#4653 同根因；#4657 只修了 /v1/responses 桥）。
func TestHandleCCBufferedFromAnthropic_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_c1","type":"message","role":"assistant","content":[],"model":"k3","stop_reason":null,"usage":{"input_tokens":15,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:content_block_stop`,
			`data:{"type":"content_block_stop","index":0}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "k3", "k3", nil, time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 15, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, 2, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `"OK"`, "紧凑格式事件必须被解析并产出响应内容")
}

func TestHandleCCStreamingFromAnthropic_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_c2","type":"message","role":"assistant","content":[],"model":"k3","stop_reason":null,"usage":{"input_tokens":21,"cache_read_input_tokens":6,"cache_creation_input_tokens":1}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:content_block_stop`,
			`data:{"type":"content_block_stop","index":0}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCStreamingFromAnthropic(context.Background(), resp, c, "k3", "k3", nil, time.Now(), true, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `[DONE]`)
}

func TestHandleCCStreamingFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "medium"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
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
	result, err := svc.handleCCStreamingFromAnthropic(context.Background(), resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now(), true, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.InDelta(t, 0.23, result.Usage.KiroCredits, 0.000001)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "medium", *result.ReasoningEffort)
	require.Contains(t, rec.Body.String(), `[DONE]`)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
}

func TestHandleCCBufferedFromAnthropic_KiroMarkedFinalUsageClearsProvisionalTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := markedKiroFinalUsageAnthropicResponse("msg_cc_buffered_final")

	result, err := (&GatewayService{}).handleCCBufferedFromAnthropic(
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(120), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int())
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

func TestHandleCCStreamingFromAnthropic_KiroMarkedFinalUsageClearsProvisionalTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := markedKiroFinalUsageAnthropicResponse("msg_cc_stream_final")

	result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
		context.Background(), resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, true,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Contains(t, rec.Body.String(), `"prompt_tokens":120`)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

func TestAnthropicToChatCompatibilityClientDisconnectCompleteAfterProviderTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
	resp := markedKiroFinalUsageAnthropicResponse("msg_cc_disconnect_complete")

	result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
		context.Background(), resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, true,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.CaptureResponseComplete)
}

func TestAnthropicToChatCompatibilityRejectsIncompleteProviderTailAfterTerminal(t *testing.T) {
	complete := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_tail","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	tails := map[string]string{
		"event without companion":       `event: content_block_delta`,
		"event with non-data companion": `event: content_block_delta` + "\n" + `: keepalive`,
	}

	for name, tail := range tails {
		t.Run("buffered/"+name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(complete + tail))}
			result, err := (&GatewayService{}).handleCCBufferedFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now(), false)
			require.Error(t, err)
			require.Nil(t, result)
		})
		t.Run("stream/"+name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(complete + tail))}
			result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(context.Background(), resp, c, "claude-test", "claude-test", nil, time.Now(), true, false)
			require.Error(t, err)
			require.NotNil(t, result)
			require.True(t, result.CaptureTerminalError)
		})
	}
}

func incompleteAnthropicCompatStreamPrefix() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_idle","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":2}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		``,
	}, "\n")
}

func TestAnthropicToChatCompatibilityHonorsProviderIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, streamed := range []bool{false, true} {
		name := "buffered"
		if streamed {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			body := newRawChatBlockingAfterPrefixReadCloser(incompleteAnthropicCompatStreamPrefix())
			resp := &http.Response{Body: body}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			if streamed {
				c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			}
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
				MaxLineSize:               defaultMaxLineSize,
				StreamDataIntervalTimeout: 1,
			}}}
			type outcome struct {
				result *ForwardResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				if streamed {
					result, err := svc.handleCCStreamingFromAnthropic(context.Background(), resp, c, "claude-test", "claude-test", nil, time.Now(), true, false)
					done <- outcome{result: result, err: err}
					return
				}
				result, err := svc.handleCCBufferedFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now(), false)
				done <- outcome{result: result, err: err}
			}()

			select {
			case got := <-done:
				if streamed {
					require.ErrorContains(t, got.err, "stream data interval timeout")
					require.NotNil(t, got.result)
					require.True(t, got.result.ClientDisconnect)
					require.False(t, got.result.CaptureResponseComplete)
					require.True(t, got.result.CaptureTerminalError)
				} else {
					require.Nil(t, got.result)
					var failoverErr *UpstreamFailoverError
					require.ErrorAs(t, got.err, &failoverErr)
					require.Contains(t, string(failoverErr.ResponseBody), "upstream stream read failed before message_stop")
				}
			case <-time.After(2 * time.Second):
				_ = body.Close()
				<-done
				t.Fatal("Anthropic-to-Chat compatibility stream ignored StreamDataIntervalTimeout")
			}
			require.NoError(t, body.Close())
		})
	}
}

func markedKiroFinalUsageAnthropicResponse(messageID string) *http.Response {
	return &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_kiro_marked_final"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"` + messageID + `","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":30,"output_tokens":0,"cache_read_input_tokens":60,"cache_creation_input_tokens":30}}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":120,"cache_creation_input_tokens":0,"_sub2api_kiro_final_usage":true}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}
}

func TestForwardAsChatCompletionsKiroDirectUsesKiroEndpointMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

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
		ID:          101,
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
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"stream":false}`)

	_, _ = svc.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)

	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer kiro-access-token", upstream.requests[0].Header.Get("Authorization"))
}

func TestForwardAsChatCompletionsKiroDirectSkipsClaudeCodeMimicry(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(string(accountType), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

			upstream := &queuedHTTPUpstream{responses: []*http.Response{
				newJSONResponse(http.StatusForbidden, `{"message":"blocked"}`),
			}}
			svc := &GatewayService{
				httpUpstream:        upstream,
				tlsFPProfileService: &TLSFingerprintProfileService{},
				kiroCooldownStore:   &stubKiroCooldownStore{},
			}
			account := &Account{
				ID: 102, Name: "kiro direct", Platform: PlatformKiro,
				Type: accountType, Concurrency: 1,
				Credentials: map[string]any{
					"access_token": "kiro-access-token",
					"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
				},
			}
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"lookup","parameters":{"type":"object"}}}],"stream":false}`)

			_, _ = svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
				Model: "claude-sonnet-4-6", Group: &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS},
			})

			require.Len(t, upstream.requests, 1)
			outbound, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(outbound, "metadata.user_id").Exists())
			require.False(t, gjson.GetBytes(outbound, "tools.0.cache_control").Exists())
			require.NotContains(t, string(outbound), strings.TrimSpace(claudeCodeSystemPrompt))
		})
	}
}

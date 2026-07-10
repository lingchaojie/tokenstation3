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

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardResponses_ForceChatCompletionsRoutesNonStreamingToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_chat_json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"image_tokens":5}}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, 4, int(gjson.Get(rec.Body.String(), "usage.input_tokens_details.cached_tokens").Int()))
	require.Equal(t, 6, int(gjson.Get(rec.Body.String(), "usage.input_tokens_details.cache_write_tokens").Int()))
	require.Equal(t, 5, int(gjson.Get(rec.Body.String(), "usage.output_tokens_details.image_tokens").Int()))
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 5, result.Usage.ImageOutputTokens)
	require.False(t, result.Stream)
}

func TestForwardResponses_ForceChatCompletionsRoutesStreamingToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"image_tokens":5}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_resp_chat_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"he"`)
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Contains(t, rec.Body.String(), `"input_tokens":12`)
	require.Contains(t, rec.Body.String(), `"cached_tokens":4`)
	require.Contains(t, rec.Body.String(), `"cache_write_tokens":6`)
	require.Contains(t, rec.Body.String(), `"image_tokens":5`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 5, result.Usage.ImageOutputTokens)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
}

func TestForwardResponses_DeepSeekReasoningOnlyStreamProducesVisibleText(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"visible fallback"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_deepseek_reasoning_responses_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"visible fallback"`)
	require.Contains(t, rec.Body.String(), `"status":"incomplete"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardResponses_AutoSupportedAccountStillUsesResponsesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_native"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_native","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
		openai_compat.ExtraKeyResponsesSupported: true,
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
}

func forceChatResponsesFallbackAccount() *Account {
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}
	return account
}

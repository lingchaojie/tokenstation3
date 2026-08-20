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
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func forceChatMessagesFallbackAccount() *Account {
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}
	return account
}

// errTailReader yields the given data, then returns err instead of io.EOF,
// simulating an upstream connection that breaks mid-stream.
type errTailReader struct {
	data []byte
	off  int
	err  error
}

func (r *errTailReader) Read(p []byte) (int, error) {
	if r.off < len(r.data) {
		n := copy(p, r.data[r.off:])
		r.off += n
		return n, nil
	}
	return 0, r.err
}

func (r *errTailReader) Close() error { return nil }

func TestForwardAsAnthropic_ForceChatCompletionsPreservesFinalModelReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		model      string
		mapped     string
		effortJSON string
		wantEffort string
		maxPolicy  string
	}{
		{
			name:       "policy caps converted effort",
			model:      "gpt-5.6-luna",
			mapped:     "gpt-5.6-luna",
			effortJSON: `,"output_config":{"effort":"max"}`,
			wantEffort: "medium",
			maxPolicy:  "medium",
		},
		{
			name:       "GPT56 max",
			model:      "luna",
			mapped:     "gpt-5.6-luna",
			effortJSON: `,"output_config":{"effort":"max"}`,
			wantEffort: "max",
		},
		{
			name:       "old model max",
			model:      "gpt-5.5",
			mapped:     "gpt-5.5",
			effortJSON: `,"output_config":{"effort":"max"}`,
			wantEffort: "xhigh",
		},
		{
			name:       "high remains high",
			model:      "gpt-5.6-luna",
			mapped:     "gpt-5.6-luna",
			effortJSON: `,"output_config":{"effort":"high"}`,
			wantEffort: "high",
		},
		{
			name:       "omitted defaults medium",
			model:      "gpt-5.6-luna",
			mapped:     "gpt-5.6-luna",
			wantEffort: "medium",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := `{"model":"` + tt.model + `","max_tokens":16,"messages":[{"role":"user","content":"hello"}]` + tt.effortJSON + `,"stream":false}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(body)))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chatcmpl_effort","object":"chat.completion","model":"` + tt.mapped + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
				)),
			}}
			account := forceChatMessagesFallbackAccount()
			account.Credentials["model_mapping"] = map[string]any{tt.model: tt.mapped}

			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			ctx := context.Background()
			if tt.maxPolicy != "" {
				ctx = WithOpenAIReasoningEffortPolicy(ctx, tt.maxPolicy, nil)
			}
			result, err := svc.ForwardAsAnthropic(ctx, c, account, []byte(body), "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.mapped, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, tt.wantEffort, gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, tt.wantEffort, *result.ReasoningEffort)
		})
	}
}

func TestForwardAsAnthropic_ForceChatCompletionsNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	enableCaptureForTest(t, c)

	upstreamBody := []byte(`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"input_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"image_tokens":2}}}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_msg_chat_json"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}}
	capture := newOpenAITypedCaptureTestHarness(t)
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		capturePool:  capture.pool,
	}
	svc.cfg.Gateway.Capture.Enabled = true
	svc.cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options").Exists() == false)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "assistant", gjson.Get(rec.Body.String(), "role").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "content.0.text").String())
	require.Equal(t, 2, int(gjson.Get(rec.Body.String(), "usage.input_tokens").Int()))
	require.Equal(t, 4, int(gjson.Get(rec.Body.String(), "usage.cache_read_input_tokens").Int()))
	require.Equal(t, 6, int(gjson.Get(rec.Body.String(), "usage.cache_creation_input_tokens").Int()))
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
	require.False(t, result.Stream)
	require.Nil(t, result.UpstreamRequest, "typed capture must not republish a legacy whole-body snapshot")
	require.Nil(t, result.CaptureResponse, "typed capture must not republish a legacy whole-body snapshot")
	capture.commit(t, c, result, upstream.lastBody, upstreamBody, false)
}

// Covers the fully-new streaming composition: text block is still open when
// [DONE] arrives, so finalization must close it (content_block_stop) before
// message_delta / message_stop.
func TestForwardAsAnthropic_ForceChatCompletionsStreamingClosesOpenBlockOnDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	enableCaptureForTest(t, c)

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_s","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_s","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_s","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_s","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_s","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_chat_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	capture := newOpenAITypedCaptureTestHarness(t)
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		capturePool:  capture.pool,
	}
	svc.cfg.Gateway.Capture.Enabled = true
	svc.cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())

	out := rec.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, `"text":"he"`)
	require.Contains(t, out, `"text":"llo"`)
	require.Contains(t, out, "event: content_block_stop")
	require.Contains(t, out, `"stop_reason":"end_turn"`)
	require.Contains(t, out, "event: message_stop")

	blockStop := strings.Index(out, "event: content_block_stop")
	msgDelta := strings.Index(out, `"stop_reason":"end_turn"`)
	msgStop := strings.Index(out, "event: message_stop")
	require.Greater(t, msgDelta, blockStop, "content_block_stop must precede message_delta")
	require.Greater(t, msgStop, msgDelta, "message_delta must precede message_stop")

	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.True(t, result.Stream)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureResponse)
	capture.commit(t, c, result, upstream.lastBody, []byte(upstreamBody), false)
	require.NotNil(t, result.FirstTokenMs)
}

// Covers multi-chunk tool_call fragments aggregated by index and finalized as
// an Anthropic tool_use block with stop_reason=tool_use.
func TestForwardAsAnthropic_ForceChatCompletionsStreamingToolCallAggregation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"weather in sf?"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_t","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_t","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_t","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"sf\"}"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_t","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: {"id":"chatcmpl_t","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":5,"total_tokens":11}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_chat_tool"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	svc.cfg.Gateway.Capture.Enabled = true
	svc.cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	out := rec.Body.String()
	require.Contains(t, out, `"type":"tool_use"`)
	require.Contains(t, out, `"name":"get_weather"`)
	require.Contains(t, out, `"input_json_delta"`)
	require.Contains(t, out, `"stop_reason":"tool_use"`)
	require.Contains(t, out, "event: message_stop")
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

// finish_reason=length must survive the double conversion (CC → Responses →
// Anthropic) as stop_reason=max_tokens.
func TestForwardAsAnthropic_ForceChatCompletionsStreamingLengthMapsToMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_l","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":"truncat"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_l","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":8,"total_tokens":12}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_chat_len"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	out := rec.Body.String()
	require.Contains(t, out, `"stop_reason":"max_tokens"`)
	require.Contains(t, out, "event: message_stop")
}

// A framing-only [DONE] remains protocol-compatible, but without upstream
// usage it must be recorded as a billing failure rather than a zero-cost success.
func TestForwardAsAnthropic_ForceChatCompletionsEmptyStreamReportsMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_chat_empty"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.ErrorIs(t, err, ErrOpenAIUpstreamUsageMissing)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.Contains(t, rec.Body.String(), "event: message_stop")
}

// Non-failover 4xx responses must go through the shared compat error handler:
// status-specific Anthropic error type, upstream message preserved, and ops
// upstream-error events recorded (previously this branch bypassed all three).
func TestForwardAsAnthropic_ForceChatCompletionsNonFailover400UsesSharedErrorHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_msg_chat_400"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid roles","type":"invalid_request_error"}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.Error(t, err)
	require.Nil(t, result)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "error", gjson.Get(rec.Body.String(), "type").String())
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "invalid roles", gjson.Get(rec.Body.String(), "error.message").String())

	statusVal, ok := c.Get(OpsUpstreamStatusCodeKey)
	require.True(t, ok, "shared handler must record the upstream status for ops")
	require.Equal(t, http.StatusBadRequest, statusVal)

	eventsVal, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok, "shared handler must append an ops upstream error event")
	events, castOK := eventsVal.([]*OpsUpstreamErrorEvent)
	require.True(t, castOK)
	require.Len(t, events, 1)
	require.Equal(t, http.StatusBadRequest, events[0].UpstreamStatusCode)
	require.Equal(t, "http_error", events[0].Kind)
	require.Equal(t, "invalid roles", events[0].Message)
}

// A broken upstream read mid-stream must surface an error and must NOT emit a
// synthetic message_stop that would disguise the truncation as a completion.
func TestForwardAsAnthropic_ForceChatCompletionsStreamReadErrorSkipsFinalize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	enableCaptureForTest(t, c)

	partial := strings.Join([]string{
		`data: {"id":"chatcmpl_e","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_e","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}]}`,
		"",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_chat_err"}},
		Body:       &errTailReader{data: []byte(partial), err: errors.New("simulated upstream read failure")},
	}}
	capture := newOpenAITypedCaptureTestHarness(t)
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		capturePool:  capture.pool,
	}
	svc.cfg.Gateway.Capture.Enabled = true
	svc.cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

	result, err := svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream usage incomplete")
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureResponse)
	capture.commit(t, c, result, upstream.lastBody, []byte(partial), true)

	out := rec.Body.String()
	require.Contains(t, out, `"text":"he"`, "delta emitted before the failure must reach the client")
	require.NotContains(t, out, "event: message_stop", "no synthetic completion after a broken read")
}

func TestForwardMessagesRawCCSkipsMalformedChunksAndRequiresUsage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		sse      string
		wantText string
	}{
		{
			name: "empty converted delta remains retryable",
			sse:  "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"\",\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"\",\"arguments\":\"\"}}]}}]}\n\n",
		},
		{
			name: "malformed before output is terminal and retryable",
			sse:  "data: {not-json}\n\ndata: [DONE]\n\n",
		},
		{
			name:     "malformed after converted text does not disconnect",
			sse:      "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {not-json}\n\ndata: [DONE]\n\n",
			wantText: `"text":"hello"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			enableCaptureForTest(t, c)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.sse))}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			capture := newOpenAITypedCaptureTestHarness(t)

			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: capture.pool}).ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
			require.ErrorIs(t, err, ErrOpenAIUpstreamUsageMissing)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.NotNil(t, result)
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			require.True(t, result.CaptureResponseComplete)
			if tt.wantText != "" {
				require.Contains(t, recorder.Body.String(), tt.wantText)
			}
			require.Nil(t, result.CaptureResponse)
			capture.commit(t, c, result, upstream.lastBody, []byte(tt.sse), false)
		})
	}
}

func TestForwardMessagesRawCCClientWriteFailureStillDrainsUsage(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	for _, tt := range []struct {
		name          string
		acceptedBytes int
	}{
		{name: "zero bytes accepted", acceptedBytes: 0},
		{name: "partial bytes accepted", acceptedBytes: 1024},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writer := &stagedConvertedFailingResponseWriter{
				ResponseWriter: c.Writer,
				accept:         tt.acceptedBytes,
				err:            errors.New("forced downstream write failure"),
			}
			c.Writer = writer
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(rawCCSpilledCommitFailureSSE())),
			}}

			result, err := (&OpenAIGatewayService{
				cfg:          rawChatCompletionsTestConfig(),
				httpUpstream: upstream,
			}).ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), requestBody, "", "")

			require.NoError(t, err)
			if tt.acceptedBytes == 0 {
				require.Zero(t, writer.wrote)
			} else {
				require.Positive(t, writer.wrote)
				require.LessOrEqual(t, writer.wrote, tt.acceptedBytes)
			}
			require.Equal(t, writer.wrote, recorder.Body.Len())
			require.NotNil(t, result)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NotNil(t, result.FirstTokenMs)
			require.True(t, result.ClientDisconnect)
		})
	}
}

func TestForwardMessagesRawCCCancellationClosesBody(t *testing.T) {
	for _, tt := range []struct {
		name      string
		payload   []byte
		committed bool
	}{
		{name: "before output"},
		{name: "after text", payload: []byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), committed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requestBody := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
			blocked := newBlockingAfterPayloadBody(tt.payload)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: blocked}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resultCh := make(chan *OpenAIForwardResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := svc.ForwardAsAnthropic(ctx, c, forceChatMessagesFallbackAccount(), requestBody, "", "")
				resultCh <- result
				errCh <- err
			}()
			select {
			case <-blocked.blocked:
			case <-time.After(time.Second):
				t.Fatal("upstream reader did not reach its blocking point")
			}
			cancel()
			var result *OpenAIForwardResult
			var err error
			select {
			case result = <-resultCh:
				err = <-errCh
			case <-time.After(time.Second):
				_ = blocked.Close()
				result = <-resultCh
				err = <-errCh
				t.Fatal("raw CC messages bridge did not terminate after cancellation")
			}
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.NotNil(t, result)
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			require.ErrorIs(t, err, context.Canceled)
			if tt.committed {
				require.Contains(t, recorder.Body.String(), "hello")
			}
			select {
			case <-blocked.closed:
			default:
				t.Fatal("raw CC messages bridge returned without closing upstream body")
			}
			require.Equal(t, int32(1), blocked.closes.Load(), "raw CC Messages bridge must close its upstream body exactly once")
		})
	}
}

func TestForwardMessagesRawCCFirstSemanticOverflowUsesCommittedOutputBoundary(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	firstSemantic := strings.Repeat("x", openAIFirstOutputStageMaxBytes-256)
	firstSemanticLine := `data: {"id":"x","choices":[{"delta":{"content":"` + firstSemantic + `"}}]}`
	require.Less(t, len(firstSemanticLine), openAIFirstOutputStageMaxBytes, "fixture must reach converted staging rather than the scanner-token guard")

	for _, tt := range []struct {
		name        string
		upstreamSSE string
		committed   bool
		wantOutput  string
	}{
		{name: "oversized first semantic converted event", upstreamSSE: firstSemanticLine + "\n\n", committed: true, wantOutput: firstSemantic[:128]},
		{
			name: "oversized newline-free token after committed text",
			upstreamSSE: "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"committed\"}}]}\n\n" +
				"data: " + strings.Repeat("x", openAIFirstOutputStageMaxBytes+1),
			committed:  true,
			wantOutput: "committed",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.upstreamSSE)),
			}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}).ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), requestBody, "", "")
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			if tt.committed {
				require.NotNil(t, result)
				require.False(t, errors.As(err, &failoverErr))
				require.Contains(t, recorder.Body.String(), tt.wantOutput)
				return
			}
			require.Nil(t, result)
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, -1, c.Writer.Size())
			require.Empty(t, recorder.Body.String())
		})
	}
}

// Gate regression: an API-key account whose upstream is confirmed to support
// the Responses API must keep using /v1/responses, never the CC fallback.
func TestForwardAsAnthropic_ResponsesSupportedAccountStillUsesResponsesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"high"},"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "third-party-client/1.0.0")
	c.Request.Header.Set("originator", "opencode")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_native","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_msg_native"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
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

	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "medium", nil)
	result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"),
		"responses-capable account must stay on /v1/responses, got %s", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "medium", *result.ReasoningEffort)
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.Equal(t, "third-party-client/1.0.0", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "opencode", upstream.lastReq.Header.Get("originator"))
	require.Empty(t, upstream.lastReq.Header.Get("version"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "content.0.text").String())
}

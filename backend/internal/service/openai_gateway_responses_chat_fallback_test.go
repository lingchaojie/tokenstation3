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
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
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
	enableCaptureForTest(t, c)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_chat_json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"image_tokens":2}}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	svc.cfg.Gateway.Capture.Enabled = true
	svc.cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

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
	require.Equal(t, 2, int(gjson.Get(rec.Body.String(), "usage.output_tokens_details.image_tokens").Int()))
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
	require.False(t, result.Stream)
}

func TestForwardResponses_PassthroughFlagWithUnsupportedResponsesUsesAccountMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		path := path
		t.Run(path, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4-channel","input":"hello","stream":false}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chatcmpl_mapping","object":"chat.completion","model":"gpt-5.4-account","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
				)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          rawChatCompletionsTestConfig(),
				httpUpstream: upstream,
			}
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{
				"gpt-5.4-channel": "gpt-5.4-account",
			}
			account.Credentials["compact_model_mapping"] = map[string]any{
				"gpt-5.4-account": "gpt-5.4-compact",
			}
			account.Extra = map[string]any{
				"openai_passthrough":                     true,
				openai_compat.ExtraKeyResponsesSupported: false,
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, "gpt-5.4-account", gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
}

func TestForwardResponses_ForceChatCompletionsRoutesStreamingToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	enableCaptureForTest(t, c)

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}],"usage":{"prompt_tokens":12,"input_tokens":12,"prompt_tokens_details":{"audio_tokens":2},"output_tokens_details":{"reasoning_tokens":7},"_sub2api_kiro_credits":0.17}}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"input_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"audio_tokens":3,"image_tokens":2,"accepted_prediction_tokens":4,"rejected_prediction_tokens":1},"_sub2api_kiro_credits":0}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_resp_chat_stream"}},
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
	require.Contains(t, rec.Body.String(), `"reasoning_tokens":7`)
	require.Contains(t, rec.Body.String(), `"audio_tokens":3`)
	require.Contains(t, rec.Body.String(), `"image_tokens":2`)
	require.Contains(t, rec.Body.String(), `"accepted_prediction_tokens":4`)
	require.Contains(t, rec.Body.String(), `"rejected_prediction_tokens":1`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
	require.Zero(t, result.Usage.KiroCredits)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureResponse)
	capture.commit(t, c, result, upstream.lastBody, []byte(upstreamBody), false)
}

func TestForwardResponsesRawCCDoesNotRequireDoneButRequiresUsage(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sse       string
		committed bool
	}{
		{"pre-output", "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n", false},
		{"committed", "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			enableCaptureForTest(t, c)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.sse))}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			capture := newOpenAITypedCaptureTestHarness(t)
			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: capture.pool}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
			if tt.committed {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Nil(t, result.UpstreamRequest)
				require.Nil(t, result.CaptureResponse)
				capture.commit(t, c, result, upstream.lastBody, []byte(tt.sse), false)
				require.Contains(t, recorder.Body.String(), `"delta":"hi"`)
			} else {
				require.ErrorIs(t, err, ErrOpenAIUpstreamUsageMissing)
				require.NotNil(t, result)
				require.True(t, result.UpstreamFailed)
				require.True(t, result.CaptureTerminalError)
				require.True(t, result.CaptureResponseComplete)
				var fo *UpstreamFailoverError
				require.False(t, errors.As(err, &fo))
				capture.commit(t, c, result, upstream.lastBody, []byte(tt.sse), false)
			}
		})
	}
}

func TestForwardResponsesRawCCSkipsMalformedChunksAndRequiresUsage(t *testing.T) {
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
			wantText: `"delta":"hello"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			enableCaptureForTest(t, c)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.sse))}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			capture := newOpenAITypedCaptureTestHarness(t)

			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: capture.pool}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
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

func rawCCSpilledCommitFailureSSE() string {
	largeText := strings.Repeat("s", openAIFirstOutputStageMemoryLimit+1024)
	return strings.Join([]string{
		`data: {"id":"x","choices":[{"delta":{"role":"assistant","content":"` + largeText + `"}}]}`,
		"",
		`data: {"id":"x","choices":[{"delta":{"content":"hello"}}]}`,
		"",
		`data: {"id":"x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
		"",
		"data: {not-json}",
		"",
	}, "\n")
}

func TestForwardResponsesRawCCClientWriteFailureStillDrainsUsage(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
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
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(rawCCSpilledCommitFailureSSE())),
			}}

			result, err := (&OpenAIGatewayService{
				cfg:          rawChatCompletionsTestConfig(),
				httpUpstream: upstream,
			}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), requestBody)

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
		})
	}
}

func TestForwardResponsesRawCCCancellationAndIdleCloseBody(t *testing.T) {
	for _, tt := range []struct {
		name      string
		payload   []byte
		committed bool
		cancel    bool
		idle      bool
	}{
		{name: "cancel before output", cancel: true},
		{name: "cancel after text", payload: []byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), committed: true, cancel: true},
		{name: "idle before output", idle: true},
		{name: "idle after text", payload: []byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), committed: true, idle: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requestBody := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
			blocked := newBlockingAfterPayloadBody(tt.payload)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: blocked}}
			cfg := rawChatCompletionsTestConfig()
			if tt.idle {
				cfg.Gateway.StreamDataIntervalTimeout = 1
			}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resultCh := make(chan *OpenAIForwardResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := svc.Forward(ctx, c, forceChatResponsesFallbackAccount(), requestBody)
				resultCh <- result
				errCh <- err
			}()

			select {
			case <-blocked.blocked:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream reader did not reach its blocking point")
			}
			if tt.cancel {
				cancel()
			}
			var result *OpenAIForwardResult
			var err error
			select {
			case result = <-resultCh:
				err = <-errCh
			case <-time.After(2 * time.Second):
				_ = blocked.Close()
				result = <-resultCh
				err = <-errCh
				t.Fatalf("raw CC bridge did not terminate after cancellation/stage overflow")
			}
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.NotNil(t, result)
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			if tt.cancel {
				require.ErrorIs(t, err, context.Canceled)
			}
			if tt.idle {
				require.ErrorContains(t, err, "data interval timeout")
			}
			if tt.committed {
				require.Contains(t, recorder.Body.String(), "hello")
			}
			select {
			case <-blocked.closed:
			default:
				t.Fatal("raw CC bridge returned without closing the upstream body")
			}
			require.Equal(t, int32(1), blocked.closes.Load(), "raw CC Responses bridge must close its upstream body exactly once")
		})
	}
}

func TestForwardResponsesRawCCFirstSemanticOverflowUsesCommittedOutputBoundary(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
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
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.upstreamSSE)),
			}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20

			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), requestBody)
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

func TestForwardResponses_ForceChatCompletionsReportsBillingErrorForNegativeUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_negative","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":-1,"completion_tokens":-2,"completion_tokens_details":{"image_tokens":-3}}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.ErrorIs(t, err, ErrOpenAIUpstreamUsageMissing)
	require.NotNil(t, result, "failed result retains capture metadata")
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, c.Writer.Written(), "forwarding must not buffer the response solely to validate usage")
	require.Contains(t, rec.Body.String(), "ok")
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

func TestScanCCStreamStopsAtDoneWithoutValidatingProviderTail(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"tail"},"finish_reason":null}]}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	state := (&OpenAIGatewayService{}).scanCCStream(
		context.Background(), resp, "test", "rid-tail", time.Now(),
		func(*apicompat.ChatCompletionsChunk) (bool, error) { return false, nil },
	)

	require.True(t, state.SawDone)
	require.NoError(t, state.Err)
}

func TestScanCCStreamParserSkipDrainsThroughDoneForCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/chat/completions", nil)
	setCaptureUpstreamRequest(c, request, 1<<20)
	first := []byte("data: {bad}\n\n")
	tail := []byte("data: [DONE]\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &rawChatDelayedFiniteTailReadCloser{first: first, tail: tail, closed: make(chan struct{})},
		Request:    request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)

	state := (&OpenAIGatewayService{}).scanCCStream(
		c.Request.Context(), resp, "test", "rid-parser-tail", time.Now(),
		func(*apicompat.ChatCompletionsChunk) (bool, error) { return false, nil },
	)
	finishCapture()

	require.NoError(t, state.Err)
	require.True(t, state.SawDone)
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, append(append([]byte(nil), first...), tail...), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

func TestScanCCStreamDoesNotWaitForDelayedChunkedDataAfterDone(t *testing.T) {
	terminal := []byte(strings.Join([]string{
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	tail := []byte(`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"tail"},"finish_reason":null}]}` + "\n\n")
	body := &delayedOpenAITerminalTailBody{terminal: terminal, tail: tail, delay: 5 * time.Millisecond, closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: body}

	state := (&OpenAIGatewayService{}).scanCCStream(
		context.Background(), resp, "test", "rid-tail", time.Now(),
		func(*apicompat.ChatCompletionsChunk) (bool, error) { return false, nil },
	)

	require.True(t, state.SawDone)
	require.NoError(t, state.Err)
}

func TestScanCCStreamReturnsAtDoneWithoutInspectingBufferedPartialTail(t *testing.T) {
	terminal := []byte(strings.Join([]string{
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	partialTail := []byte(`data: {"id":"chatcmpl-tail","choices":[{"index":0,"delta":{"content":"tail"}`)
	body := &providerPrefixThenBlockReader{
		prefix: append(append([]byte(nil), terminal...), partialTail...),
		closed: make(chan struct{}),
	}
	resp := &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: body}

	state := (&OpenAIGatewayService{}).scanCCStream(
		context.Background(), resp, "test", "rid-tail", time.Now(),
		func(*apicompat.ChatCompletionsChunk) (bool, error) { return false, nil },
	)

	require.True(t, state.SawDone)
	require.NoError(t, state.Err)
	require.NoError(t, body.Close())
	select {
	case <-body.closed:
	default:
		t.Fatal("caller cleanup must close the provider body")
	}
}

func TestScanCCStreamAllowsSingleSemanticFrameBeyondFirstOutputGuard(t *testing.T) {
	largeContent := strings.Repeat("x", openAIFirstOutputStageMaxBytes+1024)
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-large","choices":[{"index":0,"delta":{"content":"` + largeContent + `"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-large","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	state := (&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: 16 * 1024 * 1024,
	}}}).scanCCStream(
		context.Background(), resp, "test", "rid-large", time.Now(),
		func(chunk *apicompat.ChatCompletionsChunk) (bool, error) {
			return len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil && *chunk.Choices[0].Delta.Content != "", nil
		},
	)

	require.NoError(t, state.Err)
	require.True(t, state.SawOutput)
	require.True(t, state.SawDone)
}

func forceChatResponsesFallbackAccount() *Account {
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}
	return account
}

// reasoningRecordingCache 记录 reasoning 缓存写入、并按需响应回查。
type reasoningRecordingCache struct {
	stubGatewayCache
	mu      sync.Mutex
	sets    map[string]string
	getResp map[string]string
}

func (c *reasoningRecordingCache) SetReasoningContent(_ context.Context, itemID string, content string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sets == nil {
		c.sets = make(map[string]string)
	}
	c.sets[itemID] = content
	return nil
}

func (c *reasoningRecordingCache) GetReasoningContent(_ context.Context, itemID string) (string, error) {
	if v, ok := c.getResp[itemID]; ok {
		return v, nil
	}
	return "", ErrReasoningContentNotFound
}

func (c *reasoningRecordingCache) snapshotSets() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.sets))
	for k, v := range c.sets {
		out[k] = v
	}
	return out
}

// 流式响应里的 reasoning_content 应按 reasoning item id 写入缓存，供后续轮次
// 客户端不回传明文 summary 时回注（DeepSeek thinking mode 400 修复的写入侧）。
func TestForwardResponses_ChatFallbackCachesStreamedReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"think "},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"first"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_rc","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_reasoning_cache_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		cache:        cache,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)

	sets := cache.snapshotSets()
	require.Len(t, sets, 1, "应恰好缓存一个 reasoning item")
	for itemID, content := range sets {
		require.NotEmpty(t, itemID)
		require.Equal(t, "think first", content)
	}
}

// 请求侧：encrypted-only reasoning item（无明文 summary）经缓存回查补回
// reasoning_content；带明文 summary 的 item 顺手回写缓存（自愈）。
func TestForwardResponses_ChatFallbackRestoresReasoningFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model":"deepseek-reasoner",
		"stream":false,
		"input":[
			{"type":"reasoning","id":"item_plain","summary":[{"type":"summary_text","text":"plain thinking"}]},
			{"type":"function_call","call_id":"call_0","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_0","output":"ok"},
			{"type":"reasoning","id":"item_enc1","summary":[],"encrypted_content":"opaque"},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_reasoning_cache_restore"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_restore","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		)),
	}}
	cache := &reasoningRecordingCache{
		getResp: map[string]string{"item_enc1": "cached thinking"},
	}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
		cache:        cache,
	}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)

	// 明文 summary 的 assistant 工具调用消息：reasoning_content 来自 summary 本身。
	require.Equal(t, "plain thinking", gjson.GetBytes(upstream.lastBody, "messages.0.reasoning_content").String())
	require.Equal(t, "call_0", gjson.GetBytes(upstream.lastBody, "messages.0.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
	// encrypted-only 的 assistant 工具调用消息：reasoning_content 来自缓存回查。
	require.Equal(t, "cached thinking", gjson.GetBytes(upstream.lastBody, "messages.2.reasoning_content").String())
	require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, "messages.2.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.lastBody, "messages.3.role").String())

	// 明文 summary 的 item 被回写进缓存（自愈）。
	require.Equal(t, "plain thinking", cache.snapshotSets()["item_plain"])
}

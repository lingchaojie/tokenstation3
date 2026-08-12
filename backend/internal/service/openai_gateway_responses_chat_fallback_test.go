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
			`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"image_tokens":5}}}`,
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
	enableCaptureForTest(t, c)

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}],"usage":{"prompt_tokens":"invalid","input_tokens":12,"prompt_tokens_details":{"audio_tokens":2},"output_tokens_details":{"reasoning_tokens":7},"_sub2api_kiro_credits":0.17}}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"completion_tokens":3,"total_tokens":15,"cache_read_input_tokens":4,"cache_creation_input_tokens":6,"completion_tokens_details":{"audio_tokens":3,"image_tokens":5,"accepted_prediction_tokens":4,"rejected_prediction_tokens":1},"_sub2api_kiro_credits":0}}`,
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
	require.Contains(t, rec.Body.String(), `"image_tokens":5`)
	require.Contains(t, rec.Body.String(), `"accepted_prediction_tokens":4`)
	require.Contains(t, rec.Body.String(), `"rejected_prediction_tokens":1`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 5, result.Usage.ImageOutputTokens)
	require.Zero(t, result.Usage.KiroCredits)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, upstream.lastBody, result.UpstreamRequest)
	require.Equal(t, upstreamBody, string(result.CaptureResponse))
}

func TestForwardResponsesRawCCMissingDoneClassifiesCommitBoundary(t *testing.T) {
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
			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
			require.Error(t, err)
			if tt.committed {
				require.NotNil(t, result)
				require.Equal(t, upstream.lastBody, result.UpstreamRequest)
				require.Equal(t, tt.sse, string(result.CaptureResponse))
				require.Contains(t, recorder.Body.String(), `"delta":"hi"`)
			} else {
				require.Nil(t, result)
				var fo *UpstreamFailoverError
				require.ErrorAs(t, err, &fo)
				require.Equal(t, -1, c.Writer.Size(), "protocol preamble must remain retryable")
				require.Empty(t, recorder.Body.String(), "discard staged preamble before failover")
			}
		})
	}
}

func TestForwardResponsesRawCCUsesConvertedCommitBoundaryAndStopsOnMalformedChunk(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sse       string
		committed bool
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
			name:      "malformed after converted text returns partial error",
			sse:       "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {not-json}\n\ndata: [DONE]\n\n",
			committed: true,
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

			result, err := (&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}).Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			if tt.committed {
				require.NotNil(t, result)
				require.False(t, errors.As(err, &failoverErr))
				require.Contains(t, recorder.Body.String(), `"delta":"hello"`)
				require.Equal(t, tt.sse, string(result.CaptureResponse))
				return
			}
			require.Nil(t, result)
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, -1, c.Writer.Size())
			require.Empty(t, recorder.Body.String())
		})
	}
}

func rawCCSpilledCommitFailureSSE() string {
	largeID := strings.Repeat("s", openAIFirstOutputStageMemoryLimit+1024)
	return strings.Join([]string{
		`data: {"id":"` + largeID + `","choices":[{"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"x","choices":[{"delta":{"content":"hello"}}]}`,
		"",
		`data: {"id":"x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
		"",
		"data: {not-json}",
		"",
	}, "\n")
}

func TestForwardResponsesRawCCSpilledCommitWriteFailureUsesDeliveredByteBoundary(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	for _, tt := range []struct {
		name          string
		acceptedBytes int
		wantCommitted bool
	}{
		{name: "zero bytes remains failover safe", acceptedBytes: 0},
		{name: "partial bytes prevents replay", acceptedBytes: 1024, wantCommitted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Writer.Header().Set("X-Preexisting-Test", "kept")
			headersBeforeAttempt := c.Writer.Header().Clone()
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

			require.Error(t, err)
			require.Equal(t, tt.acceptedBytes, writer.wrote)
			require.Equal(t, tt.acceptedBytes, recorder.Body.Len())
			var failoverErr *UpstreamFailoverError
			if !tt.wantCommitted {
				require.Nil(t, result)
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, -1, c.Writer.Size())
				require.Empty(t, recorder.Body.String(), "a zero-byte failed attempt must not pollute the replay stream")
				require.Equal(t, headersBeforeAttempt, c.Writer.Header(), "a zero-byte failed attempt must not pollute replay response headers")
				return
			}
			require.NotNil(t, result)
			require.False(t, errors.As(err, &failoverErr), "partial downstream delivery must not be replayed")
			require.ErrorContains(t, err, "stream usage incomplete")
			require.Equal(t, tt.acceptedBytes, c.Writer.Size())
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NotNil(t, result.FirstTokenMs)
		})
	}
}

func TestForwardResponsesRawCCCancellationAndStageBoundCloseBody(t *testing.T) {
	oversizedConvertedPreamble := []byte("data: {\"id\":\"" + strings.Repeat("x", openAIFirstOutputStageMaxBytes-64) + "\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	require.Less(t, len(bytes.TrimSuffix(oversizedConvertedPreamble, []byte("\n\n"))), openAIFirstOutputStageMaxBytes,
		"fixture must reach converted staging rather than the scanner-token guard")
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
		{name: "oversized converted preamble", payload: oversizedConvertedPreamble},
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
			if tt.committed {
				require.NotNil(t, result)
				require.False(t, errors.As(err, &failoverErr))
				require.Contains(t, recorder.Body.String(), "hello")
				if tt.idle {
					require.Contains(t, err.Error(), "idle timeout")
				}
			} else {
				require.Nil(t, result)
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, -1, c.Writer.Size())
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
	}{
		{name: "oversized first semantic converted event", upstreamSSE: firstSemanticLine + "\n\n"},
		{
			name: "oversized newline-free token after committed text",
			upstreamSSE: "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"committed\"}}]}\n\n" +
				"data: " + strings.Repeat("x", openAIFirstOutputStageMaxBytes+1),
			committed: true,
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
				require.Contains(t, recorder.Body.String(), "committed")
				return
			}
			require.Nil(t, result)
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, -1, c.Writer.Size())
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestForwardResponses_ForceChatCompletionsNormalizesNegativeUsage(t *testing.T) {
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
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
	require.Zero(t, gjson.Get(rec.Body.String(), "usage.output_tokens").Int())
	require.Zero(t, gjson.Get(rec.Body.String(), "usage.total_tokens").Int())
	require.False(t, gjson.Get(rec.Body.String(), "usage.output_tokens_details").Exists())
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.ImageOutputTokens)
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

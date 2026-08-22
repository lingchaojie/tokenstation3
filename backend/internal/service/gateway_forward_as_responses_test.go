//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicBufferedContentAccumulatorUsesLinearAllocationForTinyFragments(t *testing.T) {
	const fragmentCount = 128 * 1024
	response := &apicompat.AnthropicResponse{}
	var accumulator anthropicBufferedContentAccumulator
	accumulator.start(response, apicompat.AnthropicContentBlock{Type: "text"})
	delta := &apicompat.AnthropicDelta{Type: "text_delta", Text: "x"}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < fragmentCount; i++ {
		accumulator.delta(0, delta)
	}
	accumulator.materialize(response)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.Len(t, response.Content, 1)
	require.Len(t, response.Content[0].Text, fragmentCount)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestAdaptResponsesClientToolsForAnthropic_FlattensNamespace(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"claude-fable-5",
		"input":[{"type":"function_call","call_id":"call_1","namespace":"codex_app","name":"read_thread","arguments":"{}"}],
		"tools":[{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"read_thread","description":"Read a task","parameters":{"type":"object","properties":{}}}]}]
	}`)

	adapted, mapping, err := adaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.Equal(t, apicompat.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := request["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "codex_app__read_thread", tool["name"])

	input := request["input"].([]any)
	call := input[0].(map[string]any)
	require.Equal(t, "codex_app__read_thread", call["name"])
	require.NotContains(t, call, "namespace")
}

func TestAdaptResponsesClientToolsForAnthropic_LiftsAdditionalTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-fable-5",
		"input":[
			{"type":"additional_tools","tools":[
				{"type":"custom","name":"exec","description":"Run a command"},
				{"type":"namespace","name":"codex_app","tools":[
					{"type":"function","name":"read_thread","parameters":{"type":"object"}}
				]}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"}]}
		]
	}`)

	adapted, mapping, err := adaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.True(t, mapping.CustomTools["exec"])
	require.Equal(t, apicompat.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := request["tools"].([]any)
	require.Len(t, tools, 2)
	require.Equal(t, "function", tools[0].(map[string]any)["type"])
	require.Equal(t, "codex_app__read_thread", tools[1].(map[string]any)["name"])

	input := request["input"].([]any)
	require.Len(t, input, 1)
	require.Equal(t, "message", input[0].(map[string]any)["type"])
}

func namespaceToolAnthropicStream() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_namespace","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":null,"usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_namespace","name":"codex_app__read_thread","input":{"thread_id":"123"}}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func namespaceToolMapping() apicompat.ResponsesClientToolMapping {
	return apicompat.ResponsesClientToolMapping{NamespaceTools: map[string]apicompat.ResponsesNamespaceName{
		"codex_app__read_thread": {Namespace: "codex_app", Name: "read_thread"},
	}}
}

func TestHandleResponsesBufferedStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	svc := &GatewayService{}
	_, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), false, namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `"type":"function_call"`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

func TestHandleResponsesStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	svc := &GatewayService{}
	_, err := svc.handleResponsesStreamingResponse(resp, c, "claude-fable-5", "claude-fable-5", nil, time.Now(), false, namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `response.output_item.added`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

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
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7,"_sub2api_kiro_credits":0.17}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
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
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
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
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, apicompat.ResponsesClientToolMapping{},
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
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, apicompat.ResponsesClientToolMapping{},
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

func TestAnthropicToResponsesCompatibilityClientDisconnectCompleteAfterProviderTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
	resp := markedKiroFinalUsageAnthropicResponse("msg_responses_disconnect_complete")

	result, err := (&GatewayService{}).handleResponsesStreamingResponse(
		resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, apicompat.ResponsesClientToolMapping{},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.CaptureResponseComplete)
}

func TestAnthropicToResponsesCompatibilityRejectsIncompleteProviderTailAfterTerminal(t *testing.T) {
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
			result, err := (&GatewayService{}).handleResponsesBufferedStreamingResponse(resp, c, "claude-test", "claude-test", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
			require.Error(t, err)
			require.Nil(t, result)
		})
		t.Run("stream/"+name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(complete + tail))}
			result, err := (&GatewayService{}).handleResponsesStreamingResponse(resp, c, "claude-test", "claude-test", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
			require.Error(t, err)
			require.NotNil(t, result)
			require.True(t, result.CaptureTerminalError)
		})
	}
}

func TestAnthropicToResponsesCompatibilityHonorsProviderIdleTimeout(t *testing.T) {
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
					result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-test", "claude-test", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
					done <- outcome{result: result, err: err}
					return
				}
				result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-test", "claude-test", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
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
				t.Fatal("Anthropic-to-Responses compatibility stream ignored StreamDataIntervalTimeout")
			}
			require.NoError(t, body.Close())
		})
	}
}

func TestAnthropicToResponsesParserFailureDrainsFiniteProviderTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginCaptureAttempt(c)

	prefix := []byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_bad","type":"message","role":"assistant","content":"invalid","model":"claude-test","usage":{"input_tokens":1}}}`,
		``,
	}, "\n"))
	tail := []byte(strings.Join([]string{
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))
	body := &delayedOpenAITerminalTailBody{
		terminal: prefix,
		tail:     tail,
		delay:    75 * time.Millisecond,
		closed:   make(chan struct{}),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
		Request:    c.Request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)

	result, err := (&GatewayService{}).handleResponsesStreamingResponse(
		resp, c, "claude-test", "claude-test", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{},
	)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, append(append([]byte(nil), prefix...), tail...), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

func TestOpenAIResponsesPassthroughParserFailureDrainsFiniteProviderTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginCaptureAttempt(c)

	first := []byte("data: {bad}\n\n")
	tail := []byte(`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"tail"}}}` + "\n\n")
	body := &delayedOpenAITerminalTailBody{
		terminal: first,
		tail:     tail,
		delay:    75 * time.Millisecond,
		closed:   make(chan struct{}),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
		Request:    c.Request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)

	result, err := (&OpenAIGatewayService{}).handleStreamingResponsePassthrough(
		c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5", "gpt-5",
	)
	require.NotNil(t, result)
	require.Error(t, err)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, append(append([]byte(nil), first...), tail...), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

func TestOpenAIResponsesNativeParserFailureDrainsFiniteProviderTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginCaptureAttempt(c)

	first := []byte("data: {bad}\n\n")
	tail := []byte(`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"tail"}}}` + "\n\n")
	body := &delayedOpenAITerminalTailBody{
		terminal: first,
		tail:     tail,
		delay:    75 * time.Millisecond,
		closed:   make(chan struct{}),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
		Request:    c.Request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		StreamDataIntervalTimeout: 1,
	}}}

	result, err := svc.handleStreamingResponse(
		c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5", "gpt-5",
	)
	require.NotNil(t, result)
	require.Error(t, err)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, append(append([]byte(nil), first...), tail...), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

func TestOpenAIResponsesPassthroughScannerLimitDrainsFiniteProviderTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	providerBody := bytes.Repeat([]byte{'x'}, (2<<20)+6)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
		Request:    request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 3<<20)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: 1 << 20,
	}}}

	result, err := svc.handleStreamingResponsePassthrough(
		c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5", "gpt-5",
	)
	require.NotNil(t, result)
	require.ErrorIs(t, err, bufio.ErrTooLong)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, providerBody, capture.Response)
	require.False(t, capture.ResponseTruncated)
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

func TestParseAnthropicSSEField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		line      string
		field     string
		wantValue string
		wantOK    bool
	}{
		{
			name:      "standard format with space",
			line:      "event: message_start",
			field:     "event",
			wantValue: "message_start",
			wantOK:    true,
		},
		{
			name:      "compact format without space",
			line:      "event:message_start",
			field:     "event",
			wantValue: "message_start",
			wantOK:    true,
		},
		{
			name:      "data field with space",
			line:      "data: {\"type\":\"message_start\"}",
			field:     "data",
			wantValue: "{\"type\":\"message_start\"}",
			wantOK:    true,
		},
		{
			name:      "data field without space",
			line:      "data:{\"type\":\"message_start\"}",
			field:     "data",
			wantValue: "{\"type\":\"message_start\"}",
			wantOK:    true,
		},
		{
			name:      "field with multiple spaces after colon",
			line:      "event:  message_delta",
			field:     "event",
			wantValue: "message_delta",
			wantOK:    true,
		},
		{
			name:      "wrong field name",
			line:      "event: message_start",
			field:     "data",
			wantValue: "",
			wantOK:    false,
		},
		{
			name:      "empty line",
			line:      "",
			field:     "event",
			wantValue: "",
			wantOK:    false,
		},
		{
			name:      "line without colon",
			line:      "invalid line",
			field:     "event",
			wantValue: "",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotOK := parseAnthropicSSEField(tt.line, tt.field)
			require.Equal(t, tt.wantOK, gotOK, "parseAnthropicSSEField() ok")
			require.Equal(t, tt.wantValue, gotValue, "parseAnthropicSSEField() value")
		})
	}
}

func TestHandleResponsesBufferedStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Simulate compact SSE format without spaces after colons (e.g. Kimi API)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":10}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:content_block_stop`,
			`data:{"type":"content_block_stop","index":0}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestHandleResponsesStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Simulate compact SSE format without spaces after colons (e.g. Kimi API)
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact_stream","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":null,"usage":{"input_tokens":15}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:content_block_stop`,
			`data:{"type":"content_block_stop","index":0}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 15, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `response.completed`)
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type cursorChatTestStream struct {
	events   <-chan cursorpkg.AgentEvent
	response *http.Response
}

func (s *cursorChatTestStream) Events() <-chan cursorpkg.AgentEvent { return s.events }
func (s *cursorChatTestStream) Response() *http.Response            { return s.response }

func newCursorChatTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, recorder
}

func cursorChatTestEvents(events ...cursorpkg.AgentEvent) <-chan cursorpkg.AgentEvent {
	ch := make(chan cursorpkg.AgentEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func cursorChatTestMeta(stream, includeUsage bool) cursorChatMeta {
	return cursorChatMeta{
		originalModel: "caller-model", billingModel: "billing-model", upstreamModel: "upstream-model",
		stream: stream, includeUsage: includeUsage,
	}
}

func cursorChatTestStreamWithHeader(events ...cursorpkg.AgentEvent) *cursorChatTestStream {
	return &cursorChatTestStream{
		events: cursorChatTestEvents(events...),
		response: &http.Response{Header: http.Header{
			"X-Request-Id": []string{"cursor-request-id"},
		}},
	}
}

func TestCursorChatBufferedResponseIsNativeAndOmitsStreamingToolIndexes(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "思考"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "hello 世界"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "call_0", Name: "weather", Arguments: `{"city":"巴黎"}`}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "call_1", Name: "time", Arguments: `{"tz":"UTC"}`}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{InputTokens: 3, OutputTokens: 5}},
	)

	result, err := (&OpenAIGatewayService{}).bufferCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(false, false), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))

	var response apicompat.ChatCompletionsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "chat.completion", response.Object)
	require.Equal(t, "caller-model", response.Model)
	require.Len(t, response.Choices, 1)
	require.Equal(t, "assistant", response.Choices[0].Message.Role)
	require.JSONEq(t, `"hello 世界"`, string(response.Choices[0].Message.Content))
	require.Equal(t, "思考", response.Choices[0].Message.ReasoningContent)
	require.Equal(t, "tool_calls", response.Choices[0].FinishReason)
	require.Len(t, response.Choices[0].Message.ToolCalls, 2)
	for _, call := range response.Choices[0].Message.ToolCalls {
		require.Nil(t, call.Index)
	}
	require.Equal(t, &apicompat.ChatUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}, response.Usage)
	require.Equal(t, "cursor-request-id", result.RequestID)
	require.Equal(t, "caller-model", result.Model)
	require.Equal(t, "billing-model", result.BillingModel)
	require.Equal(t, "upstream-model", result.UpstreamModel)
	require.False(t, result.Stream)
	require.True(t, result.CaptureResponseComplete)
}

func TestCursorChatStreamingEmitsNativeDeltasFinishUsageAndOneDone(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "hi 世界"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "reasoning"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "call_0", Name: "lookup", Arguments: `{"q":"天气"}`}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{InputTokens: 7, OutputTokens: 11}},
	)

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(true, true), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]\n\n"))

	chunks := decodeCursorChatSSEChunks(t, recorder.Body.String())
	require.Len(t, chunks, 6)
	require.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	require.Equal(t, "hi 世界", *chunks[1].Choices[0].Delta.Content)
	require.Equal(t, "reasoning", *chunks[2].Choices[0].Delta.ReasoningContent)
	require.Len(t, chunks[3].Choices[0].Delta.ToolCalls, 1)
	require.Equal(t, 0, *chunks[3].Choices[0].Delta.ToolCalls[0].Index)
	require.Equal(t, "tool_calls", *chunks[4].Choices[0].FinishReason)
	require.Empty(t, chunks[5].Choices)
	require.Equal(t, &apicompat.ChatUsage{PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18}, chunks[5].Usage)
	for _, chunk := range chunks {
		require.Equal(t, "caller-model", chunk.Model)
	}
	require.True(t, result.Stream)
	require.True(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed)
}

func TestCursorChatStreamingLocalLengthIsNotProviderTerminal(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	meta := cursorChatTestMeta(true, false)
	meta.maxOutputTokens = 1
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: strings.Repeat("界", 20)},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{OutputTokens: 999}},
	)

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, meta, cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	chunks := decodeCursorChatSSEChunks(t, recorder.Body.String())
	require.Equal(t, "length", *chunks[len(chunks)-1].Choices[0].FinishReason)
	require.False(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed)
}

func TestCursorChatStreamingPreOutputUpstreamErrorWithholdsResponseForFailover(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	providerErr := &cursorpkg.AgentError{Code: "unavailable", Message: "private", Raw: `{"token":"secret"}`, HTTPStatus: http.StatusServiceUnavailable}
	stream := cursorChatTestStreamWithHeader(cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: providerErr})

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now(),
	)
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, recorder.Body.Bytes())
	require.False(t, recorder.Result().Header.Get("Content-Type") == "text/event-stream")
	require.False(t, IsResponseCommitted(c))
}

func TestCursorChatStreamingProviderErrorAfterOutputIsSafeTerminalFailure(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	providerErr := &cursorpkg.AgentError{
		Code: "unavailable", Message: "secret bearer prompt proxy.example", Raw: `{"token":"secret","prompt":"private"}`,
		HTTPStatus: http.StatusBadGateway, HasHTTPResponse: true, ActualHTTPStatus: http.StatusServiceUnavailable,
	}
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "visible"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: providerErr},
	)

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(true, true), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	body := recorder.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"upstream_error"`))
	require.Equal(t, 1, strings.Count(body, "data: [DONE]\n\n"))
	require.NotContains(t, body, `"finish_reason":"stop"`)
	require.NotContains(t, body, "secret")
	require.NotContains(t, body, "proxy.example")
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
	require.Equal(t, http.StatusServiceUnavailable, result.UpstreamHTTPStatus)
}

type cursorChatFailingWriter struct {
	gin.ResponseWriter
	failAt              int
	writes              int
	postFailureAttempts int
}

func (w *cursorChatFailingWriter) Write(payload []byte) (int, error) {
	w.writes++
	if w.writes >= w.failAt {
		if w.writes > w.failAt {
			w.postFailureAttempts++
		}
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(payload)
}

func TestCursorChatClientDisconnectWriteFailureDrainsProviderUsageWithoutMoreWrites(t *testing.T) {
	c, _ := newCursorChatTestContext(t)
	failing := &cursorChatFailingWriter{ResponseWriter: c.Writer, failAt: 2}
	c.Writer = failing
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "first"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "second"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{InputTokens: 13, OutputTokens: 17}},
	)

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(true, true), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed)
	require.Equal(t, OpenAIUsage{InputTokens: 13, OutputTokens: 17}, result.Usage)
	require.Zero(t, failing.postFailureAttempts)
}

func TestCursorChatClientDisconnectThenProviderErrorPreservesBothCauses(t *testing.T) {
	c, _ := newCursorChatTestContext(t)
	failing := &cursorChatFailingWriter{ResponseWriter: c.Writer, failAt: 2}
	c.Writer = failing
	stream := cursorChatTestStreamWithHeader(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "first"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: &cursorpkg.AgentError{Code: "internal", Raw: "private", HTTPStatus: http.StatusBadGateway}},
	)

	result, err := (&OpenAIGatewayService{}).streamCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
	require.Zero(t, failing.postFailureAttempts)
}

func TestCursorChatBufferedUpstreamFailureWritesNoSuccessBody(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	stream := cursorChatTestStreamWithHeader(cursorpkg.AgentEvent{
		Type: cursorpkg.AgentEventError, Err: &cursorpkg.AgentError{Code: "unavailable", HTTPStatus: http.StatusServiceUnavailable},
	})

	result, err := (&OpenAIGatewayService{}).bufferCursorChatCompletions(
		c, cursorGatewayAccount(), stream, cursorChatTestMeta(false, false), cursorInputEstimate{}, time.Now(),
	)
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, recorder.Body.Bytes())
	require.False(t, IsResponseCommitted(c))
}

type cursorCaptureRecordingAttempt struct{ response bytes.Buffer }

func (*cursorCaptureRecordingAttempt) ID() uuid.UUID            { return uuid.New() }
func (*cursorCaptureRecordingAttempt) WriteRequest([]byte) bool { return true }
func (a *cursorCaptureRecordingAttempt) WriteResponse(p []byte) bool {
	_, _ = a.response.Write(p)
	return true
}
func (*cursorCaptureRecordingAttempt) WriteRequestHeaders([]byte) bool  { return true }
func (*cursorCaptureRecordingAttempt) WriteResponseHeaders([]byte) bool { return true }
func (*cursorCaptureRecordingAttempt) Finalize(model.Final) bool        { return true }
func (*cursorCaptureRecordingAttempt) Commit() bool                     { return true }
func (*cursorCaptureRecordingAttempt) Abort()                           {}

type cursorPartialWriter struct {
	gin.ResponseWriter
	n   int
	err error
}

func (w *cursorPartialWriter) Write([]byte) (int, error) { return w.n, w.err }

func TestCursorDeliveryWriteRecordsOnlySuccessfullyDeliveredBytes(t *testing.T) {
	c, _ := newCursorChatTestContext(t)
	sink := &cursorCaptureRecordingAttempt{}
	replaceCaptureAttemptForRequest(c, &CaptureAttempt{
		attempt: sink,
		policy:  model.ContentPolicy{StoreResponseBody: true},
	})
	partialErr := errors.New("partial client write")
	c.Writer = &cursorPartialWriter{ResponseWriter: c.Writer, n: 3, err: partialErr}

	n, err := writeCursorDeliveryBytes(c, []byte("abcdef"))
	require.Equal(t, 3, n)
	require.ErrorIs(t, err, partialErr)
	require.Equal(t, "abc", sink.response.String())
}

func TestCursorDeliveryWriteWithoutCaptureMatchesGinWriter(t *testing.T) {
	c, recorder := newCursorChatTestContext(t)
	n, err := writeCursorDeliveryBytes(c, []byte("caller-json"))
	require.NoError(t, err)
	require.Equal(t, len("caller-json"), n)
	require.Equal(t, "caller-json", recorder.Body.String())
}

func TestForwardCursorChatValidationUsesFixedSecretFreeError(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"model":`),
		[]byte(`{"model":"","messages":[{"role":"user","content":"bearer-secret"}]}`),
		[]byte(`{"model":"caller-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"safe","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"bearer-secret"}}}`),
	} {
		c, recorder := newCursorChatTestContext(t)
		result, err := (&OpenAIGatewayService{}).forwardCursorChatCompletions(
			context.Background(), c, cursorGatewayAccount(), body, "",
		)
		require.Nil(t, result)
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"type":"invalid_request_error"`)
		require.NotContains(t, recorder.Body.String(), "bearer-secret")
	}
}

func TestCursorChatUsageClampsNegativeCountsAndSaturatesTotal(t *testing.T) {
	usage := cursorChatUsage(OpenAIUsage{
		InputTokens: -1, OutputTokens: math.MaxInt,
		CacheReadInputTokens: -2, CacheCreationInputTokens: 3,
	})
	require.Equal(t, 0, usage.PromptTokens)
	require.Equal(t, math.MaxInt, usage.CompletionTokens)
	require.Equal(t, math.MaxInt, usage.TotalTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 3, usage.PromptTokensDetails.CacheWriteTokens)
}

func decodeCursorChatSSEChunks(t *testing.T, body string) []apicompat.ChatCompletionsChunk {
	t.Helper()
	var chunks []apicompat.ChatCompletionsChunk
	for _, frame := range strings.Split(body, "\n\n") {
		if frame == "" || frame == "data: [DONE]" {
			continue
		}
		require.True(t, strings.HasPrefix(frame, "data: "), frame)
		var chunk apicompat.ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &chunk), frame)
		chunks = append(chunks, chunk)
	}
	return chunks
}

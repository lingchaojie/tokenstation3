package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const cursorChatDoneSSE = "data: [DONE]\n\n"

const cursorChatDefaultDisconnectDrainTimeout = 2 * time.Second

var cursorChatValidationErrorPrefix = []byte(`{"error":{"type":"invalid_request_error","message":`)

type cursorChatEventStream interface {
	Events() <-chan cursorpkg.AgentEvent
	Response() *http.Response
	Close() error
}

func writeCursorDeliveryBytes(c *gin.Context, payload []byte) (int, error) {
	markCursorDeliveryResponse(c)
	n, err := c.Writer.Write(payload)
	if n > 0 {
		if attempt := captureAttemptForRequest(c); attempt != nil {
			attempt.WriteResponse(payload[:n])
		}
	}
	return n, err
}

// WriteCursorTerminalDeliveryBytes lets the handler deliver its final
// protocol-native Cursor error through the same successful-byte capture sink
// as ordinary Cursor JSON/SSE output.
func WriteCursorTerminalDeliveryBytes(c *gin.Context, payload []byte) (int, error) {
	return writeCursorDeliveryBytes(c, payload)
}

func writeCursorChatValidationError(c *gin.Context, message string) {
	encodedMessage, err := json.Marshal(message)
	if err != nil {
		encodedMessage = []byte(`"Invalid Chat Completions request"`)
	}
	payload := make([]byte, 0, len(cursorChatValidationErrorPrefix)+len(encodedMessage)+2)
	payload = append(payload, cursorChatValidationErrorPrefix...)
	payload = append(payload, encodedMessage...)
	payload = append(payload, '}', '}')
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(http.StatusBadRequest)
	MarkResponseCommitted(c)
	_, _ = writeCursorDeliveryBytes(c, payload)
}

func (s *OpenAIGatewayService) streamCursorChatCompletions(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "chatcmpl-")
	completionID := "chatcmpl-" + uuid.NewString()
	created := time.Now().Unix()
	delivery := cursorChatSSEDelivery{c: c}
	relay := newCursorChatEventRelay(c, stream, meta.disconnectDrainTimeout)
	roleSent := false

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return false
		}
		frame := make([]byte, 0, len(payload)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, payload...)
		frame = append(frame, '\n', '\n')
		return delivery.write(frame)
	}
	emitDelta := func(delta apicompat.ChatDelta) {
		writeChunk(apicompat.ChatCompletionsChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: created, Model: meta.originalModel,
			Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: delta}},
		})
	}

	outcome, upstreamErr := consumeCursorAgentEvents(relay.Events(), startTime, meta.maxOutputTokens, func(delta cursorDelta) error {
		if relay.Disconnected() || cursorChatRequestCanceled(c) {
			relay.Disconnect()
			delivery.disconnect()
			return nil
		}
		if delivery.stopped {
			return nil
		}
		if !roleSent {
			emitDelta(apicompat.ChatDelta{Role: "assistant"})
			roleSent = true
		}
		if delivery.stopped {
			if delivery.clientDisconnected {
				relay.Disconnect()
			}
			return nil
		}
		switch delta.kind {
		case cursorDeltaText:
			text := delta.text
			emitDelta(apicompat.ChatDelta{Content: &text})
		case cursorDeltaReasoning:
			reasoning := delta.text
			emitDelta(apicompat.ChatDelta{ReasoningContent: &reasoning})
		case cursorDeltaToolCall:
			emitDelta(apicompat.ChatDelta{ToolCalls: []apicompat.ChatToolCall{cursorToolCallDelta(delta)}})
		}
		if delivery.clientDisconnected {
			relay.Disconnect()
		}
		return nil
	})
	if relay.Disconnected() || cursorChatRequestCanceled(c) {
		relay.Disconnect()
		delivery.disconnect()
	}
	relay.Stop()
	clientDisconnectedBeforeTerminal := delivery.clientDisconnected || relay.Disconnected()

	if upstreamErr != nil && !delivery.committed && !clientDisconnectedBeforeTerminal {
		return nil, s.cursorAgentFailure(c, account, upstreamErr)
	}

	if upstreamErr != nil {
		reportCursorChatStreamFailure(c, account, upstreamErr)
		if !delivery.stopped {
			message := sanitizeStreamError(upstreamErr)
			if delivery.write(cursorChatStreamErrorSSE(message)) {
				delivery.write([]byte(cursorChatDoneSSE))
			}
		}
	} else if !delivery.stopped {
		if !roleSent {
			emitDelta(apicompat.ChatDelta{Role: "assistant"})
			roleSent = true
		}
		if !delivery.stopped {
			finishReason := outcome.finishReason
			finishChunk := apicompat.ChatCompletionsChunk{
				ID: completionID, Object: "chat.completion.chunk", Created: created, Model: meta.originalModel,
				Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{}, FinishReason: &finishReason}},
			}
			if writeChunk(finishChunk) && meta.includeUsage {
				writeChunk(apicompat.ChatCompletionsChunk{
					ID: completionID, Object: "chat.completion.chunk", Created: created, Model: meta.originalModel,
					Choices: []apicompat.ChatChunkChoice{}, Usage: cursorChatUsage(resolveCursorUsage(input, outcome)),
				})
			}
			if !delivery.stopped {
				delivery.write([]byte(cursorChatDoneSSE))
			}
		}
	}

	usage := resolveCursorUsage(input, outcome)
	result := cursorChatForwardResult(requestID, completionID, meta, usage, outcome, startTime)
	result.ClientDisconnect = delivery.clientDisconnected || relay.Disconnected()
	result.UpstreamFailed = upstreamErr != nil
	result.CaptureTerminalError = upstreamErr != nil
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, upstreamErr)
	return result, nil
}

type cursorChatSSEDelivery struct {
	c                  *gin.Context
	headersSet         bool
	committed          bool
	stopped            bool
	clientDisconnected bool
}

func (delivery *cursorChatSSEDelivery) write(payload []byte) bool {
	if delivery == nil || delivery.stopped {
		return false
	}
	if cursorChatRequestCanceled(delivery.c) {
		delivery.disconnect()
		return false
	}
	if !delivery.headersSet {
		delivery.c.Writer.Header().Set("Content-Type", "text/event-stream")
		delivery.c.Writer.Header().Set("Cache-Control", "no-cache")
		delivery.c.Writer.Header().Set("Connection", "keep-alive")
		delivery.c.Writer.Header().Set("X-Accel-Buffering", "no")
		delivery.headersSet = true
	}
	n, err := writeCursorDeliveryBytes(delivery.c, payload)
	if n > 0 {
		delivery.committed = true
		MarkResponseCommitted(delivery.c)
	}
	if err != nil || n != len(payload) {
		delivery.stopped = true
		delivery.clientDisconnected = true
		return false
	}
	delivery.c.Writer.Flush()
	return true
}

func (delivery *cursorChatSSEDelivery) disconnect() {
	if delivery == nil {
		return
	}
	delivery.stopped = true
	delivery.clientDisconnected = true
}

func cursorChatRequestCanceled(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

type cursorChatEventRelay struct {
	stream       cursorChatEventStream
	events       chan cursorpkg.AgentEvent
	disconnectCh chan struct{}
	stopCh       chan struct{}
	done         chan struct{}
	drainTimeout time.Duration

	disconnected   atomic.Bool
	disconnectOnce sync.Once
	stopOnce       sync.Once
}

func newCursorChatEventRelay(c *gin.Context, stream cursorChatEventStream, drainTimeout time.Duration) *cursorChatEventRelay {
	if drainTimeout <= 0 {
		drainTimeout = cursorChatDefaultDisconnectDrainTimeout
	}
	relay := &cursorChatEventRelay{
		stream: stream, events: make(chan cursorpkg.AgentEvent, 32),
		disconnectCh: make(chan struct{}), stopCh: make(chan struct{}), done: make(chan struct{}),
		drainTimeout: drainTimeout,
	}
	var requestDone <-chan struct{}
	if c != nil && c.Request != nil {
		requestDone = c.Request.Context().Done()
	}
	go relay.run(requestDone)
	return relay
}

func (relay *cursorChatEventRelay) Events() <-chan cursorpkg.AgentEvent { return relay.events }

func (relay *cursorChatEventRelay) Disconnect() {
	if relay == nil {
		return
	}
	relay.disconnectOnce.Do(func() {
		relay.disconnected.Store(true)
		close(relay.disconnectCh)
	})
}

func (relay *cursorChatEventRelay) Disconnected() bool {
	return relay != nil && relay.disconnected.Load()
}

func (relay *cursorChatEventRelay) Stop() {
	if relay == nil {
		return
	}
	relay.stopOnce.Do(func() { close(relay.stopCh) })
	<-relay.done
}

func (relay *cursorChatEventRelay) run(requestDone <-chan struct{}) {
	defer close(relay.done)
	defer close(relay.events)
	upstreamEvents := relay.stream.Events()
	disconnectSignal := relay.disconnectCh
	var drainTimer *time.Timer
	var drainDeadline <-chan time.Time
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()
	startDrain := func() {
		relay.Disconnect()
		requestDone = nil
		disconnectSignal = nil
		if drainTimer == nil {
			drainTimer = time.NewTimer(relay.drainTimeout)
			drainDeadline = drainTimer.C
		}
	}

	for {
		if requestDone != nil {
			select {
			case <-requestDone:
				startDrain()
			default:
			}
		}
		if drainDeadline != nil {
			select {
			case <-drainDeadline:
				_ = relay.stream.Close()
				return
			default:
			}
		}
		select {
		case <-requestDone:
			startDrain()
		case <-disconnectSignal:
			startDrain()
		case <-drainDeadline:
			_ = relay.stream.Close()
			return
		case <-relay.stopCh:
			return
		case event, ok := <-upstreamEvents:
			if !ok {
				return
			}
			if requestDone != nil {
				select {
				case <-requestDone:
					startDrain()
				default:
				}
			}
			select {
			case relay.events <- event:
			case <-drainDeadline:
				_ = relay.stream.Close()
				return
			case <-relay.stopCh:
				return
			}
		}
	}
}

func (s *OpenAIGatewayService) bufferCursorChatCompletions(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "chatcmpl-")
	completionID := "chatcmpl-" + uuid.NewString()
	outcome, err := consumeCursorAgentEvents(stream.Events(), startTime, meta.maxOutputTokens, nil)
	if err != nil {
		return nil, s.cursorAgentFailure(c, account, err)
	}

	usage := resolveCursorUsage(input, outcome)
	payload, err := json.Marshal(cursorChatCompletionsResponse(completionID, meta.originalModel, outcome, usage))
	if err != nil {
		return nil, errors.New("cursor: marshal chat completions response")
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(http.StatusOK)
	n, writeErr := writeCursorDeliveryBytes(c, payload)
	if n > 0 {
		MarkResponseCommitted(c)
	}

	result := cursorChatForwardResult(requestID, completionID, meta, usage, outcome, startTime)
	result.ClientDisconnect = writeErr != nil || n != len(payload)
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, nil)
	return result, nil
}

func cursorChatCompletionsResponse(
	completionID string,
	model string,
	outcome cursorChatOutcome,
	usage OpenAIUsage,
) apicompat.ChatCompletionsResponse {
	content := json.RawMessage("null")
	if outcome.content != "" {
		content, _ = json.Marshal(outcome.content)
	}
	toolCalls := make([]apicompat.ChatToolCall, len(outcome.toolCalls))
	copy(toolCalls, outcome.toolCalls)
	for index := range toolCalls {
		toolCalls[index].Index = nil
	}
	return apicompat.ChatCompletionsResponse{
		ID: completionID, Object: "chat.completion", Created: time.Now().Unix(), Model: model,
		Choices: []apicompat.ChatChoice{{
			Index: 0,
			Message: apicompat.ChatMessage{
				Role: "assistant", Content: content, ReasoningContent: outcome.reasoning, ToolCalls: toolCalls,
			},
			FinishReason: outcome.finishReason,
		}},
		Usage: cursorChatUsage(usage),
	}
}

func cursorChatUsage(usage OpenAIUsage) *apicompat.ChatUsage {
	prompt := nonnegativeCursorInt(usage.InputTokens)
	completion := nonnegativeCursorInt(usage.OutputTokens)
	result := &apicompat.ChatUsage{
		PromptTokens: prompt, CompletionTokens: completion,
		TotalTokens: saturatingAddNonnegativeInt(prompt, completion),
	}
	cacheRead := nonnegativeCursorInt(usage.CacheReadInputTokens)
	cacheWrite := nonnegativeCursorInt(usage.CacheCreationInputTokens)
	if cacheRead > 0 || cacheWrite > 0 {
		result.PromptTokensDetails = &apicompat.ChatTokenDetails{
			CachedTokens: cacheRead, CacheWriteTokens: cacheWrite,
		}
	}
	return result
}

func nonnegativeCursorInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func cursorToolCallDelta(delta cursorDelta) apicompat.ChatToolCall {
	return apicompat.ChatToolCall{
		Index: intPtr(delta.toolIndex), ID: delta.toolID, Type: "function",
		Function: apicompat.ChatFunctionCall{Name: delta.toolName, Arguments: delta.toolArguments},
	}
}

func cursorAgentRequestID(stream cursorChatEventStream, prefix string) string {
	if stream != nil {
		if response := stream.Response(); response != nil {
			if requestID := strings.TrimSpace(response.Header.Get("x-request-id")); requestID != "" {
				return requestID
			}
		}
	}
	return prefix + uuid.NewString()
}

func cursorChatForwardResult(
	requestID string,
	responseID string,
	meta cursorChatMeta,
	usage OpenAIUsage,
	outcome cursorChatOutcome,
	startTime time.Time,
) *OpenAIForwardResult {
	return &OpenAIForwardResult{
		RequestID: requestID, ResponseID: responseID, Usage: usage,
		Model: meta.originalModel, BillingModel: meta.billingModel, UpstreamModel: meta.upstreamModel,
		UpstreamEndpoint: cursorAgentEndpoint, Stream: meta.stream, Duration: time.Since(startTime),
		FirstTokenMs: outcome.firstTokenMs,
	}
}

func cursorChatOpeningDisconnectResult(meta cursorChatMeta, startTime time.Time, openingErr error) *OpenAIForwardResult {
	requestID := "chatcmpl-" + uuid.NewString()
	result := cursorChatForwardResult(
		requestID, "chatcmpl-"+uuid.NewString(), meta, OpenAIUsage{}, cursorChatOutcome{}, startTime,
	)
	result.ClientDisconnect = true
	result.UpstreamFailed = cursorChatOpeningUpstreamFailed(openingErr)
	result.CaptureTerminalError = result.UpstreamFailed
	result.CaptureResponseComplete = false
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(nil, openingErr)
	return result
}

func cursorChatOpeningUpstreamFailed(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func cursorChatStreamErrorSSE(message string) []byte {
	payload, err := json.Marshal(gin.H{
		"error": gin.H{"type": "upstream_error", "message": message},
	})
	if err != nil {
		payload = []byte(`{"error":{"type":"upstream_error","message":"upstream connection error"}}`)
	}
	frame := make([]byte, 0, len(payload)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, payload...)
	frame = append(frame, '\n', '\n')
	return frame
}

func reportCursorChatStreamFailure(c *gin.Context, account *Account, err error) {
	mappedStatus := http.StatusBadGateway
	var agentErr *cursorpkg.AgentError
	if errors.As(err, &agentErr) {
		if agentErr.HTTPStatus > 0 {
			mappedStatus = agentErr.HTTPStatus
		} else if status := cursorpkg.ConnectCodeToHTTPStatus(agentErr.Code); status > 0 {
			mappedStatus = status
		}
	}
	message := sanitizeStreamError(err)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: PlatformCursor, AccountID: cursorAccountID(account), AccountName: cursorAccountName(account),
		UpstreamStatusCode: mappedStatus, Stage: "upstream", Scope: string(GatewayFailureScopeRequest),
		Reason: "cursor_stream_failed", Kind: "stream_error", Message: message,
	})
	MarkOpsStreamFailure(c, "upstream_error", "cursor_stream_failed", message, mappedStatus)
}

func cursorChatActualHTTPStatus(stream cursorChatEventStream, err error) int {
	var agentErr *cursorpkg.AgentError
	if errors.As(err, &agentErr) && agentErr.HasHTTPResponse && agentErr.ActualHTTPStatus > 0 {
		return agentErr.ActualHTTPStatus
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) && failoverErr.HasUpstreamHTTPResponse {
		return failoverErr.HTTPStatusForCapture()
	}
	if stream != nil {
		if response := stream.Response(); response != nil && response.StatusCode > 0 {
			return response.StatusCode
		}
	}
	return 0
}

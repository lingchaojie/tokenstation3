package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const cursorResponsesDoneSSE = "data: [DONE]\n\n"

type cursorChunkSynthesizer struct {
	completionID string
	created      int64
	model        string
	roleSent     bool
	emit         func(*apicompat.ChatCompletionsChunk)
}

func newCursorChunkSynthesizer(model string, emit func(*apicompat.ChatCompletionsChunk)) *cursorChunkSynthesizer {
	return &cursorChunkSynthesizer{
		completionID: "chatcmpl-" + uuid.NewString(),
		created:      time.Now().Unix(),
		model:        model,
		emit:         emit,
	}
}

func (synth *cursorChunkSynthesizer) chunk(
	delta apicompat.ChatDelta,
	finishReason *string,
	usage *apicompat.ChatUsage,
) *apicompat.ChatCompletionsChunk {
	return &apicompat.ChatCompletionsChunk{
		ID: synth.completionID, Object: "chat.completion.chunk", Created: synth.created, Model: synth.model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
		Usage:   usage,
	}
}

func (synth *cursorChunkSynthesizer) emitRole() {
	if synth == nil || synth.roleSent {
		return
	}
	synth.roleSent = true
	if synth.emit != nil {
		synth.emit(synth.chunk(apicompat.ChatDelta{Role: "assistant"}, nil, nil))
	}
}

func (synth *cursorChunkSynthesizer) onDelta(delta cursorDelta) {
	if synth == nil {
		return
	}
	synth.emitRole()
	if synth.emit == nil {
		return
	}
	switch delta.kind {
	case cursorDeltaText:
		text := delta.text
		synth.emit(synth.chunk(apicompat.ChatDelta{Content: &text}, nil, nil))
	case cursorDeltaReasoning:
		reasoning := delta.text
		synth.emit(synth.chunk(apicompat.ChatDelta{ReasoningContent: &reasoning}, nil, nil))
	case cursorDeltaToolCall:
		synth.emit(synth.chunk(apicompat.ChatDelta{
			ToolCalls: []apicompat.ChatToolCall{cursorToolCallDelta(delta)},
		}, nil, nil))
	}
}

func (synth *cursorChunkSynthesizer) finish(finishReason string, usage OpenAIUsage) {
	if synth == nil {
		return
	}
	synth.emitRole()
	if synth.emit == nil {
		return
	}
	reason := finishReason
	synth.emit(synth.chunk(apicompat.ChatDelta{}, &reason, cursorChatUsage(usage)))
}

func (s *OpenAIGatewayService) forwardCursorResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	var request apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeCursorResponsesJSONError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, errors.New("cursor: invalid Responses request")
	}
	if strings.TrimSpace(originalModel) == "" {
		originalModel = strings.TrimSpace(request.Model)
	}
	if originalModel == "" {
		writeCursorResponsesJSONError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, errors.New("cursor: Responses model is required")
	}

	effectiveTools, err := apicompat.EffectiveResponsesTools(&request)
	if err != nil {
		writeCursorResponsesJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid Responses request")
		return nil, errors.New("cursor: invalid Responses tools")
	}
	customTools := apicompat.CustomToolNames(effectiveTools)
	toolSearch := apicompat.HasToolSearchTool(effectiveTools)
	namespaceTools := apicompat.NamespaceToolNames(effectiveTools)
	s.recacheReasoningItemsFromInput(request.Input)
	chatRequest, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(&request, &apicompat.ResponsesToChatOptions{
		ReasoningContentByID: s.reasoningContentByID,
	})
	if err != nil {
		writeCursorResponsesJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid Responses request")
		return nil, errors.New("cursor: invalid Responses request")
	}

	clientStream := request.Stream || reqStream
	meta := s.resolveCursorChatMeta(account, originalModel, "", clientStream)
	meta.maxOutputTokens = cursorRequestOutputLimit(chatRequest)
	if request.MaxOutputTokens != nil && *request.MaxOutputTokens > 0 {
		meta.maxOutputTokens = *request.MaxOutputTokens
	}
	params, input, err := buildCursorAgentRun(account, meta.upstreamModel, chatRequest)
	if err != nil {
		writeCursorResponsesJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid Responses request")
		return nil, errors.New("cursor: invalid Responses request")
	}
	if wireModel := strings.TrimSpace(params.Model); wireModel != "" {
		meta.upstreamModel = wireModel
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, clientStream)
	if !clientStream {
		stream, openErr := s.openCursorAgentStream(upstreamCtx, c, account, params)
		releaseUpstreamCtx()
		if openErr != nil {
			return nil, openErr
		}
		defer func() { _ = stream.Close() }()
		result, forwardErr := s.bufferCursorResponses(
			c, account, stream, meta, input, startTime, customTools, toolSearch, namespaceTools,
		)
		applyCursorResponsesRequestMetadata(result, &request)
		return result, forwardErr
	}

	opening := newCursorChatOpeningBridge(upstreamCtx, cursorChatCallerContext(ctx, c))
	stream, openErr := s.openCursorAgentStream(opening.ctx, c, account, params)
	callerCanceled := opening.handoff()
	if openErr != nil {
		opening.release()
		releaseUpstreamCtx()
		if callerCanceled {
			return cursorProtocolOpeningDisconnectResult(meta, startTime, openErr, "resp-"), nil
		}
		return nil, openErr
	}
	if callerCanceled {
		_ = stream.Close()
		opening.release()
		releaseUpstreamCtx()
		return cursorProtocolOpeningDisconnectResult(meta, startTime, nil, "resp-"), nil
	}
	defer releaseUpstreamCtx()
	defer opening.release()
	defer func() { _ = stream.Close() }()
	result, forwardErr := s.streamCursorResponses(
		c, account, stream, meta, input, startTime, customTools, toolSearch, namespaceTools,
	)
	applyCursorResponsesRequestMetadata(result, &request)
	return result, forwardErr
}

func applyCursorResponsesRequestMetadata(result *OpenAIForwardResult, request *apicompat.ResponsesRequest) {
	if result == nil || request == nil {
		return
	}
	if request.Reasoning != nil {
		if effort := strings.TrimSpace(request.Reasoning.Effort); effort != "" {
			result.ReasoningEffort = &effort
		}
	}
	if tier := strings.TrimSpace(request.ServiceTier); tier != "" {
		result.ServiceTier = &tier
	}
}

func (s *OpenAIGatewayService) bufferCursorResponses(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
	customTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "resp-")
	completionID := "chatcmpl-" + uuid.NewString()
	outcome, upstreamErr := consumeCursorAgentEvents(stream.Events(), startTime, meta.maxOutputTokens, nil)
	if upstreamErr != nil {
		return nil, s.cursorAgentFailure(c, account, upstreamErr)
	}
	usage := resolveCursorUsage(input, outcome)
	chatResponse := cursorChatCompletionsResponse(completionID, meta.originalModel, outcome, usage)
	response := apicompat.ChatCompletionsResponseToResponses(
		&chatResponse, meta.originalModel, customTools, toolSearch, namespaceTools,
	)
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, errors.New("cursor: marshal Responses response")
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(http.StatusOK)
	n, writeErr := writeCursorDeliveryBytes(c, payload)
	if n > 0 {
		MarkResponseCommitted(c)
	}

	result := cursorChatForwardResult(requestID, response.ID, meta, usage, outcome, startTime)
	result.ClientDisconnect = writeErr != nil || n != len(payload)
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, nil)
	return result, nil
}

func (s *OpenAIGatewayService) streamCursorResponses(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
	customTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "resp-")
	delivery := cursorChatSSEDelivery{c: c}
	relay := newCursorChatEventRelay(c, stream, meta.disconnectDrainTimeout)
	state := apicompat.NewChatCompletionsToResponsesStreamState(meta.originalModel)
	state.CustomTools = customTools
	state.ToolSearchDeclared = toolSearch
	state.NamespaceTools = namespaceTools

	writeEvents := func(events []apicompat.ResponsesStreamEvent) {
		for _, event := range events {
			frame, err := apicompat.ResponsesEventToSSE(event)
			if err != nil || delivery.stopped {
				continue
			}
			delivery.write([]byte(frame))
			if delivery.clientDisconnected {
				relay.Disconnect()
			}
		}
	}
	synth := newCursorChunkSynthesizer(meta.originalModel, func(chunk *apicompat.ChatCompletionsChunk) {
		writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(chunk, state))
	})
	outcome, upstreamErr := consumeCursorAgentEvents(relay.Events(), startTime, meta.maxOutputTokens, func(delta cursorDelta) error {
		if relay.Disconnected() || cursorChatRequestCanceled(c) {
			relay.Disconnect()
			delivery.disconnect()
			return nil
		}
		if !delivery.stopped {
			synth.onDelta(delta)
		}
		return nil
	})
	if relay.Disconnected() || cursorChatRequestCanceled(c) {
		relay.Disconnect()
		delivery.disconnect()
	}
	relay.Stop()
	disconnectedBeforeTerminal := delivery.clientDisconnected || relay.Disconnected()
	if upstreamErr != nil && !delivery.committed && !disconnectedBeforeTerminal {
		return nil, s.cursorAgentFailure(c, account, upstreamErr)
	}

	usage := resolveCursorUsage(input, outcome)
	if upstreamErr != nil {
		reportCursorChatStreamFailure(c, account, upstreamErr)
		if !delivery.stopped && delivery.write(cursorResponsesStreamErrorSSE()) {
			delivery.write([]byte(cursorResponsesDoneSSE))
		}
	} else if !delivery.stopped {
		synth.finish(outcome.finishReason, usage)
		writeEvents(apicompat.FinalizeChatCompletionsResponsesStream(state))
		if !delivery.stopped {
			delivery.write([]byte(cursorResponsesDoneSSE))
		}
	}

	result := cursorChatForwardResult(requestID, state.ResponseID, meta, usage, outcome, startTime)
	result.ClientDisconnect = delivery.clientDisconnected || relay.Disconnected()
	result.UpstreamFailed = upstreamErr != nil
	result.CaptureTerminalError = upstreamErr != nil
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, upstreamErr)
	return result, nil
}

func (s *OpenAIGatewayService) forwardCursorAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	var request apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeCursorAnthropicJSONError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, errors.New("cursor: invalid Anthropic request")
	}
	originalModel := strings.TrimSpace(request.Model)
	if originalModel == "" {
		writeCursorAnthropicJSONError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, errors.New("cursor: Anthropic model is required")
	}
	if request.MaxTokens <= 0 {
		writeCursorAnthropicJSONError(c, http.StatusBadRequest, "invalid_request_error", "max_tokens must be positive")
		return nil, errors.New("cursor: Anthropic max_tokens must be positive")
	}
	chatRequest, err := apicompat.AnthropicToChatCompletionsRequest(&request)
	if err != nil {
		writeCursorAnthropicJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid Messages request")
		return nil, errors.New("cursor: invalid Anthropic request")
	}

	meta := s.resolveCursorChatMeta(account, originalModel, defaultMappedModel, request.Stream)
	meta.maxOutputTokens = request.MaxTokens
	params, input, err := buildCursorAgentRun(account, meta.upstreamModel, chatRequest)
	if err != nil {
		writeCursorAnthropicJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid Messages request")
		return nil, errors.New("cursor: invalid Anthropic request")
	}
	if wireModel := strings.TrimSpace(params.Model); wireModel != "" {
		meta.upstreamModel = wireModel
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, request.Stream)
	if !request.Stream {
		stream, openErr := s.openCursorAgentStream(upstreamCtx, c, account, params)
		releaseUpstreamCtx()
		if openErr != nil {
			return nil, openErr
		}
		defer func() { _ = stream.Close() }()
		result, forwardErr := s.bufferCursorAnthropic(c, account, stream, meta, input, startTime)
		applyCursorAnthropicRequestMetadata(result, chatRequest)
		return result, forwardErr
	}

	opening := newCursorChatOpeningBridge(upstreamCtx, cursorChatCallerContext(ctx, c))
	stream, openErr := s.openCursorAgentStream(opening.ctx, c, account, params)
	callerCanceled := opening.handoff()
	if openErr != nil {
		opening.release()
		releaseUpstreamCtx()
		if callerCanceled {
			return cursorProtocolOpeningDisconnectResult(meta, startTime, openErr, "msg-"), nil
		}
		return nil, openErr
	}
	if callerCanceled {
		_ = stream.Close()
		opening.release()
		releaseUpstreamCtx()
		return cursorProtocolOpeningDisconnectResult(meta, startTime, nil, "msg-"), nil
	}
	defer releaseUpstreamCtx()
	defer opening.release()
	defer func() { _ = stream.Close() }()
	result, forwardErr := s.streamCursorAnthropic(c, account, stream, meta, input, startTime)
	applyCursorAnthropicRequestMetadata(result, chatRequest)
	return result, forwardErr
}

func applyCursorAnthropicRequestMetadata(result *OpenAIForwardResult, request *apicompat.ChatCompletionsRequest) {
	if result == nil || request == nil {
		return
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		result.ReasoningEffort = &effort
	}
}

func (s *OpenAIGatewayService) bufferCursorAnthropic(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "msg-")
	completionID := "chatcmpl-" + uuid.NewString()
	outcome, upstreamErr := consumeCursorAgentEvents(stream.Events(), startTime, meta.maxOutputTokens, nil)
	if upstreamErr != nil {
		return nil, s.cursorAgentFailure(c, account, upstreamErr)
	}
	usage := resolveCursorUsage(input, outcome)
	chatResponse := cursorChatCompletionsResponse(completionID, meta.originalModel, outcome, usage)
	response := apicompat.ChatCompletionsResponseToAnthropic(&chatResponse, meta.originalModel)
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, errors.New("cursor: marshal Anthropic response")
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(http.StatusOK)
	n, writeErr := writeCursorDeliveryBytes(c, payload)
	if n > 0 {
		MarkResponseCommitted(c)
	}

	result := cursorChatForwardResult(requestID, response.ID, meta, usage, outcome, startTime)
	result.ClientDisconnect = writeErr != nil || n != len(payload)
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, nil)
	return result, nil
}

func (s *OpenAIGatewayService) streamCursorAnthropic(
	c *gin.Context,
	account *Account,
	stream cursorChatEventStream,
	meta cursorChatMeta,
	input cursorInputEstimate,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := cursorAgentRequestID(stream, "msg-")
	delivery := cursorChatSSEDelivery{c: c}
	relay := newCursorChatEventRelay(c, stream, meta.disconnectDrainTimeout)
	state := apicompat.NewChatCompletionsToAnthropicStreamState(meta.originalModel)

	writeEvents := func(events []apicompat.AnthropicStreamEvent) {
		for _, event := range events {
			frame, err := apicompat.ResponsesAnthropicEventToSSE(event)
			if err != nil || delivery.stopped {
				continue
			}
			delivery.write([]byte(frame))
			if delivery.clientDisconnected {
				relay.Disconnect()
			}
		}
	}
	synth := newCursorChunkSynthesizer(meta.originalModel, func(chunk *apicompat.ChatCompletionsChunk) {
		writeEvents(apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, state))
	})
	outcome, upstreamErr := consumeCursorAgentEvents(relay.Events(), startTime, meta.maxOutputTokens, func(delta cursorDelta) error {
		if relay.Disconnected() || cursorChatRequestCanceled(c) {
			relay.Disconnect()
			delivery.disconnect()
			return nil
		}
		if !delivery.stopped {
			synth.onDelta(delta)
		}
		return nil
	})
	if relay.Disconnected() || cursorChatRequestCanceled(c) {
		relay.Disconnect()
		delivery.disconnect()
	}
	relay.Stop()
	disconnectedBeforeTerminal := delivery.clientDisconnected || relay.Disconnected()
	if upstreamErr != nil && !delivery.committed && !disconnectedBeforeTerminal {
		return nil, s.cursorAgentFailure(c, account, upstreamErr)
	}

	usage := resolveCursorUsage(input, outcome)
	if upstreamErr != nil {
		reportCursorChatStreamFailure(c, account, upstreamErr)
		if !delivery.stopped {
			delivery.write(cursorAnthropicStreamErrorSSE())
		}
	} else if !delivery.stopped {
		synth.finish(outcome.finishReason, usage)
		writeEvents(apicompat.FinalizeChatCompletionsAnthropicStream(state))
	}

	result := cursorChatForwardResult(requestID, state.ResponseID, meta, usage, outcome, startTime)
	result.ClientDisconnect = delivery.clientDisconnected || relay.Disconnected()
	result.UpstreamFailed = upstreamErr != nil
	result.CaptureTerminalError = upstreamErr != nil
	result.CaptureResponseComplete = outcome.providerTerminal
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(stream, upstreamErr)
	return result, nil
}

func cursorProtocolOpeningDisconnectResult(
	meta cursorChatMeta,
	startTime time.Time,
	openingErr error,
	prefix string,
) *OpenAIForwardResult {
	requestID := prefix + uuid.NewString()
	result := cursorChatForwardResult(requestID, prefix+uuid.NewString(), meta, OpenAIUsage{}, cursorChatOutcome{}, startTime)
	result.ClientDisconnect = true
	result.UpstreamFailed = cursorChatOpeningUpstreamFailed(openingErr)
	result.CaptureTerminalError = result.UpstreamFailed
	result.CaptureResponseComplete = false
	result.UpstreamHTTPStatus = cursorChatActualHTTPStatus(nil, openingErr)
	return result
}

func writeCursorResponsesJSONError(c *gin.Context, status int, code, message string) {
	writeCursorProtocolJSON(c, status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func writeCursorAnthropicJSONError(c *gin.Context, status int, errorType, message string) {
	writeCursorProtocolJSON(c, status, gin.H{
		"type": "error", "error": gin.H{"type": errorType, "message": message},
	})
}

func writeCursorProtocolJSON(c *gin.Context, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte(`{"error":{"message":"invalid request"}}`)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(status)
	MarkResponseCommitted(c)
	_, _ = writeCursorDeliveryBytes(c, payload)
}

func cursorResponsesStreamErrorSSE() []byte {
	payload, err := json.Marshal(struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Type: "error", Code: "server_error", Message: "Cursor upstream request failed"})
	if err != nil {
		payload = []byte(`{"type":"error","code":"server_error","message":"Cursor upstream request failed"}`)
	}
	return []byte(fmt.Sprintf("event: error\ndata: %s\n\n", payload))
}

func cursorAnthropicStreamErrorSSE() []byte {
	payload, err := json.Marshal(gin.H{
		"type": "error", "error": gin.H{"type": "api_error", "message": "Cursor upstream request failed"},
	})
	if err != nil {
		payload = []byte(`{"type":"error","error":{"type":"api_error","message":"Cursor upstream request failed"}}`)
	}
	return []byte(fmt.Sprintf("event: error\ndata: %s\n\n", payload))
}

var _ cursorChatEventStream = (*cursorpkg.AgentStream)(nil)

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
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// forwardResponsesViaRawChatCompletions serves /v1/responses clients through an
// upstream that only supports /v1/chat/completions.
func (s *OpenAIGatewayService) forwardResponsesViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	clientStream := responsesReq.Stream
	serviceTier := extractOpenAIServiceTierFromBody(body)
	// custom 工具（如 codex 的 exec）降级为 function 工具转发，回程需按名字还原为
	// custom_tool_call 项，先记下名字集合；tool_search 工具同理，回程还原为
	// tool_search_call 项；namespace 子工具（如 MCP 工具）摊平转发，回程按映射还原
	// 为带 namespace 字段的 function_call 项。
	effectiveTools, err := apicompat.EffectiveResponsesTools(&responsesReq)
	if err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("resolve responses tools: %w", err)
	}
	customTools := apicompat.CustomToolNames(effectiveTools)
	toolSearch := apicompat.HasToolSearchTool(effectiveTools)
	namespaceTools := apicompat.NamespaceToolNames(effectiveTools)

	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	// 国产模型默认 effort 补充：需要 mappedModel 判定，推迟到 billingModel 算出之后。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)
	chatReq.Model = upstreamModel
	if clientStream {
		chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions fallback request: %w", err)
	}
	chatBody, err = s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, chatBody)
	if err != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(err, &blocked) {
			writeOpenAIFastPolicyBlockedResponse(c, blocked)
		}
		return nil, err
	}
	if serviceTier == nil {
		serviceTier = extractOpenAIServiceTierFromBody(chatBody)
	}

	logger.L().Debug("openai responses: forwarding via raw chat completions",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// Build and send upstream request via the shared CC pipeline
	apiKey, targetURL, err := s.resolveCCFallbackTarget(account)
	if err != nil {
		return nil, err
	}
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, chatBody, clientStream, apiKey, account.GetOpenAIUserAgent(), "")
	if err != nil {
		return nil, err
	}
	streamOwnsResponseBody := false
	defer func() {
		if !streamOwnsResponseBody {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		finishOpenAIHTTPCapture(resp)
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return s.handleErrorResponse(ctx, resp, c, account, chatBody, billingModel)
	}

	var result *OpenAIForwardResult
	var forwardErr error
	if clientStream {
		streamOwnsResponseBody = true
		result, forwardErr = s.streamChatCompletionsAsResponses(ctx, c, resp, originalModel, customTools, toolSearch, namespaceTools, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	} else {
		result, forwardErr = s.bufferChatCompletionsAsResponses(c, resp, originalModel, customTools, toolSearch, namespaceTools, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	}
	finishOpenAIHTTPCapture(resp)
	if result != nil && forwardErr != nil {
		result.CaptureTerminalError = true
	}
	if result != nil {
		s.applyOpenAIHTTPSuccessCapture(c, account, result)
	}
	return finalizeOpenAIForwardResult(c, result, chatBody), forwardErr
}

func (s *OpenAIGatewayService) bufferChatCompletionsAsResponses(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	customTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	ccResp, parsedUsage, sawUsage, err := s.readCCUpstreamJSONResponse(c, resp, writeOpenAIResponsesFallbackError)
	if err != nil {
		return nil, err
	}
	responsesResp := apicompat.ChatCompletionsResponseToResponses(ccResp, originalModel, customTools, toolSearch, namespaceTools)
	if sawUsage {
		responsesResp.Usage = responsesUsageFromCCUsage(parsedUsage.Usage, parsedUsage)
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, responsesResp)

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           parsedUsage.Usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) streamChatCompletionsAsResponses(
	ctx context.Context,
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	customTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)

	state := apicompat.NewChatCompletionsToResponsesStreamState(originalModel)
	state.CustomTools = customTools
	state.ToolSearchDeclared = toolSearch
	state.NamespaceTools = namespaceTools
	clientDisconnected := false
	var staged stagedConvertedStream
	defer func() {
		if err := staged.close(); err != nil {
			logger.L().Warn("openai responses chat fallback: converted stage cleanup failed",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}()

	writeEvents := func(events []apicompat.ResponsesStreamEvent, commit bool) (bool, error) {
		if len(events) == 0 {
			return staged.committed, nil
		}
		var wire strings.Builder
		for _, event := range events {
			sse, err := apicompat.ResponsesEventToSSE(event)
			if err != nil {
				return staged.committed, fmt.Errorf("marshal responses stream event %q: %w", event.Type, err)
			}
			_, _ = wire.WriteString(sse)
		}
		if clientDisconnected {
			return staged.committed, nil
		}
		if err := staged.write(c, writeStreamHeaders, wire.String(), commit); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				return staged.committed, err
			}
			clientDisconnected = true
			logger.L().Debug("openai responses chat fallback: client disconnected, continuing to drain upstream for billing",
				zap.Error(clientWriteErr),
				zap.String("request_id", requestID),
			)
		}
		return staged.committed, nil
	}

	scan := s.scanCCStream(ctx, resp, "openai responses chat fallback", requestID, startTime, func(chunk *apicompat.ChatCompletionsChunk) (bool, error) {
		events := apicompat.ChatCompletionsChunkToResponsesEvents(chunk, state)
		return writeEvents(events, responsesConvertedEventsHaveSemanticOutput(events))
	})

	if scan.Err != nil {
		if !scan.SawOutput {
			return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream read failed before semantic output")
		}
		return &OpenAIForwardResult{
			RequestID:       requestID,
			Usage:           scan.Usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   upstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     serviceTier,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    scan.FirstTokenMs,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}
	if !scan.SawDone {
		logCCStreamMissingDoneSentinel("openai responses chat fallback", requestID)
		if !scan.SawOutput {
			return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream ended before [DONE]")
		}
		return &OpenAIForwardResult{RequestID: requestID, Usage: scan.Usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs}, errors.New("upstream stream ended without [DONE]")
	}
	if !scan.SawProviderPayload {
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream contained only [DONE]")
	}
	if scan.SawUsage {
		// Keep response.completed aligned with the generic scanner used for
		// billing, including compatible cache aliases and explicit nested zero
		// precedence lost by the narrower ChatUsage structure.
		state.Usage = responsesUsageFromCCUsage(scan.Usage, scan.UsageFields)
	}

	finalSemantic, finalErr := writeEvents(apicompat.FinalizeChatCompletionsResponsesStream(state), true)
	if finalSemantic && !scan.SawOutput {
		scan.SawOutput = true
		ms := int(time.Since(startTime).Milliseconds())
		scan.FirstTokenMs = &ms
	}
	if finalErr != nil {
		if !scan.SawOutput {
			return nil, &UpstreamFailoverError{Stage: GatewayFailureStageInference}
		}
		return &OpenAIForwardResult{RequestID: requestID, Usage: scan.Usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs}, fmt.Errorf("finalize converted response stream: %w", finalErr)
	}
	if !clientDisconnected {
		if err := staged.write(c, writeStreamHeaders, "data: [DONE]\n\n", true); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if errors.As(err, &clientWriteErr) {
				clientDisconnected = true
			} else if !scan.SawOutput {
				return nil, &UpstreamFailoverError{Stage: GatewayFailureStageInference}
			} else {
				return &OpenAIForwardResult{RequestID: requestID, Usage: scan.Usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs}, fmt.Errorf("write converted response terminator: %w", err)
			}
		}
	}

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           scan.Usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          true,
		Duration:        time.Since(startTime),
		FirstTokenMs:    scan.FirstTokenMs,
	}, nil
}

func responsesConvertedEventsHaveSemanticOutput(events []apicompat.ResponsesStreamEvent) bool {
	for _, event := range events {
		switch strings.TrimSpace(event.Type) {
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			if event.Delta != "" {
				return true
			}
		case "response.output_item.added":
			if event.Item == nil {
				continue
			}
			switch strings.TrimSpace(event.Item.Type) {
			case "function_call", "custom_tool_call", "tool_search_call":
				if strings.TrimSpace(event.Item.Name) != "" || event.Item.Arguments != "" || event.Item.Input != "" {
					return true
				}
			}
		}
	}
	return false
}

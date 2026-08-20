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
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// forwardAnthropicViaRawChatCompletions serves /v1/messages clients through
// an OpenAI-compatible upstream that only supports /v1/chat/completions.
//
// Conversion chain (direct, no Responses intermediary):
//
//	Request:  Anthropic Messages → Chat Completions (AnthropicToChatCompletionsRequest)
//	Response: CC chunk/response → Anthropic events/response (direct bridge)
//
// This is the /v1/messages counterpart of forwardResponsesViaRawChatCompletions
// (which serves /v1/responses clients). Unlike the Responses path, the direct
// bridge skips the Responses API intermediate representation entirely — every
// streaming token runs through a single state machine instead of two.
func (s *OpenAIGatewayService) forwardAnthropicViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse Anthropic request
	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	originalModel := anthropicReq.Model
	if strings.TrimSpace(originalModel) == "" {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	applyOpenAICompatModelNormalization(&anthropicReq)
	clientStream := anthropicReq.Stream

	// 2. Anthropic → Chat Completions (direct, no Responses intermediary)
	chatReq, err := apicompat.AnthropicToChatCompletionsRequest(&anthropicReq)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert anthropic to chat completions: %w", err)
	}

	billingModel := resolveOpenAIForwardModel(account, anthropicReq.Model, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	chatReq.Model = upstreamModel
	chatReq.ReasoningEffort = openAICompatAnthropicReasoningEffort(&anthropicReq, upstreamModel, chatReq.ReasoningEffort)
	chatReq.Stream = clientStream
	if clientStream {
		chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}

	convertedEffort := chatReq.ReasoningEffort
	reasoningEffort := &convertedEffort
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}
	if normalizedBody, normalized := NormalizeGLMOpenAIReasoningEffort(chatBody, upstreamModel); normalized {
		chatBody = normalizedBody
	}
	if account.Platform == PlatformOpenAI {
		if policyBody, changed := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, chatBody); changed {
			chatBody = policyBody
			if effectiveEffort := strings.TrimSpace(gjson.GetBytes(chatBody, "reasoning_effort").String()); effectiveEffort != "" {
				reasoningEffort = &effectiveEffort
			}
		}
	}
	// Unlike forwardResponsesViaRawChatCompletions, applyOpenAIFastPolicyToBody
	// is intentionally skipped: Anthropic Messages bodies carry no service_tier,
	// so the converted Chat Completions body never contains one and the policy
	// would always be a no-op on this path.

	logger.L().Debug("openai messages: forwarding via raw chat completions",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// 3. Build and send upstream request via the shared CC pipeline
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

	// 4. Handle error responses
	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		finishOpenAIHTTPCapture(resp)
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// Non-failover error: return Anthropic-formatted error to client via the
		// shared compat handler (passthrough rules, ops recording, cyber_policy).
		return s.handleAnthropicErrorResponse(resp, c, account, billingModel)
	}
	// 5. Convert response
	var result *OpenAIForwardResult
	var forwardErr error
	if clientStream {
		streamOwnsResponseBody = true
		result, forwardErr = s.streamChatCompletionsAsAnthropic(ctx, c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	} else {
		result, forwardErr = s.bufferChatCompletionsAsAnthropic(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
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

func (s *OpenAIGatewayService) bufferChatCompletionsAsAnthropic(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	ccResp, parsedUsage, _, err := s.readCCUpstreamJSONResponse(c, resp, writeAnthropicError)
	if err != nil {
		return nil, err
	}
	anthropicResp := apicompat.ChatCompletionsResponseToAnthropic(ccResp, originalModel)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, anthropicResp)

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

func (s *OpenAIGatewayService) streamChatCompletionsAsAnthropic(
	ctx context.Context,
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)

	anthropicState := apicompat.NewChatCompletionsToAnthropicStreamState(originalModel)
	clientDisconnected := false
	var staged stagedConvertedStream
	defer func() {
		if err := staged.close(); err != nil {
			logger.L().Warn("openai messages chat fallback: converted stage cleanup failed",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}()

	// 与 responses 兄弟不同：客户端断开后仍继续做事件转换（喂 anthropicState），
	// 仅跳过写出，保证 finalize 阶段的 usage 汇总不受断开影响。
	emitChunk := func(chunk *apicompat.ChatCompletionsChunk) (bool, error) {
		// CC chunk → Anthropic events (direct, single state machine)
		anthropicEvents, err := apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, anthropicState)
		if err != nil {
			return staged.committed, fmt.Errorf("convert upstream Chat Completions stream: %w", err)
		}
		semanticOutput := anthropicConvertedEventsHaveSemanticOutput(anthropicEvents)
		var wire strings.Builder
		for _, aEvt := range anthropicEvents {
			sse, err := apicompat.ResponsesAnthropicEventToSSE(aEvt)
			if err != nil {
				return staged.committed, fmt.Errorf("marshal anthropic stream event %q: %w", aEvt.Type, err)
			}
			_, _ = wire.WriteString(sse)
		}
		if clientDisconnected {
			return staged.committed, nil
		}
		if err := staged.write(c, writeStreamHeaders, wire.String(), semanticOutput); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				return staged.committed, err
			}
			clientDisconnected = true
		}
		return staged.committed, nil
	}

	scan := s.scanCCStream(ctx, resp, "openai messages chat fallback", requestID, startTime, emitChunk)
	usage := scan.Usage

	if scan.Err != nil {
		if !scan.SawOutput {
			return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream read failed before semantic output")
		}
		// Broken upstream read: skip finalization so no synthetic message_stop
		// masks the truncation, and surface the error to flag usage incomplete
		// (mirrors forwardResponsesViaRawChatCompletions).
		return &OpenAIForwardResult{
			RequestID:        requestID,
			Usage:            usage,
			Model:            originalModel,
			BillingModel:     billingModel,
			UpstreamModel:    upstreamModel,
			ReasoningEffort:  reasoningEffort,
			ServiceTier:      serviceTier,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     scan.FirstTokenMs,
			ClientDisconnect: clientDisconnected,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}
	if !scan.SawDone {
		logCCStreamMissingDoneSentinel("openai messages chat fallback", requestID)
		if !scan.SawOutput {
			return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream ended before [DONE]")
		}
		return &OpenAIForwardResult{RequestID: requestID, Usage: usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected}, errors.New("upstream stream ended without [DONE]")
	}
	if !scan.SawProviderPayload {
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Chat Completions stream contained only [DONE]")
	}
	// Finalize: close open blocks + emit message_delta/message_stop.
	finalEvents := apicompat.FinalizeChatCompletionsAnthropicStream(anthropicState)
	finalSemantic := anthropicConvertedEventsHaveSemanticOutput(finalEvents)
	var wire strings.Builder
	for _, aEvt := range finalEvents {
		sse, err := apicompat.ResponsesAnthropicEventToSSE(aEvt)
		if err != nil {
			if finalSemantic && !scan.SawOutput {
				scan.SawOutput = true
				ms := int(time.Since(startTime).Milliseconds())
				scan.FirstTokenMs = &ms
			}
			if !scan.SawOutput {
				return nil, &UpstreamFailoverError{Stage: GatewayFailureStageInference}
			}
			return &OpenAIForwardResult{RequestID: requestID, Usage: usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected}, fmt.Errorf("marshal final anthropic stream event %q: %w", aEvt.Type, err)
		}
		_, _ = wire.WriteString(sse)
	}
	if finalSemantic && !scan.SawOutput {
		scan.SawOutput = true
		ms := int(time.Since(startTime).Milliseconds())
		scan.FirstTokenMs = &ms
	}
	if !clientDisconnected {
		if err := staged.write(c, writeStreamHeaders, wire.String(), true); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				if !scan.SawOutput {
					return nil, &UpstreamFailoverError{Stage: GatewayFailureStageInference}
				}
				return &OpenAIForwardResult{RequestID: requestID, Usage: usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, ReasoningEffort: reasoningEffort, ServiceTier: serviceTier, Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected}, fmt.Errorf("finalize converted anthropic stream: %w", err)
			}
			clientDisconnected = true
		}
	}

	return &OpenAIForwardResult{
		RequestID:        requestID,
		Usage:            usage,
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		ReasoningEffort:  reasoningEffort,
		ServiceTier:      serviceTier,
		Stream:           true,
		Duration:         time.Since(startTime),
		FirstTokenMs:     scan.FirstTokenMs,
		ClientDisconnect: clientDisconnected,
	}, nil
}

func anthropicConvertedEventsHaveSemanticOutput(events []apicompat.AnthropicStreamEvent) bool {
	for _, event := range events {
		switch strings.TrimSpace(event.Type) {
		case "content_block_delta":
			if event.Delta == nil {
				continue
			}
			switch strings.TrimSpace(event.Delta.Type) {
			case "text_delta":
				if event.Delta.Text != "" {
					return true
				}
			case "thinking_delta":
				if event.Delta.Thinking != "" {
					return true
				}
			case "input_json_delta":
				if event.Delta.PartialJSON != "" {
					return true
				}
			}
		case "content_block_start":
			if event.ContentBlock != nil && strings.TrimSpace(event.ContentBlock.Type) == "tool_use" &&
				strings.TrimSpace(event.ContentBlock.Name) != "" {
				return true
			}
		}
	}
	return false
}

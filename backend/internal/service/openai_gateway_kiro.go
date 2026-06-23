package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const kiroMaxOutputTokens = 32000

type kiroPayload struct {
	ConversationState kiroConversationState `json:"conversationState"`
	ProfileARN        string                `json:"profileArn,omitempty"`
	InferenceConfig   *kiroInferenceConfig  `json:"inferenceConfig,omitempty"`
}

type kiroInferenceConfig struct {
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

type kiroConversationState struct {
	ChatTriggerType string               `json:"chatTriggerType"`
	ConversationID  string               `json:"conversationId"`
	CurrentMessage  kiroCurrentMessage   `json:"currentMessage"`
	History         []kiroHistoryMessage `json:"history,omitempty"`
}

type kiroCurrentMessage struct {
	UserInputMessage kiroUserInputMessage `json:"userInputMessage"`
}

type kiroHistoryMessage struct {
	UserInputMessage         *kiroUserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *kiroAssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type kiroUserInputMessage struct {
	Content                 string                  `json:"content"`
	ModelID                 string                  `json:"modelId"`
	Origin                  string                  `json:"origin"`
	UserInputMessageContext *kiroUserMessageContext `json:"userInputMessageContext,omitempty"`
}

type kiroUserMessageContext struct {
	ToolResults []kiroToolResult `json:"toolResults,omitempty"`
	Tools       []kiroTool       `json:"tools,omitempty"`
}

type kiroToolResult struct {
	Content   []kiroTextContent `json:"content"`
	Status    string            `json:"status"`
	ToolUseID string            `json:"toolUseId"`
}

type kiroTextContent struct {
	Text string `json:"text"`
}

type kiroTool struct {
	ToolSpecification kiroToolSpecification `json:"toolSpecification"`
}

type kiroToolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema kiroInputSchema `json:"inputSchema"`
}

type kiroInputSchema struct {
	JSON any `json:"json"`
}

type kiroAssistantResponseMessage struct {
	Content  string        `json:"content"`
	ToolUses []kiroToolUse `json:"toolUses,omitempty"`
}

type kiroToolUse struct {
	ToolUseID string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

func (s *OpenAIGatewayService) forwardAnthropicViaKiro(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	anthropicReq *apicompat.AnthropicRequest,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	payload, err := buildKiroPayloadFromAnthropic(anthropicReq, upstreamModel, account.GetKiroProfileARN(), kiroEndpointForAccount(account).Origin)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	resp, err := s.sendKiroRequest(ctx, c, account, payload, upstreamModel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return s.handleKiroAnthropicError(ctx, c, account, resp, upstreamModel, billingModel)
	}
	if anthropicReq.Stream {
		return s.streamKiroAsAnthropic(c, resp, originalModel, billingModel, upstreamModel, startTime)
	}
	return s.bufferKiroAsAnthropic(c, resp, originalModel, billingModel, upstreamModel, startTime)
}

func (s *OpenAIGatewayService) forwardAsKiroChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := ResolveKiroModelID(billingModel)

	responsesReq, err := apicompat.ChatCompletionsToResponses(&chatReq)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Model = billingModel
	anthropicReq.Stream = chatReq.Stream

	payload, err := buildKiroPayloadFromAnthropic(anthropicReq, upstreamModel, account.GetKiroProfileARN(), kiroEndpointForAccount(account).Origin)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	resp, err := s.sendKiroRequest(ctx, c, account, payload, upstreamModel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return s.handleKiroChatError(ctx, c, account, resp, upstreamModel)
	}
	if chatReq.Stream {
		return s.streamKiroAsChatCompletions(c, resp, originalModel, billingModel, upstreamModel, startTime)
	}
	return s.bufferKiroAsChatCompletions(c, resp, originalModel, billingModel, upstreamModel, startTime)
}

func (s *OpenAIGatewayService) forwardResponsesViaKiro(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	clientStream := responsesReq.Stream
	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)
	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := ResolveKiroModelID(billingModel)

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Model = billingModel
	anthropicReq.Stream = clientStream

	payload, err := buildKiroPayloadFromAnthropic(anthropicReq, upstreamModel, account.GetKiroProfileARN(), kiroEndpointForAccount(account).Origin)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	resp, err := s.sendKiroRequest(ctx, c, account, payload, upstreamModel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return s.handleKiroResponsesError(ctx, c, account, resp, upstreamModel)
	}
	if clientStream {
		return s.streamKiroAsResponses(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	}
	return s.bufferKiroAsResponses(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
}

func (s *OpenAIGatewayService) sendKiroRequest(ctx context.Context, c *gin.Context, account *Account, payload []byte, upstreamModel string) (*http.Response, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, fmt.Errorf("kiro upstream client is not configured")
	}
	_ = s.refreshKiroAccessTokenIfNeeded(ctx, account)
	accessToken := account.GetKiroAccessToken()
	if accessToken == "" {
		return nil, fmt.Errorf("account %d missing Kiro access_token", account.ID)
	}
	endpoint := kiroEndpointForAccount(account)
	validatedURL, err := s.validateUpstreamBaseURL(endpoint.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid kiro endpoint: %w", err)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, validatedURL, bytes.NewReader(payload))
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build kiro request: %w", err)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", KiroContentType)
	req.Header.Set("Accept", KiroAcceptStream)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", KiroUserAgent)
	req.Header.Set("X-Amz-User-Agent", KiroFullUserAgent)
	req.Header.Set("X-Amz-Target", endpoint.Target)
	req.Header.Set("X-Amzn-Bedrock-Origin", endpoint.Origin)
	if c != nil {
		if requestID := c.GetHeader("X-Request-ID"); requestID != "" {
			req.Header.Set("X-Request-ID", requestID)
		}
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			Kind:        "request_error",
			Message:     safeErr,
		})
		return nil, fmt.Errorf("kiro upstream request failed for %s: %s", upstreamModel, safeErr)
	}
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && account.GetKiroRefreshToken() != "" {
		_ = resp.Body.Close()
		if refreshErr := s.refreshKiroAccessToken(ctx, account); refreshErr == nil && account.GetKiroAccessToken() != accessToken {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, validatedURL, bytes.NewReader(payload))
			if err != nil {
				return nil, fmt.Errorf("rebuild kiro request after refresh: %w", err)
			}
			req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
			req.Header.Set("Content-Type", KiroContentType)
			req.Header.Set("Accept", KiroAcceptStream)
			req.Header.Set("Authorization", "Bearer "+account.GetKiroAccessToken())
			req.Header.Set("User-Agent", KiroUserAgent)
			req.Header.Set("X-Amz-User-Agent", KiroFullUserAgent)
			req.Header.Set("X-Amz-Target", endpoint.Target)
			req.Header.Set("X-Amzn-Bedrock-Origin", endpoint.Origin)
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		}
	}
	return resp, nil
}

func (s *OpenAIGatewayService) handleKiroAnthropicError(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, upstreamModel string, billingModel string) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	msg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-amzn-requestid"),
		Kind:               "failover",
		Message:            msg,
	})
	if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, msg, body) || resp.StatusCode == http.StatusTooManyRequests {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, upstreamModel)
		return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body}
	}
	writeAnthropicError(c, resp.StatusCode, "api_error", msg)
	return nil, fmt.Errorf("kiro upstream returned %d for %s/%s: %s", resp.StatusCode, upstreamModel, billingModel, msg)
}

func (s *OpenAIGatewayService) handleKiroChatError(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, upstreamModel string) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	msg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-amzn-requestid"),
		Kind:               "failover",
		Message:            msg,
	})
	if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, msg, body) || resp.StatusCode == http.StatusTooManyRequests {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, upstreamModel)
		return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body}
	}
	writeChatCompletionsError(c, resp.StatusCode, "upstream_error", msg)
	return nil, fmt.Errorf("kiro upstream returned %d for %s: %s", resp.StatusCode, upstreamModel, msg)
}

func (s *OpenAIGatewayService) handleKiroResponsesError(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, upstreamModel string) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	msg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-amzn-requestid"),
		Kind:               "failover",
		Message:            msg,
	})
	if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, msg, body) || resp.StatusCode == http.StatusTooManyRequests {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, upstreamModel)
		return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body}
	}
	writeResponsesError(c, resp.StatusCode, "upstream_error", msg)
	return nil, fmt.Errorf("kiro upstream returned %d for %s: %s", resp.StatusCode, upstreamModel, msg)
}

func (s *OpenAIGatewayService) bufferKiroAsAnthropic(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	anthropicResp := buildKiroAnthropicResponse(parsed, originalModel)
	c.JSON(http.StatusOK, anthropicResp)
	return &OpenAIForwardResult{
		RequestID:     firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:         parsed.Usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) streamKiroAsAnthropic(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	writeKiroAnthropicSSE(c, parsed, originalModel)
	return &OpenAIForwardResult{
		RequestID:     firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:         parsed.Usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        true,
		Duration:      time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) bufferKiroAsChatCompletions(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, buildKiroChatCompletionsResponse(parsed, originalModel))
	return &OpenAIForwardResult{
		RequestID:     firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:         parsed.Usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) streamKiroAsChatCompletions(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	writeKiroChatCompletionsSSE(c, parsed, originalModel)
	return &OpenAIForwardResult{
		RequestID:     firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:         parsed.Usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        true,
		Duration:      time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) bufferKiroAsResponses(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, reasoningEffort *string, serviceTier *string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	anthropicResp := buildKiroAnthropicResponse(parsed, originalModel)
	responsesResp := apicompat.AnthropicToResponsesResponse(anthropicResp)
	c.JSON(http.StatusOK, responsesResp)
	return &OpenAIForwardResult{
		RequestID:       firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:           parsed.Usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) streamKiroAsResponses(c *gin.Context, resp *http.Response, originalModel, billingModel, upstreamModel string, reasoningEffort *string, serviceTier *string, startTime time.Time) (*OpenAIForwardResult, error) {
	parsed, err := parseKiroEventStream(resp.Body)
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to parse Kiro response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	writeKiroResponsesSSE(c, parsed, originalModel)
	return &OpenAIForwardResult{
		RequestID:       firstHeader(resp.Header, "x-amzn-requestid", "x-request-id"),
		Usage:           parsed.Usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          true,
		Duration:        time.Since(startTime),
	}, nil
}

func buildKiroPayloadFromAnthropic(req *apicompat.AnthropicRequest, modelID, profileARN, origin string) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing anthropic request")
	}
	if origin == "" {
		origin = "AI_EDITOR"
	}
	currentIndex := lastAnthropicUserMessageIndex(req.Messages)
	systemText := anthropicSystemText(req.System)

	history := make([]kiroHistoryMessage, 0, len(req.Messages))
	var current kiroUserInputMessage
	for i, msg := range req.Messages {
		switch msg.Role {
		case "assistant":
			content, toolUses := anthropicAssistantMessageToKiro(msg)
			history = append(history, kiroHistoryMessage{AssistantResponseMessage: &kiroAssistantResponseMessage{Content: content, ToolUses: toolUses}})
		default:
			userMsg := anthropicUserMessageToKiro(msg, modelID, origin, nil)
			if i == currentIndex {
				if systemText != "" {
					if strings.TrimSpace(userMsg.Content) != "" {
						userMsg.Content = systemText + "\n\n" + userMsg.Content
					} else {
						userMsg.Content = systemText
					}
				}
				userMsg.UserInputMessageContext = mergeKiroUserContext(userMsg.UserInputMessageContext, req.Tools)
				current = userMsg
			} else {
				history = append(history, kiroHistoryMessage{UserInputMessage: &userMsg})
			}
		}
	}
	if currentIndex < 0 {
		current = kiroUserInputMessage{Content: systemText, ModelID: modelID, Origin: origin}
		current.UserInputMessageContext = mergeKiroUserContext(current.UserInputMessageContext, req.Tools)
	}
	inference := &kiroInferenceConfig{}
	if req.MaxTokens == -1 {
		inference.MaxTokens = kiroMaxOutputTokens
	} else if req.MaxTokens > 0 {
		inference.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		inference.Temperature = req.Temperature
	}
	if req.TopP != nil {
		inference.TopP = req.TopP
	}
	if inference.MaxTokens == 0 && inference.Temperature == nil && inference.TopP == nil {
		inference = nil
	}

	return json.Marshal(kiroPayload{
		ConversationState: kiroConversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  "kiro-" + uuid.NewString(),
			CurrentMessage:  kiroCurrentMessage{UserInputMessage: current},
			History:         history,
		},
		ProfileARN:      profileARN,
		InferenceConfig: inference,
	})
}

func lastAnthropicUserMessageIndex(messages []apicompat.AnthropicMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" || messages[i].Role == "" {
			return i
		}
	}
	return -1
}

func anthropicUserMessageToKiro(msg apicompat.AnthropicMessage, modelID, origin string, tools []apicompat.AnthropicTool) kiroUserInputMessage {
	content, toolResults := anthropicUserContentToTextAndResults(msg.Content)
	var ctx *kiroUserMessageContext
	if len(toolResults) > 0 {
		ctx = &kiroUserMessageContext{ToolResults: toolResults}
	}
	ctx = mergeKiroUserContext(ctx, tools)
	return kiroUserInputMessage{Content: content, ModelID: modelID, Origin: origin, UserInputMessageContext: ctx}
}

func anthropicAssistantMessageToKiro(msg apicompat.AnthropicMessage) (string, []kiroToolUse) {
	if text, ok := rawJSONString(msg.Content); ok {
		return text, nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return strings.TrimSpace(string(msg.Content)), nil
	}
	var b strings.Builder
	var toolUses []kiroToolUse
	for _, block := range blocks {
		switch block.Type {
		case "text":
			_, _ = b.WriteString(block.Text)
		case "thinking":
			_, _ = b.WriteString(block.Thinking)
		case "tool_use":
			input := map[string]any{}
			if len(block.Input) > 0 {
				_ = json.Unmarshal(block.Input, &input)
			}
			toolUses = append(toolUses, kiroToolUse{ToolUseID: block.ID, Name: block.Name, Input: input})
		}
	}
	return b.String(), toolUses
}

func anthropicUserContentToTextAndResults(raw json.RawMessage) (string, []kiroToolResult) {
	if text, ok := rawJSONString(raw); ok {
		return text, nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return strings.TrimSpace(string(raw)), nil
	}
	var b strings.Builder
	var results []kiroToolResult
	for _, block := range blocks {
		switch block.Type {
		case "text":
			_, _ = b.WriteString(block.Text)
		case "tool_result":
			text := anthropicToolResultText(block.Content)
			status := "success"
			if block.IsError {
				status = "error"
			}
			results = append(results, kiroToolResult{
				Content:   []kiroTextContent{{Text: text}},
				Status:    status,
				ToolUseID: block.ToolUseID,
			})
		case "image":
			if b.Len() > 0 {
				_, _ = b.WriteString("\n")
			}
			_, _ = b.WriteString("[image]")
		}
	}
	return b.String(), results
}

func anthropicToolResultText(raw json.RawMessage) string {
	if text, ok := rawJSONString(raw); ok {
		return text
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				if b.Len() > 0 {
					_, _ = b.WriteString("\n")
				}
				_, _ = b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return strings.TrimSpace(string(raw))
}

func anthropicSystemText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	if text, ok := rawJSONString(raw); ok {
		return text
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				if b.Len() > 0 {
					_, _ = b.WriteString("\n")
				}
				_, _ = b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return strings.TrimSpace(string(raw))
}

func mergeKiroUserContext(ctx *kiroUserMessageContext, tools []apicompat.AnthropicTool) *kiroUserMessageContext {
	kiroTools := make([]kiroTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		var schema any = map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.InputSchema) > 0 {
			var decoded any
			if err := json.Unmarshal(tool.InputSchema, &decoded); err == nil && decoded != nil {
				schema = decoded
			}
		}
		kiroTools = append(kiroTools, kiroTool{ToolSpecification: kiroToolSpecification{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: kiroInputSchema{JSON: schema},
		}})
	}
	if len(kiroTools) == 0 {
		return ctx
	}
	if ctx == nil {
		ctx = &kiroUserMessageContext{}
	}
	ctx.Tools = append(ctx.Tools, kiroTools...)
	return ctx
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, true
	}
	return "", false
}

func buildKiroAnthropicResponse(parsed *KiroParsedEventStream, model string) *apicompat.AnthropicResponse {
	if parsed == nil {
		parsed = &KiroParsedEventStream{StopReason: "end_turn"}
	}
	content := make([]apicompat.AnthropicContentBlock, 0, 1+len(parsed.ToolUses))
	if parsed.Content != "" || len(parsed.ToolUses) == 0 {
		content = append(content, apicompat.AnthropicContentBlock{Type: "text", Text: parsed.Content})
	}
	for _, tool := range parsed.ToolUses {
		input := tool.Input
		if len(bytes.TrimSpace(input)) == 0 {
			input = json.RawMessage(`{}`)
		}
		content = append(content, apicompat.AnthropicContentBlock{Type: "tool_use", ID: tool.ID, Name: tool.Name, Input: input})
	}
	return &apicompat.AnthropicResponse{
		ID:         "",
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: parsed.StopReason,
		Usage: apicompat.AnthropicUsage{
			InputTokens:              parsed.Usage.InputTokens,
			OutputTokens:             parsed.Usage.OutputTokens,
			CacheCreationInputTokens: parsed.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     parsed.Usage.CacheReadInputTokens,
		},
	}
}

func buildKiroChatCompletionsResponse(parsed *KiroParsedEventStream, model string) *apicompat.ChatCompletionsResponse {
	if parsed == nil {
		parsed = &KiroParsedEventStream{StopReason: "end_turn"}
	}
	message := apicompat.ChatMessage{
		Role:    "assistant",
		Content: json.RawMessage(kiroJSONString(parsed.Content)),
	}
	for i, tool := range parsed.ToolUses {
		idx := i
		message.ToolCalls = append(message.ToolCalls, apicompat.ChatToolCall{
			Index: &idx,
			ID:    tool.ID,
			Type:  "function",
			Function: apicompat.ChatFunctionCall{
				Name:      tool.Name,
				Arguments: string(tool.Input),
			},
		})
	}
	return &apicompat.ChatCompletionsResponse{
		ID:      "chatcmpl_" + uuid.NewString(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChoice{{
			Index:        0,
			Message:      message,
			FinishReason: kiroChatFinishReason(parsed.StopReason),
		}},
		Usage: &apicompat.ChatUsage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}
}

func writeKiroAnthropicSSE(c *gin.Context, parsed *KiroParsedEventStream, model string) {
	events := buildKiroAnthropicStreamEvents(parsed, model)
	for _, event := range events {
		data, err := apicompat.ResponsesAnthropicEventToSSE(event)
		if err == nil {
			_, _ = fmt.Fprint(c.Writer, data)
		}
	}
	c.Writer.Flush()
}

func buildKiroAnthropicStreamEvents(parsed *KiroParsedEventStream, model string) []apicompat.AnthropicStreamEvent {
	resp := buildKiroAnthropicResponse(parsed, model)
	resp.Content = nil
	resp.Usage.OutputTokens = 0
	events := []apicompat.AnthropicStreamEvent{{Type: "message_start", Message: resp}}
	index := 0
	if parsed.Content != "" || len(parsed.ToolUses) == 0 {
		i := index
		events = append(events,
			apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &i, ContentBlock: &apicompat.AnthropicContentBlock{Type: "text", Text: ""}},
			apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: &i, Delta: &apicompat.AnthropicDelta{Type: "text_delta", Text: parsed.Content}},
			apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &i},
		)
		index++
	}
	for _, tool := range parsed.ToolUses {
		i := index
		input := tool.Input
		if len(bytes.TrimSpace(input)) == 0 {
			input = json.RawMessage(`{}`)
		}
		events = append(events,
			apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &i, ContentBlock: &apicompat.AnthropicContentBlock{Type: "tool_use", ID: tool.ID, Name: tool.Name, Input: input}},
			apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &i},
		)
		index++
	}
	events = append(events,
		apicompat.AnthropicStreamEvent{Type: "message_delta", Delta: &apicompat.AnthropicDelta{StopReason: parsed.StopReason}, Usage: &apicompat.AnthropicUsage{OutputTokens: parsed.Usage.OutputTokens}},
		apicompat.AnthropicStreamEvent{Type: "message_stop"},
	)
	return events
}

func writeKiroChatCompletionsSSE(c *gin.Context, parsed *KiroParsedEventStream, model string) {
	id := "chatcmpl_" + uuid.NewString()
	created := time.Now().Unix()
	roleChunk := apicompat.ChatCompletionsChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{Role: "assistant"}}},
	}
	writeKiroChatChunk(c, roleChunk)
	if parsed.Content != "" {
		text := parsed.Content
		writeKiroChatChunk(c, apicompat.ChatCompletionsChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{Content: &text}}},
		})
	}
	finish := kiroChatFinishReason(parsed.StopReason)
	writeKiroChatChunk(c, apicompat.ChatCompletionsChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{}, FinishReason: &finish}},
		Usage: &apicompat.ChatUsage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	})
	_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

func writeKiroChatChunk(c *gin.Context, chunk apicompat.ChatCompletionsChunk) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
}

func writeKiroResponsesSSE(c *gin.Context, parsed *KiroParsedEventStream, model string) {
	state := apicompat.NewAnthropicEventToResponsesState()
	for _, anthropicEvent := range buildKiroAnthropicStreamEvents(parsed, model) {
		for _, event := range apicompat.AnthropicEventToResponsesEvents(&anthropicEvent, state) {
			writeKiroResponsesEvent(c, event)
		}
	}
	for _, event := range apicompat.FinalizeAnthropicResponsesStream(state) {
		writeKiroResponsesEvent(c, event)
	}
	_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

func writeKiroResponsesEvent(c *gin.Context, event apicompat.ResponsesStreamEvent) {
	data, err := apicompat.ResponsesEventToSSE(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprint(c.Writer, data)
}

func kiroChatFinishReason(stopReason string) string {
	switch stopReason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func kiroJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

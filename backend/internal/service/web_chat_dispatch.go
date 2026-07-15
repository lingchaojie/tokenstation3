package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/gin-gonic/gin"
)

type webChatAPIKeyService interface {
	APIKeyQuotaUpdater
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	EnsureWebChatKey(ctx context.Context, user *User, group *Group) (*APIKey, error)
}

type webChatSubscriptionService interface {
	ResolveActiveSubscriptionForRoutedGroup(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
	ValidateAndCheckLimits(sub *UserSubscription, group *Group) (bool, error)
}

type webChatBillingEligibilityService interface {
	CheckBillingEligibility(ctx context.Context, user *User, apiKey *APIKey, group *Group, subscription *UserSubscription, platform string) error
}

type webChatGatewayService interface {
	SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, metadataUserID string, sub2apiUserID int64) (*AccountSelectionResult, error)
	ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte, parsed *ParsedRequest) (*ForwardResult, error)
	ForwardAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte, parsed *ParsedRequest) (*ForwardResult, error)
	RecordUsage(ctx context.Context, input *RecordUsageInput) error
}

type webChatOpenAIGatewayService interface {
	SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*AccountSelectionResult, error)
	Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error)
	ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte, promptCacheKey string, defaultMappedModel string) (*OpenAIForwardResult, error)
	RecordUsage(ctx context.Context, input *OpenAIRecordUsageInput) error
}

type webChatGeminiCompatService interface {
	ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error)
}

type webChatUsageLogLookupRepository interface {
	GetByRequestIDAndAPIKeyID(ctx context.Context, requestID string, apiKeyID int64) (*UsageLog, error)
}

type webChatDispatchInput struct {
	User               *User
	ConversationID     int64
	AssistantMessageID int64
	Model              string
	Provider           string
	Capabilities       WebChatModelCapability
	Messages           []WebChatMessage
	Stream             bool
	Thinking           WebChatThinkingConfig
	ImageGeneration    WebChatImageGenerationConfig
	WebSearch          WebChatWebSearchConfig
}

type webChatDispatchResult struct {
	ResponseBody       []byte
	UsageLogID         *int64
	ArtifactCandidates []WebChatArtifactCandidate
}

func (s *WebChatService) dispatchChatCompletions(c *gin.Context, input webChatDispatchInput) (*webChatDispatchResult, error) {
	if s == nil || c == nil || c.Request == nil || input.User == nil {
		return nil, ErrWebChatInvalidModel
	}
	ctx := c.Request.Context()
	if err := validateWebChatAdapterContext(input.Capabilities, input.Messages); err != nil {
		return nil, err
	}
	group, err := s.webChatAvailableGroup(ctx, input.User.ID, input.Capabilities.Platform)
	if err != nil {
		return nil, err
	}
	if s.subscriptionService == nil || s.billingCacheService == nil {
		return nil, ErrBillingServiceUnavailable
	}
	hiddenKey, err := s.apiKeyService.EnsureWebChatKey(ctx, input.User, group)
	if err != nil {
		return nil, err
	}
	if hiddenKey.User == nil {
		hiddenKey.User = input.User
	}
	if hiddenKey.Group == nil {
		hiddenKey.Group = group
	}

	subscription, err := s.subscriptionService.ResolveActiveSubscriptionForRoutedGroup(ctx, input.User.ID, group.ID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			subscription = nil
		} else {
			return nil, err
		}
	}
	if subscription != nil {
		if _, err := s.subscriptionService.ValidateAndCheckLimits(subscription, group); err != nil {
			return nil, err
		}
	}
	if err := s.billingCacheService.CheckBillingEligibility(ctx, input.User, hiddenKey, group, subscription, input.Capabilities.Platform); err != nil {
		return nil, err
	}

	payloadOptions := WebChatCompletionsPayloadOptions{
		Thinking:        input.Thinking,
		ImageGeneration: input.ImageGeneration,
		WebSearch:       input.WebSearch,
	}
	useResponsesPayload := webChatUseResponsesPayload(input)
	var body []byte
	switch {
	case strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai"):
		body, err = BuildOpenAIWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
	case useResponsesPayload:
		body, err = BuildWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
	default:
		body, err = BuildWebChatCompletionsPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
	}
	if err != nil {
		return nil, err
	}
	parsed := &ParsedRequest{
		Body:    NewRequestBodyRef(body),
		Model:   input.Model,
		Stream:  input.Stream,
		GroupID: &group.ID,
	}

	downstreamCapture := NewWebChatResponseCapture(c.Writer, 4<<20)
	upstreamCapture := newWebChatStreamCapture(4 << 20)
	c.Writer = downstreamCapture

	usageClientID := fmt.Sprintf("webchat-message-%d", input.AssistantMessageID)
	usageCtx := context.WithValue(ctx, ctxkey.ClientRequestID, usageClientID)
	usageCtx = withWebChatStreamCapture(usageCtx, upstreamCapture)
	postDispatchCtx := context.WithValue(context.WithoutCancel(ctx), ctxkey.ClientRequestID, usageClientID)
	usageRequestID := "client:" + usageClientID
	inboundEndpoint := fmt.Sprintf("/api/v1/chat/conversations/%d/messages", input.ConversationID)
	channelMapping := ChannelMappingResult{MappedModel: input.Model}
	usageRecorded := false
	artifactCandidates := make([]WebChatArtifactCandidate, 0, 1)

	switch input.Capabilities.Platform {
	case PlatformOpenAI:
		upstreamEndpoint := "/v1/chat/completions"
		var result *OpenAIForwardResult
		var account *Account
		if useResponsesPayload {
			result, account, err = s.forwardWebChatOpenAIResponses(usageCtx, c, group, body, input)
			upstreamEndpoint = "/v1/responses"
		} else {
			result, account, err = s.forwardWebChatOpenAI(usageCtx, c, group, body, input)
		}
		if err != nil {
			return nil, err
		}
		if result != nil {
			artifactCandidates = append(artifactCandidates, webChatArtifactCandidatesFromOpenAIImageResults(result.imageResults)...)
		}
		recordUsageErr := s.openAIGatewayService.RecordUsage(postDispatchCtx, &OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             hiddenKey,
			User:               input.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          c.GetHeader("User-Agent"),
			IPAddress:          ip.GetClientIP(c),
			APIKeyService:      s.apiKeyService,
			ChannelUsageFields: channelMapping.ToUsageFields(input.Model, result.UpstreamModel),
		})
		if recordUsageErr != nil {
			log.Printf("[WARN] web chat: record OpenAI usage failed after upstream response: %v", recordUsageErr)
		} else {
			usageRecorded = true
		}
	case PlatformAnthropic:
		upstreamEndpoint := "/v1/chat/completions"
		var result *ForwardResult
		var account *Account
		if useResponsesPayload {
			result, account, err = s.forwardWebChatGatewayResponses(usageCtx, c, group, body, parsed, input)
			upstreamEndpoint = "/v1/responses"
		} else {
			result, account, err = s.forwardWebChatGateway(usageCtx, c, group, body, parsed, input)
		}
		if err != nil {
			return nil, err
		}
		recordUsageErr := s.gatewayService.RecordUsage(postDispatchCtx, &RecordUsageInput{
			Result:             result,
			APIKey:             hiddenKey,
			User:               input.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          c.GetHeader("User-Agent"),
			IPAddress:          ip.GetClientIP(c),
			APIKeyService:      s.apiKeyService,
			QuotaPlatform:      QuotaPlatform(ctx, hiddenKey),
			ChannelUsageFields: channelMapping.ToUsageFields(input.Model, result.UpstreamModel),
		})
		if recordUsageErr != nil {
			log.Printf("[WARN] web chat: record gateway usage failed after upstream response: %v", recordUsageErr)
		} else {
			usageRecorded = true
		}
	case PlatformGemini:
		result, account, err := s.forwardWebChatGemini(usageCtx, c, group, body, parsed, input)
		if err != nil {
			return nil, err
		}
		recordUsageErr := s.gatewayService.RecordUsage(postDispatchCtx, &RecordUsageInput{
			Result:             result,
			APIKey:             hiddenKey,
			User:               input.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   "/v1/chat/completions",
			UserAgent:          c.GetHeader("User-Agent"),
			IPAddress:          ip.GetClientIP(c),
			APIKeyService:      s.apiKeyService,
			QuotaPlatform:      QuotaPlatform(ctx, hiddenKey),
			ChannelUsageFields: channelMapping.ToUsageFields(input.Model, result.UpstreamModel),
		})
		if recordUsageErr != nil {
			log.Printf("[WARN] web chat: record Gemini usage failed after upstream response: %v", recordUsageErr)
		} else {
			usageRecorded = true
		}
	default:
		return nil, ErrWebChatInvalidModel
	}

	var usageLogID *int64
	if usageRecorded && s.usageLogRepository != nil && hiddenKey.ID > 0 {
		usageLog, err := s.usageLogRepository.GetByRequestIDAndAPIKeyID(postDispatchCtx, usageRequestID, hiddenKey.ID)
		if err == nil && usageLog != nil {
			usageLogID = &usageLog.ID
		} else if err != nil && !errors.Is(err, ErrUsageLogNotFound) {
			log.Printf("[WARN] web chat: lookup usage log failed after upstream response: %v", err)
		}
	}
	responseBody := upstreamCapture.Body()
	if len(responseBody) == 0 {
		responseBody = downstreamCapture.Body()
	}
	artifactCandidates = append(artifactCandidates, ExtractArtifactsFromChatCompletions(responseBody, input.Stream)...)
	return &webChatDispatchResult{ResponseBody: responseBody, UsageLogID: usageLogID, ArtifactCandidates: artifactCandidates}, nil
}

func webChatUseResponsesPayload(input webChatDispatchInput) bool {
	if strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai") {
		return true
	}
	if !input.Capabilities.SupportsWebSearch {
		return false
	}
	if !input.WebSearch.Configured && !input.WebSearch.Enabled {
		return false
	}
	return input.Capabilities.Platform == PlatformAnthropic
}

func (s *WebChatService) webChatAvailableGroup(ctx context.Context, userID int64, platform string) (*Group, error) {
	if s.apiKeyService == nil {
		return nil, ErrDefaultAPIKeyGroupMissing
	}
	groups, err := s.apiKeyService.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Platform == platform {
			return &groups[i], nil
		}
	}
	return nil, ErrDefaultAPIKeyGroupMissing
}

func (s *WebChatService) forwardWebChatGateway(ctx context.Context, c *gin.Context, group *Group, body []byte, parsed *ParsedRequest, input webChatDispatchInput) (*ForwardResult, *Account, error) {
	if s.gatewayService == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	selection, err := s.gatewayService.SelectAccountWithLoadAwareness(ctx, &group.ID, "", input.Model, nil, "", input.User.ID)
	if err != nil {
		return nil, nil, err
	}
	defer releaseWebChatSelection(selection)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	result, err := s.gatewayService.ForwardAsChatCompletions(ctx, c, selection.Account, body, parsed)
	return result, selection.Account, err
}

func (s *WebChatService) forwardWebChatGatewayResponses(ctx context.Context, c *gin.Context, group *Group, body []byte, parsed *ParsedRequest, input webChatDispatchInput) (*ForwardResult, *Account, error) {
	if s.gatewayService == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	selection, err := s.gatewayService.SelectAccountWithLoadAwareness(ctx, &group.ID, "", input.Model, nil, "", input.User.ID)
	if err != nil {
		return nil, nil, err
	}
	defer releaseWebChatSelection(selection)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	result, err := s.gatewayService.ForwardAsResponses(ctx, c, selection.Account, body, parsed)
	return result, selection.Account, err
}

func (s *WebChatService) forwardWebChatOpenAI(ctx context.Context, c *gin.Context, group *Group, body []byte, input webChatDispatchInput) (*OpenAIForwardResult, *Account, error) {
	if s.openAIGatewayService == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	selection, err := s.openAIGatewayService.SelectAccountWithLoadAwareness(ctx, &group.ID, "", input.Model, nil)
	if err != nil {
		return nil, nil, err
	}
	defer releaseWebChatSelection(selection)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	result, err := s.openAIGatewayService.ForwardAsChatCompletions(ctx, c, selection.Account, body, "", group.DefaultMappedModel)
	return result, selection.Account, err
}

func (s *WebChatService) forwardWebChatOpenAIResponses(ctx context.Context, c *gin.Context, group *Group, body []byte, input webChatDispatchInput) (*OpenAIForwardResult, *Account, error) {
	if s.openAIGatewayService == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	selection, err := s.openAIGatewayService.SelectAccountWithLoadAwareness(ctx, &group.ID, "", input.Model, nil)
	if err != nil {
		return nil, nil, err
	}
	defer releaseWebChatSelection(selection)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	result, err := s.openAIGatewayService.Forward(ctx, c, selection.Account, body)
	return result, selection.Account, err
}

func (s *WebChatService) forwardWebChatGemini(ctx context.Context, c *gin.Context, group *Group, body []byte, parsed *ParsedRequest, input webChatDispatchInput) (*ForwardResult, *Account, error) {
	if s.gatewayService == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	selection, err := s.gatewayService.SelectAccountWithLoadAwareness(ctx, &group.ID, "", input.Model, nil, "", input.User.ID)
	if err != nil {
		return nil, nil, err
	}
	defer releaseWebChatSelection(selection)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	if selection.Account != nil && selection.Account.Platform == PlatformGemini && s.geminiCompatService != nil {
		result, err := s.geminiCompatService.ForwardAsChatCompletions(ctx, c, selection.Account, body)
		return result, selection.Account, err
	}
	result, err := s.gatewayService.ForwardAsChatCompletions(ctx, c, selection.Account, body, parsed)
	return result, selection.Account, err
}

func releaseWebChatSelection(selection *AccountSelectionResult) {
	if selection != nil && selection.Acquired && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

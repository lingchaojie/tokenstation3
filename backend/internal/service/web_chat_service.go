package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	webChatMaxUploadBytes      = 20 << 20
	webChatMaxTextPreviewBytes = 64 << 10
	webChatCatalogCacheTTL     = 30 * time.Second
)

const webChatCatalogSingleflightKey = "web-chat-catalog"

type webChatCatalogCacheEntry struct {
	models    []WebChatModelCapability
	expiresAt time.Time
}

type webChatAttachmentCreator interface {
	CreateAttachment(ctx context.Context, in CreateWebChatAttachmentInput) (*WebChatAttachment, error)
}

type webChatUserResolver interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type WebChatService struct {
	repo           WebChatRepository
	attachmentRepo webChatAttachmentCreator
	storage        WebChatStorage

	userResolver         webChatUserResolver
	apiKeyService        webChatAPIKeyService
	subscriptionService  webChatSubscriptionService
	billingCacheService  webChatBillingEligibilityService
	gatewayService       webChatGatewayService
	openAIGatewayService webChatOpenAIGatewayService
	geminiCompatService  webChatGeminiCompatService
	usageLogRepository   webChatUsageLogLookupRepository

	defaultGroups webChatDefaultGroupResolver
	accountLister webChatAccountLister

	catalogCacheMu sync.RWMutex
	catalogCache   webChatCatalogCacheEntry
	catalogFlight  singleflight.Group

	activeCancelMu sync.Mutex
	activeCancels  map[webChatAssistantKey]context.CancelFunc
}

type webChatAssistantKey struct {
	userID         int64
	conversationID int64
	messageID      int64
}

func NewWebChatService(
	repo WebChatRepository,
	storage WebChatStorage,
	userRepo UserRepository,
	apiKeyService *APIKeyService,
	subscriptionService *SubscriptionService,
	billingCacheService *BillingCacheService,
	gatewayService *GatewayService,
	openAIGatewayService *OpenAIGatewayService,
	geminiCompatService *GeminiMessagesCompatService,
	usageLogRepo UsageLogRepository,
	settingService *SettingService,
	accountService *AccountService,
	cfg *config.Config,
) *WebChatService {
	if storage == nil && cfg != nil {
		storage = NewLocalWebChatStorageFromConfig(cfg)
	}
	return &WebChatService{
		repo:                 repo,
		attachmentRepo:       repo,
		storage:              storage,
		userResolver:         userRepo,
		apiKeyService:        apiKeyService,
		subscriptionService:  subscriptionService,
		billingCacheService:  billingCacheService,
		gatewayService:       gatewayService,
		openAIGatewayService: openAIGatewayService,
		geminiCompatService:  geminiCompatService,
		usageLogRepository:   usageLogRepo,
		defaultGroups:        settingService,
		accountLister:        accountService,
	}
}

type WebChatSendInput struct {
	UserID          int64
	User            *User
	ConversationID  int64
	Model           string
	Provider        string
	Text            string
	Stream          bool
	Thinking        WebChatThinkingConfig
	ImageGeneration WebChatImageGenerationConfig
	WebSearch       WebChatWebSearchConfig
	AttachmentIDs   []int64
	GinContext      *gin.Context
}

type WebChatSendResult struct {
	UserMessageID      int64
	AssistantMessageID int64
}

func (s *WebChatService) ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]WebChatConversation, *pagination.PaginationResult, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, nil, ErrWebChatConversationNotFound
	}
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	return s.repo.ListConversations(ctx, userID, params)
}

func (s *WebChatService) CreateConversation(ctx context.Context, userID int64, in CreateWebChatConversationInput) (*WebChatConversation, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, ErrWebChatConversationNotFound
	}
	caps, err := s.resolveWebChatSendCapability(ctx, in.DefaultProvider, in.DefaultModel)
	if err != nil {
		return nil, err
	}
	in.UserID = userID
	in.DefaultProvider = caps.Provider
	in.DefaultModel = caps.Model
	return s.repo.CreateConversation(ctx, in)
}

func (s *WebChatService) GetConversation(ctx context.Context, userID, conversationID int64) (*WebChatConversationDetail, error) {
	if s == nil || s.repo == nil || userID <= 0 || conversationID <= 0 {
		return nil, ErrWebChatConversationNotFound
	}
	conversation, err := s.repo.GetConversationForUser(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	messages, err := s.repo.ListMessages(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}
	return &WebChatConversationDetail{Conversation: *conversation, Messages: messages}, nil
}

func (s *WebChatService) UpdateConversation(ctx context.Context, userID, conversationID int64, in UpdateWebChatConversationInput) (*WebChatConversation, error) {
	if s == nil || s.repo == nil || userID <= 0 || conversationID <= 0 {
		return nil, ErrWebChatConversationNotFound
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		switch status {
		case WebChatConversationStatusActive, WebChatConversationStatusArchived:
			in.Status = &status
		default:
			return nil, ErrWebChatInvalidConversationStatus
		}
	}
	if in.DefaultModel != nil || in.DefaultProvider != nil {
		current, err := s.repo.GetConversationForUser(ctx, userID, conversationID)
		if err != nil {
			return nil, err
		}
		provider := current.DefaultProvider
		model := current.DefaultModel
		if in.DefaultProvider != nil {
			provider = *in.DefaultProvider
		}
		if in.DefaultModel != nil {
			model = *in.DefaultModel
		}
		caps, err := s.resolveWebChatSendCapability(ctx, provider, model)
		if err != nil {
			return nil, err
		}
		in.DefaultProvider = &caps.Provider
		in.DefaultModel = &caps.Model
	}
	return s.repo.UpdateConversation(ctx, userID, conversationID, in)
}

func (s *WebChatService) DeleteConversation(ctx context.Context, userID, conversationID int64) error {
	if s == nil || s.repo == nil || userID <= 0 || conversationID <= 0 {
		return ErrWebChatConversationNotFound
	}
	return s.repo.SoftDeleteConversation(ctx, userID, conversationID)
}

func (s *WebChatService) OpenAttachment(ctx context.Context, userID, attachmentID int64) (io.ReadCloser, WebChatDownloadMeta, error) {
	if s == nil || s.repo == nil || s.storage == nil || userID <= 0 || attachmentID <= 0 {
		return nil, WebChatDownloadMeta{}, ErrWebChatAttachmentNotFound
	}
	attachment, err := s.repo.GetAttachmentForUser(ctx, userID, attachmentID)
	if err != nil {
		return nil, WebChatDownloadMeta{}, err
	}
	rc, stored, err := s.storage.Open(ctx, attachment.StorageKey)
	if err != nil {
		return nil, WebChatDownloadMeta{}, err
	}
	size := attachment.SizeBytes
	if size <= 0 {
		size = stored.SizeBytes
	}
	return rc, WebChatDownloadMeta{Filename: attachment.Filename, ContentType: attachment.ContentType, SizeBytes: size}, nil
}

func (s *WebChatService) OpenArtifact(ctx context.Context, userID, artifactID int64) (io.ReadCloser, WebChatDownloadMeta, error) {
	if s == nil || s.repo == nil || s.storage == nil || userID <= 0 || artifactID <= 0 {
		return nil, WebChatDownloadMeta{}, ErrWebChatArtifactNotFound
	}
	artifact, err := s.repo.GetArtifactForUser(ctx, userID, artifactID)
	if err != nil {
		return nil, WebChatDownloadMeta{}, err
	}
	rc, stored, err := s.storage.Open(ctx, artifact.StorageKey)
	if err != nil {
		return nil, WebChatDownloadMeta{}, err
	}
	size := artifact.SizeBytes
	if size <= 0 {
		size = stored.SizeBytes
	}
	return rc, WebChatDownloadMeta{Filename: artifact.Filename, ContentType: artifact.ContentType, SizeBytes: size}, nil
}

func (s *WebChatService) ListModels(ctx context.Context, userID int64) ([]WebChatModelCapability, error) {
	if userID <= 0 {
		return nil, ErrUserNotFound
	}
	return s.resolveCachedWebChatCatalog(ctx)
}

func (s *WebChatService) CancelMessage(ctx context.Context, userID, conversationID, messageID int64) error {
	if s == nil || s.repo == nil || userID <= 0 || conversationID <= 0 || messageID <= 0 {
		return ErrWebChatMessageNotFound
	}
	if _, err := s.repo.GetConversationForUser(ctx, userID, conversationID); err != nil {
		return err
	}
	messages, err := s.repo.ListMessages(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	message, ok := webChatFindMessage(messages, messageID)
	if !ok {
		return ErrWebChatMessageNotFound
	}
	if !webChatMessageIsCancelable(message) {
		return ErrWebChatMessageNotCancelable
	}
	status := WebChatMessageStatusCanceled
	role := WebChatRoleAssistant
	_, err = s.repo.UpdateMessage(ctx, userID, messageID, UpdateWebChatMessageInput{
		Status:                 &status,
		ExpectedConversationID: &conversationID,
		ExpectedRole:           &role,
		ExpectedStatuses:       []string{WebChatMessageStatusPending, WebChatMessageStatusStreaming},
	})
	if err != nil {
		if errors.Is(err, ErrWebChatMessageNotFound) {
			return ErrWebChatMessageNotCancelable
		}
		return err
	}
	s.cancelActiveWebChatAssistant(userID, conversationID, messageID)
	return nil
}

func (s *WebChatService) SendMessage(c *gin.Context, in WebChatSendInput) (*WebChatSendResult, error) {
	if in.GinContext == nil {
		in.GinContext = c
	}
	if s == nil || s.repo == nil || in.ConversationID <= 0 {
		return nil, ErrWebChatConversationNotFound
	}
	if in.GinContext == nil || in.GinContext.Request == nil {
		return nil, ErrWebChatContextRequired
	}
	ctx := in.GinContext.Request.Context()
	user, err := s.resolveWebChatSendUser(ctx, in)
	if err != nil {
		return nil, err
	}
	caps, err := s.resolveWebChatSendCapability(ctx, in.Provider, in.Model)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetConversationForUser(ctx, user.ID, in.ConversationID); err != nil {
		return nil, err
	}

	attachments := make([]WebChatAttachment, 0, len(in.AttachmentIDs))
	for _, attachmentID := range in.AttachmentIDs {
		attachment, err := s.repo.GetAttachmentForUser(ctx, user.ID, attachmentID)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *attachment)
	}
	if err := validateWebChatAdapterContext(caps, []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: in.Text,
		Attachments: attachments,
	}}); err != nil {
		return nil, err
	}

	existingMessages, err := s.repo.ListMessages(ctx, user.ID, in.ConversationID)
	if err != nil {
		return nil, err
	}
	proposedMessages := make([]WebChatMessage, 0, len(existingMessages)+1)
	proposedMessages = append(proposedMessages, existingMessages...)
	proposedMessages = append(proposedMessages, WebChatMessage{
		ConversationID: in.ConversationID,
		UserID:         user.ID,
		Role:           WebChatRoleUser,
		Model:          caps.Model,
		Provider:       caps.Provider,
		ContentText:    in.Text,
		Status:         WebChatMessageStatusCompleted,
		Attachments:    attachments,
	})
	if err := validateWebChatAdapterContext(caps, proposedMessages); err != nil {
		return nil, err
	}

	userMessage, err := s.repo.CreateMessage(ctx, CreateWebChatMessageInput{
		ConversationID: in.ConversationID,
		UserID:         user.ID,
		Role:           WebChatRoleUser,
		Model:          caps.Model,
		Provider:       caps.Provider,
		ContentText:    in.Text,
		Status:         WebChatMessageStatusCompleted,
		AttachmentIDs:  in.AttachmentIDs,
	})
	if err != nil {
		return nil, err
	}
	if len(in.AttachmentIDs) > 0 {
		if len(userMessage.Attachments) == 0 {
			userMessage.Attachments = attachments
		}
	}

	messages := make([]WebChatMessage, 0, len(existingMessages)+1)
	messages = append(messages, existingMessages...)
	messages = append(messages, *userMessage)

	assistantMessage, err := s.repo.CreateMessage(ctx, CreateWebChatMessageInput{
		ConversationID: in.ConversationID,
		UserID:         user.ID,
		Role:           WebChatRoleAssistant,
		Model:          caps.Model,
		Provider:       caps.Provider,
		Status:         WebChatMessageStatusStreaming,
	})
	if err != nil {
		return nil, err
	}
	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	s.registerWebChatAssistantCancel(user.ID, in.ConversationID, assistantMessage.ID, dispatchCancel)
	defer s.unregisterWebChatAssistantCancel(user.ID, in.ConversationID, assistantMessage.ID)
	defer dispatchCancel()
	originalRequest := in.GinContext.Request
	in.GinContext.Request = originalRequest.WithContext(dispatchCtx)
	defer func() {
		in.GinContext.Request = originalRequest
	}()

	in.GinContext.Header("X-Web-Chat-Conversation-ID", strconv.FormatInt(in.ConversationID, 10))
	in.GinContext.Header("X-Web-Chat-User-Message-ID", strconv.FormatInt(userMessage.ID, 10))
	in.GinContext.Header("X-Web-Chat-Assistant-Message-ID", strconv.FormatInt(assistantMessage.ID, 10))
	dispatchResult, err := s.dispatchChatCompletions(in.GinContext, webChatDispatchInput{
		User:               user,
		ConversationID:     in.ConversationID,
		AssistantMessageID: assistantMessage.ID,
		Model:              caps.Model,
		Provider:           caps.Provider,
		Capabilities:       caps,
		Messages:           messages,
		Stream:             in.Stream,
		Thinking:           in.Thinking,
		ImageGeneration:    in.ImageGeneration,
		WebSearch:          in.WebSearch,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) && s.webChatAssistantIsCanceled(context.WithoutCancel(ctx), user.ID, in.ConversationID, assistantMessage.ID) {
			return &WebChatSendResult{UserMessageID: userMessage.ID, AssistantMessageID: assistantMessage.ID}, nil
		}
		status := WebChatMessageStatusFailed
		errMsg := err.Error()
		role := WebChatRoleAssistant
		_, updateErr := s.repo.UpdateMessage(context.WithoutCancel(ctx), user.ID, assistantMessage.ID, UpdateWebChatMessageInput{
			Status:                 &status,
			ErrorMessage:           &errMsg,
			ExpectedConversationID: &in.ConversationID,
			ExpectedRole:           &role,
			ExpectedStatuses:       []string{WebChatMessageStatusPending, WebChatMessageStatusStreaming},
		})
		if updateErr != nil && s.webChatAssistantIsCanceled(context.WithoutCancel(ctx), user.ID, in.ConversationID, assistantMessage.ID) {
			return &WebChatSendResult{UserMessageID: userMessage.ID, AssistantMessageID: assistantMessage.ID}, nil
		}
		return nil, err
	}

	persistCtx := context.WithoutCancel(ctx)
	status := WebChatMessageStatusCompleted
	content := ExtractAssistantTextFromChatCompletions(dispatchResult.ResponseBody, in.Stream)
	contentJSON := ExtractAssistantProcessFromChatCompletions(dispatchResult.ResponseBody, in.Stream)
	role := WebChatRoleAssistant
	update := UpdateWebChatMessageInput{
		ContentText:            &content,
		Status:                 &status,
		ExpectedConversationID: &in.ConversationID,
		ExpectedRole:           &role,
		ExpectedStatuses:       []string{WebChatMessageStatusPending, WebChatMessageStatusStreaming},
	}
	if len(contentJSON) > 0 {
		update.ContentJSON = &contentJSON
	}
	if dispatchResult.UsageLogID != nil {
		update.UsageLogID = dispatchResult.UsageLogID
	}
	if _, err := s.repo.UpdateMessage(persistCtx, user.ID, assistantMessage.ID, update); err != nil {
		if s.webChatAssistantIsCanceled(persistCtx, user.ID, in.ConversationID, assistantMessage.ID) {
			return &WebChatSendResult{UserMessageID: userMessage.ID, AssistantMessageID: assistantMessage.ID}, nil
		}
		return nil, err
	}
	s.saveWebChatArtifactCandidates(persistCtx, user.ID, in.ConversationID, assistantMessage.ID, dispatchResult.ArtifactCandidates)
	return &WebChatSendResult{UserMessageID: userMessage.ID, AssistantMessageID: assistantMessage.ID}, nil
}

func (s *WebChatService) resolveWebChatSendUser(ctx context.Context, in WebChatSendInput) (*User, error) {
	userID := in.UserID
	if userID <= 0 && in.User != nil {
		userID = in.User.ID
	}
	if userID <= 0 {
		return nil, ErrUserNotFound
	}
	if in.User != nil {
		if in.User.ID != userID {
			return nil, ErrUserNotFound
		}
		return in.User, nil
	}
	if s.userResolver == nil {
		return nil, ErrUserNotFound
	}
	user, err := s.userResolver.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID != userID {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// resolveWebChatSendCapability validates the requested model against the live
// dynamic catalog. NOTE: this rebuilds the catalog (2 setting lookups + 2
// ListByGroup queries) on every send/create/update. Acceptable at current
// scale (the send path already does heavier DB work), but consider a short-TTL
// cache or a lighter single-model check if this path gets hot.
func (s *WebChatService) resolveWebChatSendCapability(ctx context.Context, provider, model string) (WebChatModelCapability, error) {
	catalog, err := s.resolveCachedWebChatCatalog(ctx)
	if err != nil {
		return WebChatModelCapability{}, err
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	base := normalizeWebChatModelName(strings.TrimSpace(model))
	for _, c := range catalog {
		if c.Provider == p && normalizeWebChatModelName(c.Model) == base {
			return c, nil
		}
	}
	return WebChatModelCapability{}, ErrWebChatInvalidModel
}

func (s *WebChatService) resolveCachedWebChatCatalog(ctx context.Context) ([]WebChatModelCapability, error) {
	if models, ok := s.cachedWebChatCatalog(time.Now()); ok {
		return models, nil
	}

	value, err, _ := s.catalogFlight.Do(webChatCatalogSingleflightKey, func() (any, error) {
		if models, ok := s.cachedWebChatCatalog(time.Now()); ok {
			return models, nil
		}

		models, err := resolveWebChatCatalog(ctx, s.defaultGroups, s.accountLister)
		if err != nil {
			return nil, err
		}

		cached := cloneWebChatModelCapabilities(models)
		s.catalogCacheMu.Lock()
		s.catalogCache = webChatCatalogCacheEntry{
			models:    cached,
			expiresAt: time.Now().Add(webChatCatalogCacheTTL),
		}
		s.catalogCacheMu.Unlock()

		return cloneWebChatModelCapabilities(cached), nil
	})
	if err != nil {
		return nil, err
	}

	models, ok := value.([]WebChatModelCapability)
	if !ok {
		return nil, errors.New("web chat catalog cache returned unexpected value")
	}
	return cloneWebChatModelCapabilities(models), nil
}

func (s *WebChatService) cachedWebChatCatalog(now time.Time) ([]WebChatModelCapability, bool) {
	s.catalogCacheMu.RLock()
	defer s.catalogCacheMu.RUnlock()
	if !s.catalogCache.expiresAt.After(now) {
		return nil, false
	}
	return cloneWebChatModelCapabilities(s.catalogCache.models), true
}

func cloneWebChatModelCapabilities(models []WebChatModelCapability) []WebChatModelCapability {
	if len(models) == 0 {
		return []WebChatModelCapability{}
	}
	out := make([]WebChatModelCapability, len(models))
	for i := range models {
		out[i] = models[i]
		out[i].ThinkingEfforts = cloneWebChatStringSlice(models[i].ThinkingEfforts)
		out[i].ImageGenerationSizes = cloneWebChatStringSlice(models[i].ImageGenerationSizes)
		out[i].ImageGenerationAspectRatios = cloneWebChatStringSlice(models[i].ImageGenerationAspectRatios)
		out[i].ImageGenerationQualities = cloneWebChatStringSlice(models[i].ImageGenerationQualities)
		out[i].ImageGenerationOutputFormats = cloneWebChatStringSlice(models[i].ImageGenerationOutputFormats)
		out[i].ImageGenerationBackgrounds = cloneWebChatStringSlice(models[i].ImageGenerationBackgrounds)
	}
	return out
}

func cloneWebChatStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func webChatFindMessage(messages []WebChatMessage, id int64) (WebChatMessage, bool) {
	for _, message := range messages {
		if message.ID == id {
			return message, true
		}
	}
	return WebChatMessage{}, false
}

func webChatMessageIsCancelable(message WebChatMessage) bool {
	return message.Role == WebChatRoleAssistant &&
		(message.Status == WebChatMessageStatusPending || message.Status == WebChatMessageStatusStreaming)
}

func (s *WebChatService) registerWebChatAssistantCancel(userID, conversationID, messageID int64, cancel context.CancelFunc) {
	if s == nil || cancel == nil {
		return
	}
	s.activeCancelMu.Lock()
	defer s.activeCancelMu.Unlock()
	if s.activeCancels == nil {
		s.activeCancels = make(map[webChatAssistantKey]context.CancelFunc)
	}
	s.activeCancels[webChatAssistantKey{userID: userID, conversationID: conversationID, messageID: messageID}] = cancel
}

func (s *WebChatService) unregisterWebChatAssistantCancel(userID, conversationID, messageID int64) {
	if s == nil {
		return
	}
	s.activeCancelMu.Lock()
	defer s.activeCancelMu.Unlock()
	delete(s.activeCancels, webChatAssistantKey{userID: userID, conversationID: conversationID, messageID: messageID})
}

func (s *WebChatService) cancelActiveWebChatAssistant(userID, conversationID, messageID int64) {
	if s == nil {
		return
	}
	s.activeCancelMu.Lock()
	cancel := s.activeCancels[webChatAssistantKey{userID: userID, conversationID: conversationID, messageID: messageID}]
	s.activeCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *WebChatService) webChatAssistantIsCanceled(ctx context.Context, userID, conversationID, messageID int64) bool {
	if s == nil || s.repo == nil {
		return false
	}
	messages, err := s.repo.ListMessages(ctx, userID, conversationID)
	if err != nil {
		log.Printf("[WARN] web chat: failed to inspect canceled assistant message: %v", err)
		return false
	}
	message, ok := webChatFindMessage(messages, messageID)
	return ok && message.Role == WebChatRoleAssistant && message.Status == WebChatMessageStatusCanceled
}

type UploadWebChatAttachmentInput struct {
	UserID      int64
	Filename    string
	ContentType string
	Reader      io.Reader
}

func (s *WebChatService) UploadAttachment(ctx context.Context, userID int64, file multipart.File, header *multipart.FileHeader) (*WebChatAttachment, error) {
	if header == nil || file == nil {
		return nil, ErrWebChatUploadRejected
	}
	return s.uploadAttachmentFromReader(ctx, UploadWebChatAttachmentInput{
		UserID:      userID,
		Filename:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		Reader:      file,
	})
}

func (s *WebChatService) uploadAttachmentFromReader(ctx context.Context, in UploadWebChatAttachmentInput) (*WebChatAttachment, error) {
	if s == nil || s.attachmentRepo == nil || s.storage == nil || in.UserID <= 0 || in.Reader == nil {
		return nil, ErrWebChatUploadRejected
	}

	body, err := io.ReadAll(io.LimitReader(in.Reader, webChatMaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > webChatMaxUploadBytes {
		return nil, ErrWebChatUploadRejected
	}

	contentType, kind, textPreviewEnabled, err := classifyWebChatUploadContentType(in.Filename, in.ContentType, body)
	if err != nil {
		return nil, err
	}

	var textPreview *string
	if textPreviewEnabled {
		preview := boundedUTF8Preview(body, webChatMaxTextPreviewBytes)
		textPreview = &preview
	}

	saved, err := s.storage.Save(ctx, WebChatStorageSaveInput{
		UserID:      in.UserID,
		Filename:    in.Filename,
		ContentType: contentType,
		Reader:      bytes.NewReader(body),
		MaxBytes:    webChatMaxUploadBytes,
	})
	if err != nil {
		return nil, err
	}

	attachment, err := s.attachmentRepo.CreateAttachment(ctx, CreateWebChatAttachmentInput{
		UserID:      in.UserID,
		Kind:        kind,
		Filename:    saved.Filename,
		ContentType: contentType,
		SizeBytes:   saved.SizeBytes,
		StorageKey:  saved.StorageKey,
		SHA256:      saved.SHA256,
		TextPreview: textPreview,
		Status:      WebChatAttachmentStatusUploaded,
	})
	if err != nil {
		_ = s.storage.Delete(context.WithoutCancel(ctx), saved.StorageKey)
		return nil, err
	}
	return attachment, nil
}

func boundedUTF8Preview(body []byte, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(body) > maxBytes {
		body = body[:maxBytes]
	}
	for len(body) > 0 {
		r, size := utf8.DecodeLastRune(body)
		if r != utf8.RuneError || size != 1 {
			break
		}
		body = body[:len(body)-1]
	}
	return strings.ToValidUTF8(string(body), "")
}

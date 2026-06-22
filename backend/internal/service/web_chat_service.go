package service

import (
	"bytes"
	"context"
	"io"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const (
	webChatMaxUploadBytes      = 20 << 20
	webChatMaxTextPreviewBytes = 64 << 10
)

type webChatAttachmentCreator interface {
	CreateAttachment(ctx context.Context, in CreateWebChatAttachmentInput) (*WebChatAttachment, error)
}

type webChatUserResolver interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type webChatCapabilityResolver interface {
	ResolveWebChatCapability(provider, model string) (WebChatModelCapability, error)
}

type WebChatService struct {
	repo           WebChatRepository
	attachmentRepo webChatAttachmentCreator
	storage        WebChatStorage

	userResolver         webChatUserResolver
	capabilityResolver   webChatCapabilityResolver
	apiKeyService        webChatAPIKeyService
	subscriptionService  webChatSubscriptionService
	billingCacheService  webChatBillingEligibilityService
	gatewayService       webChatGatewayService
	openAIGatewayService webChatOpenAIGatewayService
	geminiCompatService  webChatGeminiCompatService
	usageLogRepository   webChatUsageLogLookupRepository
}

func NewWebChatService(repo WebChatRepository, storage WebChatStorage) *WebChatService {
	return &WebChatService{
		repo:               repo,
		attachmentRepo:     repo,
		storage:            storage,
		capabilityResolver: NewWebChatCatalogCapabilityResolver(DefaultWebChatCatalogModels()),
	}
}

type WebChatSendInput struct {
	UserID         int64
	User           *User
	ConversationID int64
	Model          string
	Provider       string
	Text           string
	Stream         bool
	AttachmentIDs  []int64
	GinContext     *gin.Context
}

type WebChatSendResult struct {
	UserMessageID      int64
	AssistantMessageID int64
}

func (s *WebChatService) SendMessage(ctx context.Context, in WebChatSendInput) (*WebChatSendResult, error) {
	if s == nil || s.repo == nil || in.ConversationID <= 0 {
		return nil, ErrWebChatConversationNotFound
	}
	user, err := s.resolveWebChatSendUser(ctx, in)
	if err != nil {
		return nil, err
	}
	caps, err := s.resolveWebChatSendCapability(in.Provider, in.Model)
	if err != nil {
		return nil, err
	}
	if in.GinContext == nil || in.GinContext.Request == nil {
		return nil, ErrWebChatContextRequired
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
	dispatchResult, err := s.dispatchChatCompletions(in.GinContext, webChatDispatchInput{
		User:               user,
		ConversationID:     in.ConversationID,
		AssistantMessageID: assistantMessage.ID,
		Model:              caps.Model,
		Provider:           caps.Provider,
		Capabilities:       caps,
		Messages:           messages,
		Stream:             in.Stream,
	})
	if err != nil {
		status := WebChatMessageStatusFailed
		errMsg := err.Error()
		_, _ = s.repo.UpdateMessage(context.WithoutCancel(ctx), user.ID, assistantMessage.ID, UpdateWebChatMessageInput{
			Status:       &status,
			ErrorMessage: &errMsg,
		})
		return nil, err
	}

	status := WebChatMessageStatusCompleted
	content := ExtractAssistantTextFromChatCompletions(dispatchResult.ResponseBody, in.Stream)
	update := UpdateWebChatMessageInput{
		ContentText: &content,
		Status:      &status,
	}
	if dispatchResult.UsageLogID != nil {
		update.UsageLogID = dispatchResult.UsageLogID
	}
	if _, err := s.repo.UpdateMessage(ctx, user.ID, assistantMessage.ID, update); err != nil {
		return nil, err
	}
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

func (s *WebChatService) resolveWebChatSendCapability(provider, model string) (WebChatModelCapability, error) {
	if s.capabilityResolver == nil {
		return WebChatModelCapability{}, ErrWebChatInvalidModel
	}
	caps, err := s.capabilityResolver.ResolveWebChatCapability(strings.TrimSpace(provider), strings.TrimSpace(model))
	if err != nil {
		return WebChatModelCapability{}, err
	}
	if caps.Model == "" || caps.Provider == "" || caps.Platform == "" {
		return WebChatModelCapability{}, ErrWebChatInvalidModel
	}
	return caps, nil
}

func webChatMessageListContains(messages []WebChatMessage, id int64) bool {
	for _, message := range messages {
		if message.ID == id {
			return true
		}
	}
	return false
}

type UploadWebChatAttachmentInput struct {
	UserID      int64
	Filename    string
	ContentType string
	Reader      io.Reader
}

func (s *WebChatService) UploadAttachment(ctx context.Context, in UploadWebChatAttachmentInput) (*WebChatAttachment, error) {
	if s == nil || s.attachmentRepo == nil || s.storage == nil || in.UserID <= 0 || in.Reader == nil {
		return nil, ErrWebChatUploadRejected
	}

	contentType, kind, textPreviewEnabled, err := classifyWebChatUploadContentType(in.ContentType)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(in.Reader, webChatMaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > webChatMaxUploadBytes {
		return nil, ErrWebChatUploadRejected
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

func classifyWebChatUploadContentType(raw string) (string, string, bool, error) {
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil || contentType == "" {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType = strings.ToLower(contentType)

	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return contentType, WebChatAttachmentKindImage, false, nil
	case "text/plain", "text/markdown", "application/json", "text/csv":
		return contentType, WebChatAttachmentKindFile, true, nil
	case "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return contentType, WebChatAttachmentKindFile, false, nil
	default:
		return "", "", false, ErrWebChatUploadRejected
	}
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

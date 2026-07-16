package handler

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const webChatMaxMultipartUploadBytes = (20 << 20) + (1 << 20)

type WebChatService interface {
	ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.WebChatConversation, *pagination.PaginationResult, error)
	CreateConversation(ctx context.Context, userID int64, in service.CreateWebChatConversationInput) (*service.WebChatConversation, error)
	GetConversation(ctx context.Context, userID, conversationID int64) (*service.WebChatConversationDetail, error)
	UpdateConversation(ctx context.Context, userID, conversationID int64, in service.UpdateWebChatConversationInput) (*service.WebChatConversation, error)
	DeleteConversation(ctx context.Context, userID, conversationID int64) error
	UploadAttachment(ctx context.Context, userID int64, file multipart.File, header *multipart.FileHeader) (*service.WebChatAttachment, error)
	OpenAttachment(ctx context.Context, userID, attachmentID int64) (io.ReadCloser, service.WebChatDownloadMeta, error)
	OpenArtifact(ctx context.Context, userID, artifactID int64) (io.ReadCloser, service.WebChatDownloadMeta, error)
	ListModels(ctx context.Context, userID int64) ([]service.WebChatModelCapability, error)
	SendMessage(c *gin.Context, in service.WebChatSendInput) (*service.WebChatSendResult, error)
	GenerateConversationTitle(c *gin.Context, userID, conversationID int64) (*service.WebChatConversation, error)
	CancelMessage(ctx context.Context, userID, conversationID, messageID int64) error
}

type WebChatHandler struct {
	service WebChatService
}

func NewWebChatHandler(service WebChatService) *WebChatHandler {
	return &WebChatHandler{service: service}
}

type webChatConversationRequest struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Title    string `json:"title"`
}

type webChatUpdateConversationRequest struct {
	Model    *string `json:"model"`
	Provider *string `json:"provider"`
	Title    *string `json:"title"`
	Status   *string `json:"status"`
}

type webChatSendMessageRequest struct {
	Model           string                               `json:"model"`
	Provider        string                               `json:"provider"`
	Content         string                               `json:"content"`
	AttachmentIDs   []int64                              `json:"attachment_ids"`
	Stream          bool                                 `json:"stream"`
	Thinking        service.WebChatThinkingConfig        `json:"thinking"`
	ImageGeneration service.WebChatImageGenerationConfig `json:"image_generation"`
	WebSearch       *service.WebChatWebSearchConfig      `json:"web_search"`
}

func (h *WebChatHandler) ListModels(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	models, err := h.service.ListModels(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, models)
}

func (h *WebChatHandler) ListConversations(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "updated_at"),
		SortOrder: c.DefaultQuery("sort_order", pagination.SortOrderDesc),
	}
	items, result, err := h.service.ListConversations(c.Request.Context(), subject.UserID, params)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	total := int64(0)
	if result != nil {
		total = result.Total
		page = result.Page
		pageSize = result.PageSize
	}
	response.Paginated(c, items, total, page, pageSize)
}

func (h *WebChatHandler) CreateConversation(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	var req webChatConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	conversation, err := h.service.CreateConversation(c.Request.Context(), subject.UserID, service.CreateWebChatConversationInput{
		Title:           strings.TrimSpace(req.Title),
		DefaultModel:    strings.TrimSpace(req.Model),
		DefaultProvider: strings.TrimSpace(req.Provider),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, conversation)
}

func (h *WebChatHandler) GetConversation(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetConversation(c.Request.Context(), subject.UserID, conversationID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}

func (h *WebChatHandler) UpdateConversation(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	var req webChatUpdateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	in := service.UpdateWebChatConversationInput{
		Title:           trimStringPtr(req.Title),
		DefaultModel:    trimStringPtr(req.Model),
		DefaultProvider: trimStringPtr(req.Provider),
		Status:          trimStringPtr(req.Status),
	}
	conversation, err := h.service.UpdateConversation(c.Request.Context(), subject.UserID, conversationID, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, conversation)
}

func (h *WebChatHandler) DeleteConversation(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteConversation(c.Request.Context(), subject.UserID, conversationID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *WebChatHandler) GenerateConversationTitle(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	conversation, err := h.service.GenerateConversationTitle(c, subject.UserID, conversationID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, conversation)
}

func (h *WebChatHandler) SendMessage(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	var req webChatSendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	webSearch := service.WebChatWebSearchConfig{}
	if req.WebSearch != nil {
		webSearch = *req.WebSearch
		webSearch.Configured = true
	}
	_, err := h.service.SendMessage(c, service.WebChatSendInput{
		UserID:         subject.UserID,
		ConversationID: conversationID,
		Model:          strings.TrimSpace(req.Model),
		Provider:       strings.TrimSpace(req.Provider),
		Text:           req.Content,
		AttachmentIDs:  req.AttachmentIDs,
		Stream:         req.Stream,
		Thinking: service.WebChatThinkingConfig{
			Enabled: req.Thinking.Enabled,
			Effort:  strings.TrimSpace(req.Thinking.Effort),
		},
		ImageGeneration: service.WebChatImageGenerationConfig{
			Enabled:      req.ImageGeneration.Enabled,
			Size:         strings.TrimSpace(req.ImageGeneration.Size),
			AspectRatio:  strings.TrimSpace(req.ImageGeneration.AspectRatio),
			Quality:      strings.TrimSpace(req.ImageGeneration.Quality),
			OutputFormat: strings.TrimSpace(req.ImageGeneration.OutputFormat),
			Background:   strings.TrimSpace(req.ImageGeneration.Background),
		},
		WebSearch:  webSearch,
		GinContext: c,
	})
	if err != nil && !c.Writer.Written() {
		response.ErrorFrom(c, err)
	}
}

func (h *WebChatHandler) CancelMessage(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	conversationID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	messageID, ok := webChatIDParam(c, "message_id")
	if !ok {
		return
	}
	if err := h.service.CancelMessage(c.Request.Context(), subject.UserID, conversationID, messageID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"canceled": true})
}

func (h *WebChatHandler) UploadAttachment(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, webChatMaxMultipartUploadBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "Invalid upload: "+err.Error())
		return
	}
	defer func() { _ = file.Close() }()

	attachment, err := h.service.UploadAttachment(c.Request.Context(), subject.UserID, file, header)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, attachment)
}

func (h *WebChatHandler) DownloadAttachment(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	attachmentID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	rc, meta, err := h.service.OpenAttachment(c.Request.Context(), subject.UserID, attachmentID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	webChatStreamDownload(c, rc, meta)
}

func (h *WebChatHandler) DownloadArtifact(c *gin.Context) {
	subject, ok := webChatAuthSubject(c)
	if !ok {
		return
	}
	artifactID, ok := webChatIDParam(c, "id")
	if !ok {
		return
	}
	rc, meta, err := h.service.OpenArtifact(c.Request.Context(), subject.UserID, artifactID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	webChatStreamDownload(c, rc, meta)
}

func webChatAuthSubject(c *gin.Context) (servermiddleware.AuthSubject, bool) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return servermiddleware.AuthSubject{}, false
	}
	return subject, true
}

func webChatIDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param(name)), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

func webChatStreamDownload(c *gin.Context, rc io.Reader, meta service.WebChatDownloadMeta) {
	contentType := strings.TrimSpace(meta.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Filename}))
	c.Header("Content-Length", strconv.FormatInt(meta.SizeBytes, 10))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, rc)
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

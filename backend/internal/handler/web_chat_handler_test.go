package handler_test

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebChatRoutesAreRegisteredForRegularUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatUserRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil)
	router.ServeHTTP(w, req)

	// WebChat is open to all authenticated users (no admin gate). The route is
	// registered under the authenticated user group, so a regular user reaches
	// the handler (200) rather than a 404.
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.listCalled)
}

func TestModelCatalogRouteIsRegisteredForRegularUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newWebChatUserRoutesTestRouter(&fakeWebChatService{}, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-catalog", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"models"`)
}

func TestWebChatAdminRoutesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatAdminRoutesTestRouter(fake, 0)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, fake.listCalled)
}

func TestWebChatCreateConversationUsesCurrentUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{
		createConversation: &service.WebChatConversation{
			ID:              77,
			UserID:          42,
			Title:           "work",
			DefaultModel:    "claude-sonnet-4-20250514",
			DefaultProvider: "anthropic",
		},
	}
	router := newWebChatAdminRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/conversations",
		strings.NewReader(`{"model":"claude-sonnet-4-20250514","provider":"anthropic","title":"work"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.True(t, fake.createCalled)
	require.Equal(t, int64(42), fake.createUserID)
	require.Equal(t, service.CreateWebChatConversationInput{
		Title:           "work",
		DefaultModel:    "claude-sonnet-4-20250514",
		DefaultProvider: "anthropic",
	}, fake.createInput)
}

func TestWebChatSendMessageBindsThinkingSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatAdminRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/conversations/7/messages",
		strings.NewReader(`{"model":"gpt-5.4","provider":"openai","content":"hello","stream":true,"thinking":{"enabled":true,"effort":"high"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.sendCalled)
	require.Equal(t, int64(42), fake.sendInput.UserID)
	require.Equal(t, int64(7), fake.sendInput.ConversationID)
	require.Equal(t, "gpt-5.4", fake.sendInput.Model)
	require.Equal(t, "openai", fake.sendInput.Provider)
	require.True(t, fake.sendInput.Thinking.Enabled)
	require.Equal(t, "high", fake.sendInput.Thinking.Effort)
}

func TestWebChatSendMessageBindsImageGenerationSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatAdminRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/conversations/7/messages",
		strings.NewReader(`{"model":"gpt-image-2","provider":"openai","content":"draw","stream":true,"image_generation":{"enabled":true,"size":"1536x1024","aspect_ratio":"3:2","quality":"high","output_format":"webp","background":"auto"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.sendCalled)
	require.Equal(t, int64(42), fake.sendInput.UserID)
	require.Equal(t, int64(7), fake.sendInput.ConversationID)
	require.Equal(t, "gpt-image-2", fake.sendInput.Model)
	require.Equal(t, "openai", fake.sendInput.Provider)
	require.True(t, fake.sendInput.ImageGeneration.Enabled)
	require.Equal(t, "1536x1024", fake.sendInput.ImageGeneration.Size)
	require.Equal(t, "3:2", fake.sendInput.ImageGeneration.AspectRatio)
	require.Equal(t, "high", fake.sendInput.ImageGeneration.Quality)
	require.Equal(t, "webp", fake.sendInput.ImageGeneration.OutputFormat)
	require.Equal(t, "auto", fake.sendInput.ImageGeneration.Background)
}

func TestWebChatSendMessageBindsWebSearchSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatAdminRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/conversations/7/messages",
		strings.NewReader(`{"model":"gpt-5.5","provider":"openai","content":"latest news","stream":true,"web_search":{"enabled":true}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.sendCalled)
	require.Equal(t, int64(42), fake.sendInput.UserID)
	require.Equal(t, int64(7), fake.sendInput.ConversationID)
	require.Equal(t, "gpt-5.5", fake.sendInput.Model)
	require.Equal(t, "openai", fake.sendInput.Provider)
	require.True(t, fake.sendInput.WebSearch.Enabled)
	require.True(t, fake.sendInput.WebSearch.Configured)
}

func TestWebChatGenerateTitleUsesCurrentUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{
		generatedTitleConversation: &service.WebChatConversation{
			ID:     7,
			UserID: 42,
			Title:  "Generated title",
		},
	}
	router := newWebChatAdminRoutesTestRouter(fake, 42)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/title/generate", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.generateTitleCalled)
	require.Equal(t, int64(42), fake.generateTitleUserID)
	require.Equal(t, int64(7), fake.generateTitleConversationID)
	require.Contains(t, w.Body.String(), `"title":"Generated title"`)
}

func newWebChatUserRoutesTestRouter(fake *fakeWebChatService, userID int64) *gin.Engine {
	router := gin.New()
	v1 := router.Group("/api/v1")
	routes.RegisterUserRoutes(v1, &handler.Handlers{
		User:             &handler.UserHandler{},
		APIKey:           &handler.APIKeyHandler{},
		Usage:            &handler.UsageHandler{},
		Redeem:           &handler.RedeemHandler{},
		Subscription:     &handler.SubscriptionHandler{},
		Announcement:     &handler.AnnouncementHandler{},
		ChannelMonitor:   &handler.ChannelMonitorUserHandler{},
		Totp:             &handler.TotpHandler{},
		AvailableChannel: &handler.AvailableChannelHandler{},
		Setting:          &handler.SettingHandler{},
		WebChat:          handler.NewWebChatHandler(fake),
	}, servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
		}
		c.Next()
	}), nil)
	return router
}

func newWebChatAdminRoutesTestRouter(fake *fakeWebChatService, userID int64) *gin.Engine {
	router := gin.New()
	admin := router.Group("/api/v1")
	admin.Use(func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"message": "Authorization required"})
		c.Abort()
	})
	chat := admin.Group("/chat")
	webChat := handler.NewWebChatHandler(fake)
	{
		chat.GET("/models", webChat.ListModels)
		chat.GET("/conversations", webChat.ListConversations)
		chat.POST("/conversations", webChat.CreateConversation)
		chat.GET("/conversations/:id", webChat.GetConversation)
		chat.PATCH("/conversations/:id", webChat.UpdateConversation)
		chat.DELETE("/conversations/:id", webChat.DeleteConversation)
		chat.POST("/conversations/:id/title/generate", webChat.GenerateConversationTitle)
		chat.POST("/conversations/:id/messages", webChat.SendMessage)
		chat.POST("/conversations/:id/messages/:message_id/cancel", webChat.CancelMessage)
		chat.POST("/attachments", webChat.UploadAttachment)
		chat.GET("/attachments/:id/download", webChat.DownloadAttachment)
		chat.GET("/artifacts/:id/download", webChat.DownloadArtifact)
	}
	return router
}

type fakeWebChatService struct {
	listCalled bool

	createCalled       bool
	createUserID       int64
	createInput        service.CreateWebChatConversationInput
	createConversation *service.WebChatConversation

	sendCalled bool
	sendInput  service.WebChatSendInput

	generateTitleCalled         bool
	generateTitleUserID         int64
	generateTitleConversationID int64
	generatedTitleConversation  *service.WebChatConversation
}

func (s *fakeWebChatService) ListConversations(context.Context, int64, pagination.PaginationParams) ([]service.WebChatConversation, *pagination.PaginationResult, error) {
	s.listCalled = true
	return nil, &pagination.PaginationResult{Page: 1, PageSize: 20, Pages: 1}, nil
}

func (s *fakeWebChatService) CreateConversation(_ context.Context, userID int64, in service.CreateWebChatConversationInput) (*service.WebChatConversation, error) {
	s.createCalled = true
	s.createUserID = userID
	s.createInput = in
	return s.createConversation, nil
}

func (s *fakeWebChatService) GetConversation(context.Context, int64, int64) (*service.WebChatConversationDetail, error) {
	panic("unexpected GetConversation call")
}

func (s *fakeWebChatService) UpdateConversation(context.Context, int64, int64, service.UpdateWebChatConversationInput) (*service.WebChatConversation, error) {
	panic("unexpected UpdateConversation call")
}

func (s *fakeWebChatService) DeleteConversation(context.Context, int64, int64) error {
	panic("unexpected DeleteConversation call")
}

func (s *fakeWebChatService) UploadAttachment(context.Context, int64, multipart.File, *multipart.FileHeader) (*service.WebChatAttachment, error) {
	panic("unexpected UploadAttachment call")
}

func (s *fakeWebChatService) OpenAttachment(context.Context, int64, int64) (io.ReadCloser, service.WebChatDownloadMeta, error) {
	panic("unexpected OpenAttachment call")
}

func (s *fakeWebChatService) OpenArtifact(context.Context, int64, int64) (io.ReadCloser, service.WebChatDownloadMeta, error) {
	panic("unexpected OpenArtifact call")
}

func (s *fakeWebChatService) ListModels(context.Context, int64) ([]service.WebChatModelCapability, error) {
	panic("unexpected ListModels call")
}

func (s *fakeWebChatService) SendMessage(_ *gin.Context, in service.WebChatSendInput) (*service.WebChatSendResult, error) {
	s.sendCalled = true
	s.sendInput = in
	return &service.WebChatSendResult{UserMessageID: 100, AssistantMessageID: 101}, nil
}

func (s *fakeWebChatService) GenerateConversationTitle(_ *gin.Context, userID, conversationID int64) (*service.WebChatConversation, error) {
	s.generateTitleCalled = true
	s.generateTitleUserID = userID
	s.generateTitleConversationID = conversationID
	return s.generatedTitleConversation, nil
}

func (s *fakeWebChatService) CancelMessage(context.Context, int64, int64, int64) error {
	panic("unexpected CancelMessage call")
}

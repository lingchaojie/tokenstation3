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

func TestWebChatRoutesRequireAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeWebChatService{}
	router := newWebChatRoutesTestRouter(fake, 0)

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
	router := newWebChatRoutesTestRouter(fake, 42)

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

func newWebChatRoutesTestRouter(fake *fakeWebChatService, userID int64) *gin.Engine {
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
		WebChat:          handler.NewWebChatHandler(fake),
	}, servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
		}
		c.Next()
	}), nil)
	return router
}

type fakeWebChatService struct {
	listCalled bool

	createCalled       bool
	createUserID       int64
	createInput        service.CreateWebChatConversationInput
	createConversation *service.WebChatConversation
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

func (s *fakeWebChatService) SendMessage(*gin.Context, service.WebChatSendInput) (*service.WebChatSendResult, error) {
	panic("unexpected SendMessage call")
}

func (s *fakeWebChatService) CancelMessage(context.Context, int64, int64, int64) error {
	panic("unexpected CancelMessage call")
}

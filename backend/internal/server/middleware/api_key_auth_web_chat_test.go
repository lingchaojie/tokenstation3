package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type webChatAuthRepoStub struct {
	service.APIKeyRepository
	apiKey *service.APIKey
}

func (r webChatAuthRepoStub) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if r.apiKey == nil || key != r.apiKey.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.apiKey
	return &clone, nil
}

func TestAPIKeyAuthRejectsWebChatKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	apiKey := &service.APIKey{
		ID:      100,
		UserID:  7,
		Key:     "wc-leaked-key",
		KeyType: service.APIKeyTypeWebChat,
		Status:  service.StatusActive,
		User: &service.User{
			ID:          7,
			Role:        service.RoleUser,
			Status:      service.StatusActive,
			Balance:     10,
			Concurrency: 3,
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKeyService := service.NewAPIKeyService(webChatAuthRepoStub{apiKey: apiKey}, nil, nil, nil, nil, nil, cfg)

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "INVALID_API_KEY", resp.Code)
	require.Equal(t, "Invalid API key", resp.Message)
}

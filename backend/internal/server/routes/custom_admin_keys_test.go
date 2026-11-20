//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type legacyAccessSettingStore struct {
	service.SettingRepository
	current string
}

func (r *legacyAccessSettingStore) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyAdminAPIKey && r.current != "" {
		return r.current, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *legacyAccessSettingStore) Set(_ context.Context, _ string, value string) error {
	r.current = value
	return nil
}

func (r *legacyAccessSettingStore) Delete(context.Context, string) error {
	r.current = ""
	return nil
}

type legacyAccessAdminUser struct{ scopedRouteUsers }

func (*legacyAccessAdminUser) GetFirstAdmin(context.Context) (*service.User, error) {
	return &service.User{ID: 9, Role: service.RoleAdmin, Status: service.StatusActive}, nil
}

func TestLegacyAdminKeyLifecycleAndScopedKeyBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &legacyAccessSettingStore{current: "admin-legacy-credential"}
	settingService := service.NewSettingService(store, nil)
	keyRepo := &scopedRouteKeys{}
	settingService.SetScopedAdminKeyRepository(keyRepo)
	users := service.NewUserService(&legacyAccessAdminUser{}, nil, nil, nil)
	machineAuth := middleware.NewAdminAuthMiddleware(nil, users, settingService, nil)
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("x-api-key") != "" {
			gin.HandlerFunc(machineAuth)(c)
			return
		}
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	r := gin.New()
	settingHandler := adminhandler.NewSettingHandler(settingService, nil, nil, nil, nil, nil, nil)
	RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{Setting: settingHandler}}, auth, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), stepUp, nil)
	serve := func(method, path, key, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/admin/settings"+path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	legacy := store.current
	require.Equal(t, http.StatusOK, serve(http.MethodGet, "/admin-api-key", legacy, "").Code)
	w := serve(http.MethodPost, "/admin-api-key/regenerate", legacy, "{}")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotEqual(t, legacy, store.current)
	require.Contains(t, w.Body.String(), store.current)
	require.Equal(t, http.StatusUnauthorized, serve(http.MethodGet, "/admin-api-key", legacy, "").Code)
	require.Equal(t, http.StatusForbidden, serve(http.MethodPost, "/admin-api-keys", store.current, "{}").Code)
	require.Equal(t, http.StatusOK, serve(http.MethodDelete, "/admin-api-key", store.current, "").Code)
	require.Empty(t, store.current)
	require.Equal(t, http.StatusOK, serve(http.MethodPost, "/admin-api-key/regenerate", "", "{}").Code)
	require.NotEmpty(t, store.current)

	for _, body := range []string{
		`{"name":"unrestricted","policy":{"full_access":true}}`,
		`{"name":"unrestricted","policy":{"full_access":true},"confirm_full_access":true}`,
		`{"name":"payment","policy":{"global_permissions":["payment.read"]}}`,
	} {
		w = serve(http.MethodPost, "/admin-api-keys", "", body)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Nil(t, keyRepo.item)
	}
	w = serve(http.MethodPost, "/admin-api-keys", "", `{"name":"scoped","enabled":true,"policy":{"global_permissions":["users.read"]}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, keyRepo.item)
	require.NotContains(t, w.Body.String(), "full_access")
}

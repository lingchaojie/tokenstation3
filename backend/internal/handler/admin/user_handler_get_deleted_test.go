package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type getByIDAdminStub struct {
	service.AdminService
}

func (s *getByIDAdminStub) GetUser(_ context.Context, _ int64) (*service.User, error) {
	return nil, service.ErrUserNotFound
}

func (s *getByIDAdminStub) GetUserIncludeDeleted(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Email: "del@test.com"}, nil
}

func setupGetByIDRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewUserHandler(svc, nil, nil, nil)
	r.GET("/admin/users/:id", h.GetByID)
	return r
}

func TestAdminUserGetByID_IncludeDeleted(t *testing.T) {
	svc := &getByIDAdminStub{AdminService: newStubAdminService()}
	router := setupGetByIDRouter(svc)

	t.Run("normal path returns 404 for deleted user", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/admin/users/7", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("include_deleted=true returns 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/admin/users/7?include_deleted=true", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})
}

type userAPIKeyRoutesAdminStub struct {
	service.AdminService
	routes *service.UserAPIKeyRoutes
}

func (s *userAPIKeyRoutesAdminStub) GetUserAPIKeyRoutes(_ context.Context, _ int64) (*service.UserAPIKeyRoutes, error) {
	return s.routes, nil
}

func (s *userAPIKeyRoutesAdminStub) UpdateUserAPIKeyRoutes(_ context.Context, _ int64, _ service.UserAPIKeyRouteUpdate) (*service.UserAPIKeyRoutes, error) {
	return s.routes, nil
}

func setupUserAPIKeyRoutesRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewUserHandler(svc, nil, nil, nil)
	r.GET("/admin/users/:id/api-key-routes", h.GetUserAPIKeyRoutes)
	r.PUT("/admin/users/:id/api-key-routes", h.UpdateUserAPIKeyRoutes)
	return r
}

func TestAdminUserAPIKeyRoutes_ResponseUsesSnakeCaseDTO(t *testing.T) {
	createdAt := time.Date(2026, 6, 12, 1, 2, 3, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	routes := &service.UserAPIKeyRoutes{
		Anthropic: &service.UserAPIKeyRoute{
			ID:        100,
			UserID:    42,
			KeyType:   service.APIKeyTypeAnthropic,
			GroupID:   10,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Group: &service.Group{
				ID:          10,
				Name:        "anthropic group",
				Description: "internal description should not be exposed here",
				Platform:    service.PlatformAnthropic,
				Status:      service.StatusActive,
				CreatedAt:   createdAt,
				UpdatedAt:   updatedAt,
			},
		},
	}
	router := setupUserAPIKeyRoutesRouter(&userAPIKeyRoutesAdminStub{AdminService: newStubAdminService(), routes: routes})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/users/42/api-key-routes", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeJSONResponse(t, w.Body.Bytes())
	data := requireJSONObject(t, body["data"], "data")
	anthropic := requireJSONObject(t, data["anthropic"], "data.anthropic")
	require.Equal(t, float64(42), anthropic["user_id"])
	require.Equal(t, service.APIKeyTypeAnthropic, anthropic["key_type"])
	require.Equal(t, float64(10), anthropic["group_id"])
	require.Contains(t, anthropic, "created_at")
	require.Contains(t, anthropic, "updated_at")
	require.NotContains(t, anthropic, "UserID")
	require.NotContains(t, anthropic, "KeyType")
	require.NotContains(t, anthropic, "GroupID")
	group := requireJSONObject(t, anthropic["group"], "data.anthropic.group")
	require.Equal(t, float64(10), group["id"])
	require.Equal(t, "anthropic group", group["name"])
	require.Equal(t, service.PlatformAnthropic, group["platform"])
	require.NotContains(t, group, "description")
	require.NotContains(t, group, "Description")
}

func TestAdminUserAPIKeyRoutes_UpdateResponseUsesSnakeCaseDTO(t *testing.T) {
	routes := &service.UserAPIKeyRoutes{
		OpenAI: &service.UserAPIKeyRoute{
			ID:      200,
			UserID:  42,
			KeyType: service.APIKeyTypeOpenAI,
			GroupID: 20,
			Group:   &service.Group{ID: 20, Name: "openai group", Platform: service.PlatformOpenAI},
		},
	}
	router := setupUserAPIKeyRoutesRouter(&userAPIKeyRoutesAdminStub{AdminService: newStubAdminService(), routes: routes})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/admin/users/42/api-key-routes", bytes.NewBufferString(`{"openai_group_id":20}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeJSONResponse(t, w.Body.Bytes())
	data := requireJSONObject(t, body["data"], "data")
	openAI := requireJSONObject(t, data["openai"], "data.openai")
	require.Equal(t, float64(42), openAI["user_id"])
	require.Equal(t, service.APIKeyTypeOpenAI, openAI["key_type"])
	require.NotContains(t, openAI, "UserID")
	require.NotContains(t, openAI, "KeyType")
}

func decodeJSONResponse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	return body
}

func requireJSONObject(t *testing.T, value any, path string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.Truef(t, ok, "%s must be an object", path)
	return object
}

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexModelsRouteGroupRepoStub struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (s *codexModelsRouteGroupRepoStub) GetByID(_ context.Context, id int64) (*service.Group, error) {
	group := s.groups[id]
	if group == nil {
		return nil, service.ErrGroupNotFound
	}
	clone := *group
	return &clone, nil
}

type codexModelsRouteDefaultGroupsStub struct {
	ids map[string]*int64
}

func (s codexModelsRouteDefaultGroupsStub) GetDefaultAPIKeyGroupID(_ context.Context, keyType string) (*int64, error) {
	return s.ids[keyType], nil
}

type codexModelsRouteAccountRepoStub struct {
	service.AccountRepository
	byGroup map[int64][]service.Account
}

func (s codexModelsRouteAccountRepoStub) ListModelAvailabilityCandidates(
	_ context.Context,
	groupID *int64,
	_ []string,
	_ bool,
) ([]service.Account, error) {
	if groupID == nil {
		return nil, nil
	}
	return append([]service.Account(nil), s.byGroup[*groupID]...), nil
}

func TestGatewayRoutesCodexModelsManifestPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	registered := make(map[string]string)
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet {
			registered[route.Path] = route.Handler
		}
	}

	require.NotEmpty(t, registered["/backend-api/codex/models"], "GET /backend-api/codex/models should be registered")
	require.NotEmpty(t, registered["/v1/models"], "GET /v1/models should be registered")
	require.NotEmpty(t, registered["/models"], "GET /models should be registered")
	require.Equal(t, registered["/v1/models"], registered["/models"], "root alias should use the same platform-aware handler")
}

func TestGatewayRoutesUnifiedCodexModelsManifestUsesLocalHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	anthropicGroupID := int64(3)
	openAIGroupID := int64(6)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}}
	groupRepo := &codexModelsRouteGroupRepoStub{groups: map[int64]*service.Group{
		anthropicGroupID: {ID: anthropicGroupID, Platform: service.PlatformAnthropic, Status: service.StatusActive},
		openAIGroupID:    {ID: openAIGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	}}
	apiKeyService := service.NewAPIKeyService(nil, nil, groupRepo, nil, nil, nil, cfg)
	apiKeyService.SetProviderRouting(nil, codexModelsRouteDefaultGroupsStub{ids: map[string]*int64{
		service.APIKeyTypeAnthropic: &anthropicGroupID,
		service.APIKeyTypeOpenAI:    &openAIGroupID,
	}})
	gatewayService := service.NewGatewayService(
		codexModelsRouteAccountRepoStub{byGroup: map[int64][]service.Account{
			anthropicGroupID: {{ID: 31, Platform: service.PlatformAnthropic, Credentials: map[string]any{
				"model_mapping": map[string]any{"shared-model": "anthropic-upstream", "claude-public": "claude-upstream"},
			}}},
			openAIGroupID: {{ID: 61, Platform: service.PlatformOpenAI, Credentials: map[string]any{
				"model_mapping": map[string]any{"shared-model": "openai-upstream", "gpt-public": "gpt-upstream"},
			}}},
		}},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil,
	)
	gatewayHandler := handler.NewGatewayHandler(
		gatewayService, nil, nil, nil, nil, nil, nil, nil, apiKeyService, nil, nil, nil, nil, cfg, nil, nil,
	)
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       gatewayHandler,
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
			AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				UserID:           42,
				User:             &service.User{ID: 42, Status: service.StatusActive},
				GroupID:          &openAIGroupID,
				Group:            groupRepo.groups[openAIGroupID],
				GroupBindingMode: service.APIKeyGroupBindingModeAuto,
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	for _, path := range []string{"/models", "/v1/models"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?client_version=0.144.0", nil)
			rec := httptest.NewRecorder()
			require.NotPanics(t, func() { router.ServeHTTP(rec, req) })

			require.Equal(t, http.StatusOK, rec.Code)
			require.JSONEq(t, `{
				"models": [
					{"slug": "claude-public"},
					{"slug": "gpt-public"},
					{"slug": "shared-model"}
				]
			}`, rec.Body.String())
		})
	}
}

func TestDispatchCodexModelsGatewayKeepsOnlyOpenAIOnLiveManifestHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		platform   string
		wantOpenAI bool
	}{
		{platform: service.PlatformOpenAI, wantOpenAI: true},
		{platform: service.PlatformGrok},
		{platform: service.PlatformDeepseek},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/models?client_version=0.147.0", nil)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				Group: &service.Group{Platform: tt.platform},
			})
			called := ""

			dispatchCodexModelsGateway(c,
				func(c *gin.Context) { called = "openai" },
				func(c *gin.Context) { called = "generated" },
			)

			if tt.wantOpenAI {
				require.Equal(t, "openai", called)
			} else {
				require.Equal(t, "generated", called)
			}
		})
	}
}

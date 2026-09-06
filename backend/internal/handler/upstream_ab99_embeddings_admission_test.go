package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type embeddingsAdmissionAccountRepo struct {
	openAIWSUsageHandlerAccountRepoStub
}

func (r *embeddingsAdmissionAccountRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return []service.Account{r.account}, nil
}

type embeddingsAdmissionUpstream struct {
	service.HTTPUpstream
	body []byte
}

func (u *embeddingsAdmissionUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var err error
	u.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.25,0.75]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)),
	}, nil
}

// Removing the original-model binding must reject the permitted requested alias
// and double-map the channel-mapped case. Passing the alias to account selection
// instead must also fail: this account supports only the native embedding name.
func TestUpstreamAB99EmbeddingsPreservesChannelAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, source, pricedModel string
		allowed                   bool
	}{
		{"requested alias admitted", service.BillingModelSourceRequested, "public-embedding", true},
		{"unpriced requested alias denied", service.BillingModelSourceRequested, "text-embedding-3-small", false},
		{"channel mapping applied once", service.BillingModelSourceChannelMapped, "text-embedding-3-small", true},
		{"upstream model admitted", service.BillingModelSourceUpstream, "text-embedding-3-small", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(10)
			price := 1e-6
			channelSvc := service.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
				channels: []service.Channel{{
					ID: 1, Status: service.StatusActive, GroupIDs: []int64{groupID}, RestrictModels: true,
					BillingModelSource: tc.source,
					ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {
						"public-embedding": "text-embedding-3-small", "text-embedding-3-small": "unpriced-second-hop",
					}},
					ModelPricing: []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{tc.pricedModel}, InputPrice: &price}},
				}},
				groupPlatforms: map[int64]string{groupID: service.PlatformOpenAI},
			}, nil, nil, nil)
			accountRepo := &embeddingsAdmissionAccountRepo{openAIWSUsageHandlerAccountRepoStub{account: service.Account{
				ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-key", "base_url": "https://embedding.example",
					"model_mapping": map[string]any{"text-embedding-3-small": "text-embedding-3-small"}},
			}}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			upstream := &embeddingsAdmissionUpstream{}
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			gateway := service.NewOpenAIGatewayService(
				accountRepo, &openAIWSUsageHandlerUsageLogRepoStub{}, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{},
				nil, nil, nil, channelSvc, nil, nil, nil, nil,
			)
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := &OpenAIGatewayHandler{
				gatewayService: gateway, billingCacheService: billingCache, apiKeyService: &service.APIKeyService{},
				concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second),
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"public-embedding","input":"hello"}`))
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 1, UserID: 1, GroupID: &groupID, User: &service.User{ID: 1, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1, Concurrency: 1})
			h.Embeddings(c)
			if !tc.allowed {
				require.GreaterOrEqual(t, w.Code, http.StatusBadRequest)
				require.Empty(t, upstream.body, "unpriced requested model must not be forwarded")
				return
			}
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, 0.25, gjson.Get(w.Body.String(), "data.0.embedding.0").Float())
			require.Equal(t, "text-embedding-3-small", gjson.GetBytes(upstream.body, "model").String())
			require.Equal(t, "hello", gjson.GetBytes(upstream.body, "input").String())
		})
	}
}

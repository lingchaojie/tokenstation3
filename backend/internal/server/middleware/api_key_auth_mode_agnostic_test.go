//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// After the mode-agnostic change: a user's active (generic/universal) subscription
// must be honored even when the routed group is in billing/standard mode.
// Previously a standard-mode group skipped subscription loading entirely and fell
// straight to a balance check, returning 403 for a $0-balance subscriber.
func TestBillingModeGroupHonorsActiveSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)

	group := &service.Group{
		ID:               1,
		Name:             "default",
		Status:           service.StatusActive,
		Platform:         service.PlatformAnthropic,
		Hydrated:         true,
		SubscriptionType: service.SubscriptionTypeStandard, // billing mode
	}
	user := &service.User{
		ID:          2,
		Role:        service.RoleUser,
		Status:      service.StatusActive,
		Balance:     0, // no balance — must still pass via the subscription
		Concurrency: 3,
	}
	apiKey := &service.APIKey{
		ID:     7,
		UserID: user.ID,
		Key:    "uni-key",
		Status: service.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*service.APIKey, error) {
			if key != apiKey.Key {
				return nil, service.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	now := time.Now()
	limit := 550.0
	genericSub := &service.UserSubscription{
		ID:                3,
		UserID:            user.ID,
		GroupID:           0, // generic / universal — not bound to any routed group
		Status:            service.SubscriptionStatusActive,
		ExpiresAt:         now.Add(720 * time.Hour),
		WeeklyWindowStart: &now,
		WeeklyUsageUSD:    0,
		SevenDayLimitUSD:  &limit,
	}
	subscriptionRepo := &stubUserSubscriptionRepo{
		getGeneric: func(ctx context.Context, userID int64) (*service.UserSubscription, error) {
			if userID != user.ID {
				return nil, service.ErrSubscriptionNotFound
			}
			clone := *genericSub
			return &clone, nil
		},
		updateStatus:   func(ctx context.Context, id int64, status string) error { return nil },
		activateWindow: func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetDaily:     func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetWeekly:    func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetMonthly:   func(ctx context.Context, id int64, start time.Time) error { return nil },
	}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	subscriptionService := service.NewSubscriptionService(nil, subscriptionRepo, nil, nil, cfg)
	t.Cleanup(subscriptionService.Stop)
	router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"billing-mode group must honor the user's active generic subscription; body: %s", w.Body.String())
}

// After the mode-agnostic change: a subscription-mode group with NO active
// subscription must fall back to the balance path (pure billing user), instead of
// hard-rejecting with 403 SUBSCRIPTION_NOT_FOUND.
func TestSubscriptionModeGroupFallsBackToBalanceWhenNoSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)

	group := &service.Group{
		ID:               3,
		Name:             "Anthropic Default",
		Status:           service.StatusActive,
		Platform:         service.PlatformAnthropic,
		Hydrated:         true,
		SubscriptionType: service.SubscriptionTypeSubscription,
	}
	user := &service.User{
		ID:          5,
		Role:        service.RoleUser,
		Status:      service.StatusActive,
		Balance:     10, // pure billing user with balance
		Concurrency: 3,
	}
	apiKey := &service.APIKey{
		ID:     9,
		UserID: user.ID,
		Key:    "balance-key",
		Status: service.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*service.APIKey, error) {
			if key != apiKey.Key {
				return nil, service.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	// No subscription of any kind for this user.
	subscriptionRepo := &stubUserSubscriptionRepo{
		getActive: func(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
			return nil, service.ErrSubscriptionNotFound
		},
		getGeneric: func(ctx context.Context, userID int64) (*service.UserSubscription, error) {
			return nil, service.ErrSubscriptionNotFound
		},
	}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	subscriptionService := service.NewSubscriptionService(nil, subscriptionRepo, nil, nil, cfg)
	t.Cleanup(subscriptionService.Stop)
	router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"subscription-mode group with no subscription must fall back to balance; body: %s", w.Body.String())
}

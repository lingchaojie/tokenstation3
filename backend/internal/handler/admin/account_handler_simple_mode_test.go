package admin

import (
	"bytes"
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

type simpleModeAccountService struct {
	*stubAdminService
	account     service.Account
	createCalls int
	updateCalls int
	bulkCalls   int
}

func (s *simpleModeAccountService) GetAccount(context.Context, int64) (*service.Account, error) {
	return &s.account, nil
}

func (s *simpleModeAccountService) CreateAccount(ctx context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	if err := s.ValidateAccountGroupBindings(ctx, input.GroupIDs); err != nil {
		return nil, err
	}
	s.createCalls++
	return &s.account, nil
}

func (s *simpleModeAccountService) UpdateAccount(ctx context.Context, _ int64, input *service.UpdateAccountInput) (*service.Account, error) {
	if input.GroupIDs != nil {
		if err := s.ValidateAccountGroupBindings(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}
	}
	s.updateCalls++
	return &s.account, nil
}

func (s *simpleModeAccountService) BulkUpdateAccounts(ctx context.Context, input *service.BulkUpdateAccountsInput) (*service.BulkUpdateAccountsResult, error) {
	if input.GroupIDs != nil {
		if err := s.ValidateAccountGroupBindings(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}
	}
	s.bulkCalls++
	return &service.BulkUpdateAccountsResult{}, nil
}

func (s *simpleModeAccountService) ValidateAccountGroupBindings(_ context.Context, _ []int64) error {
	return nil
}

func TestAccountHandlerSimpleModePreservesBasicGroupBinding(t *testing.T) {
	svc := &simpleModeAccountService{stubAdminService: newStubAdminService(), account: service.Account{ID: 3}}
	svc.groups = []service.Group{{ID: 7, Platform: service.PlatformAnthropic}}
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	r := gin.New()
	r.POST("/accounts", h.Create)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(`{"name":"account","platform":"anthropic","type":"apikey","credentials":{"key":"x"},"group_ids":[7]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, 1, svc.createCalls)
}

func TestAccountHandlerAdvancedModeKeepsFullGroupReferences(t *testing.T) {
	group := &service.Group{ID: 7, Name: "advanced", RateMultiplier: 9, SubscriptionType: service.SubscriptionTypeSubscription}
	h := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	raw, err := json.Marshal(h.buildAccountResponseWithRuntime(context.Background(), &service.Account{Groups: []*service.Group{group}}))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"rate_multiplier":9`)
	require.Contains(t, string(raw), `"subscription_type":"subscription"`)
}

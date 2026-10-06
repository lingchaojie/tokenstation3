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

// This crosses HTTP binding, partial-update merging, persisted settings and the
// runtime allowlist cache. Losing omission or explicit-empty semantics fails it.
func TestB8deSettingsAllowlistAndCampaignRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewSettingHandler(svc, nil, nil, nil, service.NewPaymentConfigService(nil, repo, nil), nil, nil)
	put := func(payload string, wantStatus int) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		h.UpdateSettings(c)
		require.Equal(t, wantStatus, rec.Code, rec.Body.String())
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		return response.Data
	}
	put(`{"cyber_policy_user_allowlist":"12, 34","payment_recharge_bonus_mode":"bonus","payment_recharge_bonus_tiers":[{"min_amount":100,"bonus_percent":10}],"payment_recharge_bonus_notice":"campaign"}`, http.StatusOK)
	require.True(t, svc.IsCyberPolicyUserAllowlisted(context.Background(), 12))
	kept := put(`{}`, http.StatusOK)
	require.Equal(t, "12, 34", kept["cyber_policy_user_allowlist"])
	require.Equal(t, "campaign", kept["payment_recharge_bonus_notice"])
	require.Equal(t, []any{map[string]any{"min_amount": float64(100), "bonus_percent": float64(10)}}, kept["payment_recharge_bonus_tiers"])
	put(`{"cyber_policy_user_allowlist":"12, invalid"}`, http.StatusBadRequest)
	require.True(t, svc.IsCyberPolicyUserAllowlisted(context.Background(), 12))
	put(`{"cyber_policy_user_allowlist":"","payment_recharge_bonus_mode":"discount","payment_recharge_bonus_tiers":[{"min_amount":0,"bonus_percent":100}]}`, http.StatusBadRequest)
	require.True(t, svc.IsCyberPolicyUserAllowlisted(context.Background(), 12), "invalid campaign must not partially save unrelated settings")
	kept = put(`{}`, http.StatusOK)
	require.Equal(t, "bonus", kept["payment_recharge_bonus_mode"])
	cleared := put(`{"cyber_policy_user_allowlist":"","payment_recharge_bonus_tiers":[],"payment_recharge_bonus_notice":""}`, http.StatusOK)
	require.Equal(t, "", cleared["cyber_policy_user_allowlist"])
	require.Equal(t, []any{}, cleared["payment_recharge_bonus_tiers"])
	require.Equal(t, "", cleared["payment_recharge_bonus_notice"])
	require.False(t, svc.IsCyberPolicyUserAllowlisted(context.Background(), 12))
}

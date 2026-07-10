//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettings_RejectsInvalidFastPolicyBeforeAnyWrite(t *testing.T) {
	tests := []struct {
		name    string
		userIDs []int64
	}{
		{name: "non-positive", userIDs: []int64{0}},
		{name: "duplicate", userIDs: []int64{42, 42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repo := &settingHandlerRepoStub{
				values: map[string]string{service.SettingKeyPromoCodeEnabled: "true"},
			}
			svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
			handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

			body := map[string]any{
				"registration_enabled": false,
				"openai_fast_policy_settings": map[string]any{
					"rules": []map[string]any{{
						"service_tier": "priority",
						"action":       "pass",
						"scope":        "all",
						"user_ids":     tt.userIDs,
					}},
				},
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")

			handler.UpdateSettings(c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Nil(t, repo.lastUpdates, "generic settings must not be written before fast policy validation")
			require.Equal(t, map[string]string{service.SettingKeyPromoCodeEnabled: "true"}, repo.values)
		})
	}
}

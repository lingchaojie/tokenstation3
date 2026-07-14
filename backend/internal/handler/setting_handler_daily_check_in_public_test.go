package handler

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

type dailyCheckInPublicRepo struct {
	values map[string]string
}

func (r *dailyCheckInPublicRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r *dailyCheckInPublicRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (r *dailyCheckInPublicRepo) Set(context.Context, string, string) error { return nil }
func (r *dailyCheckInPublicRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}
func (r *dailyCheckInPublicRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *dailyCheckInPublicRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *dailyCheckInPublicRepo) Delete(context.Context, string) error { return nil }

func TestSettingHandler_GetPublicSettings_ExposesDailyCheckInWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &dailyCheckInPublicRepo{values: map[string]string{
		service.SettingKeyDailyCheckInEnabled:      "true",
		service.SettingKeyDailyCheckInStartAt:      "2026-07-19T16:00:00Z",
		service.SettingKeyDailyCheckInDurationDays: "7",
		service.SettingKeyDailyCheckInRewardAmount: "10.00000000",
	}}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int `json:"code"`
		Data struct {
			Enabled bool   `json:"daily_check_in_enabled"`
			StartAt string `json:"daily_check_in_start_at"`
			EndAt   string `json:"daily_check_in_end_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.True(t, response.Data.Enabled)
	require.Equal(t, "2026-07-19T16:00:00Z", response.Data.StartAt)
	require.Equal(t, "2026-07-26T16:00:00Z", response.Data.EndAt)
}

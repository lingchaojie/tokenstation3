package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminCheckInUseCaseFake struct {
	config      service.DailyCheckInConfig
	state       service.DailyCheckInActivityState
	endAt       *time.Time
	getErr      error
	updateErr   error
	updateInput service.DailyCheckInConfig
}

func (f *adminCheckInUseCaseFake) GetAdminConfig(context.Context) (service.DailyCheckInConfig, service.DailyCheckInActivityState, *time.Time, error) {
	return f.config, f.state, f.endAt, f.getErr
}

func (f *adminCheckInUseCaseFake) UpdateAdminConfig(_ context.Context, cfg service.DailyCheckInConfig) (service.DailyCheckInConfig, service.DailyCheckInActivityState, *time.Time, error) {
	f.updateInput = cfg
	if f.updateErr != nil {
		return service.DailyCheckInConfig{}, "", nil, f.updateErr
	}
	return cfg, service.DailyCheckInStateUpcoming, cfg.EndAt(), nil
}

func TestAdminCheckInHandler_GetConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	fake := &adminCheckInUseCaseFake{
		config: service.DailyCheckInConfig{Enabled: true, StartAt: &start, DurationDays: 7, RewardAmount: 10},
		state:  service.DailyCheckInStateActive,
		endAt:  &end,
	}
	h := &CheckInHandler{service: fake}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/check-in/config", nil)

	h.GetConfig(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data dto.DailyCheckInConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "active", response.Data.State)
	require.Equal(t, 7, response.Data.DurationDays)
}

func TestAdminCheckInHandler_UpdateRejectsInvalidConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &adminCheckInUseCaseFake{updateErr: service.ErrDailyCheckInConfigInvalid}
	h := &CheckInHandler{service: fake}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/check-in/config", bytes.NewBufferString(
		`{"enabled":true,"start_at":null,"duration_days":0,"reward_amount":10}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateConfig(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var response struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "DAILY_CHECK_IN_CONFIG_INVALID", response.Reason)
}

func TestAdminCheckInHandler_UpdatePassesCompleteConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &adminCheckInUseCaseFake{}
	h := &CheckInHandler{service: fake}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/check-in/config", bytes.NewBufferString(
		`{"enabled":true,"start_at":"2026-07-19T16:00:00Z","duration_days":7,"reward_amount":10}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateConfig(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, fake.updateInput.Enabled)
	require.Equal(t, 7, fake.updateInput.DurationDays)
	require.InDelta(t, 10, fake.updateInput.RewardAmount, 1e-9)
	require.Equal(t, "2026-07-19T16:00:00Z", fake.updateInput.StartAt.Format(time.RFC3339))
}

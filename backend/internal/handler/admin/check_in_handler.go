package admin

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type adminCheckInUseCase interface {
	GetAdminConfig(context.Context) (service.DailyCheckInConfig, service.DailyCheckInActivityState, *time.Time, error)
	UpdateAdminConfig(context.Context, service.DailyCheckInConfig) (service.DailyCheckInConfig, service.DailyCheckInActivityState, *time.Time, error)
}

type CheckInHandler struct {
	service adminCheckInUseCase
}

func NewCheckInHandler(checkInService *service.CheckInService) *CheckInHandler {
	return &CheckInHandler{service: checkInService}
}

type UpdateDailyCheckInConfigRequest struct {
	Enabled      bool       `json:"enabled"`
	StartAt      *time.Time `json:"start_at"`
	DurationDays int        `json:"duration_days"`
	RewardAmount float64    `json:"reward_amount"`
}

func (h *CheckInHandler) GetConfig(c *gin.Context) {
	config, state, endAt, err := h.service.GetAdminConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.DailyCheckInConfigFromService(config, state, endAt))
}

func (h *CheckInHandler) UpdateConfig(c *gin.Context) {
	var request UpdateDailyCheckInConfigRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	config, state, endAt, err := h.service.UpdateAdminConfig(c.Request.Context(), service.DailyCheckInConfig{
		Enabled:      request.Enabled,
		StartAt:      request.StartAt,
		DurationDays: request.DurationDays,
		RewardAmount: request.RewardAmount,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.DailyCheckInConfigFromService(config, state, endAt))
}

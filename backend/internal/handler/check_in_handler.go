package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type checkInUseCase interface {
	GetStatus(ctx context.Context, userID int64) (service.DailyCheckInStatus, error)
	Claim(ctx context.Context, userID int64, role string) (service.DailyCheckInClaimResult, error)
}

type CheckInHandler struct {
	service checkInUseCase
}

func NewCheckInHandler(checkInService *service.CheckInService) *CheckInHandler {
	return &CheckInHandler{service: checkInService}
}

func (h *CheckInHandler) GetStatus(c *gin.Context) {
	subject, role, ok := checkInAuthContext(c)
	if !ok {
		return
	}
	if role != service.RoleUser {
		response.ErrorFrom(c, service.ErrDailyCheckInUserOnly)
		return
	}
	status, err := h.service.GetStatus(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.DailyCheckInStatusFromService(status))
}

func (h *CheckInHandler) Claim(c *gin.Context) {
	subject, role, ok := checkInAuthContext(c)
	if !ok {
		return
	}
	result, err := h.service.Claim(c.Request.Context(), subject.UserID, role)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.DailyCheckInClaimResultFromService(result))
}

func checkInAuthContext(c *gin.Context) (middleware2.AuthSubject, string, bool) {
	subject, subjectOK := middleware2.GetAuthSubjectFromContext(c)
	role, roleOK := middleware2.GetUserRoleFromContext(c)
	if !subjectOK || !roleOK {
		response.Unauthorized(c, "User not authenticated")
		return middleware2.AuthSubject{}, "", false
	}
	return subject, role, true
}

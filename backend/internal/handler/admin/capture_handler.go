package admin

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const captureSettingsRequestMaxBytes = 1 << 20

type CaptureHandler struct {
	service *service.CaptureAdminService
}

func NewCaptureHandler(captureService *service.CaptureAdminService) *CaptureHandler {
	return &CaptureHandler{service: captureService}
}

// Get returns the runtime policy and non-sensitive spool/delivery operations.
// GET /api/v1/admin/capture-settings
func (h *CaptureHandler) Get(c *gin.Context) {
	if h == nil || h.service == nil {
		response.InternalError(c, "Capture settings service is unavailable")
		return
	}
	view, err := h.service.Get(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Failed to load capture settings")
		return
	}
	response.Success(c, view)
}

// Update completely replaces the versioned runtime policy.
// PUT /api/v1/admin/capture-settings
func (h *CaptureHandler) Update(c *gin.Context) {
	if h == nil || h.service == nil {
		response.InternalError(c, "Capture settings service is unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, captureSettingsRequestMaxBytes+1))
	if err != nil {
		response.BadRequest(c, "Invalid capture settings request")
		return
	}
	if len(body) > captureSettingsRequestMaxBytes {
		response.BadRequest(c, "Capture settings request is too large")
		return
	}
	policy, err := service.DecodeCaptureRuntimePolicy(body)
	if err != nil {
		response.BadRequest(c, "Invalid capture settings: "+err.Error())
		return
	}
	view, err := h.service.Update(c.Request.Context(), policy)
	if errors.Is(err, service.ErrCaptureInfrastructureNotReady) {
		response.Error(c, http.StatusConflict, "Capture infrastructure is not ready; enable gateway.capture and verify the local sidecar spool")
		return
	}
	if errors.Is(err, service.ErrInvalidCapturePolicy) {
		response.BadRequest(c, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to update capture settings")
		return
	}
	response.Success(c, view)
}

// History returns durable minute-level loss aggregates for one supported range.
// GET /api/v1/admin/capture-settings/history?range=24h|7d|30d
func (h *CaptureHandler) History(c *gin.Context) {
	if h == nil || h.service == nil {
		response.InternalError(c, "Capture settings service is unavailable")
		return
	}
	selectedRange := strings.TrimSpace(c.Query("range"))
	if selectedRange == "" {
		selectedRange = "24h"
	}
	history, err := h.service.History(c.Request.Context(), selectedRange)
	if errors.Is(err, service.ErrInvalidCaptureHistoryRange) {
		response.BadRequest(c, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load capture loss history")
		return
	}
	response.Success(c, history)
}

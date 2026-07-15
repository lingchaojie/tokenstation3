package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const beginnerGuideMaxRequestBodyBytes int64 = 8 * 1024

type patchBeginnerGuideRequest struct {
	PromptState *service.BeginnerGuidePromptState `json:"prompt_state"`
	Progress    *service.BeginnerGuideProgress    `json:"progress"`
}

// GetBeginnerGuide returns the authenticated user's beginner guide state.
func (h *UserHandler) GetBeginnerGuide(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	state, err := h.userService.GetBeginnerGuideState(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, state)
}

// PatchBeginnerGuide updates the authenticated user's beginner guide state.
func (h *UserHandler) PatchBeginnerGuide(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, beginnerGuideMaxRequestBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var req patchBeginnerGuideRequest
	if err := decoder.Decode(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		response.BadRequest(c, "Invalid request")
		return
	}

	state, err := h.userService.PatchBeginnerGuideState(
		c.Request.Context(),
		subject.UserID,
		service.PatchBeginnerGuideStateRequest{
			PromptState: req.PromptState,
			Progress:    req.Progress,
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, state)
}

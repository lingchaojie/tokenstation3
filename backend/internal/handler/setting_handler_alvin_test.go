//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type alvinHandlerSettingRepo struct {
	service.SettingRepository
	value string
	err   error
}

func (r *alvinHandlerSettingRepo) GetValue(_ context.Context, _ string) (string, error) {
	return r.value, r.err
}

func TestSettingHandler_GetAlvin_ReturnsStandardEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &alvinHandlerSettingRepo{value: "false"}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)

	h.GetAlvin(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"code":0,"message":"success","data":{"alvin":false}}`, recorder.Body.String())
}

func TestSettingHandler_GetAlvin_ReturnsInternalErrorForDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &alvinHandlerSettingRepo{err: errors.New("database unavailable")}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)

	h.GetAlvin(c)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	var body struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, http.StatusInternalServerError, body.Code)
}

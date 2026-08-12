package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type captureAdminSettingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func (r *captureAdminSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r *captureAdminSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}
func (r *captureAdminSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}
func (r *captureAdminSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *captureAdminSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *captureAdminSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *captureAdminSettingRepo) Delete(context.Context, string) error { return nil }

type captureAdminHealthRepo struct{}

func (*captureAdminHealthRepo) UpsertEvents(context.Context, []service.CaptureHealthEvent) error {
	return nil
}
func (*captureAdminHealthRepo) ListEvents(context.Context, time.Time, time.Time) ([]service.CaptureHealthEvent, error) {
	return []service.CaptureHealthEvent{}, nil
}
func (*captureAdminHealthRepo) DeleteBefore(context.Context, time.Time) (int64, error) { return 0, nil }

func TestCaptureSettingsResponseDoesNotLeakCredentials(t *testing.T) {
	h := newCaptureHandlerForTest(&config.GatewayCaptureConfig{
		Enabled: true,
		ClickHouse: config.CaptureClickHouseConfig{
			Addr:     []string{"clickhouse://archive-user:super-secret@db.example.com:9440/llm?token=hidden"},
			Database: "llm_archive", Table: "model_call_archive", Username: "archive-user", Password: "super-secret",
		},
	})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/capture-settings", nil)

	h.Get(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "super-secret")
	require.NotContains(t, recorder.Body.String(), `"password"`)
	require.Contains(t, recorder.Body.String(), "clickhouse://db.example.com:9440")
}

func TestCaptureSettingsPUTRejectsEnableWhenUnready(t *testing.T) {
	h := newCaptureHandlerForTest(nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/capture-settings", strings.NewReader(`{
		"version":1,"enabled":true,
		"platforms":{"anthropic":true,"kiro":true,"openai":false},
		"outcomes":{"success":true,"terminal_error":true},
		"content":{"raw_request":true,"raw_response":true,"request_headers":true,"response_headers":true},
		"group_ids":[],"user_ids":[]
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Update(c)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not ready")
}

func TestCaptureSettingsPUTRejectsUnknownAndInvalidFields(t *testing.T) {
	h := newCaptureHandlerForTest(nil)
	for name, body := range map[string]string{
		"unknown": `{"version":1,"enabled":false,"unexpected":true}`,
		"version": `{"version":2,"enabled":false}`,
		"user id": `{"version":1,"enabled":false,"user_ids":[0]}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/capture-settings", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.Update(c)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestCaptureSettingsHistoryRejectsInvalidRange(t *testing.T) {
	h := newCaptureHandlerForTest(nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/capture-settings/history?range=1h", nil)

	h.History(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func newCaptureHandlerForTest(captureConfig *config.GatewayCaptureConfig) *CaptureHandler {
	cfg := &config.Config{}
	if captureConfig != nil {
		cfg.Gateway.Capture = *captureConfig
	}
	settings := service.NewSettingService(&captureAdminSettingRepo{values: map[string]string{}}, nil)
	adminService := service.NewCaptureAdminService(cfg, settings, nil, &captureAdminHealthRepo{})
	return NewCaptureHandler(adminService)
}

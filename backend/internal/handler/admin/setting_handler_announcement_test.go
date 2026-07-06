package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newAnnouncementTestHandler wires up a SettingHandler backed by an in-memory
// settingHandlerRepoStub (defined in setting_handler_auth_source_defaults_test.go),
// matching the scaffolding used by the other admin setting handler tests.
func newAnnouncementTestHandler(values map[string]string) (*SettingHandler, *settingHandlerRepoStub) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: values}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	return handler, repo
}

func TestSettingHandler_UpdateSettings_AnnouncementBannersRoundTrip(t *testing.T) {
	handler, repo := newAnnouncementTestHandler(map[string]string{
		service.SettingKeyPromoCodeEnabled: "true",
	})

	body := map[string]any{
		"promo_code_enabled": true,
		"announcement_banners": []map[string]any{
			{"text_zh": "公告一", "text_en": "Announcement one"},
			{"text_zh": "公告二", "text_en": "Announcement two"},
		},
		"announcement_banner_interval_ms": 5000,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)

	// GET should round-trip the 2 banners and the interval.
	getRec := httptest.NewRecorder()
	getC, _ := gin.CreateTestContext(getRec)
	getC.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)

	handler.GetSettings(getC)
	require.Equal(t, http.StatusOK, getRec.Code)

	var resp response.Response
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)

	banners, ok := data["announcement_banners"].([]any)
	require.True(t, ok)
	require.Len(t, banners, 2)

	require.Equal(t, float64(5000), data["announcement_banner_interval_ms"])
	require.NotEmpty(t, repo.values[service.SettingKeyAnnouncementBanners])
	require.Equal(t, "5000", repo.values[service.SettingKeyAnnouncementBannerIntervalMs])
}

func TestSettingHandler_UpdateSettings_AnnouncementBannersRejectsTooMany(t *testing.T) {
	handler, _ := newAnnouncementTestHandler(map[string]string{
		service.SettingKeyPromoCodeEnabled: "true",
	})

	banners := make([]map[string]any, 21)
	for i := range banners {
		banners[i] = map[string]any{"text_zh": "公告", "text_en": "Announcement"}
	}
	body := map[string]any{
		"promo_code_enabled":   true,
		"announcement_banners": banners,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Message, "Too many announcement banners")
}

func TestSettingHandler_UpdateSettings_AnnouncementBannersRejectsBothTextsEmpty(t *testing.T) {
	handler, _ := newAnnouncementTestHandler(map[string]string{
		service.SettingKeyPromoCodeEnabled: "true",
	})

	body := map[string]any{
		"promo_code_enabled": true,
		"announcement_banners": []map[string]any{
			{"text_zh": "", "text_en": ""},
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Message, "at least one non-empty text")
}

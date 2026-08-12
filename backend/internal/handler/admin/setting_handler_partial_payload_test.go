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

// Saving settings is a whole-document PUT. A client that sends only the field it
// cares about must not reset everything else: a payload as small as
// `{"risk_control_enabled":true}` used to clear site_name, after which
// getStringOrDefault rendered the empty value as the built-in default and the
// login page silently changed name.

func newPartialPayloadTestHandler(t *testing.T, stored map[string]string) (*SettingHandler, *settingHandlerRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: stored}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func doPartialSettingsUpdate(t *testing.T, h *SettingHandler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateSettings(c)
	return rec
}

func TestUpdateSettingsOmittingPreservesLocalExtensions(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "kiro scheduling threshold", key: "account_scheduling_thresholds", value: `{"kiro":87}`},
		{name: "public plans", key: service.SettingKeyPurchaseSubscriptionURL, value: "https://plans.example.com"},
		{name: "IkunPay routing", key: "payment_visible_method_alipay_source", value: "ikunpay_alipay"},
		{name: "reward", key: service.SettingKeyAffiliateInviterReward, value: "13.00000000"},
		{name: "affiliate", key: service.SettingKeyAffiliateEnabled, value: "true"},
		{name: "check-in", key: service.SettingKeyDailyCheckInEnabled, value: "true"},
		{name: "capture config extension", key: "gateway_capture_enabled", value: "true"},
		{name: "hidden passkey switch", key: service.SettingKeyPasskeyEnabled, value: "true"},
		{name: "announcement", key: service.SettingKeyAnnouncementBanners, value: `[{"id":"maintenance","zh":"维护中"}]`},
		{name: "alvin", key: service.SettingKeyAlvin, value: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := newPartialPayloadTestHandler(t, map[string]string{
				tt.key:                     tt.value,
				service.SettingKeySiteName: "Local Gateway",
			})

			rec := doPartialSettingsUpdate(t, h, map[string]any{"risk_control_enabled": true})

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "true", repo.values[service.SettingKeyRiskControlEnabled])
			require.Equal(t, tt.value, repo.values[tt.key])
			require.Equal(t, "Local Gateway", repo.values[service.SettingKeySiteName])
		})
	}
}

func TestUpdateSettingsPartialPayloadKeepsUnsentKeys(t *testing.T) {
	h, repo := newPartialPayloadTestHandler(t, map[string]string{
		service.SettingKeySiteName:         "Example Gateway",
		service.SettingKeySiteSubtitle:     "Example Gateway Platform",
		service.SettingKeySMTPHost:         "smtp.example.com",
		service.SettingKeySMTPFrom:         "noreply@example.com",
		service.SettingKeyTurnstileEnabled: "true",
	})

	rec := doPartialSettingsUpdate(t, h, map[string]any{"risk_control_enabled": true})
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "true", repo.values[service.SettingKeyRiskControlEnabled],
		"the field the caller actually sent must be written")

	require.Equal(t, "Example Gateway", repo.values[service.SettingKeySiteName])
	require.Equal(t, "Example Gateway Platform", repo.values[service.SettingKeySiteSubtitle])
	require.Equal(t, "smtp.example.com", repo.values[service.SettingKeySMTPHost])
	require.Equal(t, "noreply@example.com", repo.values[service.SettingKeySMTPFrom])
	require.Equal(t, "true", repo.values[service.SettingKeyTurnstileEnabled])
}

// A full payload keeps whole-document semantics: fields explicitly set to their
// zero value are still cleared.
func TestUpdateSettingsFullPayloadStillClearsSentEmptyFields(t *testing.T) {
	h, repo := newPartialPayloadTestHandler(t, map[string]string{
		service.SettingKeySiteName: "Example Gateway",
	})

	rec := doPartialSettingsUpdate(t, h, map[string]any{"site_name": ""})
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "", repo.values[service.SettingKeySiteName],
		"an explicitly sent empty value is a deliberate clear, not an omission")
}

// smtp_from_email is the one request field whose JSON name differs from its
// setting key; the alias keeps it from being treated as always-omitted.
func TestUpdateSettingsSMTPFromAliasIsWritable(t *testing.T) {
	h, repo := newPartialPayloadTestHandler(t, map[string]string{
		service.SettingKeySMTPFrom: "old@example.com",
	})

	rec := doPartialSettingsUpdate(t, h, map[string]any{"smtp_from_email": "new@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "new@example.com", repo.values[service.SettingKeySMTPFrom])
}

func TestUpdateSettingsSMTPFieldsUpdateIndependentlyWithConfiguredHost(t *testing.T) {
	stored := func() map[string]string {
		return map[string]string{
			service.SettingKeySMTPHost:     "smtp.old.example",
			service.SettingKeySMTPPort:     "2525",
			service.SettingKeySMTPUsername: "old-user",
			service.SettingKeySMTPPassword: "stored-secret",
			service.SettingKeySMTPFrom:     "old@example.com",
			service.SettingKeySMTPFromName: "Old Sender",
			service.SettingKeySMTPUseTLS:   "true",
		}
	}
	tests := []struct {
		name, key, want string
		payload         map[string]any
	}{
		{name: "host", key: service.SettingKeySMTPHost, want: "smtp.new.example", payload: map[string]any{"smtp_host": " smtp.new.example "}},
		{name: "port", key: service.SettingKeySMTPPort, want: "465", payload: map[string]any{"smtp_port": 465}},
		{name: "username", key: service.SettingKeySMTPUsername, want: "new-user", payload: map[string]any{"smtp_username": " new-user "}},
		{name: "from alias", key: service.SettingKeySMTPFrom, want: "new@example.com", payload: map[string]any{"smtp_from_email": " new@example.com "}},
		{name: "from name", key: service.SettingKeySMTPFromName, want: "New Sender", payload: map[string]any{"smtp_from_name": " New Sender "}},
		{name: "TLS false", key: service.SettingKeySMTPUseTLS, want: "false", payload: map[string]any{"smtp_use_tls": false}},
		{name: "empty password mask", key: service.SettingKeySMTPPassword, want: "stored-secret", payload: map[string]any{"smtp_password": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := newPartialPayloadTestHandler(t, stored())
			rec := doPartialSettingsUpdate(t, h, tt.payload)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, tt.want, repo.values[tt.key])
			for key, original := range stored() {
				if key != tt.key {
					require.Equal(t, original, repo.values[key], "omitted SMTP field %s changed", key)
				}
			}
		})
	}
}

func TestPartialUpdateAuditUsesReloadedSnapshotOnlyActualField(t *testing.T) {
	enabled := true
	before := &service.SystemSettings{SiteName: "Local Gateway", SMTPHost: "smtp.example.com"}
	updated := &service.SystemSettings{SiteName: "Local Gateway", SMTPHost: "smtp.example.com", RiskControlEnabled: true, OIDCConnectUsePKCE: true}

	changed := diffSettings(before, updated, nil, nil, UpdateSettingsRequest{RiskControlEnabled: &enabled})
	changed = filterSettingChangesToSentFields(changed, map[string]json.RawMessage{"risk_control_enabled": nil})

	require.Equal(t, []string{"risk_control_enabled"}, changed)
}

func TestUpdateSettingsGrokDefaultBaseURLModeIsWritable(t *testing.T) {
	h, repo := newPartialPayloadTestHandler(t, map[string]string{
		service.SettingKeyGrokDefaultBaseURLMode: service.GrokDefaultBaseURLModeCLI,
	})

	rec := doPartialSettingsUpdate(t, h, map[string]any{
		"grok_default_base_url_mode": service.GrokDefaultBaseURLModeEUWest1,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, service.GrokDefaultBaseURLModeEUWest1, repo.values[service.SettingKeyGrokDefaultBaseURLMode])
}

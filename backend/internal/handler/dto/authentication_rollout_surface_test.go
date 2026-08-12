//go:build unit

package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemSettingsOmitsDeferredAuthenticationRolloutFields(t *testing.T) {
	payload, err := json.Marshal(SystemSettings{})
	require.NoError(t, err)

	var values map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &values))
	for _, field := range []string{
		"passkey_enabled",
		"passkey_configured",
		"passkey_rp_id",
		"passkey_rp_origins",
		"session_binding_enabled",
		"step_up_enabled",
		"tencent_captcha_enabled",
		"aliyun_captcha_enabled",
	} {
		_, exposed := values[field]
		require.Falsef(t, exposed, "%s must not be exposed before rollout approval", field)
	}
}

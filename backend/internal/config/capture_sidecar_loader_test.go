package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The narrow child loader must enforce the same one-release migration guard as
// ordinary startup. Otherwise the parent can reject a legacy enabled config
// while a directly invoked child silently accepts it.
func TestLoadCaptureSidecarRejectsEnabledLegacyKeysExactlyLikeLoad(t *testing.T) {
	resetViperWithJWTSecret(t)
	configPath := filepath.Join(t.TempDir(), "capture.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
jwt:
  secret: 0123456789abcdef0123456789abcdef
gateway:
  capture:
    enabled: true
    worker_count: 4
    tailscale:
      auth_key: tskey-auth-test
    clickhouse:
      password: capture-password-test
`), 0o600))
	t.Setenv("CONFIG_FILE", configPath)

	_, sidecarErr := LoadCaptureSidecar()
	_, ordinaryErr := Load()
	require.Error(t, ordinaryErr)
	require.EqualError(t, sidecarErr, ordinaryErr.Error())
}

// A full Config decode or JWT validation would make a capture child depend on
// unrelated server/bootstrap state. This catches that regression using a file
// that contains only its two allowed sections.
func TestLoadCaptureSidecarDecodesOnlyLogAndStaticCapture(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "capture.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
log:
  level: debug
gateway:
  capture:
    enabled: true
    tailscale:
      auth_key: tskey-auth-from-file
    clickhouse:
      password: capture-password-from-file
`), 0o600))
	t.Setenv("CONFIG_FILE", configPath)

	loaded, err := LoadCaptureSidecar()
	require.NoError(t, err)
	require.Equal(t, "debug", loaded.Log.Level)
	require.True(t, loaded.Capture.Enabled)
	require.Equal(t, "tskey-auth-from-file", loaded.Capture.Tailscale.AuthKey)
	require.Equal(t, "capture-password-from-file", loaded.Capture.ClickHouse.Password)
}

// Environment bindings must use the existing dotted-key-to-underscore shape,
// otherwise a deployed sidecar silently loses its separate credentials.
func TestLoadCaptureSidecarUsesCaptureEnvironmentBindings(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "capture.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))
	t.Setenv("CONFIG_FILE", configPath)
	t.Setenv("GATEWAY_CAPTURE_ENABLED", "true")
	t.Setenv("GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY", "tskey-auth-from-env")
	t.Setenv("GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD", "capture-password-from-env")

	loaded, err := LoadCaptureSidecar()
	require.NoError(t, err)
	require.True(t, loaded.Capture.Enabled)
	require.Equal(t, "tskey-auth-from-env", loaded.Capture.Tailscale.AuthKey)
	require.Equal(t, "capture-password-from-env", loaded.Capture.ClickHouse.Password)
}

// The child and ordinary server must agree on every static log/capture default.
// A copied defaults table silently drifts when either startup path is changed.
func TestLoadCaptureSidecarDefaultsMatchOrdinaryLoad(t *testing.T) {
	resetViperWithJWTSecret(t)
	ordinary, err := Load()
	require.NoError(t, err)
	sidecar, err := LoadCaptureSidecar()
	require.NoError(t, err)
	require.Equal(t, ordinary.Log, sidecar.Log)
	require.Equal(t, ordinary.Gateway.Capture, sidecar.Capture)
}

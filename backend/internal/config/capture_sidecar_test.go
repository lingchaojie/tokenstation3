package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCaptureDefaultsAreSafeAndDisabled(t *testing.T) {
	resetViperWithJWTSecret(t)
	var cfg Config
	require.NoError(t, LoadIntoForTest(nil, &cfg))
	require.False(t, cfg.Gateway.Capture.Enabled)
	require.Zero(t, cfg.Gateway.Capture.MaxBodyBytes)
	require.Zero(t, cfg.Gateway.Capture.MaxHeaderBytes)
	require.EqualValues(t, 12<<30, cfg.Gateway.Capture.Spool.MaxBytes)
	require.EqualValues(t, 8<<30, cfg.Gateway.Capture.Spool.MinFreeBytes)
	require.Equal(t, "/app/data/capture/spool", cfg.Gateway.Capture.Spool.Dir)
	require.Equal(t, "/app/data/capture/capture.sock", cfg.Gateway.Capture.Sidecar.Socket)
	require.Equal(t, 32, cfg.Gateway.Capture.Sidecar.MaxActiveAttempts)
	require.Equal(t, "http://clickhouse-win:18000", cfg.Gateway.Capture.ClickHouse.URL)
}

func TestCaptureRejectsEnabledConfigWithoutSecretsOrAddress(t *testing.T) {
	cfg := validCaptureConfig()
	cfg.ClickHouse.Password = ""
	require.ErrorContains(t, cfg.Validate(), "clickhouse.password")
}

func TestDisabledLegacyCaptureConfigBootsWithoutStartingAnything(t *testing.T) {
	cfg, warnings, err := loadYAMLForTest(t, `gateway: {capture: {enabled: false, worker_count: 4, queue_size: 100}}`)
	require.NoError(t, err)
	require.False(t, cfg.Gateway.Capture.Enabled)
	require.Contains(t, warnings, "legacy capture queue settings are ignored while disabled")
	require.Zero(t, cfg.Gateway.Capture.WorkerCount)
	require.Zero(t, cfg.Gateway.Capture.QueueSize)
}

func TestEnabledLegacyCaptureConfigRequiresExplicitMigration(t *testing.T) {
	_, _, err := loadYAMLForTest(t, `gateway: {capture: {enabled: true, worker_count: 4}}`)
	require.ErrorContains(t, err, "legacy capture setting worker_count")
}

func TestCaptureSecretsAreReachableFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY", "tskey-auth-test")
	t.Setenv("GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD", "ingest-secret")
	cfg := loadConfigForTest(t)
	require.Equal(t, "tskey-auth-test", cfg.Gateway.Capture.Tailscale.AuthKey)
	require.Equal(t, "ingest-secret", cfg.Gateway.Capture.ClickHouse.Password)
}

func TestCaptureValidationRejectsProtocolAndBatchCapacityErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CaptureConfig)
		want   string
	}{
		{"wrong frame size", func(c *CaptureConfig) { c.Sidecar.FrameBytes = 1024 }, "frame_bytes"},
		{"relative spool directory", func(c *CaptureConfig) { c.Spool.Dir = "capture/spool" }, "spool.dir"},
		{"url query", func(c *CaptureConfig) { c.ClickHouse.URL += "?password=secret" }, "clickhouse.url"},
		{"batch cannot fit maximum record", func(c *CaptureConfig) { c.ClickHouse.BatchMaxBytes = 1 }, "batch_max_bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCaptureConfig()
			tt.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), tt.want)
		})
	}
}

// The v2 child always writes zstd RowBinary. Allowing a different configured
// compression would advertise a choice the runtime cannot honor.
func TestCaptureValidationAcceptsOnlyZstdCompression(t *testing.T) {
	for _, compression := range []string{"lz4", "none"} {
		t.Run(compression, func(t *testing.T) {
			cfg := validCaptureConfig()
			cfg.ClickHouse.Compression = compression
			require.ErrorContains(t, cfg.Validate(), "compression")
		})
	}
}

func validCaptureConfig() CaptureConfig {
	return CaptureConfig{
		Enabled:        true,
		MaxBodyBytes:   32 << 20,
		MaxHeaderBytes: 1 << 20,
		Spool: CaptureSpoolConfig{
			Dir:          "/app/data/capture/spool",
			MaxBytes:     12 << 30,
			MinFreeBytes: 8 << 30,
		},
		Sidecar: CaptureSidecarConfig{
			Socket:            "/app/data/capture/capture.sock",
			FrameBytes:        65536,
			MemoryLimitBytes:  256 << 20,
			MaxActiveAttempts: 32,
		},
		Tailscale: CaptureTailscaleConfig{
			StateDir: "/app/data/capture/tsnet",
			Hostname: "sub2api-capture-writer",
			AuthKey:  "tskey-auth-test",
		},
		ClickHouse: CaptureClickHouseConfig{
			URL:                "http://clickhouse-win:18000",
			Database:           "llm_archive",
			Table:              "model_call_archive",
			Username:           "capture_ingest",
			Password:           "ingest-secret",
			Compression:        "zstd",
			BatchMaxRows:       100,
			BatchMaxBytes:      128 << 20,
			BatchMaxIntervalMS: 2000,
			DialTimeoutMS:      5000,
			WriteTimeoutMS:     60000,
		},
	}
}

func TestCaptureConfigAllowsUnlimitedPerRecordCapture(t *testing.T) {
	cfg := validCaptureConfig()
	cfg.MaxBodyBytes = 0
	cfg.MaxHeaderBytes = 0
	require.NoError(t, cfg.Validate())
}

func loadConfigForTest(t *testing.T) *Config {
	t.Helper()
	cfg, err := Load()
	require.NoError(t, err)
	return cfg
}

func loadYAMLForTest(t *testing.T, content string) (*Config, string, error) {
	t.Helper()
	resetViperWithJWTSecret(t)
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(content), 0o600))
	t.Setenv("CONFIG_FILE", configFile)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg, err := Load()
	return cfg, logs.String(), err
}

func LoadIntoForTest(_ any, cfg *Config) error {
	setDefaults()
	return viper.Unmarshal(cfg)
}

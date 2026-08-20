package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// CaptureSidecarStaticConfig is the deliberately narrow configuration surface
// used by the same-binary capture child. It excludes setup, server, database,
// Redis, JWT, and provider configuration.
type CaptureSidecarStaticConfig struct {
	Log     LogConfig
	Capture CaptureConfig
}

// LoadCaptureSidecar reads only logging and gateway.capture using the normal
// CONFIG_FILE/DATA_DIR source order and environment spelling. Unlike Load, it
// does not run bootstrap validation or materialize unrelated secrets.
func LoadCaptureSidecar() (*CaptureSidecarStaticConfig, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	configureConfigSource(v.SetConfigFile, v.AddConfigPath)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	setCaptureSidecarDefaults(v)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read capture sidecar config: %w", err)
		}
	}
	legacyCaptureSettings := configuredLegacyCaptureSettings(v.InConfig)
	var decoded struct {
		Log     LogConfig `mapstructure:"log"`
		Gateway struct {
			Capture CaptureConfig `mapstructure:"capture"`
		} `mapstructure:"gateway"`
	}
	if err := v.Unmarshal(&decoded); err != nil {
		return nil, fmt.Errorf("decode capture sidecar settings: %w", err)
	}
	loaded := CaptureSidecarStaticConfig{Log: decoded.Log, Capture: decoded.Gateway.Capture}
	if err := rejectEnabledLegacyCaptureSettings(loaded.Capture.Enabled, legacyCaptureSettings); err != nil {
		return nil, err
	}
	if err := loaded.Capture.Validate(); err != nil {
		return nil, fmt.Errorf("validate capture sidecar settings: %w", err)
	}
	return &loaded, nil
}

func setCaptureSidecarDefaults(v *viper.Viper) {
	setLogAndCaptureDefaults(v.SetDefault)
}

func setLogAndCaptureDefaults(setDefault func(string, any)) {
	setDefault("log.level", "info")
	setDefault("log.format", "console")
	setDefault("log.service_name", "sub2api")
	setDefault("log.env", "production")
	setDefault("log.caller", true)
	setDefault("log.stacktrace_level", "error")
	setDefault("log.output.to_stdout", true)
	setDefault("log.output.to_file", true)
	setDefault("log.output.file_path", "")
	setDefault("log.rotation.max_size_mb", 100)
	setDefault("log.rotation.max_backups", 10)
	setDefault("log.rotation.max_age_days", 7)
	setDefault("log.rotation.compress", true)
	setDefault("log.rotation.local_time", true)
	setDefault("log.sampling.enabled", false)
	setDefault("log.sampling.initial", 100)
	setDefault("log.sampling.thereafter", 100)

	setDefault("gateway.capture.enabled", false)
	setDefault("gateway.capture.max_body_bytes", GatewayCaptureMaxBodyBytes)
	setDefault("gateway.capture.max_header_bytes", 1<<20)
	setDefault("gateway.capture.spool.dir", "/app/data/capture/spool")
	setDefault("gateway.capture.spool.max_bytes", int64(12)<<30)
	setDefault("gateway.capture.spool.min_free_bytes", int64(8)<<30)
	setDefault("gateway.capture.sidecar.socket", "/app/data/capture/capture.sock")
	setDefault("gateway.capture.sidecar.frame_bytes", int64(captureProtocolV2Frame))
	setDefault("gateway.capture.sidecar.memory_limit_bytes", int64(256)<<20)
	setDefault("gateway.capture.sidecar.max_active_attempts", 32)
	setDefault("gateway.capture.tailscale.state_dir", "/app/data/capture/tsnet")
	setDefault("gateway.capture.tailscale.hostname", "sub2api-capture-writer")
	setDefault("gateway.capture.tailscale.auth_key", "")
	setDefault("gateway.capture.clickhouse.url", "http://clickhouse-win:18000")
	setDefault("gateway.capture.clickhouse.database", "llm_archive")
	setDefault("gateway.capture.clickhouse.table", "model_call_archive")
	setDefault("gateway.capture.clickhouse.username", "capture_ingest")
	setDefault("gateway.capture.clickhouse.password", "")
	setDefault("gateway.capture.clickhouse.compression", "zstd")
	setDefault("gateway.capture.clickhouse.batch_max_rows", 100)
	setDefault("gateway.capture.clickhouse.batch_max_bytes", int64(128)<<20)
	setDefault("gateway.capture.clickhouse.batch_max_interval_ms", 2000)
	setDefault("gateway.capture.clickhouse.dial_timeout_ms", 5000)
	setDefault("gateway.capture.clickhouse.write_timeout_ms", 60000)
}

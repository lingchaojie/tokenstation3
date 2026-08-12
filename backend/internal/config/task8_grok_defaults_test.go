package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestTask8GrokFreeQuotaSoftGateDefaultsOff(t *testing.T) {
	viper.Reset()
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))

	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.Grok.FreeQuotaSoftGateEnabled)
}

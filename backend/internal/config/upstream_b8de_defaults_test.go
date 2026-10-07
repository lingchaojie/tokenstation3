package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamB8deDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Billing.InflightReservation.Enabled, "upgrade must not introduce a new billing admission gate without opt-in")
	require.Equal(t, 200, cfg.APIKeyCreate.MaxActivePerUser)
	require.Equal(t, 60, cfg.APIKeyCreate.MaxPerUserPerHour)
}

func TestUpstreamB8deInflightExplicitOptIn(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("BILLING_INFLIGHT_RESERVATION_ENABLED", "true")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Billing.InflightReservation.Enabled)
}

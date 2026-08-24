package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCursorPlatformRegistration(t *testing.T) {
	require.Equal(t, "cursor", PlatformCursor)
	require.Contains(t, AllowedQuotaPlatforms, PlatformCursor)
	require.NotContains(t, AllowedSchedulingThresholdPlatforms, PlatformCursor)
	require.Contains(t, model.AllPlatforms(), PlatformCursor)
}

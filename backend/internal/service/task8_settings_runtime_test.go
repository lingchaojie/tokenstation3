//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestTask8OpenAITTFTSettingRoundTripAndValidation(t *testing.T) {
	repo := newMockSettingRepo()
	repo.data[SettingKeyOpenAITTFTMode] = OpenAITTFTModeVisible
	svc := NewSettingService(repo, &config.Config{})

	gatewayForwardingCache = atomic.Value{}
	require.Equal(t, OpenAITTFTModeVisible, svc.GetOpenAITTFTMode(context.Background()))

	settings, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, OpenAITTFTModeVisible, settings.OpenAITTFTMode)

	updates, err := svc.buildSystemSettingsUpdates(context.Background(), settings)
	require.NoError(t, err)
	require.Equal(t, OpenAITTFTModeVisible, updates[SettingKeyOpenAITTFTMode])

	settings.OpenAITTFTMode = "unknown"
	_, err = svc.buildSystemSettingsUpdates(context.Background(), settings)
	require.ErrorContains(t, err, SettingKeyOpenAITTFTMode)
}

func TestTask8OpenAITTFTSettingDefaultsToSemantic(t *testing.T) {
	gatewayForwardingCache = atomic.Value{}
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})
	require.Equal(t, OpenAITTFTModeSemantic, svc.GetOpenAITTFTMode(context.Background()))
}

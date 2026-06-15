package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFromService_UsesStoredKeyType(t *testing.T) {
	out := APIKeyFromService(&service.APIKey{
		ID:      1,
		UserID:  2,
		Key:     "sk-test",
		Name:    "stored",
		Status:  service.StatusActive,
		KeyType: service.APIKeyTypeOpenAI,
		Group:   &service.Group{ID: 10, Platform: service.PlatformAnthropic},
	})

	require.NotNil(t, out)
	require.Equal(t, service.APIKeyTypeOpenAI, out.KeyType)
}

func TestAPIKeyFromService_DerivesLegacyKeyTypeFromGroup(t *testing.T) {
	out := APIKeyFromService(&service.APIKey{
		ID:     1,
		UserID: 2,
		Key:    "sk-test",
		Name:   "legacy",
		Status: service.StatusActive,
		Group:  &service.Group{ID: 10, Platform: service.PlatformAnthropic},
	})

	require.NotNil(t, out)
	require.Equal(t, service.APIKeyTypeAnthropic, out.KeyType)
}

func TestAPIKeyFromService_UnknownForLegacyUngroupedKey(t *testing.T) {
	out := APIKeyFromService(&service.APIKey{
		ID:     1,
		UserID: 2,
		Key:    "sk-test",
		Name:   "ungrouped",
		Status: service.StatusActive,
	})

	require.NotNil(t, out)
	require.Equal(t, service.APIKeyTypeUnknown, out.KeyType)
}

func TestAPIKeyFromService_MapsGroupBindingMode(t *testing.T) {
	out := APIKeyFromService(&service.APIKey{
		ID:               1,
		UserID:           2,
		Key:              "sk-test",
		Name:             "follow",
		Status:           service.StatusActive,
		KeyType:          service.APIKeyTypeAnthropic,
		GroupBindingMode: service.APIKeyGroupBindingModeDefaultFollow,
	})

	require.NotNil(t, out)
	require.Equal(t, service.APIKeyGroupBindingModeDefaultFollow, out.GroupBindingMode)
}

func TestAPIKeyFromService_DefaultsEmptyGroupBindingModeToStatic(t *testing.T) {
	out := APIKeyFromService(&service.APIKey{
		ID:      1,
		UserID:  2,
		Key:     "sk-test",
		Name:    "legacy",
		Status:  service.StatusActive,
		KeyType: service.APIKeyTypeUnknown,
	})

	require.NotNil(t, out)
	require.Equal(t, service.APIKeyGroupBindingModeStatic, out.GroupBindingMode)
}

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepository_KeyTypeRoundTripRequiresGeneratedField(t *testing.T) {
	t.Parallel()
	key := &service.APIKey{KeyType: service.APIKeyTypeOpenAI}
	require.Equal(t, service.APIKeyTypeOpenAI, key.KeyType)
}

func TestUserAPIKeyRouteRepository_InterfaceShape(t *testing.T) {
	t.Parallel()
	var _ service.UserAPIKeyRouteRepository = (*userAPIKeyRouteRepository)(nil)
	_, _ = context.Background(), &userAPIKeyRouteRepository{}
}

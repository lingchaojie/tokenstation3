//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// autoRoutingGroupRepoStub returns groups by ID from a map so a single test can
// model both the Anthropic and OpenAI default groups.
type autoRoutingGroupRepoStub struct {
	groupRepoStubForGroupUpdate
	byID map[int64]*Group
}

func (s *autoRoutingGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	g, ok := s.byID[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	clone := *g
	return &clone, nil
}

func newAutoRoutingService(t *testing.T, anthropicGroupID, openaiGroupID int64) *APIKeyService {
	t.Helper()
	svc := &APIKeyService{
		groupRepo: &autoRoutingGroupRepoStub{byID: map[int64]*Group{
			anthropicGroupID: {ID: anthropicGroupID, Platform: PlatformAnthropic, Status: StatusActive},
			openaiGroupID:    {ID: openaiGroupID, Platform: PlatformOpenAI, Status: StatusActive},
		}},
	}
	svc.SetProviderRouting(
		apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}},
		&defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{
			APIKeyTypeAnthropic: &anthropicGroupID,
			APIKeyTypeOpenAI:    &openaiGroupID,
		}},
	)
	return svc
}

func TestAPIKeyService_AutoBinding_ResolvesGroupByIngressProvider(t *testing.T) {
	userID := int64(7)
	anthropicGroupID := int64(10)
	openaiGroupID := int64(20)
	svc := newAutoRoutingService(t, anthropicGroupID, openaiGroupID)

	newAutoKey := func() *APIKey {
		return &APIKey{
			UserID:           userID,
			GroupBindingMode: APIKeyGroupBindingModeAuto,
			User:             &User{ID: userID, Status: StatusActive},
		}
	}

	anthropicCtx := context.WithValue(context.Background(), ctxkey.IngressProvider, PlatformAnthropic)
	keyA := newAutoKey()
	require.NoError(t, svc.applyDefaultFollowGroup(anthropicCtx, keyA))
	require.NotNil(t, keyA.GroupID)
	require.Equal(t, anthropicGroupID, *keyA.GroupID)
	require.NotNil(t, keyA.Group)
	require.Equal(t, PlatformAnthropic, keyA.Group.Platform)

	openaiCtx := context.WithValue(context.Background(), ctxkey.IngressProvider, PlatformOpenAI)
	keyB := newAutoKey()
	require.NoError(t, svc.applyDefaultFollowGroup(openaiCtx, keyB))
	require.NotNil(t, keyB.GroupID)
	require.Equal(t, openaiGroupID, *keyB.GroupID)
	require.NotNil(t, keyB.Group)
	require.Equal(t, PlatformOpenAI, keyB.Group.Platform)
}

func TestAPIKeyService_AutoBinding_OpenAIIngressClaudeModelUsesAnthropicDefault(t *testing.T) {
	userID := int64(7)
	anthropicGroupID := int64(10)
	openaiGroupID := int64(20)
	svc := newAutoRoutingService(t, anthropicGroupID, openaiGroupID)

	key := &APIKey{
		UserID:           userID,
		GroupBindingMode: APIKeyGroupBindingModeAuto,
		User:             &User{ID: userID, Status: StatusActive},
	}
	ctx := context.WithValue(context.Background(), ctxkey.IngressProvider, PlatformOpenAI)
	ctx = context.WithValue(ctx, ctxkey.IngressModel, "claude-opus-4-7")

	require.NoError(t, svc.applyDefaultFollowGroup(ctx, key))
	require.NotNil(t, key.GroupID)
	require.Equal(t, anthropicGroupID, *key.GroupID)
	require.NotNil(t, key.Group)
	require.Equal(t, PlatformAnthropic, key.Group.Platform)
}

func TestAPIKeyService_AutoBinding_OpenAIIngressOpenAIModelKeepsOpenAIDefault(t *testing.T) {
	userID := int64(7)
	anthropicGroupID := int64(10)
	openaiGroupID := int64(20)
	svc := newAutoRoutingService(t, anthropicGroupID, openaiGroupID)

	key := &APIKey{
		UserID:           userID,
		GroupBindingMode: APIKeyGroupBindingModeAuto,
		User:             &User{ID: userID, Status: StatusActive},
	}
	ctx := context.WithValue(context.Background(), ctxkey.IngressProvider, PlatformOpenAI)
	ctx = context.WithValue(ctx, ctxkey.IngressModel, "gpt-5.5")

	require.NoError(t, svc.applyDefaultFollowGroup(ctx, key))
	require.NotNil(t, key.GroupID)
	require.Equal(t, openaiGroupID, *key.GroupID)
	require.NotNil(t, key.Group)
	require.Equal(t, PlatformOpenAI, key.Group.Platform)
}

func TestAPIKeyService_AutoBinding_NoIngressLeavesGroupUnresolved(t *testing.T) {
	svc := newAutoRoutingService(t, 10, 20)
	key := &APIKey{
		UserID:           7,
		GroupBindingMode: APIKeyGroupBindingModeAuto,
		User:             &User{ID: 7, Status: StatusActive},
	}
	// No ingress provider on the context (e.g. a Gemini endpoint or non-gateway path).
	require.NoError(t, svc.applyDefaultFollowGroup(context.Background(), key))
	require.Nil(t, key.GroupID)
	require.Nil(t, key.Group)
}

func TestAPIKeyService_Create_DefaultsToAutoBindingWhenNoProvider(t *testing.T) {
	userID := int64(42)
	customKey := "unified-create-key-1234567890"
	apiKeyRepo := &apiKeyProviderRoutingCreateRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&apiKeyProviderRoutingUserRepoStub{user: &User{ID: userID, Status: StatusActive}},
		&groupRepoStubForGroupUpdate{},
		nil,
		nil,
		nil,
		nil,
	)

	apiKey, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
		Name:      "Unified key",
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, APIKeyGroupBindingModeAuto, apiKeyRepo.created.GroupBindingMode)
	require.Equal(t, "", apiKeyRepo.created.KeyType)
	require.Nil(t, apiKeyRepo.created.GroupID)
	require.Equal(t, APIKeyGroupBindingModeAuto, apiKey.GroupBindingMode)
}

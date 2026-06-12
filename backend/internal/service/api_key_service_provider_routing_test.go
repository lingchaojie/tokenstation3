//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type apiKeyProviderRouteRepoStub struct {
	routes map[string]*UserAPIKeyRoute
}

func providerRouteKey(userID int64, keyType string) string {
	return strconv.FormatInt(userID, 10) + ":" + keyType
}

func (r apiKeyProviderRouteRepoStub) GetByUserID(context.Context, int64) ([]UserAPIKeyRoute, error) {
	return nil, nil
}

func (r apiKeyProviderRouteRepoStub) GetByUserIDAndKeyType(_ context.Context, userID int64, keyType string) (*UserAPIKeyRoute, error) {
	return r.routes[providerRouteKey(userID, keyType)], nil
}

func (r apiKeyProviderRouteRepoStub) Upsert(context.Context, UserAPIKeyRoute) (*UserAPIKeyRoute, error) {
	return nil, nil
}

func (r apiKeyProviderRouteRepoStub) DeleteByUserIDAndKeyType(context.Context, int64, string) error {
	return nil
}

type defaultAPIKeyGroupSettingsStub struct {
	ids map[string]*int64
}

func (s defaultAPIKeyGroupSettingsStub) GetDefaultAPIKeyGroupID(_ context.Context, keyType string) (*int64, error) {
	return s.ids[keyType], nil
}

func TestAPIKeyService_ResolveProviderGroup_UsesUserProviderRoute(t *testing.T) {
	userID := int64(42)
	routeGroupID := int64(20)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{ID: routeGroupID, Platform: PlatformOpenAI, Status: StatusActive}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: routeGroupID},
	}}, defaultAPIKeyGroupSettingsStub{})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: userID, Status: StatusActive}, APIKeyTypeOpenAI)

	require.NoError(t, err)
	require.NotNil(t, groupID)
	require.Equal(t, routeGroupID, *groupID)
	require.NotNil(t, group)
	require.Equal(t, PlatformOpenAI, group.Platform)
}

func TestAPIKeyService_ResolveProviderGroup_FallsBackToGlobalProviderRoute(t *testing.T) {
	userID := int64(42)
	globalGroupID := int64(10)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{ID: globalGroupID, Platform: PlatformAnthropic, Status: StatusActive}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &globalGroupID}})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: userID, Status: StatusActive}, APIKeyTypeAnthropic)

	require.NoError(t, err)
	require.NotNil(t, groupID)
	require.Equal(t, globalGroupID, *groupID)
	require.NotNil(t, group)
	require.Equal(t, PlatformAnthropic, group.Platform)
}

func TestAPIKeyService_ResolveProviderGroup_RejectsMissingDefault(t *testing.T) {
	svc := &APIKeyService{}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{}})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: 42, Status: StatusActive}, APIKeyTypeOpenAI)

	require.Nil(t, groupID)
	require.Nil(t, group)
	require.ErrorIs(t, err, ErrDefaultAPIKeyGroupMissing)
}

func TestAPIKeyService_ResolveProviderGroup_RejectsPlatformMismatch(t *testing.T) {
	groupID := int64(10)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeOpenAI: &groupID}})

	resolvedGroupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: 42, Status: StatusActive}, APIKeyTypeOpenAI)

	require.Nil(t, resolvedGroupID)
	require.Nil(t, group)
	require.ErrorIs(t, err, ErrDefaultAPIKeyGroupInvalid)
}

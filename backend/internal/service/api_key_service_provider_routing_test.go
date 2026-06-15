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

func (r apiKeyProviderRouteRepoStub) ReconcileGroupReplacement(context.Context, int64, int64, int64, string) error {
	return nil
}

type defaultAPIKeyGroupSettingsStub struct {
	ids   map[string]*int64
	calls int
}

func (s *defaultAPIKeyGroupSettingsStub) GetDefaultAPIKeyGroupID(_ context.Context, keyType string) (*int64, error) {
	s.calls++
	return s.ids[keyType], nil
}

type apiKeyProviderRoutingUserRepoStub struct {
	userRepoStubForGroupUpdate
	user *User
}

func (s *apiKeyProviderRoutingUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	if s.user == nil || s.user.ID != id {
		return nil, ErrUserNotFound
	}
	clone := *s.user
	return &clone, nil
}

type apiKeyProviderRoutingCreateRepoStub struct {
	authRepoStub
	created *APIKey
	exists  bool
}

func (s *apiKeyProviderRoutingCreateRepoStub) Create(_ context.Context, key *APIKey) error {
	clone := *key
	s.created = &clone
	return nil
}

func (s *apiKeyProviderRoutingCreateRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	return s.exists, nil
}

func TestAPIKeyService_ResolveProviderGroup_UsesUserProviderRoute(t *testing.T) {
	userID := int64(42)
	routeGroupID := int64(20)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{ID: routeGroupID, Platform: PlatformOpenAI, Status: StatusActive}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: routeGroupID},
	}}, &defaultAPIKeyGroupSettingsStub{})

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
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &globalGroupID}})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: userID, Status: StatusActive}, APIKeyTypeAnthropic)

	require.NoError(t, err)
	require.NotNil(t, groupID)
	require.Equal(t, globalGroupID, *groupID)
	require.NotNil(t, group)
	require.Equal(t, PlatformAnthropic, group.Platform)
}

func TestAPIKeyService_ResolveProviderGroup_RejectsMissingDefault(t *testing.T) {
	svc := &APIKeyService{}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{}})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: 42, Status: StatusActive}, APIKeyTypeOpenAI)

	require.Nil(t, groupID)
	require.Nil(t, group)
	require.ErrorIs(t, err, ErrDefaultAPIKeyGroupMissing)
}

func TestAPIKeyService_ResolveProviderGroup_RejectsPlatformMismatch(t *testing.T) {
	groupID := int64(10)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeOpenAI: &groupID}})

	resolvedGroupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: 42, Status: StatusActive}, APIKeyTypeOpenAI)

	require.Nil(t, resolvedGroupID)
	require.Nil(t, group)
	require.ErrorIs(t, err, ErrDefaultAPIKeyGroupInvalid)
}

func TestAPIKeyService_CreatePersistsUserProviderRouteGroupAndKeyType(t *testing.T) {
	userID := int64(42)
	routeGroupID := int64(20)
	customKey := "provider-route-create-key"
	apiKeyRepo := &apiKeyProviderRoutingCreateRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&apiKeyProviderRoutingUserRepoStub{user: &User{ID: userID, Status: StatusActive}},
		&groupRepoStubForGroupUpdate{group: &Group{ID: routeGroupID, Platform: PlatformOpenAI, Status: StatusActive}},
		nil,
		nil,
		nil,
		nil,
	)
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: routeGroupID},
	}}, &defaultAPIKeyGroupSettingsStub{})

	apiKey, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
		Name:      "OpenAI key",
		KeyType:   APIKeyTypeOpenAI,
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, customKey, apiKey.Key)
	require.Equal(t, customKey, apiKeyRepo.created.Key)
	require.Equal(t, APIKeyTypeOpenAI, apiKey.KeyType)
	require.Equal(t, APIKeyTypeOpenAI, apiKeyRepo.created.KeyType)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, routeGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKeyRepo.created.GroupID)
	require.Equal(t, routeGroupID, *apiKeyRepo.created.GroupID)
}

func TestAPIKeyService_CreateFallsBackToDefaultProviderGroup(t *testing.T) {
	userID := int64(42)
	defaultGroupID := int64(30)
	customKey := "default-route-create-key"
	apiKeyRepo := &apiKeyProviderRoutingCreateRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&apiKeyProviderRoutingUserRepoStub{user: &User{ID: userID, Status: StatusActive}},
		&groupRepoStubForGroupUpdate{group: &Group{ID: defaultGroupID, Platform: PlatformAnthropic, Status: StatusActive}},
		nil,
		nil,
		nil,
		nil,
	)
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &defaultGroupID}})

	_, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
		Name:      "Anthropic key",
		KeyType:   APIKeyTypeAnthropic,
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, APIKeyTypeAnthropic, apiKeyRepo.created.KeyType)
	require.Equal(t, APIKeyGroupBindingModeDefaultFollow, apiKeyRepo.created.GroupBindingMode)
	require.NotNil(t, apiKeyRepo.created.GroupID)
	require.Equal(t, defaultGroupID, *apiKeyRepo.created.GroupID)
}

func TestAPIKeyService_CreateRejectsManualGroupWhenKeyTypeUsesProviderRouting(t *testing.T) {
	userID := int64(42)
	manualGroupID := int64(99)
	customKey := "manual-group-blocked-key"
	apiKeyRepo := &apiKeyProviderRoutingCreateRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&apiKeyProviderRoutingUserRepoStub{user: &User{ID: userID, Status: StatusActive}},
		&groupRepoStubForGroupUpdate{group: &Group{ID: manualGroupID, Platform: PlatformOpenAI, Status: StatusActive}},
		nil,
		nil,
		nil,
		nil,
	)

	apiKey, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
		Name:      "Blocked key",
		KeyType:   APIKeyTypeOpenAI,
		GroupID:   &manualGroupID,
		CustomKey: &customKey,
	})

	require.Nil(t, apiKey)
	require.Nil(t, apiKeyRepo.created)
	require.ErrorIs(t, err, ErrAPIKeyGroupSelectionBlocked)
}

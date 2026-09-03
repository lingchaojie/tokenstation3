//go:build unit

package service

import (
	"context"
	"errors"
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

type modelCatalogGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
	errs   map[int64]error
}

func (s *modelCatalogGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if err := s.errs[id]; err != nil {
		return nil, err
	}
	group := s.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	clone := *group
	return &clone, nil
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

func (s *apiKeyRepoStubForGroupUpdate) GetWebChatKeyByUserAndGroup(context.Context, int64, int64) (*APIKey, error) {
	panic("unexpected")
}

func TestAPIKeyService_ResolveModelCatalogGroups_UnifiedUsesAuthorizedProviderRoutes(t *testing.T) {
	userID := int64(42)
	anthropicID := int64(3)
	openAIID := int64(6)
	svc := &APIKeyService{groupRepo: &modelCatalogGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		openAIID:    {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive},
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: openAIID},
	}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &anthropicID}})

	apiKey := &APIKey{
		UserID:           userID,
		User:             &User{ID: userID, Status: StatusActive},
		GroupBindingMode: APIKeyGroupBindingModeAuto,
	}
	groups, err := svc.ResolveModelCatalogGroups(context.Background(), apiKey)

	require.NoError(t, err)
	require.Equal(t, []int64{anthropicID, openAIID}, []int64{groups[0].ID, groups[1].ID})
	require.Nil(t, apiKey.GroupID)
	require.Nil(t, apiKey.Group)
}

func TestAPIKeyService_ResolveModelCatalogGroups_UnifiedSkipsMissingProviderDefault(t *testing.T) {
	userID := int64(42)
	openAIID := int64(6)
	svc := &APIKeyService{groupRepo: &modelCatalogGroupRepoStub{groups: map[int64]*Group{
		openAIID: {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive},
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: openAIID},
	}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{}})

	groups, err := svc.ResolveModelCatalogGroups(context.Background(), &APIKey{
		UserID:           userID,
		User:             &User{ID: userID, Status: StatusActive},
		GroupBindingMode: APIKeyGroupBindingModeAuto,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{openAIID}, []int64{groups[0].ID})
}

func TestAPIKeyService_ResolveModelCatalogGroups_UnifiedSkipsInvalidOrForbiddenProviderGroup(t *testing.T) {
	userID := int64(42)
	invalidAnthropicID := int64(3)
	forbiddenOpenAIID := int64(6)
	svc := &APIKeyService{groupRepo: &modelCatalogGroupRepoStub{groups: map[int64]*Group{
		invalidAnthropicID: {ID: invalidAnthropicID, Platform: PlatformAnthropic, Status: StatusDisabled},
		forbiddenOpenAIID:  {ID: forbiddenOpenAIID, Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true},
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: forbiddenOpenAIID},
	}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &invalidAnthropicID}})

	groups, err := svc.ResolveModelCatalogGroups(context.Background(), &APIKey{
		UserID:           userID,
		User:             &User{ID: userID, Status: StatusActive},
		GroupBindingMode: APIKeyGroupBindingModeAuto,
	})

	require.NoError(t, err)
	require.Empty(t, groups)
}

func TestAPIKeyService_ResolveModelCatalogGroups_StaticReturnsCurrentGroupOnly(t *testing.T) {
	settings := &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{}}
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive}
	svc := &APIKeyService{}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{}, settings)

	groups, err := svc.ResolveModelCatalogGroups(context.Background(), &APIKey{
		GroupBindingMode: APIKeyGroupBindingModeStatic,
		Group:            group,
	})

	require.NoError(t, err)
	require.Equal(t, []*Group{group}, groups)
	require.Zero(t, settings.calls)
}

func TestAPIKeyService_ResolveModelCatalogGroups_PropagatesRepositoryFailure(t *testing.T) {
	repositoryFailure := errors.New("group repository unavailable")
	anthropicID := int64(3)
	svc := &APIKeyService{groupRepo: &modelCatalogGroupRepoStub{errs: map[int64]error{
		anthropicID: repositoryFailure,
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{
		APIKeyTypeAnthropic: &anthropicID,
	}})

	groups, err := svc.ResolveModelCatalogGroups(context.Background(), &APIKey{
		UserID:           42,
		User:             &User{ID: 42, Status: StatusActive},
		GroupBindingMode: APIKeyGroupBindingModeAuto,
	})

	require.Nil(t, groups)
	require.ErrorIs(t, err, repositoryFailure)
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

// Mode-agnostic access: a subscription-mode default group must be resolvable even
// when the user has no group-bound subscription for it (their universal/generic
// subscription applies at billing time). Previously canUserBindGroup required a
// group-bound subscription for subscription-type groups and rejected resolution.
func TestAPIKeyService_ResolveProviderGroup_AllowsSubscriptionModeGroupWithoutGroupBoundSubscription(t *testing.T) {
	userID := int64(42)
	defaultGroupID := int64(3)
	svc := &APIKeyService{groupRepo: &groupRepoStubForGroupUpdate{group: &Group{
		ID:               defaultGroupID,
		Platform:         PlatformAnthropic,
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeSubscription,
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &defaultGroupID}})

	groupID, group, err := svc.resolveProviderGroupForCreate(context.Background(), &User{ID: userID, Status: StatusActive}, APIKeyTypeAnthropic)

	require.NoError(t, err)
	require.NotNil(t, groupID)
	require.Equal(t, defaultGroupID, *groupID)
	require.NotNil(t, group)
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

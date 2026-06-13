//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type userAPIKeyRouteGroupRepoStub struct {
	groups map[int64]*Group
}

func (s *userAPIKeyRouteGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if group, ok := s.groups[id]; ok {
		clone := *group
		return &clone, nil
	}
	return nil, ErrGroupNotFound
}

func (s *userAPIKeyRouteGroupRepoStub) Create(context.Context, *Group) error { panic("unexpected") }
func (s *userAPIKeyRouteGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) Update(context.Context, *Group) error { panic("unexpected") }
func (s *userAPIKeyRouteGroupRepoStub) Delete(context.Context, int64) error  { panic("unexpected") }
func (s *userAPIKeyRouteGroupRepoStub) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected")
}
func (s *userAPIKeyRouteGroupRepoStub) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected")
}

type userAPIKeyRouteRepoStub struct {
	routes                       map[string]UserAPIKeyRoute
	getByUserIDCalls             int
	upsertCalls                  int
	deleteCalls                  int
	reconcileCalls               int
	reconcileUserID              int64
	reconcileOldGroupID          int64
	reconcileNewGroupID          int64
	reconcileNewGroupKeyType     string
	reconcileGroupReplacementErr error
}

func routeKey(userID int64, keyType string) string {
	return strconv.FormatInt(userID, 10) + ":" + keyType
}

func (r *userAPIKeyRouteRepoStub) GetByUserID(_ context.Context, userID int64) ([]UserAPIKeyRoute, error) {
	r.getByUserIDCalls++
	out := []UserAPIKeyRoute{}
	for _, route := range r.routes {
		if route.UserID == userID {
			out = append(out, route)
		}
	}
	return out, nil
}

func (r *userAPIKeyRouteRepoStub) GetByUserIDAndKeyType(_ context.Context, userID int64, keyType string) (*UserAPIKeyRoute, error) {
	if route, ok := r.routes[routeKey(userID, keyType)]; ok {
		copy := route
		return &copy, nil
	}
	return nil, nil
}

func (r *userAPIKeyRouteRepoStub) Upsert(_ context.Context, route UserAPIKeyRoute) (*UserAPIKeyRoute, error) {
	r.upsertCalls++
	if r.routes == nil {
		r.routes = map[string]UserAPIKeyRoute{}
	}
	r.routes[routeKey(route.UserID, route.KeyType)] = route
	copy := route
	return &copy, nil
}

func (r *userAPIKeyRouteRepoStub) DeleteByUserIDAndKeyType(_ context.Context, userID int64, keyType string) error {
	r.deleteCalls++
	delete(r.routes, routeKey(userID, keyType))
	return nil
}

func (r *userAPIKeyRouteRepoStub) ReconcileGroupReplacement(_ context.Context, userID, oldGroupID, newGroupID int64, newGroupKeyType string) error {
	r.reconcileCalls++
	r.reconcileUserID = userID
	r.reconcileOldGroupID = oldGroupID
	r.reconcileNewGroupID = newGroupID
	r.reconcileNewGroupKeyType = newGroupKeyType
	if r.reconcileGroupReplacementErr != nil {
		return r.reconcileGroupReplacementErr
	}
	for key, route := range r.routes {
		if route.UserID != userID || route.GroupID != oldGroupID {
			continue
		}
		if route.KeyType == newGroupKeyType {
			route.GroupID = newGroupID
			r.routes[key] = route
			continue
		}
		delete(r.routes, key)
	}
	return nil
}

func TestAdminService_UpdateUserAPIKeyRoutes_ValidatesPlatform(t *testing.T) {
	anthropicID := int64(10)
	openAIID := int64(20)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		openAIID:    {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{}
	svc := &adminServiceImpl{userRepo: &userRepoStub{user: &User{ID: 42}}, groupRepo: groupRepo, userAPIKeyRouteRepo: routeRepo, apiKeyRepo: apiKeyRepo}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{
		AnthropicGroupID: &anthropicID,
		OpenAIGroupID:    &openAIID,
	})

	require.NoError(t, err)
	require.NotNil(t, got.Anthropic)
	require.Equal(t, anthropicID, got.Anthropic.GroupID)
	require.NotNil(t, got.OpenAI)
	require.Equal(t, openAIID, got.OpenAI.GroupID)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RebindsExistingKeysForSavedRoute(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 2}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             &userRepoStub{user: &User{ID: 42}},
		groupRepo:            groupRepo,
		userAPIKeyRouteRepo:  routeRepo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{AnthropicGroupID: &anthropicID})

	require.NoError(t, err)
	require.NotNil(t, got.Anthropic)
	require.Equal(t, anthropicID, got.Anthropic.GroupID)
	require.Len(t, apiKeyRepo.effectiveBulkCalls, 1)
	require.Equal(t, effectiveKeyTypeBulkCall{UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: anthropicID}, apiKeyRepo.effectiveBulkCalls[0])
	require.Equal(t, []int64{42}, invalidator.userIDs)
}

func TestAdminService_UpdateUserAPIKeyRoutes_SavesOpenAIMessageDispatchGroup(t *testing.T) {
	openAIID := int64(20)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		openAIID: {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive, AllowMessagesDispatch: true},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	svc := &adminServiceImpl{
		userRepo:            &userRepoStub{user: &User{ID: 42}},
		groupRepo:           groupRepo,
		userAPIKeyRouteRepo: routeRepo,
		apiKeyRepo:          apiKeyRepo,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{OpenAIGroupID: &openAIID})

	require.NoError(t, err)
	require.NotNil(t, got.OpenAI)
	require.Equal(t, openAIID, got.OpenAI.GroupID)
	require.Len(t, apiKeyRepo.effectiveBulkCalls, 1)
	require.Equal(t, effectiveKeyTypeBulkCall{UserID: 42, KeyType: APIKeyTypeOpenAI, GroupID: openAIID}, apiKeyRepo.effectiveBulkCalls[0])
}

func TestAdminService_UpdateUserAPIKeyRoutes_ClearRouteRebindsToGlobalDefault(t *testing.T) {
	oldAnthropicID := int64(10)
	defaultAnthropicID := int64(11)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		oldAnthropicID:     {ID: oldAnthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		defaultAnthropicID: {ID: defaultAnthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: oldAnthropicID},
	}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	invalidator := &authCacheInvalidatorStub{}
	settingSvc := NewSettingService(newMemorySettingRepo(map[string]string{
		SettingKeyDefaultAnthropicGroupID: "11",
	}), nil)
	svc := &adminServiceImpl{
		userRepo:             &userRepoStub{user: &User{ID: 42}},
		groupRepo:            groupRepo,
		userAPIKeyRouteRepo:  routeRepo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
		settingService:       settingSvc,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{})

	require.NoError(t, err)
	require.Nil(t, got.Anthropic)
	require.Len(t, apiKeyRepo.effectiveBulkCalls, 1)
	require.Equal(t, effectiveKeyTypeBulkCall{UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: defaultAnthropicID}, apiKeyRepo.effectiveBulkCalls[0])
	require.Equal(t, []int64{42}, invalidator.userIDs)
}

func TestAdminService_UpdateUserAPIKeyRoutes_ClearRouteWithoutGlobalDefaultDoesNotRebind(t *testing.T) {
	oldAnthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		oldAnthropicID: {ID: oldAnthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: oldAnthropicID},
	}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{}
	invalidator := &authCacheInvalidatorStub{}
	settingSvc := NewSettingService(newMemorySettingRepo(map[string]string{}), nil)
	svc := &adminServiceImpl{
		userRepo:             &userRepoStub{user: &User{ID: 42}},
		groupRepo:            groupRepo,
		userAPIKeyRouteRepo:  routeRepo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
		settingService:       settingSvc,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{})

	require.NoError(t, err)
	require.Nil(t, got.Anthropic)
	require.Empty(t, apiKeyRepo.effectiveBulkCalls)
	require.Empty(t, invalidator.userIDs)
}

func TestAdminService_UpdateUserAPIKeyRoutes_SameExistingRouteStillRebinds(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: anthropicID},
	}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             &userRepoStub{user: &User{ID: 42}},
		groupRepo:            groupRepo,
		userAPIKeyRouteRepo:  routeRepo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{AnthropicGroupID: &anthropicID})

	require.NoError(t, err)
	require.NotNil(t, got.Anthropic)
	require.Equal(t, anthropicID, got.Anthropic.GroupID)
	require.Len(t, apiKeyRepo.effectiveBulkCalls, 1)
	require.Equal(t, effectiveKeyTypeBulkCall{UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: anthropicID}, apiKeyRepo.effectiveBulkCalls[0])
	require.Equal(t, []int64{42}, invalidator.userIDs)
}

func TestAdminService_UpdateUserAPIKeyRoutes_FullStateRebindsAllSavedRoutes(t *testing.T) {
	oldAnthropicID := int64(10)
	newAnthropicID := int64(11)
	openAIID := int64(20)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		newAnthropicID: {ID: newAnthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		openAIID:       {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: oldAnthropicID},
		routeKey(42, APIKeyTypeOpenAI):    {UserID: 42, KeyType: APIKeyTypeOpenAI, GroupID: openAIID},
	}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	svc := &adminServiceImpl{
		userRepo:            &userRepoStub{user: &User{ID: 42}},
		groupRepo:           groupRepo,
		userAPIKeyRouteRepo: routeRepo,
		apiKeyRepo:          apiKeyRepo,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{
		AnthropicGroupID: &newAnthropicID,
		OpenAIGroupID:    &openAIID,
	})

	require.NoError(t, err)
	require.NotNil(t, got.Anthropic)
	require.Equal(t, newAnthropicID, got.Anthropic.GroupID)
	require.NotNil(t, got.OpenAI)
	require.Equal(t, openAIID, got.OpenAI.GroupID)
	require.Equal(t, []effectiveKeyTypeBulkCall{
		{UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: newAnthropicID},
		{UserID: 42, KeyType: APIKeyTypeOpenAI, GroupID: openAIID},
	}, apiKeyRepo.effectiveBulkCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsExclusiveGroupWithoutAllowedGroup(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	svc := &adminServiceImpl{
		userRepo:            &userRepoStub{user: &User{ID: 42, AllowedGroups: []int64{}}},
		groupRepo:           groupRepo,
		userAPIKeyRouteRepo: routeRepo,
		apiKeyRepo:          apiKeyRepo,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{AnthropicGroupID: &anthropicID})

	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, 0, routeRepo.upsertCalls)
	require.Empty(t, apiKeyRepo.effectiveBulkCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsSubscriptionGroupWithoutActiveSubscription(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	userSubRepo := &userSubRepoStubForGroupUpdate{}
	svc := &adminServiceImpl{
		userRepo:            &userRepoStub{user: &User{ID: 42}},
		groupRepo:           groupRepo,
		userAPIKeyRouteRepo: routeRepo,
		apiKeyRepo:          apiKeyRepo,
		userSubRepo:         userSubRepo,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{AnthropicGroupID: &anthropicID})

	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, userSubRepo.called)
	require.Equal(t, int64(42), userSubRepo.calledUserID)
	require.Equal(t, anthropicID, userSubRepo.calledGroupID)
	require.Equal(t, 0, routeRepo.upsertCalls)
	require.Empty(t, apiKeyRepo.effectiveBulkCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_AllowsSubscriptionGroupWithActiveSubscription(t *testing.T) {
	openAIID := int64(20)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		openAIID: {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription, AllowMessagesDispatch: true},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{effectiveBulkAffected: 1}
	userSubRepo := &userSubRepoStubForGroupUpdate{getActiveSub: &UserSubscription{UserID: 42, GroupID: openAIID}}
	svc := &adminServiceImpl{
		userRepo:            &userRepoStub{user: &User{ID: 42}},
		groupRepo:           groupRepo,
		userAPIKeyRouteRepo: routeRepo,
		apiKeyRepo:          apiKeyRepo,
		userSubRepo:         userSubRepo,
	}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{OpenAIGroupID: &openAIID})

	require.NoError(t, err)
	require.NotNil(t, got.OpenAI)
	require.Equal(t, openAIID, got.OpenAI.GroupID)
	require.True(t, userSubRepo.called)
	require.Equal(t, int64(42), userSubRepo.calledUserID)
	require.Equal(t, openAIID, userSubRepo.calledGroupID)
	require.Equal(t, []effectiveKeyTypeBulkCall{{UserID: 42, KeyType: APIKeyTypeOpenAI, GroupID: openAIID}}, apiKeyRepo.effectiveBulkCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsPlatformMismatch(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{user: &User{ID: 42}}, groupRepo: groupRepo, userAPIKeyRouteRepo: &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}}

	errRoutes, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{OpenAIGroupID: &anthropicID})

	require.Nil(t, errRoutes)
	require.Error(t, err)
}

func TestAdminService_GetUserAPIKeyRoutes_RejectsInvalidUserID(t *testing.T) {
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: 10},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{user: &User{ID: 42}}, userAPIKeyRouteRepo: routeRepo}

	got, err := svc.GetUserAPIKeyRoutes(context.Background(), 0)

	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, 0, routeRepo.getByUserIDCalls)
}

func TestAdminService_GetUserAPIKeyRoutes_RejectsMissingUser(t *testing.T) {
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: 10},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{getErr: ErrUserNotFound}, userAPIKeyRouteRepo: routeRepo}

	got, err := svc.GetUserAPIKeyRoutes(context.Background(), 42)

	require.Nil(t, got)
	require.ErrorIs(t, err, ErrUserNotFound)
	require.Equal(t, 0, routeRepo.getByUserIDCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsInvalidUserID(t *testing.T) {
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: 10},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{user: &User{ID: 42}}, userAPIKeyRouteRepo: routeRepo}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 0, UserAPIKeyRouteUpdate{})

	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, 0, routeRepo.upsertCalls)
	require.Equal(t, 0, routeRepo.deleteCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsMissingUser(t *testing.T) {
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: 10},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{getErr: ErrUserNotFound}, userAPIKeyRouteRepo: routeRepo}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{})

	require.Nil(t, got)
	require.ErrorIs(t, err, ErrUserNotFound)
	require.Equal(t, 0, routeRepo.upsertCalls)
	require.Equal(t, 0, routeRepo.deleteCalls)
}

func TestAdminService_UpdateUserAPIKeyRoutes_InvalidOpenAIGroupDoesNotMutateAnthropicRoute(t *testing.T) {
	anthropicID := int64(10)
	invalidOpenAIID := int64(20)
	originalAnthropicID := int64(11)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID:     {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		invalidOpenAIID: {ID: invalidOpenAIID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: originalAnthropicID},
	}}
	svc := &adminServiceImpl{userRepo: &userRepoStub{user: &User{ID: 42}}, groupRepo: groupRepo, userAPIKeyRouteRepo: routeRepo}

	got, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{
		AnthropicGroupID: &anthropicID,
		OpenAIGroupID:    &invalidOpenAIID,
	})

	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, originalAnthropicID, routeRepo.routes[routeKey(42, APIKeyTypeAnthropic)].GroupID)
	require.Equal(t, 0, routeRepo.upsertCalls)
	require.Equal(t, 0, routeRepo.deleteCalls)
}

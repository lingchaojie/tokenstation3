//go:build unit

package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// task4SetBoolField lets this test run unchanged against the clean first parent,
// where RestrictPublicGroups did not exist yet. The behavioral assertions below
// (rather than reflection itself) are the contract under test.
func task4SetBoolField(target any, name string, value bool) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	f := v.Elem().FieldByName(name)
	if !f.IsValid() || !f.CanSet() {
		return
	}
	switch f.Kind() {
	case reflect.Bool:
		f.SetBool(value)
	case reflect.Pointer:
		if f.Type().Elem().Kind() == reflect.Bool {
			p := reflect.New(f.Type().Elem())
			p.Elem().SetBool(value)
			f.Set(p)
		}
	}
}

func TestUserCanBindGroupPublicGroupRestriction(t *testing.T) {
	const (
		publicA   int64 = 10
		publicB   int64 = 11
		exclusive int64 = 20
	)

	restrictedWithPublicA := User{AllowedGroups: []int64{publicA}}
	task4SetBoolField(&restrictedWithPublicA, "RestrictPublicGroups", true)
	restrictedEmpty := User{}
	task4SetBoolField(&restrictedEmpty, "RestrictPublicGroups", true)
	restrictedWithBoth := User{AllowedGroups: []int64{publicA, exclusive}}
	task4SetBoolField(&restrictedWithBoth, "RestrictPublicGroups", true)

	tests := []struct {
		name        string
		user        User
		groupID     int64
		isExclusive bool
		want        bool
	}{
		{name: "standard public unrestricted", user: User{}, groupID: publicA, want: true},
		{name: "standard public restricted and listed", user: restrictedWithPublicA, groupID: publicA, want: true},
		{name: "standard public restricted and absent", user: restrictedWithPublicA, groupID: publicB, want: false},
		{name: "exclusive explicitly granted", user: restrictedWithBoth, groupID: exclusive, isExclusive: true, want: true},
		{name: "exclusive not granted", user: restrictedWithPublicA, groupID: exclusive, isExclusive: true, want: false},
		{name: "public removed while restricted", user: restrictedEmpty, groupID: publicA, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := tt.user
			require.Equal(t, tt.want, user.CanBindGroup(tt.groupID, tt.isExclusive))
		})
	}
}

func TestAdminServicePublicGroupRestrictionPointerSemantics(t *testing.T) {
	const publicGroupID int64 = 31

	t.Run("omitted leaves restriction unchanged", func(t *testing.T) {
		user := &User{ID: 42, Email: "restricted@example.test"}
		task4SetBoolField(user, "RestrictPublicGroups", true)
		repo := &rpmUserRepoStub{userRepoStub: &userRepoStub{user: user}}
		svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

		updated, err := svc.UpdateUser(context.Background(), user.ID, &UpdateUserInput{})

		require.NoError(t, err)
		require.False(t, updated.CanBindGroup(publicGroupID, false))
	})

	t.Run("explicit false clears effective restriction", func(t *testing.T) {
		user := &User{ID: 42, Email: "restricted@example.test"}
		task4SetBoolField(user, "RestrictPublicGroups", true)
		repo := &rpmUserRepoStub{userRepoStub: &userRepoStub{user: user}}
		svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}
		input := &UpdateUserInput{}
		task4SetBoolField(input, "RestrictPublicGroups", false)

		updated, err := svc.UpdateUser(context.Background(), user.ID, input)

		require.NoError(t, err)
		require.True(t, updated.CanBindGroup(publicGroupID, false))
	})
}

func TestAPIKeyServiceDefaultGroupPublicGroupRestriction(t *testing.T) {
	const userID int64 = 42
	defaultGroupID := int64(30)
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{
		ID:               defaultGroupID,
		Platform:         PlatformAnthropic,
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeStandard,
		RateMultiplier:   1,
	}}
	newService := func() *APIKeyService {
		svc := &APIKeyService{groupRepo: groupRepo}
		svc.SetProviderRouting(
			apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{}},
			&defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &defaultGroupID}},
		)
		return svc
	}

	t.Run("unrestricted user keeps existing default route", func(t *testing.T) {
		groupID, group, err := newService().resolveProviderGroupForCreate(
			context.Background(),
			&User{ID: userID, Status: StatusActive},
			APIKeyTypeAnthropic,
		)

		require.NoError(t, err)
		require.Equal(t, defaultGroupID, *groupID)
		require.Equal(t, defaultGroupID, group.ID)
	})

	t.Run("restricted user needs default in allow list", func(t *testing.T) {
		user := &User{ID: userID, Status: StatusActive}
		task4SetBoolField(user, "RestrictPublicGroups", true)

		groupID, group, err := newService().resolveProviderGroupForCreate(
			context.Background(), user, APIKeyTypeAnthropic,
		)

		require.ErrorIs(t, err, ErrGroupNotAllowed)
		require.Nil(t, groupID)
		require.Nil(t, group)
	})
}

func TestAPIKeyServiceRejectsInactiveGroupBeforeUserAuthorization(t *testing.T) {
	const userID int64 = 42
	groupID := int64(73)
	customKey := "inactive-public-group-key"
	apiKeyRepo := &apiKeyProviderRoutingCreateRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&apiKeyProviderRoutingUserRepoStub{user: &User{ID: userID, Status: StatusActive}},
		&groupRepoStubForGroupUpdate{group: &Group{
			ID:               groupID,
			Platform:         PlatformAnthropic,
			Status:           StatusDisabled,
			SubscriptionType: SubscriptionTypeStandard,
		}},
		nil, nil, nil, nil,
	)

	created, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
		Name:      "inactive",
		GroupID:   &groupID,
		CustomKey: &customKey,
	})

	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Nil(t, created)
	require.Nil(t, apiKeyRepo.created)
}

func TestAPIKeyServicePublicGroupRestrictionAuthorizationCache(t *testing.T) {
	const (
		key           = "warmed-policy-key"
		userID  int64 = 42
		groupID int64 = 91
	)

	user := &User{ID: userID, Email: "cache@example.test", Status: StatusActive, Balance: 10}
	authRepo := &authRepoStub{}
	authRepo.getByKeyForAuth = func(context.Context, string) (*APIKey, error) {
		clone := *user
		return &APIKey{
			ID:      1,
			UserID:  userID,
			GroupID: &[]int64{groupID}[0],
			Status:  StatusActive,
			User:    &clone,
			Group: &Group{
				ID:               groupID,
				Platform:         PlatformAnthropic,
				Status:           StatusActive,
				SubscriptionType: SubscriptionTypeStandard,
				RateMultiplier:   1,
			},
		}, nil
	}
	authRepo.listKeysByUserID = func(context.Context, int64) ([]string, error) {
		return []string{key}, nil
	}
	cache := &authCacheStub{}
	authService := NewAPIKeyService(authRepo, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 100, L1TTLSeconds: 3600},
	})

	warmed, err := authService.GetByKey(context.Background(), key)
	require.NoError(t, err)
	require.True(t, warmed.User.CanBindGroup(groupID, false))
	authService.authCacheL1.Wait()

	adminRepo := &rpmUserRepoStub{userRepoStub: &userRepoStub{user: user}}
	adminService := &adminServiceImpl{
		userRepo:             adminRepo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: authService,
	}
	input := &UpdateUserInput{}
	task4SetBoolField(input, "RestrictPublicGroups", true)
	_, err = adminService.UpdateUser(context.Background(), userID, input)
	require.NoError(t, err)
	user = adminRepo.lastUpdated

	reloaded, err := authService.GetByKey(context.Background(), key)
	require.NoError(t, err)
	require.False(t, reloaded.User.CanBindGroup(groupID, false), "policy change must bypass the warmed cache immediately")
}

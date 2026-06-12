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
	routes map[string]UserAPIKeyRoute
}

func routeKey(userID int64, keyType string) string {
	return strconv.FormatInt(userID, 10) + ":" + keyType
}

func (r *userAPIKeyRouteRepoStub) GetByUserID(_ context.Context, userID int64) ([]UserAPIKeyRoute, error) {
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
	if r.routes == nil {
		r.routes = map[string]UserAPIKeyRoute{}
	}
	r.routes[routeKey(route.UserID, route.KeyType)] = route
	copy := route
	return &copy, nil
}

func (r *userAPIKeyRouteRepoStub) DeleteByUserIDAndKeyType(_ context.Context, userID int64, keyType string) error {
	delete(r.routes, routeKey(userID, keyType))
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
	svc := &adminServiceImpl{groupRepo: groupRepo, userAPIKeyRouteRepo: routeRepo}

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

func TestAdminService_UpdateUserAPIKeyRoutes_RejectsPlatformMismatch(t *testing.T) {
	anthropicID := int64(10)
	groupRepo := &userAPIKeyRouteGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	svc := &adminServiceImpl{groupRepo: groupRepo, userAPIKeyRouteRepo: &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{}}}

	errRoutes, err := svc.UpdateUserAPIKeyRoutes(context.Background(), 42, UserAPIKeyRouteUpdate{OpenAIGroupID: &anthropicID})

	require.Nil(t, errRoutes)
	require.Error(t, err)
}

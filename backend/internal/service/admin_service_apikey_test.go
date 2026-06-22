//go:build unit

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

// userRepoStubForGroupUpdate implements UserRepository for AdminUpdateAPIKeyGroupID tests.
type userRepoStubForGroupUpdate struct {
	addGroupErr       error
	addGroupCalled    bool
	addedUserID       int64
	addedGroupID      int64
	removeGroupErr    error
	removeGroupCalled bool
	removedUserID     int64
	removedGroupID    int64
}

func (s *userRepoStubForGroupUpdate) AddGroupToAllowedGroups(_ context.Context, userID int64, groupID int64) error {
	s.addGroupCalled = true
	s.addedUserID = userID
	s.addedGroupID = groupID
	return s.addGroupErr
}

func (s *userRepoStubForGroupUpdate) Create(context.Context, *User) error { panic("unexpected") }
func (s *userRepoStubForGroupUpdate) GetByID(context.Context, int64) (*User, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) GetByEmail(context.Context, string) (*User, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) GetFirstAdmin(context.Context) (*User, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) Update(context.Context, *User) error { panic("unexpected") }
func (s *userRepoStubForGroupUpdate) Delete(context.Context, int64) error { panic("unexpected") }
func (s *userRepoStubForGroupUpdate) GetUserAvatar(context.Context, int64) (*UserAvatar, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) UpsertUserAvatar(context.Context, int64, UpsertUserAvatarInput) (*UserAvatar, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) DeductBalance(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected")
}

func (s *userRepoStubForGroupUpdate) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}
func (s *userRepoStubForGroupUpdate) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}
func (s *userRepoStubForGroupUpdate) ExistsByEmail(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) EnableTotp(context.Context, int64) error  { panic("unexpected") }
func (s *userRepoStubForGroupUpdate) DisableTotp(context.Context, int64) error { panic("unexpected") }
func (s *userRepoStubForGroupUpdate) GetByIDIncludeDeleted(ctx context.Context, id int64) (*User, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}
func (s *userRepoStubForGroupUpdate) ListUserAuthIdentities(context.Context, int64) ([]UserAuthIdentityRecord, error) {
	panic("unexpected")
}

func (s *userRepoStubForGroupUpdate) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected")
}

func (s *userRepoStubForGroupUpdate) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *userRepoStubForGroupUpdate) RemoveGroupFromUserAllowedGroups(_ context.Context, userID int64, groupID int64) error {
	s.removeGroupCalled = true
	s.removedUserID = userID
	s.removedGroupID = groupID
	return s.removeGroupErr
}

// apiKeyRepoStubForGroupUpdate implements APIKeyRepository for AdminUpdateAPIKeyGroupID tests.
type effectiveKeyTypeBulkCall struct {
	UserID  int64
	KeyType string
	GroupID int64
}

type apiKeyRepoStubForGroupUpdate struct {
	key                            *APIKey
	getErr                         error
	updateErr                      error
	updated                        *APIKey // captures what was passed to Update
	bulkMigrated                   int64
	bulkErr                        error
	bulkCalled                     bool
	bulkUserID                     int64
	bulkOldGroupID                 int64
	bulkNewGroupID                 int64
	bulkKeyTypeUpdate              *APIKeyGroupKeyTypeUpdate
	legacyBulkCalled               bool
	listKeysByUserIDResult         []string
	listKeysByUserIDUnexpectedCall bool
	effectiveBulkCalls             []effectiveKeyTypeBulkCall
	effectiveBulkAffected          int64
	effectiveBulkErr               error
}

func (s *apiKeyRepoStubForGroupUpdate) GetByID(_ context.Context, _ int64) (*APIKey, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	clone := *s.key
	return &clone, nil
}
func (s *apiKeyRepoStubForGroupUpdate) Update(_ context.Context, key *APIKey) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	clone := *key
	s.updated = &clone
	return nil
}

// Unused methods – panic on unexpected call.
func (s *apiKeyRepoStubForGroupUpdate) Create(context.Context, *APIKey) error { panic("unexpected") }
func (s *apiKeyRepoStubForGroupUpdate) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) GetByKey(context.Context, string) (*APIKey, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) GetByKeyForAuth(context.Context, string) (*APIKey, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) Delete(context.Context, int64) error { panic("unexpected") }
func (s *apiKeyRepoStubForGroupUpdate) DeleteWithAudit(context.Context, int64) error {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ListByUserID(context.Context, int64, pagination.PaginationParams, APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ListByUserIDIncludingHidden(context.Context, int64, pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) VerifyOwnership(context.Context, int64, []int64) ([]int64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) CountByUserID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ExistsByKey(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) SearchAPIKeys(context.Context, int64, string, int) ([]APIKey, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) CountByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ListKeysByUserID(context.Context, int64) ([]string, error) {
	if s.listKeysByUserIDUnexpectedCall {
		panic("unexpected")
	}
	return append([]string(nil), s.listKeysByUserIDResult...), nil
}
func (s *apiKeyRepoStubForGroupUpdate) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) IncrementQuotaUsed(context.Context, int64, float64) (float64, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) UpdateLastUsed(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) IncrementRateLimitUsage(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) ResetRateLimitWindows(context.Context, int64) error {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	panic("unexpected")
}
func (s *apiKeyRepoStubForGroupUpdate) UpdateGroupIDByUserAndGroup(_ context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	s.legacyBulkCalled = true
	s.bulkUserID = userID
	s.bulkOldGroupID = oldGroupID
	s.bulkNewGroupID = newGroupID
	return s.bulkMigrated, s.bulkErr
}

func (s *apiKeyRepoStubForGroupUpdate) UpdateGroupIDAndKeyTypeByUserAndGroup(_ context.Context, userID, oldGroupID, newGroupID int64, keyTypeUpdate APIKeyGroupKeyTypeUpdate) (int64, error) {
	s.bulkCalled = true
	s.bulkUserID = userID
	s.bulkOldGroupID = oldGroupID
	s.bulkNewGroupID = newGroupID
	copyUpdate := keyTypeUpdate
	s.bulkKeyTypeUpdate = &copyUpdate
	return s.bulkMigrated, s.bulkErr
}

func (s *apiKeyRepoStubForGroupUpdate) UpdateGroupIDAndKeyTypeByUserAndEffectiveKeyType(_ context.Context, userID int64, keyType string, groupID int64) (int64, error) {
	s.effectiveBulkCalls = append(s.effectiveBulkCalls, effectiveKeyTypeBulkCall{UserID: userID, KeyType: keyType, GroupID: groupID})
	return s.effectiveBulkAffected, s.effectiveBulkErr
}

// groupRepoStubForGroupUpdate implements GroupRepository for AdminUpdateAPIKeyGroupID tests.
type groupRepoStubForGroupUpdate struct {
	group          *Group
	getErr         error
	lastGetByIDArg int64
}

func (s *groupRepoStubForGroupUpdate) GetByID(_ context.Context, id int64) (*Group, error) {
	s.lastGetByIDArg = id
	if s.getErr != nil {
		return nil, s.getErr
	}
	clone := *s.group
	return &clone, nil
}

// Unused methods – panic on unexpected call.
func (s *groupRepoStubForGroupUpdate) Create(context.Context, *Group) error { panic("unexpected") }
func (s *groupRepoStubForGroupUpdate) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) Update(context.Context, *Group) error { panic("unexpected") }
func (s *groupRepoStubForGroupUpdate) Delete(context.Context, int64) error  { panic("unexpected") }
func (s *groupRepoStubForGroupUpdate) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) ListActive(context.Context) ([]Group, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected")
}
func (s *groupRepoStubForGroupUpdate) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected")
}

type userSubRepoStubForGroupUpdate struct {
	userSubRepoNoop
	getActiveSub  *UserSubscription
	getActiveErr  error
	called        bool
	calledUserID  int64
	calledGroupID int64
}

func (s *userSubRepoStubForGroupUpdate) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	s.called = true
	s.calledUserID = userID
	s.calledGroupID = groupID
	if s.getActiveErr != nil {
		return nil, s.getActiveErr
	}
	if s.getActiveSub == nil {
		return nil, ErrSubscriptionNotFound
	}
	clone := *s.getActiveSub
	return &clone, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func newAdminAPIKeyServiceTestEntClient(t *testing.T) *dbent.Client {
	t.Helper()

	dbName := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()),
	)
	db, err := sql.Open("sqlite", dbName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestAdminService_AdminUpdateAPIKeyGroupID_KeyNotFound(t *testing.T) {
	repo := &apiKeyRepoStubForGroupUpdate{getErr: ErrAPIKeyNotFound}
	svc := &adminServiceImpl{apiKeyRepo: repo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 999, int64Ptr(1))
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_NilGroupID_NoOp(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(5)}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing}
	svc := &adminServiceImpl{apiKeyRepo: repo}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.APIKey.ID)
	// Update should NOT have been called (updated stays nil)
	require.Nil(t, repo.updated)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_Unbind(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(5), Group: &Group{ID: 5, Name: "Old"}, KeyType: APIKeyTypeOpenAI, GroupBindingMode: APIKeyGroupBindingModeDefaultFollow}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: repo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.NoError(t, err)
	require.Nil(t, got.APIKey.GroupID, "group_id should be nil after unbind")
	require.Nil(t, got.APIKey.Group, "group object should be nil after unbind")
	require.Equal(t, APIKeyTypeOpenAI, got.APIKey.KeyType, "unbind should not change key_type")
	require.Equal(t, APIKeyGroupBindingModeStatic, got.APIKey.GroupBindingMode)
	require.NotNil(t, repo.updated, "Update should have been called")
	require.Nil(t, repo.updated.GroupID)
	require.Equal(t, APIKeyTypeOpenAI, repo.updated.KeyType, "unbind should leave persisted key_type unchanged")
	require.Equal(t, APIKeyGroupBindingModeStatic, repo.updated.GroupBindingMode)
	require.False(t, repo.updated.ClearKeyType, "unbind should not request key_type clearing")
	require.Equal(t, []string{"sk-test"}, cache.keys, "cache should be invalidated")
}

func TestAdminService_AdminUpdateAPIKeyGroupID_BindActiveGroup(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: nil, GroupBindingMode: APIKeyGroupBindingModeDefaultFollow}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Pro", Status: StatusActive}}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	require.Equal(t, APIKeyGroupBindingModeStatic, got.APIKey.GroupBindingMode)
	require.Equal(t, int64(10), *apiKeyRepo.updated.GroupID)
	require.Equal(t, APIKeyGroupBindingModeStatic, apiKeyRepo.updated.GroupBindingMode)
	require.Equal(t, []string{"sk-test"}, cache.keys)
	// M3: verify correct group ID was passed to repo
	require.Equal(t, int64(10), groupRepo.lastGetByIDArg)
	// C1 fix: verify Group object is populated
	require.NotNil(t, got.APIKey.Group)
	require.Equal(t, "Pro", got.APIKey.Group.Name)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_UpdatesKeyTypeFromGroupPlatform(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 100, Key: "sk-test", GroupID: nil, KeyType: APIKeyTypeAnthropic}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 20, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive}}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(20))

	require.NoError(t, err)
	require.NotNil(t, got.APIKey)
	require.Equal(t, APIKeyTypeOpenAI, got.APIKey.KeyType)
	require.Equal(t, APIKeyTypeOpenAI, apiKeyRepo.updated.KeyType)
	require.False(t, apiKeyRepo.updated.ClearKeyType, "mapped platforms should persist their key_type rather than clearing it")
}

func TestAdminService_AdminUpdateAPIKeyGroupID_ClearsKeyTypeForNonMappedGroupPlatform(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 100, Key: "sk-test", GroupID: nil, KeyType: APIKeyTypeOpenAI}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 30, Name: "gemini", Platform: PlatformGemini, Status: StatusActive}}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(30))

	require.NoError(t, err)
	require.NotNil(t, got.APIKey)
	require.Empty(t, got.APIKey.KeyType)
	require.NotNil(t, apiKeyRepo.updated)
	require.Empty(t, apiKeyRepo.updated.KeyType)
	require.True(t, apiKeyRepo.updated.ClearKeyType, "non-Anthropic/OpenAI group binding should explicitly clear persisted key_type")
}

func TestAdminService_AdminUpdateAPIKeyGroupID_SameGroup_Idempotent(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(10), Group: &Group{ID: 10, Name: "Pro"}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Pro", Status: StatusActive}}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	// Update is still called (current impl doesn't short-circuit on same group)
	require.NotNil(t, apiKeyRepo.updated)
	require.Equal(t, []string{"sk-test"}, cache.keys)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotFound(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{getErr: ErrGroupNotFound}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(99))
	require.ErrorIs(t, err, ErrGroupNotFound)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotActive(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 5, Status: StatusDisabled}}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(5))
	require.Error(t, err)
	require.Equal(t, "GROUP_NOT_ACTIVE", infraerrors.Reason(err))
}

func TestAdminService_AdminUpdateAPIKeyGroupID_UpdateFails(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(3)}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing, updateErr: errors.New("db write error")}
	svc := &adminServiceImpl{apiKeyRepo: repo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.Error(t, err)
	require.Contains(t, err.Error(), "update api key")
}

func TestAdminService_AdminUpdateAPIKeyGroupID_NegativeGroupID(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(-5))
	require.Error(t, err)
	require.Equal(t, "INVALID_GROUP_ID", infraerrors.Reason(err))
}

func TestAdminService_AdminUpdateAPIKeyGroupID_PointerIsolation(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Pro", Status: StatusActive}}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, authCacheInvalidator: cache}

	inputGID := int64(10)
	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, &inputGID)
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	// Mutating the input pointer must NOT affect the stored value
	inputGID = 999
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	require.Equal(t, int64(10), *apiKeyRepo.updated.GroupID)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_NilCacheInvalidator(t *testing.T) {
	existing := &APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 7, Status: StatusActive}}
	// authCacheInvalidator is nil – should not panic
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(7))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(7), *got.APIKey.GroupID)
}

func TestAdminService_ReplaceUserGroup_PassesMappedTargetKeyTypeToBulkMigration(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{bulkMigrated: 2}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 20, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, authCacheInvalidator: cache, entClient: newAdminAPIKeyServiceTestEntClient(t)}

	result, err := svc.ReplaceUserGroup(context.Background(), 42, 10, 20)

	require.NoError(t, err)
	require.Equal(t, int64(2), result.MigratedKeys)
	require.True(t, userRepo.addGroupCalled)
	require.Equal(t, int64(42), userRepo.addedUserID)
	require.Equal(t, int64(20), userRepo.addedGroupID)
	require.True(t, apiKeyRepo.bulkCalled)
	require.Equal(t, int64(42), apiKeyRepo.bulkUserID)
	require.Equal(t, int64(10), apiKeyRepo.bulkOldGroupID)
	require.Equal(t, int64(20), apiKeyRepo.bulkNewGroupID)
	require.NotNil(t, apiKeyRepo.bulkKeyTypeUpdate)
	require.Equal(t, APIKeyTypeOpenAI, apiKeyRepo.bulkKeyTypeUpdate.KeyType)
	require.False(t, apiKeyRepo.bulkKeyTypeUpdate.ClearKeyType)
	require.True(t, userRepo.removeGroupCalled)
	require.Equal(t, int64(42), userRepo.removedUserID)
	require.Equal(t, int64(10), userRepo.removedGroupID)
}

func TestAdminService_ReplaceUserGroup_PassesClearKeyTypeForUnmappedTargetPlatform(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{bulkMigrated: 1}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 30, Name: "gemini", Platform: PlatformGemini, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, entClient: newAdminAPIKeyServiceTestEntClient(t)}

	result, err := svc.ReplaceUserGroup(context.Background(), 42, 10, 30)

	require.NoError(t, err)
	require.Equal(t, int64(1), result.MigratedKeys)
	require.True(t, apiKeyRepo.bulkCalled)
	require.NotNil(t, apiKeyRepo.bulkKeyTypeUpdate)
	require.Empty(t, apiKeyRepo.bulkKeyTypeUpdate.KeyType)
	require.True(t, apiKeyRepo.bulkKeyTypeUpdate.ClearKeyType)
}

func TestAdminService_ReplaceUserGroup_MovesMatchingUserAPIKeyRoute(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{bulkMigrated: 1}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 20, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeOpenAI): {UserID: 42, KeyType: APIKeyTypeOpenAI, GroupID: 10},
	}}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, userAPIKeyRouteRepo: routeRepo, entClient: newAdminAPIKeyServiceTestEntClient(t)}

	result, err := svc.ReplaceUserGroup(context.Background(), 42, 10, 20)

	require.NoError(t, err)
	require.Equal(t, int64(1), result.MigratedKeys)
	require.Equal(t, 1, routeRepo.reconcileCalls)
	require.Equal(t, int64(42), routeRepo.reconcileUserID)
	require.Equal(t, int64(10), routeRepo.reconcileOldGroupID)
	require.Equal(t, int64(20), routeRepo.reconcileNewGroupID)
	require.Equal(t, APIKeyTypeOpenAI, routeRepo.reconcileNewGroupKeyType)
	route, ok := routeRepo.routes[routeKey(42, APIKeyTypeOpenAI)]
	require.True(t, ok)
	require.Equal(t, int64(20), route.GroupID)
}

func TestAdminService_ReplaceUserGroup_RemovesMismatchedUserAPIKeyRoute(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{bulkMigrated: 1}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 20, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	routeRepo := &userAPIKeyRouteRepoStub{routes: map[string]UserAPIKeyRoute{
		routeKey(42, APIKeyTypeAnthropic): {UserID: 42, KeyType: APIKeyTypeAnthropic, GroupID: 10},
	}}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, userAPIKeyRouteRepo: routeRepo, entClient: newAdminAPIKeyServiceTestEntClient(t)}

	result, err := svc.ReplaceUserGroup(context.Background(), 42, 10, 20)

	require.NoError(t, err)
	require.Equal(t, int64(1), result.MigratedKeys)
	require.Equal(t, 1, routeRepo.reconcileCalls)
	require.Equal(t, APIKeyTypeOpenAI, routeRepo.reconcileNewGroupKeyType)
	require.NotContains(t, routeRepo.routes, routeKey(42, APIKeyTypeAnthropic))
}

// ---------------------------------------------------------------------------
// Tests: AllowedGroup auto-sync
// ---------------------------------------------------------------------------

func TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AddsAllowedGroup(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Exclusive", Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	// 验证 AddGroupToAllowedGroups 被调用，且参数正确
	require.True(t, userRepo.addGroupCalled)
	require.Equal(t, int64(42), userRepo.addedUserID)
	require.Equal(t, int64(10), userRepo.addedGroupID)
	// 验证 result 标记了自动授权
	require.True(t, got.AutoGrantedGroupAccess)
	require.NotNil(t, got.GrantedGroupID)
	require.Equal(t, int64(10), *got.GrantedGroupID)
	require.Equal(t, "Exclusive", got.GrantedGroupName)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_NonExclusiveGroup_NoAllowedGroupUpdate(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Public", Status: StatusActive, IsExclusive: false, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	// 非专属分组不触发 AddGroupToAllowedGroups
	require.False(t, userRepo.addGroupCalled)
	require.False(t, got.AutoGrantedGroupAccess)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_SubscriptionGroup_Blocked(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Sub", Status: StatusActive, IsExclusive: false, SubscriptionType: SubscriptionTypeSubscription}}
	userRepo := &userRepoStubForGroupUpdate{}
	userSubRepo := &userSubRepoStubForGroupUpdate{getActiveErr: ErrSubscriptionNotFound}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, userSubRepo: userSubRepo}

	// 无有效订阅时应拒绝绑定
	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.Error(t, err)
	require.Equal(t, "SUBSCRIPTION_REQUIRED", infraerrors.Reason(err))
	require.True(t, userSubRepo.called)
	require.Equal(t, int64(42), userSubRepo.calledUserID)
	require.Equal(t, int64(10), userSubRepo.calledGroupID)
	require.False(t, userRepo.addGroupCalled)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_SubscriptionGroup_RequiresRepo(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Sub", Status: StatusActive, IsExclusive: false, SubscriptionType: SubscriptionTypeSubscription}}
	userRepo := &userRepoStubForGroupUpdate{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo}

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.Error(t, err)
	require.Equal(t, "SUBSCRIPTION_REPOSITORY_UNAVAILABLE", infraerrors.Reason(err))
	require.False(t, userRepo.addGroupCalled)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_SubscriptionGroup_AllowsActiveSubscription(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Sub", Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeSubscription}}
	userRepo := &userRepoStubForGroupUpdate{}
	userSubRepo := &userSubRepoStubForGroupUpdate{
		getActiveSub: &UserSubscription{ID: 99, UserID: 42, GroupID: 10},
	}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo, userSubRepo: userSubRepo}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.True(t, userSubRepo.called)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	require.False(t, userRepo.addGroupCalled)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AllowedGroupAddFails_ReturnsError(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &Group{ID: 10, Name: "Exclusive", Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard}}
	userRepo := &userRepoStubForGroupUpdate{addGroupErr: errors.New("db error")}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, groupRepo: groupRepo, userRepo: userRepo}

	// 严格模式：AddGroupToAllowedGroups 失败时，整体操作报错
	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.Error(t, err)
	require.Contains(t, err.Error(), "add group to user allowed groups")
	require.True(t, userRepo.addGroupCalled)
	// apiKey 不应被更新
	require.Nil(t, apiKeyRepo.updated)
}

func TestAdminService_AdminUpdateAPIKeyGroupID_Unbind_NoAllowedGroupUpdate(t *testing.T) {
	existing := &APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: int64Ptr(10), Group: &Group{ID: 10, Name: "Exclusive"}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: apiKeyRepo, userRepo: userRepo, authCacheInvalidator: cache}

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.NoError(t, err)
	require.Nil(t, got.APIKey.GroupID)
	// 解绑时不修改 allowed_groups
	require.False(t, userRepo.addGroupCalled)
	require.False(t, got.AutoGrantedGroupAccess)
}

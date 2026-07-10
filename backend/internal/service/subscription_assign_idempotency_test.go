package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/dgraph-io/ristretto"
	"github.com/stretchr/testify/require"
)

func TestWithSubscriptionUpdateTx_ReusesExistingTransaction(t *testing.T) {
	existingTx := &dbent.Tx{}
	ctx := dbent.NewTxContext(context.Background(), existingTx)
	svc := &SubscriptionService{entClient: &dbent.Client{}}

	called := false
	err := svc.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		called = true
		require.Same(t, existingTx, dbent.TxFromContext(txCtx))
		return nil
	})

	require.NoError(t, err)
	require.True(t, called)
}

func TestMaybeInvalidateAssignmentCaches_DefersForOuterTransactionOwner(t *testing.T) {
	cache, err := ristretto.NewCache(&ristretto.Config{NumCounters: 1_000, MaxCost: 100, BufferItems: 64})
	require.NoError(t, err)
	t.Cleanup(cache.Close)

	svc := &SubscriptionService{subCacheL1: cache}
	key := subCacheKey(7, 9)
	require.True(t, cache.Set(key, &UserSubscription{ID: 42}, 1))
	cache.Wait()

	svc.maybeInvalidateAssignmentCaches(7, 9, true)
	_, cachedBeforeCommit := cache.Get(key)
	require.True(t, cachedBeforeCommit, "outer transaction must retain caches until its owner commits")

	svc.maybeInvalidateAssignmentCaches(7, 9, false)
	cache.Wait()
	_, cachedAfterCommit := cache.Get(key)
	require.False(t, cachedAfterCommit, "post-commit invalidation must remove the cached subscription")
}

type groupRepoNoop struct{}

func (groupRepoNoop) Create(context.Context, *Group) error { panic("unexpected Create call") }
func (groupRepoNoop) GetByID(context.Context, int64) (*Group, error) {
	panic("unexpected GetByID call")
}
func (groupRepoNoop) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected GetByIDLite call")
}
func (groupRepoNoop) Update(context.Context, *Group) error { panic("unexpected Update call") }
func (groupRepoNoop) Delete(context.Context, int64) error  { panic("unexpected Delete call") }
func (groupRepoNoop) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}
func (groupRepoNoop) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (groupRepoNoop) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}
func (groupRepoNoop) ListActive(context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}
func (groupRepoNoop) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (groupRepoNoop) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected ExistsByName call")
}
func (groupRepoNoop) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}
func (groupRepoNoop) HasSchedulableMixedKiroStickyAccount(context.Context, int64) (bool, error) {
	return false, nil
}
func (groupRepoNoop) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}
func (groupRepoNoop) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}
func (groupRepoNoop) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected BindAccountsToGroup call")
}
func (groupRepoNoop) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

type subscriptionGroupRepoStub struct {
	groupRepoNoop
	group *Group
}

func (s *subscriptionGroupRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return s.group, nil
}

type userSubRepoNoop struct{}

func (userSubRepoNoop) Create(context.Context, *UserSubscription) error {
	panic("unexpected Create call")
}
func (userSubRepoNoop) GetByID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetByID call")
}
func (userSubRepoNoop) GetByIDIncludeDeleted(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}
func (userSubRepoNoop) GetByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetByUserIDAndGroupID call")
}
func (userSubRepoNoop) GetGenericByUserID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetGenericByUserID call")
}
func (userSubRepoNoop) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetActiveByUserIDAndGroupID call")
}
func (userSubRepoNoop) GetActiveGenericByUserID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetActiveGenericByUserID call")
}
func (userSubRepoNoop) GetActivePlanBackedByUserID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetActivePlanBackedByUserID call")
}
func (userSubRepoNoop) Update(context.Context, *UserSubscription) error {
	panic("unexpected Update call")
}
func (userSubRepoNoop) Delete(context.Context, int64) error { panic("unexpected Delete call") }
func (userSubRepoNoop) Restore(context.Context, int64, string) (*UserSubscription, error) {
	panic("unexpected Restore call")
}
func (userSubRepoNoop) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListByUserID call")
}
func (userSubRepoNoop) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListActiveByUserID call")
}
func (userSubRepoNoop) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}
func (userSubRepoNoop) List(context.Context, pagination.PaginationParams, *int64, *int64, string, string, string, string) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (userSubRepoNoop) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsByUserIDAndGroupID call")
}

func (userSubRepoNoop) ExistsGenericByUserID(context.Context, int64) (bool, error) {
	panic("unexpected ExistsGenericByUserID call")
}

func (userSubRepoNoop) ExistsActiveByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsActiveByUserIDAndGroupID call")
}
func (userSubRepoNoop) ExtendExpiry(context.Context, int64, time.Time) error {
	panic("unexpected ExtendExpiry call")
}
func (userSubRepoNoop) UpdateStatus(context.Context, int64, string) error {
	panic("unexpected UpdateStatus call")
}
func (userSubRepoNoop) UpdateNotes(context.Context, int64, string) error {
	panic("unexpected UpdateNotes call")
}
func (userSubRepoNoop) UpdatePlanSnapshot(context.Context, int64, *int64, *string, *float64, time.Time, time.Time, *string) error {
	panic("unexpected UpdatePlanSnapshot call")
}
func (userSubRepoNoop) SchedulePlanChange(context.Context, int64, *int64, *string, *float64, time.Time, time.Time, *int64, *string) error {
	panic("unexpected SchedulePlanChange call")
}
func (userSubRepoNoop) ClearScheduledPlanChange(context.Context, int64) error {
	panic("unexpected ClearScheduledPlanChange call")
}
func (userSubRepoNoop) ApplyScheduledPlanChange(context.Context, int64, time.Time) (*UserSubscription, bool, error) {
	panic("unexpected ApplyScheduledPlanChange call")
}
func (userSubRepoNoop) UpdatePlanID(context.Context, int64, int64) error {
	panic("unexpected UpdatePlanID call")
}
func (userSubRepoNoop) ActivateWindows(context.Context, int64, time.Time) error {
	panic("unexpected ActivateWindows call")
}
func (userSubRepoNoop) ResetUsageWindows(context.Context, int64, bool, bool, bool, time.Time) error {
	panic("unexpected ResetUsageWindows call")
}
func (userSubRepoNoop) ResetDailyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetDailyUsage call")
}
func (userSubRepoNoop) ResetWeeklyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetWeeklyUsage call")
}
func (userSubRepoNoop) ResetMonthlyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetMonthlyUsage call")
}
func (userSubRepoNoop) IncrementUsage(context.Context, int64, float64) error {
	panic("unexpected IncrementUsage call")
}
func (userSubRepoNoop) BatchUpdateExpiredStatus(context.Context) (int64, error) {
	panic("unexpected BatchUpdateExpiredStatus call")
}

type subscriptionUserSubRepoStub struct {
	userSubRepoNoop

	nextID            int64
	byID              map[int64]*UserSubscription
	byUserGroup       map[string]*UserSubscription
	createCalls       int
	createErr         error
	existsAlwaysFalse bool

	incrementUsageAfterGetByUserGroup bool
	concurrentDailyUsageDelta         float64
	concurrentWeeklyUsageDelta        float64
	concurrentMonthlyUsageDelta       float64
}

func newSubscriptionUserSubRepoStub() *subscriptionUserSubRepoStub {
	return &subscriptionUserSubRepoStub{
		nextID:      1,
		byID:        make(map[int64]*UserSubscription),
		byUserGroup: make(map[string]*UserSubscription),
	}
}

func (s *subscriptionUserSubRepoStub) key(userID, groupID int64) string {
	return strconvFormatInt(userID) + ":" + strconvFormatInt(groupID)
}

func (s *subscriptionUserSubRepoStub) seed(sub *UserSubscription) {
	if sub == nil {
		return
	}
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
}

func (s *subscriptionUserSubRepoStub) ExistsByUserIDAndGroupID(_ context.Context, userID, groupID int64) (bool, error) {
	if s.existsAlwaysFalse {
		return false, nil
	}
	_, ok := s.byUserGroup[s.key(userID, groupID)]
	return ok, nil
}

func (s *subscriptionUserSubRepoStub) ExistsGenericByUserID(ctx context.Context, userID int64) (bool, error) {
	return s.ExistsByUserIDAndGroupID(ctx, userID, 0)
}

func (s *subscriptionUserSubRepoStub) GetByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	sub := s.byUserGroup[s.key(userID, groupID)]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	if s.incrementUsageAfterGetByUserGroup {
		s.incrementUsageAfterGetByUserGroup = false
		sub.DailyUsageUSD += s.concurrentDailyUsageDelta
		sub.WeeklyUsageUSD += s.concurrentWeeklyUsageDelta
		sub.MonthlyUsageUSD += s.concurrentMonthlyUsageDelta
	}
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) GetGenericByUserID(ctx context.Context, userID int64) (*UserSubscription, error) {
	return s.GetByUserIDAndGroupID(ctx, userID, 0)
}

func (s *subscriptionUserSubRepoStub) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	sub, err := s.GetByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(time.Now()) {
		return nil, ErrSubscriptionNotFound
	}
	return sub, nil
}

func (s *subscriptionUserSubRepoStub) GetActiveGenericByUserID(ctx context.Context, userID int64) (*UserSubscription, error) {
	return s.GetActiveByUserIDAndGroupID(ctx, userID, 0)
}

func (s *subscriptionUserSubRepoStub) GetActivePlanBackedByUserID(_ context.Context, userID int64) (*UserSubscription, error) {
	now := time.Now()
	for _, sub := range s.byID {
		if sub != nil && sub.UserID == userID && sub.PlanID != nil && sub.Status == SubscriptionStatusActive && sub.ExpiresAt.After(now) {
			cp := *sub
			return &cp, nil
		}
	}
	return nil, ErrSubscriptionNotFound
}

func (s *subscriptionUserSubRepoStub) Create(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return nil
	}
	s.createCalls++
	if s.createErr != nil {
		return s.createErr
	}
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	sub.ID = cp.ID
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	sub := s.byID[id]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) Update(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return ErrSubscriptionNilInput
	}
	existing := s.byID[sub.ID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	oldKey := s.key(existing.UserID, existing.GroupID)
	cp := *sub
	s.byID[cp.ID] = &cp
	if oldKey != s.key(cp.UserID, cp.GroupID) {
		delete(s.byUserGroup, oldKey)
	}
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdatePlanSnapshot(_ context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, windowStart time.Time, expiresAt time.Time, notes *string) error {
	existing := s.byID[id]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	cp := *existing
	cp.PlanID = planID
	cp.PlanName = planName
	cp.SevenDayLimitUSD = sevenDayLimitUSD
	cp.ExpiresAt = expiresAt
	cp.Status = SubscriptionStatusActive
	cp.ScheduledPlanID = nil
	cp.ScheduledPlanName = nil
	cp.ScheduledSevenDayLimitUSD = nil
	cp.ScheduledPlanEffectiveAt = nil
	cp.ScheduledExpiresAt = nil
	cp.ScheduledOrderID = nil
	cp.WeeklyWindowStart = &windowStart
	cp.WeeklyUsageUSD = 0
	if notes != nil {
		cp.Notes = *notes
	}
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) SchedulePlanChange(_ context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, effectiveAt time.Time, expiresAt time.Time, orderID *int64, notes *string) error {
	existing := s.byID[id]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	cp := *existing
	cp.ScheduledPlanID = planID
	cp.ScheduledPlanName = planName
	cp.ScheduledSevenDayLimitUSD = sevenDayLimitUSD
	cp.ScheduledPlanEffectiveAt = &effectiveAt
	cp.ScheduledExpiresAt = &expiresAt
	cp.ScheduledOrderID = orderID
	if notes != nil {
		cp.Notes = *notes
	}
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) ClearScheduledPlanChange(_ context.Context, id int64) error {
	existing := s.byID[id]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	cp := *existing
	cp.ScheduledPlanID = nil
	cp.ScheduledPlanName = nil
	cp.ScheduledSevenDayLimitUSD = nil
	cp.ScheduledPlanEffectiveAt = nil
	cp.ScheduledExpiresAt = nil
	cp.ScheduledOrderID = nil
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) ApplyScheduledPlanChange(_ context.Context, id int64, now time.Time) (*UserSubscription, bool, error) {
	existing := s.byID[id]
	if existing == nil {
		return nil, false, ErrSubscriptionNotFound
	}
	if existing.ScheduledPlanEffectiveAt == nil || existing.ScheduledPlanEffectiveAt.After(now) {
		return nil, false, nil
	}
	cp := *existing
	cp.PlanID = existing.ScheduledPlanID
	cp.PlanName = existing.ScheduledPlanName
	cp.SevenDayLimitUSD = existing.ScheduledSevenDayLimitUSD
	cp.StartsAt = *existing.ScheduledPlanEffectiveAt
	if existing.ScheduledExpiresAt != nil {
		cp.ExpiresAt = *existing.ScheduledExpiresAt
	}
	cp.Status = SubscriptionStatusActive
	cp.DailyWindowStart = existing.ScheduledPlanEffectiveAt
	cp.WeeklyWindowStart = existing.ScheduledPlanEffectiveAt
	cp.MonthlyWindowStart = existing.ScheduledPlanEffectiveAt
	cp.DailyUsageUSD = 0
	cp.WeeklyUsageUSD = 0
	cp.MonthlyUsageUSD = 0
	cp.ScheduledPlanID = nil
	cp.ScheduledPlanName = nil
	cp.ScheduledSevenDayLimitUSD = nil
	cp.ScheduledPlanEffectiveAt = nil
	cp.ScheduledExpiresAt = nil
	cp.ScheduledOrderID = nil
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	out := cp
	return &out, true, nil
}

func (s *subscriptionUserSubRepoStub) IncrementUsage(_ context.Context, id int64, costUSD float64) error {
	existing := s.byID[id]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	existing.DailyUsageUSD += costUSD
	existing.WeeklyUsageUSD += costUSD
	existing.MonthlyUsageUSD += costUSD
	return nil
}
func (s *subscriptionUserSubRepoStub) ExtendExpiry(_ context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	existing := s.byID[subscriptionID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	existing.ExpiresAt = newExpiresAt
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateStatus(_ context.Context, subscriptionID int64, status string) error {
	existing := s.byID[subscriptionID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	existing.Status = status
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateNotes(_ context.Context, subscriptionID int64, notes string) error {
	existing := s.byID[subscriptionID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	existing.Notes = notes
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdatePlanID(_ context.Context, subscriptionID int64, planID int64) error {
	existing := s.byID[subscriptionID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	existing.PlanID = &planID
	return nil
}

func (s *subscriptionUserSubRepoStub) Delete(_ context.Context, id int64) error {
	existing := s.byID[id]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	delete(s.byID, id)
	delete(s.byUserGroup, s.key(existing.UserID, existing.GroupID))
	return nil
}

func TestResolveActiveSubscriptionForRoutedGroupIgnoresUnrelatedLegacyPlanBackedSubscription(t *testing.T) {
	now := time.Now()
	userSubRepo := newSubscriptionUserSubRepoStub()
	planID := int64(7)
	userSubRepo.seed(&UserSubscription{
		UserID:    42,
		GroupID:   999,
		PlanID:    &planID,
		Status:    SubscriptionStatusActive,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now.Add(time.Minute),
	})
	userSubRepo.seed(&UserSubscription{
		UserID:    42,
		GroupID:   20,
		Status:    SubscriptionStatusActive,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	})
	svc := NewSubscriptionService(nil, userSubRepo, nil, nil, nil)

	got, err := svc.ResolveActiveSubscriptionForRoutedGroup(context.Background(), 42, 20)

	require.NoError(t, err)
	require.Equal(t, int64(20), got.GroupID)
}

func TestAssignOrExtendSubscription_NonExpiredRenewalWithPlanIDPreservesConcurrentUsage(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now()
	subRepo.seed(&UserSubscription{
		ID:              30,
		UserID:          3001,
		GroupID:         1,
		StartsAt:        now.AddDate(0, 0, -1),
		ExpiresAt:       now.AddDate(0, 0, 29),
		Status:          SubscriptionStatusActive,
		DailyUsageUSD:   12.50,
		WeeklyUsageUSD:  34.25,
		MonthlyUsageUSD: 56.75,
		Notes:           "old",
	})
	subRepo.incrementUsageAfterGetByUserGroup = true
	subRepo.concurrentDailyUsageDelta = 0.25
	subRepo.concurrentWeeklyUsageDelta = 0.50
	subRepo.concurrentMonthlyUsageDelta = 0.75
	planID := int64(99)
	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)

	renewed, reused, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       3001,
		GroupID:      1,
		PlanID:       &planID,
		ValidityDays: 30,
		Notes:        "renew",
	})

	require.NoError(t, err)
	require.True(t, reused)
	require.NotNil(t, renewed.PlanID)
	require.Equal(t, planID, *renewed.PlanID)
	require.Equal(t, 12.75, renewed.DailyUsageUSD)
	require.Equal(t, 34.75, renewed.WeeklyUsageUSD)
	require.Equal(t, 57.50, renewed.MonthlyUsageUSD)
}

func ptrSubscriptionString(value string) *string {
	return &value
}

func ptrSubscriptionFloat64(value float64) *float64 {
	return &value
}

func TestAssignOrExtendGenericPlanSubscriptionInvalidatesPublicPlansCache(t *testing.T) {
	planID := int64(77)
	subRepo := newSubscriptionUserSubRepoStub()
	svc := NewSubscriptionService(nil, subRepo, nil, nil, nil)
	invalidateCalls := 0
	svc.SetPublicPlansCacheInvalidator(func() { invalidateCalls++ })

	_, reused, err := svc.AssignOrExtendGenericPlanSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:           7001,
		PlanID:           &planID,
		PlanName:         ptrSubscriptionString("Pro monthly"),
		SevenDayLimitUSD: ptrSubscriptionFloat64(260),
		ValidityDays:     30,
	})

	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, 1, invalidateCalls)
}

func TestRevokeSubscriptionWithPlanIDInvalidatesPublicPlansCache(t *testing.T) {
	planID := int64(77)
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        12,
		UserID:    7002,
		GroupID:   0,
		PlanID:    &planID,
		Status:    SubscriptionStatusActive,
		StartsAt:  time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	svc := NewSubscriptionService(nil, subRepo, nil, nil, nil)
	invalidateCalls := 0
	svc.SetPublicPlansCacheInvalidator(func() { invalidateCalls++ })

	require.NoError(t, svc.RevokeSubscription(context.Background(), 12))
	require.Equal(t, 1, invalidateCalls)
}

func TestAssignSubscriptionReuseWhenSemanticsMatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        10,
		UserID:    1001,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "init",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	sub, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       1001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "init",
	})
	require.NoError(t, err)
	require.Equal(t, int64(10), sub.ID)
	require.Equal(t, 0, subRepo.createCalls, "reuse should not create new subscription")
}

func TestAssignSubscriptionConflictWhenSemanticsMismatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        11,
		UserID:    2001,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "old-note",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	_, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       2001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "new-note",
	})
	require.Error(t, err)
	require.Equal(t, "SUBSCRIPTION_ASSIGN_CONFLICT", infraerrorsReason(err))
	require.Equal(t, 0, subRepo.createCalls, "conflict should not create or mutate existing subscription")
}

func TestBulkAssignSubscriptionCreatedReusedAndConflict(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	// user 1: 语义一致，可 reused
	subRepo.seed(&UserSubscription{
		ID:        21,
		UserID:    1,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "same-note",
	})
	// user 3: 语义冲突（有效期不一致），应 failed
	subRepo.seed(&UserSubscription{
		ID:        23,
		UserID:    3,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 60),
		Notes:     "same-note",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	result, err := svc.BulkAssignSubscription(context.Background(), &BulkAssignSubscriptionInput{
		UserIDs:      []int64{1, 2, 3},
		GroupID:      1,
		ValidityDays: 30,
		AssignedBy:   9,
		Notes:        "same-note",
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Equal(t, 1, result.CreatedCount)
	require.Equal(t, 1, result.ReusedCount)
	require.Equal(t, 1, result.FailedCount)
	require.Equal(t, "reused", result.Statuses[1])
	require.Equal(t, "created", result.Statuses[2])
	require.Equal(t, "failed", result.Statuses[3])
	require.Equal(t, 1, subRepo.createCalls)
}

func TestAssignSubscriptionKeepsWorkingWhenIdempotencyStoreUnavailable(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	SetDefaultIdempotencyCoordinator(NewIdempotencyCoordinator(failingIdempotencyRepo{}, DefaultIdempotencyConfig()))
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	sub, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       9001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "new",
	})
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.Equal(t, 1, subRepo.createCalls, "semantic idempotent endpoint should not depend on idempotency store availability")
}

func TestAssignSubscriptionReusesExistingAfterCreateConflictWhenSemanticsMatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        42,
		UserID:    4201,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "race-safe",
	})
	subRepo.createErr = ErrSubscriptionAlreadyExists
	subRepo.existsAlwaysFalse = true

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	sub, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       4201,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "race-safe",
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), sub.ID)
	require.Equal(t, 1, subRepo.createCalls, "create conflict path should re-read and reuse matching subscription")
}

func TestAssignSubscriptionReturnsConflictAfterCreateConflictWhenSemanticsMismatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        43,
		UserID:    4301,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 60),
		Notes:     "race-safe",
	})
	subRepo.createErr = ErrSubscriptionAlreadyExists
	subRepo.existsAlwaysFalse = true

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	_, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       4301,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "race-safe",
	})

	require.Error(t, err)
	require.Equal(t, "SUBSCRIPTION_ASSIGN_CONFLICT", infraerrorsReason(err))
	require.Equal(t, 1, subRepo.createCalls, "create conflict path should preserve semantic conflict behavior")
}

func TestNormalizeAssignValidityDays(t *testing.T) {
	require.Equal(t, 30, normalizeAssignValidityDays(0))
	require.Equal(t, 30, normalizeAssignValidityDays(-5))
	require.Equal(t, MaxValidityDays, normalizeAssignValidityDays(MaxValidityDays+100))
	require.Equal(t, 7, normalizeAssignValidityDays(7))
}

func TestDetectAssignSemanticConflictCases(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	basePlanID := int64(10)
	basePlanName := "Plus"
	baseSevenDayLimit := 110.0
	base := &UserSubscription{
		UserID:           1,
		GroupID:          1,
		PlanID:           &basePlanID,
		PlanName:         &basePlanName,
		SevenDayLimitUSD: &baseSevenDayLimit,
		StartsAt:         start,
		ExpiresAt:        start.AddDate(0, 0, 30),
		Notes:            "same",
	}

	samePlanID := int64(10)
	samePlanName := "Plus"
	sameSevenDayLimit := 110.0 + 1e-10
	reason, conflict := detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:           1,
		GroupID:          1,
		ValidityDays:     30,
		Notes:            "same",
		PlanID:           &samePlanID,
		PlanName:         &samePlanName,
		SevenDayLimitUSD: &sameSevenDayLimit,
	})
	require.False(t, conflict)
	require.Equal(t, "", reason)

	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 60,
		Notes:        "same",
	})
	require.True(t, conflict)
	require.Equal(t, "validity_days_mismatch", reason)

	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "other",
	})
	require.True(t, conflict)
	require.Equal(t, "notes_mismatch", reason)
}

func TestDetectAssignSemanticConflictDetectsPlanSnapshotMismatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	planID := int64(10)
	planName := "Plus"
	sevenDayLimit := 110.0
	base := &UserSubscription{
		UserID:           1,
		GroupID:          1,
		PlanID:           &planID,
		PlanName:         &planName,
		SevenDayLimitUSD: &sevenDayLimit,
		StartsAt:         start,
		ExpiresAt:        start.AddDate(0, 0, 30),
		Notes:            "same",
	}

	otherPlanID := int64(11)
	reason, conflict := detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "same",
		PlanID:       &otherPlanID,
		PlanName:     &planName,
	})
	require.True(t, conflict)
	require.Equal(t, "plan_id_mismatch", reason)

	otherPlanName := "Pro"
	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "same",
		PlanID:       &planID,
		PlanName:     &otherPlanName,
	})
	require.True(t, conflict)
	require.Equal(t, "plan_name_mismatch", reason)

	otherSevenDayLimit := 260.0
	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:           1,
		GroupID:          1,
		ValidityDays:     30,
		Notes:            "same",
		PlanID:           &planID,
		PlanName:         &planName,
		SevenDayLimitUSD: &otherSevenDayLimit,
	})
	require.True(t, conflict)
	require.Equal(t, "seven_day_limit_mismatch", reason)

	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "same",
		PlanID:       &planID,
		PlanName:     &planName,
	})
	require.True(t, conflict)
	require.Equal(t, "seven_day_limit_mismatch", reason)
}

func TestAssignSubscriptionGroupTypeValidation(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeStandard},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)

	_, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrGroupNotSubscriptionType), infraerrors.Code(err))
}

func strconvFormatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func infraerrorsReason(err error) string {
	return infraerrors.Reason(err)
}

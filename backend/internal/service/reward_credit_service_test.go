package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rewardCreditRepositoryFake struct {
	summary        RewardBalanceSummary
	expired        float64
	expireErr      error
	listItems      []RewardCredit
	listTotal      int64
	lastListFilter RewardCreditListFilter
	batchResponses [][]RewardCreditExpiryResult
	batchCalls     int
}

func (f *rewardCreditRepositoryFake) Grant(context.Context, RewardCreditGrant) (RewardCreditGrantResult, error) {
	panic("unexpected Grant call")
}
func (f *rewardCreditRepositoryFake) GetSummary(context.Context, int64, time.Time) (RewardBalanceSummary, error) {
	return f.summary, nil
}
func (f *rewardCreditRepositoryFake) ListCredits(_ context.Context, filter RewardCreditListFilter) ([]RewardCredit, int64, error) {
	f.lastListFilter = filter
	return f.listItems, f.listTotal, nil
}
func (f *rewardCreditRepositoryFake) ExpireUser(context.Context, int64, time.Time) (float64, error) {
	return f.expired, f.expireErr
}
func (f *rewardCreditRepositoryFake) ExpireBatch(context.Context, time.Time, int) ([]RewardCreditExpiryResult, error) {
	f.batchCalls++
	if len(f.batchResponses) == 0 {
		return nil, nil
	}
	result := f.batchResponses[0]
	f.batchResponses = f.batchResponses[1:]
	return result, nil
}

type rewardCreditAuthCacheFake struct {
	APIKeyAuthCacheInvalidator
	userIDs []int64
}

func (f *rewardCreditAuthCacheFake) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	f.userIDs = append(f.userIDs, userID)
}

type rewardCreditBillingCacheFake struct {
	BillingCache
	userIDs []int64
}

func (f *rewardCreditBillingCacheFake) InvalidateUserBalance(_ context.Context, userID int64) error {
	f.userIDs = append(f.userIDs, userID)
	return nil
}

func TestRewardCreditService_ExpireUserAndGetSummaryInvalidatesCaches(t *testing.T) {
	repo := &rewardCreditRepositoryFake{
		expired: 5,
		summary: RewardBalanceSummary{
			DailyCheckIn: DailyRewardBalanceSummary{Amount: 3},
			Affiliate:    AffiliateRewardBalanceSummary{Amount: 7, CreditCount: 2},
		},
	}
	authCache := &rewardCreditAuthCacheFake{}
	billingCache := &rewardCreditBillingCacheFake{}
	svc := NewRewardCreditService(repo, authCache, billingCache)
	svc.now = func() time.Time { return time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC) }

	summary, expired, err := svc.ExpireUserAndGetSummary(context.Background(), 42)

	require.NoError(t, err)
	require.InDelta(t, 5, expired, 1e-9)
	require.InDelta(t, 3, summary.DailyCheckIn.Amount, 1e-9)
	require.InDelta(t, 7, summary.Affiliate.Amount, 1e-9)
	require.Equal(t, []int64{42}, authCache.userIDs)
	require.Equal(t, []int64{42}, billingCache.userIDs)
}

func TestRewardCreditService_ListCreditsScopesAffiliateToCurrentUser(t *testing.T) {
	repo := &rewardCreditRepositoryFake{listTotal: 2}
	svc := NewRewardCreditService(repo, nil, nil)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, total, err := svc.ListCredits(context.Background(), RewardCreditQuery{
		UserID:   42,
		Type:     "affiliate",
		Status:   RewardCreditStatusActive,
		Page:     2,
		PageSize: 20,
	})

	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, int64(42), repo.lastListFilter.UserID)
	require.Equal(t, []RewardCreditType{RewardCreditAffiliateInviter, RewardCreditAffiliateInvitee}, repo.lastListFilter.CreditTypes)
	require.Equal(t, now, repo.lastListFilter.Now)
}

func TestRewardCreditService_ListCreditsRejectsInvalidQuery(t *testing.T) {
	svc := NewRewardCreditService(&rewardCreditRepositoryFake{}, nil, nil)
	for _, query := range []RewardCreditQuery{
		{UserID: 42, Type: "daily", Status: RewardCreditStatusActive, Page: 1, PageSize: 20},
		{UserID: 42, Type: "affiliate", Status: "unknown", Page: 1, PageSize: 20},
		{UserID: 42, Type: "affiliate", Status: RewardCreditStatusActive, Page: 0, PageSize: 20},
		{UserID: 42, Type: "affiliate", Status: RewardCreditStatusActive, Page: 1, PageSize: 101},
	} {
		_, _, err := svc.ListCredits(context.Background(), query)
		require.Error(t, err)
	}
}

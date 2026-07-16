package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRewardCreditExpiryService_RunOnceExpiresAllBatchesAndInvalidatesCaches(t *testing.T) {
	repo := &rewardCreditRepositoryFake{batchResponses: [][]RewardCreditExpiryResult{
		{{UserID: 11, ExpiredAmount: 3}, {UserID: 12, ExpiredAmount: 4}},
		{{UserID: 11, ExpiredAmount: 2}},
		{},
	}}
	authCache := &rewardCreditAuthCacheFake{}
	billingCache := &rewardCreditBillingCacheFake{}
	svc := NewRewardCreditExpiryService(repo, authCache, billingCache)

	err := svc.RunOnce(context.Background(), time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	require.Equal(t, 3, repo.batchCalls)
	require.Equal(t, []int64{11, 12, 11}, authCache.userIDs)
	require.Equal(t, []int64{11, 12, 11}, billingCache.userIDs)
}

func TestRewardCreditExpiryService_StartStopIsIdempotent(t *testing.T) {
	svc := NewRewardCreditExpiryService(&rewardCreditRepositoryFake{}, nil, nil)
	svc.interval = time.Millisecond

	svc.Start()
	svc.Start()
	time.Sleep(3 * time.Millisecond)
	svc.Stop()
	svc.Stop()
}

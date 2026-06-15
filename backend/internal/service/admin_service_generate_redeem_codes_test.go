package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestGenerateRedeemCodes_SubscriptionGroupPreservesNegativeValidityDays(t *testing.T) {
	groupID := int64(42)
	redeemRepo := &generateRedeemCodesRedeemRepoStub{}
	svc := &adminServiceImpl{
		redeemCodeRepo: redeemRepo,
		groupRepo: &generateRedeemCodesGroupRepoStub{group: &Group{
			ID:               groupID,
			SubscriptionType: SubscriptionTypeSubscription,
		}},
	}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count:        1,
		Type:         RedeemTypeSubscription,
		Value:        1,
		GroupID:      &groupID,
		ValidityDays: -7,
	})

	require.NoError(t, err)
	require.Len(t, codes, 1)
	require.Equal(t, -7, codes[0].ValidityDays)
	require.Len(t, redeemRepo.created, 1)
	require.Equal(t, -7, redeemRepo.created[0].ValidityDays)
}

func TestGenerateRedeemCodes_SubscriptionRejectsProvidedZeroPlanID(t *testing.T) {
	groupID := int64(42)
	zeroPlanID := int64(0)
	redeemRepo := &generateRedeemCodesRedeemRepoStub{}
	svc := &adminServiceImpl{
		redeemCodeRepo: redeemRepo,
		groupRepo: &generateRedeemCodesGroupRepoStub{group: &Group{
			ID:               groupID,
			SubscriptionType: SubscriptionTypeSubscription,
		}},
	}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count:        1,
		Type:         RedeemTypeSubscription,
		Value:        1,
		GroupID:      &groupID,
		PlanID:       &zeroPlanID,
		ValidityDays: 30,
	})

	require.Nil(t, codes)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Empty(t, redeemRepo.created)
}

func TestGenerateRedeemCodes_SubscriptionRejectsProvidedZeroGroupID(t *testing.T) {
	zeroGroupID := int64(0)
	planID := int64(7)
	redeemRepo := &generateRedeemCodesRedeemRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: redeemRepo}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count:   1,
		Type:    RedeemTypeSubscription,
		Value:   1,
		GroupID: &zeroGroupID,
		PlanID:  &planID,
	})

	require.Nil(t, codes)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Empty(t, redeemRepo.created)
}

type generateRedeemCodesRedeemRepoStub struct {
	created []*RedeemCode
}

func (s *generateRedeemCodesRedeemRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	s.created = append(s.created, code)
	return nil
}

func (s *generateRedeemCodesRedeemRepoStub) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *generateRedeemCodesRedeemRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	panic("unexpected GetByID call")
}

func (s *generateRedeemCodesRedeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	panic("unexpected GetByCode call")
}

func (s *generateRedeemCodesRedeemRepoStub) Update(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Update call")
}

func (s *generateRedeemCodesRedeemRepoStub) BatchUpdate(ctx context.Context, ids []int64, fields RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected BatchUpdate call")
}

func (s *generateRedeemCodesRedeemRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *generateRedeemCodesRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	panic("unexpected Use call")
}

func (s *generateRedeemCodesRedeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *generateRedeemCodesRedeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *generateRedeemCodesRedeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *generateRedeemCodesRedeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *generateRedeemCodesRedeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

type generateRedeemCodesGroupRepoStub struct {
	group *Group
}

func (s *generateRedeemCodesGroupRepoStub) Create(ctx context.Context, group *Group) error {
	panic("unexpected Create call")
}

func (s *generateRedeemCodesGroupRepoStub) GetByID(ctx context.Context, id int64) (*Group, error) {
	if s.group == nil {
		return nil, ErrGroupNotFound
	}
	return s.group, nil
}

func (s *generateRedeemCodesGroupRepoStub) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	panic("unexpected GetByIDLite call")
}

func (s *generateRedeemCodesGroupRepoStub) Update(ctx context.Context, group *Group) error {
	panic("unexpected Update call")
}

func (s *generateRedeemCodesGroupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *generateRedeemCodesGroupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *generateRedeemCodesGroupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *generateRedeemCodesGroupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *generateRedeemCodesGroupRepoStub) ListActive(ctx context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *generateRedeemCodesGroupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *generateRedeemCodesGroupRepoStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *generateRedeemCodesGroupRepoStub) GetAccountCount(ctx context.Context, groupID int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *generateRedeemCodesGroupRepoStub) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *generateRedeemCodesGroupRepoStub) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *generateRedeemCodesGroupRepoStub) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *generateRedeemCodesGroupRepoStub) UpdateSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

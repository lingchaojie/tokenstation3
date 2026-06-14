package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type batchUpdateTargetRedeemRepoStub struct {
	codesByID         map[int64]*RedeemCode
	batchUpdateCalled bool
}

func (s *batchUpdateTargetRedeemRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Create call")
}

func (s *batchUpdateTargetRedeemRepoStub) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *batchUpdateTargetRedeemRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	if code, ok := s.codesByID[id]; ok {
		copy := *code
		return &copy, nil
	}
	return nil, ErrRedeemCodeNotFound
}

func (s *batchUpdateTargetRedeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	panic("unexpected GetByCode call")
}

func (s *batchUpdateTargetRedeemRepoStub) Update(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Update call")
}

func (s *batchUpdateTargetRedeemRepoStub) BatchUpdate(ctx context.Context, ids []int64, fields RedeemCodeBatchUpdateFields) (int64, error) {
	s.batchUpdateCalled = true
	return int64(len(ids)), nil
}

func (s *batchUpdateTargetRedeemRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *batchUpdateTargetRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	panic("unexpected Use call")
}

func (s *batchUpdateTargetRedeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *batchUpdateTargetRedeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *batchUpdateTargetRedeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *batchUpdateTargetRedeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *batchUpdateTargetRedeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func TestRedeemService_BatchUpdate_RejectsPlanIDForNonSubscriptionCodeTargetState(t *testing.T) {
	planID := int64(10)
	repo := &batchUpdateTargetRedeemRepoStub{codesByID: map[int64]*RedeemCode{
		42: {ID: 42, Type: RedeemTypeBalance, Status: StatusUnused},
	}}
	svc := &RedeemService{redeemRepo: repo}

	result, err := svc.BatchUpdate(context.Background(), &RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: RedeemCodeBatchUpdateFields{
			PlanID: NullableInt64Update{Set: true, Value: &planID},
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.False(t, repo.batchUpdateCalled)
}

func TestRedeemService_BatchUpdate_RejectsSubscriptionTargetConflictAfterUpdateTargetState(t *testing.T) {
	groupID := int64(5)
	planID := int64(10)
	repo := &batchUpdateTargetRedeemRepoStub{codesByID: map[int64]*RedeemCode{
		42: {ID: 42, Type: RedeemTypeSubscription, Status: StatusUnused, GroupID: &groupID},
	}}
	svc := &RedeemService{redeemRepo: repo}

	result, err := svc.BatchUpdate(context.Background(), &RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: RedeemCodeBatchUpdateFields{
			PlanID: NullableInt64Update{Set: true, Value: &planID},
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.False(t, repo.batchUpdateCalled)
}

func TestRedeemService_BatchUpdate_RejectsSubscriptionMissingTargetAfterUpdateTargetState(t *testing.T) {
	groupID := int64(5)
	repo := &batchUpdateTargetRedeemRepoStub{codesByID: map[int64]*RedeemCode{
		42: {ID: 42, Type: RedeemTypeSubscription, Status: StatusUnused, GroupID: &groupID},
	}}
	svc := &RedeemService{redeemRepo: repo}

	result, err := svc.BatchUpdate(context.Background(), &RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: RedeemCodeBatchUpdateFields{
			GroupID: NullableInt64Update{Set: true, Value: nil},
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.False(t, repo.batchUpdateCalled)
}

func TestRedeemService_BatchUpdate_RejectsNonexistentPositivePlanIDTargetState(t *testing.T) {
	ctx := context.Background()
	client := newBatchUpdateTargetTestClient(t)
	planID := int64(999999)
	repo := &batchUpdateTargetRedeemRepoStub{codesByID: map[int64]*RedeemCode{
		42: {ID: 42, Type: RedeemTypeSubscription, Status: StatusUnused},
	}}
	svc := &RedeemService{redeemRepo: repo, entClient: client}

	result, err := svc.BatchUpdate(ctx, &RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: RedeemCodeBatchUpdateFields{
			PlanID: NullableInt64Update{Set: true, Value: &planID},
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, infraerrors.IsNotFound(err))
	require.False(t, repo.batchUpdateCalled)
}

func TestRedeemService_BatchUpdate_RejectsPositivePlanIDWhenValidationUnavailableTargetState(t *testing.T) {
	planID := int64(10)
	repo := &batchUpdateTargetRedeemRepoStub{codesByID: map[int64]*RedeemCode{
		42: {ID: 42, Type: RedeemTypeSubscription, Status: StatusUnused},
	}}
	svc := &RedeemService{redeemRepo: repo}

	result, err := svc.BatchUpdate(context.Background(), &RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: RedeemCodeBatchUpdateFields{
			PlanID: NullableInt64Update{Set: true, Value: &planID},
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, infraerrors.IsInternalServer(err))
	require.False(t, repo.batchUpdateCalled)
}

func newBatchUpdateTargetTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	dbName := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_fk=1",
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

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newCheckInRepoSQLite(t *testing.T) (service.CheckInRepository, *dbent.Client, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return NewCheckInRepository(client), client, db
}

func mustCreateCheckInUser(t *testing.T, client *dbent.Client, balance, totalRecharged float64) *dbent.User {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(fmt.Sprintf("check-in-%d@example.com", time.Now().UnixNano())).
		SetPasswordHash("test-password-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(balance).
		SetTotalRecharged(totalRecharged).
		Save(context.Background())
	require.NoError(t, err)
	return user
}

func checkInClaimInput(userID int64) service.DailyCheckInClaimInput {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	return service.DailyCheckInClaimInput{
		UserID:          userID,
		ActivityStartAt: start,
		CheckInDate:     time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		RewardAmount:    10,
		ClaimedAt:       start.Add(25 * time.Hour),
	}
}

func TestCheckInRepository_CreateClaim_CreditsBalanceWithoutRecharge(t *testing.T) {
	repo, client, _ := newCheckInRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateCheckInUser(t, client, 3.5, 99)

	got, err := repo.CreateClaim(ctx, checkInClaimInput(user.ID))

	require.NoError(t, err)
	require.InDelta(t, 10, got.RewardAmount, 1e-9)
	require.InDelta(t, 13.5, got.BalanceAfter, 1e-9)
	persisted, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 13.5, persisted.Balance, 1e-9)
	require.InDelta(t, 99, persisted.TotalRecharged, 1e-9)
}

func TestCheckInRepository_CreateClaim_DuplicateRollsBackCredit(t *testing.T) {
	repo, client, _ := newCheckInRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateCheckInUser(t, client, 0, 0)
	input := checkInClaimInput(user.ID)

	_, err := repo.CreateClaim(ctx, input)
	require.NoError(t, err)
	_, err = repo.CreateClaim(ctx, input)

	require.ErrorIs(t, err, service.ErrDailyCheckInAlreadyClaimed)
	persisted, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 10, persisted.Balance, 1e-9)
}

func TestCheckInRepository_CreateClaim_BalanceFailureRollsBackClaim(t *testing.T) {
	repo, client, db := newCheckInRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateCheckInUser(t, client, 0, 0)
	_, err := db.Exec(`
CREATE TRIGGER fail_check_in_balance
BEFORE UPDATE OF balance ON users
BEGIN
  SELECT RAISE(FAIL, 'forced balance update failure');
END`)
	require.NoError(t, err)

	_, err = repo.CreateClaim(ctx, checkInClaimInput(user.ID))

	require.ErrorContains(t, err, "forced balance update failure")
	count, err := client.DailyCheckInClaim.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestCheckInRepository_CreateClaim_UserNotFound(t *testing.T) {
	repo, client, _ := newCheckInRepoSQLite(t)
	ctx := context.Background()

	_, err := repo.CreateClaim(ctx, checkInClaimInput(999999))

	require.ErrorIs(t, err, service.ErrUserNotFound)
	count, countErr := client.DailyCheckInClaim.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count)
}

func TestCheckInRepository_FindClaim_ReturnsMatchingAuditRecord(t *testing.T) {
	repo, client, _ := newCheckInRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateCheckInUser(t, client, 0, 0)
	input := checkInClaimInput(user.ID)
	created, err := repo.CreateClaim(ctx, input)
	require.NoError(t, err)

	got, err := repo.FindClaim(ctx, user.ID, input.ActivityStartAt, input.CheckInDate)

	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, user.ID, got.UserID)
	require.InDelta(t, 10, got.RewardAmount, 1e-9)
}

func TestCheckInRepository_HardUserDeletionCascadesClaims(t *testing.T) {
	repo, client, _ := newCheckInRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateCheckInUser(t, client, 0, 0)
	_, err := repo.CreateClaim(ctx, checkInClaimInput(user.ID))
	require.NoError(t, err)

	err = client.User.DeleteOneID(user.ID).Exec(mixins.SkipSoftDelete(ctx))

	require.NoError(t, err)
	count, err := client.DailyCheckInClaim.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

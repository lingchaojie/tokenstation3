//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func newPlanSeatTestClient(t *testing.T) *dbent.Client {
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

func createSeatUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) int64 {
	t.Helper()
	return client.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		SetRole(domain.RoleUser).
		SaveX(ctx).
		ID
}

func createSeatPlanFixture(t *testing.T, ctx context.Context, client *dbent.Client, limit *int) (*dbent.SubscriptionPlan, int64) {
	t.Helper()
	userID := createSeatUser(t, ctx, client, "seat-user@example.com")
	group := client.Group.Create().
		SetName("Seat Group").
		SetPlatform(domain.PlatformAnthropic).
		SetSubscriptionType(domain.SubscriptionTypeSubscription).
		SaveX(ctx)
	planCreate := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Seat Plan").
		SetDescription("").
		SetPrice(9.99).
		SetValidityDays(30).
		SetValidityUnit("days").
		SetFeatures("").
		SetProductName("").
		SetForSale(true).
		SetSortOrder(0)
	if limit != nil {
		planCreate.SetSeatLimit(*limit)
	}
	plan := planCreate.SaveX(ctx)
	return plan, userID
}

func createSeatSubscription(t *testing.T, ctx context.Context, client *dbent.Client, userID, groupID, planID int64, status string, expiresAt time.Time) {
	t.Helper()
	client.UserSubscription.Create().
		SetUserID(userID).
		SetGroupID(groupID).
		SetPlanID(planID).
		SetStartsAt(time.Now()).
		SetExpiresAt(expiresAt).
		SetStatus(status).
		SaveX(ctx)
}

func TestPlanSeatSummary_UnlimitedHasNoFullState(t *testing.T) {
	ctx := context.Background()
	client := newPlanSeatTestClient(t)
	plan, _ := createSeatPlanFixture(t, ctx, client, nil)
	svc := NewPaymentConfigService(client, nil, nil)

	summaries, err := svc.SeatSummariesForPlans(ctx, []*dbent.SubscriptionPlan{plan})

	require.NoError(t, err)
	summary := summaries[plan.ID]
	require.Nil(t, summary.SeatLimit)
	require.Equal(t, 0, summary.SeatUsed)
	require.Nil(t, summary.SeatAvailable)
	require.False(t, summary.SeatFull)
	require.False(t, summary.SeatOverLimit)
}

func TestPlanSeatSummary_CountsDistinctActiveUnexpiredUsers(t *testing.T) {
	ctx := context.Background()
	client := newPlanSeatTestClient(t)
	limit := 2
	plan, userID := createSeatPlanFixture(t, ctx, client, &limit)
	planID := plan.ID
	now := time.Now()
	statusExpiredUserID := createSeatUser(t, ctx, client, "seat-expired-status@example.com")
	activeExpiredUserID := createSeatUser(t, ctx, client, "seat-active-expired@example.com")

	createSeatSubscription(t, ctx, client, userID, plan.GroupID, planID, SubscriptionStatusActive, now.Add(time.Hour))
	createSeatSubscription(t, ctx, client, statusExpiredUserID, plan.GroupID, planID, SubscriptionStatusExpired, now.Add(time.Hour))
	createSeatSubscription(t, ctx, client, activeExpiredUserID, plan.GroupID, planID, SubscriptionStatusActive, now.Add(-time.Hour))

	svc := NewPaymentConfigService(client, nil, nil)
	summaries, err := svc.SeatSummariesForPlans(ctx, []*dbent.SubscriptionPlan{plan})

	require.NoError(t, err)
	summary := summaries[plan.ID]
	require.Equal(t, 1, summary.SeatUsed)
	require.NotNil(t, summary.SeatAvailable)
	require.Equal(t, 1, *summary.SeatAvailable)
	require.False(t, summary.SeatFull)
	require.False(t, summary.SeatOverLimit)
}

func TestPlanSeatSummary_DuplicateActiveRowsForSameUserCountOnce(t *testing.T) {
	ctx := context.Background()
	client := newPlanSeatTestClient(t)
	limit := 2
	plan, userID := createSeatPlanFixture(t, ctx, client, &limit)
	planID := plan.ID
	now := time.Now()

	createSeatSubscription(t, ctx, client, userID, plan.GroupID, planID, SubscriptionStatusActive, now.Add(time.Hour))
	createSeatSubscription(t, ctx, client, userID, plan.GroupID, planID, SubscriptionStatusActive, now.Add(2*time.Hour))

	svc := NewPaymentConfigService(client, nil, nil)
	summaries, err := svc.SeatSummariesForPlans(ctx, []*dbent.SubscriptionPlan{plan})

	require.NoError(t, err)
	summary := summaries[plan.ID]
	require.Equal(t, 1, summary.SeatUsed)
	require.NotNil(t, summary.SeatAvailable)
	require.Equal(t, 1, *summary.SeatAvailable)
	require.False(t, summary.SeatFull)
}

func TestValidatePlanSeatAvailable_AllowsSameUserRenewalWhenFullAndRejectsNewUser(t *testing.T) {
	ctx := context.Background()
	client := newPlanSeatTestClient(t)
	limit := 1
	plan, userID := createSeatPlanFixture(t, ctx, client, &limit)
	planID := plan.ID
	now := time.Now()
	newUserID := createSeatUser(t, ctx, client, "seat-new-user@example.com")
	createSeatSubscription(t, ctx, client, userID, plan.GroupID, planID, SubscriptionStatusActive, now.Add(time.Hour))

	svc := NewPaymentConfigService(client, nil, nil)

	require.NoError(t, svc.ValidatePlanSeatAvailable(ctx, plan, userID))
	err := svc.ValidatePlanSeatAvailable(ctx, plan, newUserID)
	require.ErrorIs(t, err, ErrPlanSeatLimitReached)
	require.Equal(t, "PLAN_SEAT_LIMIT_REACHED", infraerrors.Reason(err))
}

func TestLockPlanForUpdate_ReturnsPlanWithSQLite(t *testing.T) {
	ctx := context.Background()
	client := newPlanSeatTestClient(t)
	plan, _ := createSeatPlanFixture(t, ctx, client, nil)
	svc := NewPaymentConfigService(client, nil, nil)

	lockedPlan, err := svc.LockPlanForUpdate(ctx, plan.ID)

	require.NoError(t, err)
	require.Equal(t, plan.ID, lockedPlan.ID)
}

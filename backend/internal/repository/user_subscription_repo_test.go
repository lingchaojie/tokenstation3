package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newUserSubscriptionRepoSQLite(t *testing.T) (*userSubscriptionRepository, *dbent.Client, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	return &userSubscriptionRepository{client: client}, client, db
}

func mustCreateUserSubscriptionRepoUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) *dbent.User {
	t.Helper()

	user, err := client.User.Create().
		SetEmail(email).
		SetPasswordHash("test-password-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func mustCreateUserSubscriptionRepoGroup(t *testing.T, ctx context.Context, client *dbent.Client, name string) *dbent.Group {
	t.Helper()

	group, err := client.Group.Create().
		SetName(name).
		SetStatus(service.StatusActive).
		SetPlatform(service.PlatformAnthropic).
		SetRateMultiplier(1).
		SetSubscriptionType(service.SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	return group
}

func TestUserSubscriptionRepository_ResetWeeklyUsage_StaleResetDoesNotWipeUsageAfterConcurrentReset(t *testing.T) {
	repo, client, _ := newUserSubscriptionRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateUserSubscriptionRepoUser(t, ctx, client, "resetw-stale@test.com")
	group := mustCreateUserSubscriptionRepoGroup(t, ctx, client, "g-resetw-stale")
	oldWindowStart := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	sub, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetStartsAt(oldWindowStart).
		SetExpiresAt(oldWindowStart.AddDate(0, 0, 30)).
		SetStatus(service.SubscriptionStatusActive).
		SetAssignedAt(oldWindowStart).
		SetWeeklyWindowStart(oldWindowStart).
		SetWeeklyUsageUsd(15.0).
		Save(ctx)
	require.NoError(t, err)

	firstResetAt := oldWindowStart.Add(7 * 24 * time.Hour)
	err = repo.ResetWeeklyUsage(ctx, sub.ID, sub.WeeklyWindowStart, firstResetAt)
	require.NoError(t, err, "first ResetWeeklyUsage")

	const billedUsage = 3.25
	_, err = client.UserSubscription.UpdateOneID(sub.ID).
		SetWeeklyUsageUsd(billedUsage).
		Save(ctx)
	require.NoError(t, err, "simulate usage billed after first reset")

	staleResetAt := firstResetAt.Add(time.Second)
	err = repo.ResetWeeklyUsage(ctx, sub.ID, sub.WeeklyWindowStart, staleResetAt)
	require.NoError(t, err, "stale ResetWeeklyUsage should be treated as success")

	got, err := repo.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.InDelta(t, billedUsage, got.WeeklyUsageUSD, 1e-6)
	require.NotNil(t, got.WeeklyWindowStart)
	require.WithinDuration(t, firstResetAt, *got.WeeklyWindowStart, time.Microsecond)
}

func TestUserSubscriptionRepository_ExpiredPlanSnapshotRenewalClearsNilSevenDayLimit(t *testing.T) {
	repo, client, db := newUserSubscriptionRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateUserSubscriptionRepoUser(t, ctx, client, "expired-plan-snapshot-clear@test.com")
	group := mustCreateUserSubscriptionRepoGroup(t, ctx, client, "g-expired-plan-snapshot-clear")
	oldPlanID := int64(10)
	oldPlanName := "old quota plan"
	oldQuota := 77.0
	created, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetStartsAt(time.Now().AddDate(0, 0, -60)).
		SetExpiresAt(time.Now().Add(-24 * time.Hour)).
		SetStatus(service.SubscriptionStatusExpired).
		SetAssignedAt(time.Now().AddDate(0, 0, -60)).
		SetNotes("old").
		SetPlanID(oldPlanID).
		SetPlanName(oldPlanName).
		SetSevenDayLimitUsd(oldQuota).
		Save(ctx)
	require.NoError(t, err)

	newPlanID := int64(20)
	newPlanName := "paid plan without quota"
	subscriptionSvc := service.NewSubscriptionService(NewGroupRepository(client, db), repo, nil, nil, nil)

	renewed, reused, err := subscriptionSvc.AssignOrExtendSubscription(ctx, &service.AssignSubscriptionInput{
		UserID:       user.ID,
		GroupID:      group.ID,
		ValidityDays: 30,
		PlanID:       &newPlanID,
		PlanName:     &newPlanName,
	})

	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, created.ID, renewed.ID)
	require.Equal(t, service.SubscriptionStatusActive, renewed.Status)
	require.NotNil(t, renewed.PlanID)
	require.Equal(t, newPlanID, *renewed.PlanID)
	require.NotNil(t, renewed.PlanName)
	require.Equal(t, newPlanName, *renewed.PlanName)
	require.Nil(t, renewed.SevenDayLimitUSD)

	persisted, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, persisted.SevenDayLimitUSD)
}

func TestUserSubscriptionRepository_ScheduleAndClearPlanChange(t *testing.T) {
	repo, client, _ := newUserSubscriptionRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateUserSubscriptionRepoUser(t, ctx, client, "schedule-clear@test.com")
	group := mustCreateUserSubscriptionRepoGroup(t, ctx, client, "g-schedule-clear")
	created, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetStartsAt(time.Now()).
		SetExpiresAt(time.Now().AddDate(0, 0, 30)).
		SetStatus(service.SubscriptionStatusActive).
		SetAssignedAt(time.Now()).
		SetNotes("old").
		Save(ctx)
	require.NoError(t, err)

	planID := int64(42)
	planName := "Basic monthly"
	limit := 60.0
	orderID := int64(99)
	effectiveAt := time.Now().AddDate(0, 0, 30).UTC().Truncate(time.Second)
	expiresAt := effectiveAt.AddDate(0, 0, 30)
	notes := "scheduled downgrade"

	require.NoError(t, repo.SchedulePlanChange(ctx, created.ID, &planID, &planName, &limit, effectiveAt, expiresAt, &orderID, &notes))

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ScheduledPlanID)
	require.Equal(t, planID, *got.ScheduledPlanID)
	require.NotNil(t, got.ScheduledPlanName)
	require.Equal(t, planName, *got.ScheduledPlanName)
	require.NotNil(t, got.ScheduledSevenDayLimitUSD)
	require.InDelta(t, limit, *got.ScheduledSevenDayLimitUSD, 1e-6)
	require.NotNil(t, got.ScheduledPlanEffectiveAt)
	require.WithinDuration(t, effectiveAt, *got.ScheduledPlanEffectiveAt, time.Second)
	require.NotNil(t, got.ScheduledExpiresAt)
	require.WithinDuration(t, expiresAt, *got.ScheduledExpiresAt, time.Second)
	require.NotNil(t, got.ScheduledOrderID)
	require.Equal(t, orderID, *got.ScheduledOrderID)
	require.Equal(t, notes, got.Notes)

	require.NoError(t, repo.ClearScheduledPlanChange(ctx, created.ID))

	got, err = repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, got.ScheduledPlanID)
	require.Nil(t, got.ScheduledPlanName)
	require.Nil(t, got.ScheduledSevenDayLimitUSD)
	require.Nil(t, got.ScheduledPlanEffectiveAt)
	require.Nil(t, got.ScheduledExpiresAt)
	require.Nil(t, got.ScheduledOrderID)
}

func TestUserSubscriptionRepository_ApplyScheduledPlanChange(t *testing.T) {
	repo, client, _ := newUserSubscriptionRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateUserSubscriptionRepoUser(t, ctx, client, "apply-scheduled@test.com")
	group := mustCreateUserSubscriptionRepoGroup(t, ctx, client, "g-apply-scheduled")
	oldPlanID := int64(10)
	oldPlanName := "Pro monthly"
	oldLimit := 260.0
	newPlanID := int64(20)
	newPlanName := "Basic monthly"
	newLimit := 60.0
	effectiveAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	scheduledExpiresAt := effectiveAt.AddDate(0, 0, 30)
	created, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetStartsAt(effectiveAt.AddDate(0, 0, -30)).
		SetExpiresAt(effectiveAt).
		SetStatus(service.SubscriptionStatusActive).
		SetAssignedAt(effectiveAt.AddDate(0, 0, -30)).
		SetPlanID(oldPlanID).
		SetPlanName(oldPlanName).
		SetSevenDayLimitUsd(oldLimit).
		SetScheduledPlanID(newPlanID).
		SetScheduledPlanName(newPlanName).
		SetScheduledSevenDayLimitUsd(newLimit).
		SetScheduledPlanEffectiveAt(effectiveAt).
		SetScheduledExpiresAt(scheduledExpiresAt).
		SetScheduledOrderID(123).
		SetDailyWindowStart(effectiveAt.AddDate(0, 0, -1)).
		SetWeeklyWindowStart(effectiveAt.AddDate(0, 0, -7)).
		SetMonthlyWindowStart(effectiveAt.AddDate(0, 0, -30)).
		SetDailyUsageUsd(3).
		SetWeeklyUsageUsd(25).
		SetMonthlyUsageUsd(80).
		Save(ctx)
	require.NoError(t, err)

	applied, changed, err := repo.ApplyScheduledPlanChange(ctx, created.ID, time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, applied)
	require.NotNil(t, applied.PlanID)
	require.Equal(t, newPlanID, *applied.PlanID)
	require.NotNil(t, applied.PlanName)
	require.Equal(t, newPlanName, *applied.PlanName)
	require.NotNil(t, applied.SevenDayLimitUSD)
	require.InDelta(t, newLimit, *applied.SevenDayLimitUSD, 1e-6)
	require.WithinDuration(t, effectiveAt, applied.StartsAt, time.Second)
	require.WithinDuration(t, scheduledExpiresAt, applied.ExpiresAt, time.Second)
	require.Equal(t, service.SubscriptionStatusActive, applied.Status)
	require.Zero(t, applied.DailyUsageUSD)
	require.Zero(t, applied.WeeklyUsageUSD)
	require.Zero(t, applied.MonthlyUsageUSD)
	require.NotNil(t, applied.DailyWindowStart)
	require.WithinDuration(t, effectiveAt, *applied.DailyWindowStart, time.Second)
	require.NotNil(t, applied.WeeklyWindowStart)
	require.WithinDuration(t, effectiveAt, *applied.WeeklyWindowStart, time.Second)
	require.NotNil(t, applied.MonthlyWindowStart)
	require.WithinDuration(t, effectiveAt, *applied.MonthlyWindowStart, time.Second)
	require.Nil(t, applied.ScheduledPlanID)
	require.Nil(t, applied.ScheduledPlanName)
	require.Nil(t, applied.ScheduledSevenDayLimitUSD)
	require.Nil(t, applied.ScheduledPlanEffectiveAt)
	require.Nil(t, applied.ScheduledExpiresAt)
	require.Nil(t, applied.ScheduledOrderID)

	applied, changed, err = repo.ApplyScheduledPlanChange(ctx, created.ID, time.Now())
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, applied)
}

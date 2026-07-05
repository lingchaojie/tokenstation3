//go:build unit

package service_test

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
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newRedeemPlanTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	dbName := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()),
	)
	db, err := sql.Open("sqlite", dbName)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedRedeemPlanUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) int64 {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(email).
		SetUsername(email).
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	return user.ID
}

func newRedeemPlanService(client *dbent.Client) *service.RedeemService {
	groupRepo := repository.NewGroupRepository(client, nil)
	userSubRepo := repository.NewUserSubscriptionRepository(client)
	subSvc := service.NewSubscriptionService(groupRepo, userSubRepo, nil, client, nil)
	return service.NewRedeemService(
		repository.NewRedeemCodeRepository(client),
		repository.NewUserRepository(client, nil),
		subSvc,
		nil,
		nil,
		client,
		nil,
	)
}

func createRedeemPlan(t *testing.T, ctx context.Context, client *dbent.Client, name string, validityDays int, quota float64, seatLimit *int) *dbent.SubscriptionPlan {
	t.Helper()
	builder := client.SubscriptionPlan.Create().
		SetName(name).
		SetDescription("").
		SetPrice(10).
		SetSevenDayQuotaUsd(quota).
		SetValidityDays(validityDays).
		SetValidityUnit("days").
		SetFeatures("").
		SetProductName("").
		SetForSale(true).
		SetSortOrder(0)
	if seatLimit != nil {
		builder.SetSeatLimit(*seatLimit)
	}
	plan, err := builder.Save(ctx)
	require.NoError(t, err)
	return plan
}

func createRedeemPlanCode(t *testing.T, ctx context.Context, client *dbent.Client, code string, planID int64, validityDays int) int64 {
	t.Helper()
	created, err := client.RedeemCode.Create().
		SetCode(code).
		SetType(service.RedeemTypeSubscription).
		SetValue(1).
		SetStatus(service.StatusUnused).
		SetValidityDays(validityDays).
		SetPlanID(planID).
		Save(ctx)
	require.NoError(t, err)
	return created.ID
}

func createOrphanRedeemPlanCode(t *testing.T, ctx context.Context, client *dbent.Client, code string, planID int64, validityDays int) int64 {
	t.Helper()
	require.NoError(t, client.Driver().Exec(ctx, "PRAGMA foreign_keys = OFF", []any{}, nil))
	created, err := client.RedeemCode.Create().
		SetCode(code).
		SetType(service.RedeemTypeSubscription).
		SetValue(1).
		SetStatus(service.StatusUnused).
		SetValidityDays(validityDays).
		SetPlanID(planID).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Driver().Exec(ctx, "PRAGMA foreign_keys = ON", []any{}, nil))
	return created.ID
}

func requireRedeemPlanSubscription(t *testing.T, ctx context.Context, client *dbent.Client, userID int64) *dbent.UserSubscription {
	t.Helper()
	sub, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDIsNil()).
		Only(ctx)
	require.NoError(t, err)
	return sub
}

func TestRedeemPlanSubscriptionCodeUsesPlanDefaultDays(t *testing.T) {
	ctx := context.Background()
	client := newRedeemPlanTestClient(t)
	svc := newRedeemPlanService(client)
	userID := seedRedeemPlanUser(t, ctx, client, "plan-default@example.com")
	plan := createRedeemPlan(t, ctx, client, "Default Plan", 45, 12.5, nil)
	code := "PLAN-DEFAULT-DAYS"
	createRedeemPlanCode(t, ctx, client, code, plan.ID, 0)

	before := time.Now()
	usedCode, err := svc.Redeem(ctx, userID, code)

	require.NoError(t, err)
	require.Equal(t, service.StatusUsed, usedCode.Status)
	require.NotNil(t, usedCode.Plan)
	require.Equal(t, plan.ID, usedCode.Plan.ID)
	require.Equal(t, plan.Name, usedCode.Plan.Name)
	require.Equal(t, 45, usedCode.Plan.ValidityDays)
	sub := requireRedeemPlanSubscription(t, ctx, client, userID)
	require.NotNil(t, sub.PlanID)
	require.Equal(t, plan.ID, *sub.PlanID)
	require.NotNil(t, sub.PlanName)
	require.Equal(t, plan.Name, *sub.PlanName)
	require.NotNil(t, sub.SevenDayLimitUsd)
	require.InDelta(t, 12.5, *sub.SevenDayLimitUsd, 0.000001)
	require.WithinDuration(t, before.AddDate(0, 0, 45), sub.ExpiresAt, 5*time.Second)
}

func TestRedeemPlanSubscriptionCodeUsesOverrideDays(t *testing.T) {
	ctx := context.Background()
	client := newRedeemPlanTestClient(t)
	svc := newRedeemPlanService(client)
	userID := seedRedeemPlanUser(t, ctx, client, "plan-override@example.com")
	plan := createRedeemPlan(t, ctx, client, "Override Plan", 30, 8.75, nil)
	code := "PLAN-OVERRIDE-DAYS"
	createRedeemPlanCode(t, ctx, client, code, plan.ID, 90)

	before := time.Now()
	_, err := svc.Redeem(ctx, userID, code)

	require.NoError(t, err)
	sub := requireRedeemPlanSubscription(t, ctx, client, userID)
	require.WithinDuration(t, before.AddDate(0, 0, 90), sub.ExpiresAt, 5*time.Second)
}

func TestRedeemPlanSubscriptionCodeMissingPlanDoesNotConsumeCode(t *testing.T) {
	ctx := context.Background()
	client := newRedeemPlanTestClient(t)
	svc := newRedeemPlanService(client)
	userID := seedRedeemPlanUser(t, ctx, client, "plan-missing@example.com")
	code := "PLAN-MISSING"
	codeID := createOrphanRedeemPlanCode(t, ctx, client, code, 9999, 30)

	usedCode, err := svc.Redeem(ctx, userID, code)

	require.Nil(t, usedCode)
	require.Error(t, err)
	stored, err := client.RedeemCode.Query().Where(redeemcode.IDEQ(codeID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, service.StatusUnused, stored.Status)
	require.Nil(t, stored.UsedBy)
	require.Nil(t, stored.UsedAt)
}

func TestRedeemPlanSubscriptionCodeSeatLimitDoesNotConsumeCode(t *testing.T) {
	ctx := context.Background()
	client := newRedeemPlanTestClient(t)
	svc := newRedeemPlanService(client)
	occupyingUserID := seedRedeemPlanUser(t, ctx, client, "plan-seat-used@example.com")
	userID := seedRedeemPlanUser(t, ctx, client, "plan-seat-new@example.com")
	seatLimit := 1
	plan := createRedeemPlan(t, ctx, client, "Seat Plan", 30, 20, &seatLimit)
	client.UserSubscription.Create().
		SetUserID(occupyingUserID).
		SetPlanID(plan.ID).
		SetStartsAt(time.Now().Add(-time.Hour)).
		SetExpiresAt(time.Now().Add(24 * time.Hour)).
		SetStatus(service.SubscriptionStatusActive).
		SaveX(ctx)
	code := "PLAN-SEAT-FULL"
	codeID := createRedeemPlanCode(t, ctx, client, code, plan.ID, 30)

	usedCode, err := svc.Redeem(ctx, userID, code)

	require.Nil(t, usedCode)
	require.Error(t, err)
	stored, err := client.RedeemCode.Query().Where(redeemcode.IDEQ(codeID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, service.StatusUnused, stored.Status)
	require.Nil(t, stored.UsedBy)
	require.Nil(t, stored.UsedAt)
}

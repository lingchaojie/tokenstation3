//go:build unit

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type paymentFulfillmentPlanSnapshotHarness struct {
	client           *dbent.Client
	user             *dbent.User
	paymentService   *PaymentService
	subscriptionRepo *subscriptionUserSubRepoStub
}

func newPaymentFulfillmentPlanSnapshotHarness(t *testing.T) *paymentFulfillmentPlanSnapshotHarness {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:payment_fulfillment_plan_snapshot_%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	user := client.User.Create().
		SetEmail("snapshot@example.com").
		SetPasswordHash("hash").
		SetUsername("snapshot").
		SaveX(ctx)

	groupRepo := &paymentFulfillmentGroupRepoStub{}
	subscriptionRepo := newSubscriptionUserSubRepoStub()
	configService := NewPaymentConfigService(client, nil, nil)
	subscriptionSvc := NewSubscriptionService(groupRepo, subscriptionRepo, nil, nil, nil)
	paymentService := &PaymentService{
		entClient:       client,
		subscriptionSvc: subscriptionSvc,
		configService:   configService,
		groupRepo:       groupRepo,
	}

	return &paymentFulfillmentPlanSnapshotHarness{
		client:           client,
		user:             user,
		paymentService:   paymentService,
		subscriptionRepo: subscriptionRepo,
	}
}

type paymentFulfillmentGroupRepoStub struct {
	groupRepoNoop
	groups map[int64]*Group
}

func (s *paymentFulfillmentGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group := s.groups[id]
	if group == nil {
		return nil, ErrGroupNotSubscriptionType
	}
	cp := *group
	return &cp, nil
}

func (h *paymentFulfillmentPlanSnapshotHarness) createSubscriptionGroup(t *testing.T, name string) *dbent.Group {
	t.Helper()
	entGroup := h.client.Group.Create().
		SetName(name).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		SetPlatform(PlatformAnthropic).
		SetRateMultiplier(1).
		SaveX(context.Background())

	stub := h.paymentService.groupRepo.(*paymentFulfillmentGroupRepoStub)
	if stub.groups == nil {
		stub.groups = make(map[int64]*Group)
	}
	stub.groups[entGroup.ID] = &Group{
		ID:               entGroup.ID,
		Name:             entGroup.Name,
		Status:           entGroup.Status,
		SubscriptionType: entGroup.SubscriptionType,
		Platform:         entGroup.Platform,
		RateMultiplier:   entGroup.RateMultiplier,
	}
	return entGroup
}

func (h *paymentFulfillmentPlanSnapshotHarness) createSubscriptionPlan(t *testing.T, groupID int64, name string, price float64, days int, quota *float64) *dbent.SubscriptionPlan {
	t.Helper()
	builder := h.client.SubscriptionPlan.Create().
		SetGroupID(groupID).
		SetName(name).
		SetDescription(name).
		SetPrice(price).
		SetValidityDays(days).
		SetValidityUnit("day").
		SetFeatures("Seven-day quota").
		SetProductName(name).
		SetForSale(true).
		SetSortOrder(10)
	if quota != nil {
		builder.SetSevenDayQuotaUsd(*quota)
	}
	return builder.SaveX(context.Background())
}

func (h *paymentFulfillmentPlanSnapshotHarness) createPaidSubscriptionOrder(t *testing.T, userID, planID, groupID int64, days int) *dbent.PaymentOrder {
	t.Helper()
	now := time.Now()
	return h.client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail(h.user.Email).
		SetUserName(h.user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetRechargeCode("").
		SetOutTradeNo(fmt.Sprintf("sub2_%d_%d", planID, now.UnixNano())).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(planID).
		SetSubscriptionGroupID(groupID).
		SetSubscriptionDays(days).
		SetStatus(OrderStatusPaid).
		SetExpiresAt(now.Add(time.Hour)).
		SetPaidAt(now).
		SetClientIP("127.0.0.1").
		SetSrcHost("example.test").
		SaveX(context.Background())
}

func TestPaymentFulfillmentSnapshotsSubscriptionPlanQuota(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	quota := 110.0
	plan := h.createSubscriptionPlan(t, group.ID, "Plus monthly", 399, 30, &quota)
	order := h.createPaidSubscriptionOrder(t, h.user.ID, plan.ID, group.ID, 30)

	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, order.ID))

	sub, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, sub.PlanID)
	require.Equal(t, int64(plan.ID), *sub.PlanID)
	require.NotNil(t, sub.PlanName)
	require.Equal(t, "Plus monthly", *sub.PlanName)
	require.NotNil(t, sub.SevenDayLimitUSD)
	require.InDelta(t, 110.0, *sub.SevenDayLimitUSD, 0.000001)
	require.NotNil(t, sub.WeeklyWindowStart)
	require.WithinDuration(t, time.Now(), *sub.WeeklyWindowStart, 2*time.Second)
	require.Zero(t, sub.WeeklyUsageUSD)
}

func TestPaymentFulfillmentAllowsHiddenPlanAfterCheckout(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	quota := 110.0
	plan := h.createSubscriptionPlan(t, group.ID, "Hidden after checkout", 399, 30, &quota)
	order := h.createPaidSubscriptionOrder(t, h.user.ID, plan.ID, group.ID, 30)
	h.client.SubscriptionPlan.UpdateOneID(plan.ID).SetForSale(false).SaveX(ctx)

	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, order.ID))

	sub, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, sub.PlanID)
	require.Equal(t, int64(plan.ID), *sub.PlanID)
	require.NotNil(t, sub.PlanName)
	require.Equal(t, "Hidden after checkout", *sub.PlanName)
	require.NotNil(t, sub.SevenDayLimitUSD)
	require.InDelta(t, quota, *sub.SevenDayLimitUSD, 0.000001)
}

func TestPaymentFulfillmentRestartsExpiredPlanSnapshotFromPurchaseCompletion(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	oldQuota := 50.0
	newQuota := 260.0
	oldPlanID := int64(1000)
	oldPlanName := "Old monthly"
	pro := h.createSubscriptionPlan(t, group.ID, "Pro monthly", 799, 30, &newQuota)
	oldStart := time.Now().AddDate(0, 0, -45)
	oldDailyWindow := oldStart.Add(12 * time.Hour)
	oldWeeklyWindow := oldStart.Add(24 * time.Hour)
	oldMonthlyWindow := oldStart.Add(48 * time.Hour)
	h.subscriptionRepo.seed(&UserSubscription{
		ID:                 77,
		UserID:             h.user.ID,
		GroupID:            group.ID,
		PlanID:             &oldPlanID,
		PlanName:           &oldPlanName,
		SevenDayLimitUSD:   &oldQuota,
		StartsAt:           oldStart,
		ExpiresAt:          oldStart.AddDate(0, 0, 30),
		Status:             SubscriptionStatusExpired,
		DailyWindowStart:   &oldDailyWindow,
		WeeklyWindowStart:  &oldWeeklyWindow,
		MonthlyWindowStart: &oldMonthlyWindow,
		DailyUsageUSD:      11,
		WeeklyUsageUSD:     22,
		MonthlyUsageUSD:    33,
		Notes:              "old",
	})

	order := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, order.ID))

	after, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusActive, after.Status)
	require.True(t, after.StartsAt.After(oldStart), "expired snapshot renewal should reset StartsAt to purchase completion time")
	require.WithinDuration(t, time.Now(), after.StartsAt, 2*time.Second)
	require.WithinDuration(t, after.StartsAt.AddDate(0, 0, 30), after.ExpiresAt, 2*time.Second)
	require.NotNil(t, after.DailyWindowStart)
	require.NotNil(t, after.WeeklyWindowStart)
	require.NotNil(t, after.MonthlyWindowStart)
	require.WithinDuration(t, after.StartsAt, *after.DailyWindowStart, time.Second)
	require.WithinDuration(t, after.StartsAt, *after.WeeklyWindowStart, time.Second)
	require.WithinDuration(t, after.StartsAt, *after.MonthlyWindowStart, time.Second)
	require.Zero(t, after.DailyUsageUSD)
	require.Zero(t, after.WeeklyUsageUSD)
	require.Zero(t, after.MonthlyUsageUSD)
	require.NotNil(t, after.PlanID)
	require.Equal(t, int64(pro.ID), *after.PlanID)
	require.NotNil(t, after.PlanName)
	require.Equal(t, "Pro monthly", *after.PlanName)
	require.NotNil(t, after.SevenDayLimitUSD)
	require.InDelta(t, newQuota, *after.SevenDayLimitUSD, 0.000001)
	require.Equal(t, "old\npayment order "+strconv.FormatInt(order.ID, 10), after.Notes)
}

func TestPaymentFulfillmentSwitchesActiveSubscriptionPlanAndExtendsExpiry(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	basicQuota := 50.0
	proQuota := 260.0
	basic := h.createSubscriptionPlan(t, group.ID, "Basic monthly", 179, 30, &basicQuota)
	pro := h.createSubscriptionPlan(t, group.ID, "Pro monthly", 799, 30, &proQuota)

	firstOrder := h.createPaidSubscriptionOrder(t, h.user.ID, basic.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, firstOrder.ID))

	before, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	originalExpiry := before.ExpiresAt
	require.NoError(t, h.subscriptionRepo.IncrementUsage(ctx, before.ID, 20))

	switchOrder := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, switchOrder.ID))

	after, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, after.PlanID)
	require.Equal(t, int64(pro.ID), *after.PlanID)
	require.NotNil(t, after.PlanName)
	require.Equal(t, "Pro monthly", *after.PlanName)
	require.NotNil(t, after.SevenDayLimitUSD)
	require.InDelta(t, 260.0, *after.SevenDayLimitUSD, 0.000001)
	require.Zero(t, after.WeeklyUsageUSD)
	require.NotNil(t, after.WeeklyWindowStart)
	require.NotNil(t, before.WeeklyWindowStart)
	require.True(t, after.WeeklyWindowStart.After(*before.WeeklyWindowStart) || after.WeeklyWindowStart.Equal(*before.WeeklyWindowStart))
	require.WithinDuration(t, originalExpiry.Add(30*24*time.Hour), after.ExpiresAt, time.Second)
}

func TestPaymentFulfillmentRenewingSameActivePlanExtendsExpiryWithoutResettingWeeklyUsage(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	basicQuota := 50.0
	proQuota := 260.0
	basic := h.createSubscriptionPlan(t, group.ID, "Basic monthly", 179, 30, &basicQuota)
	pro := h.createSubscriptionPlan(t, group.ID, "Pro monthly", 799, 30, &proQuota)

	firstOrder := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, firstOrder.ID))

	before, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	originalExpiry := before.ExpiresAt
	originalWeeklyWindow := *before.WeeklyWindowStart
	require.NoError(t, h.subscriptionRepo.IncrementUsage(ctx, before.ID, 20))

	basicPlanID := int64(basic.ID)
	basicPlanName := "Basic monthly"
	scheduledEffectiveAt := originalExpiry
	scheduledExpiresAt := originalExpiry.AddDate(0, 0, 30)
	require.NoError(t, h.subscriptionRepo.SchedulePlanChange(ctx, before.ID, &basicPlanID, &basicPlanName, &basicQuota, scheduledEffectiveAt, scheduledExpiresAt, nil, nil))

	renewOrder := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, renewOrder.ID))

	after, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, after.PlanID)
	require.Equal(t, int64(pro.ID), *after.PlanID)
	require.NotNil(t, after.PlanName)
	require.Equal(t, "Pro monthly", *after.PlanName)
	require.NotNil(t, after.SevenDayLimitUSD)
	require.InDelta(t, proQuota, *after.SevenDayLimitUSD, 0.000001)
	require.InDelta(t, 20.0, after.WeeklyUsageUSD, 0.000001)
	require.NotNil(t, after.WeeklyWindowStart)
	require.Equal(t, originalWeeklyWindow, *after.WeeklyWindowStart)
	require.WithinDuration(t, originalExpiry.Add(30*24*time.Hour), after.ExpiresAt, time.Second)
	require.Nil(t, after.ScheduledPlanID)
	require.Nil(t, after.ScheduledPlanName)
	require.Nil(t, after.ScheduledSevenDayLimitUSD)
	require.Nil(t, after.ScheduledPlanEffectiveAt)
	require.Nil(t, after.ScheduledExpiresAt)
	require.Nil(t, after.ScheduledOrderID)
}

func TestPaymentFulfillmentDowngradeSchedulesNextPeriodPlan(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	basicQuota := 50.0
	proQuota := 260.0
	basic := h.createSubscriptionPlan(t, group.ID, "Basic monthly", 179, 30, &basicQuota)
	pro := h.createSubscriptionPlan(t, group.ID, "Pro monthly", 799, 30, &proQuota)

	firstOrder := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, firstOrder.ID))

	before, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	originalExpiry := before.ExpiresAt
	originalWeeklyWindow := *before.WeeklyWindowStart
	require.NoError(t, h.subscriptionRepo.IncrementUsage(ctx, before.ID, 20))

	downgradeOrder := h.createPaidSubscriptionOrder(t, h.user.ID, basic.ID, group.ID, 30)
	require.NoError(t, h.paymentService.ExecuteSubscriptionFulfillment(ctx, downgradeOrder.ID))

	after, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, after.PlanID)
	require.Equal(t, int64(pro.ID), *after.PlanID)
	require.NotNil(t, after.PlanName)
	require.Equal(t, "Pro monthly", *after.PlanName)
	require.NotNil(t, after.SevenDayLimitUSD)
	require.InDelta(t, proQuota, *after.SevenDayLimitUSD, 0.000001)
	require.InDelta(t, 20.0, after.WeeklyUsageUSD, 0.000001)
	require.NotNil(t, after.WeeklyWindowStart)
	require.Equal(t, originalWeeklyWindow, *after.WeeklyWindowStart)
	require.WithinDuration(t, originalExpiry, after.ExpiresAt, time.Second)
	require.NotNil(t, after.ScheduledPlanID)
	require.Equal(t, int64(basic.ID), *after.ScheduledPlanID)
	require.NotNil(t, after.ScheduledPlanName)
	require.Equal(t, "Basic monthly", *after.ScheduledPlanName)
	require.NotNil(t, after.ScheduledSevenDayLimitUSD)
	require.InDelta(t, basicQuota, *after.ScheduledSevenDayLimitUSD, 0.000001)
	require.NotNil(t, after.ScheduledPlanEffectiveAt)
	require.WithinDuration(t, originalExpiry, *after.ScheduledPlanEffectiveAt, time.Second)
	require.NotNil(t, after.ScheduledExpiresAt)
	require.WithinDuration(t, originalExpiry.Add(30*24*time.Hour), *after.ScheduledExpiresAt, time.Second)
	require.NotNil(t, after.ScheduledOrderID)
	require.Equal(t, downgradeOrder.ID, *after.ScheduledOrderID)
}

func TestPaymentFulfillmentRetryAfterCompletionMarkerFailureDoesNotExtendSubscriptionTwice(t *testing.T) {
	ctx := context.Background()
	h := newPaymentFulfillmentPlanSnapshotHarness(t)

	group := h.createSubscriptionGroup(t, "LINX2 Subscription")
	quota := 110.0
	plan := h.createSubscriptionPlan(t, group.ID, "Plus monthly", 399, 30, &quota)
	order := h.createPaidSubscriptionOrder(t, h.user.ID, plan.ID, group.ID, 30)

	lease, err := h.paymentService.acquirePaymentFulfillmentLease(ctx, order)
	require.NoError(t, err)
	require.NotNil(t, lease)
	recharging := h.client.PaymentOrder.GetX(ctx, order.ID)
	require.NoError(t, h.paymentService.doSub(ctx, recharging, lease))

	afterFirst, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	firstExpiry := afterFirst.ExpiresAt

	h.client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("SUBSCRIPTION_SUCCESS")).ExecX(ctx)
	h.client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusFailed).ClearCompletedAt().SaveX(ctx)
	require.NoError(t, h.paymentService.RetryFulfillment(ctx, order.ID))

	afterRetry, err := h.subscriptionRepo.GetByUserIDAndGroupID(ctx, h.user.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, firstExpiry, afterRetry.ExpiresAt, "retry after completion marker failure must not extend the same payment order twice")

	reloadedOrder := h.client.PaymentOrder.GetX(ctx, order.ID)
	require.Equal(t, OrderStatusCompleted, reloadedOrder.Status)
}

type paymentFulfillmentTestProvider struct {
	key            string
	supportedTypes []payment.PaymentType
}

func (p paymentFulfillmentTestProvider) Name() string        { return p.key }
func (p paymentFulfillmentTestProvider) ProviderKey() string { return p.key }
func (p paymentFulfillmentTestProvider) SupportedTypes() []payment.PaymentType {
	return p.supportedTypes
}
func (p paymentFulfillmentTestProvider) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	panic("unexpected call")
}
func (p paymentFulfillmentTestProvider) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	panic("unexpected call")
}
func (p paymentFulfillmentTestProvider) VerifyNotification(ctx context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	panic("unexpected call")
}
func (p paymentFulfillmentTestProvider) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	panic("unexpected call")
}

func TestSubscriptionFulfillmentRejectsActiveNonSubscriptionGroupForPlanBackedOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, _ := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	payerID := createPaymentOrderSeatUser(t, ctx, client, "seat-fulfillment-standard-group@example.com")
	order := createPaidSeatSubscriptionOrder(t, ctx, client, payerID, plan.ID, plan.GroupID, 30)

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: plan.GroupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeStandard},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	svc := &PaymentService{
		entClient:       client,
		configService:   NewPaymentConfigService(client, nil, nil),
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
	}

	err := svc.ExecuteSubscriptionFulfillment(ctx, order.ID)

	require.Error(t, err)
	require.Equal(t, infraerrors.Reason(ErrGroupNotSubscriptionType), infraerrors.Reason(err))
	require.Zero(t, subRepo.createCalls)
	count, countErr := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(payerID), usersubscription.GroupIDEQ(plan.GroupID)).
		Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count)
}

func TestSubscriptionFulfillmentRejectsNewUserWhenPlanSeatLimitReached(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, existingUserID := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	payerID := createPaymentOrderSeatUser(t, ctx, client, "seat-fulfillment-new@example.com")
	createSeatSubscription(t, ctx, client, existingUserID, plan.GroupID, plan.ID, SubscriptionStatusActive, time.Now().Add(time.Hour))
	order := createPaidSeatSubscriptionOrder(t, ctx, client, payerID, plan.ID, plan.GroupID, 30)

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: plan.GroupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	svc := &PaymentService{
		entClient:       client,
		configService:   NewPaymentConfigService(client, nil, nil),
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, newSubscriptionUserSubRepoStub(), nil, nil, nil),
	}

	err := svc.ExecuteSubscriptionFulfillment(ctx, order.ID)

	require.Error(t, err)
	require.Equal(t, "PLAN_SEAT_LIMIT_REACHED", infraerrors.Reason(err))
	count, countErr := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(payerID), usersubscription.GroupIDEQ(plan.GroupID)).
		Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count)
}

func TestSubscriptionFulfillmentAllowsSameUserRenewalWhenPlanSeatLimitReached(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, userID := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	expiresAt := time.Now().Add(24 * time.Hour)
	createSeatSubscription(t, ctx, client, userID, plan.GroupID, plan.ID, SubscriptionStatusActive, expiresAt)
	order := createPaidSeatSubscriptionOrder(t, ctx, client, userID, plan.ID, plan.GroupID, 30)

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: plan.GroupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        99,
		UserID:    userID,
		GroupID:   plan.GroupID,
		PlanID:    &plan.ID,
		StartsAt:  time.Now().Add(-24 * time.Hour),
		ExpiresAt: expiresAt,
		Status:    SubscriptionStatusActive,
	})
	svc := &PaymentService{
		entClient:       client,
		configService:   NewPaymentConfigService(client, nil, nil),
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
	}

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	renewed, err := subRepo.GetByUserIDAndGroupID(ctx, userID, plan.GroupID)
	require.NoError(t, err)
	require.True(t, renewed.ExpiresAt.After(expiresAt))
	require.NotNil(t, renewed.PlanID)
	require.Equal(t, plan.ID, *renewed.PlanID)
}

func TestSubscriptionFulfillmentPlanSeatAssignmentUsesTransactionContext(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, userID := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	expiresAt := time.Now().Add(24 * time.Hour)
	createSeatSubscription(t, ctx, client, userID, plan.GroupID, plan.ID, SubscriptionStatusActive, expiresAt)
	order := createPaidSeatSubscriptionOrder(t, ctx, client, userID, plan.ID, plan.GroupID, 30)

	groupRepo := &paymentFulfillmentTxGuardGroupRepo{
		group: &Group{ID: plan.GroupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := &paymentFulfillmentTxGuardUserSubRepo{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	subRepo.seed(&UserSubscription{
		ID:        199,
		UserID:    userID,
		GroupID:   plan.GroupID,
		PlanID:    &plan.ID,
		StartsAt:  time.Now().Add(-24 * time.Hour),
		ExpiresAt: expiresAt,
		Status:    SubscriptionStatusActive,
	})
	svc := &PaymentService{
		entClient:       client,
		configService:   NewPaymentConfigService(client, nil, nil),
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
	}

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	require.Equal(t, 1, groupRepo.getByIDCalls, "plan-backed fulfillment should reuse the pre-transaction group validation")
	require.True(t, subRepo.extendExpirySawTx, "subscription renewal write should use the plan-seat transaction context")
	require.True(t, subRepo.updatePlanIDSawTx, "subscription plan update should use the plan-seat transaction context")
}

type paymentFulfillmentTxGuardGroupRepo struct {
	groupRepoNoop
	group        *Group
	getByIDCalls int
}

func (r *paymentFulfillmentTxGuardGroupRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	r.getByIDCalls++
	if dbent.TxFromContext(ctx) != nil {
		return nil, errors.New("group validation unexpectedly called inside transaction")
	}
	return r.group, nil
}

type paymentFulfillmentTxGuardUserSubRepo struct {
	*subscriptionUserSubRepoStub
	extendExpirySawTx bool
	updatePlanIDSawTx bool
}

func (r *paymentFulfillmentTxGuardUserSubRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	if dbent.TxFromContext(ctx) != nil {
		r.extendExpirySawTx = true
	}
	return r.subscriptionUserSubRepoStub.ExtendExpiry(ctx, subscriptionID, newExpiresAt)
}

func (r *paymentFulfillmentTxGuardUserSubRepo) UpdatePlanID(ctx context.Context, subscriptionID int64, planID int64) error {
	if dbent.TxFromContext(ctx) != nil {
		r.updatePlanIDSawTx = true
	}
	return r.subscriptionUserSubRepoStub.UpdatePlanID(ctx, subscriptionID, planID)
}

func createPaidSeatSubscriptionOrder(t *testing.T, ctx context.Context, client *dbent.Client, userID, planID, groupID int64, days int) *dbent.PaymentOrder {
	t.Helper()
	return client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("seat-fulfillment@example.com").
		SetUserName("seat-fulfillment-user").
		SetAmount(9.99).
		SetPayAmount(9.99).
		SetFeeRate(0).
		SetRechargeCode("SUB-SEAT-FULFILLMENT").
		SetOutTradeNo("sub2_seat_fulfillment").
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("trade-seat-fulfillment").
		SetOrderType(payment.OrderTypeSubscription).
		SetStatus(OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetPlanID(planID).
		SetSubscriptionGroupID(groupID).
		SetSubscriptionDays(days).
		SaveX(ctx)
}

type paymentFulfillmentAffiliateRepoStub struct {
	inviteeSummary  *AffiliateSummary
	inviterSummary  *AffiliateSummary
	settlementCalls []AffiliateSettlementInput
	resolved        bool
}

func (r *paymentFulfillmentAffiliateRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	switch {
	case r.inviteeSummary != nil && r.inviteeSummary.UserID == userID:
		cp := *r.inviteeSummary
		return &cp, nil
	case r.inviterSummary != nil && r.inviterSummary.UserID == userID:
		cp := *r.inviterSummary
		return &cp, nil
	default:
		return &AffiliateSummary{UserID: userID, AffCode: "AFFTEST", CreatedAt: time.Now().Add(-time.Hour)}, nil
	}
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAffiliateByCode(context.Context, string) (*AffiliateSummary, error) {
	panic("unexpected GetAffiliateByCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) BindInviter(context.Context, AffiliateBindInput) (AffiliateRewardResult, error) {
	panic("unexpected BindInviter call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ResolveFirstRecharge(_ context.Context, input AffiliateSettlementInput) (AffiliateRewardResult, error) {
	r.settlementCalls = append(r.settlementCalls, input)
	if r.resolved {
		return AffiliateRewardResult{}, nil
	}
	r.resolved = true
	if !input.Qualified || r.inviteeSummary == nil || r.inviteeSummary.InviterID == nil {
		return AffiliateRewardResult{Resolved: true}, nil
	}
	return AffiliateRewardResult{
		Resolved:        true,
		Qualified:       true,
		InviterID:       *r.inviteeSummary.InviterID,
		InviterReward:   input.InviterReward,
		InviteeReward:   input.InviteeReward,
		InviterRewarded: input.InviterReward > 0,
		InviteeRewarded: input.InviteeReward > 0,
	}, nil
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	return 0, nil
}

func (r *paymentFulfillmentAffiliateRepoStub) ThawFrozenQuota(context.Context, int64) (float64, error) {
	panic("unexpected ThawFrozenQuota call")
}

func (r *paymentFulfillmentAffiliateRepoStub) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListInvitees(context.Context, int64, int) ([]AffiliateInvitee, error) {
	panic("unexpected ListInvitees call")
}

func (r *paymentFulfillmentAffiliateRepoStub) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

func (r *paymentFulfillmentAffiliateRepoStub) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListUsersWithCustomSettings(context.Context, AffiliateAdminFilter) ([]AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateInviteRecords(context.Context, AffiliateRecordFilter) ([]AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateRebateRecords(context.Context, AffiliateRecordFilter) ([]AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateTransferRecords(context.Context, AffiliateRecordFilter) ([]AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAffiliateUserOverview(context.Context, int64) (*AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

type paymentFulfillmentSettingRepoStub struct {
	values map[string]string
}

func (s *paymentFulfillmentSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (s *paymentFulfillmentSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.values == nil {
		return "", ErrSettingNotFound
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *paymentFulfillmentSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *paymentFulfillmentSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.values[key]
	}
	return out, nil
}

func (s *paymentFulfillmentSettingRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func (s *paymentFulfillmentSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}

func (s *paymentFulfillmentSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func ensurePaymentAuditOrderActionUniqueIndex(t *testing.T, ctx context.Context, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action)")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// resolveRedeemAction — pure idempotency decision logic
// ---------------------------------------------------------------------------

func TestResolveRedeemAction_CodeNotFound(t *testing.T) {
	t.Parallel()
	action := resolveRedeemAction(nil, nil)
	assert.Equal(t, redeemActionCreate, action, "nil code with nil error should create")
}

func TestResolveRedeemAction_LookupError(t *testing.T) {
	t.Parallel()
	action := resolveRedeemAction(nil, errors.New("db connection lost"))
	assert.Equal(t, redeemActionCreate, action, "lookup error should fall back to create")
}

func TestResolveRedeemAction_LookupErrorWithNonNilCode(t *testing.T) {
	t.Parallel()
	// Edge case: both code and error are non-nil (shouldn't happen in practice,
	// but the function should still treat error as authoritative)
	code := &RedeemCode{Status: StatusUnused}
	action := resolveRedeemAction(code, errors.New("partial error"))
	assert.Equal(t, redeemActionCreate, action, "non-nil error should always result in create regardless of code")
}

func TestResolveRedeemAction_CodeExistsAndUsed(t *testing.T) {
	t.Parallel()
	code := &RedeemCode{
		Code:   "test-code-123",
		Status: StatusUsed,
		Type:   RedeemTypeBalance,
		Value:  10.0,
	}
	action := resolveRedeemAction(code, nil)
	assert.Equal(t, redeemActionSkipCompleted, action, "used code should skip to completed")
}

func TestResolveRedeemAction_CodeExistsAndUnused(t *testing.T) {
	t.Parallel()
	code := &RedeemCode{
		Code:   "test-code-456",
		Status: StatusUnused,
		Type:   RedeemTypeBalance,
		Value:  25.0,
	}
	action := resolveRedeemAction(code, nil)
	assert.Equal(t, redeemActionRedeem, action, "unused code should skip creation and proceed to redeem")
}

func TestResolveRedeemAction_CodeExistsWithExpiredStatus(t *testing.T) {
	t.Parallel()
	// A code with a non-standard status (neither "unused" nor "used")
	// should NOT be treated as used, so it falls through to redeemActionRedeem.
	code := &RedeemCode{
		Code:   "expired-code",
		Status: StatusExpired,
	}
	action := resolveRedeemAction(code, nil)
	assert.Equal(t, redeemActionRedeem, action, "expired-status code is not IsUsed(), should redeem")
}

// ---------------------------------------------------------------------------
// Table-driven comprehensive test
// ---------------------------------------------------------------------------

func TestResolveRedeemAction_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     *RedeemCode
		err      error
		expected redeemAction
	}{
		{
			name:     "nil code, nil error — first run",
			code:     nil,
			err:      nil,
			expected: redeemActionCreate,
		},
		{
			name:     "nil code, lookup error — treat as not found",
			code:     nil,
			err:      ErrRedeemCodeNotFound,
			expected: redeemActionCreate,
		},
		{
			name:     "nil code, generic DB error — treat as not found",
			code:     nil,
			err:      errors.New("connection refused"),
			expected: redeemActionCreate,
		},
		{
			name:     "code exists, used — previous run completed redeem",
			code:     &RedeemCode{Status: StatusUsed},
			err:      nil,
			expected: redeemActionSkipCompleted,
		},
		{
			name:     "code exists, unused — previous run created code but crashed before redeem",
			code:     &RedeemCode{Status: StatusUnused},
			err:      nil,
			expected: redeemActionRedeem,
		},
		{
			name:     "code exists but error also set — error takes precedence",
			code:     &RedeemCode{Status: StatusUsed},
			err:      errors.New("unexpected"),
			expected: redeemActionCreate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveRedeemAction(tt.code, tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ---------------------------------------------------------------------------
// redeemAction enum value sanity
// ---------------------------------------------------------------------------

func TestRedeemAction_DistinctValues(t *testing.T) {
	t.Parallel()
	// Ensure the three actions have distinct values (iota correctness)
	assert.NotEqual(t, redeemActionCreate, redeemActionRedeem)
	assert.NotEqual(t, redeemActionCreate, redeemActionSkipCompleted)
	assert.NotEqual(t, redeemActionRedeem, redeemActionSkipCompleted)
}

// ---------------------------------------------------------------------------
// RedeemCode.IsUsed / CanUse interaction with resolveRedeemAction
// ---------------------------------------------------------------------------

func TestResolveRedeemAction_IsUsedCanUseConsistency(t *testing.T) {
	t.Parallel()

	usedCode := &RedeemCode{Status: StatusUsed}
	unusedCode := &RedeemCode{Status: StatusUnused}

	// Verify our decision function is consistent with the domain model methods
	assert.True(t, usedCode.IsUsed())
	assert.False(t, usedCode.CanUse())
	assert.Equal(t, redeemActionSkipCompleted, resolveRedeemAction(usedCode, nil))

	assert.False(t, unusedCode.IsUsed())
	assert.True(t, unusedCode.CanUse())
	assert.Equal(t, redeemActionRedeem, resolveRedeemAction(unusedCode, nil))
}

func TestExpectedNotificationProviderKeyPrefersOrderInstanceProvider(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymentFulfillmentTestProvider{
		key:            payment.TypeAlipay,
		supportedTypes: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		expectedNotificationProviderKey(registry, payment.TypeAlipay, "", payment.TypeEasyPay),
	)
}

func TestExpectedNotificationProviderKeyUsesRegistryMappingForLegacyOrders(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymentFulfillmentTestProvider{
		key:            payment.TypeEasyPay,
		supportedTypes: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		expectedNotificationProviderKey(registry, payment.TypeAlipay, "", ""),
	)
}

func TestExpectedNotificationProviderKeyFallsBackToPaymentType(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		payment.TypeWxpay,
		expectedNotificationProviderKey(nil, payment.TypeWxpay, "", ""),
	)
}

func TestExpectedNotificationProviderKeyPrefersOrderSnapshotProviderKey(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymentFulfillmentTestProvider{
		key:            payment.TypeAlipay,
		supportedTypes: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		expectedNotificationProviderKey(registry, payment.TypeAlipay, payment.TypeEasyPay, ""),
	)
}

func TestExpectedNotificationProviderKeyForOrderUsesSnapshotProviderKey(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymentFulfillmentTestProvider{
		key:            payment.TypeAlipay,
		supportedTypes: []payment.PaymentType{payment.TypeAlipay},
	})

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version": 1,
			"provider_key":   payment.TypeEasyPay,
		},
	}

	assert.Equal(t,
		payment.TypeEasyPay,
		expectedNotificationProviderKeyForOrder(registry, order, ""),
	)
}

func TestValidateProviderNotificationMetadataRejectsWxpaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":  1,
			"merchant_app_id": "wx-app-expected",
			"merchant_id":     "mch-expected",
			"currency":        "CNY",
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeWxpay, map[string]string{
		"appid":       "wx-app-other",
		"mchid":       "mch-expected",
		"currency":    "CNY",
		"trade_state": "SUCCESS",
	})
	assert.ErrorContains(t, err, "wxpay appid mismatch")
}

func TestValidateProviderNotificationMetadataAllowsLegacyOrdersWithoutSnapshotFields(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":       1,
			"provider_instance_id": "9",
			"provider_key":         payment.TypeWxpay,
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeWxpay, map[string]string{
		"appid":       "wx-app-runtime",
		"mchid":       "mch-runtime",
		"currency":    "CNY",
		"trade_state": "SUCCESS",
	})
	assert.NoError(t, err)
}

func TestParseLegacyPaymentOrderID(t *testing.T) {
	t.Parallel()

	oid, ok := parseLegacyPaymentOrderID("sub2_42", &dbent.NotFoundError{})
	assert.True(t, ok)
	assert.EqualValues(t, 42, oid)

	_, ok = parseLegacyPaymentOrderID("42", &dbent.NotFoundError{})
	assert.False(t, ok)

	_, ok = parseLegacyPaymentOrderID("sub2_42", errors.New("db down"))
	assert.False(t, ok)
}

func TestIsValidProviderAmount(t *testing.T) {
	t.Parallel()

	assert.True(t, isValidProviderAmount(0.01))
	assert.False(t, isValidProviderAmount(0))
	assert.False(t, isValidProviderAmount(-1))
	assert.False(t, isValidProviderAmount(math.NaN()))
	assert.False(t, isValidProviderAmount(math.Inf(1)))
}

func TestValidateProviderNotificationMetadataRejectsAlipaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version":  2,
			"merchant_app_id": "alipay-app-expected",
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeAlipay, map[string]string{
		"app_id": "alipay-app-other",
	})
	assert.ErrorContains(t, err, "alipay app_id mismatch")
}

func TestValidateProviderNotificationMetadataRejectsEasyPaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"merchant_id":    "pid-expected",
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeEasyPay, map[string]string{
		"pid": "pid-other",
	})
	assert.ErrorContains(t, err, "easypay pid mismatch")
}

func TestValidateProviderNotificationMetadataRejectsAirwallexSnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeAirwallex,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"merchant_id":    "acct_expected",
			"currency":       "CNY",
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeAirwallex, map[string]string{
		"account_id": "acct_other",
		"currency":   "CNY",
		"status":     "SUCCEEDED",
	})
	assert.ErrorContains(t, err, "airwallex account_id mismatch")

	err = validateProviderNotificationMetadata(order, payment.TypeAirwallex, map[string]string{
		"account_id": "acct_expected",
		"currency":   "USD",
		"status":     "SUCCEEDED",
	})
	assert.ErrorContains(t, err, "airwallex currency mismatch")
}

func TestValidateProviderNotificationMetadataRejectsStripeCurrencyMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeStripe,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"currency":       "HKD",
		},
	}

	err := validateProviderNotificationMetadata(order, payment.TypeStripe, map[string]string{
		"currency": "USD",
	})
	assert.ErrorContains(t, err, "stripe currency mismatch")
}

func TestPaymentAmountToleranceForThreeDecimalCurrency(t *testing.T) {
	t.Parallel()

	assert.Equal(t, amountToleranceCNY, paymentAmountToleranceForCurrency("CNY"))
	assert.Equal(t, amountToleranceCNY, paymentAmountToleranceForCurrency("JPY"))
	assert.InDelta(t, 0.0005, paymentAmountToleranceForCurrency("KWD"), 1e-12)
}

func TestRetryFulfillmentRejectsFreshRechargingLease(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusRecharging, time.Now())

	svc := &PaymentService{entClient: client}
	err := svc.RetryFulfillment(ctx, order.ID)
	require.Error(t, err)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, OrderStatusRecharging, reloaded.Status)
}

func TestAlreadyProcessedRecoversStaleRechargingLease(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(
		t,
		ctx,
		client,
		OrderStatusRecharging,
		time.Now().Add(-paymentFulfillmentLeaseDuration-time.Minute),
	)
	_, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_SUCCESS").
		SetDetail(`{"groupID":7,"validityDays":30}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 7, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	svc := &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, userSubRepoNoop{}, nil, nil, nil),
	}

	require.NoError(t, svc.alreadyProcessed(ctx, order))
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

func TestFulfillmentLeaseVersionRejectsStaleWorker(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	staleAt := time.Now().Add(-paymentFulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusRecharging, staleAt)
	svc := &PaymentService{entClient: client}

	firstLease, err := svc.acquirePaymentFulfillmentLease(ctx, order)
	require.NoError(t, err)
	require.NotNil(t, firstLease)

	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetUpdatedAt(staleAt).Save(ctx)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	staleOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	secondLease, err := svc.acquirePaymentFulfillmentLease(ctx, staleOrder)
	require.NoError(t, err)
	require.NotNil(t, secondLease)
	require.False(t, firstLease.version.Equal(secondLease.version))

	err = svc.markCompleted(ctx, order, firstLease, "SUBSCRIPTION_SUCCESS")
	require.Error(t, err)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	svc.markFailed(ctx, order.ID, firstLease, errors.New("stale worker failure"))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRecharging, reloaded.Status)
	require.NoError(t, svc.markCompleted(ctx, order, secondLease, "SUBSCRIPTION_SUCCESS"))
}

func TestExecuteBalanceFulfillmentRecoversAfterRedeemWithoutCreditingAgain(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	staleAt := time.Now().Add(-paymentFulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusRecharging, staleAt)
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		SetUpdatedAt(staleAt).
		Save(ctx)
	require.NoError(t, err)

	redeemRepo := &redeemCodeRepoStub{codesByCode: map[string]*RedeemCode{
		order.RechargeCode: {
			ID:     101,
			Code:   order.RechargeCode,
			Type:   RedeemTypeBalance,
			Value:  order.Amount,
			Status: StatusUsed,
		},
	}}
	svc := &PaymentService{
		entClient:     client,
		redeemService: &RedeemService{redeemRepo: redeemRepo},
	}

	require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
	require.Empty(t, redeemRepo.useCalls, "an already-used order code must not be redeemed again")
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

func TestExecuteSubscriptionFulfillmentRecoversCommittedAssignmentWithoutExtendingAgain(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	staleAt := time.Now().Add(-paymentFulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusRecharging, staleAt)

	expiresAt := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        99,
		UserID:    order.UserID,
		GroupID:   *order.SubscriptionGroupID,
		StartsAt:  time.Now().Add(-time.Hour),
		ExpiresAt: expiresAt,
		Status:    SubscriptionStatusActive,
		Notes:     "manual note\n" + paymentSubscriptionOrderNote(order.ID) + "\nretained note",
	})
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 7, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	svc := &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
	}

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	assertPaymentSubscriptionExpiry(t, subRepo, order, expiresAt)

	completionAuditCount, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_SUCCESS"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, completionAuditCount)

	// Simulate another stale recovery attempt after completion. The durable audit
	// must make replay a no-op for the subscription entitlement.
	_, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusRecharging).
		SetUpdatedAt(staleAt).
		ClearCompletedAt().
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	assertPaymentSubscriptionExpiry(t, subRepo, order, expiresAt)

	completionAuditCount, err = client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_SUCCESS"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, completionAuditCount)
}

func TestHasPaymentSubscriptionOrderNoteRequiresIndependentExactLine(t *testing.T) {
	t.Parallel()
	require.True(t, hasPaymentSubscriptionOrderNote("before\r\npayment order 42\r\nafter", "payment order 42"))
	require.False(t, hasPaymentSubscriptionOrderNote("payment order 420", "payment order 42"))
	require.False(t, hasPaymentSubscriptionOrderNote("prefix payment order 42 suffix", "payment order 42"))
}

func createPaymentFulfillmentSubscriptionOrder(
	t *testing.T,
	ctx context.Context,
	client *dbent.Client,
	status string,
	updatedAt time.Time,
) *dbent.PaymentOrder {
	t.Helper()
	user, err := client.User.Create().
		SetEmail("fulfillment-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com").
		SetPasswordHash("hash").
		SetUsername("payment-fulfillment-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(80).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-" + strconv.FormatInt(time.Now().UnixNano(), 10)).
		SetOutTradeNo("sub2_fulfillment_" + strconv.FormatInt(time.Now().UnixNano(), 10)).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-fulfillment").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(100).
		SetSubscriptionGroupID(7).
		SetSubscriptionDays(30).
		SetStatus(status).
		SetPaidAt(time.Now().Add(-time.Hour)).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetUpdatedAt(updatedAt).
		Save(ctx)
	require.NoError(t, err)
	return order
}

func assertPaymentSubscriptionExpiry(t *testing.T, repo *subscriptionUserSubRepoStub, order *dbent.PaymentOrder, expected time.Time) {
	t.Helper()
	sub, err := repo.GetByUserIDAndGroupID(context.Background(), order.UserID, *order.SubscriptionGroupID)
	require.NoError(t, err)
	require.True(t, sub.ExpiresAt.Equal(expected), "subscription expiry changed from %s to %s", expected, sub.ExpiresAt)
}

// NOTE: The old percentage-rebate subscription tests are intentionally not
// restored. This branch uses the fixed-amount first-recharge reward model.
func newFirstRechargeRewardEnv(t *testing.T, inviterID int64) (*PaymentService, *dbent.Client, *paymentFulfillmentAffiliateRepoStub, *mockUserRepo, int64) {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	invitee := client.User.Create().
		SetEmail("invitee@example.com").
		SetPasswordHash("hash").
		SetUsername("invitee-user").
		SaveX(ctx)
	inviteeID := invitee.ID

	affiliateRepo := &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &AffiliateSummary{
			UserID:    inviteeID,
			AffCode:   "INVITEE",
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
		inviterSummary: &AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
	}
	settingSvc := NewSettingService(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		SettingKeyAffiliateEnabled:                "true",
		SettingKeyAffiliateFirstRechargeThreshold: "20",
		SettingKeyAffiliateInviterReward:          "5",
		SettingKeyAffiliateInviteeReward:          "5",
		SettingKeyAffiliateRebateFreezeHours:      "0",
	}}, nil)
	userRepo := &mockUserRepo{}
	affiliateSvc := NewAffiliateService(affiliateRepo, settingSvc, nil, nil, userRepo)

	svc := &PaymentService{
		entClient:        client,
		affiliateService: affiliateSvc,
	}
	return svc, client, affiliateRepo, userRepo, inviteeID
}

func createFirstRechargeBalanceOrder(t *testing.T, ctx context.Context, client *dbent.Client, userID int64, amount float64, status, code, outTradeNo string) *dbent.PaymentOrder {
	t.Helper()
	order, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("invitee@example.com").
		SetUserName("invitee-user").
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode(code).
		SetOutTradeNo(outTradeNo).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(outTradeNo + "-trade").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)
	return order
}

func createFirstRechargeSubscriptionOrder(t *testing.T, ctx context.Context, client *dbent.Client, userID, groupID int64, amount float64, code, outTradeNo string) *dbent.PaymentOrder {
	t.Helper()
	order, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("invitee@example.com").
		SetUserName("invitee-user").
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode(code).
		SetOutTradeNo(outTradeNo).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(outTradeNo + "-trade").
		SetOrderType(payment.OrderTypeSubscription).
		SetSubscriptionGroupID(groupID).
		SetSubscriptionDays(30).
		SetStatus(OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)
	return order
}

func TestExecuteSubscriptionFulfillmentAppliesFirstRechargeReward(t *testing.T) {
	ctx := context.Background()
	const (
		inviterID = int64(9001)
		groupID   = int64(7)
	)
	svc, client, affiliateRepo, userRepo, inviteeID := newFirstRechargeRewardEnv(t, inviterID)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: groupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	svc.groupRepo = groupRepo
	svc.subscriptionSvc = NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)

	_ = userRepo

	order := createFirstRechargeSubscriptionOrder(t, ctx, client, inviteeID, groupID, 9.99, "PAY-SUB-FIRST", "sub2_subscription_first_reward")

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.Equal(t, 1, subRepo.createCalls)

	require.Len(t, affiliateRepo.settlementCalls, 1)
	require.Equal(t, inviteeID, affiliateRepo.settlementCalls[0].InviteeUserID)
	require.Equal(t, order.ID, affiliateRepo.settlementCalls[0].SourceOrderID)
	require.True(t, affiliateRepo.settlementCalls[0].Qualified)
	require.Equal(t, 5.0, affiliateRepo.settlementCalls[0].InviterReward)
	require.Equal(t, 5.0, affiliateRepo.settlementCalls[0].InviteeReward)

	applied, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"baseAmount":9.99`)
	require.Contains(t, applied.Detail, `"inviterReward":5`)
	require.Contains(t, applied.Detail, `"inviteeReward":5`)
	require.Contains(t, applied.Detail, `"inviter_rewarded":true`)
	require.Contains(t, applied.Detail, `"invitee_rewarded":true`)
	require.Contains(t, applied.Detail, `"inviter_limit_reached":false`)
	require.Contains(t, applied.Detail, `"isSubscription":true`)
}

// TestApplyAffiliateRebate_FirstRechargeApplied verifies that the invitee's
// first qualifying balance recharge pays both parties and records an
// AFFILIATE_REBATE_APPLIED audit log.
func TestApplyAffiliateRebate_FirstRechargeApplied(t *testing.T) {
	ctx := context.Background()
	const inviterID = int64(6001)
	svc, client, affiliateRepo, userRepo, inviteeID := newFirstRechargeRewardEnv(t, inviterID)

	_ = userRepo

	order := createFirstRechargeBalanceOrder(t, ctx, client, inviteeID, 20, OrderStatusRecharging, "PAY-FIRST-APPLIED", "sub2_first_applied")

	require.NoError(t, svc.applyAffiliateRebateForOrder(ctx, order))

	require.Len(t, affiliateRepo.settlementCalls, 1)
	require.Equal(t, inviteeID, affiliateRepo.settlementCalls[0].InviteeUserID)
	require.Equal(t, order.ID, affiliateRepo.settlementCalls[0].SourceOrderID)
	require.True(t, affiliateRepo.settlementCalls[0].Qualified)

	applied, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"inviterReward":5`)
	require.Contains(t, applied.Detail, `"inviteeReward":5`)
	require.Contains(t, applied.Detail, `"inviter_rewarded":true`)
	require.Contains(t, applied.Detail, `"invitee_rewarded":true`)
}

// TestApplyAffiliateRebate_SecondRechargeSkipped verifies that once the invitee
// already has a resolved affiliate relationship, a subsequent recharge is
// skipped by the repository state machine.
func TestApplyAffiliateRebate_SecondRechargeSkipped(t *testing.T) {
	ctx := context.Background()
	const inviterID = int64(6002)
	svc, client, affiliateRepo, userRepo, inviteeID := newFirstRechargeRewardEnv(t, inviterID)
	affiliateRepo.resolved = true

	userRepo.updateBalanceFn = func(context.Context, int64, float64) error {
		t.Fatalf("UpdateBalance should not be called for a skipped (non-first) recharge")
		return nil
	}

	// Prior COMPLETED recharge → makes the next order non-first.
	createFirstRechargeBalanceOrder(t, ctx, client, inviteeID, 50, OrderStatusCompleted, "PAY-HIST", "sub2_hist")
	secondOrder := createFirstRechargeBalanceOrder(t, ctx, client, inviteeID, 50, OrderStatusRecharging, "PAY-SECOND", "sub2_second")

	require.NoError(t, svc.applyAffiliateRebateForOrder(ctx, secondOrder))

	require.Len(t, affiliateRepo.settlementCalls, 1)

	skipped, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(secondOrder.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_SKIPPED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, skipped.Detail, `"resolved":false`)
}

var _ AffiliateRepository = (*paymentFulfillmentAffiliateRepoStub)(nil)
var _ SettingRepository = (*paymentFulfillmentSettingRepoStub)(nil)

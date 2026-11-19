//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type alipayRefundQueryForTest struct {
	refundQueryProviderTestDouble
}

func (*alipayRefundQueryForTest) ProviderKey() string { return payment.TypeAlipay }

func TestAlipayRefundRetryWithUniqueOrderActions(t *testing.T) {
	ctx := t.Context()
	client := newUniqueRefundAuditTestClient(t)
	order := createCompletedRefundableOrderForTest(t, ctx, client, "retry-with-audit")
	repo := &mockUserRepo{
		getByIDUser: &User{ID: order.UserID, Balance: 35},
		deductBalanceFn: func(ctx context.Context, userID int64, amount float64) error {
			tx := dbent.TxFromContext(ctx)
			require.NotNil(t, tx)
			_, err := tx.Client().User.UpdateOneID(userID).AddBalance(-amount).Save(ctx)
			return err
		},
	}
	prov := &pendingRefundProviderTestDouble{queryStatus: payment.ProviderStatusFailed}
	t.Cleanup(replacePaymentProviderFactoryForTest(t, prov))
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: repo}

	attempt := func(reason string) *dbent.PaymentAuditLog {
		plan, early, err := svc.PrepareRefund(ctx, order.ID, 0, reason, false, true)
		require.NoError(t, err)
		require.Nil(t, early)
		result, err := svc.ExecuteRefund(ctx, plan)
		require.NoError(t, err)
		require.Contains(t, result.Warning, "pending confirmation")
		account, err := client.User.Get(ctx, order.UserID)
		require.NoError(t, err)
		require.Zero(t, account.Balance)
		audit, err := pendingRefundAuditQuery(client, order.ID).Only(ctx)
		require.NoError(t, err)
		require.Contains(t, audit.Detail, reason)
		return audit
	}

	first := attempt("first request")
	_, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	oldFailure, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ("REFUND_FAILED"),
	).Only(ctx)
	require.NoError(t, err)

	second := attempt("second request")
	require.NotEqual(t, first.ID, second.ID)
	_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, 2, prov.refundCalls)
	account, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 35.0, account.Balance)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundFailed, stored.Status)
	currentFailure, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ("REFUND_FAILED"),
	).Only(ctx)
	require.NoError(t, err)
	require.NotEqual(t, oldFailure.ID, currentFailure.ID)
	for _, oldAudit := range []*dbent.PaymentAuditLog{first, oldFailure} {
		archived, err := client.PaymentAuditLog.Get(ctx, oldAudit.ID)
		require.NoError(t, err)
		require.NotEqual(t, oldAudit.Action, archived.Action)
		require.Equal(t, oldAudit.Detail, archived.Detail)
		require.Equal(t, oldAudit.CreatedAt, archived.CreatedAt)
	}
}

func TestAlipayPendingRefundCanFailWithEarlierUniqueFailureAction(t *testing.T) {
	ctx := t.Context()
	client := newUniqueRefundAuditTestClient(t)
	order := makeAlipayRefundPendingForTest(t, client, "pending-refund-prior-failure", `{"refundID":"rf_test","deductionHeld":true,"deductionType":"balance","balanceDeducted":40}`)
	_, err := client.User.UpdateOneID(order.UserID).SetBalance(0).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, createPaymentAuditLogTx(ctx, client, order.ID, "REFUND_FAILED", map[string]any{"detail": "previous failure"}))

	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}}
	t.Cleanup(replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
	}}))

	_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundFailed, stored.Status)
	account, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 40.0, account.Balance)
}

func newUniqueRefundAuditTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	client := newPaymentConfigServiceTestClient(t)
	// Match migration 131, which is absent from the generated Ent schema.
	_, err := client.ExecContext(t.Context(), "CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq ON payment_audit_logs (order_id, action)")
	require.NoError(t, err)
	return client
}

func TestAlipayFailedPendingRefundReturnsCashWithoutIncreasingRechargeTotal(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := makeAlipayRefundPendingForTest(t, client, "alipay-held-balance", `{"refundID":"rf_test","deductionHeld":true,"deductionType":"balance","balanceDeducted":40}`)
	_, err := client.User.UpdateOneID(order.UserID).SetBalance(-5).SetTotalRecharged(70).Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}}
	restoreFactory := replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
	}})
	defer restoreFactory()

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.False(t, result.Success)
	u, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 35.0, u.Balance)
	require.Equal(t, 70.0, u.TotalRecharged)

	_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err))
	u, err = client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 35.0, u.Balance)
}

func TestAlipayFailedPendingRefundRollsBackCashReleaseWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := makeAlipayRefundPendingForTest(t, client, "alipay-held-audit", `{"refundID":"rf_test","deductionHeld":true,"deductionType":"balance","balanceDeducted":40}`)
	_, err := client.User.UpdateOneID(order.UserID).SetBalance(0).Save(ctx)
	require.NoError(t, err)

	failAudit := true
	client.PaymentAuditLog.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
			if action, _ := mutation.Field(paymentauditlog.FieldAction); failAudit && action == "REFUND_FAILED" {
				return nil, errors.New("test audit write failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}}
	restoreFactory := replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
	}})
	defer restoreFactory()

	_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.ErrorContains(t, err, "test audit write failure")
	user, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Zero(t, user.Balance)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, stored.Status)

	failAudit = false
	_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	user, err = client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 40.0, user.Balance)
}

func TestAlipayFailedPendingRefundRestoresRevokedSubscription(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replaced bool
	}{
		{name: "restore original row"},
		{name: "preserve a replacement for manual reconciliation", replaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := makeAlipayRefundPendingForTest(t, client, "alipay-held-subscription", `{"refundID":"rf_test","deductionHeld":true,"deductionType":"subscription","subDaysDeducted":7,"subscriptionID":5,"subscriptionRevoked":true}`)
			order, err := client.PaymentOrder.UpdateOneID(order.ID).SetOrderType(payment.OrderTypeSubscription).Save(ctx)
			require.NoError(t, err)
			now := time.Now()
			expires := now.Add(6 * time.Hour)
			subRepo := &restoreUserSubRepoStub{
				sub:           &UserSubscription{ID: 5, UserID: order.UserID, Status: SubscriptionStatusActive, ExpiresAt: expires, DeletedAt: &now},
				existsGeneric: tc.replaced,
			}
			svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, subscriptionSvc: NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)}
			restoreFactory := replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
			}})
			defer restoreFactory()

			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.False(t, result.Success)
			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefundFailed, reloaded.Status)
			if tc.replaced {
				require.NotNil(t, subRepo.sub.DeletedAt)
				require.Zero(t, subRepo.restoreCalls)
				require.Contains(t, result.Warning, "manual reconciliation")
				require.Equal(t, 1, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED"))
				_, _, err = svc.PrepareRefund(ctx, order.ID, 100, "retry", true, true)
				require.Equal(t, "REFUND_RECONCILIATION_REQUIRED", infraerrors.Reason(err))
			} else {
				require.Nil(t, subRepo.sub.DeletedAt)
				require.Equal(t, 1, subRepo.restoreCalls)
			}
			require.Equal(t, expires, subRepo.sub.ExpiresAt)
		})
	}
}

func makeAlipayRefundPendingForTest(t *testing.T, client *dbent.Client, suffix, detail string) *dbent.PaymentOrder {
	t.Helper()
	ctx := context.Background()
	order := createPendingRefundOrderForTest(t, ctx, client, suffix)
	instanceID, err := strconv.ParseInt(*order.ProviderInstanceID, 10, 64)
	require.NoError(t, err)
	_, err = client.PaymentProviderInstance.UpdateOneID(instanceID).SetProviderKey(payment.TypeAlipay).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Update().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).SetDetail(detail).Save(ctx)
	require.NoError(t, err)
	return order
}

func countAlipayRefundAuditForTest(t *testing.T, client *dbent.Client, orderID int64, action string) int {
	t.Helper()
	count, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)), paymentauditlog.ActionEQ(action)).Count(context.Background())
	require.NoError(t, err)
	return count
}

type concurrentAlipayRefundQueryForTest struct {
	alipayRefundQueryForTest
	beforeAnswer func(context.Context)
}

func (p *concurrentAlipayRefundQueryForTest) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	p.beforeAnswer(ctx)
	return p.alipayRefundQueryForTest.QueryRefund(ctx, req)
}

func TestAlipayRefundQueryCannotFinalizeAReplacementAttempt(t *testing.T) {
	ctx := context.Background()
	client := newUniqueRefundAuditTestClient(t)
	order := makeAlipayRefundPendingForTest(t, client, "alipay-stale-refund-query", `{"refundID":"old-refund","deductionHeld":true,"deductionType":"none"}`)
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}}
	prov := &concurrentAlipayRefundQueryForTest{
		alipayRefundQueryForTest: alipayRefundQueryForTest{refundQueryProviderTestDouble{
			refundResponse: &payment.RefundResponse{RefundID: "old-refund", Status: payment.ProviderStatusSuccess},
		}},
		beforeAnswer: func(ctx context.Context) {
			// Another admin resolves the old attempt and starts a new one while
			// this query is awaiting the gateway reply.
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefundFailed).Save(ctx)
			require.NoError(t, err)
			replacement, early, err := svc.PrepareRefund(ctx, order.ID, 20, "new request", false, false)
			require.NoError(t, err)
			require.Nil(t, early)
			replacement.refundID = "new-refund"
			_, err = svc.holdAlipayRefund(ctx, replacement)
			require.NoError(t, err)
		},
	}
	restoreFactory := replacePaymentProviderFactoryForTest(t, prov)
	defer restoreFactory()

	_, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, stored.Status)
	require.Equal(t, 20.0, stored.RefundAmount)
}

func TestAlipayRefundQuerySnapshotsOrderAndAttemptBeforeProviderLookup(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "held"
		if legacy {
			name = "pre-audit legacy"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			client := newPaymentConfigServiceTestClient(t)
			order := makeAlipayRefundPendingForTest(t, client, "alipay-replaced-during-provider-lookup", `{"refundID":"old-refund","deductionHeld":true,"deductionType":"none"}`)
			if legacy {
				_, err := pendingRefundAuditQuery(client, order.ID).OnlyID(ctx)
				require.NoError(t, err)
				_, err = client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10))).Exec(ctx)
				require.NoError(t, err)
			}
			prov := &alipayRefundQueryForTest{refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess},
			}}
			original := createPaymentProviderFromInstance
			t.Cleanup(func() { createPaymentProviderFromInstance = original })
			createPaymentProviderFromInstance = func(string, string, map[string]string) (payment.Provider, error) {
				tx, err := client.Tx(ctx)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback() }()
				_, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRefundAmount(20).SetRefundReason("replacement").SetForceRefund(true).Save(ctx)
				require.NoError(t, err)
				err = createPaymentAuditLogTx(ctx, tx.Client(), order.ID, "REFUND_PENDING", map[string]any{
					"refundID": "new-refund", "deductionHeld": true, "deductionType": "none",
				})
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				return prov, nil
			}
			svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}}

			_, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.Equal(t, "CONFLICT", infraerrors.Reason(err))
			if legacy {
				require.Empty(t, prov.lastQuery.RefundID)
			} else {
				require.Equal(t, "old-refund", prov.lastQuery.RefundID)
			}
			stored, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefundPending, stored.Status)
			require.Equal(t, 20.0, stored.RefundAmount)
			require.Equal(t, "replacement", psStringValue(stored.RefundReason))
			require.True(t, stored.ForceRefund)
		})
	}
}

type heldRefundSubscriptionRepo struct {
	userSubRepoNoop
}

func (heldRefundSubscriptionRepo) GetByIDForUpdate(ctx context.Context, id int64) (*UserSubscription, error) {
	sub, err := dbent.TxFromContext(ctx).Client().UserSubscription.Get(ctx, id)
	if dbent.IsNotFound(err) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &UserSubscription{ID: sub.ID, UserID: sub.UserID, Status: sub.Status, ExpiresAt: sub.ExpiresAt}, nil
}

func (heldRefundSubscriptionRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*UserSubscription, error) {
	sub, err := dbent.TxFromContext(ctx).Client().UserSubscription.Get(mixins.SkipSoftDelete(ctx), id)
	if dbent.IsNotFound(err) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &UserSubscription{ID: sub.ID, UserID: sub.UserID, Status: sub.Status, ExpiresAt: sub.ExpiresAt, DeletedAt: sub.DeletedAt}, nil
}

func (heldRefundSubscriptionRepo) ExtendExpiry(ctx context.Context, id int64, expiresAt time.Time) error {
	return dbent.TxFromContext(ctx).Client().UserSubscription.UpdateOneID(id).SetExpiresAt(expiresAt).Exec(ctx)
}

func (heldRefundSubscriptionRepo) ExistsGenericByUserID(ctx context.Context, userID int64) (bool, error) {
	return dbent.TxFromContext(ctx).Client().UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDIsNil()).Exist(ctx)
}

func TestAlipayFailedHoldWithShortenedSubscriptionReplaced(t *testing.T) {
	ctx := t.Context()
	client := newPaymentConfigServiceTestClient(t)
	order := makeAlipayRefundPendingForTest(t, client, "alipay-shortened-and-replaced", `{"refundID":"rf_test","deductionHeld":true,"deductionType":"subscription","subDaysDeducted":7,"subscriptionID":5}`)
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetOrderType(payment.OrderTypeSubscription).Save(ctx)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	original, err := client.UserSubscription.Create().SetUserID(order.UserID).SetStartsAt(now.AddDate(0, 0, -1)).
		SetExpiresAt(now.AddDate(0, 0, 3)).SetDeletedAt(now).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Update().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING"),
	).SetDetail(fmt.Sprintf(`{"refundID":"rf_test","deductionHeld":true,"deductionType":"subscription","subDaysDeducted":7,"subscriptionID":%d}`, original.ID)).Save(ctx)
	require.NoError(t, err)
	replacement, err := client.UserSubscription.Create().SetUserID(order.UserID).SetStartsAt(now).
		SetExpiresAt(now.AddDate(0, 0, 30)).Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		subscriptionSvc: NewSubscriptionService(groupRepoNoop{}, heldRefundSubscriptionRepo{}, nil, nil, nil),
	}
	t.Cleanup(replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
	}}))

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	stored, loadErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, loadErr)
	reloaded, loadErr := client.UserSubscription.Get(ctx, replacement.ID)
	require.NoError(t, loadErr)
	require.True(t, replacement.ExpiresAt.Equal(reloaded.ExpiresAt))
	require.NoError(t, err, "refund remained in status %s", stored.Status)
	require.Contains(t, result.Warning, "manual reconciliation")
	require.Equal(t, OrderStatusRefundFailed, stored.Status)
	require.Equal(t, 1, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED"))
}

func TestAlipayFailedHoldReconcilesOriginalSubscription(t *testing.T) {
	for _, tc := range []struct {
		name         string
		holdRevoked  bool
		adminRevoked bool
	}{
		{name: "shortened subscription is extended"},
		{name: "admin revokes shortened subscription", adminRevoked: true},
		{name: "admin restores subscription revoked by hold", holdRevoked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			client := newUniqueRefundAuditTestClient(t)
			order := makeAlipayRefundPendingForTest(t, client, "alipay-admin-subscription-change", `{"refundID":"rf_test"}`)
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetOrderType(payment.OrderTypeSubscription).Save(ctx)
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Second)
			var deletedAt *time.Time
			if tc.adminRevoked {
				deletedAt = &now
			}
			original, err := client.UserSubscription.Create().SetUserID(order.UserID).SetStartsAt(now.AddDate(0, 0, -1)).
				SetExpiresAt(now.AddDate(0, 0, 3)).SetNillableDeletedAt(deletedAt).Save(ctx)
			require.NoError(t, err)
			_, err = client.PaymentAuditLog.Update().Where(
				paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING"),
			).SetDetail(fmt.Sprintf(`{"refundID":"rf_test","deductionHeld":true,"deductionType":"subscription","subDaysDeducted":7,"subscriptionID":%d,"subscriptionRevoked":%t}`, original.ID, tc.holdRevoked)).Save(ctx)
			require.NoError(t, err)
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				subscriptionSvc: NewSubscriptionService(groupRepoNoop{}, heldRefundSubscriptionRepo{}, nil, nil, nil),
			}
			t.Cleanup(replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
			}}))

			lookupErr := errors.New("transient subscription database error")
			failLookup := true
			client.UserSubscription.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
				return dbent.QuerierFunc(func(ctx context.Context, query dbent.Query) (dbent.Value, error) {
					if failLookup {
						return nil, lookupErr
					}
					return next.Query(ctx, query)
				})
			}))
			_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.ErrorIs(t, err, lookupErr)
			pending, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefundPending, pending.Status)
			require.Zero(t, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_FAILED"))
			require.Zero(t, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED"))
			failLookup = false

			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			stored, loadErr := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, loadErr)
			require.NoError(t, err, "refund remained in status %s", stored.Status)
			require.NotNil(t, result)
			require.False(t, result.Success)
			require.Equal(t, OrderStatusRefundFailed, stored.Status)
			require.Equal(t, 1, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_FAILED"))

			subs, err := client.UserSubscription.Query().Where(usersubscription.UserIDEQ(order.UserID)).All(mixins.SkipSoftDelete(ctx))
			require.NoError(t, err)
			require.Len(t, subs, 1)
			require.Equal(t, original.ID, subs[0].ID)
			require.Equal(t, original.Status, subs[0].Status)
			if !tc.adminRevoked && !tc.holdRevoked {
				require.True(t, original.ExpiresAt.AddDate(0, 0, 7).Equal(subs[0].ExpiresAt))
				require.Nil(t, subs[0].DeletedAt)
				require.Zero(t, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED"))
				return
			}

			require.Contains(t, result.Warning, "manual reconciliation")
			require.Equal(t, 1, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED"))
			require.True(t, original.ExpiresAt.Equal(subs[0].ExpiresAt))
			if deletedAt == nil {
				require.Nil(t, subs[0].DeletedAt)
			} else {
				require.NotNil(t, subs[0].DeletedAt)
				require.True(t, deletedAt.Equal(*subs[0].DeletedAt))
			}

			_, _, err = svc.PrepareRefund(ctx, order.ID, 100, "retry", true, true)
			require.Equal(t, "REFUND_RECONCILIATION_REQUIRED", infraerrors.Reason(err))
		})
	}
}

func (r heldRefundSubscriptionRepo) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	return r.GetByIDForUpdate(ctx, id)
}

func (heldRefundSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	// Store a canonical UTC timestamp so SQLite compares its text representation consistently.
	return dbent.TxFromContext(ctx).Client().UserSubscription.UpdateOneID(id).
		SetDeletedAt(time.Now().UTC().Truncate(time.Second)).Exec(ctx)
}

func (r heldRefundSubscriptionRepo) Restore(ctx context.Context, id int64, status string, deletedAt time.Time) (*UserSubscription, error) {
	count, err := dbent.TxFromContext(ctx).Client().UserSubscription.Update().
		Where(usersubscription.IDEQ(id), usersubscription.DeletedAtEQ(deletedAt)).
		SetStatus(status).ClearDeletedAt().Save(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrSubscriptionNotRevoked
	}
	return r.GetByID(ctx, id)
}

func TestAlipayFailedHoldRespectsRefundRevocationGeneration(t *testing.T) {
	for _, tc := range []struct {
		name          string
		adminReplaced bool
	}{
		{name: "restore the refund revocation"},
		{name: "preserve the later administrator revocation", adminReplaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			client := newUniqueRefundAuditTestClient(t)
			order := makeAlipayRefundPendingForTest(t, client, "alipay-revocation-generation", `{"refundID":"old-refund"}`)
			order, err := client.PaymentOrder.UpdateOneID(order.ID).
				SetOrderType(payment.OrderTypeSubscription).SetStatus(OrderStatusCompleted).Save(ctx)
			require.NoError(t, err)
			now := time.Now()
			sub, err := client.UserSubscription.Create().SetUserID(order.UserID).
				SetStartsAt(now.AddDate(0, 0, -1)).SetExpiresAt(now.AddDate(0, 0, 3)).
				SetStatus(SubscriptionStatusActive).Save(ctx)
			require.NoError(t, err)
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				subscriptionSvc: NewSubscriptionService(groupRepoNoop{}, heldRefundSubscriptionRepo{}, nil, client, nil),
			}
			_, err = svc.holdAlipayRefund(ctx, &RefundPlan{
				OrderID: order.ID, Order: order, RefundAmount: 10, refundID: "rf_test",
				DeductBalance: true, DeductionType: payment.DeductionTypeSubscription,
				SubscriptionID: sub.ID, SubDaysToDeduct: 7,
			})
			require.NoError(t, err)
			held, err := client.UserSubscription.Get(mixins.SkipSoftDelete(ctx), sub.ID)
			require.NoError(t, err)
			require.NotNil(t, held.DeletedAt)
			adminDeletion := held.DeletedAt.Add(time.Second)
			if tc.adminReplaced {
				require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).
					ClearDeletedAt().Exec(mixins.SkipSoftDelete(ctx)))
				require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetDeletedAt(adminDeletion).Exec(ctx))
			}

			t.Cleanup(replacePaymentProviderFactoryForTest(t, &alipayRefundQueryForTest{refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusFailed},
			}}))
			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			stored, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefundFailed, stored.Status)
			require.Equal(t, 1, countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_FAILED"))
			current, err := client.UserSubscription.Get(mixins.SkipSoftDelete(ctx), sub.ID)
			require.NoError(t, err)
			require.True(t, sub.ExpiresAt.Equal(current.ExpiresAt))
			manualAudits := countAlipayRefundAuditForTest(t, client, order.ID, "REFUND_RECONCILIATION_REQUIRED")
			retry, _, retryErr := svc.PrepareRefund(ctx, order.ID, 10, "retry", true, false)
			if tc.adminReplaced {
				require.NotNil(t, current.DeletedAt)
				require.True(t, adminDeletion.Equal(*current.DeletedAt))
				require.Contains(t, result.Warning, "manual reconciliation")
				require.Equal(t, 1, manualAudits)
				require.Equal(t, "REFUND_RECONCILIATION_REQUIRED", infraerrors.Reason(retryErr))
			} else {
				require.Nil(t, current.DeletedAt)
				require.Zero(t, manualAudits)
				require.NoError(t, retryErr)
				require.NotNil(t, retry)
			}
		})
	}
}

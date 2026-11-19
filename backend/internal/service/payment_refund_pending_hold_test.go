//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type pendingRefundProviderTestDouble struct {
	refundProviderTestDouble
	queryStatus string
	refundCalls int
}

func (p *pendingRefundProviderTestDouble) Refund(_ context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	p.refundCalls++
	return &payment.RefundResponse{RefundID: provider.AlipayRefundRequestID(req.OrderID, req.Amount), Status: payment.ProviderStatusPending}, nil
}

func (p *pendingRefundProviderTestDouble) QueryRefund(context.Context, payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	return &payment.RefundResponse{RefundID: "rf_hold", Status: p.queryStatus}, nil
}

func (p *pendingRefundProviderTestDouble) ProviderKey() string { return payment.TypeAlipay }

func TestRefundPendingKeepsBalanceDeductedUntilGatewaySettles(t *testing.T) {
	for _, tc := range []struct {
		name, queryStatus, wantStatus string
		wantBalance                   float64
	}{
		{name: "gateway succeeds", queryStatus: payment.ProviderStatusSuccess, wantStatus: OrderStatusRefunded, wantBalance: 0},
		{name: "gateway fails", queryStatus: payment.ProviderStatusFailed, wantStatus: OrderStatusRefundFailed, wantBalance: 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createCompletedRefundableOrderForTest(t, ctx, client, "hold-flow")
			deductCalls := 0
			repo := &mockUserRepo{
				getByIDUser: &User{ID: order.UserID, Balance: 40},
				deductBalanceFn: func(txCtx context.Context, userID int64, amount float64) error {
					deductCalls++
					require.Equal(t, 35.0, amount)
					tx := dbent.TxFromContext(txCtx)
					require.NotNil(t, tx)
					_, err := tx.Client().User.UpdateOneID(userID).AddBalance(-amount).Save(txCtx)
					return err
				},
			}
			prov := &pendingRefundProviderTestDouble{queryStatus: tc.queryStatus}
			restore := replacePaymentProviderFactoryForTest(t, prov)
			defer restore()
			svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: repo}
			plan, early, err := svc.PrepareRefund(ctx, order.ID, 0, "user asked", false, true)
			require.NoError(t, err)
			require.Nil(t, early)

			result, err := svc.ExecuteRefund(ctx, plan)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Contains(t, result.Warning, "pending confirmation")
			pending, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefundPending, pending.Status)
			account, err := client.User.Get(ctx, order.UserID)
			require.NoError(t, err)
			require.Zero(t, account.Balance)
			_, _, err = svc.PrepareRefund(ctx, order.ID, 0, "again", true, true)
			require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err))

			result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.queryStatus == payment.ProviderStatusSuccess, result.Success)
			if result.Success {
				require.Equal(t, 35.0, result.BalanceDeducted)
			}
			require.Equal(t, 1, deductCalls)
			require.Equal(t, 1, prov.refundCalls)
			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, reloaded.Status)
			account, err = client.User.Get(ctx, order.UserID)
			require.NoError(t, err)
			require.Equal(t, tc.wantBalance, account.Balance)
		})
	}
}

func TestQueryAndFinalizeRefundHeldDeductionWithoutBalanceDeduction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	// 管理员选择不扣余额时，终态收敛也不能按旧逻辑补扣。
	order := createPendingRefundOrderWithDetailForTest(t, ctx, client, "hold-no-deduct",
		`{"refundID":"rf_test","deductionHeld":true,"deductionType":"none","balanceDeducted":0}`)

	restore := replacePaymentProviderFactoryForTest(t, &pendingRefundProviderTestDouble{queryStatus: payment.ProviderStatusSuccess})
	defer restore()
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: &mockUserRepo{deductBalanceFn: func(context.Context, int64, float64) error {
		t.Fatal("admin requested no deduction")
		return nil
	}}}

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, result.BalanceDeducted)
}

func TestQueryAndFinalizeRefundRejectsUnreadablePendingAudit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	// 审计无法解析时不能退化为旧逻辑：成功会二次扣减，失败会吞掉保留的扣减。
	order := createPendingRefundOrderWithDetailForTest(t, ctx, client, "hold-bad-audit", `not-json`)

	for _, queryStatus := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusFailed} {
		restore := replacePaymentProviderFactoryForTest(t, &pendingRefundProviderTestDouble{queryStatus: queryStatus})
		svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: &mockUserRepo{deductBalanceFn: func(context.Context, int64, float64) error {
			t.Fatal("unreadable audit cannot authorize a deduction")
			return nil
		}}}

		result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
		restore()
		require.Error(t, err, queryStatus)
		require.Nil(t, result)
	}

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, reloaded.Status)
}

func createCompletedRefundableOrderForTest(t *testing.T, ctx context.Context, client *dbent.Client, suffix string) *dbent.PaymentOrder {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(suffix + "@example.com").
		SetPasswordHash("hash").
		SetUsername(suffix).
		SetBalance(35).
		Save(ctx)
	require.NoError(t, err)
	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName(suffix + "-provider").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("REFUND-" + suffix).
		SetOutTradeNo("sub2_" + suffix).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("pi_" + suffix).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).
		Save(ctx)
	require.NoError(t, err)
	return order
}

func TestAlipayDoesNotSendRefundWhenHoldCannotBeRecorded(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createCompletedRefundableOrderForTest(t, ctx, client, "hold-intent-write-failure")
	client.PaymentAuditLog.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
			if action, _ := mutation.Field("action"); action == "REFUND_PENDING" {
				return nil, errors.New("test cannot record refund intent")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	repo := &mockUserRepo{
		getByIDUser: &User{ID: order.UserID, Balance: 35},
		deductBalanceFn: func(txCtx context.Context, userID int64, amount float64) error {
			tx := dbent.TxFromContext(txCtx)
			require.NotNil(t, tx)
			_, err := tx.Client().User.UpdateOneID(userID).AddBalance(-amount).Save(txCtx)
			return err
		},
	}
	prov := &pendingRefundProviderTestDouble{queryStatus: payment.ProviderStatusSuccess}
	restore := replacePaymentProviderFactoryForTest(t, prov)
	defer restore()
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: repo}
	plan, early, err := svc.PrepareRefund(ctx, order.ID, 0, "user asked", false, true)
	require.NoError(t, err)
	require.Nil(t, early)

	_, err = svc.ExecuteRefund(ctx, plan)
	require.ErrorContains(t, err, "test cannot record refund intent")
	require.Zero(t, prov.refundCalls)
	account, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 35.0, account.Balance)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

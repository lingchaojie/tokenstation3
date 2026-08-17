//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestRefundPreparationPreservesExistingPartialBalanceDeductionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		balance    float64
		force      bool
		wantDeduct float64
	}{
		{name: "insufficient balance without force", balance: 40, wantDeduct: 40},
		{name: "forced insufficient balance", balance: 40, force: true, wantDeduct: 40},
		{name: "equal balance", balance: 100, wantDeduct: 100},
		{name: "existing deficit is not deepened", balance: -5, wantDeduct: -5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &RefundPlan{RefundAmount: 100}
			svc := &PaymentService{userRepo: &mockUserRepo{getByIDUser: &User{Balance: tc.balance}}}

			result := svc.prepDeduct(context.Background(), &dbent.PaymentOrder{
				UserID:    1,
				OrderType: payment.OrderTypeBalance,
			}, plan, tc.force)

			require.Nil(t, result)
			require.Equal(t, payment.DeductionTypeBalance, plan.DeductionType)
			require.Equal(t, tc.wantDeduct, plan.BalanceToDeduct)
		})
	}
}

type refundLocalPolicyRepo struct {
	*mockUserRepo
	availableDeductionCalled bool
}

func (r *refundLocalPolicyRepo) DeductAvailableBalance(context.Context, int64, float64) (float64, error) {
	r.availableDeductionCalled = true
	return 0, errors.New("refund must not use clamp-to-available deduction")
}

func TestRefundExecutionPreservesExistingBalanceDeductionContract(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("refund-local-policy@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-local-policy").
		Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("REFUND-LOCAL-POLICY").
		SetOutTradeNo("refund_local_policy").
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	repo := &refundLocalPolicyRepo{mockUserRepo: &mockUserRepo{deductBalanceFn: func(_ context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		require.Equal(t, 100.0, amount)
		return nil
	}}}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "preserve local policy", DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}

	result, err := (&PaymentService{entClient: client, userRepo: repo}).ExecuteRefund(ctx, plan)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.False(t, repo.availableDeductionCalled)
	require.Equal(t, 100.0, result.BalanceDeducted)
	audit, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, audit.Detail, `"balanceDeducted":100`)
}

func TestPendingRefundBalanceDeductionAmountPreservesLocalPartialPolicy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		balance    float64
		wantDeduct float64
	}{
		{name: "insufficient balance", balance: 40, wantDeduct: 40},
		{name: "existing deficit", balance: -5, wantDeduct: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantDeduct, pendingRefundBalanceDeductionAmount(100, tc.balance))
		})
	}
}

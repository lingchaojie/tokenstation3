//go:build unit

package service

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type rechargeCurrencySelection struct {
	payment.LoadBalancer
	amounts     []float64
	rejectFinal bool
	changeAgain bool
}

func (lb *rechargeCurrencySelection) SelectInstance(_ context.Context, _ string, _ payment.PaymentType, _ payment.Strategy, amount float64) (*payment.InstanceSelection, error) {
	lb.amounts = append(lb.amounts, amount)
	if lb.rejectFinal && amount > 86 {
		return nil, errors.New("final amount exceeds channel maximum")
	}
	currency := "JPY"
	if lb.changeAgain && len(lb.amounts) > 1 {
		currency = "EUR"
	}
	// No secretKey: stop at provider construction, before any network call,
	// while retaining the actual transactionally persisted order snapshot.
	return &payment.InstanceSelection{InstanceID: "currency-test", ProviderKey: payment.TypeStripe,
		SupportedTypes: payment.TypeStripe, Config: map[string]string{"currency": currency}}, nil
}

// A provider currency changed after the configuration read must re-quote the
// discount and re-check channel limits before persisting or contacting it.
func TestRechargeBonusFinalCurrencyRequotesAndRevalidates(t *testing.T) {
	for _, mode := range []string{"snapshot", "channel_limit", "currency_changes_again"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			uid := createPaymentOrderSeatUser(t, ctx, client, "currency@example.com")
			svc := newPaymentOrderSeatCreateOrderService(t, client, 0, uid)
			settings := svc.configService.settingRepo.(*paymentConfigSettingRepoStub)
			settings.values[SettingBalanceRechargeMult] = "1"
			settings.values[SettingRechargeFeeRate] = "0.1"
			settings.values[SettingRechargeBonusMode] = "discount"
			settings.values[SettingRechargeBonusTiers] = `[{"min_amount":0,"bonus_percent":15}]`
			lb := &rechargeCurrencySelection{rejectFinal: mode == "channel_limit", changeAgain: mode == "currency_changes_again"}
			svc.loadBalancer = lb
			_, err := svc.CreateOrder(ctx, CreateOrderRequest{UserID: uid, PaymentType: payment.TypeStripe,
				OrderType: payment.OrderTypeBalance, Amount: 101, ClientIP: "127.0.0.1", SrcHost: "app.example.com"})
			require.Error(t, err)
			require.Equal(t, []float64{85.94, 87}, lb.amounts)
			orders, readErr := client.PaymentOrder.Query().All(ctx)
			require.NoError(t, readErr)
			if mode != "snapshot" {
				require.Empty(t, orders, "invalid final selection must not create an order")
				return
			}
			require.Equal(t, "PAYMENT_PROVIDER_MISCONFIGURED", infraerrors.Reason(err))
			require.Len(t, orders, 1)
			require.Equal(t, float64(101), orders[0].Amount)
			require.Equal(t, float64(15), orders[0].BonusAmount)
			require.Equal(t, float64(87), orders[0].PayAmount)
			require.Equal(t, "JPY", PaymentOrderCurrency(orders[0]))
		})
	}
}

// These cases fail if checkout loses the bonus snapshot, fulfillment re-quotes
// against changed settings, retries double-credit, or free credit qualifies a
// first recharge that was actually below the local reward threshold.
func TestRechargeBonusLocalFulfillmentContract(t *testing.T) {
	for _, tc := range []struct {
		name, mode, tiers, money                string
		input, credited, pay, bonus, halfRefund float64
		qualified                               bool
	}{
		{"plain", "bonus", "", "100.00", 100, 100, 100, 0, 50, true},
		{"bonus", "bonus", `[{"min_amount":100,"bonus_percent":10}]`, "100.00", 100, 110, 100, 10, 50, true},
		{"discount", "discount", `[{"min_amount":100,"bonus_percent":10}]`, "90.00", 100, 100, 90, 10, 45, true},
		{"gift_does_not_qualify_first_recharge", "bonus", `[{"min_amount":0,"bonus_percent":20}]`, "19.00", 19, 22.8, 19, 3.8, 9.5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rewardSvc, client, affiliateRepo, _, uid := newFirstRechargeRewardEnv(t, 9001)
			svc := newPaymentOrderSeatCreateOrderService(t, client, 0, uid)
			svc.affiliateService = rewardSvc.affiliateService
			settings := svc.configService.settingRepo.(*paymentConfigSettingRepoStub)
			settings.values[SettingBalanceRechargeMult] = "1"
			settings.values[SettingRechargeBonusMode] = tc.mode
			settings.values[SettingRechargeBonusTiers] = tc.tiers
			response, err := svc.CreateOrder(ctx, CreateOrderRequest{
				UserID: uid, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeBalance,
				Amount: tc.input, ClientIP: "127.0.0.1", SrcHost: "app.example.com",
			})
			require.NoError(t, err)
			require.Equal(t, tc.credited, response.Amount)
			require.Equal(t, tc.pay, response.PayAmount)
			require.Equal(t, tc.bonus, response.BonusAmount)
			paymentURL, err := url.Parse(response.PayURL)
			require.NoError(t, err)
			require.Equal(t, tc.money, paymentURL.Query().Get("money"))
			order, err := client.PaymentOrder.Get(ctx, response.OrderID)
			require.NoError(t, err)
			require.Equal(t, tc.bonus, order.BonusAmount)
			require.Equal(t, tc.halfRefund, calculateGatewayRefundAmount(order.Amount, order.PayAmount, order.Amount/2, PaymentOrderCurrency(order)))

			// Existing orders must not pick up a newly configured 1000% campaign.
			settings.values[SettingRechargeBonusTiers] = `[{"min_amount":0,"bonus_percent":1000}]`
			settings.values[SettingRechargeBonusMode] = "bonus"
			userRepo := svc.userRepo.(*mockUserRepo)
			credited := 0.0
			userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
				require.Equal(t, uid, id)
				credited += amount
				return nil
			}
			redeemRepo := &paymentFulfillmentRedeemRepo{}
			svc.redeemService = NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
			_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).Save(ctx)
			require.NoError(t, err)
			require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
			require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
			notification := &payment.PaymentNotification{TradeNo: "local-test-paid", OrderID: order.OutTradeNo, Amount: order.PayAmount, Status: payment.NotificationStatusSuccess}
			require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay))
			require.Equal(t, tc.credited, credited)
			require.Len(t, redeemRepo.useCalls, 1)
			require.Len(t, affiliateRepo.settlementCalls, 1)
			require.Equal(t, tc.qualified, affiliateRepo.settlementCalls[0].Qualified)
			require.Equal(t, 7, affiliateRepo.settlementCalls[0].ValidityDays, "existing invite reward lifetime stays unchanged")
			completed, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, completed.Status)
			require.Equal(t, tc.bonus, completed.BonusAmount)
		})
	}
}

func TestRechargeBonusDoesNotDiscountSubscription(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	plan, uid := createPaymentOrderSeatPlanFixture(t, ctx, client, nil)
	svc := newPaymentOrderSeatCreateOrderService(t, client, plan.GroupID, uid)
	settings := svc.configService.settingRepo.(*paymentConfigSettingRepoStub)
	settings.values[SettingRechargeBonusMode] = "discount"
	settings.values[SettingRechargeBonusTiers] = `[{"min_amount":0,"bonus_percent":50}]`
	response, err := svc.CreateOrder(ctx, CreateOrderRequest{
		UserID: uid, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeSubscription,
		PlanID: plan.ID, ClientIP: "127.0.0.1", SrcHost: "app.example.com",
	})
	require.NoError(t, err)
	require.Equal(t, 9.99, response.Amount)
	require.Equal(t, 9.99, response.PayAmount)
	require.Zero(t, response.BonusAmount)
}

//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	paymentprovider "github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

type paymentOrderLifecycleQueryProvider struct {
	key               string
	lastQueryTradeNo  string
	lastCancelTradeNo string
	queryCalls        int
	cancelCalls       int
	responses         []*payment.QueryOrderResponse
	resp              *payment.QueryOrderResponse
}

type paymentOrderLifecycleRedeemRepo struct {
	codesByCode map[string]*RedeemCode
	useCalls    []struct {
		id     int64
		userID int64
	}
}

type paymentOrderLifecycleLoadBalancer struct {
	selection *payment.InstanceSelection
}

func (lb *paymentOrderLifecycleLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return lb.selection.Config, nil
}

func (lb *paymentOrderLifecycleLoadBalancer) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return lb.selection, nil
}

func (p *paymentOrderLifecycleQueryProvider) Name() string {
	return "payment-order-lifecycle-query-provider"
}

func (p *paymentOrderLifecycleQueryProvider) ProviderKey() string {
	if p.key != "" {
		return p.key
	}
	return payment.TypeAlipay
}

func (p *paymentOrderLifecycleQueryProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{p.ProviderKey()}
}

func (p *paymentOrderLifecycleQueryProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) QueryOrder(_ context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	p.lastQueryTradeNo = tradeNo
	p.queryCalls++
	if len(p.responses) > 0 {
		resp := p.responses[0]
		if len(p.responses) > 1 {
			p.responses = p.responses[1:]
		}
		return resp, nil
	}
	return p.resp, nil
}

func (p *paymentOrderLifecycleQueryProvider) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) CancelPayment(_ context.Context, tradeNo string) error {
	p.lastCancelTradeNo = tradeNo
	p.cancelCalls++
	return nil
}

func (r *paymentOrderLifecycleRedeemRepo) Create(context.Context, *RedeemCode) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) CreateBatch(context.Context, []RedeemCode) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) GetByID(_ context.Context, id int64) (*RedeemCode, error) {
	for _, code := range r.codesByCode {
		if code.ID != id {
			continue
		}
		cloned := *code
		return &cloned, nil
	}
	return nil, ErrRedeemCodeNotFound
}

func (r *paymentOrderLifecycleRedeemRepo) GetByCode(_ context.Context, code string) (*RedeemCode, error) {
	redeemCode, ok := r.codesByCode[code]
	if !ok {
		return nil, ErrRedeemCodeNotFound
	}
	cloned := *redeemCode
	return &cloned, nil
}

func (r *paymentOrderLifecycleRedeemRepo) Update(context.Context, *RedeemCode) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) BatchUpdate(context.Context, []int64, RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) Delete(context.Context, int64) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) Use(_ context.Context, id, userID int64) error {
	for code, redeemCode := range r.codesByCode {
		if redeemCode.ID != id {
			continue
		}
		now := time.Now().UTC()
		redeemCode.Status = StatusUsed
		redeemCode.UsedBy = &userID
		redeemCode.UsedAt = &now
		r.codesByCode[code] = redeemCode
		r.useCalls = append(r.useCalls, struct {
			id     int64
			userID int64
		}{id: id, userID: userID})
		return nil
	}
	return ErrRedeemCodeNotFound
}

func (r *paymentOrderLifecycleRedeemRepo) List(context.Context, pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListByUser(context.Context, int64, int) ([]RedeemCode, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected call")
}

func TestCreateOrderRejectsNewUserWhenPlanSeatLimitReached(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, existingUserID := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	newUserID := createPaymentOrderSeatUser(t, ctx, client, "seat-order-new@example.com")
	createSeatSubscription(t, ctx, client, existingUserID, plan.GroupID, plan.ID, SubscriptionStatusActive, time.Now().Add(time.Hour))
	svc := newPaymentOrderSeatCreateOrderService(t, client, plan.GroupID, newUserID)

	_, createErr := svc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      newUserID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
		ClientIP:    "127.0.0.1",
		SrcHost:     "app.example.com",
	})

	require.Error(t, createErr)
	require.Equal(t, "PLAN_SEAT_LIMIT_REACHED", infraerrors.Reason(createErr))
}

func TestCreateOrderAllowsSameUserRenewalWhenPlanSeatLimitReached(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	limit := 1
	plan, userID := createPaymentOrderSeatPlanFixture(t, ctx, client, &limit)
	createSeatSubscription(t, ctx, client, userID, plan.GroupID, plan.ID, SubscriptionStatusActive, time.Now().Add(time.Hour))
	svc := newPaymentOrderSeatCreateOrderService(t, client, plan.GroupID, userID)

	resp, err := svc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      userID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
		ClientIP:    "127.0.0.1",
		SrcHost:     "app.example.com",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotZero(t, resp.OrderID)
	reloaded, err := client.PaymentOrder.Get(ctx, resp.OrderID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.PlanID)
	require.Equal(t, plan.ID, *reloaded.PlanID)
}

func TestVerifyOrderByOutTradeNoBackfillsTradeNoFromPaidQuery(t *testing.T) {
	for _, tc := range []struct {
		name, firstCheck string
		easyPay, unpaid  bool
	}{
		{name: "Alipay"},
		{name: "EasyPay active verification places a hold", firstCheck: "verify", easyPay: true},
		{name: "EasyPay cancellation places a hold", firstCheck: "cancel", easyPay: true},
		{name: "EasyPay hold survives a concurrent cancellation", firstCheck: "cancel-race", easyPay: true},
		{name: "EasyPay expiry places a hold", firstCheck: "expire", easyPay: true},
		{name: "EasyPay webhook confirmation places a hold", firstCheck: "webhook", easyPay: true},
		{name: "EasyPay active verification recovers an old expiry", firstCheck: "late-active", easyPay: true},
		{name: "EasyPay unpaid order still cancels", firstCheck: "cancel", easyPay: true, unpaid: true},
		{name: "EasyPay unpaid order still expires", firstCheck: "expire", easyPay: true, unpaid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)

			user, err := client.User.Create().
				SetEmail("checkpaid@example.com").
				SetPasswordHash("hash").
				SetUsername("checkpaid-user").
				Save(ctx)
			require.NoError(t, err)

			order, err := client.PaymentOrder.Create().
				SetUserID(user.ID).
				SetUserEmail(user.Email).
				SetUserName(user.Username).
				SetAmount(88).
				SetPayAmount(88).
				SetFeeRate(0).
				SetRechargeCode("CHECKPAID-UPSTREAM-TRADE-NO").
				SetOutTradeNo("sub2_checkpaid_trade_no_missing").
				SetPaymentType(payment.TypeAlipay).
				SetPaymentTradeNo("").
				SetOrderType(payment.OrderTypeBalance).
				SetStatus(OrderStatusPending).
				SetExpiresAt(time.Now().Add(time.Hour)).
				SetClientIP("127.0.0.1").
				SetSrcHost("api.example.com").
				Save(ctx)
			require.NoError(t, err)

			userRepo := &mockUserRepo{
				getByIDUser: &User{
					ID:       user.ID,
					Email:    user.Email,
					Username: user.Username,
					Balance:  0,
				},
			}
			userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
				require.Equal(t, user.ID, id)
				if userRepo.getByIDUser != nil {
					userRepo.getByIDUser.Balance += amount
				}
				return nil
			}
			redeemRepo := &paymentOrderLifecycleRedeemRepo{
				codesByCode: map[string]*RedeemCode{
					order.RechargeCode: {
						ID:     1,
						Code:   order.RechargeCode,
						Type:   RedeemTypeBalance,
						Value:  order.Amount,
						Status: StatusUnused,
					},
				},
			}
			redeemService := NewRedeemService(
				redeemRepo,
				userRepo,
				nil,
				nil,
				nil,
				client,
				nil,
				nil,
			)
			registry := payment.NewRegistry()
			provider := &paymentOrderLifecycleQueryProvider{
				resp: &payment.QueryOrderResponse{
					TradeNo: "upstream-trade-123",
					Status:  payment.ProviderStatusPaid,
					Amount:  88,
				},
			}
			const (
				missingTradeNo = iota
				merchantUnavailable
				publishTradeNo
				merchantUnpaid
			)
			var queryPhase atomic.Int32
			var queryCalls atomic.Int32
			cancelQueryStarted := make(chan struct{})
			releaseCancelQuery := make(chan struct{})
			var easyPay *paymentprovider.EasyPay
			tradeNo := "upstream-trade-123"
			if tc.firstCheck == "verify" {
				tradeNo = order.OutTradeNo
			}
			if tc.easyPay {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("out_trade_no") != order.OutTradeNo || r.URL.Query().Get("pid") != "merchant" {
						t.Error("unexpected merchant order reference")
					}
					if tc.firstCheck == "cancel-race" && queryCalls.Add(1) == 1 {
						close(cancelQueryStarted)
						select {
						case <-releaseCancelQuery:
							w.WriteHeader(http.StatusServiceUnavailable)
						case <-r.Context().Done():
						}
						return
					}
					switch queryPhase.Load() {
					case merchantUnavailable:
						w.WriteHeader(http.StatusServiceUnavailable)
					case merchantUnpaid:
						_, _ = fmt.Fprint(w, `{"code":1,"status":0,"money":"88.00"}`)
					case publishTradeNo:
						_, _ = fmt.Fprintf(w, `{"code":1,"status":1,"money":"88.00","trade_no":%q}`, tradeNo)
					case missingTradeNo:
						_, _ = fmt.Fprint(w, `{"code":1,"status":1,"money":"88.00"}`)
					}
				}))
				t.Cleanup(server.Close)
				p, err := paymentprovider.NewEasyPay("easypay-checkpaid", map[string]string{
					"apiBase": server.URL, "pid": "merchant", "pkey": "synthetic-secret",
					"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
				})
				require.NoError(t, err)
				easyPay = p
				registry.Register(p)
			} else {
				registry.Register(provider)
			}

			svc := &PaymentService{
				entClient:       client,
				registry:        registry,
				redeemService:   redeemService,
				userRepo:        userRepo,
				providersLoaded: true,
			}

			if tc.firstCheck == "expire" {
				_, err := client.PaymentOrder.UpdateOneID(order.ID).SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx)
				require.NoError(t, err)
			}
			if tc.unpaid {
				queryPhase.Store(merchantUnpaid)
				expected := OrderStatusCancelled
				if tc.firstCheck == "cancel" {
					outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
					require.NoError(t, err)
					require.Equal(t, checkPaidResultCancelled, outcome)
				} else {
					expected = OrderStatusExpired
					n, err := svc.ExpireTimedOutOrders(ctx)
					require.NoError(t, err)
					require.Equal(t, 1, n)
				}
				stored, err := client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, expected, stored.Status)
				return
			}

			webhook := &payment.PaymentNotification{
				OrderID: order.OutTradeNo, TradeNo: tradeNo, Amount: 88,
				Status: payment.NotificationStatusSuccess, Metadata: map[string]string{"pid": "merchant"},
			}
			if tc.firstCheck == "late-active" {
				queryPhase.Store(publishTradeNo)
				updatedAt := time.Now().Add(-time.Duration(paymentGraceMinutes+1) * time.Minute)
				_, err := client.PaymentOrder.UpdateOneID(order.ID).
					SetStatus(OrderStatusExpired).SetUpdatedAt(updatedAt).Save(ctx)
				require.NoError(t, err)
				require.NoError(t, svc.HandleVerifiedPaymentNotification(ctx, webhook, easyPay))
				expired, err := client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, OrderStatusExpired, expired.Status, "a late callback still respects the expiry grace")
				require.Empty(t, expired.PaymentTradeNo)
				require.Zero(t, userRepo.getByIDUser.Balance)
			} else if tc.easyPay {
				switch tc.firstCheck {
				case "verify":
					_, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
					require.NoError(t, err)
				case "cancel":
					outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
					require.Empty(t, outcome)
					require.Equal(t, "PAYMENT_CONFIRMATION_PENDING", infraerrors.Reason(err))
				case "cancel-race":
					cancelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					cancelResult := make(chan error, 1)
					go func() {
						_, err := svc.CancelOrder(cancelCtx, order.ID, user.ID)
						cancelResult <- err
					}()
					select {
					case <-cancelQueryStarted:
					case <-cancelCtx.Done():
						t.Fatal("cancellation never queried EasyPay")
					}
					_, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
					require.NoError(t, err)
					close(releaseCancelQuery)
					select {
					case err := <-cancelResult:
						require.Equal(t, "PAYMENT_CONFIRMATION_PENDING", infraerrors.Reason(err))
					case <-cancelCtx.Done():
						t.Fatal("cancellation did not finish")
					}
				case "expire":
					n, err := svc.ExpireTimedOutOrders(ctx)
					require.NoError(t, err)
					require.Zero(t, n)
				case "webhook":
					require.ErrorIs(t, svc.HandleVerifiedPaymentNotification(ctx, webhook, easyPay), ErrPaymentUpstreamUnconfirmed)
				}
				pending, err := client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, OrderStatusPending, pending.Status)
				require.Empty(t, pending.PaymentTradeNo)
				require.Nil(t, pending.PaidAt)
				require.Zero(t, userRepo.getByIDUser.Balance)
				require.Empty(t, redeemRepo.useCalls)
				hold := client.PaymentAuditLog.Query().Where(
					paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
					paymentauditlog.ActionEQ("PAYMENT_AWAITING_TRADE_NO"),
				)
				holdCount, err := hold.Count(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, holdCount, "affirmative payment evidence must survive the query")
				require.ErrorIs(t, svc.HandleVerifiedPaymentNotification(ctx, webhook, easyPay), ErrPaymentUpstreamUnconfirmed)

				queryPhase.Store(merchantUnavailable)
				outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
				require.Empty(t, outcome)
				require.Equal(t, "PAYMENT_CONFIRMATION_PENDING", infraerrors.Reason(err))
				outcome, err = svc.AdminCancelOrder(ctx, order.ID)
				require.Empty(t, outcome)
				require.Equal(t, "PAYMENT_CONFIRMATION_PENDING", infraerrors.Reason(err))
				_, err = client.PaymentOrder.UpdateOneID(order.ID).SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx)
				require.NoError(t, err)
				n, err := svc.ExpireTimedOutOrders(ctx)
				require.NoError(t, err)
				require.Zero(t, n)
				pending, err = client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, OrderStatusPending, pending.Status)
				holdCount, err = hold.Count(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, holdCount)
				queryPhase.Store(publishTradeNo)
				if tc.firstCheck == "cancel" {
					outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
					require.Empty(t, outcome)
					require.Equal(t, "ORDER_ALREADY_PAID", infraerrors.Reason(err))
				}
			}

			got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
			require.NoError(t, err)
			if tc.easyPay {
				require.NoError(t, svc.HandleVerifiedPaymentNotification(ctx, webhook, easyPay))
			} else {
				require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
			}
			require.Equal(t, OrderStatusCompleted, got.Status)
			require.Equal(t, tradeNo, got.PaymentTradeNo)

			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, reloaded.Status)
			require.Equal(t, tradeNo, reloaded.PaymentTradeNo)

			require.Equal(t, 88.0, userRepo.getByIDUser.Balance)
			require.Len(t, redeemRepo.useCalls, 1)
			require.Equal(t, int64(1), redeemRepo.useCalls[0].id)
			require.Equal(t, user.ID, redeemRepo.useCalls[0].userID)
		})
	}
}

func TestVerifyOrderByOutTradeNoRetriesZeroAmountPaidQueryOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-retry@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-retry-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-UPSTREAM-RETRY").
		SetOutTradeNo("sub2_checkpaid_retry_zero_amount").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   RedeemTypeBalance,
				Value:  order.Amount,
				Status: StatusUnused,
			},
		},
	}
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		responses: []*payment.QueryOrderResponse{
			{
				TradeNo: "upstream-trade-zero",
				Status:  payment.ProviderStatusPaid,
				Amount:  0,
			},
			{
				TradeNo: "upstream-trade-retry",
				Status:  payment.ProviderStatusPaid,
				Amount:  88,
			},
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		redeemService:   redeemService,
		userRepo:        userRepo,
		providersLoaded: true,
	}

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, provider.queryCalls)
	require.Equal(t, OrderStatusCompleted, got.Status)
	require.Equal(t, "upstream-trade-retry", got.PaymentTradeNo)
}

func TestVerifyOrderByOutTradeNoRejectsPaidQueryWithZeroAmount(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-zero-amount@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-zero-amount-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-ZERO-AMOUNT").
		SetOutTradeNo("sub2_checkpaid_zero_amount").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   RedeemTypeBalance,
				Value:  order.Amount,
				Status: StatusUnused,
			},
		},
	}
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-zero",
			Status:  payment.ProviderStatusPaid,
			Amount:  0,
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		redeemService:   redeemService,
		userRepo:        userRepo,
		providersLoaded: true,
	}

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, OrderStatusPending, got.Status)
	require.Empty(t, got.PaymentTradeNo)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, reloaded.Status)
	require.Empty(t, reloaded.PaymentTradeNo)

	require.Equal(t, 0.0, userRepo.getByIDUser.Balance)
	require.Empty(t, redeemRepo.useCalls)
}

func TestVerifyOrderByOutTradeNoDoesNotCancelUnpaidUpstreamOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-pending-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-PENDING").
		SetOutTradeNo("sub2_checkpaid_pending").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: order.OutTradeNo,
			Status:  payment.ProviderStatusPending,
			Amount:  0,
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		providersLoaded: true,
	}

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, got.Status)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Zero(t, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, reloaded.Status)
}

func TestCancelOrderStillClosesUnpaidUpstreamOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("cancel-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("cancel-pending-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CANCEL-PENDING").
		SetOutTradeNo("sub2_cancel_pending").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: order.OutTradeNo,
			Status:  payment.ProviderStatusPending,
			Amount:  0,
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		providersLoaded: true,
	}

	outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, checkPaidResultCancelled, outcome)
	require.Equal(t, order.OutTradeNo, provider.lastCancelTradeNo)
	require.Equal(t, 1, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, reloaded.Status)
}

func TestCancelOrderPinnedNonEasyPayDoesNotReadConfirmationAudit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	userID := createPaymentOrderSeatUser(t, ctx, client, "cancel-pinned-alipay@example.com")
	order := createFirstRechargeBalanceOrder(t, ctx, client, userID, 88, OrderStatusPending,
		"CANCEL-PINNED-ALIPAY", "sub2_cancel_pinned_alipay")
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetProviderKey(payment.TypeAlipay).Save(ctx)
	require.NoError(t, err)

	client.PaymentAuditLog.Intercept(dbent.InterceptFunc(func(_ dbent.Querier) dbent.Querier {
		return dbent.QuerierFunc(func(context.Context, dbent.Query) (dbent.Value, error) {
			return nil, fmt.Errorf("confirmation audit read unavailable")
		})
	}))
	svc := &PaymentService{entClient: client}
	outcome, err := svc.CancelOrder(ctx, order.ID, userID)
	require.NoError(t, err)
	require.Equal(t, checkPaidResultCancelled, outcome)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
}

func TestReconcilePendingPaymentOrdersBackfillsPaidOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("wxpay-reconcile@example.com").
		SetPasswordHash("hash").
		SetUsername("wxpay-reconcile-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(50).
		SetPayAmount(50).
		SetFeeRate(0).
		SetRechargeCode("WXPAY-RECONCILE").
		SetOutTradeNo("sub2_wxpay_reconcile").
		SetPaymentType(payment.TypeWxpay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   RedeemTypeBalance,
				Value:  order.Amount,
				Status: StatusUnused,
			},
		},
	}
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeWxpay,
		resp: &payment.QueryOrderResponse{
			TradeNo: "wxpay-upstream-trade-123",
			Status:  payment.ProviderStatusPaid,
			Amount:  50,
			Metadata: map[string]string{
				"trade_state": "SUCCESS",
			},
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		redeemService:   redeemService,
		userRepo:        userRepo,
		providersLoaded: true,
	}

	recovered, err := svc.ReconcilePendingPaymentOrders(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Zero(t, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "wxpay-upstream-trade-123", reloaded.PaymentTradeNo)
	require.Equal(t, 50.0, userRepo.getByIDUser.Balance)
	require.Len(t, redeemRepo.useCalls, 1)
}

func TestReconcilePendingWxpayOrdersBackfillsPaidIkunPayOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("ikunpay-reconcile@example.com").
		SetPasswordHash("hash").
		SetUsername("ikunpay-reconcile-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(60).
		SetPayAmount(60).
		SetFeeRate(0).
		SetRechargeCode("IKUNPAY-RECONCILE").
		SetOutTradeNo("sub2_ikunpay_reconcile").
		SetPaymentType(payment.TypeIkunPay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   RedeemTypeBalance,
				Value:  order.Amount,
				Status: StatusUnused,
			},
		},
	}
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeIkunPay,
		resp: &payment.QueryOrderResponse{
			TradeNo: "ikunpay-upstream-trade-123",
			Status:  payment.ProviderStatusPaid,
			Amount:  60,
			Metadata: map[string]string{
				"pid":          "ikunpay-test-pid",
				"out_trade_no": order.OutTradeNo,
				"status":       "1",
			},
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		redeemService:   redeemService,
		userRepo:        userRepo,
		providersLoaded: true,
	}

	recovered, err := svc.ReconcilePendingWxpayOrders(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Zero(t, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "ikunpay-upstream-trade-123", reloaded.PaymentTradeNo)
	require.Equal(t, 60.0, userRepo.getByIDUser.Balance)
	require.Len(t, redeemRepo.useCalls, 1)
}

func TestReconcilePendingProviderOrdersSharesBatchLimitAcrossProviders(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	user, err := client.User.Create().SetEmail("mixed-reconcile@example.com").
		SetPasswordHash("hash").SetUsername("mixed-reconcile").Save(ctx)
	require.NoError(t, err)
	registry := payment.NewRegistry()
	keys := []string{payment.TypeWxpay, payment.TypeIkunPay, payment.TypeAlipay}
	providers := make([]*paymentOrderLifecycleQueryProvider, len(keys))
	for i, key := range keys {
		providers[i] = &paymentOrderLifecycleQueryProvider{
			key:  key,
			resp: &payment.QueryOrderResponse{Status: payment.ProviderStatusPending},
		}
		registry.Register(providers[i])
	}
	now := time.Now()
	for i := 0; i < 24; i++ {
		_, err := client.PaymentOrder.Create().SetUserID(user.ID).
			SetUserEmail(user.Email).SetUserName(user.Username).
			SetAmount(50).SetPayAmount(50).SetFeeRate(0).
			SetRechargeCode(fmt.Sprintf("MIXED-%d", i)).
			SetOutTradeNo(fmt.Sprintf("sub2_mixed_%d", i)).
			SetPaymentType(keys[i%len(keys)]).SetPaymentTradeNo("").
			SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPending).
			SetCreatedAt(now.Add(time.Duration(i-24) * time.Minute)).
			SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").
			SetSrcHost("api.example.com").Save(ctx)
		require.NoError(t, err)
	}
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	recovered, err := svc.ReconcilePendingProviderOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, recovered)
	// The oldest 20 orders are shared across all three providers, not 20 each.
	for i, count := range []int{7, 7, 6} {
		require.Equal(t, count, providers[i].queryCalls, keys[i])
		require.Zero(t, providers[i].cancelCalls, keys[i])
	}
	require.Equal(t, "sub2_mixed_18", providers[0].lastQueryTradeNo)
	require.Equal(t, "sub2_mixed_19", providers[1].lastQueryTradeNo)
	require.Equal(t, "sub2_mixed_17", providers[2].lastQueryTradeNo)
}

func TestReconcilePendingPaymentOrdersQueriesAlipayOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("alipay-reconcile@example.com").
		SetPasswordHash("hash").
		SetUsername("alipay-reconcile-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(50).
		SetPayAmount(50).
		SetFeeRate(0).
		SetRechargeCode("ALIPAY-RECONCILE").
		SetOutTradeNo("sub2_alipay_reconcile").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeAlipay,
		resp: &payment.QueryOrderResponse{
			TradeNo: order.OutTradeNo,
			Status:  payment.ProviderStatusPending,
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		providersLoaded: true,
	}

	recovered, err := svc.ReconcilePendingPaymentOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Equal(t, 1, provider.queryCalls)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Zero(t, provider.cancelCalls)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, reloaded.Status)
}

func TestVerifyOrderByOutTradeNoUsesOutTradeNoWhenPaymentTradeNoAlreadyExistsForAlipay(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-existing-trade@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-existing-trade-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-EXISTING-TRADE-NO").
		SetOutTradeNo("sub2_checkpaid_use_out_trade_no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("upstream-trade-existing").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   RedeemTypeBalance,
				Value:  order.Amount,
				Status: StatusUnused,
			},
		},
	}
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-existing",
			Status:  payment.ProviderStatusPaid,
			Amount:  88,
		},
	}
	registry.Register(provider)

	svc := &PaymentService{
		entClient:       client,
		registry:        registry,
		redeemService:   redeemService,
		userRepo:        userRepo,
		providersLoaded: true,
	}

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, "upstream-trade-existing", got.PaymentTradeNo)
}

func TestPaymentOrderAllowsRegistryFallbackOnlyForLegacyOrdersWithoutPinnedProviderState(t *testing.T) {
	t.Parallel()

	require.True(t, paymentOrderAllowsRegistryFallback(&dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
	}))

	instanceID := "12"
	require.False(t, paymentOrderAllowsRegistryFallback(&dbent.PaymentOrder{
		PaymentType:        payment.TypeAlipay,
		ProviderInstanceID: &instanceID,
	}))

	require.False(t, paymentOrderAllowsRegistryFallback(&dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version":       2,
			"provider_instance_id": "12",
		},
	}))
}

func TestPaymentOrderQueryReferenceUsesOutTradeNoForOfficialProviders(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType:    payment.TypeWxpay,
		OutTradeNo:     "sub2_out_trade_no",
		PaymentTradeNo: "wx-transaction-id",
	}

	require.Equal(t, "sub2_out_trade_no", paymentOrderQueryReference(order, &paymentOrderLifecycleQueryProvider{}))
	require.Equal(t, "sub2_out_trade_no", paymentOrderQueryReference(order, paymentFulfillmentTestProvider{
		key: payment.TypeWxpay,
	}))
}

func TestPaymentOrderQueryReferenceUsesOutTradeNoForIkunPay(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType:    payment.TypeAlipay,
		OutTradeNo:     "sub2_out_trade_no",
		PaymentTradeNo: "ikunpay-upstream-trade-no",
	}

	require.Equal(t, "sub2_out_trade_no", paymentOrderQueryReference(order, paymentFulfillmentTestProvider{key: payment.TypeIkunPay}))
}

func newPaymentOrderSeatCreateOrderService(t *testing.T, client *dbent.Client, groupID, userID int64) *PaymentService {
	t.Helper()
	configService := NewPaymentConfigService(client, &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled: "true",
	}}, nil)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: groupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	return &PaymentService{
		entClient:     client,
		configService: configService,
		groupRepo:     groupRepo,
		userRepo: &mockUserRepo{getByIDUser: &User{
			ID:       userID,
			Email:    "seat-order@example.com",
			Username: "seat-order-user",
			Status:   payment.EntityStatusActive,
		}},
		loadBalancer: &paymentOrderLifecycleLoadBalancer{selection: &payment.InstanceSelection{
			InstanceID:     "seat-test-easypay",
			ProviderKey:    payment.TypeEasyPay,
			SupportedTypes: payment.TypeAlipay,
			PaymentMode:    "popup",
			Config: map[string]string{
				"pid":         "1001",
				"pkey":        "secret",
				"apiBase":     "https://pay.example.com",
				"notifyUrl":   "https://api.example.com/notify",
				"returnUrl":   "https://app.example.com/payment/result",
				"paymentMode": "popup",
			},
		}},
	}
}

func createPaymentOrderSeatUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) int64 {
	t.Helper()
	return client.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		SetUsername(email).
		SetRole(domain.RoleUser).
		SetStatus(payment.EntityStatusActive).
		SaveX(ctx).
		ID
}

func createPaymentOrderSeatPlanFixture(t *testing.T, ctx context.Context, client *dbent.Client, limit *int) (*dbent.SubscriptionPlan, int64) {
	t.Helper()
	userID := createPaymentOrderSeatUser(t, ctx, client, "seat-order-existing@example.com")
	group := client.Group.Create().
		SetName("Seat Order Group").
		SetPlatform(domain.PlatformAnthropic).
		SetStatus(payment.EntityStatusActive).
		SetSubscriptionType(domain.SubscriptionTypeSubscription).
		SaveX(ctx)
	planCreate := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Seat Order Plan").
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
	return planCreate.SaveX(ctx), userID
}

func newPaymentOrderLifecycleTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	db, err := sql.Open("sqlite", "file:payment_order_lifecycle?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestExpiryRotatesPastStalledEasyPayHoldsWithSeparateBudget(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	userID := createPaymentOrderSeatUser(t, ctx, client, "expiry-fairness@example.com")
	createOrder := func(reference string, age time.Duration) *dbent.PaymentOrder {
		order, err := client.PaymentOrder.Create().
			SetUserID(userID).
			SetUserEmail("expiry-fairness@example.com").
			SetUserName("expiry-fairness").
			SetAmount(88).
			SetPayAmount(88).
			SetFeeRate(0).
			SetRechargeCode(reference).
			SetOutTradeNo(reference).
			SetPaymentType(payment.TypeAlipay).
			SetPaymentTradeNo("").
			SetOrderType(payment.OrderTypeBalance).
			SetStatus(OrderStatusPending).
			SetExpiresAt(time.Now().Add(-age)).
			SetClientIP("127.0.0.1").
			SetSrcHost("api.example.com").
			Save(ctx)
		require.NoError(t, err)
		return order
	}
	held := createOrder("sub2_held_expired", 2*time.Minute)
	laterHeld := createOrder("sub2_later_held", 90*time.Second)
	unpaid := createOrder("sub2_unpaid_expired", time.Minute)
	sweepCtx, interruptSweep := context.WithCancel(ctx)
	defer interruptSweep()
	secondSweepCtx, interruptSecondSweep := context.WithCancel(ctx)
	defer interruptSecondSweep()
	var stalledQueries atomic.Int32
	var sawLaterHeld atomic.Bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("out_trade_no") {
		case held.OutTradeNo:
			// The old payment remains unverifiable until the sweep budget ends.
			if stalledQueries.Add(1) == 1 {
				interruptSweep()
			} else {
				interruptSecondSweep()
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case laterHeld.OutTradeNo:
			sawLaterHeld.Store(true)
			_, _ = fmt.Fprint(w, `{"code":1,"status":1,"money":"88.00"}`)
		case unpaid.OutTradeNo:
			_, _ = fmt.Fprint(w, `{"code":1,"status":0,"money":"88.00"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()
	provider, err := paymentprovider.NewEasyPay("expiry-fairness", map[string]string{
		"apiBase": gateway.URL, "pid": "merchant", "pkey": "synthetic-secret",
		"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
	})
	require.NoError(t, err)
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	require.NoError(t, svc.markEasyPayAwaitingTradeNo(ctx, held.ID, provider))
	require.NoError(t, svc.markEasyPayAwaitingTradeNo(ctx, laterHeld.ID, provider))

	count, err := svc.ExpireTimedOutOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, stalledQueries.Load(), "ordinary expiry must use its own budget")
	require.Equal(t, 1, count)

	cursor, err := svc.reconcileExpiredEasyPayHolds(sweepCtx, easyPayHoldCursor{})
	require.NoError(t, err)
	require.Equal(t, int32(1), stalledQueries.Load(), "the held order must still be rechecked")
	require.Equal(t, held.ID, cursor.afterID)
	require.False(t, sawLaterHeld.Load(), "the oldest hold consumed the first hold budget")

	_, err = svc.reconcileExpiredEasyPayHolds(secondSweepCtx, cursor)
	require.NoError(t, err)
	require.True(t, sawLaterHeld.Load(), "a stalled older hold must not consume every later retry sweep")
	reloadedHeld, err := client.PaymentOrder.Get(ctx, held.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, reloadedHeld.Status)
	reloadedUnpaid, err := client.PaymentOrder.Get(ctx, unpaid.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusExpired, reloadedUnpaid.Status)
}

func TestLegacyEasyPayHoldRemainsBoundToObservedMerchant(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	userID := createPaymentOrderSeatUser(t, ctx, client, "replacement-merchant@example.com")
	order, err := client.PaymentOrder.Create().SetUserID(userID).SetUserEmail("replacement-merchant@example.com").SetUserName("customer").
		SetAmount(88).SetPayAmount(88).SetFeeRate(0).SetRechargeCode("LEGACY-EASYPAY-MERCHANT").
		SetOutTradeNo("sub2_legacy_merchant").SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPending).SetExpiresAt(time.Now().Add(-time.Minute)).
		SetClientIP("127.0.0.1").SetSrcHost("fixture.invalid").Save(ctx)
	require.NoError(t, err)
	var originalTradeNumberReady atomic.Bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if originalTradeNumberReady.Load() {
			_, _ = fmt.Fprint(w, `{"code":1,"status":1,"money":"88.00","pid":"original-merchant","trade_no":"original-trade"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"code":1,"status":1,"money":"88.00","pid":"original-merchant"}`)
	}))
	defer gateway.Close()
	easyPay, err := paymentprovider.NewEasyPay("original", map[string]string{
		"apiBase": gateway.URL, "pid": "original-merchant", "pkey": "synthetic-fixture",
		"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
	})
	require.NoError(t, err)
	registry := payment.NewRegistry()
	registry.Register(easyPay)
	userRepo := &mockUserRepo{getByIDUser: &User{ID: userID}}
	userRepo.updateBalanceFn = func(_ context.Context, _ int64, amount float64) error {
		userRepo.getByIDUser.Balance += amount
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{codesByCode: map[string]*RedeemCode{
		order.RechargeCode: {ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUnused},
	}}
	redeemService := NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true, redeemService: redeemService, userRepo: userRepo}
	_, err = svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, order.UserID)
	require.NoError(t, err)
	held, err := paymentAwaitsTradeNo(ctx, client, order.ID)
	require.NoError(t, err)
	require.True(t, held)
	replacement := &paymentOrderLifecycleQueryProvider{key: payment.TypeAlipay,
		resp: &payment.QueryOrderResponse{TradeNo: "replacement-trade-number", Status: payment.ProviderStatusPaid, Amount: 88}}
	registry.Register(replacement)
	_, err = svc.reconcileExpiredEasyPayHolds(ctx, easyPayHoldCursor{})
	require.NoError(t, err)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	t.Logf("after replacement provider: status=%s tradeNo=%s balance=%v", reloaded.Status, reloaded.PaymentTradeNo, userRepo.getByIDUser.Balance)
	require.Equal(t, OrderStatusPending, reloaded.Status)
	require.Zero(t, replacement.queryCalls, "the replacement must never be queried for the held order")
	require.Empty(t, reloaded.PaymentTradeNo)
	require.Zero(t, userRepo.getByIDUser.Balance)

	var newMerchantQueries atomic.Int32
	newGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newMerchantQueries.Add(1)
		_, _ = fmt.Fprint(w, `{"code":1,"status":1,"money":"88.00","pid":"another-merchant","trade_no":"wrong-merchant-trade"}`)
	}))
	defer newGateway.Close()
	anotherMerchant, err := paymentprovider.NewEasyPay("current", map[string]string{
		"apiBase": newGateway.URL, "pid": "another-merchant", "pkey": "another-synthetic-fixture",
		"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
	})
	require.NoError(t, err)
	registry.Register(anotherMerchant)
	_, err = svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, order.UserID)
	require.NoError(t, err)
	require.ErrorIs(t, svc.HandleVerifiedPaymentNotification(ctx, &payment.PaymentNotification{
		OrderID: order.OutTradeNo, TradeNo: "wrong-merchant-trade", Amount: 88,
		Status: payment.NotificationStatusSuccess, Metadata: anotherMerchant.MerchantIdentityMetadata(),
	}, anotherMerchant), ErrPaymentUpstreamUnconfirmed)
	require.Zero(t, newMerchantQueries.Load(), "another EasyPay merchant cannot confirm the hold")
	require.Zero(t, userRepo.getByIDUser.Balance)

	registry.Register(easyPay)
	originalTradeNumberReady.Store(true)
	for i := 0; i < 2; i++ {
		_, err = svc.reconcileExpiredEasyPayHolds(ctx, easyPayHoldCursor{})
		require.NoError(t, err)
	}
	reloaded, err = client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "original-trade", reloaded.PaymentTradeNo)
	require.Equal(t, 88.0, userRepo.getByIDUser.Balance)
}

func TestEasyPayHoldSweepFinishesRoundBeforeNewArrivals(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	userID := createPaymentOrderSeatUser(t, ctx, client, "hold-rotation@example.com")
	sweepCancellation := make(chan context.CancelFunc, 1)
	attemptedOrders := make(chan string, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel := <-sweepCancellation
		attemptedOrders <- r.URL.Query().Get("out_trade_no")
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer gateway.Close()
	provider, err := paymentprovider.NewEasyPay("rotating-merchant", map[string]string{
		"apiBase": gateway.URL, "pid": "merchant", "pkey": "synthetic-fixture",
		"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
	})
	require.NoError(t, err)
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	createHold := func(reference string) {
		order, err := client.PaymentOrder.Create().
			SetUserID(userID).
			SetUserEmail("hold-rotation@example.com").
			SetUserName("hold-rotation").
			SetAmount(88).
			SetPayAmount(88).
			SetFeeRate(0).
			SetRechargeCode(reference).
			SetOutTradeNo(reference).
			SetPaymentType(payment.TypeAlipay).
			SetPaymentTradeNo("").
			SetOrderType(payment.OrderTypeBalance).
			SetStatus(OrderStatusPending).
			SetExpiresAt(time.Now().Add(-time.Minute)).
			SetClientIP("127.0.0.1").
			SetSrcHost("api.example.com").
			Save(ctx)
		require.NoError(t, err)
		require.NoError(t, svc.markEasyPayAwaitingTradeNo(ctx, order.ID, provider))
	}

	var cursor easyPayHoldCursor
	runSweep := func(want string) {
		sweepCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		sweepCancellation <- cancel
		cursor, err = svc.reconcileExpiredEasyPayHolds(sweepCtx, cursor)
		require.NoError(t, err)
		select {
		case attempted := <-attemptedOrders:
			require.Equal(t, want, attempted)
		default:
			t.Fatal("the sweep did not query a held order")
		}
	}

	createHold("sub2_oldest_hold")
	createHold("sub2_next_hold")
	runSweep("sub2_oldest_hold")
	createHold("sub2_new_hold")
	runSweep("sub2_next_hold")
	createHold("sub2_newer_hold")
	runSweep("sub2_oldest_hold")
}

func TestEasyPayWebhookChecksQueriedAmountAgainstOrder(t *testing.T) {
	for _, tradeNo := range []string{"", "gateway-trade"} {
		t.Run("trade-no="+tradeNo, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			userID := createPaymentOrderSeatUser(t, ctx, client, "gateway-amount@example.com")
			order, err := client.PaymentOrder.Create().
				SetUserID(userID).
				SetUserEmail("gateway-amount@example.com").
				SetUserName("gateway-amount").
				SetAmount(88).
				SetPayAmount(88).
				SetFeeRate(0).
				SetRechargeCode("GATEWAY-AMOUNT").
				SetOutTradeNo("sub2_gateway_amount").
				SetPaymentType(payment.TypeEasyPay).
				SetPaymentTradeNo("").
				SetOrderType(payment.OrderTypeBalance).
				SetStatus(OrderStatusPending).
				SetExpiresAt(time.Now().Add(time.Minute)).
				SetClientIP("127.0.0.1").
				SetSrcHost("api.example.com").
				Save(ctx)
			require.NoError(t, err)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"code":1,"status":1,"money":"87.988","trade_no":%q}`, tradeNo)
			}))
			defer gateway.Close()
			provider, err := paymentprovider.NewEasyPay("authoritative-amount", map[string]string{
				"apiBase": gateway.URL, "pid": "merchant", "pkey": "synthetic-fixture",
				"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
			})
			require.NoError(t, err)
			registry := payment.NewRegistry()
			registry.Register(provider)
			userRepo := &mockUserRepo{getByIDUser: &User{ID: userID}}
			userRepo.updateBalanceFn = func(_ context.Context, _ int64, amount float64) error {
				userRepo.getByIDUser.Balance += amount
				return nil
			}
			redeemRepo := &paymentOrderLifecycleRedeemRepo{codesByCode: map[string]*RedeemCode{
				order.RechargeCode: {ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUnused},
			}}
			redeemService := NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
			svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true, redeemService: redeemService, userRepo: userRepo}
			// The signed amount is within the order tolerance and the queried
			// amount is within the notification tolerance. The queried amount
			// is outside the order tolerance.
			err = svc.HandleVerifiedPaymentNotification(ctx, &payment.PaymentNotification{
				OrderID: order.OutTradeNo, TradeNo: tradeNo, Amount: 87.992,
				Status: payment.NotificationStatusSuccess, Metadata: provider.MerchantIdentityMetadata(),
			}, provider)
			require.ErrorIs(t, err, ErrPaymentUpstreamUnconfirmed)
			stored, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, stored.Status)
			require.Empty(t, stored.PaymentTradeNo)
			held, err := paymentAwaitsTradeNo(ctx, client, order.ID)
			require.NoError(t, err)
			require.False(t, held, "the authoritative amount never confirmed this order")
		})
	}
}

type pausedLifecyclePaymentQuery struct {
	paymentOrderLifecycleQueryProvider
	started chan struct{}
	release chan struct{}
}

func (p *pausedLifecyclePaymentQuery) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	close(p.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return p.paymentOrderLifecycleQueryProvider.QueryOrder(ctx, tradeNo)
	}
}

func TestPendingPaymentKeepsTradeReferenceWhenHoldAppearsDuringVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := newPaymentOrderLifecycleTestClient(t)
	userID := createPaymentOrderSeatUser(t, ctx, client, "hold-during-payment-check@example.com")
	order, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("hold-during-payment-check@example.com").
		SetUserName("hold-during-payment-check").
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("HOLD-DURING-CHECK").
		SetOutTradeNo("sub2_hold_during_payment_check").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Minute)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	query := &pausedLifecyclePaymentQuery{
		paymentOrderLifecycleQueryProvider: paymentOrderLifecycleQueryProvider{
			key: payment.TypeAlipay,
			resp: &payment.QueryOrderResponse{
				Status: payment.ProviderStatusPaid, TradeNo: "later-reference", Amount: 88,
			},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	registry := payment.NewRegistry()
	registry.Register(query)
	original, err := paymentprovider.NewEasyPay("fixture-original", map[string]string{
		"apiBase": "https://fixture.invalid", "pid": "fixture-merchant", "pkey": "fixture-key",
		"notifyUrl": "https://fixture.invalid/notify", "returnUrl": "https://fixture.invalid/return",
	})
	require.NoError(t, err)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	done := make(chan error, 1)
	go func() {
		_, verifyErr := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, userID)
		done <- verifyErr
	}()
	select {
	case <-query.started:
	case <-ctx.Done():
		t.Fatal("verification did not reach the payment query")
	}
	require.NoError(t, svc.markEasyPayAwaitingTradeNo(ctx, order.ID, original))
	close(query.release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("verification did not finish")
	}

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, stored.Status)
	require.Empty(t, stored.PaymentTradeNo)
	require.Equal(t, 88.0, stored.PayAmount)
}

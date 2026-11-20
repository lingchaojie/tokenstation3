package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
)

// --- Cancel & Expire ---

// Cancel rate limit configuration constants.
const (
	rateLimitUnitDay                  = "day"
	rateLimitUnitMinute               = "minute"
	rateLimitUnitHour                 = "hour"
	rateLimitModeFixed                = "fixed"
	checkPaidResultAlreadyPaid        = "already_paid"
	checkPaidResultCancelled          = "cancelled"
	checkPaidResultPaidUnconfirmed    = "paid_unconfirmed"
	paymentAwaitingTradeNoAuditAction = "PAYMENT_AWAITING_TRADE_NO"

	pendingProviderReconcileLimit = 20
)

type checkPaidOptions struct {
	cancelIfUnpaid bool
}

func (s *PaymentService) checkCancelRateLimit(ctx context.Context, userID int64, cfg *PaymentConfig) error {
	if !cfg.CancelRateLimitEnabled || cfg.CancelRateLimitMax <= 0 {
		return nil
	}
	windowStart := cancelRateLimitWindowStart(cfg)
	operator := fmt.Sprintf("user:%d", userID)
	count, err := s.entClient.PaymentAuditLog.Query().
		Where(
			paymentauditlog.ActionEQ("ORDER_CANCELLED"),
			paymentauditlog.OperatorEQ(operator),
			paymentauditlog.CreatedAtGTE(windowStart),
		).Count(ctx)
	if err != nil {
		slog.Error("check cancel rate limit failed", "userID", userID, "error", err)
		return nil // fail open
	}
	if count >= cfg.CancelRateLimitMax {
		return infraerrors.TooManyRequests("CANCEL_RATE_LIMITED", "cancel rate limited").
			WithMetadata(map[string]string{
				"max":    strconv.Itoa(cfg.CancelRateLimitMax),
				"window": strconv.Itoa(cfg.CancelRateLimitWindow),
				"unit":   cfg.CancelRateLimitUnit,
			})
	}
	return nil
}

func cancelRateLimitWindowStart(cfg *PaymentConfig) time.Time {
	now := time.Now()
	w := cfg.CancelRateLimitWindow
	if w <= 0 {
		w = 1
	}
	unit := cfg.CancelRateLimitUnit
	if unit == "" {
		unit = rateLimitUnitDay
	}
	if cfg.CancelRateLimitMode == rateLimitModeFixed {
		switch unit {
		case rateLimitUnitMinute:
			t := now.Truncate(time.Minute)
			return t.Add(-time.Duration(w-1) * time.Minute)
		case rateLimitUnitDay:
			y, m, d := now.Date()
			t := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
			return t.AddDate(0, 0, -(w - 1))
		default: // hour
			t := now.Truncate(time.Hour)
			return t.Add(-time.Duration(w-1) * time.Hour)
		}
	}
	// rolling window
	switch unit {
	case rateLimitUnitMinute:
		return now.Add(-time.Duration(w) * time.Minute)
	case rateLimitUnitDay:
		return now.AddDate(0, 0, -w)
	default: // hour
		return now.Add(-time.Duration(w) * time.Hour)
	}
}

func (s *PaymentService) CancelOrder(ctx context.Context, orderID, userID int64) (string, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return "", infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.cancelPendingOrder(ctx, o, fmt.Sprintf("user:%d", userID), "user cancelled order")
}

func (s *PaymentService) AdminCancelOrder(ctx context.Context, orderID int64) (string, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.cancelPendingOrder(ctx, o, "admin", "admin cancelled order")
}

func (s *PaymentService) cancelPendingOrder(ctx context.Context, o *dbent.PaymentOrder, operator, detail string) (string, error) {
	outcome, err := s.cancelCore(ctx, o, OrderStatusCancelled, operator, detail)
	if err != nil {
		return "", err
	}
	switch outcome {
	case checkPaidResultPaidUnconfirmed:
		return "", infraerrors.Conflict("PAYMENT_CONFIRMATION_PENDING", "EasyPay reports this order as paid. The order is awaiting a transaction number and cannot be cancelled.")
	case checkPaidResultAlreadyPaid:
		if !orderMayHaveEasyPayConfirmationHold(o) {
			return outcome, nil
		}
		held, err := paymentAwaitsTradeNo(ctx, s.entClient, o.ID)
		if err != nil {
			return "", err
		}
		if held {
			return "", infraerrors.Conflict("ORDER_ALREADY_PAID", "This order has already been paid and cannot be cancelled.")
		}
	}
	return outcome, nil
}

func (s *PaymentService) cancelCore(ctx context.Context, o *dbent.PaymentOrder, fs, op, ad string) (string, error) {
	if o.PaymentTradeNo != "" || o.PaymentType != "" {
		switch result := s.checkPaid(ctx, o); result {
		case checkPaidResultAlreadyPaid, checkPaidResultPaidUnconfirmed:
			return result, nil
		}
	}
	client := s.entClient
	var tx *dbent.Tx
	if orderMayHaveEasyPayConfirmationHold(o) {
		var err error
		tx, err = client.Tx(ctx)
		if err != nil {
			return "", fmt.Errorf("begin order cancellation: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}
	c, err := client.PaymentOrder.Update().Where(paymentorder.IDEQ(o.ID), paymentorder.StatusEQ(OrderStatusPending)).SetStatus(fs).Save(ctx)
	if err != nil {
		return "", fmt.Errorf("update order status: %w", err)
	}
	if c > 0 && tx != nil {
		// Query the hold after claiming the order row so this transaction
		// sees evidence committed by a concurrent verifier.
		held, err := paymentAwaitsTradeNo(ctx, tx.Client(), o.ID)
		if err != nil {
			return "", err
		}
		if held {
			return checkPaidResultPaidUnconfirmed, nil
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit order cancellation: %w", err)
		}
	}
	if c > 0 {
		auditAction := "ORDER_CANCELLED"
		if fs == OrderStatusExpired {
			auditAction = "ORDER_EXPIRED"
		}
		s.writeAuditLog(ctx, o.ID, auditAction, op, map[string]any{"detail": ad})
	}
	return checkPaidResultCancelled, nil
}

func orderMayHaveEasyPayConfirmationHold(order *dbent.PaymentOrder) bool {
	snapshot := psOrderProviderSnapshot(order)
	if snapshot != nil && snapshot.ProviderKey != "" {
		return strings.EqualFold(snapshot.ProviderKey, payment.TypeEasyPay)
	}
	providerKey := strings.TrimSpace(psStringValue(order.ProviderKey))
	// Legacy orders can share a visible payment method across merchants.
	// Their current registry mapping may differ from the merchant that
	// recorded the hold.
	return providerKey == "" || strings.EqualFold(providerKey, payment.TypeEasyPay)
}

func paymentAwaitsTradeNo(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	held, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ(paymentAwaitingTradeNoAuditAction),
	).Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("read EasyPay confirmation hold: %w", err)
	}
	return held, nil
}

func (s *PaymentService) markEasyPayAwaitingTradeNo(ctx context.Context, orderID int64, prov payment.Provider) error {
	merchant := confirmedEasyPayMerchant(prov)
	if merchant.Fingerprint == "" {
		return fmt.Errorf("%w: EasyPay confirmation merchant is unknown", ErrPaymentUpstreamUnconfirmed)
	}
	detail, err := json.Marshal(struct {
		easyPayConfirmationMerchant
		Detail string `json:"detail"`
	}{merchant, "EasyPay confirmed the payment amount. The transaction number is pending."})
	if err != nil {
		return err
	}
	held, err := paymentAwaitsTradeNo(ctx, s.entClient, orderID)
	if err != nil || held {
		return err
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin EasyPay confirmation hold: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize with cancellation and expiry using the same order row.
	// A repeated observation rolls back without changing the order.
	claimed, err := tx.PaymentOrder.Update().Where(
		paymentorder.IDEQ(orderID), paymentorder.StatusEQ(OrderStatusPending),
	).SetStatus(OrderStatusPending).Save(ctx)
	if err != nil {
		return fmt.Errorf("claim pending order for EasyPay confirmation hold: %w", err)
	}
	if claimed == 0 {
		return nil
	}
	held, err = paymentAwaitsTradeNo(ctx, tx.Client(), orderID)
	if err != nil || held {
		return err
	}
	if _, err := tx.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction(paymentAwaitingTradeNoAuditAction).
		SetOperator(payment.TypeEasyPay).
		SetDetail(string(detail)).
		Save(ctx); err != nil {
		return fmt.Errorf("record EasyPay confirmation hold: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit EasyPay confirmation hold: %w", err)
	}
	return nil
}

func (s *PaymentService) checkPaid(ctx context.Context, o *dbent.PaymentOrder) string {
	return s.checkPaidWithOptions(ctx, o, checkPaidOptions{cancelIfUnpaid: true})
}

func (s *PaymentService) reconcilePaid(ctx context.Context, o *dbent.PaymentOrder) string {
	return s.checkPaidWithOptions(ctx, o, checkPaidOptions{})
}

func (s *PaymentService) checkPaidWithOptions(ctx context.Context, o *dbent.PaymentOrder, opts checkPaidOptions) string {
	var merchant *easyPayConfirmationMerchant
	if o.Status == OrderStatusPending && orderMayHaveEasyPayConfirmationHold(o) {
		var err error
		merchant, err = findEasyPayConfirmationMerchant(ctx, s.entClient, o.ID)
		if err != nil {
			slog.Error("read EasyPay confirmation hold failed", "orderID", o.ID, "error", err)
			return ""
		}
	}
	var prov payment.Provider
	if merchant != nil {
		var err error
		prov, err = s.heldEasyPayProvider(ctx, o, *merchant)
		if err != nil {
			return checkPaidResultPaidUnconfirmed
		}
	} else {
		var err error
		prov, err = s.getOrderProvider(ctx, o)
		if err != nil {
			return ""
		}
	}
	queryRef := paymentOrderQueryReference(o, prov)
	if queryRef == "" {
		return ""
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	if err != nil {
		slog.Warn("query upstream failed", "orderID", o.ID, "error", err)
		return ""
	}
	if resp == nil {
		return ""
	}
	if resp.Status == payment.ProviderStatusPaid && !isValidProviderAmount(resp.Amount) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", prov.ProviderKey(), map[string]any{
			"expected": o.PayAmount,
			"paid":     resp.Amount,
			"tradeNo":  resp.TradeNo,
			"queryRef": queryRef,
		})
		slog.Warn("query upstream returned invalid paid amount", "orderID", o.ID, "queryRef", queryRef, "paid", resp.Amount)
		retriedResp, retryOK := requeryPaidOrderOnce(ctx, prov, queryRef)
		if !retryOK {
			return ""
		}
		resp = retriedResp
	}
	if resp.Status == payment.ProviderStatusPaidUnconfirmed && prov.ProviderKey() == payment.TypeEasyPay {
		if !easyPayQueryMatchesOrder(o, resp) {
			return ""
		}
		if o.Status == OrderStatusPending {
			if err := s.markEasyPayAwaitingTradeNo(ctx, o.ID, prov); err != nil {
				slog.Error("record EasyPay confirmation hold failed", "orderID", o.ID, "error", err)
			}
		}
		return checkPaidResultPaidUnconfirmed
	}
	if resp.Status == payment.ProviderStatusPaid {
		notificationTradeNo := o.PaymentTradeNo
		upstreamTradeNo := strings.TrimSpace(resp.TradeNo)
		easyPay := prov.ProviderKey() == payment.TypeEasyPay
		if easyPay {
			if upstreamTradeNo == "" {
				return ""
			}
			// EasyPay supplies the actual field even when it equals the
			// site's lookup reference.
			notificationTradeNo = upstreamTradeNo
		}
		if paymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, o.PaymentTradeNo) {
			notificationTradeNo = upstreamTradeNo
			// A pending legacy order may acquire an EasyPay hold during
			// the query. Settlement performs its trade number write under
			// the same order lock as that hold.
			canBackfill := o.Status != OrderStatusPending || !orderMayHaveEasyPayConfirmationHold(o)
			if canBackfill && (!easyPay || (o.Status == OrderStatusExpired && easyPayQueryMatchesOrder(o, resp))) {
				// Keep active recovery of an expired order when a distinct,
				// confirmed trade number refreshed its grace period.
				if _, updateErr := s.entClient.PaymentOrder.Update().
					Where(paymentorder.IDEQ(o.ID), paymentorder.StatusEQ(o.Status)).
					SetPaymentTradeNo(upstreamTradeNo).
					Save(ctx); updateErr != nil {
					slog.Error("persist upstream trade no during checkPaid failed", "orderID", o.ID, "tradeNo", upstreamTradeNo, "error", updateErr)
				}
			}
		}
		if err := s.handlePaymentNotification(ctx, &payment.PaymentNotification{TradeNo: notificationTradeNo, OrderID: o.OutTradeNo, Amount: resp.Amount, Status: payment.ProviderStatusSuccess, Metadata: resp.Metadata}, prov.ProviderKey(), prov, false); err != nil {
			if errors.Is(err, ErrPaymentUpstreamUnconfirmed) {
				return checkPaidResultPaidUnconfirmed
			}
			slog.Error("fulfillment failed during checkPaid", "orderID", o.ID, "error", err)
			// Still return already_paid — order was paid, fulfillment can be retried
		}
		return checkPaidResultAlreadyPaid
	}
	if !opts.cancelIfUnpaid {
		return ""
	}
	if cp, ok := prov.(payment.CancelableProvider); ok {
		finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
		_ = cp.CancelPayment(ctx, queryRef)
		finishProviderCall()
	}
	return ""
}

func easyPayQueryMatchesOrder(order *dbent.PaymentOrder, resp *payment.QueryOrderResponse) bool {
	if !paymentOrderAmountMatches(order, resp.Amount) {
		return false
	}
	if err := validateProviderNotificationMetadata(order, payment.TypeEasyPay, resp.Metadata); err != nil {
		slog.Warn("EasyPay confirmation merchant mismatch", "orderID", order.ID, "error", err)
		return false
	}
	return true
}

func requeryPaidOrderOnce(ctx context.Context, prov payment.Provider, queryRef string) (*payment.QueryOrderResponse, bool) {
	if prov == nil || strings.TrimSpace(queryRef) == "" {
		return nil, false
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	if err != nil {
		slog.Warn("query upstream retry failed", "queryRef", queryRef, "error", err)
		return nil, false
	}
	if resp == nil || !isValidProviderAmount(resp.Amount) {
		return nil, false
	}
	if resp.Status == payment.ProviderStatusPaid ||
		(prov.ProviderKey() == payment.TypeEasyPay && resp.Status == payment.ProviderStatusPaidUnconfirmed) {
		return resp, true
	}
	return nil, false
}

func paymentOrderQueryReference(order *dbent.PaymentOrder, prov payment.Provider) string {
	if order == nil {
		return ""
	}

	providerKey := ""
	if prov != nil {
		providerKey = strings.TrimSpace(prov.ProviderKey())
	}
	if providerKey == "" {
		if snapshot := psOrderProviderSnapshot(order); snapshot != nil {
			providerKey = strings.TrimSpace(snapshot.ProviderKey)
		}
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(psStringValue(order.ProviderKey))
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(order.PaymentType)
	}

	switch payment.GetBasePaymentType(providerKey) {
	case payment.TypeAlipay, payment.TypeEasyPay, payment.TypeIkunPay, payment.TypeWxpay:
		return strings.TrimSpace(order.OutTradeNo)
	default:
		if tradeNo := strings.TrimSpace(order.PaymentTradeNo); tradeNo != "" {
			return tradeNo
		}
		return strings.TrimSpace(order.OutTradeNo)
	}
}

func paymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, currentTradeNo string) bool {
	upstreamTradeNo = strings.TrimSpace(upstreamTradeNo)
	if upstreamTradeNo == "" {
		return false
	}
	if strings.EqualFold(upstreamTradeNo, strings.TrimSpace(currentTradeNo)) {
		return false
	}
	if strings.EqualFold(upstreamTradeNo, strings.TrimSpace(queryRef)) {
		return false
	}
	return true
}

// VerifyOrderByOutTradeNo actively queries the upstream provider to check
// if a payment was made, and processes it if so. This handles the case where
// the provider's notify callback was missed (e.g. EasyPay popup mode).
func (s *PaymentService) VerifyOrderByOutTradeNo(ctx context.Context, outTradeNo string, userID int64) (*dbent.PaymentOrder, error) {
	outTradeNo, err := normalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNo(outTradeNo)).
		Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	// Only verify orders that are still pending or recently expired
	if o.Status == OrderStatusPending || o.Status == OrderStatusExpired {
		result := s.reconcilePaid(ctx, o)
		if result == checkPaidResultAlreadyPaid {
			// Reload order to get updated status
			o, err = s.entClient.PaymentOrder.Get(ctx, o.ID)
			if err != nil {
				return nil, fmt.Errorf("reload order: %w", err)
			}
		}
	}
	return o, nil
}

// ReconcilePendingWxpayOrders preserves the previous service method name for
// callers while reconciling every provider that supports reliable order query.
func (s *PaymentService) ReconcilePendingWxpayOrders(ctx context.Context) (int, error) {
	return s.ReconcilePendingProviderOrders(ctx)
}

// ReconcilePendingPaymentOrders keeps the upstream entry point compatible with
// the shared WeChat, IkunPay and Alipay reconciliation batch.
func (s *PaymentService) ReconcilePendingPaymentOrders(ctx context.Context) (int, error) {
	return s.ReconcilePendingProviderOrders(ctx)
}

// ReconcilePendingProviderOrders actively checks recent pending orders for
// providers where missed notifications should not wait until order expiry.
func (s *PaymentService) ReconcilePendingProviderOrders(ctx context.Context) (int, error) {
	now := time.Now()
	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.StatusEQ(OrderStatusPending),
			paymentorder.ExpiresAtGT(now),
			paymentorder.Or(
				paymentorder.PaymentTypeEQ(payment.TypeWxpay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeWxpay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeWxpay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeWxpay+"_"),
				paymentorder.PaymentTypeEQ(payment.TypeIkunPay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeIkunPay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeIkunPay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeIkunPay+"_"),
				paymentorder.PaymentTypeEQ(payment.TypeAlipay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeAlipay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeAlipay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeAlipay+"_"),
			),
		).
		Order(dbent.Asc(paymentorder.FieldCreatedAt)).
		Limit(pendingProviderReconcileLimit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query pending provider orders: %w", err)
	}

	recovered := 0
	for _, order := range orders {
		if s.reconcilePaid(ctx, order) == checkPaidResultAlreadyPaid {
			recovered++
		}
	}
	return recovered, nil
}

// VerifyOrderPublic returns the currently persisted public order state without
// triggering any upstream reconciliation. Signed resume-token recovery is the
// only public recovery path allowed to query upstream state.
func (s *PaymentService) VerifyOrderPublic(ctx context.Context, outTradeNo string) (*dbent.PaymentOrder, error) {
	outTradeNo, err := normalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNo(outTradeNo)).
		Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}

func normalizeOrderLookupOutTradeNo(raw string) (string, error) {
	outTradeNo := strings.TrimSpace(raw)
	if outTradeNo == "" {
		return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is required")
	}
	if len(outTradeNo) > 64 {
		return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is invalid")
	}
	for _, ch := range outTradeNo {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '_' || ch == '-':
		default:
			return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is invalid")
		}
	}
	return outTradeNo, nil
}

func (s *PaymentService) ExpireTimedOutOrders(ctx context.Context) (int, error) {
	orders, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.StatusEQ(OrderStatusPending), paymentorder.ExpiresAtLTE(time.Now()), func(orders *entsql.Selector) {
			orders.Where(entsql.NotExists(easyPayConfirmationHolds(orders)))
		}).
		Order(dbent.Asc(paymentorder.FieldExpiresAt), dbent.Asc(paymentorder.FieldID)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query expired: %w", err)
	}
	n := 0
	for _, o := range orders {
		if ctx.Err() != nil {
			break
		}
		// Check upstream payment status before expiring — the user may have
		// paid just before timeout and the webhook hasn't arrived yet.
		outcome, _ := s.cancelCore(ctx, o, OrderStatusExpired, "system", "order expired")
		if outcome == checkPaidResultAlreadyPaid {
			slog.Info("order was paid during expiry", "orderID", o.ID)
			continue
		}
		if outcome == checkPaidResultCancelled {
			n++
		}
	}
	return n, nil
}

type easyPayHoldCursor struct {
	afterID   int64
	throughID int64
}

// reconcileExpiredEasyPayHolds gives paid orders their own retry budget.
func (s *PaymentService) reconcileExpiredEasyPayHolds(ctx context.Context, cursor easyPayHoldCursor) (easyPayHoldCursor, error) {
	query := s.entClient.PaymentOrder.Query().
		Where(paymentorder.StatusEQ(OrderStatusPending), paymentorder.ExpiresAtLTE(time.Now()), func(orders *entsql.Selector) {
			orders.Where(entsql.Exists(easyPayConfirmationHolds(orders)))
		})
	if cursor.throughID == 0 {
		// Bound the round so new arrivals cannot continually delay
		// another attempt for an older held order.
		id, err := query.Clone().Order(dbent.Desc(paymentorder.FieldID)).FirstID(ctx)
		if dbent.IsNotFound(err) {
			return cursor, nil
		}
		if err != nil {
			return cursor, fmt.Errorf("bound expired EasyPay confirmation holds: %w", err)
		}
		cursor.throughID = id
	}
	orders, err := query.Where(paymentorder.IDGT(cursor.afterID), paymentorder.IDLTE(cursor.throughID)).
		Order(dbent.Asc(paymentorder.FieldID)).
		Limit(pendingProviderReconcileLimit).
		All(ctx)
	if err != nil {
		return cursor, fmt.Errorf("query expired EasyPay confirmation holds: %w", err)
	}
	for _, order := range orders {
		if ctx.Err() != nil {
			return cursor, nil
		}
		// A merchant that spends the entire deadline must not be first next time.
		cursor.afterID = order.ID
		s.reconcilePaid(ctx, order)
	}
	if cursor.afterID == cursor.throughID || len(orders) < pendingProviderReconcileLimit {
		return easyPayHoldCursor{}, nil
	}
	return cursor, nil
}

func easyPayConfirmationHolds(orders *entsql.Selector) *entsql.Selector {
	dialect := entsql.Dialect(orders.Dialect())
	audit := dialect.Table(paymentauditlog.Table)
	return dialect.Select(audit.C(paymentauditlog.FieldOrderID)).From(audit).
		Where(entsql.And(
			entsql.EQ(audit.C(paymentauditlog.FieldAction), paymentAwaitingTradeNoAuditAction),
			entsql.P(func(b *entsql.Builder) {
				b.Ident(audit.C(paymentauditlog.FieldOrderID)).WriteOp(entsql.OpEQ).
					WriteString("CAST(").Ident(orders.C(paymentorder.FieldID)).WriteString(" AS TEXT)")
			}),
		))
}

// getOrderProvider creates a provider using the order's original instance config.
// Falls back to registry lookup if instance ID is missing (legacy orders).
func (s *PaymentService) getOrderProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, error) {
	inst, err := s.getOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, fmt.Errorf("load order provider instance: %w", err)
	}
	if inst != nil {
		return s.createProviderFromInstance(ctx, inst)
	}
	if !paymentOrderAllowsRegistryFallback(o) {
		return nil, fmt.Errorf("order %d provider instance is unresolved", o.ID)
	}
	providerKey := paymentOrderFallbackProviderKey(s.registry, o)
	if providerKey == "" {
		return nil, fmt.Errorf("order %d provider fallback key is missing", o.ID)
	}
	if !s.webhookRegistryFallbackAllowed(ctx, providerKey) {
		return nil, fmt.Errorf("order %d provider fallback is ambiguous for %s", o.ID, providerKey)
	}
	s.EnsureProviders(ctx)
	return s.registry.GetProvider(o.PaymentType)
}

func paymentOrderAllowsRegistryFallback(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	if psOrderProviderSnapshot(order) != nil {
		return false
	}
	if strings.TrimSpace(psStringValue(order.ProviderInstanceID)) != "" {
		return false
	}
	if strings.TrimSpace(psStringValue(order.ProviderKey)) != "" {
		return false
	}
	return true
}

func paymentOrderFallbackProviderKey(registry *payment.Registry, order *dbent.PaymentOrder) string {
	if order == nil {
		return ""
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(payment.PaymentType(order.PaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(payment.GetBasePaymentType(strings.TrimSpace(order.PaymentType)))
}

func (s *PaymentService) createProviderFromInstance(ctx context.Context, inst *dbent.PaymentProviderInstance) (payment.Provider, error) {
	if inst == nil {
		return nil, fmt.Errorf("payment provider instance is missing")
	}

	cfg, err := s.loadBalancer.GetInstanceConfig(ctx, int64(inst.ID))
	if err != nil {
		return nil, fmt.Errorf("load provider instance config: %w", err)
	}
	if inst.PaymentMode != "" {
		cfg["paymentMode"] = inst.PaymentMode
	}

	instID := strconv.FormatInt(int64(inst.ID), 10)
	prov, err := createPaymentProviderFromInstance(inst.ProviderKey, instID, cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider from instance: %w", err)
	}
	return prov, nil
}

func psStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

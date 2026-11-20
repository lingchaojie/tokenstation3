package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	paymentprovider "github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
)

// ErrOrderNotFound is returned by HandlePaymentNotification when the webhook
// references an out_trade_no that does not exist in our DB. Callers (webhook
// handlers) should treat this as a terminal, non-retryable condition and still
// respond with a 2xx success to the provider — otherwise the provider will keep
// retrying forever (e.g. when a foreign environment's webhook endpoint is
// misconfigured to point at us, or when our orders table has been wiped).
var ErrOrderNotFound = errors.New("payment order not found")

// ErrPaymentUpstreamUnconfirmed is retryable by the gateway.
var ErrPaymentUpstreamUnconfirmed = errors.New("payment upstream unconfirmed")

const paymentFulfillmentLeaseDuration = 5 * time.Minute

type paymentFulfillmentLease struct {
	version time.Time
}

// --- Payment Notification & Fulfillment ---

// HandlePaymentNotification applies a notification synthesized from a trusted
// upstream order query. Webhooks use HandleVerifiedPaymentNotification.
func (s *PaymentService) HandlePaymentNotification(ctx context.Context, n *payment.PaymentNotification, pk string) error {
	return s.handlePaymentNotification(ctx, n, pk, nil, false)
}

// HandleVerifiedPaymentNotification keeps verification and confirmation on the
// same gateway instance. Only the webhook path needs this additional query.
func (s *PaymentService) HandleVerifiedPaymentNotification(ctx context.Context, n *payment.PaymentNotification, prov payment.Provider) error {
	return s.handlePaymentNotification(ctx, n, prov.ProviderKey(), prov, true)
}

func (s *PaymentService) handlePaymentNotification(ctx context.Context, n *payment.PaymentNotification, pk string, prov payment.Provider, confirmUpstream bool) error {
	if n == nil || n.Status != payment.NotificationStatusSuccess {
		return nil
	}
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNo(n.OrderID)).Only(ctx)
	// Older gateway payloads used the database ID after the sub2_ prefix.
	if oid, ok := parseLegacyPaymentOrderID(n.OrderID, err); ok {
		o, err = s.entClient.PaymentOrder.Get(ctx, oid)
	}
	if dbent.IsNotFound(err) {
		return fmt.Errorf("%w: out_trade_no=%s", ErrOrderNotFound, n.OrderID)
	}
	if err != nil {
		return fmt.Errorf("lookup order failed for out_trade_no %s: %w", n.OrderID, err)
	}
	return s.confirmPayment(ctx, o, n, pk, prov, confirmUpstream)
}

func (s *PaymentService) confirmNotificationUpstream(ctx context.Context, order *dbent.PaymentOrder, prov payment.Provider, n *payment.PaymentNotification) error {
	notifiedTradeNo, notifiedAmount := n.TradeNo, n.Amount
	var err error
	if confirmer, ok := prov.(payment.NotificationConfirmer); ok {
		finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
		err = confirmer.ConfirmNotification(ctx, n)
		finishProviderCall()
	} else {
		err = errors.New("EasyPay provider cannot confirm notifications")
	}
	paidWithoutTradeNo := errors.Is(err, paymentprovider.ErrEasyPayPaidWithoutTradeNo)
	if (err == nil || paidWithoutTradeNo) && !paymentOrderAmountMatches(order, n.Amount) {
		err = fmt.Errorf("confirmed EasyPay amount %v differs from order amount %v", n.Amount, order.PayAmount)
		paidWithoutTradeNo = false
	}
	if err == nil {
		return nil
	}
	if order.Status == OrderStatusPending && paidWithoutTradeNo {
		if holdErr := s.markEasyPayAwaitingTradeNo(ctx, order.ID, prov); holdErr != nil {
			return fmt.Errorf("%w: %w: %w", ErrPaymentUpstreamUnconfirmed, err, holdErr)
		}
		return fmt.Errorf("%w: %w", ErrPaymentUpstreamUnconfirmed, err)
	}
	s.writeAuditLog(ctx, order.ID, "PAYMENT_UPSTREAM_UNCONFIRMED", prov.ProviderKey(), map[string]any{
		"detail":  err.Error(),
		"tradeNo": notifiedTradeNo,
		"amount":  notifiedAmount,
	})
	return fmt.Errorf("%w: %w", ErrPaymentUpstreamUnconfirmed, err)
}

func parseLegacyPaymentOrderID(orderID string, lookupErr error) (int64, bool) {
	if !dbent.IsNotFound(lookupErr) {
		return 0, false
	}
	orderID = strings.TrimSpace(orderID)
	if !strings.HasPrefix(orderID, orderIDPrefix) {
		return 0, false
	}
	trimmed := strings.TrimPrefix(orderID, orderIDPrefix)
	if trimmed == "" || trimmed == orderID {
		return 0, false
	}
	oid, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || oid <= 0 {
		return 0, false
	}
	return oid, true
}

func (s *PaymentService) confirmPayment(ctx context.Context, o *dbent.PaymentOrder, n *payment.PaymentNotification, pk string, prov payment.Provider, confirmUpstream bool) error {
	tradeNo, paid, metadata := n.TradeNo, n.Amount, n.Metadata
	merchant := confirmedEasyPayMerchant(prov)
	var held bool
	if o.Status == OrderStatusPending && orderMayHaveEasyPayConfirmationHold(o) {
		var err error
		held, err = verifyEasyPayConfirmationMerchant(ctx, s.entClient, o.ID, merchant)
		if err != nil {
			return err
		}
	}
	expectedProviderKey := payment.TypeEasyPay
	if !held {
		instanceProviderKey := ""
		if inst, instErr := s.getOrderProviderInstance(ctx, o); instErr == nil && inst != nil {
			instanceProviderKey = inst.ProviderKey
		}
		expectedProviderKey = expectedNotificationProviderKeyForOrder(s.registry, o, instanceProviderKey)
	}
	if expectedProviderKey != "" && strings.TrimSpace(pk) != "" && !strings.EqualFold(expectedProviderKey, strings.TrimSpace(pk)) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_PROVIDER_MISMATCH", pk, map[string]any{
			"expectedProvider": expectedProviderKey,
			"actualProvider":   pk,
			"tradeNo":          tradeNo,
		})
		return fmt.Errorf("provider mismatch: expected %s, got %s", expectedProviderKey, pk)
	}
	if err := validateProviderNotificationMetadata(o, pk, metadata); err != nil {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_PROVIDER_METADATA_MISMATCH", pk, map[string]any{
			"detail":  err.Error(),
			"tradeNo": tradeNo,
		})
		return err
	}
	if !isValidProviderAmount(paid) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", pk, map[string]any{
			"expected": o.PayAmount,
			"paid":     paid,
			"tradeNo":  tradeNo,
		})
		return fmt.Errorf("invalid paid amount from provider: %v", paid)
	}
	if math.Abs(paid-o.PayAmount) > paymentAmountToleranceForCurrency(PaymentOrderCurrency(o)) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_AMOUNT_MISMATCH", pk, map[string]any{"expected": o.PayAmount, "paid": paid, "tradeNo": tradeNo})
		return fmt.Errorf("amount mismatch: expected %s, got %s", strconv.FormatFloat(o.PayAmount, 'f', -1, 64), strconv.FormatFloat(paid, 'f', -1, 64))
	}
	if confirmUpstream && pk == payment.TypeEasyPay && o.PaidAt == nil &&
		o.Status != OrderStatusCompleted && !psIsRefundStatus(o.Status) {
		if err := s.confirmNotificationUpstream(ctx, o, prov, n); err != nil {
			return err
		}
		paid = n.Amount
	}
	return s.toPaid(ctx, o, n.TradeNo, paid, pk, merchant)
}

func paymentAmountToleranceForCurrency(currency string) float64 {
	minorUnit := payment.CurrencyMinorUnit(currency)
	if minorUnit <= 2 {
		return amountToleranceCNY
	}
	return math.Pow10(-minorUnit) / 2
}

func isValidProviderAmount(amount float64) bool {
	return amount > 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

func paymentOrderAmountMatches(order *dbent.PaymentOrder, amount float64) bool {
	return isValidProviderAmount(amount) &&
		math.Abs(amount-order.PayAmount) <= paymentAmountToleranceForCurrency(PaymentOrderCurrency(order))
}

func validateProviderNotificationMetadata(order *dbent.PaymentOrder, providerKey string, metadata map[string]string) error {
	return validateProviderSnapshotMetadata(order, providerKey, metadata)
}

func expectedNotificationProviderKey(registry *payment.Registry, orderPaymentType string, orderProviderKey string, instanceProviderKey string) string {
	if key := strings.TrimSpace(instanceProviderKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(orderProviderKey); key != "" {
		return key
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(payment.PaymentType(orderPaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(orderPaymentType)
}

func (s *PaymentService) toPaid(ctx context.Context, o *dbent.PaymentOrder, tradeNo string, paid float64, pk string, merchant easyPayConfirmationMerchant) error {
	previousStatus := o.Status
	now := time.Now()
	grace := now.Add(-paymentGraceMinutes * time.Minute)
	client := s.entClient
	var tx *dbent.Tx
	if previousStatus == OrderStatusPending && orderMayHaveEasyPayConfirmationHold(o) {
		var err error
		tx, err = client.Tx(ctx)
		if err != nil {
			return fmt.Errorf("begin payment claim: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}
	c, err := client.PaymentOrder.Update().Where(
		paymentorder.IDEQ(o.ID),
		paymentorder.Or(
			paymentorder.StatusEQ(OrderStatusPending),
			paymentorder.StatusEQ(OrderStatusCancelled),
			paymentorder.And(
				paymentorder.StatusEQ(OrderStatusExpired),
				paymentorder.UpdatedAtGTE(grace),
			),
		),
	).SetStatus(OrderStatusPaid).SetPayAmount(paid).SetPaymentTradeNo(tradeNo).SetPaidAt(now).ClearFailedAt().ClearFailedReason().Save(ctx)
	if err != nil {
		return fmt.Errorf("update to PAID: %w", err)
	}
	if tx != nil {
		if c > 0 {
			// A concurrent verification may have written a hold while
			// this request was waiting for the order row.
			if _, err := verifyEasyPayConfirmationMerchant(ctx, client, o.ID, merchant); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit payment claim: %w", err)
		}
	}
	if c == 0 {
		return s.alreadyProcessed(ctx, o)
	}
	if previousStatus == OrderStatusCancelled || previousStatus == OrderStatusExpired {
		slog.Info("order recovered from webhook payment success",
			"orderID", o.ID,
			"previousStatus", previousStatus,
			"tradeNo", tradeNo,
			"provider", pk,
		)
		s.writeAuditLog(ctx, o.ID, "ORDER_RECOVERED", pk, map[string]any{
			"previous_status": previousStatus,
			"tradeNo":         tradeNo,
			"paidAmount":      paid,
			"reason":          "webhook payment success received after order " + previousStatus,
		})
	}
	s.writeAuditLog(ctx, o.ID, "ORDER_PAID", pk, map[string]any{"tradeNo": tradeNo, "paidAmount": paid})
	return s.executeFulfillment(ctx, o.ID)
}

func (s *PaymentService) alreadyProcessed(ctx context.Context, o *dbent.PaymentOrder) error {
	cur, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return nil
	}
	switch cur.Status {
	case OrderStatusCompleted, OrderStatusRefunded:
		return nil
	case OrderStatusFailed, OrderStatusPaid, OrderStatusRecharging:
		return s.executeFulfillment(ctx, o.ID)
	case OrderStatusExpired:
		slog.Warn("webhook payment success for expired order beyond grace period",
			"orderID", o.ID,
			"status", cur.Status,
			"updatedAt", cur.UpdatedAt,
		)
		s.writeAuditLog(ctx, o.ID, "PAYMENT_AFTER_EXPIRY", "system", map[string]any{
			"status":    cur.Status,
			"updatedAt": cur.UpdatedAt,
			"reason":    "payment arrived after expiry grace period",
		})
		return nil
	default:
		return nil
	}
}

func (s *PaymentService) executeFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	switch o.OrderType {
	case payment.OrderTypeSubscription:
		return s.ExecuteSubscriptionFulfillment(ctx, oid)
	case payment.OrderTypeBalance:
		return s.ExecuteBalanceFulfillment(ctx, oid)
	default:
		// 不支持的订单类型不履约：标记 FAILED 后正常返回，避免支付回调持续 500 重试。
		s.rejectUnsupportedOrderType(ctx, o)
		return nil
	}
}

func isSupportedPaymentOrderType(orderType string) bool {
	return orderType == payment.OrderTypeBalance || orderType == payment.OrderTypeSubscription
}

func (s *PaymentService) rejectUnsupportedOrderType(ctx context.Context, o *dbent.PaymentOrder) {
	reason := "unsupported order type: " + o.OrderType
	c, err := s.entClient.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(o.ID),
			paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging),
		).
		SetStatus(OrderStatusFailed).SetFailedAt(time.Now()).SetFailedReason(reason).Save(ctx)
	if err != nil {
		slog.Error("mark unsupported order type FAILED", "orderID", o.ID, "error", err)
		return
	}
	if c > 0 {
		slog.Warn("payment order with unsupported order type not fulfilled", "orderID", o.ID, "orderType", o.OrderType)
		s.writeAuditLog(ctx, o.ID, "FULFILLMENT_FAILED", "system", map[string]any{"reason": reason})
	}
}

func (s *PaymentService) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.OrderType != payment.OrderTypeBalance {
		return infraerrors.BadRequest("INVALID_ORDER_TYPE", "order is not a balance order")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doBalance(ctx, o, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) acquirePaymentFulfillmentLease(ctx context.Context, o *dbent.PaymentOrder) (*paymentFulfillmentLease, error) {
	if o == nil {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-paymentFulfillmentLeaseDuration)
	updated, err := s.entClient.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(o.ID),
			paymentorder.Or(
				paymentorder.StatusIn(OrderStatusPaid, OrderStatusFailed),
				paymentorder.And(
					paymentorder.StatusEQ(OrderStatusRecharging),
					paymentorder.UpdatedAtLTE(staleBefore),
				),
			),
		).
		SetStatus(OrderStatusRecharging).
		SetUpdatedAt(now).
		ClearFailedAt().
		ClearFailedReason().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire fulfillment lease: %w", err)
	}
	if updated == 0 {
		current, getErr := s.entClient.PaymentOrder.Get(ctx, o.ID)
		if getErr != nil {
			return nil, fmt.Errorf("reload fulfillment lease: %w", getErr)
		}
		if current.Status == OrderStatusCompleted {
			return nil, nil
		}
		if current.Status == OrderStatusRecharging {
			return nil, infraerrors.Conflict("CONFLICT", "order is being processed")
		}
		return nil, infraerrors.Conflict("CONFLICT", "order status changed while acquiring fulfillment lease")
	}

	// Reload the persisted timestamp instead of trusting application clock precision.
	claimed, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return nil, fmt.Errorf("reload acquired fulfillment lease: %w", err)
	}
	if claimed.Status != OrderStatusRecharging {
		return nil, infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	return &paymentFulfillmentLease{version: claimed.UpdatedAt}, nil
}

// redeemAction represents the idempotency decision for balance fulfillment.
type redeemAction int

const (
	// redeemActionCreate: code does not exist — create it, then redeem.
	redeemActionCreate redeemAction = iota
	// redeemActionRedeem: code exists but is unused — skip creation, redeem only.
	redeemActionRedeem
	// redeemActionSkipCompleted: code exists and is already used — skip to mark completed.
	redeemActionSkipCompleted
)

// resolveRedeemAction decides the idempotency action based on an existing redeem code lookup.
// existing is the result of GetByCode; lookupErr is the error from that call.
func resolveRedeemAction(existing *RedeemCode, lookupErr error) (redeemAction, error) {
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrRedeemCodeNotFound) {
			return redeemActionCreate, nil
		}
		return redeemActionCreate, fmt.Errorf("lookup payment redeem code: %w", lookupErr)
	}
	if existing == nil {
		return redeemActionCreate, nil
	}
	if existing.IsUsed() {
		return redeemActionSkipCompleted, nil
	}
	return redeemActionRedeem, nil
}

func validatePaymentRedeemCode(o *dbent.PaymentOrder, code *RedeemCode) error {
	if o == nil || code == nil {
		return errors.New("payment redeem code validation requires an order and code")
	}
	if code.Code != o.RechargeCode {
		return fmt.Errorf("payment redeem code mismatch for order %d", o.ID)
	}
	if code.Type != RedeemTypeBalance {
		return fmt.Errorf("payment redeem code type mismatch for order %d: got %s", o.ID, code.Type)
	}
	if math.IsNaN(code.Value) || math.IsInf(code.Value, 0) || math.Abs(code.Value-o.Amount) > 1e-8 {
		return fmt.Errorf("payment redeem code amount mismatch for order %d: expected %.8f, got %.8f", o.ID, o.Amount, code.Value)
	}
	switch code.Status {
	case StatusUnused:
		if code.UsedBy != nil {
			return fmt.Errorf("unused payment redeem code has a user for order %d", o.ID)
		}
	case StatusUsed:
		if code.UsedBy == nil || *code.UsedBy != o.UserID {
			return fmt.Errorf("payment redeem code user mismatch for order %d", o.ID)
		}
	default:
		return fmt.Errorf("payment redeem code has invalid status for order %d: %s", o.ID, code.Status)
	}
	return nil
}

func (s *PaymentService) doBalance(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	// Idempotency: check if redeem code already exists (from a previous partial run)
	existing, lookupErr := s.redeemService.GetByCode(ctx, o.RechargeCode)
	action, err := resolveRedeemAction(existing, lookupErr)
	if err != nil {
		return err
	}
	if existing != nil {
		if err := validatePaymentRedeemCode(o, existing); err != nil {
			return err
		}
	}

	switch action {
	case redeemActionSkipCompleted:
		if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		// Code already created and redeemed — just mark completed
		return s.markCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
	case redeemActionCreate:
		rc := &RedeemCode{Code: o.RechargeCode, Type: RedeemTypeBalance, Value: o.Amount, Status: StatusUnused}
		if err := s.redeemService.CreateCode(ctx, rc); err != nil {
			return fmt.Errorf("create redeem code: %w", err)
		}
	case redeemActionRedeem:
		// Code exists but unused — skip creation, proceed to redeem
	}
	if _, err := s.redeemService.redeemForPaymentFulfillment(ctx, o.UserID, o.RechargeCode); err != nil {
		return fmt.Errorf("redeem balance: %w", err)
	}
	if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.markCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
}

func (s *PaymentService) markCompleted(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease, auditAction string) error {
	if lease == nil {
		return errors.New("missing payment fulfillment lease")
	}
	now := time.Now()
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(o.ID),
		paymentorder.StatusEQ(OrderStatusRecharging),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusCompleted).SetCompletedAt(now).Save(ctx)
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if updated == 0 {
		current, getErr := s.entClient.PaymentOrder.Get(ctx, o.ID)
		if getErr == nil && current.Status == OrderStatusCompleted {
			return nil
		}
		return infraerrors.Conflict("CONFLICT", "fulfillment lease was lost before completion")
	}
	if !s.hasAuditLog(ctx, o.ID, auditAction) {
		s.writeAuditLog(ctx, o.ID, auditAction, "system", map[string]any{
			"rechargeCode":   o.RechargeCode,
			"creditedAmount": o.Amount,
			"payAmount":      o.PayAmount,
		})
		s.dispatchPaymentFulfillmentNotification(o, auditAction)
	}
	return nil
}

func (s *PaymentService) dispatchPaymentFulfillmentNotification(o *dbent.PaymentOrder, auditAction string) {
	if s == nil || s.notificationEmailService == nil || o == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		defer cancel()
		var err error
		switch auditAction {
		case "RECHARGE_SUCCESS":
			err = s.sendBalanceRechargeSuccessNotification(ctx, o)
		case "SUBSCRIPTION_SUCCESS":
			err = s.sendSubscriptionPurchaseSuccessNotification(ctx, o)
		default:
			return
		}
		if err != nil {
			slog.Warn("payment fulfillment notification email failed", "order_id", o.ID, "action", auditAction, "err", err.Error())
		}
	}()
}

func (s *PaymentService) sendBalanceRechargeSuccessNotification(ctx context.Context, o *dbent.PaymentOrder) error {
	currentBalance := ""
	if s.userRepo != nil {
		if user, err := s.userRepo.GetByID(ctx, o.UserID); err == nil && user != nil {
			currentBalance = fmt.Sprintf("%.2f", user.Balance)
		}
	}
	return s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventBalanceRechargeSuccess,
		RecipientEmail: o.UserEmail,
		RecipientName:  firstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables: map[string]string{
			"recharge_amount": fmt.Sprintf("%.2f", o.Amount),
			"current_balance": currentBalance,
			"order_id":        strconv.FormatInt(o.ID, 10),
		},
	})
}

func (s *PaymentService) sendSubscriptionPurchaseSuccessNotification(ctx context.Context, o *dbent.PaymentOrder) error {
	variables := map[string]string{
		"subscription_group": "Subscription",
		"subscription_days":  "",
		"expiry_time":        "",
		"order_id":           strconv.FormatInt(o.ID, 10),
	}
	if o.SubscriptionDays != nil {
		variables["subscription_days"] = strconv.Itoa(*o.SubscriptionDays)
	}
	if o.SubscriptionGroupID != nil {
		if s.groupRepo != nil {
			if group, err := s.groupRepo.GetByID(ctx, *o.SubscriptionGroupID); err == nil && group != nil && strings.TrimSpace(group.Name) != "" {
				variables["subscription_group"] = group.Name
			}
		}
		if s.subscriptionSvc != nil {
			if sub, err := s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.SubscriptionGroupID); err == nil && sub != nil {
				variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
			}
		}
	} else if o.PlanID != nil && s.subscriptionSvc != nil {
		if sub, err := s.subscriptionSvc.GetActiveGenericSubscription(ctx, o.UserID); err == nil && sub != nil {
			if sub.PlanName != nil && strings.TrimSpace(*sub.PlanName) != "" {
				variables["subscription_group"] = *sub.PlanName
			}
			variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
		}
	}
	return s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionPurchaseSuccess,
		RecipientEmail: o.UserEmail,
		RecipientName:  firstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables:      variables,
	})
}

func (s *PaymentService) ExecuteSubscriptionFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	if o.SubscriptionDays == nil || (o.PlanID == nil && o.SubscriptionGroupID == nil) {
		return infraerrors.BadRequest("INVALID_STATUS", "missing subscription info")
	}
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doSub(ctx, o, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) doSub(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	days := *o.SubscriptionDays
	orderNote := paymentSubscriptionOrderNote(o.ID)

	// Idempotency: check durable completion marker first.
	if s.hasAuditLog(ctx, o.ID, "SUBSCRIPTION_SUCCESS") {
		slog.Info("subscription already assigned for order, skipping", "orderID", o.ID, "groupID", paymentOrderSubscriptionGroupIDLogValue(o))
		if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		return s.markCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
	}
	if s.subscriptionHasOrderNote(ctx, o.UserID, o.SubscriptionGroupID, orderNote) {
		slog.Info("subscription already contains payment order note, skipping", "orderID", o.ID, "groupID", paymentOrderSubscriptionGroupIDLogValue(o))
		if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		return s.markCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
	}
	if o.PlanID != nil {
		if err := s.doSubWithPlanSeat(ctx, o, days, orderNote); err != nil {
			return err
		}
		if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		return s.markCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
	}
	if o.SubscriptionGroupID == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "missing subscription group")
	}

	gid := *o.SubscriptionGroupID
	g, err := s.groupRepo.GetByID(ctx, gid)
	if err != nil || g.Status != payment.EntityStatusActive {
		return fmt.Errorf("group %d no longer exists or inactive", gid)
	}
	if !g.IsSubscriptionType() {
		return ErrGroupNotSubscriptionType
	}
	if err := s.ensurePaymentSubscriptionAssigned(ctx, o, gid, days); err != nil {
		return err
	}
	if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.markCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
}

func (s *PaymentService) doSubWithPlanSeat(ctx context.Context, o *dbent.PaymentOrder, days int, orderNote string) error {
	if o.SubscriptionGroupID != nil && *o.SubscriptionGroupID > 0 {
		gid := *o.SubscriptionGroupID
		g, err := s.groupRepo.GetByID(ctx, gid)
		if err != nil || g.Status != payment.EntityStatusActive {
			return fmt.Errorf("group %d no longer exists or inactive", gid)
		}
		if !g.IsSubscriptionType() {
			return ErrGroupNotSubscriptionType
		}
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin plan seat transaction: %w", err)
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	txClosed := false
	defer func() {
		if !txClosed {
			_ = tx.Rollback()
		}
	}()

	plan, err := s.configService.LockPlanForUpdate(txCtx, *o.PlanID)
	if err != nil {
		return err
	}
	if err := s.configService.ValidatePlanSeatAvailable(txCtx, plan, o.UserID); err != nil {
		_ = tx.Rollback()
		txClosed = true
		s.writeAuditLog(ctx, o.ID, "SUBSCRIPTION_SEAT_LIMIT_REACHED", "system", map[string]any{
			"plan_id": *o.PlanID,
			"user_id": o.UserID,
		})
		return err
	}

	planID := int64(plan.ID)
	planName := plan.Name
	orderID := o.ID
	input := &AssignSubscriptionInput{
		UserID:           o.UserID,
		PlanID:           &planID,
		PlanName:         &planName,
		SevenDayLimitUSD: plan.SevenDayQuotaUsd,
		ValidityDays:     days,
		AssignedBy:       0,
		OrderID:          &orderID,
		Notes:            orderNote,
	}
	if o.SubscriptionGroupID != nil && *o.SubscriptionGroupID > 0 {
		input.GroupID = *o.SubscriptionGroupID
		_, _, err = s.subscriptionSvc.assignOrExtendSubscriptionTerm(txCtx, input, true)
	} else {
		_, _, err = s.subscriptionSvc.assignOrExtendSubscriptionTerm(txCtx, input, true)
	}
	if err != nil {
		return fmt.Errorf("assign subscription: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit plan seat transaction: %w", err)
	}
	txClosed = true
	if s.configService != nil {
		s.configService.InvalidatePublicPlansCache()
	}
	if err := s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, input.GroupID); err != nil {
		return fmt.Errorf("invalidate subscription cache after plan fulfillment: %w", err)
	}
	return nil
}

func (s *PaymentService) ensurePaymentSubscriptionAssigned(ctx context.Context, o *dbent.PaymentOrder, groupID int64, days int) error {
	if s.subscriptionSvc == nil {
		return errors.New("subscription service is unavailable")
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin subscription fulfillment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	txClient := tx.Client()
	alreadyAssigned, err := hasPaymentSubscriptionAssignmentAudit(txCtx, txClient, o.ID)
	if err != nil {
		return fmt.Errorf("check subscription assignment audit: %w", err)
	}

	recoveredFromNote := false
	if !alreadyAssigned {
		orderNote := paymentSubscriptionOrderNote(o.ID)
		existing, lookupErr := s.subscriptionSvc.userSubRepo.GetByUserIDAndGroupID(txCtx, o.UserID, groupID)
		switch {
		case lookupErr == nil && existing != nil && hasPaymentSubscriptionOrderNote(existing.Notes, orderNote):
			recoveredFromNote = true
		case lookupErr != nil && !errors.Is(lookupErr, ErrSubscriptionNotFound):
			return fmt.Errorf("check existing subscription assignment: %w", lookupErr)
		default:
			orderID := o.ID
			if _, _, err := s.subscriptionSvc.assignOrExtendSubscription(txCtx, &AssignSubscriptionInput{
				UserID:       o.UserID,
				GroupID:      groupID,
				ValidityDays: days,
				AssignedBy:   0,
				OrderID:      &orderID,
				Notes:        orderNote,
			}, true); err != nil {
				return fmt.Errorf("assign subscription: %w", err)
			}
		}

		detail, _ := json.Marshal(map[string]any{
			"groupID":           groupID,
			"validityDays":      days,
			"recoveredFromNote": recoveredFromNote,
		})
		if _, err := txClient.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(o.ID, 10)).
			SetAction("SUBSCRIPTION_ASSIGNED").
			SetDetail(string(detail)).
			SetOperator("system").
			Save(txCtx); err != nil {
			if dbent.IsConstraintError(err) {
				_ = tx.Rollback()
				claimed, checkErr := hasPaymentSubscriptionAssignmentAudit(ctx, s.entClient, o.ID)
				if checkErr == nil && claimed {
					return s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID)
				}
			}
			return fmt.Errorf("record subscription assignment audit: %w", err)
		}
	} else {
		slog.Info("subscription already assigned for order, skipping", "orderID", o.ID, "groupID", groupID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit subscription fulfillment tx: %w", err)
	}
	// Assignment cache invalidation is deferred while this transaction is open,
	// then performed synchronously against the committed subscription.
	if err := s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID); err != nil {
		return fmt.Errorf("invalidate subscription cache after fulfillment: %w", err)
	}
	return nil
}

func hasPaymentSubscriptionAssignmentAudit(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	count, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionIn("SUBSCRIPTION_ASSIGNED", "SUBSCRIPTION_SUCCESS"),
		).
		Limit(1).
		Count(ctx)
	return count > 0, err
}

func paymentSubscriptionOrderNote(orderID int64) string {
	return fmt.Sprintf("payment order %d", orderID)
}

func hasPaymentSubscriptionOrderNote(notes string, orderNote string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == orderNote {
			return true
		}
	}
	return false
}

func (s *PaymentService) hasAuditLog(ctx context.Context, orderID int64, action string) bool {
	oid := strconv.FormatInt(orderID, 10)
	c, _ := s.entClient.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(oid), paymentauditlog.ActionEQ(action)).
		Limit(1).Count(ctx)
	return c > 0
}

func paymentOrderSubscriptionGroupIDLogValue(o *dbent.PaymentOrder) int64 {
	if o == nil || o.SubscriptionGroupID == nil {
		return 0
	}
	return *o.SubscriptionGroupID
}

func (s *PaymentService) subscriptionHasOrderNote(ctx context.Context, userID int64, groupID *int64, orderNote string) bool {
	if s == nil || s.subscriptionSvc == nil || strings.TrimSpace(orderNote) == "" {
		return false
	}
	var (
		sub *UserSubscription
		err error
	)
	if groupID != nil && *groupID > 0 {
		sub, err = s.subscriptionSvc.userSubRepo.GetByUserIDAndGroupID(ctx, userID, *groupID)
	} else {
		sub, err = s.subscriptionSvc.userSubRepo.GetGenericByUserID(ctx, userID)
	}
	if err != nil || sub == nil {
		return false
	}
	for _, line := range strings.Split(sub.Notes, "\n") {
		if strings.TrimSpace(line) == orderNote {
			return true
		}
	}
	return false
}

func (s *PaymentService) applyAffiliateRebateForOrder(ctx context.Context, o *dbent.PaymentOrder) error {
	if o == nil {
		return nil
	}
	baseAmount := affiliateRebateBaseAmount(o)
	if baseAmount <= 0 && o.OrderType != payment.OrderTypeSubscription {
		return nil
	}
	if s.affiliateService == nil {
		return nil
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": fmt.Sprintf("begin affiliate rebate tx: %v", err),
		})
		return fmt.Errorf("begin affiliate rebate tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := s.tryClaimAffiliateRebateAudit(txCtx, tx.Client(), o.ID, baseAmount)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("claim affiliate rebate audit: %w", err)
	}
	if !claimed {
		return nil
	}

	isSubscription := o.OrderType == payment.OrderTypeSubscription

	sourceOrderID := o.ID
	// Every successful order may attempt settlement. The repository locks the
	// relationship and only the first pending event can resolve it.
	result, err := s.affiliateService.GrantFirstRechargeReward(txCtx, o.UserID, baseAmount, isSubscription, true, &sourceOrderID)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("grant first recharge reward: %w", err)
	}

	if !result.Qualified {
		reason := "relationship already resolved, activity disabled, below threshold, or no inviter"
		if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_SKIPPED", map[string]any{
			"baseAmount":            baseAmount,
			"resolved":              result.Resolved,
			"inviter_rewarded":      result.InviterRewarded,
			"invitee_rewarded":      result.InviteeRewarded,
			"inviter_limit_reached": result.InviterLimitReached,
			"isSubscription":        isSubscription,
			"reason":                reason,
		}); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": err.Error()})
			return fmt.Errorf("update affiliate rebate skipped audit: %w", err)
		}
		if err := tx.Commit(); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": fmt.Sprintf("commit: %v", err)})
			return fmt.Errorf("commit affiliate rebate tx: %w", err)
		}
		return nil
	}

	if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_APPLIED", map[string]any{
		"baseAmount":            baseAmount,
		"inviterID":             result.InviterID,
		"inviterReward":         result.InviterReward,
		"inviteeReward":         result.InviteeReward,
		"inviter_rewarded":      result.InviterRewarded,
		"invitee_rewarded":      result.InviteeRewarded,
		"inviter_limit_reached": result.InviterLimitReached,
		"isSubscription":        isSubscription,
	}); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": err.Error()})
		return fmt.Errorf("update affiliate rebate applied audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": fmt.Sprintf("commit: %v", err)})
		return fmt.Errorf("commit affiliate rebate tx: %w", err)
	}

	if result.InviterRewarded || result.InviteeRewarded {
		s.affiliateService.invalidateRewardCaches(ctx, o.UserID, result)
	}
	return nil
}

func affiliateRebateBaseAmount(o *dbent.PaymentOrder) float64 {
	if o == nil {
		return 0
	}
	switch o.OrderType {
	case payment.OrderTypeBalance:
		// 返利只按实充部分计算，赠送额度不参与
		return paymentOrderAmountWithoutBonus(o)
	case payment.OrderTypeSubscription:
		return o.Amount
	default:
		return 0
	}
}

func (s *PaymentService) tryClaimAffiliateRebateAudit(ctx context.Context, client *dbent.Client, orderID int64, baseAmount float64) (bool, error) {
	if client == nil {
		return false, errors.New("nil payment client")
	}
	oid := strconv.FormatInt(orderID, 10)
	detail, _ := json.Marshal(map[string]any{
		"baseAmount": baseAmount,
		"status":     "reserved",
	})
	query, args := buildAffiliateRebateAuditClaimQuery(client, oid, string(detail))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var claimID int64
	if err := rows.Scan(&claimID); err != nil {
		return false, err
	}
	return true, nil
}

func buildAffiliateRebateAuditClaimQuery(client *dbent.Client, orderID, detail string) (string, []any) {
	nowExpr := paymentAuditCurrentTimestampExpr(client)
	if paymentAuditDialect(client) == dialect.Postgres {
		return fmt.Sprintf(`
INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT $1::text, 'AFFILIATE_REBATE_APPLIED', $2::text, 'system', %s
WHERE NOT EXISTS (
	SELECT 1
	FROM payment_audit_logs
	WHERE order_id = $1::text
	  AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action) DO NOTHING
RETURNING id`, nowExpr), []any{orderID, detail}
	}
	return fmt.Sprintf(`
INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT ?, 'AFFILIATE_REBATE_APPLIED', ?, 'system', %s
WHERE NOT EXISTS (
	SELECT 1
	FROM payment_audit_logs
	WHERE order_id = ?
	  AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action) DO NOTHING
RETURNING id`, nowExpr), []any{orderID, detail, orderID}
}

func paymentAuditCurrentTimestampExpr(client *dbent.Client) string {
	if paymentAuditDialect(client) == dialect.Postgres {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}

func paymentAuditDialect(client *dbent.Client) string {
	if client == nil || client.Driver() == nil {
		return ""
	}
	return client.Driver().Dialect()
}

func (s *PaymentService) updateClaimedAffiliateRebateAudit(ctx context.Context, client *dbent.Client, orderID int64, action string, detail map[string]any) error {
	if client == nil {
		return errors.New("nil payment client")
	}
	oid := strconv.FormatInt(orderID, 10)
	detailJSON, _ := json.Marshal(detail)
	updated, err := client.PaymentAuditLog.Update().
		Where(
			paymentauditlog.OrderIDEQ(oid),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED"),
		).
		SetAction(action).
		SetDetail(string(detailJSON)).
		SetOperator("system").
		Save(ctx)
	if err != nil {
		return err
	}
	if updated == 0 {
		return errors.New("affiliate rebate claim log not found")
	}
	return nil
}

func (s *PaymentService) markFailed(ctx context.Context, oid int64, lease *paymentFulfillmentLease, cause error) {
	if lease == nil {
		slog.Error("mark FAILED without fulfillment lease", "orderID", oid)
		return
	}
	now := time.Now()
	r := psErrMsg(cause)
	// The lease version prevents a stale worker from overwriting a newer owner.
	c, e := s.entClient.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(oid),
			paymentorder.StatusEQ(OrderStatusRecharging),
			paymentorder.UpdatedAtEQ(lease.version),
		).
		SetStatus(OrderStatusFailed).SetFailedAt(now).SetFailedReason(r).Save(ctx)
	if e != nil {
		slog.Error("mark FAILED", "orderID", oid, "error", e)
	}
	if c > 0 {
		s.writeAuditLog(ctx, oid, "FULFILLMENT_FAILED", "system", map[string]any{"reason": r})
	}
}

func (s *PaymentService) RetryFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.PaidAt == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "order is not paid")
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot retry")
	}
	if o.Status == OrderStatusCompleted {
		return infraerrors.BadRequest("INVALID_STATUS", "order already completed")
	}
	if o.Status != OrderStatusFailed && o.Status != OrderStatusPaid && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "only paid, failed, and recoverable recharging orders can retry")
	}
	if !isSupportedPaymentOrderType(o.OrderType) {
		return infraerrors.BadRequest("INVALID_ORDER_TYPE", "unsupported order type")
	}
	s.writeAuditLog(ctx, oid, "RECHARGE_RETRY", "admin", map[string]any{"detail": "admin manual retry"})
	return s.executeFulfillment(ctx, oid)
}

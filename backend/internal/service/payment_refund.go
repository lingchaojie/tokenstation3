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
	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
)

// --- Refund Flow ---

var createPaymentProviderFromInstance = provider.CreateProvider

// getOrderProviderInstance looks up the provider instance that processed this order.
// For legacy orders without provider_instance_id, it resolves only when the
// historical instance is uniquely identifiable from the stored order fields.
func (s *PaymentService) getOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	if s == nil || s.entClient == nil || o == nil {
		return nil, nil
	}

	if snapshot := psOrderProviderSnapshot(o); snapshot != nil {
		return s.resolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(psStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return s.resolveUniqueLegacyOrderProviderInstance(ctx, o)
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, nil
	}
	return s.entClient.PaymentProviderInstance.Get(ctx, instID)
}

// getRefundOrderProviderInstance resolves the provider instance for refund paths.
// Refunds must be pinned to an explicit historical binding, so legacy
// "best-effort" provider guessing is intentionally not allowed here.
func (s *PaymentService) getRefundOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	if s == nil || s.entClient == nil || o == nil {
		return nil, nil
	}

	if snapshot := psOrderProviderSnapshot(o); snapshot != nil {
		return s.resolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(psStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return nil, nil
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("order %d refund provider instance id is invalid: %s", o.ID, instIDStr)
	}
	inst, err := s.entClient.PaymentProviderInstance.Get(ctx, instID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, fmt.Errorf("order %d refund provider instance %s is missing", o.ID, instIDStr)
		}
		return nil, err
	}
	return inst, nil
}

func (s *PaymentService) resolveUniqueLegacyOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	paymentType := payment.GetBasePaymentType(strings.TrimSpace(o.PaymentType))
	providerKey := strings.TrimSpace(psStringValue(o.ProviderKey))
	if providerKey != "" {
		instances, err := s.entClient.PaymentProviderInstance.Query().
			Where(paymentproviderinstance.ProviderKeyEQ(providerKey)).
			All(ctx)
		if err != nil {
			return nil, err
		}
		matched := psFilterLegacyOrderProviderInstances(paymentType, instances)
		if len(matched) == 1 {
			return matched[0], nil
		}
		return nil, nil
	}

	if paymentType == "" {
		return nil, nil
	}

	instances, err := s.entClient.PaymentProviderInstance.Query().
		All(ctx)
	if err != nil {
		return nil, err
	}

	matched := psFilterLegacyOrderProviderInstances(paymentType, instances)
	if len(matched) == 1 {
		return matched[0], nil
	}
	return nil, nil
}

func psFilterLegacyOrderProviderInstances(orderPaymentType string, instances []*dbent.PaymentProviderInstance) []*dbent.PaymentProviderInstance {
	if len(instances) == 0 {
		return nil
	}
	if strings.TrimSpace(orderPaymentType) == "" {
		return instances
	}
	var matched []*dbent.PaymentProviderInstance
	for _, inst := range instances {
		if psLegacyOrderMatchesInstance(orderPaymentType, inst) {
			matched = append(matched, inst)
		}
	}
	return matched
}

func psLegacyOrderMatchesInstance(orderPaymentType string, inst *dbent.PaymentProviderInstance) bool {
	if inst == nil {
		return false
	}

	baseType := payment.GetBasePaymentType(strings.TrimSpace(orderPaymentType))
	instanceProviderKey := strings.TrimSpace(inst.ProviderKey)
	if baseType == "" {
		return false
	}

	if baseType == payment.TypeStripe {
		return instanceProviderKey == payment.TypeStripe
	}
	if instanceProviderKey == payment.TypeStripe {
		return false
	}
	if instanceProviderKey == baseType {
		return true
	}
	return payment.InstanceSupportsType(inst.SupportedTypes, baseType)
}

func (s *PaymentService) PrepareRefund(ctx context.Context, oid int64, amt float64, reason string, force, deduct bool) (*RefundPlan, *RefundResult, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return nil, nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	// REFUND_PENDING 只能通过 QueryAndFinalizeRefund 收敛：重新发起会重复扣减并向网关重复退款。
	ok := []string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed}
	if !psSliceContains(ok, o.Status) {
		return nil, nil, infraerrors.BadRequest("INVALID_STATUS", "order status does not allow refund")
	}
	// Check provider instance allows admin refund
	inst, instErr := s.getRefundOrderProviderInstance(ctx, o)
	if instErr != nil {
		slog.Warn("refund: provider instance lookup failed", "orderID", oid, "error", instErr)
		return nil, nil, infraerrors.InternalServer("PROVIDER_LOOKUP_FAILED", "failed to look up payment provider for this order")
	}
	if inst == nil {
		// Legacy order without provider_instance_id — block refund
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.RefundEnabled {
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not enabled for this provider")
	}
	if inst.ProviderKey == payment.TypeAlipay && o.Status == OrderStatusRefundFailed {
		if err := ensureAlipayRefundMayRetry(ctx, s.entClient, oid); err != nil {
			return nil, nil, err
		}
	}
	if math.IsNaN(amt) || math.IsInf(amt, 0) {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid refund amount")
	}
	if amt <= 0 {
		amt = o.Amount
	}
	orderCurrency := PaymentOrderCurrency(o)
	if amt-o.Amount > paymentAmountToleranceForCurrency(orderCurrency) {
		return nil, nil, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds recharge")
	}
	ga := calculateGatewayRefundAmount(o.Amount, o.PayAmount, amt, orderCurrency)
	rr := strings.TrimSpace(reason)
	if rr == "" && o.RefundRequestReason != nil {
		rr = *o.RefundRequestReason
	}
	if rr == "" {
		rr = fmt.Sprintf("refund order:%d", o.ID)
	}
	p := &RefundPlan{OrderID: oid, Order: o, RefundAmount: amt, GatewayAmount: ga, Reason: rr, Force: force, DeductBalance: deduct, DeductionType: payment.DeductionTypeNone, providerKey: inst.ProviderKey}
	if deduct {
		if er := s.prepDeduct(ctx, o, p, force); er != nil {
			return nil, er, nil
		}
	}
	return p, nil, nil
}

func ensureAlipayRefundMayRetry(ctx context.Context, client *dbent.Client, orderID int64) error {
	reconciliationRequired, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionIn("REFUND_RECONCILIATION_REQUIRED", "REFUND_ROLLBACK_FAILED"),
	).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check refund reconciliation: %w", err)
	}
	if reconciliationRequired {
		return infraerrors.Conflict("REFUND_RECONCILIATION_REQUIRED", "refund deduction requires manual reconciliation")
	}
	return nil
}

func (s *PaymentService) prepDeduct(ctx context.Context, o *dbent.PaymentOrder, p *RefundPlan, force bool) *RefundResult {
	if o.OrderType == payment.OrderTypeSubscription {
		p.DeductionType = payment.DeductionTypeSubscription
		if o.SubscriptionDays != nil {
			p.SubDaysToDeduct = *o.SubscriptionDays
			var (
				sub *UserSubscription
				err error
			)
			if o.SubscriptionGroupID != nil {
				sub, err = s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.SubscriptionGroupID)
			} else if o.PlanID != nil {
				sub, err = s.subscriptionSvc.GetActiveGenericSubscription(ctx, o.UserID)
			}
			if err == nil && sub != nil {
				p.SubscriptionID = sub.ID
			} else if !force {
				return &RefundResult{Success: false, Warning: "cannot find active subscription for deduction, use force", RequireForce: true}
			}
		}
		return nil
	}
	u, err := s.userRepo.GetByID(ctx, o.UserID)
	if err != nil {
		if !force {
			return &RefundResult{Success: false, Warning: "cannot fetch user balance, use force", RequireForce: true}
		}
		return nil
	}
	p.DeductionType = payment.DeductionTypeBalance
	p.BalanceToDeduct = math.Min(p.RefundAmount, u.Balance)
	return nil
}

func (s *PaymentService) invalidateRefundBalanceCache(ctx context.Context, userID int64) {
	if s.redeemService != nil {
		s.redeemService.invalidateRedeemCaches(ctx, userID, &RedeemCode{Type: RedeemTypeBalance})
	}
}

const alipayRefundSubmissionWindow = time.Minute

func (s *PaymentService) executeAlipayRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	prov, err := s.validatedRefundProvider(ctx, p.Order)
	if err != nil {
		return nil, err
	}
	// The dispatch deadline is shorter than Alipay's no-record confirmation
	// grace period, so an old paused request cannot submit after release.
	submitCtx, cancel := context.WithTimeout(ctx, alipayRefundSubmissionWindow)
	defer cancel()
	p.refundID = provider.AlipayRefundRequestID(p.Order.OutTradeNo, formatGatewayRefundAmount(p.GatewayAmount, p.Order))
	invalidation, err := s.holdAlipayRefund(submitCtx, p)
	if err != nil {
		return nil, err
	}
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
	}
	if invalidation != nil {
		if err := s.subscriptionSvc.applyDeferredSubscriptionInvalidation(invalidation); err != nil {
			slog.Warn("refund hold cache invalidation failed", "orderID", p.OrderID, "error", err)
		}
	}

	resp, err := s.sendGatewayRefund(submitCtx, p, prov)
	if err != nil {
		// Pending intent is durable. Let reconciliation determine whether the
		// gateway accepted the request before allowing a further refund.
		return nil, err
	}
	if resp.RefundID != p.refundID {
		return nil, errors.New("alipay refund returned a different request number")
	}
	if strings.TrimSpace(resp.Status) == payment.ProviderStatusPending {
		return &RefundResult{Warning: "gateway refund is pending confirmation"}, nil
	}
	return s.finalizePendingRefundSuccess(ctx, p)
}

func (s *PaymentService) holdAlipayRefund(ctx context.Context, p *RefundPlan) (*deferredSubscriptionInvalidation, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin Alipay refund hold: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := claimRefundOrder(txCtx, tx.Client(), p); err != nil {
		return nil, err
	}
	// Check the audit after claiming the order, so an earlier prepared caller
	// cannot bypass a manual reconciliation recorded by another request.
	if p.Order.Status == OrderStatusRefundFailed {
		if err := ensureAlipayRefundMayRetry(txCtx, tx.Client(), p.OrderID); err != nil {
			return nil, err
		}
	}

	p.deductionHeld = true
	p.BalanceToDeduct = math.Max(0, p.BalanceToDeduct)
	if p.SubscriptionID == 0 {
		p.SubDaysToDeduct = 0
	}
	var invalidation *deferredSubscriptionInvalidation
	switch p.DeductionType {
	case payment.DeductionTypeBalance:
		if p.BalanceToDeduct > 0 {
			if err := s.deductLockedRefundBalance(txCtx, p); err != nil {
				return nil, fmt.Errorf("deduct refund balance: %w", err)
			}
		}
	case payment.DeductionTypeSubscription:
		if p.SubDaysToDeduct > 0 {
			_, invalidation, err = s.subscriptionSvc.extendSubscriptionDeferred(txCtx, p.SubscriptionID, -p.SubDaysToDeduct)
			if errors.Is(err, ErrAdjustWouldExpire) {
				p.subscriptionRevokedAt, invalidation, err = s.subscriptionSvc.revokeSubscriptionForRefundHold(txCtx, p.SubscriptionID)
				p.subscriptionRevoked = err == nil
			}
			if err != nil {
				return nil, fmt.Errorf("deduct refund subscription: %w", err)
			}
		}
	}
	if err := setRefundPendingTx(txCtx, tx.Client(), p, p.refundID, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit Alipay refund hold: %w", err)
	}
	return invalidation, nil
}

func claimRefundOrder(ctx context.Context, client *dbent.Client, p *RefundPlan) error {
	c, err := client.PaymentOrder.Update().Where(
		paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(p.Order.Status),
		paymentorder.StatusIn(OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed),
	).SetStatus(OrderStatusRefunding).Save(ctx)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	if c == 0 {
		return infraerrors.Conflict("CONFLICT", "order status changed")
	}
	return nil
}

func (s *PaymentService) ExecuteRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	if p.providerKey == payment.TypeAlipay && p.Order.PaymentTradeNo != "" {
		return s.executeAlipayRefund(ctx, p)
	}
	if err := claimRefundOrder(ctx, s.entClient, p); err != nil {
		return nil, err
	}
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		// Skip balance deduction on retry if previous attempt already deducted
		// but failed to roll back (REFUND_ROLLBACK_FAILED in audit log).
		if !s.hasAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED") {
			if err := s.userRepo.DeductBalance(ctx, p.Order.UserID, p.BalanceToDeduct); err != nil {
				s.restoreStatus(ctx, p)
				return nil, fmt.Errorf("deduction: %w", err)
			}
			s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
		} else {
			slog.Warn("skipping balance deduction on retry (previous rollback failed)", "orderID", p.OrderID)
			p.BalanceToDeduct = 0
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if !s.hasAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED") {
			_, err := s.subscriptionSvc.ExtendSubscription(ctx, p.SubscriptionID, -p.SubDaysToDeduct)
			if err != nil {
				if errors.Is(err, ErrAdjustWouldExpire) {
					// Deduction would expire the subscription — revoke it entirely
					slog.Info("subscription deduction would expire, revoking", "orderID", p.OrderID, "subID", p.SubscriptionID, "days", p.SubDaysToDeduct)
					if revokeErr := s.subscriptionSvc.RevokeSubscription(ctx, p.SubscriptionID); revokeErr != nil {
						s.restoreStatus(ctx, p)
						return nil, fmt.Errorf("revoke subscription: %w", revokeErr)
					}
				} else {
					// Other errors (DB failure, not found) — abort refund
					s.restoreStatus(ctx, p)
					return nil, fmt.Errorf("deduct subscription days: %w", err)
				}
			}
		} else {
			slog.Warn("skipping subscription deduction on retry (previous rollback failed)", "orderID", p.OrderID)
			p.SubDaysToDeduct = 0
		}
	}
	resp, err := s.gwRefund(ctx, p)
	if err != nil {
		return s.handleGwFail(ctx, p, err)
	}
	return s.finishRefund(ctx, p, resp)
}

func (s *PaymentService) gwRefund(ctx context.Context, p *RefundPlan) (*payment.RefundResponse, error) {
	if p.Order.PaymentTradeNo == "" {
		s.writeAuditLog(ctx, p.Order.ID, "REFUND_NO_TRADE_NO", "admin", map[string]any{"detail": "skipped"})
		return &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil
	}

	prov, err := s.validatedRefundProvider(ctx, p.Order)
	if err != nil {
		return nil, err
	}
	return s.sendGatewayRefund(ctx, p, prov)
}

func (s *PaymentService) validatedRefundProvider(ctx context.Context, order *dbent.PaymentOrder) (payment.Provider, error) {
	// Refund requests and queries use the order's original merchant.
	prov, err := s.getRefundProvider(ctx, order)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	if err := validateProviderSnapshotMetadata(order, prov.ProviderKey(), providerMerchantIdentityMetadata(prov)); err != nil {
		s.writeAuditLog(ctx, order.ID, "REFUND_PROVIDER_METADATA_MISMATCH", "admin", map[string]any{"detail": err.Error()})
		return nil, err
	}
	return prov, nil
}

func (s *PaymentService) sendGatewayRefund(ctx context.Context, p *RefundPlan, prov payment.Provider) (*payment.RefundResponse, error) {
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := prov.Refund(ctx, payment.RefundRequest{
		TradeNo: p.Order.PaymentTradeNo,
		OrderID: p.Order.OutTradeNo,
		Amount:  formatGatewayRefundAmount(p.GatewayAmount, p.Order),
		Reason:  p.Reason,
	})
	finishProviderCall()
	if err != nil {
		if resp != nil && strings.TrimSpace(resp.Status) == payment.ProviderStatusPending {
			return resp, nil
		}
		return nil, err
	}
	if err := validateRefundProviderResponse(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func formatGatewayRefundAmount(amount float64, order *dbent.PaymentOrder) string {
	return payment.FormatAmountForCurrency(amount, PaymentOrderCurrency(order))
}

func validateRefundProviderResponse(resp *payment.RefundResponse) error {
	if resp == nil {
		return fmt.Errorf("payment refund response missing")
	}
	status := strings.TrimSpace(resp.Status)
	switch status {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded, payment.ProviderStatusPending:
		return nil
	case payment.ProviderStatusFailed:
		return fmt.Errorf("payment refund failed: status %s", status)
	default:
		return fmt.Errorf("payment refund returned unknown status: %s", status)
	}
}

func (s *PaymentService) finishRefund(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse) (*RefundResult, error) {
	if err := validateRefundProviderResponse(resp); err != nil {
		return s.handleGwFail(ctx, p, err)
	}
	switch strings.TrimSpace(resp.Status) {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded:
		return s.markRefundOk(ctx, p)
	case payment.ProviderStatusPending:
		return s.markRefundPending(ctx, p, resp)
	default:
		return s.handleGwFail(ctx, p, fmt.Errorf("payment refund returned unknown status: %s", strings.TrimSpace(resp.Status)))
	}
}

func (s *PaymentService) QueryAndFinalizeRefund(ctx context.Context, oid int64) (*RefundResult, error) {
	o, pendingDetail, err := s.readPendingRefundAttempt(ctx, oid)
	if err != nil {
		return nil, err
	}

	prov, err := s.validatedRefundProvider(ctx, o)
	if err != nil {
		return nil, err
	}
	queryProvider, ok := prov.(payment.RefundQueryProvider)
	if !ok {
		return nil, infraerrors.BadRequest("REFUND_QUERY_UNSUPPORTED", "this payment provider does not support refund status query; please verify manually")
	}

	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := queryProvider.QueryRefund(ctx, payment.RefundQueryRequest{
		TradeNo:     o.PaymentTradeNo,
		OrderID:     o.OutTradeNo,
		RefundID:    pendingDetail.RefundID,
		Amount:      formatGatewayRefundAmount(calculateGatewayRefundAmount(o.Amount, o.PayAmount, o.RefundAmount, PaymentOrderCurrency(o)), o),
		RequestedAt: pendingDetail.requestedAt,
	})
	finishProviderCall()
	if err != nil {
		return nil, fmt.Errorf("query refund: %w", err)
	}
	plan := s.refundFinalizePlan(o)
	plan.refundID = pendingDetail.RefundID
	plan.pendingAuditID = pendingDetail.auditID
	if pendingDetail.DeductionHeld {
		pendingDetail.applyTo(plan)
	}
	if err := validateRefundProviderResponse(resp); err != nil {
		// An invalid Alipay response cannot release funds. A signed terminal
		// failure from the provider is distinct from protocol/query errors.
		if prov.ProviderKey() == payment.TypeAlipay && (resp == nil || strings.TrimSpace(resp.Status) != payment.ProviderStatusFailed) {
			return nil, err
		}
		return s.finalizeRefundFailed(ctx, plan, err)
	}
	if strings.TrimSpace(resp.Status) == payment.ProviderStatusPending {
		s.writeAuditLog(ctx, oid, "REFUND_QUERY_PENDING", "admin", map[string]any{"refundID": resp.RefundID})
		return &RefundResult{Success: false, Warning: "gateway refund is still pending confirmation"}, nil
	}
	if !pendingDetail.DeductionHeld {
		switch {
		case !pendingDetail.DeductionRollbackOK, pendingDetail.DeductionRequested != nil && !*pendingDetail.DeductionRequested:
			plan.BalanceToDeduct = 0
			plan.SubDaysToDeduct = 0
		case o.OrderType == payment.OrderTypeSubscription:
			if early := s.prepDeduct(ctx, o, plan, true); early != nil {
				return early, nil
			}
		}
	}
	return s.finalizePendingRefundSuccess(ctx, plan)
}

// Read the order and its pending audit while replacement attempts are excluded.
// Release the row lock before contacting the provider.
func (s *PaymentService) readPendingRefundAttempt(ctx context.Context, oid int64) (*dbent.PaymentOrder, refundPendingAuditDetail, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, refundPendingAuditDetail{}, fmt.Errorf("begin pending refund lookup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	query := client.PaymentOrder.Query().Where(paymentorder.IDEQ(oid))
	if client.Driver().Dialect() == dialect.Postgres {
		query = query.ForUpdate()
	}
	order, err := query.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, refundPendingAuditDetail{}, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if err != nil {
		return nil, refundPendingAuditDetail{}, fmt.Errorf("load pending refund order: %w", err)
	}
	if order.Status != OrderStatusRefundPending {
		return nil, refundPendingAuditDetail{}, infraerrors.BadRequest("INVALID_STATUS", "only refund pending orders can be finalized")
	}
	detail, err := latestRefundPendingDetail(ctx, client, oid)
	if err != nil {
		return nil, refundPendingAuditDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return nil, refundPendingAuditDetail{}, fmt.Errorf("commit pending refund lookup: %w", err)
	}
	return order, detail, nil
}

func (s *PaymentService) finalizePendingRefundSuccess(ctx context.Context, p *RefundPlan) (_ *RefundResult, err error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund finalization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	txCtx := dbent.NewTxContext(ctx, tx)

	claimed, err := tx.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefundPending)).
		SetStatus(OrderStatusRefunding).
		Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("claim pending refund: %w", err)
	}
	if claimed == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed")
	}
	if err := verifyRefundAttempt(txCtx, tx.Client(), p); err != nil {
		return nil, err
	}

	invalidation, err := s.applyRefundFinalDeduction(txCtx, p)
	if err != nil {
		return nil, err
	}
	result, err := s.markRefundOkTx(txCtx, tx.Client(), p)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refund finalization: %w", err)
	}
	if invalidation != nil {
		if invalidateErr := s.subscriptionSvc.applyDeferredSubscriptionInvalidation(invalidation); invalidateErr != nil {
			slog.Warn("pending refund subscription cache invalidation failed", "orderID", p.OrderID, "error", invalidateErr)
		}
	}
	if !p.deductionHeld && p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
	}
	return result, nil
}

func (s *PaymentService) refundFinalizePlan(o *dbent.PaymentOrder) *RefundPlan {
	refundAmount := o.RefundAmount
	reason := strings.TrimSpace(psStringValue(o.RefundReason))
	if reason == "" {
		reason = fmt.Sprintf("refund order:%d", o.ID)
	}
	return &RefundPlan{
		OrderID:       o.ID,
		Order:         o,
		RefundAmount:  refundAmount,
		GatewayAmount: calculateGatewayRefundAmount(o.Amount, o.PayAmount, refundAmount, PaymentOrderCurrency(o)),
		Reason:        reason,
		Force:         o.ForceRefund,
		DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance,
		BalanceToDeduct: func() float64 {
			if o.OrderType == payment.OrderTypeBalance {
				return refundAmount
			}
			return 0
		}(),
	}
}

func (s *PaymentService) applyRefundFinalDeduction(ctx context.Context, p *RefundPlan) (*deferredSubscriptionInvalidation, error) {
	if p.deductionHeld {
		return nil, nil
	}
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		if err := s.deductLockedRefundBalance(ctx, p); err != nil {
			return nil, err
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		_, invalidation, err := s.subscriptionSvc.extendSubscriptionDeferred(ctx, p.SubscriptionID, -p.SubDaysToDeduct)
		if err != nil {
			if errors.Is(err, ErrAdjustWouldExpire) {
				invalidation, err = s.subscriptionSvc.revokeSubscriptionDeferred(ctx, p.SubscriptionID)
				if err != nil {
					return nil, fmt.Errorf("revoke subscription: %w", err)
				}
			} else {
				return nil, fmt.Errorf("deduct subscription days: %w", err)
			}
		}
		return invalidation, nil
	}
	return nil, nil
}

func (s *PaymentService) deductLockedRefundBalance(ctx context.Context, p *RefundPlan) error {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return errors.New("pending refund balance deduction requires a transaction")
	}
	query := tx.Client().User.Query().Where(user.IDEQ(p.Order.UserID))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		query = query.ForUpdate()
	}
	current, err := query.Only(ctx)
	if err != nil {
		return fmt.Errorf("lock user balance for pending refund: %w", err)
	}
	p.BalanceToDeduct = pendingRefundBalanceDeductionAmount(p.BalanceToDeduct, current.Balance)
	if p.BalanceToDeduct == 0 {
		return nil
	}
	return s.userRepo.DeductBalance(ctx, p.Order.UserID, p.BalanceToDeduct)
}

func pendingRefundBalanceDeductionAmount(refundAmount, currentBalance float64) float64 {
	return math.Max(0, math.Min(refundAmount, currentBalance))
}

// finalizeRefundFailed 将 REFUND_PENDING 收敛为 REFUND_FAILED，并归还进入 pending 时保留的扣减。
// 认领状态、归还扣减与审计在同一事务内完成：两者分开提交时，进程在中间退出会留下
// 已失败但扣减未归还、且没有 REFUND_ROLLBACK_FAILED 标记的订单，重试退款会再扣一次。
func (s *PaymentService) finalizeRefundFailed(ctx context.Context, p *RefundPlan, gatewayErr error) (*RefundResult, error) {
	warning := "gateway refund failed: " + psErrMsg(gatewayErr)
	invalidation, err := s.finalizeRefundFailedTx(ctx, p, gatewayErr, nil)
	if errors.Is(err, ErrSubscriptionRestoreConflict) || errors.Is(err, errRefundSubscriptionRevoked) ||
		errors.Is(err, ErrSubscriptionNotRevoked) || errors.Is(err, errRefundSubscriptionRevocationChanged) {
		// Preserve the current subscription state and block another refund
		// until an operator reconciles the hold.
		if _, saveErr := s.finalizeRefundFailedTx(ctx, p, gatewayErr, err); saveErr != nil {
			return nil, saveErr
		}
		return &RefundResult{Warning: warning + "; refund deduction requires manual reconciliation"}, nil
	}
	if err != nil {
		return nil, err
	}
	if p.deductionHeld && p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
	}
	if invalidation != nil {
		if err := s.subscriptionSvc.applyDeferredSubscriptionInvalidation(invalidation); err != nil {
			slog.Warn("refund restoration cache invalidation failed", "orderID", p.OrderID, "error", err)
		}
	}
	return &RefundResult{Warning: warning}, nil
}

func (s *PaymentService) finalizeRefundFailedTx(ctx context.Context, p *RefundPlan, gatewayErr, reconciliationErr error) (*deferredSubscriptionInvalidation, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund failure finalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := tx.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefundPending)).
		SetStatus(OrderStatusRefundFailed).SetFailedAt(time.Now()).SetFailedReason(psErrMsg(gatewayErr)).Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("mark refund failed: %w", err)
	}
	if claimed == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed")
	}
	if err := verifyRefundAttempt(txCtx, tx.Client(), p); err != nil {
		return nil, err
	}
	var invalidation *deferredSubscriptionInvalidation
	if p.deductionHeld && reconciliationErr == nil {
		invalidation, err = s.returnHeldRefundDeduction(txCtx, p)
		if err != nil {
			return nil, err
		}
	}
	if reconciliationErr != nil {
		if err := createPaymentAuditLogTx(txCtx, tx.Client(), p.OrderID, "REFUND_RECONCILIATION_REQUIRED", map[string]any{
			"refundID": p.refundID, "subscriptionID": p.SubscriptionID,
			"subDaysDeducted": p.SubDaysToDeduct, "detail": psErrMsg(reconciliationErr),
		}); err != nil {
			return nil, err
		}
	}
	if err := archivePriorRefundAudit(txCtx, tx.Client(), p.OrderID, "REFUND_FAILED"); err != nil {
		return nil, err
	}
	if err := createPaymentAuditLogTx(txCtx, tx.Client(), p.OrderID, "REFUND_FAILED", map[string]any{"detail": psErrMsg(gatewayErr)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refund failure finalization: %w", err)
	}
	return invalidation, nil
}

func (s *PaymentService) returnHeldRefundDeduction(ctx context.Context, p *RefundPlan) (*deferredSubscriptionInvalidation, error) {
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		// Add to cash even when the resulting balance is still negative.
		// Returning a hold does not represent another customer recharge.
		tx := dbent.TxFromContext(ctx)
		if _, err := tx.Client().User.UpdateOneID(p.Order.UserID).AddBalance(p.BalanceToDeduct).Save(ctx); err != nil {
			return nil, fmt.Errorf("return held balance deduction: %w", err)
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if p.subscriptionRevoked {
			_, invalidation, err := s.subscriptionSvc.restoreSubscriptionDeferred(ctx, p.SubscriptionID, p.subscriptionRevokedAt)
			return invalidation, err
		}
		return s.subscriptionSvc.returnRefundSubscriptionDays(ctx, p.SubscriptionID, p.SubDaysToDeduct)
	}
	return nil, nil
}

// Verify the audit generation after acquiring the order row lock. An
// earlier gateway query cannot settle a replacement pending attempt.
func verifyRefundAttempt(ctx context.Context, client *dbent.Client, p *RefundPlan) error {
	latestID, err := pendingRefundAuditQuery(client, p.OrderID).FirstID(ctx)
	if dbent.IsNotFound(err) && p.pendingAuditID == 0 {
		return nil
	}
	if dbent.IsNotFound(err) || (err == nil && latestID != p.pendingAuditID) {
		return infraerrors.Conflict("CONFLICT", "refund attempt changed")
	}
	if err != nil {
		return fmt.Errorf("load current refund audit: %w", err)
	}
	return nil
}

// Keep the latest refund audit under its fixed action for existing readers.
// Callers hold the order row lock and replace the action in the same transaction,
// so old attempts keep their ID and detail without violating migration 131.
func archivePriorRefundAudit(ctx context.Context, client *dbent.Client, orderID int64, action string) error {
	auditID, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ(action),
	).OnlyID(ctx)
	if dbent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load previous %s audit: %w", action, err)
	}
	if err := client.PaymentAuditLog.UpdateOneID(auditID).
		SetAction(action + ":" + strconv.FormatInt(auditID, 10)).Exec(ctx); err != nil {
		return fmt.Errorf("archive previous %s audit: %w", action, err)
	}
	return nil
}

func createPaymentAuditLogTx(ctx context.Context, client *dbent.Client, oid int64, action string, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal %s audit: %w", action, err)
	}
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(oid, 10)).
		SetAction(action).
		SetDetail(string(raw)).
		SetOperator("admin").
		Save(ctx); err != nil {
		return fmt.Errorf("write %s audit: %w", action, err)
	}
	return nil
}

// refundPendingAuditDetail 记录在 REFUND_PENDING 审计日志中。DeductionHeld 为 true 表示
// 进入 pending 时扣减被保留；旧记录没有该字段，扣减已在 pending 时归还（DeductionRollbackOK）。
type refundPendingAuditDetail struct {
	RefundID              string     `json:"refundID"`
	RefundAmount          float64    `json:"refundAmount"`
	Reason                string     `json:"reason"`
	Force                 bool       `json:"force"`
	DeductionRollbackOK   bool       `json:"deductionRollbackOK"`
	DeductionHeld         bool       `json:"deductionHeld,omitempty"`
	DeductionRequested    *bool      `json:"deductionRequested,omitempty"`
	SubscriptionRevoked   bool       `json:"subscriptionRevoked,omitempty"`
	SubscriptionRevokedAt *time.Time `json:"subscriptionRevokedAt,omitempty"`
	DeductionType         string     `json:"deductionType,omitempty"`
	BalanceDeducted       float64    `json:"balanceDeducted"`
	SubDaysDeducted       int        `json:"subDaysDeducted"`
	SubscriptionID        int64      `json:"subscriptionID,omitempty"`
	// requestedAt 取自审计记录的创建时间，不参与序列化。
	requestedAt time.Time
	auditID     int64
}

func (d refundPendingAuditDetail) applyTo(p *RefundPlan) {
	p.deductionHeld = true
	p.DeductionType = d.DeductionType
	if p.DeductionType == "" {
		p.DeductionType = payment.DeductionTypeNone
	}
	p.BalanceToDeduct = d.BalanceDeducted
	p.SubDaysToDeduct = d.SubDaysDeducted
	p.SubscriptionID = d.SubscriptionID
	p.subscriptionRevoked = d.SubscriptionRevoked
	p.subscriptionRevokedAt = d.SubscriptionRevokedAt
}

// latestRefundPendingDetail 读取最近一次 REFUND_PENDING 审计。没有记录视为旧订单（扣减已在
// pending 时归还）；读取或解析失败必须返回错误：误判为旧记录会让保留的扣减被重复扣减或不再归还。
func latestRefundPendingDetail(ctx context.Context, client *dbent.Client, oid int64) (refundPendingAuditDetail, error) {
	detail := refundPendingAuditDetail{DeductionRollbackOK: true}
	logEntry, err := pendingRefundAuditQuery(client, oid).First(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return detail, nil
		}
		return refundPendingAuditDetail{}, fmt.Errorf("load refund pending audit: %w", err)
	}
	if err := json.Unmarshal([]byte(logEntry.Detail), &detail); err != nil {
		return refundPendingAuditDetail{}, fmt.Errorf("parse refund pending audit: %w", err)
	}
	detail.RefundID = strings.TrimSpace(detail.RefundID)
	detail.requestedAt = logEntry.CreatedAt
	detail.auditID = logEntry.ID
	return detail, nil
}

func pendingRefundAuditQuery(client *dbent.Client, orderID int64) *dbent.PaymentAuditLogQuery {
	return client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).
		Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc()))
}

// getRefundProvider creates a provider using the order's original instance config.
// Delegates to getOrderProvider which handles instance lookup and fallback.
func (s *PaymentService) getRefundProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, error) {
	inst, err := s.getRefundOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, fmt.Errorf("refund provider instance is unavailable for order %d", o.ID)
	}
	return s.createProviderFromInstance(ctx, inst)
}

func (s *PaymentService) handleGwFail(ctx context.Context, p *RefundPlan, gErr error) (*RefundResult, error) {
	if s.RollbackRefund(ctx, p, gErr) {
		s.restoreStatus(ctx, p)
		s.writeAuditLog(ctx, p.OrderID, "REFUND_GATEWAY_FAILED", "admin", map[string]any{"detail": psErrMsg(gErr)})
		return &RefundResult{Success: false, Warning: "gateway failed: " + psErrMsg(gErr) + ", rolled back"}, nil
	}
	now := time.Now()
	_, _ = s.entClient.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(OrderStatusRefundFailed).SetFailedAt(now).SetFailedReason(psErrMsg(gErr)).Save(ctx)
	s.writeAuditLog(ctx, p.OrderID, "REFUND_FAILED", "admin", map[string]any{"detail": psErrMsg(gErr)})
	return nil, infraerrors.InternalServer("REFUND_FAILED", psErrMsg(gErr))
}

func (s *PaymentService) markRefundOk(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	fs := OrderStatusRefunded
	if p.RefundAmount < p.Order.Amount {
		fs = OrderStatusPartiallyRefunded
	}
	now := time.Now()
	_, err := s.entClient.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(fs).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetRefundAt(now).SetForceRefund(p.Force).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	s.writeAuditLog(ctx, p.OrderID, "REFUND_SUCCESS", "admin", map[string]any{"refundAmount": p.RefundAmount, "reason": p.Reason, "balanceDeducted": p.BalanceToDeduct, "force": p.Force})
	return &RefundResult{Success: true, BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct}, nil
}

func (s *PaymentService) markRefundOkTx(ctx context.Context, client *dbent.Client, p *RefundPlan) (*RefundResult, error) {
	fs := OrderStatusRefunded
	if p.RefundAmount < p.Order.Amount {
		fs = OrderStatusPartiallyRefunded
	}
	now := time.Now()
	_, err := client.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(fs).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetRefundAt(now).SetForceRefund(p.Force).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	detail, err := json.Marshal(map[string]any{"refundAmount": p.RefundAmount, "reason": p.Reason, "balanceDeducted": p.BalanceToDeduct, "force": p.Force})
	if err != nil {
		return nil, fmt.Errorf("marshal refund audit: %w", err)
	}
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(p.OrderID, 10)).
		SetAction("REFUND_SUCCESS").
		SetDetail(string(detail)).
		SetOperator("admin").
		Save(ctx); err != nil {
		return nil, fmt.Errorf("write refund audit: %w", err)
	}
	return &RefundResult{Success: true, BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct}, nil
}

// markRefundPending persists the gateway request ID and deduction disposition
// atomically with the order status so a later query can safely finalize it.
func (s *PaymentService) markRefundPending(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse) (*RefundResult, error) {
	// Other providers keep their existing policy of releasing the deduction
	// while pending, then re-applying it after gateway success.
	rollbackOK := s.RollbackRefund(ctx, p, nil)
	if rollbackOK {
		p.BalanceToDeduct = 0
		p.SubDaysToDeduct = 0
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin mark refund pending: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := setRefundPendingTx(ctx, tx.Client(), p, refundResponseID(resp), rollbackOK); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mark refund pending: %w", err)
	}
	warning := "gateway refund is pending confirmation"
	if !rollbackOK {
		warning += "; refund deduction rollback failed"
	}
	return &RefundResult{Warning: warning}, nil
}

func setRefundPendingTx(ctx context.Context, client *dbent.Client, p *RefundPlan, refundID string, rollbackOK bool) error {
	detail := refundPendingAuditDetail{
		RefundID: refundID, RefundAmount: p.RefundAmount, Reason: p.Reason, Force: p.Force,
		DeductionHeld: p.deductionHeld, DeductionRequested: &p.DeductBalance, DeductionRollbackOK: rollbackOK,
		DeductionType: p.DeductionType, BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct,
		SubscriptionID: p.SubscriptionID, SubscriptionRevoked: p.subscriptionRevoked, SubscriptionRevokedAt: p.subscriptionRevokedAt,
	}
	audit, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal refund pending audit: %w", err)
	}
	claimed, err := client.PaymentOrder.Update().Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefunding)).
		SetStatus(OrderStatusRefundPending).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).
		ClearRefundAt().SetForceRefund(p.Force).ClearFailedAt().ClearFailedReason().Save(ctx)
	if err != nil {
		return fmt.Errorf("mark refund pending: %w", err)
	}
	if claimed == 0 {
		return infraerrors.Conflict("CONFLICT", "order status changed")
	}
	if err := archivePriorRefundAudit(ctx, client, p.OrderID, "REFUND_PENDING"); err != nil {
		return err
	}
	auditLog, err := client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(p.OrderID, 10)).
		SetAction("REFUND_PENDING").SetDetail(string(audit)).SetOperator("admin").Save(ctx)
	if err != nil {
		return fmt.Errorf("write refund pending audit: %w", err)
	}
	p.pendingAuditID = auditLog.ID
	return nil
}

func refundResponseID(resp *payment.RefundResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.RefundID)
}

func (s *PaymentService) RollbackRefund(ctx context.Context, p *RefundPlan, gErr error) bool {
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		if err := s.userRepo.UpdateBalance(ctx, p.Order.UserID, p.BalanceToDeduct); err != nil {
			slog.Error("[CRITICAL] rollback failed", "orderID", p.OrderID, "amount", p.BalanceToDeduct, "error", err)
			s.writeAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED", "admin", map[string]any{"gatewayError": psErrMsg(gErr), "rollbackError": psErrMsg(err), "balanceDeducted": p.BalanceToDeduct})
			return false
		}
		s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if _, err := s.subscriptionSvc.ExtendSubscription(ctx, p.SubscriptionID, p.SubDaysToDeduct); err != nil {
			slog.Error("[CRITICAL] subscription rollback failed", "orderID", p.OrderID, "subID", p.SubscriptionID, "days", p.SubDaysToDeduct, "error", err)
			s.writeAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED", "admin", map[string]any{"gatewayError": psErrMsg(gErr), "rollbackError": psErrMsg(err), "subDaysDeducted": p.SubDaysToDeduct})
			return false
		}
	}
	return true
}

func (s *PaymentService) restoreStatus(ctx context.Context, p *RefundPlan) {
	rs := OrderStatusCompleted
	if p.Order.Status == OrderStatusRefundRequested {
		rs = OrderStatusRefundRequested
	}
	_, _ = s.entClient.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(rs).Save(ctx)
}

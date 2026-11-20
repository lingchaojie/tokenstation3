package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	paymentprovider "github.com/Wei-Shaw/sub2api/internal/payment/provider"
)

type easyPayConfirmationMerchant struct {
	InstanceID  string `json:"instance_id,omitempty"`
	Fingerprint string `json:"merchant_fingerprint,omitempty"`
}

func confirmedEasyPayMerchant(prov payment.Provider) easyPayConfirmationMerchant {
	if easyPay, ok := prov.(*paymentprovider.EasyPay); ok {
		instanceID, fingerprint := easyPay.ConfirmationMerchant()
		return easyPayConfirmationMerchant{InstanceID: instanceID, Fingerprint: fingerprint}
	}
	return easyPayConfirmationMerchant{}
}

func (merchant easyPayConfirmationMerchant) matches(other easyPayConfirmationMerchant) bool {
	return merchant.Fingerprint != "" && merchant == other
}

func findEasyPayConfirmationMerchant(ctx context.Context, client *dbent.Client, orderID int64) (*easyPayConfirmationMerchant, error) {
	log, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ(paymentAwaitingTradeNoAuditAction),
	).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read EasyPay confirmation merchant: %w", err)
	}
	var merchant easyPayConfirmationMerchant
	if err := json.Unmarshal([]byte(log.Detail), &merchant); err != nil {
		return nil, fmt.Errorf("decode EasyPay confirmation merchant: %w", err)
	}
	return &merchant, nil
}

func verifyEasyPayConfirmationMerchant(ctx context.Context, client *dbent.Client, orderID int64, candidate easyPayConfirmationMerchant) (bool, error) {
	merchant, err := findEasyPayConfirmationMerchant(ctx, client, orderID)
	if err != nil || merchant == nil {
		return false, err
	}
	if !merchant.matches(candidate) {
		return true, fmt.Errorf("%w: held order requires its original EasyPay merchant", ErrPaymentUpstreamUnconfirmed)
	}
	return true, nil
}

func (s *PaymentService) heldEasyPayProvider(ctx context.Context, order *dbent.PaymentOrder, merchant easyPayConfirmationMerchant) (payment.Provider, error) {
	var prov payment.Provider
	var err error
	if instanceID, parseErr := strconv.ParseInt(merchant.InstanceID, 10, 64); parseErr == nil && instanceID > 0 {
		instance, loadErr := s.entClient.PaymentProviderInstance.Get(ctx, instanceID)
		if loadErr != nil {
			return nil, loadErr
		}
		prov, err = s.createProviderFromInstance(ctx, instance)
	} else {
		prov, err = s.getOrderProvider(ctx, order)
	}
	if err != nil {
		return nil, err
	}
	if !merchant.matches(confirmedEasyPayMerchant(prov)) {
		return nil, fmt.Errorf("%w: held order requires its original EasyPay merchant", ErrPaymentUpstreamUnconfirmed)
	}
	return prov, nil
}

// PaymentConfirmationHolds returns the audit-backed holds for already authorized
// orders in one query. Keeping an audit record after fulfillment does not hold
// a terminal order.
func (s *PaymentService) PaymentConfirmationHolds(ctx context.Context, orders []*dbent.PaymentOrder) (map[string]bool, error) {
	ids := make([]string, 0, len(orders))
	for _, order := range orders {
		if order != nil && order.Status == OrderStatusPending {
			ids = append(ids, strconv.FormatInt(order.ID, 10))
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	heldIDs, err := s.entClient.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDIn(ids...),
		paymentauditlog.ActionEQ(paymentAwaitingTradeNoAuditAction),
	).Select(paymentauditlog.FieldOrderID).Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read payment confirmation holds: %w", err)
	}
	held := make(map[string]bool, len(heldIDs))
	for _, id := range heldIDs {
		held[id] = true
	}
	return held, nil
}

// PaymentOrderProviderKey exposes only the known provider kind stored with an
// order. A mutable registry cannot identify the merchant for a legacy order.
func PaymentOrderProviderKey(order *dbent.PaymentOrder) string {
	key := strings.TrimSpace(psStringValue(order.ProviderKey))
	if snapshot := psOrderProviderSnapshot(order); snapshot != nil && snapshot.ProviderKey != "" {
		key = snapshot.ProviderKey
	}
	if key == "" && order.PaymentType == payment.TypeEasyPay {
		key = payment.TypeEasyPay
	}
	switch key = strings.ToLower(key); key {
	case payment.TypeEasyPay, payment.TypeAlipay, payment.TypeWxpay, payment.TypeIkunPay, payment.TypeStripe, payment.TypeAirwallex:
		return key
	default:
		return ""
	}
}

package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func deriveSeatLimitFromVirtualRange(start, total *int) (*int, error) {
	if start == nil && total == nil {
		return nil, nil
	}
	if start == nil || total == nil {
		return nil, infraerrors.BadRequest("PLAN_VIRTUAL_SEAT_RANGE_INCOMPLETE", "virtual seat start and total must be set together")
	}
	if *start < 0 || *total < 0 {
		return nil, infraerrors.BadRequest("PLAN_VIRTUAL_SEAT_RANGE_INVALID", "virtual seat values must be >= 0")
	}
	if *total < *start {
		return nil, infraerrors.BadRequest("PLAN_VIRTUAL_SEAT_RANGE_INVALID", "virtual seat total must be >= start")
	}
	limit := *total - *start
	return &limit, nil
}

func deriveSeatLimitFromOptionalVirtualRange(start, total OptionalInt) (*int, bool, error) {
	if !start.Set && !total.Set {
		return nil, false, nil
	}
	if start.Set != total.Set {
		return nil, true, infraerrors.BadRequest("PLAN_VIRTUAL_SEAT_RANGE_INCOMPLETE", "virtual seat start and total must be set together")
	}
	if start.Value == nil && total.Value == nil {
		return nil, true, nil
	}
	if start.Value == nil || total.Value == nil {
		return nil, true, infraerrors.BadRequest("PLAN_VIRTUAL_SEAT_RANGE_INCOMPLETE", "virtual seat start and total must be set together")
	}
	limit, err := deriveSeatLimitFromVirtualRange(start.Value, total.Value)
	return limit, true, err
}

func virtualSeatRangeFromLimit(limit *int) (*int, *int) {
	if limit == nil {
		return nil, nil
	}
	start := 0
	total := *limit
	return &start, &total
}

func ensureSeatLimitMatchesVirtualRange(seatLimit, derivedLimit *int) error {
	if seatLimit == nil && derivedLimit == nil {
		return nil
	}
	if seatLimit == nil || derivedLimit == nil || *seatLimit != *derivedLimit {
		return infraerrors.BadRequest("PLAN_SEAT_LIMIT_CONFLICT", "seat limit must match virtual seat total minus start")
	}
	return nil
}

func normalizeCreatePlanSeatRange(seatLimit, virtualStart, virtualTotal *int) (*int, *int, *int, error) {
	if virtualStart != nil || virtualTotal != nil {
		derivedLimit, err := deriveSeatLimitFromVirtualRange(virtualStart, virtualTotal)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := ensureSeatLimitMatchesVirtualRange(seatLimit, derivedLimit); seatLimit != nil && err != nil {
			return nil, nil, nil, err
		}
		return derivedLimit, virtualStart, virtualTotal, nil
	}
	start, total := virtualSeatRangeFromLimit(seatLimit)
	return seatLimit, start, total, nil
}

// normalizePlanCurrency validates and normalizes the display-only currency label.
// Empty means "no label" and is kept as-is so existing plans stay unchanged.
func normalizePlanCurrency(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	currency, err := payment.NormalizePaymentCurrency(raw)
	if err != nil {
		return "", infraerrors.BadRequest("PLAN_CURRENCY_INVALID", "currency must be a 3-letter ISO currency code")
	}
	return currency, nil
}

// validatePlanRequired checks that all required fields for a plan are provided.
func validatePlanRequired(name string, price float64, validityDays int, validityUnit string, originalPrice *float64, seatLimit *int) error {
	if strings.TrimSpace(name) == "" {
		return infraerrors.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if price <= 0 {
		return infraerrors.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if validityDays <= 0 {
		return infraerrors.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if strings.TrimSpace(validityUnit) == "" {
		return infraerrors.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if originalPrice != nil && *originalPrice < 0 {
		return infraerrors.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	if seatLimit != nil && *seatLimit < 0 {
		return infraerrors.BadRequest("PLAN_SEAT_LIMIT_INVALID", "seat limit must be >= 0")
	}
	return nil
}

// validatePlanPatch validates only the non-nil fields in a patch update.
func validatePlanPatch(req UpdatePlanRequest) error {
	if req.SevenDayQuotaUSD != nil && req.ClearSevenDayQuotaUSD {
		return infraerrors.BadRequest("PLAN_SEVEN_DAY_QUOTA_CONFLICT", "seven day quota cannot be set and cleared in the same request")
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return infraerrors.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if req.Price != nil && *req.Price <= 0 {
		return infraerrors.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if req.ValidityDays != nil && *req.ValidityDays <= 0 {
		return infraerrors.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if req.ValidityUnit != nil && strings.TrimSpace(*req.ValidityUnit) == "" {
		return infraerrors.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if req.OriginalPrice != nil && *req.OriginalPrice < 0 {
		return infraerrors.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	if req.SevenDayQuotaUSD != nil && *req.SevenDayQuotaUSD < 0 {
		return infraerrors.BadRequest("PLAN_SEVEN_DAY_QUOTA_INVALID", "seven day quota must be >= 0")
	}
	if req.SeatLimit.Set && req.SeatLimit.Value != nil && *req.SeatLimit.Value < 0 {
		return infraerrors.BadRequest("PLAN_SEAT_LIMIT_INVALID", "seat limit must be >= 0")
	}
	return nil
}

// --- Plan CRUD ---

func (s *PaymentConfigService) ListPlans(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*dbent.SubscriptionPlan, error) {
	seatLimit, virtualStart, virtualTotal, err := normalizeCreatePlanSeatRange(req.SeatLimit, req.VirtualSeatStart, req.VirtualSeatTotal)
	if err != nil {
		return nil, err
	}
	if err := validatePlanRequired(req.Name, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice, seatLimit); err != nil {
		return nil, err
	}
	if req.SevenDayQuotaUSD != nil && *req.SevenDayQuotaUSD < 0 {
		return nil, infraerrors.BadRequest("PLAN_SEVEN_DAY_QUOTA_INVALID", "seven day quota must be >= 0")
	}
	currency, err := normalizePlanCurrency(req.Currency)
	if err != nil {
		return nil, err
	}
	b := s.entClient.SubscriptionPlan.Create().
		SetName(req.Name).SetDescription(req.Description).
		SetPrice(req.Price).SetCurrency(currency).SetValidityDays(req.ValidityDays).SetValidityUnit(req.ValidityUnit).
		SetFeatures(req.Features).SetProductName(req.ProductName).
		SetForSale(req.ForSale).SetSortOrder(req.SortOrder)
	if req.OriginalPrice != nil {
		b.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.SevenDayQuotaUSD != nil {
		b.SetSevenDayQuotaUsd(*req.SevenDayQuotaUSD)
	}
	if seatLimit != nil {
		b.SetSeatLimit(*seatLimit)
		b.SetVirtualSeatStart(*virtualStart)
		b.SetVirtualSeatTotal(*virtualTotal)
	}
	plan, err := b.Save(ctx)
	if err != nil {
		return nil, err
	}
	s.InvalidatePublicPlansCache()
	return plan, nil
}

// UpdatePlan updates a subscription plan by ID (patch semantics).
// NOTE: This function exceeds 30 lines due to per-field nil-check patch update boilerplate
// plus a validation guard for non-nil fields.
func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanPatch(req); err != nil {
		return nil, err
	}
	u := s.entClient.SubscriptionPlan.UpdateOneID(id)
	if req.Name != nil {
		u.SetName(*req.Name)
	}
	if req.Description != nil {
		u.SetDescription(*req.Description)
	}
	if req.Price != nil {
		u.SetPrice(*req.Price)
	}
	if req.OriginalPrice != nil {
		u.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.SevenDayQuotaUSD != nil {
		u.SetSevenDayQuotaUsd(*req.SevenDayQuotaUSD)
	} else if req.ClearSevenDayQuotaUSD {
		u.ClearSevenDayQuotaUsd()
	}
	if req.Currency != nil {
		currency, err := normalizePlanCurrency(*req.Currency)
		if err != nil {
			return nil, err
		}
		u.SetCurrency(currency)
	}
	if req.ValidityDays != nil {
		u.SetValidityDays(*req.ValidityDays)
	}
	if req.ValidityUnit != nil {
		u.SetValidityUnit(*req.ValidityUnit)
	}
	if req.Features != nil {
		u.SetFeatures(*req.Features)
	}
	if req.ProductName != nil {
		u.SetProductName(*req.ProductName)
	}
	if req.ForSale != nil {
		u.SetForSale(*req.ForSale)
	}
	if req.SortOrder != nil {
		u.SetSortOrder(*req.SortOrder)
	}
	if err := applyPlanSeatRangeUpdate(u, req); err != nil {
		return nil, err
	}
	plan, err := u.Save(ctx)
	if err != nil {
		return nil, err
	}
	s.InvalidatePublicPlansCache()
	return plan, nil
}

func applyPlanSeatRangeUpdate(u *dbent.SubscriptionPlanUpdateOne, req UpdatePlanRequest) error {
	seatLimit, virtualRangeSet, err := deriveSeatLimitFromOptionalVirtualRange(req.VirtualSeatStart, req.VirtualSeatTotal)
	if err != nil {
		return err
	}
	if virtualRangeSet {
		if req.SeatLimit.Set {
			if err := ensureSeatLimitMatchesVirtualRange(req.SeatLimit.Value, seatLimit); err != nil {
				return err
			}
		}
		if seatLimit == nil {
			u.ClearSeatLimit()
			u.ClearVirtualSeatStart()
			u.ClearVirtualSeatTotal()
			return nil
		}
		u.SetSeatLimit(*seatLimit)
		u.SetVirtualSeatStart(*req.VirtualSeatStart.Value)
		u.SetVirtualSeatTotal(*req.VirtualSeatTotal.Value)
		return nil
	}
	if req.SeatLimit.Set {
		if req.SeatLimit.Value == nil {
			u.ClearSeatLimit()
			u.ClearVirtualSeatStart()
			u.ClearVirtualSeatTotal()
			return nil
		}
		start, total := virtualSeatRangeFromLimit(req.SeatLimit.Value)
		u.SetSeatLimit(*req.SeatLimit.Value)
		u.SetVirtualSeatStart(*start)
		u.SetVirtualSeatTotal(*total)
	}
	return nil
}

func (s *PaymentConfigService) DeletePlan(ctx context.Context, id int64) error {
	count, err := s.countPendingOrdersByPlan(ctx, id)
	if err != nil {
		return fmt.Errorf("check pending orders: %w", err)
	}
	if count > 0 {
		return infraerrors.Conflict("PENDING_ORDERS",
			fmt.Sprintf("this plan has %d in-progress orders and cannot be deleted — wait for orders to complete first", count))
	}
	if err := s.entClient.SubscriptionPlan.DeleteOneID(id).Exec(ctx); err != nil {
		return err
	}
	s.InvalidatePublicPlansCache()
	return nil
}

// GetPlan returns a subscription plan by ID.
func (s *PaymentConfigService) GetPlan(ctx context.Context, id int64) (*dbent.SubscriptionPlan, error) {
	plan, err := s.entClient.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return plan, nil
}

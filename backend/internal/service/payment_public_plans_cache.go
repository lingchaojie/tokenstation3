package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// PublicPlansForSale returns public subscription plan data with a short in-memory TTL.
// The homepage calls this endpoint, so cache it to avoid turning public traffic into
// repeated plan + seat summary database scans. Mutations that change plan metadata or
// active seat usage call InvalidatePublicPlansCache for immediate freshness.
func (s *PaymentConfigService) PublicPlansForSale(ctx context.Context) ([]PublicPlanResponse, error) {
	now := time.Now()
	s.publicPlansCacheMu.RLock()
	if s.publicPlansCache.expiresAt.After(now) {
		plans := clonePublicPlans(s.publicPlansCache.plans)
		s.publicPlansCacheMu.RUnlock()
		return plans, nil
	}
	version := s.publicPlansCacheVersion
	s.publicPlansCacheMu.RUnlock()

	value, err, _ := s.publicPlansCacheGroup.Do("public-plans", func() (any, error) {
		plans, err := s.listPublicPlansForSale(ctx)
		if err != nil {
			return nil, err
		}

		s.publicPlansCacheMu.Lock()
		if s.publicPlansCacheVersion == version {
			s.publicPlansCache = publicPlansCacheEntry{
				plans:     clonePublicPlans(plans),
				expiresAt: now.Add(publicPlansCacheTTL),
			}
		}
		s.publicPlansCacheMu.Unlock()

		return plans, nil
	})
	if err != nil {
		return nil, err
	}
	plans, ok := value.([]PublicPlanResponse)
	if !ok {
		return nil, fmt.Errorf("public plans cache returned unexpected type %T", value)
	}
	return clonePublicPlans(plans), nil
}

func (s *PaymentConfigService) listPublicPlansForSale(ctx context.Context) ([]PublicPlanResponse, error) {
	plans, err := s.ListPlansForSale(ctx)
	if err != nil {
		return nil, err
	}
	seatSummaries, err := s.SeatSummariesForPlans(ctx, plans)
	if err != nil {
		return nil, err
	}

	result := make([]PublicPlanResponse, 0, len(plans))
	for _, p := range plans {
		plan := publicPlanResponseFromEnt(p)
		applySeatSummaryToPublicPlanResponse(&plan, seatSummaries[p.ID])
		result = append(result, plan)
	}
	return result, nil
}

func publicPlanResponseFromEnt(p *dbent.SubscriptionPlan) PublicPlanResponse {
	if p == nil {
		return PublicPlanResponse{}
	}
	return PublicPlanResponse{
		ID:               int64(p.ID),
		Name:             p.Name,
		Description:      p.Description,
		Price:            p.Price,
		OriginalPrice:    p.OriginalPrice,
		Currency:         p.Currency,
		SevenDayQuotaUSD: p.SevenDayQuotaUsd,
		ValidityDays:     p.ValidityDays,
		ValidityUnit:     p.ValidityUnit,
		Features:         splitPlanFeatures(p.Features),
		SortOrder:        p.SortOrder,
		VirtualSeatStart: p.VirtualSeatStart,
		VirtualSeatTotal: p.VirtualSeatTotal,
	}
}

func applySeatSummaryToPublicPlanResponse(plan *PublicPlanResponse, summary PlanSeatSummary) {
	if plan == nil {
		return
	}
	plan.SeatLimit = summary.SeatLimit
	plan.SeatUsed = summary.SeatUsed
	plan.SeatAvailable = summary.SeatAvailable
	plan.SeatFull = summary.SeatFull
	plan.SeatOverLimit = summary.SeatOverLimit
}

func splitPlanFeatures(raw string) []string {
	if raw == "" {
		return []string{}
	}
	out := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// InvalidatePublicPlansCache clears the homepage/public plan payload cache.
func (s *PaymentConfigService) InvalidatePublicPlansCache() {
	if s == nil {
		return
	}
	s.publicPlansCacheMu.Lock()
	s.publicPlansCache = publicPlansCacheEntry{}
	s.publicPlansCacheVersion++
	s.publicPlansCacheMu.Unlock()
	s.publicPlansCacheGroup.Forget("public-plans")
}

func clonePublicPlans(plans []PublicPlanResponse) []PublicPlanResponse {
	if plans == nil {
		return nil
	}
	out := make([]PublicPlanResponse, len(plans))
	for i := range plans {
		out[i] = clonePublicPlan(plans[i])
	}
	return out
}

func clonePublicPlan(plan PublicPlanResponse) PublicPlanResponse {
	plan.OriginalPrice = cloneFloat64Ptr(plan.OriginalPrice)
	plan.SevenDayQuotaUSD = cloneFloat64Ptr(plan.SevenDayQuotaUSD)
	plan.Features = append([]string(nil), plan.Features...)
	plan.SeatLimit = cloneIntPtr(plan.SeatLimit)
	plan.SeatAvailable = cloneIntPtr(plan.SeatAvailable)
	plan.VirtualSeatStart = cloneIntPtr(plan.VirtualSeatStart)
	plan.VirtualSeatTotal = cloneIntPtr(plan.VirtualSeatTotal)
	return plan
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	clone := *v
	return &clone
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	clone := *v
	return &clone
}

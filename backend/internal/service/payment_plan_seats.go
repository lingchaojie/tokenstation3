package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrPlanSeatLimitReached = infraerrors.Conflict("PLAN_SEAT_LIMIT_REACHED", "该套餐名额已满，暂不支持新用户开通。")

type PlanSeatSummary struct {
	SeatLimit     *int `json:"seat_limit"`
	SeatUsed      int  `json:"seat_used"`
	SeatAvailable *int `json:"seat_available,omitempty"`
	SeatFull      bool `json:"seat_full"`
	SeatOverLimit bool `json:"seat_over_limit"`
}

func newPlanSeatSummary(limit *int, used int) PlanSeatSummary {
	summary := PlanSeatSummary{SeatLimit: limit, SeatUsed: used}
	if limit == nil {
		return summary
	}

	available := *limit - used
	if available < 0 {
		available = 0
	}
	summary.SeatAvailable = &available
	summary.SeatFull = used >= *limit
	summary.SeatOverLimit = used > *limit
	return summary
}

func (s *PaymentConfigService) entForContext(ctx context.Context) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return s.entClient
}

func (s *PaymentConfigService) SeatSummariesForPlans(ctx context.Context, plans []*dbent.SubscriptionPlan) (map[int64]PlanSeatSummary, error) {
	out := make(map[int64]PlanSeatSummary, len(plans))
	ids := make([]int64, 0, len(plans))
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		ids = append(ids, plan.ID)
		out[plan.ID] = newPlanSeatSummary(plan.SeatLimit, 0)
	}
	if len(ids) == 0 {
		return out, nil
	}

	var rows []struct {
		PlanID int64 `json:"plan_id"`
		UserID int64 `json:"user_id"`
	}
	err := s.entForContext(ctx).UserSubscription.Query().
		Where(
			usersubscription.PlanIDIn(ids...),
			usersubscription.StatusEQ(SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Unique(true).
		Select(usersubscription.FieldPlanID, usersubscription.FieldUserID).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("count plan seats: %w", err)
	}

	usedByPlan := make(map[int64]int, len(ids))
	for _, row := range rows {
		usedByPlan[row.PlanID]++
	}
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		out[plan.ID] = newPlanSeatSummary(plan.SeatLimit, usedByPlan[plan.ID])
	}
	return out, nil
}

func (s *PaymentConfigService) SeatSummaryForPlan(ctx context.Context, plan *dbent.SubscriptionPlan) (PlanSeatSummary, error) {
	if plan == nil {
		return PlanSeatSummary{}, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	summaries, err := s.SeatSummariesForPlans(ctx, []*dbent.SubscriptionPlan{plan})
	if err != nil {
		return PlanSeatSummary{}, err
	}
	return summaries[plan.ID], nil
}

func (s *PaymentConfigService) UserHasActivePlanSeat(ctx context.Context, planID, userID int64) (bool, error) {
	return s.entForContext(ctx).UserSubscription.Query().
		Where(
			usersubscription.PlanIDEQ(planID),
			usersubscription.UserIDEQ(userID),
			usersubscription.StatusEQ(SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Exist(ctx)
}

func (s *PaymentConfigService) ValidatePlanSeatAvailable(ctx context.Context, plan *dbent.SubscriptionPlan, userID int64) error {
	if plan == nil || plan.SeatLimit == nil {
		return nil
	}

	hasSeat, err := s.UserHasActivePlanSeat(ctx, plan.ID, userID)
	if err != nil {
		return fmt.Errorf("check user plan seat: %w", err)
	}
	if hasSeat {
		return nil
	}

	summary, err := s.SeatSummaryForPlan(ctx, plan)
	if err != nil {
		return err
	}
	if !summary.SeatFull {
		return nil
	}

	return ErrPlanSeatLimitReached.WithMetadata(map[string]string{
		"plan_id": strconv.FormatInt(plan.ID, 10),
		"used":    strconv.Itoa(summary.SeatUsed),
		"limit":   strconv.Itoa(*plan.SeatLimit),
	})
}

func (s *PaymentConfigService) LockPlanForUpdate(ctx context.Context, planID int64) (*dbent.SubscriptionPlan, error) {
	client := s.entForContext(ctx)
	query := client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDEQ(planID))
	if client.Driver().Dialect() == dialect.Postgres {
		query.ForUpdate()
	}

	plan, err := query.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found").WithCause(err)
		}
		return nil, fmt.Errorf("lock plan for update: %w", err)
	}
	return plan, nil
}

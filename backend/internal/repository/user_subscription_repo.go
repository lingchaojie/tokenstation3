package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userSubscriptionRepository struct {
	client *dbent.Client
}

func NewUserSubscriptionRepository(client *dbent.Client) service.UserSubscriptionRepository {
	return &userSubscriptionRepository{client: client}
}

func (r *userSubscriptionRepository) Create(ctx context.Context, sub *service.UserSubscription) error {
	if sub == nil {
		return service.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.Create().
		SetUserID(sub.UserID).
		SetNillablePlanID(sub.PlanID).
		SetNillablePlanName(sub.PlanName).
		SetNillableSevenDayLimitUsd(sub.SevenDayLimitUSD).
		SetNillableScheduledPlanID(sub.ScheduledPlanID).
		SetNillableScheduledPlanName(sub.ScheduledPlanName).
		SetNillableScheduledSevenDayLimitUsd(sub.ScheduledSevenDayLimitUSD).
		SetNillableScheduledPlanEffectiveAt(sub.ScheduledPlanEffectiveAt).
		SetNillableScheduledExpiresAt(sub.ScheduledExpiresAt).
		SetNillableScheduledOrderID(sub.ScheduledOrderID).
		SetExpiresAt(sub.ExpiresAt).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy)

	if sub.StartsAt.IsZero() {
		builder.SetStartsAt(time.Now())
	} else {
		builder.SetStartsAt(sub.StartsAt)
	}
	if sub.GroupID > 0 {
		builder.SetGroupID(sub.GroupID)
	}
	if sub.Status != "" {
		builder.SetStatus(sub.Status)
	}
	if !sub.AssignedAt.IsZero() {
		builder.SetAssignedAt(sub.AssignedAt)
	}
	// Keep compatibility with historical behavior: always store notes as a string value.
	builder.SetNotes(sub.Notes)
	if sub.PlanID != nil {
		builder.SetPlanID(*sub.PlanID)
	}

	created, err := builder.Save(ctx)
	if err == nil {
		applyUserSubscriptionEntityToService(sub, created)
	}
	return translatePersistenceError(err, nil, service.ErrSubscriptionAlreadyExists)
}

func (r *userSubscriptionRepository) GetByID(ctx context.Context, id int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithGroup().
		WithAssignedByUser().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetByIDIncludeDeleted(ctx context.Context, id int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := mixins.SkipSoftDelete(ctx)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithGroup().
		WithAssignedByUser().
		Only(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToServicePreserveStatus(m), nil
}

func (r *userSubscriptionRepository) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(groupID)).
		WithGroup().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetGenericByUserID(ctx context.Context, userID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDIsNil()).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetActiveGenericByUserID(ctx context.Context, userID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.GroupIDIsNil(),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetActivePlanBackedByUserID(ctx context.Context, userID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.PlanIDNotNil(),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) Update(ctx context.Context, sub *service.UserSubscription) error {
	if sub == nil {
		return service.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.UpdateOneID(sub.ID).
		SetUserID(sub.UserID).
		SetStartsAt(sub.StartsAt).
		SetExpiresAt(sub.ExpiresAt).
		SetStatus(sub.Status).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy).
		SetAssignedAt(sub.AssignedAt).
		SetNotes(sub.Notes)

	if sub.GroupID > 0 {
		builder.SetGroupID(sub.GroupID)
	} else {
		builder.ClearGroupID()
	}
	if sub.PlanID != nil {
		builder.SetPlanID(*sub.PlanID)
	} else {
		builder.ClearPlanID()
	}
	if sub.PlanName != nil {
		builder.SetPlanName(*sub.PlanName)
	} else {
		builder.ClearPlanName()
	}
	if sub.SevenDayLimitUSD != nil {
		builder.SetSevenDayLimitUsd(*sub.SevenDayLimitUSD)
	} else {
		builder.ClearSevenDayLimitUsd()
	}
	if sub.ScheduledPlanID != nil {
		builder.SetScheduledPlanID(*sub.ScheduledPlanID)
	} else {
		builder.ClearScheduledPlanID()
	}
	if sub.ScheduledPlanName != nil {
		builder.SetScheduledPlanName(*sub.ScheduledPlanName)
	} else {
		builder.ClearScheduledPlanName()
	}
	if sub.ScheduledSevenDayLimitUSD != nil {
		builder.SetScheduledSevenDayLimitUsd(*sub.ScheduledSevenDayLimitUSD)
	} else {
		builder.ClearScheduledSevenDayLimitUsd()
	}
	if sub.ScheduledPlanEffectiveAt != nil {
		builder.SetScheduledPlanEffectiveAt(*sub.ScheduledPlanEffectiveAt)
	} else {
		builder.ClearScheduledPlanEffectiveAt()
	}
	if sub.ScheduledExpiresAt != nil {
		builder.SetScheduledExpiresAt(*sub.ScheduledExpiresAt)
	} else {
		builder.ClearScheduledExpiresAt()
	}
	if sub.ScheduledOrderID != nil {
		builder.SetScheduledOrderID(*sub.ScheduledOrderID)
	} else {
		builder.ClearScheduledOrderID()
	}

	updated, err := builder.Save(ctx)
	if err == nil {
		applyUserSubscriptionEntityToService(sub, updated)
		return nil
	}
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, service.ErrSubscriptionAlreadyExists)
}

func (r *userSubscriptionRepository) Delete(ctx context.Context, id int64) error {
	// Match GORM semantics: deleting a missing row is not an error.
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.Delete().Where(usersubscription.IDEQ(id)).Exec(ctx)
	return err
}

func (r *userSubscriptionRepository) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := mixins.SkipSoftDelete(ctx)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(restoredStatus).
		ClearDeletedAt().
		SetUpdatedAt(time.Now()).
		Save(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, service.ErrSubscriptionRestoreConflict)
	}
	return r.GetByID(ctx, subscriptionID)
}

func (r *userSubscriptionRepository) ListByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID)).
		WithGroup().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

func (r *userSubscriptionRepository) ListActiveByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

func (r *userSubscriptionRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.UserSubscription.Query().Where(usersubscription.GroupIDEQ(groupID))

	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	subs, err := q.
		WithUser().
		WithGroup().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	return userSubscriptionEntitiesToService(subs), paginationResultFromTotal(int64(total), params), nil
}

func (r *userSubscriptionRepository) List(ctx context.Context, params pagination.PaginationParams, userID, groupID *int64, status, platform, sortBy, sortOrder string) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.UserSubscription.Query()
	includeSoftDeleted := status == "" || status == service.SubscriptionStatusRevoked
	if userID != nil {
		q = q.Where(usersubscription.UserIDEQ(*userID))
	}
	if groupID != nil {
		q = q.Where(usersubscription.GroupIDEQ(*groupID))
	}
	if platform != "" {
		groupPredicates := []predicate.Group{group.PlatformEQ(platform)}
		if includeSoftDeleted {
			groupPredicates = append(groupPredicates, group.DeletedAtIsNil())
		}
		q = q.Where(usersubscription.HasGroupWith(groupPredicates...))
	}

	// Status filtering with real-time expiration check
	now := time.Now()
	switch status {
	case service.SubscriptionStatusActive:
		// Active: status is active AND not yet expired
		q = q.Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(now),
		)
	case service.SubscriptionStatusExpired:
		// Expired: status is expired OR (status is active but already expired)
		q = q.Where(
			usersubscription.Or(
				usersubscription.StatusEQ(service.SubscriptionStatusExpired),
				usersubscription.And(
					usersubscription.StatusEQ(service.SubscriptionStatusActive),
					usersubscription.ExpiresAtLTE(now),
				),
			),
		)
	case service.SubscriptionStatusRevoked:
		// Revoked is a DTO/API display state backed by user_subscriptions.deleted_at.
		q = q.Where(usersubscription.DeletedAtNotNil())
	case "":
		// No filter. Use SkipSoftDelete below so admin "all status" includes revoked history.
	default:
		// Other persisted status.
		q = q.Where(usersubscription.StatusEQ(status))
	}

	queryCtx := ctx
	if includeSoftDeleted {
		queryCtx = mixins.SkipSoftDelete(ctx)
	}

	total, err := q.Clone().Count(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	if !includeSoftDeleted {
		q = q.WithUser().WithGroup().WithAssignedByUser()
	}

	// Determine sort field
	var field string
	switch sortBy {
	case "expires_at":
		field = usersubscription.FieldExpiresAt
	case "status":
		field = usersubscription.FieldStatus
	default:
		field = usersubscription.FieldCreatedAt
	}

	// Determine sort order (default: desc)
	if sortOrder == "asc" && sortBy != "" {
		q = q.Order(dbent.Asc(field))
	} else {
		q = q.Order(dbent.Desc(field))
	}

	subs, err := q.
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	result := userSubscriptionEntitiesToService(subs)
	if includeSoftDeleted {
		if err := r.attachUserSubscriptionRelations(ctx, result); err != nil {
			return nil, nil, err
		}
	}

	return result, paginationResultFromTotal(int64(total), params), nil
}

func (r *userSubscriptionRepository) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	client := clientFromContext(ctx, r.client)
	return client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(groupID)).
		Exist(ctx)
}

func (r *userSubscriptionRepository) ExistsGenericByUserID(ctx context.Context, userID int64) (bool, error) {
	client := clientFromContext(ctx, r.client)
	return client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDIsNil()).
		Exist(ctx)
}

func (r *userSubscriptionRepository) ExistsActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return r.ExistsByUserIDAndGroupID(ctx, userID, groupID)
}

func (r *userSubscriptionRepository) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetExpiresAt(newExpiresAt).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(status).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetNotes(notes).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) UpdatePlanSnapshot(ctx context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, windowStart time.Time, expiresAt time.Time, notes *string) error {
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	existing, err := client.UserSubscription.Get(ctx, id)
	if err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}

	update := client.UserSubscription.UpdateOneID(id).
		SetExpiresAt(expiresAt).
		SetStatus(service.SubscriptionStatusActive).
		SetUpdatedAt(now).
		ClearScheduledPlanID().
		ClearScheduledPlanName().
		ClearScheduledSevenDayLimitUsd().
		ClearScheduledPlanEffectiveAt().
		ClearScheduledExpiresAt().
		ClearScheduledOrderID()
	if !existing.ExpiresAt.After(now) {
		update.SetStartsAt(windowStart).
			SetDailyWindowStart(windowStart).
			SetWeeklyWindowStart(windowStart).
			SetMonthlyWindowStart(windowStart).
			SetDailyUsageUsd(0).
			SetWeeklyUsageUsd(0).
			SetMonthlyUsageUsd(0)
	} else {
		update.SetWeeklyWindowStart(windowStart).
			SetWeeklyUsageUsd(0)
	}
	if planID != nil {
		update.SetPlanID(*planID)
	} else {
		update.ClearPlanID()
	}
	if planName != nil {
		update.SetPlanName(*planName)
	} else {
		update.ClearPlanName()
	}
	if sevenDayLimitUSD != nil {
		update.SetSevenDayLimitUsd(*sevenDayLimitUSD)
	} else {
		update.ClearSevenDayLimitUsd()
	}
	if notes != nil {
		update.SetNotes(*notes)
	}
	_, err = update.Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) SchedulePlanChange(ctx context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, effectiveAt time.Time, expiresAt time.Time, orderID *int64, notes *string) error {
	client := clientFromContext(ctx, r.client)
	update := client.UserSubscription.UpdateOneID(id).
		SetScheduledPlanEffectiveAt(effectiveAt).
		SetScheduledExpiresAt(expiresAt).
		SetUpdatedAt(time.Now())
	if planID != nil {
		update.SetScheduledPlanID(*planID)
	} else {
		update.ClearScheduledPlanID()
	}
	if planName != nil {
		update.SetScheduledPlanName(*planName)
	} else {
		update.ClearScheduledPlanName()
	}
	if sevenDayLimitUSD != nil {
		update.SetScheduledSevenDayLimitUsd(*sevenDayLimitUSD)
	} else {
		update.ClearScheduledSevenDayLimitUsd()
	}
	if orderID != nil {
		update.SetScheduledOrderID(*orderID)
	} else {
		update.ClearScheduledOrderID()
	}
	if notes != nil {
		update.SetNotes(*notes)
	}
	_, err := update.Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ClearScheduledPlanChange(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(id).
		ClearScheduledPlanID().
		ClearScheduledPlanName().
		ClearScheduledSevenDayLimitUsd().
		ClearScheduledPlanEffectiveAt().
		ClearScheduledExpiresAt().
		ClearScheduledOrderID().
		SetUpdatedAt(time.Now()).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ApplyScheduledPlanChange(ctx context.Context, id int64, now time.Time) (*service.UserSubscription, bool, error) {
	client := clientFromContext(ctx, r.client)
	const updateSQL = `
		UPDATE user_subscriptions
		SET
			plan_id = scheduled_plan_id,
			plan_name = scheduled_plan_name,
			seven_day_limit_usd = scheduled_seven_day_limit_usd,
			starts_at = scheduled_plan_effective_at,
			expires_at = scheduled_expires_at,
			status = $3,
			daily_window_start = scheduled_plan_effective_at,
			weekly_window_start = scheduled_plan_effective_at,
			monthly_window_start = scheduled_plan_effective_at,
			daily_usage_usd = 0,
			weekly_usage_usd = 0,
			monthly_usage_usd = 0,
			scheduled_plan_id = NULL,
			scheduled_plan_name = NULL,
			scheduled_seven_day_limit_usd = NULL,
			scheduled_plan_effective_at = NULL,
			scheduled_expires_at = NULL,
			scheduled_order_id = NULL,
			updated_at = $2
		WHERE id = $1
			AND deleted_at IS NULL
			AND scheduled_plan_effective_at IS NOT NULL
			AND scheduled_plan_effective_at <= $2
		RETURNING id
	`
	var appliedID int64
	err := scanSingleRow(ctx, client, updateSQL, []any{id, now, service.SubscriptionStatusActive}, &appliedID)
	if err == nil {
		sub, getErr := r.GetByID(ctx, appliedID)
		if getErr != nil {
			return nil, true, getErr
		}
		return sub, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	exists, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		Exist(ctx)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, service.ErrSubscriptionNotFound
	}
	return nil, false, nil
}

func (r *userSubscriptionRepository) UpdatePlanID(ctx context.Context, subscriptionID int64, planID int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetPlanID(planID).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ActivateWindows(ctx context.Context, id int64, start time.Time) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(id).
		SetDailyWindowStart(start).
		SetWeeklyWindowStart(start).
		SetMonthlyWindowStart(start).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ResetUsageWindows(ctx context.Context, id int64, resetDaily, resetWeekly, resetMonthly bool, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	update := client.UserSubscription.UpdateOneID(id)
	if resetDaily {
		update.SetDailyUsageUsd(0).SetDailyWindowStart(newWindowStart)
	}
	if resetWeekly {
		update.SetWeeklyUsageUsd(0).SetWeeklyWindowStart(newWindowStart)
	}
	if resetMonthly {
		update.SetMonthlyUsageUsd(0).SetMonthlyWindowStart(newWindowStart)
	}
	_, err := update.Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ResetDailyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.DailyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.DailyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetDailyUsageUsd(0).
		SetDailyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) ResetWeeklyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.WeeklyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.Or(
			usersubscription.WeeklyWindowStartEQ(*expectedWindowStart),
			usersubscription.WeeklyWindowStartLTE(expectedWindowStart.Add(-7*24*time.Hour)),
		))
	}
	n, err := query.
		SetWeeklyUsageUsd(0).
		SetWeeklyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) ResetMonthlyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.MonthlyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.MonthlyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetMonthlyUsageUsd(0).
		SetMonthlyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) translateConditionalWindowReset(ctx context.Context, client *dbent.Client, id int64, affected int, err error) error {
	if err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	if affected > 0 {
		return nil
	}

	// A stale reset is an expected no-op: another request already advanced the
	// window. Preserve not-found semantics for callers that target a missing row.
	exists, err := client.UserSubscription.Query().Where(usersubscription.IDEQ(id)).Exist(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	if !exists {
		return service.ErrSubscriptionNotFound
	}
	return nil
}

// IncrementUsage 原子性地累加订阅用量。
// 限额检查已在请求前由 BillingCacheService.CheckBillingEligibility 完成，
// 此处仅负责记录实际消费，确保消费数据的完整性。
func (r *userSubscriptionRepository) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions us
		SET
			daily_usage_usd = us.daily_usage_usd + $1,
			weekly_usage_usd = us.weekly_usage_usd + $1,
			monthly_usage_usd = us.monthly_usage_usd + $1,
			updated_at = NOW()
		WHERE us.id = $2
			AND us.deleted_at IS NULL
			AND (
				us.group_id IS NULL
				OR EXISTS (
					SELECT 1
					FROM groups g
					WHERE g.id = us.group_id
						AND g.deleted_at IS NULL
				)
			)
	`

	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, updateSQL, costUSD, id)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if affected > 0 {
		return nil
	}

	// affected == 0：订阅不存在或已删除
	return service.ErrSubscriptionNotFound
}

func (r *userSubscriptionRepository) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	dueIDs, err := client.UserSubscription.Query().
		Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ScheduledPlanEffectiveAtNotNil(),
			usersubscription.ScheduledPlanEffectiveAtLTE(now),
		).
		IDs(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range dueIDs {
		if _, _, err := r.ApplyScheduledPlanChange(ctx, id, now); err != nil {
			return 0, err
		}
	}
	n, err := client.UserSubscription.Update().
		Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtLTE(now),
			usersubscription.ScheduledPlanEffectiveAtIsNil(),
		).
		SetStatus(service.SubscriptionStatusExpired).
		Save(ctx)
	return int64(n), err
}

// Extra repository helpers (currently used only by integration tests).

func (r *userSubscriptionRepository) ListExpired(ctx context.Context) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtLTE(time.Now()),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

func (r *userSubscriptionRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	count, err := client.UserSubscription.Query().Where(usersubscription.GroupIDEQ(groupID)).Count(ctx)
	return int64(count), err
}

func (r *userSubscriptionRepository) CountActiveByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	count, err := client.UserSubscription.Query().
		Where(
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Count(ctx)
	return int64(count), err
}

func (r *userSubscriptionRepository) DeleteByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.UserSubscription.Delete().Where(usersubscription.GroupIDEQ(groupID)).Exec(ctx)
	return int64(n), err
}

func (r *userSubscriptionRepository) attachUserSubscriptionRelations(ctx context.Context, subs []service.UserSubscription) error {
	if len(subs) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(subs))
	groupIDs := make([]int64, 0, len(subs))
	assignedByIDs := make([]int64, 0, len(subs))
	for i := range subs {
		userIDs = append(userIDs, subs[i].UserID)
		groupIDs = append(groupIDs, subs[i].GroupID)
		if subs[i].AssignedBy != nil {
			assignedByIDs = append(assignedByIDs, *subs[i].AssignedBy)
		}
	}

	client := clientFromContext(ctx, r.client)
	users, err := client.User.Query().Where(user.IDIn(uniqueInt64s(userIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	userByID := make(map[int64]*service.User, len(users))
	for _, u := range users {
		userByID[u.ID] = userEntityToService(u)
	}

	groups, err := client.Group.Query().Where(group.IDIn(uniqueInt64s(groupIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	groupByID := make(map[int64]*service.Group, len(groups))
	for _, g := range groups {
		groupByID[g.ID] = groupEntityToService(g)
	}

	assignedByID := map[int64]*service.User{}
	if len(assignedByIDs) > 0 {
		assignedUsers, err := client.User.Query().Where(user.IDIn(uniqueInt64s(assignedByIDs)...)).All(ctx)
		if err != nil {
			return err
		}
		assignedByID = make(map[int64]*service.User, len(assignedUsers))
		for _, u := range assignedUsers {
			assignedByID[u.ID] = userEntityToService(u)
		}
	}

	for i := range subs {
		subs[i].User = userByID[subs[i].UserID]
		subs[i].Group = groupByID[subs[i].GroupID]
		if subs[i].AssignedBy != nil {
			subs[i].AssignedByUser = assignedByID[*subs[i].AssignedBy]
		}
	}
	return nil
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func userSubscriptionEntityToService(m *dbent.UserSubscription) *service.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, true)
}

func userSubscriptionEntityToServicePreserveStatus(m *dbent.UserSubscription) *service.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, false)
}

func userSubscriptionEntityToServiceWithStatusMapping(m *dbent.UserSubscription, mapDeletedToRevoked bool) *service.UserSubscription {
	if m == nil {
		return nil
	}
	status := m.Status
	if mapDeletedToRevoked && m.DeletedAt != nil {
		status = service.SubscriptionStatusRevoked
	}
	out := &service.UserSubscription{
		ID:                        m.ID,
		UserID:                    m.UserID,
		GroupID:                   m.GroupID,
		PlanID:                    m.PlanID,
		PlanName:                  m.PlanName,
		SevenDayLimitUSD:          m.SevenDayLimitUsd,
		ScheduledPlanID:           m.ScheduledPlanID,
		ScheduledPlanName:         m.ScheduledPlanName,
		ScheduledSevenDayLimitUSD: m.ScheduledSevenDayLimitUsd,
		ScheduledPlanEffectiveAt:  m.ScheduledPlanEffectiveAt,
		ScheduledExpiresAt:        m.ScheduledExpiresAt,
		ScheduledOrderID:          m.ScheduledOrderID,
		StartsAt:                  m.StartsAt,
		ExpiresAt:                 m.ExpiresAt,
		Status:                    m.Status,
		DailyWindowStart:          m.DailyWindowStart,
		WeeklyWindowStart:         m.WeeklyWindowStart,
		MonthlyWindowStart:        m.MonthlyWindowStart,
		DailyUsageUSD:             m.DailyUsageUsd,
		WeeklyUsageUSD:            m.WeeklyUsageUsd,
		MonthlyUsageUSD:           m.MonthlyUsageUsd,
		AssignedBy:                m.AssignedBy,
		AssignedAt:                m.AssignedAt,
		Notes:                     derefString(m.Notes),
		CreatedAt:                 m.CreatedAt,
		UpdatedAt:                 m.UpdatedAt,
		DeletedAt:                 m.DeletedAt,
	}
	out.Status = status
	if m.Edges.User != nil {
		out.User = userEntityToService(m.Edges.User)
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToService(m.Edges.Group)
	}
	if m.Edges.AssignedByUser != nil {
		out.AssignedByUser = userEntityToService(m.Edges.AssignedByUser)
	}
	return out
}

func userSubscriptionEntitiesToService(models []*dbent.UserSubscription) []service.UserSubscription {
	out := make([]service.UserSubscription, 0, len(models))
	for i := range models {
		if s := userSubscriptionEntityToService(models[i]); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

func applyUserSubscriptionEntityToService(dst *service.UserSubscription, src *dbent.UserSubscription) {
	if dst == nil || src == nil {
		return
	}
	status := src.Status
	if src.DeletedAt != nil {
		status = service.SubscriptionStatusRevoked
	}
	dst.ID = src.ID
	dst.UserID = src.UserID
	dst.GroupID = src.GroupID
	dst.PlanID = src.PlanID
	dst.PlanName = src.PlanName
	dst.SevenDayLimitUSD = src.SevenDayLimitUsd
	dst.ScheduledPlanID = src.ScheduledPlanID
	dst.ScheduledPlanName = src.ScheduledPlanName
	dst.ScheduledSevenDayLimitUSD = src.ScheduledSevenDayLimitUsd
	dst.ScheduledPlanEffectiveAt = src.ScheduledPlanEffectiveAt
	dst.ScheduledExpiresAt = src.ScheduledExpiresAt
	dst.ScheduledOrderID = src.ScheduledOrderID
	dst.StartsAt = src.StartsAt
	dst.ExpiresAt = src.ExpiresAt
	dst.Status = status
	dst.DailyWindowStart = src.DailyWindowStart
	dst.WeeklyWindowStart = src.WeeklyWindowStart
	dst.MonthlyWindowStart = src.MonthlyWindowStart
	dst.DailyUsageUSD = src.DailyUsageUsd
	dst.WeeklyUsageUSD = src.WeeklyUsageUsd
	dst.MonthlyUsageUSD = src.MonthlyUsageUsd
	dst.AssignedBy = src.AssignedBy
	dst.AssignedAt = src.AssignedAt
	dst.Notes = derefString(src.Notes)
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
	dst.DeletedAt = src.DeletedAt
}

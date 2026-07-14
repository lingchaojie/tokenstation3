package repository

import (
	"context"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/dailycheckinclaim"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type checkInRepository struct {
	client *dbent.Client
}

func NewCheckInRepository(client *dbent.Client) service.CheckInRepository {
	return &checkInRepository{client: client}
}

func (r *checkInRepository) FindClaim(
	ctx context.Context,
	userID int64,
	activityStartAt time.Time,
	checkInDate time.Time,
) (*service.DailyCheckInClaim, error) {
	row, err := r.client.DailyCheckInClaim.Query().Where(
		dailycheckinclaim.UserIDEQ(userID),
		dailycheckinclaim.ActivityStartAtEQ(activityStartAt.UTC()),
		dailycheckinclaim.CheckInDateEQ(checkInDate.UTC()),
	).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dailyCheckInClaimEntityToService(row), nil
}

func (r *checkInRepository) CreateClaim(
	ctx context.Context,
	input service.DailyCheckInClaimInput,
) (_ *service.DailyCheckInClaim, err error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	exists, err := tx.User.Query().Where(user.IDEQ(input.UserID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, service.ErrUserNotFound
	}

	claim, err := tx.DailyCheckInClaim.Create().
		SetUserID(input.UserID).
		SetActivityStartAt(input.ActivityStartAt.UTC()).
		SetCheckInDate(input.CheckInDate.UTC()).
		SetRewardAmount(input.RewardAmount).
		SetBalanceAfter(0).
		SetClaimedAt(input.ClaimedAt.UTC()).
		Save(ctx)
	if err != nil {
		if isDailyCheckInUniqueConstraint(err) {
			return nil, service.ErrDailyCheckInAlreadyClaimed
		}
		return nil, err
	}

	updatedUser, err := tx.User.UpdateOneID(input.UserID).
		AddBalance(input.RewardAmount).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}

	claim, err = tx.DailyCheckInClaim.UpdateOneID(claim.ID).
		SetBalanceAfter(updatedUser.Balance).
		Save(ctx)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return dailyCheckInClaimEntityToService(claim), nil
}

func isDailyCheckInUniqueConstraint(err error) bool {
	if !dbent.IsConstraintError(err) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate key")
}

func dailyCheckInClaimEntityToService(row *dbent.DailyCheckInClaim) *service.DailyCheckInClaim {
	if row == nil {
		return nil
	}
	return &service.DailyCheckInClaim{
		ID:              row.ID,
		UserID:          row.UserID,
		ActivityStartAt: row.ActivityStartAt,
		CheckInDate:     row.CheckInDate,
		RewardAmount:    row.RewardAmount,
		BalanceAfter:    row.BalanceAfter,
		ClaimedAt:       row.ClaimedAt,
	}
}

package repository

import (
	"context"
	"errors"
	"fmt"
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
) (*service.DailyCheckInClaim, error) {
	var result *service.DailyCheckInClaim
	err := r.withTx(ctx, func(txCtx context.Context, txClient *dbent.Client) error {
		exists, err := txClient.User.Query().Where(user.IDEQ(input.UserID)).Exist(txCtx)
		if err != nil {
			return err
		}
		if !exists {
			return service.ErrUserNotFound
		}

		claim, err := txClient.DailyCheckInClaim.Create().
			SetUserID(input.UserID).
			SetActivityStartAt(input.ActivityStartAt.UTC()).
			SetCheckInDate(input.CheckInDate.UTC()).
			SetRewardAmount(input.RewardAmount).
			SetBalanceAfter(0).
			SetClaimedAt(input.ClaimedAt.UTC()).
			Save(txCtx)
		if err != nil {
			if isDailyCheckInUniqueConstraint(err) {
				return service.ErrDailyCheckInAlreadyClaimed
			}
			return err
		}

		grant, err := grantRewardCreditTx(txCtx, txClient, service.RewardCreditGrant{
			UserID:     input.UserID,
			CreditType: service.RewardCreditDailyCheckIn,
			SourceKey:  fmt.Sprintf("daily-check-in:%d", claim.ID),
			Amount:     input.RewardAmount,
			GrantedAt:  input.ClaimedAt.UTC(),
			ExpiresAt:  input.ExpiresAt.UTC(),
		})
		if err != nil {
			return fmt.Errorf("grant daily check-in reward credit: %w", err)
		}
		if !grant.Applied {
			return errors.New("daily check-in reward credit already exists")
		}

		claim, err = txClient.DailyCheckInClaim.UpdateOneID(claim.ID).
			SetBalanceAfter(grant.BalanceAfter).
			Save(txCtx)
		if err != nil {
			return err
		}
		result = dailyCheckInClaimEntityToService(claim)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *checkInRepository) withTx(ctx context.Context, fn func(context.Context, *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
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

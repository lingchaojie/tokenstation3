package repository

import (
	"context"
	"encoding/json"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
)

var _ service.BeginnerGuideRepository = (*userRepository)(nil)

func (r *userRepository) GetBeginnerGuideState(ctx context.Context, userID int64) (*service.BeginnerGuideState, error) {
	return getBeginnerGuideState(ctx, clientFromContext(ctx, r.client), userID, false)
}

func (r *userRepository) WithBeginnerGuideStateForUpdate(
	ctx context.Context,
	userID int64,
	update func(service.BeginnerGuideState) (service.BeginnerGuideState, error),
) (*service.BeginnerGuideState, error) {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return withLockedBeginnerGuideState(ctx, tx.Client(), userID, update)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin beginner guide transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	updated, err := withLockedBeginnerGuideState(txCtx, tx.Client(), userID, update)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit beginner guide transaction: %w", err)
	}
	return updated, nil
}

func withLockedBeginnerGuideState(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	update func(service.BeginnerGuideState) (service.BeginnerGuideState, error),
) (*service.BeginnerGuideState, error) {
	current, err := getBeginnerGuideState(ctx, client, userID, true)
	if err != nil {
		return nil, err
	}

	next, err := update(*current)
	if err != nil {
		return nil, err
	}
	return updateBeginnerGuideState(ctx, client, userID, next)
}

func getBeginnerGuideState(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	forUpdate bool,
) (*service.BeginnerGuideState, error) {
	query := client.User.Query().
		Where(dbuser.IDEQ(userID)).
		Select(
			dbuser.FieldBeginnerGuidePromptState,
			dbuser.FieldBeginnerGuideProgress,
			dbuser.FieldBeginnerGuideCompletedAt,
		)
	if forUpdate && client.Driver().Dialect() == dialect.Postgres {
		query.ForUpdate()
	}

	userEntity, err := query.Only(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"get beginner guide state: %w",
			translatePersistenceError(err, service.ErrUserNotFound, nil),
		)
	}

	return beginnerGuideStateFromEntity(userEntity)
}

func updateBeginnerGuideState(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	state service.BeginnerGuideState,
) (*service.BeginnerGuideState, error) {
	update := client.User.UpdateOneID(userID).
		SetBeginnerGuidePromptState(string(state.PromptState)).
		SetNillableBeginnerGuideCompletedAt(state.CompletedAt)

	if state.Progress == nil {
		update = update.ClearBeginnerGuideProgress()
	} else {
		raw, err := json.Marshal(state.Progress)
		if err != nil {
			return nil, fmt.Errorf("marshal beginner guide progress: %w", err)
		}
		update = update.SetBeginnerGuideProgress(raw)
	}

	userEntity, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"update beginner guide state: %w",
			translatePersistenceError(err, service.ErrUserNotFound, nil),
		)
	}

	return beginnerGuideStateFromEntity(userEntity)
}

func beginnerGuideStateFromEntity(userEntity *dbent.User) (*service.BeginnerGuideState, error) {
	var progress *service.BeginnerGuideProgress
	if len(userEntity.BeginnerGuideProgress) > 0 {
		if err := json.Unmarshal(userEntity.BeginnerGuideProgress, &progress); err != nil {
			return nil, fmt.Errorf("decode beginner guide progress: %w", err)
		}
	}

	return &service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptState(userEntity.BeginnerGuidePromptState),
		Progress:    progress,
		CompletedAt: userEntity.BeginnerGuideCompletedAt,
	}, nil
}

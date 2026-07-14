package repository

import (
	"context"
	"encoding/json"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.BeginnerGuideRepository = (*userRepository)(nil)

func (r *userRepository) GetBeginnerGuideState(ctx context.Context, userID int64) (*service.BeginnerGuideState, error) {
	userEntity, err := r.client.User.Query().
		Where(dbuser.IDEQ(userID)).
		Select(
			dbuser.FieldBeginnerGuidePromptState,
			dbuser.FieldBeginnerGuideProgress,
			dbuser.FieldBeginnerGuideCompletedAt,
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"get beginner guide state: %w",
			translatePersistenceError(err, service.ErrUserNotFound, nil),
		)
	}

	return beginnerGuideStateFromEntity(userEntity)
}

func (r *userRepository) UpdateBeginnerGuideState(ctx context.Context, userID int64, state service.BeginnerGuideState) (*service.BeginnerGuideState, error) {
	update := r.client.User.UpdateOneID(userID).
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

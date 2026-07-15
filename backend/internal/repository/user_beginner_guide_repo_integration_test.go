//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type observedBeginnerGuideRepository struct {
	service.UserRepository
	guide           service.BeginnerGuideRepository
	attempts        atomic.Int32
	secondAttempted chan struct{}
}

func (r *observedBeginnerGuideRepository) GetBeginnerGuideState(ctx context.Context, userID int64) (*service.BeginnerGuideState, error) {
	return r.guide.GetBeginnerGuideState(ctx, userID)
}

func (r *observedBeginnerGuideRepository) WithBeginnerGuideStateForUpdate(
	ctx context.Context,
	userID int64,
	update func(service.BeginnerGuideState) (service.BeginnerGuideState, error),
) (*service.BeginnerGuideState, error) {
	if r.attempts.Add(1) == 2 {
		close(r.secondAttempted)
	}
	return r.guide.WithBeginnerGuideStateForUpdate(ctx, userID, update)
}

func storeBeginnerGuideState(
	ctx context.Context,
	repo service.BeginnerGuideRepository,
	userID int64,
	state service.BeginnerGuideState,
) (*service.BeginnerGuideState, error) {
	return repo.WithBeginnerGuideStateForUpdate(
		ctx,
		userID,
		func(service.BeginnerGuideState) (service.BeginnerGuideState, error) {
			return state, nil
		},
	)
}

func TestUserRepositoryBeginnerGuideStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepositoryWithSQL(integrationEntClient, integrationDB)
	var _ service.BeginnerGuideRepository = repo

	userA := mustCreateUser(t, integrationEntClient, &service.User{
		Email: uniqueTestValue(t, "beginner-guide-a") + "@example.com",
	})
	userB := mustCreateUser(t, integrationEntClient, &service.User{
		Email: uniqueTestValue(t, "beginner-guide-b") + "@example.com",
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id IN ($1, $2)", userA.ID, userB.ID)
	})

	completedAt := time.Date(2026, time.July, 15, 1, 2, 3, 456000000, time.UTC)
	stateA := service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptStateCompleted,
		Progress: &service.BeginnerGuideProgress{
			Version:        service.BeginnerGuideProgressVersion,
			Client:         "codex",
			OS:             "windows",
			CurrentStep:    "first_run",
			CompletedSteps: []string{"understand", "choose", "terminal", "install", "api_key", "configure"},
		},
		CompletedAt: &completedAt,
	}
	stateB := service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptStateSuppressed,
		Progress: &service.BeginnerGuideProgress{
			Version:        service.BeginnerGuideProgressVersion,
			Client:         "claude_code",
			OS:             "linux",
			CurrentStep:    "terminal",
			CompletedSteps: []string{"understand", "choose"},
		},
	}

	_, err := storeBeginnerGuideState(ctx, repo, userB.ID, stateB)
	require.NoError(t, err)
	storedA, err := storeBeginnerGuideState(ctx, repo, userA.ID, stateA)
	require.NoError(t, err)
	require.Equal(t, stateA.PromptState, storedA.PromptState)
	require.Equal(t, stateA.Progress, storedA.Progress)
	require.NotNil(t, storedA.CompletedAt)
	require.Equal(t, completedAt, *storedA.CompletedAt)

	gotA, err := repo.GetBeginnerGuideState(ctx, userA.ID)
	require.NoError(t, err)
	require.Equal(t, service.BeginnerGuidePromptStateCompleted, gotA.PromptState)
	require.Equal(t, stateA.Progress, gotA.Progress)
	require.NotEqual(t, stateB.Progress, gotA.Progress, "reading user A must never return user B's progress")
	require.NotNil(t, gotA.CompletedAt)
	require.Equal(t, completedAt, *gotA.CompletedAt)

	gotB, err := repo.GetBeginnerGuideState(ctx, userB.ID)
	require.NoError(t, err)
	require.Equal(t, stateB.PromptState, gotB.PromptState)
	require.Equal(t, stateB.Progress, gotB.Progress)
	require.Nil(t, gotB.CompletedAt)

	progressJSON, err := json.Marshal(gotA.Progress)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"version": 1,
		"client": "codex",
		"os": "windows",
		"currentStep": "first_run",
		"completedSteps": ["understand", "choose", "terminal", "install", "api_key", "configure"]
	}`, string(progressJSON))
	require.Contains(t, string(progressJSON), `"currentStep"`)
	require.Contains(t, string(progressJSON), `"completedSteps"`)
	require.NotContains(t, string(progressJSON), "current_step")
	require.NotContains(t, string(progressJSON), "completed_steps")

	cleared, err := storeBeginnerGuideState(ctx, repo, userA.ID, service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptStateCompleted,
		CompletedAt: &completedAt,
	})
	require.NoError(t, err)
	require.Nil(t, cleared.Progress)

	var progressIsNull bool
	err = integrationDB.QueryRowContext(
		ctx,
		"SELECT beginner_guide_progress IS NULL FROM users WHERE id = $1",
		userA.ID,
	).Scan(&progressIsNull)
	require.NoError(t, err)
	require.True(t, progressIsNull, "clearing guide progress must persist SQL NULL")
}

func TestUserRepositoryBeginnerGuideStateTranslatesMissingUser(t *testing.T) {
	repo := newUserRepositoryWithSQL(integrationEntClient, integrationDB)
	const missingUserID int64 = 9_000_000_000

	_, getErr := repo.GetBeginnerGuideState(context.Background(), missingUserID)
	_, updateErr := storeBeginnerGuideState(context.Background(), repo, missingUserID, service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptStateSuppressed,
	})

	require.ErrorIs(t, getErr, service.ErrUserNotFound)
	require.ErrorIs(t, updateErr, service.ErrUserNotFound)
}

func TestUserRepositoryBeginnerGuideStateSerializesConcurrentPatches(t *testing.T) {
	tests := []struct {
		name         string
		secondPrompt service.BeginnerGuidePromptState
	}{
		{
			name:         "completed_never_downgrades_and_omitted_progress_stays_current",
			secondPrompt: service.BeginnerGuidePromptStateSuppressed,
		},
		{
			name:         "repeated_completion_preserves_first_timestamp",
			secondPrompt: service.BeginnerGuidePromptStateCompleted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newUserRepositoryWithSQL(integrationEntClient, integrationDB)
			observedRepo := &observedBeginnerGuideRepository{
				UserRepository:  repo,
				guide:           repo,
				secondAttempted: make(chan struct{}),
			}
			userService := service.NewUserService(observedRepo, nil, nil, nil)
			user := mustCreateUser(t, integrationEntClient, &service.User{
				Email: uniqueTestValue(t, "beginner-guide-lock") + "@example.com",
			})
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
			})

			latestProgress := &service.BeginnerGuideProgress{
				Version:        service.BeginnerGuideProgressVersion,
				Client:         "codex",
				OS:             "linux",
				CurrentStep:    "first_run",
				CompletedSteps: []string{"understand", "choose", "terminal", "install", "api_key", "configure"},
			}

			tx, err := integrationEntClient.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			txCtx := dbent.NewTxContext(ctx, tx)

			first, err := userService.PatchBeginnerGuideState(txCtx, user.ID, service.PatchBeginnerGuideStateRequest{
				PromptState: beginnerGuidePromptStatePtr(service.BeginnerGuidePromptStateCompleted),
				Progress:    latestProgress,
			})
			require.NoError(t, err)
			require.NotNil(t, first.CompletedAt)

			type patchResult struct {
				state *service.BeginnerGuideState
				err   error
			}
			result := make(chan patchResult, 1)
			go func() {
				state, patchErr := userService.PatchBeginnerGuideState(ctx, user.ID, service.PatchBeginnerGuideStateRequest{
					PromptState: beginnerGuidePromptStatePtr(test.secondPrompt),
				})
				result <- patchResult{state: state, err: patchErr}
			}()

			select {
			case <-observedRepo.secondAttempted:
			case <-time.After(3 * time.Second):
				t.Fatal("second patch did not reach the transactional repository")
			}

			select {
			case early := <-result:
				t.Fatalf("second patch completed before the first transaction released its row lock: %v", early.err)
			case <-time.After(150 * time.Millisecond):
			}

			require.NoError(t, tx.Commit())

			var second patchResult
			select {
			case second = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("second patch remained blocked after the first transaction committed")
			}
			require.NoError(t, second.err)
			require.Equal(t, service.BeginnerGuidePromptStateCompleted, second.state.PromptState)
			require.Equal(t, first.CompletedAt, second.state.CompletedAt)
			require.Equal(t, latestProgress, second.state.Progress)

			stored, err := repo.GetBeginnerGuideState(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, second.state, stored)
		})
	}
}

func beginnerGuidePromptStatePtr(state service.BeginnerGuidePromptState) *service.BeginnerGuidePromptState {
	return &state
}

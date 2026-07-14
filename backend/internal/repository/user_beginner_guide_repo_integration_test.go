//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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

	_, err := repo.UpdateBeginnerGuideState(ctx, userB.ID, stateB)
	require.NoError(t, err)
	storedA, err := repo.UpdateBeginnerGuideState(ctx, userA.ID, stateA)
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

	cleared, err := repo.UpdateBeginnerGuideState(ctx, userA.ID, service.BeginnerGuideState{
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
	_, updateErr := repo.UpdateBeginnerGuideState(context.Background(), missingUserID, service.BeginnerGuideState{
		PromptState: service.BeginnerGuidePromptStateSuppressed,
	})

	require.ErrorIs(t, getErr, service.ErrUserNotFound)
	require.ErrorIs(t, updateErr, service.ErrUserNotFound)
}

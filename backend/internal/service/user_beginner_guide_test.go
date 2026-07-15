//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type beginnerGuideRepositoryStub struct {
	UserRepository
	state       *BeginnerGuideState
	getErr      error
	updateErr   error
	getCalls    int
	updateCalls int
	updates     []BeginnerGuideState
}

var _ BeginnerGuideRepository = (*beginnerGuideRepositoryStub)(nil)

func (r *beginnerGuideRepositoryStub) GetBeginnerGuideState(_ context.Context, _ int64) (*BeginnerGuideState, error) {
	r.getCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.state == nil {
		return nil, nil
	}
	state := *r.state
	return &state, nil
}

func (r *beginnerGuideRepositoryStub) WithBeginnerGuideStateForUpdate(
	_ context.Context,
	_ int64,
	update func(BeginnerGuideState) (BeginnerGuideState, error),
) (*BeginnerGuideState, error) {
	r.updateCalls++
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	if r.state == nil {
		return nil, nil
	}
	stored, err := update(*r.state)
	if err != nil {
		return nil, err
	}
	r.updates = append(r.updates, stored)
	r.state = &stored
	return &stored, nil
}

type userRepositoryWithoutBeginnerGuide struct {
	UserRepository
}

func beginnerGuideStatePtr(value BeginnerGuidePromptState) *BeginnerGuidePromptState {
	return &value
}

func validBeginnerGuideProgress() *BeginnerGuideProgress {
	return &BeginnerGuideProgress{
		Version:        BeginnerGuideProgressVersion,
		Client:         "claude_code",
		OS:             "macos",
		CurrentStep:    "configure",
		CompletedSteps: []string{"understand", "choose", "terminal", "install", "api_key"},
	}
}

func TestValidateBeginnerGuideProgressAcceptsSupportedClientsAndOperatingSystems(t *testing.T) {
	for _, client := range []string{"claude_code", "codex"} {
		for _, operatingSystem := range []string{"macos", "windows", "linux"} {
			t.Run(client+"_"+operatingSystem, func(t *testing.T) {
				progress := validBeginnerGuideProgress()
				progress.Client = client
				progress.OS = operatingSystem
				progress.CurrentStep = "troubleshoot"
				progress.CompletedSteps = append([]string(nil), beginnerGuideStepOrder...)

				require.NoError(t, validateBeginnerGuideProgress(progress))
			})
		}
	}
}

func TestValidateBeginnerGuideProgressRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BeginnerGuideProgress)
	}{
		{
			name: "version",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.Version = BeginnerGuideProgressVersion + 1
			},
		},
		{
			name: "client",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.Client = "cursor"
			},
		},
		{
			name: "os",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.OS = "freebsd"
			},
		},
		{
			name: "current_step",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.CurrentStep = "authenticate"
			},
		},
		{
			name: "duplicate_completed_step",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.CompletedSteps = []string{"understand", "understand"}
			},
		},
		{
			name: "unknown_completed_step",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.CompletedSteps = []string{"understand", "authenticate"}
			},
		},
		{
			name: "more_than_curriculum_size",
			mutate: func(progress *BeginnerGuideProgress) {
				progress.CompletedSteps = append(append([]string(nil), beginnerGuideStepOrder...), "understand")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			progress := validBeginnerGuideProgress()
			test.mutate(progress)

			require.ErrorIs(t, validateBeginnerGuideProgress(progress), ErrBeginnerGuideProgressInvalid)
		})
	}
}

func TestPatchBeginnerGuideStateTransitions(t *testing.T) {
	tests := []struct {
		name          string
		current       BeginnerGuidePromptState
		requested     BeginnerGuidePromptState
		want          BeginnerGuidePromptState
		wantCompleted bool
	}{
		{
			name:      "eligible_to_suppressed",
			current:   BeginnerGuidePromptStateEligible,
			requested: BeginnerGuidePromptStateSuppressed,
			want:      BeginnerGuidePromptStateSuppressed,
		},
		{
			name:          "eligible_to_completed",
			current:       BeginnerGuidePromptStateEligible,
			requested:     BeginnerGuidePromptStateCompleted,
			want:          BeginnerGuidePromptStateCompleted,
			wantCompleted: true,
		},
		{
			name:          "suppressed_to_completed",
			current:       BeginnerGuidePromptStateSuppressed,
			requested:     BeginnerGuidePromptStateCompleted,
			want:          BeginnerGuidePromptStateCompleted,
			wantCompleted: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &beginnerGuideRepositoryStub{
				state: &BeginnerGuideState{PromptState: test.current},
			}
			service := NewUserService(repo, nil, nil, nil)
			startedAt := time.Now().UTC()

			got, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
				PromptState: beginnerGuideStatePtr(test.requested),
			})

			require.NoError(t, err)
			require.Equal(t, test.want, got.PromptState)
			require.Equal(t, 0, repo.getCalls)
			require.Equal(t, 1, repo.updateCalls)
			if test.wantCompleted {
				require.NotNil(t, got.CompletedAt)
				require.False(t, got.CompletedAt.Before(startedAt))
				require.False(t, got.CompletedAt.After(time.Now().UTC()))
				require.Equal(t, time.UTC, got.CompletedAt.Location())
			} else {
				require.Nil(t, got.CompletedAt)
			}
		})
	}
}

func TestPatchBeginnerGuideStateRejectsEligibleAndUnknownPromptStates(t *testing.T) {
	tests := []struct {
		name      string
		requested BeginnerGuidePromptState
	}{
		{name: "restore_eligible", requested: BeginnerGuidePromptStateEligible},
		{name: "unknown", requested: BeginnerGuidePromptState("dismissed")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &beginnerGuideRepositoryStub{
				state: &BeginnerGuideState{PromptState: BeginnerGuidePromptStateSuppressed},
			}
			service := NewUserService(repo, nil, nil, nil)

			_, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
				PromptState: beginnerGuideStatePtr(test.requested),
			})

			require.ErrorIs(t, err, ErrBeginnerGuidePromptStateInvalid)
			require.Equal(t, 0, repo.updateCalls)
		})
	}
}

func TestPatchBeginnerGuideStateValidatesProgressBeforePersistence(t *testing.T) {
	repo := &beginnerGuideRepositoryStub{
		state: &BeginnerGuideState{PromptState: BeginnerGuidePromptStateEligible},
	}
	service := NewUserService(repo, nil, nil, nil)
	progress := validBeginnerGuideProgress()
	progress.Client = "cursor"

	_, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
		Progress: progress,
	})

	require.ErrorIs(t, err, ErrBeginnerGuideProgressInvalid)
	require.Equal(t, 0, repo.getCalls)
	require.Equal(t, 0, repo.updateCalls)
}

func TestPatchBeginnerGuideStatePersistsValidProgress(t *testing.T) {
	repo := &beginnerGuideRepositoryStub{
		state: &BeginnerGuideState{PromptState: BeginnerGuidePromptStateEligible},
	}
	service := NewUserService(repo, nil, nil, nil)
	progress := validBeginnerGuideProgress()

	got, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
		Progress: progress,
	})

	require.NoError(t, err)
	require.Equal(t, progress, got.Progress)
	require.Equal(t, progress, repo.updates[0].Progress)
}

func TestPatchBeginnerGuideStatePreservesOriginalCompletionTimestamp(t *testing.T) {
	original := time.Date(2026, time.July, 15, 1, 2, 3, 456000000, time.UTC)
	repo := &beginnerGuideRepositoryStub{
		state: &BeginnerGuideState{
			PromptState: BeginnerGuidePromptStateCompleted,
			CompletedAt: &original,
		},
	}
	service := NewUserService(repo, nil, nil, nil)

	got, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
		PromptState: beginnerGuideStatePtr(BeginnerGuidePromptStateCompleted),
	})

	require.NoError(t, err)
	require.Equal(t, BeginnerGuidePromptStateCompleted, got.PromptState)
	require.Equal(t, original, *got.CompletedAt)
	require.Equal(t, original, *repo.updates[0].CompletedAt)
}

func TestPatchBeginnerGuideStateKeepsCompletedMonotonic(t *testing.T) {
	original := time.Date(2026, time.July, 15, 1, 2, 3, 456000000, time.UTC)
	repo := &beginnerGuideRepositoryStub{
		state: &BeginnerGuideState{
			PromptState: BeginnerGuidePromptStateCompleted,
			CompletedAt: &original,
		},
	}
	service := NewUserService(repo, nil, nil, nil)

	got, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
		PromptState: beginnerGuideStatePtr(BeginnerGuidePromptStateSuppressed),
	})

	require.NoError(t, err)
	require.Equal(t, BeginnerGuidePromptStateCompleted, got.PromptState)
	require.Equal(t, original, *got.CompletedAt)
}

func TestPatchBeginnerGuideStateLeavesProgressUnchangedWhenOmitted(t *testing.T) {
	progress := validBeginnerGuideProgress()
	repo := &beginnerGuideRepositoryStub{
		state: &BeginnerGuideState{
			PromptState: BeginnerGuidePromptStateEligible,
			Progress:    progress,
		},
	}
	service := NewUserService(repo, nil, nil, nil)

	got, err := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{
		PromptState: beginnerGuideStatePtr(BeginnerGuidePromptStateSuppressed),
	})

	require.NoError(t, err)
	require.Equal(t, progress, got.Progress)
	require.Equal(t, progress, repo.updates[0].Progress)
}

func TestBeginnerGuideStateUnavailableWithoutNarrowRepository(t *testing.T) {
	service := NewUserService(&userRepositoryWithoutBeginnerGuide{}, nil, nil, nil)

	_, getErr := service.GetBeginnerGuideState(context.Background(), 42)
	_, patchErr := service.PatchBeginnerGuideState(context.Background(), 42, PatchBeginnerGuideStateRequest{})

	require.ErrorIs(t, getErr, ErrBeginnerGuideUnavailable)
	require.ErrorIs(t, patchErr, ErrBeginnerGuideUnavailable)
}

package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const BeginnerGuideProgressVersion = 1

const (
	BeginnerGuidePromptStateEligible   BeginnerGuidePromptState = "eligible"
	BeginnerGuidePromptStateSuppressed BeginnerGuidePromptState = "suppressed"
	BeginnerGuidePromptStateCompleted  BeginnerGuidePromptState = "completed"
)

var (
	ErrBeginnerGuideProgressInvalid = infraerrors.BadRequest(
		"BEGINNER_GUIDE_PROGRESS_INVALID",
		"beginner guide progress is invalid",
	)
	ErrBeginnerGuidePromptStateInvalid = infraerrors.BadRequest(
		"BEGINNER_GUIDE_PROMPT_STATE_INVALID",
		"beginner guide prompt state is invalid",
	)
	ErrBeginnerGuideUnavailable = infraerrors.ServiceUnavailable(
		"BEGINNER_GUIDE_UNAVAILABLE",
		"beginner guide state is unavailable",
	)
)

var beginnerGuideStepOrder = []string{
	"understand",
	"choose",
	"terminal",
	"install",
	"api_key",
	"configure",
	"first_run",
	"troubleshoot",
}

type BeginnerGuidePromptState string

type BeginnerGuideProgress struct {
	Version        int      `json:"version"`
	Client         string   `json:"client"`
	OS             string   `json:"os"`
	CurrentStep    string   `json:"currentStep"`
	CompletedSteps []string `json:"completedSteps"`
}

type BeginnerGuideState struct {
	PromptState BeginnerGuidePromptState `json:"prompt_state"`
	Progress    *BeginnerGuideProgress   `json:"progress"`
	CompletedAt *time.Time               `json:"completed_at"`
}

type BeginnerGuideRepository interface {
	GetBeginnerGuideState(context.Context, int64) (*BeginnerGuideState, error)
	WithBeginnerGuideStateForUpdate(
		context.Context,
		int64,
		func(BeginnerGuideState) (BeginnerGuideState, error),
	) (*BeginnerGuideState, error)
}

type PatchBeginnerGuideStateRequest struct {
	PromptState *BeginnerGuidePromptState
	Progress    *BeginnerGuideProgress
}

func validateBeginnerGuideProgress(progress *BeginnerGuideProgress) error {
	if progress == nil {
		return nil
	}
	if progress.Version != BeginnerGuideProgressVersion {
		return ErrBeginnerGuideProgressInvalid
	}
	if progress.Client != "claude_code" &&
		progress.Client != "codex" &&
		progress.Client != "opencode" &&
		progress.Client != "cc_switch" {
		return ErrBeginnerGuideProgressInvalid
	}
	if progress.OS != "macos" && progress.OS != "windows" && progress.OS != "linux" {
		return ErrBeginnerGuideProgressInvalid
	}
	return validateBeginnerGuideSteps(progress.CurrentStep, progress.CompletedSteps)
}

func validateBeginnerGuideSteps(currentStep string, completedSteps []string) error {
	if len(completedSteps) > len(beginnerGuideStepOrder) {
		return ErrBeginnerGuideProgressInvalid
	}

	knownSteps := make(map[string]struct{}, len(beginnerGuideStepOrder))
	for _, step := range beginnerGuideStepOrder {
		knownSteps[step] = struct{}{}
	}
	if _, ok := knownSteps[currentStep]; !ok {
		return ErrBeginnerGuideProgressInvalid
	}

	seen := make(map[string]struct{}, len(completedSteps))
	for _, step := range completedSteps {
		if _, ok := knownSteps[step]; !ok {
			return ErrBeginnerGuideProgressInvalid
		}
		if _, ok := seen[step]; ok {
			return ErrBeginnerGuideProgressInvalid
		}
		seen[step] = struct{}{}
	}

	return nil
}

func (s *UserService) GetBeginnerGuideState(ctx context.Context, userID int64) (*BeginnerGuideState, error) {
	if s == nil || s.beginnerGuideRepo == nil {
		return nil, ErrBeginnerGuideUnavailable
	}
	return s.beginnerGuideRepo.GetBeginnerGuideState(ctx, userID)
}

func (s *UserService) PatchBeginnerGuideState(ctx context.Context, userID int64, request PatchBeginnerGuideStateRequest) (*BeginnerGuideState, error) {
	if s == nil || s.beginnerGuideRepo == nil {
		return nil, ErrBeginnerGuideUnavailable
	}
	if err := validateBeginnerGuideProgress(request.Progress); err != nil {
		return nil, err
	}
	if request.PromptState != nil &&
		*request.PromptState != BeginnerGuidePromptStateSuppressed &&
		*request.PromptState != BeginnerGuidePromptStateCompleted {
		return nil, ErrBeginnerGuidePromptStateInvalid
	}

	updated, err := s.beginnerGuideRepo.WithBeginnerGuideStateForUpdate(
		ctx,
		userID,
		func(current BeginnerGuideState) (BeginnerGuideState, error) {
			next := current
			if request.Progress != nil {
				next.Progress = request.Progress
			}
			if request.PromptState != nil {
				switch *request.PromptState {
				case BeginnerGuidePromptStateSuppressed:
					if current.PromptState != BeginnerGuidePromptStateCompleted {
						next.PromptState = BeginnerGuidePromptStateSuppressed
					}
				case BeginnerGuidePromptStateCompleted:
					next.PromptState = BeginnerGuidePromptStateCompleted
				}
			}

			if next.PromptState == BeginnerGuidePromptStateCompleted && next.CompletedAt == nil {
				completedAt := time.Now().UTC()
				next.CompletedAt = &completedAt
			}

			return next, nil
		},
	)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrBeginnerGuideUnavailable
	}
	return updated, nil
}

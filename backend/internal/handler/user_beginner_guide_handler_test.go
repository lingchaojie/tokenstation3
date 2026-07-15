//go:build unit

package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testBeginnerGuideRequestLimitBytes = 8 * 1024

func TestBeginnerGuideHandlersRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		method string
		body   []byte
		call   func(*UserHandler, *gin.Context)
	}{
		{
			name:   "get",
			method: http.MethodGet,
			call:   (*UserHandler).GetBeginnerGuide,
		},
		{
			name:   "patch",
			method: http.MethodPatch,
			body:   []byte(`{"prompt_state":"suppressed"}`),
			call:   (*UserHandler).PatchBeginnerGuide,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &userHandlerRepoStub{
				beginnerGuideState: service.BeginnerGuideState{
					PromptState: service.BeginnerGuidePromptStateEligible,
				},
			}
			handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(tt.method, "/api/v1/user/beginner-guide", bytes.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			tt.call(handler, c)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}
}

func TestGetBeginnerGuideUsesAuthenticatedUserAndReturnsPublicState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	completedAt := time.Date(2026, 7, 15, 8, 30, 0, 0, time.UTC)
	repo := &userHandlerRepoStub{
		beginnerGuideState: service.BeginnerGuideState{
			PromptState: service.BeginnerGuidePromptStateCompleted,
			Progress: &service.BeginnerGuideProgress{
				Version:        service.BeginnerGuideProgressVersion,
				Client:         "codex",
				OS:             "linux",
				CurrentStep:    "first_run",
				CompletedSteps: []string{"understand", "choose"},
			},
			CompletedAt: &completedAt,
		},
	}
	handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/beginner-guide", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	handler.GetBeginnerGuide(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), repo.beginnerGuideGetUserID)

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Data, &fields))
	require.Len(t, fields, 3)
	require.Contains(t, fields, "prompt_state")
	require.Contains(t, fields, "progress")
	require.Contains(t, fields, "completed_at")

	var state service.BeginnerGuideState
	require.NoError(t, json.Unmarshal(envelope.Data, &state))
	require.Equal(t, repo.beginnerGuideState, state)
}

func TestPatchBeginnerGuideUsesAuthenticatedUserAndReturnsUpdatedState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &userHandlerRepoStub{
		beginnerGuideState: service.BeginnerGuideState{
			PromptState: service.BeginnerGuidePromptStateEligible,
		},
	}
	handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)
	body := []byte(`{
		"prompt_state":"suppressed",
		"progress":{
			"version":1,
			"client":"claude_code",
			"os":"macos",
			"currentStep":"configure",
			"completedSteps":["understand","choose"]
		}
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/user/beginner-guide", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 73})

	handler.PatchBeginnerGuide(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(73), repo.beginnerGuideUpdateUserID)
	require.Equal(t, service.BeginnerGuidePromptStateSuppressed, repo.beginnerGuideState.PromptState)
	require.NotNil(t, repo.beginnerGuideState.Progress)
	require.Equal(t, "claude_code", repo.beginnerGuideState.Progress.Client)

	var envelope struct {
		Data service.BeginnerGuideState `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, repo.beginnerGuideState, envelope.Data)
}

func TestPatchBeginnerGuideRejectsInvalidAndUnknownJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validProgressPrefix := `{"progress":{"version":1,"client":"codex","os":"windows","currentStep":"terminal","completedSteps":[]`
	tests := []struct {
		name string
		body []byte
	}{
		{name: "eligible prompt state", body: []byte(`{"prompt_state":"eligible"}`)},
		{name: "unknown top-level field", body: []byte(`{"prompt_state":"suppressed","unexpected":true}`)},
		{name: "top-level api key", body: []byte(`{"prompt_state":"suppressed","api_key":"secret"}`)},
		{name: "user id", body: []byte(`{"prompt_state":"suppressed","user_id":99}`)},
		{name: "completion timestamp", body: []byte(`{"prompt_state":"completed","completed_at":"2026-07-15T08:30:00Z"}`)},
		{name: "unknown nested progress field", body: []byte(validProgressPrefix + `,"unexpected":true}}`)},
		{name: "nested api key", body: []byte(validProgressPrefix + `,"api_key":"secret"}}`)},
		{name: "malformed json", body: []byte(`{"prompt_state":`)},
		{name: "empty body", body: nil},
		{name: "multiple json values", body: []byte(`{"prompt_state":"suppressed"}{"prompt_state":"completed"}`)},
		{name: "larger than 8 KiB", body: beginnerGuideRequestBodyOfSize(testBeginnerGuideRequestLimitBytes + 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &userHandlerRepoStub{
				beginnerGuideState: service.BeginnerGuideState{
					PromptState: service.BeginnerGuidePromptStateEligible,
				},
			}
			handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/user/beginner-guide", bytes.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})

			handler.PatchBeginnerGuide(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestPatchBeginnerGuideAcceptsBodyAt8KiBBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &userHandlerRepoStub{
		beginnerGuideState: service.BeginnerGuideState{
			PromptState: service.BeginnerGuidePromptStateEligible,
		},
	}
	handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/user/beginner-guide",
		bytes.NewReader(beginnerGuideRequestBodyOfSize(testBeginnerGuideRequestLimitBytes)),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 92})

	handler.PatchBeginnerGuide(c)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestPatchBeginnerGuideRepeatedCompletionPreservesTimestamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &userHandlerRepoStub{
		beginnerGuideState: service.BeginnerGuideState{
			PromptState: service.BeginnerGuidePromptStateEligible,
		},
	}
	handler := NewUserHandler(service.NewUserService(repo, nil, nil, nil), nil, nil, nil, nil, nil)

	patchCompleted := func() service.BeginnerGuideState {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(
			http.MethodPatch,
			"/api/v1/user/beginner-guide",
			strings.NewReader(`{"prompt_state":"completed"}`),
		)
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 108})

		handler.PatchBeginnerGuide(c)

		require.Equal(t, http.StatusOK, recorder.Code)
		var envelope struct {
			Data service.BeginnerGuideState `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
		return envelope.Data
	}

	first := patchCompleted()
	second := patchCompleted()

	require.NotNil(t, first.CompletedAt)
	require.NotNil(t, second.CompletedAt)
	require.Equal(t, *first.CompletedAt, *second.CompletedAt)
}

func beginnerGuideRequestBodyOfSize(size int) []byte {
	const prefix = `{"prompt_state":"suppressed"`
	const suffix = `}`
	return []byte(prefix + strings.Repeat(" ", size-len(prefix)-len(suffix)) + suffix)
}

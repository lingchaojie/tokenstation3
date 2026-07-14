package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type checkInUseCaseFake struct {
	status       service.DailyCheckInStatus
	statusErr    error
	claim        service.DailyCheckInClaimResult
	claimErr     error
	statusUserID int64
	claimUserID  int64
	claimRole    string
}

func (f *checkInUseCaseFake) GetStatus(_ context.Context, userID int64) (service.DailyCheckInStatus, error) {
	f.statusUserID = userID
	return f.status, f.statusErr
}

func (f *checkInUseCaseFake) Claim(_ context.Context, userID int64, role string) (service.DailyCheckInClaimResult, error) {
	f.claimUserID = userID
	f.claimRole = role
	return f.claim, f.claimErr
}

func newCheckInHandlerContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, recorder
}

func TestCheckInHandler_GetStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &checkInUseCaseFake{status: service.DailyCheckInStatus{
		State: service.DailyCheckInStateActive, RewardAmount: 10, CheckInDate: "2026-07-21",
	}}
	h := &CheckInHandler{service: fake}
	c, recorder := newCheckInHandlerContext(http.MethodGet, "/api/v1/user/check-in/status")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	c.Set(string(middleware2.ContextKeyUserRole), service.RoleUser)

	h.GetStatus(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), fake.statusUserID)
	var response struct {
		Data dto.DailyCheckInStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Data.Active)
	require.InDelta(t, 10, response.Data.RewardAmount, 1e-9)
}

func TestCheckInHandler_ClaimPassesAuthenticatedRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &checkInUseCaseFake{claim: service.DailyCheckInClaimResult{
		RewardAmount: 10, BalanceAfter: 15, CheckInDate: "2026-07-21", ClaimedAt: time.Now(),
	}}
	h := &CheckInHandler{service: fake}
	c, recorder := newCheckInHandlerContext(http.MethodPost, "/api/v1/user/check-in/claim")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	c.Set(string(middleware2.ContextKeyUserRole), service.RoleUser)

	h.Claim(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), fake.claimUserID)
	require.Equal(t, service.RoleUser, fake.claimRole)
}

func TestCheckInHandler_ClaimRejectsMissingRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &checkInUseCaseFake{}
	h := &CheckInHandler{service: fake}
	c, recorder := newCheckInHandlerContext(http.MethodPost, "/api/v1/user/check-in/claim")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

	h.Claim(c)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, fake.claimUserID)
}

func TestCheckInHandler_ClaimMapsDuplicate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &checkInUseCaseFake{claimErr: service.ErrDailyCheckInAlreadyClaimed}
	h := &CheckInHandler{service: fake}
	c, recorder := newCheckInHandlerContext(http.MethodPost, "/api/v1/user/check-in/claim")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	c.Set(string(middleware2.ContextKeyUserRole), service.RoleUser)

	h.Claim(c)

	require.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "DAILY_CHECK_IN_ALREADY_CLAIMED", response.Reason)
}

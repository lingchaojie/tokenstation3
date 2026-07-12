package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- mock repo ---

type userBreakdownRepoCapture struct {
	service.UsageLogRepository
	capturedDim   usagestats.UserBreakdownDimension
	capturedLimit int
	result        []usagestats.UserBreakdownItem
}

func (r *userBreakdownRepoCapture) GetUserBreakdownStats(
	_ context.Context, _, _ time.Time,
	dim usagestats.UserBreakdownDimension, limit int,
) ([]usagestats.UserBreakdownItem, error) {
	r.capturedDim = dim
	r.capturedLimit = limit
	if r.result != nil {
		return r.result, nil
	}
	return []usagestats.UserBreakdownItem{}, nil
}

func (r *userBreakdownRepoCapture) GetModelStatsWithFilters(
	_ context.Context, _, _ time.Time,
	_, _, _, _ int64, _ *int16, _ *bool, _ *int8,
) ([]usagestats.ModelStat, error) {
	return []usagestats.ModelStat{}, nil
}

func (r *userBreakdownRepoCapture) GetModelStatsWithUsageFiltersBySource(
	_ context.Context, _, _ time.Time,
	_ usagestats.UsageLogFilters, _ string,
) ([]usagestats.ModelStat, error) {
	return []usagestats.ModelStat{}, nil
}

func newUserBreakdownRouter(repo *userBreakdownRepoCapture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewDashboardService(repo, nil, nil, nil)
	h := NewDashboardHandler(svc, nil)
	router := gin.New()
	router.GET("/admin/dashboard/user-breakdown", h.GetUserBreakdown)
	return router
}

func TestExcludedUserIDs_RejectsMalformedQueries(t *testing.T) {
	usageRouter := newAdminUsageRequestTypeTestRouter(&adminUsageRepoCapture{})
	dashboardRouter := newUserBreakdownRouter(&userBreakdownRepoCapture{})
	dashboardHandler := NewDashboardHandler(service.NewDashboardService(&userBreakdownRepoCapture{}, nil, nil, nil), nil)
	dashboardRouter.GET("/admin/dashboard/models", dashboardHandler.GetModelStats)
	dashboardRouter.GET("/admin/dashboard/snapshot-v2", dashboardHandler.GetSnapshotV2)
	opsRouter := gin.New()
	opsRouter.GET("/admin/ops/errors", NewOpsHandler(nil).GetErrorLogs)

	tests := []struct {
		name   string
		router http.Handler
		path   string
	}{
		{name: "usage list", router: usageRouter, path: "/admin/usage?exclude_user_ids=bad"},
		{name: "usage stats", router: usageRouter, path: "/admin/usage/stats?exclude_user_ids=bad"},
		{name: "dashboard model", router: dashboardRouter, path: "/admin/dashboard/models?exclude_user_ids=bad"},
		{name: "dashboard snapshot", router: dashboardRouter, path: "/admin/dashboard/snapshot-v2?exclude_user_ids=bad&include_stats=false&include_trend=false&include_model_stats=false"},
		{name: "dashboard ranking", router: dashboardRouter, path: "/admin/dashboard/user-breakdown?exclude_user_ids=bad"},
		{name: "ops errors", router: opsRouter, path: "/admin/ops/errors?exclude_user_ids=bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// --- tests ---

func TestGetUserBreakdown_GroupIDFilter(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&group_id=42", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(42), repo.capturedDim.GroupID)
	require.Empty(t, repo.capturedDim.Model)
	require.Empty(t, repo.capturedDim.Endpoint)
	require.Equal(t, 50, repo.capturedLimit)  // default limit
	require.Empty(t, repo.capturedDim.SortBy) // no sort_by => empty (repo falls back to default)
}

func TestGetUserBreakdown_ExcludedUserIDs(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?exclude_user_ids=9%2C3%2C9", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []int64{3, 9}, repo.capturedDim.ExcludedUserIDs)
}

func TestGetUserBreakdown_SortBy(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&sort_by=total_tokens", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "total_tokens", repo.capturedDim.SortBy)
}

func TestGetUserBreakdown_ModelFilter(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&model=claude-opus-4-6", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "claude-opus-4-6", repo.capturedDim.Model)
	require.Equal(t, usagestats.ModelSourceRequested, repo.capturedDim.ModelType)
	require.Equal(t, int64(0), repo.capturedDim.GroupID)
}

func TestGetUserBreakdown_ModelSourceFilter(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&model=claude-opus-4-6&model_source=upstream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, usagestats.ModelSourceUpstream, repo.capturedDim.ModelType)
}

func TestGetUserBreakdown_InvalidModelSource(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&model_source=foobar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetUserBreakdown_EndpointFilter(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&endpoint=/v1/messages&endpoint_type=upstream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "/v1/messages", repo.capturedDim.Endpoint)
	require.Equal(t, "upstream", repo.capturedDim.EndpointType)
}

func TestGetUserBreakdown_DefaultEndpointType(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&endpoint=/chat", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "inbound", repo.capturedDim.EndpointType)
}

func TestGetUserBreakdown_CustomLimit(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&model=test&limit=100", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 100, repo.capturedLimit)
}

func TestGetUserBreakdown_LimitClamped(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	// limit > 200 should fall back to default 50
	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&model=test&limit=999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 50, repo.capturedLimit)
}

func TestGetUserBreakdown_ResponseFormat(t *testing.T) {
	repo := &userBreakdownRepoCapture{
		result: []usagestats.UserBreakdownItem{
			{UserID: 1, Email: "alice@test.com", Requests: 100, TotalTokens: 50000, Cost: 1.5, ActualCost: 1.2},
			{UserID: 2, Email: "bob@test.com", Requests: 50, TotalTokens: 25000, Cost: 0.8, ActualCost: 0.6},
		},
	}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&group_id=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Users     []usagestats.UserBreakdownItem `json:"users"`
			StartDate string                         `json:"start_date"`
			EndDate   string                         `json:"end_date"`
		} `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Users, 2)
	require.Equal(t, int64(1), resp.Data.Users[0].UserID)
	require.Equal(t, "alice@test.com", resp.Data.Users[0].Email)
	require.Equal(t, int64(100), resp.Data.Users[0].Requests)
	require.InDelta(t, 1.2, resp.Data.Users[0].ActualCost, 0.001)
	require.Equal(t, "2026-03-01", resp.Data.StartDate)
	require.Equal(t, "2026-03-16", resp.Data.EndDate)
}

func TestGetUserBreakdown_EmptyResult(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&group_id=999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Users []usagestats.UserBreakdownItem `json:"users"`
		} `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Empty(t, resp.Data.Users)
}

func TestGetUserBreakdown_NoFilters(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(0), repo.capturedDim.GroupID)
	require.Empty(t, repo.capturedDim.Model)
	require.Empty(t, repo.capturedDim.Endpoint)
}

func TestGetUserBreakdown_RequestTypeStringFilter(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  int16
	}{
		{"ws_v2", "ws_v2", int16(service.RequestTypeWSV2)},
		{"stream", "stream", int16(service.RequestTypeStream)},
		{"sync", "sync", int16(service.RequestTypeSync)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userBreakdownRepoCapture{}
			router := newUserBreakdownRouter(repo)

			req := httptest.NewRequest(http.MethodGet,
				"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&request_type="+tc.value, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, repo.capturedDim.RequestType, "request_type=%s should set filter", tc.value)
			require.Equal(t, tc.want, *repo.capturedDim.RequestType)
		})
	}
}

func TestGetUserBreakdown_InvalidRequestType(t *testing.T) {
	repo := &userBreakdownRepoCapture{}
	router := newUserBreakdownRouter(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/dashboard/user-breakdown?start_date=2026-03-01&end_date=2026-03-16&request_type=bogus", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

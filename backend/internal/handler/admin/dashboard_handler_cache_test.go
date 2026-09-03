package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardUsageRepoCacheProbe struct {
	service.UsageLogRepository
	trendCalls      atomic.Int32
	modelCalls      atomic.Int32
	groupCalls      atomic.Int32
	usersTrendCalls atomic.Int32
}

func (r *dashboardUsageRepoCacheProbe) GetUsageTrendWithFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
) ([]usagestats.TrendDataPoint, error) {
	r.trendCalls.Add(1)
	return []usagestats.TrendDataPoint{{
		Date:        "2026-03-11",
		Requests:    1,
		TotalTokens: 2,
		Cost:        3,
		ActualCost:  4,
	}}, nil
}

func (r *dashboardUsageRepoCacheProbe) GetUsageTrendWithUsageFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	filters usagestats.UsageLogFilters,
) ([]usagestats.TrendDataPoint, error) {
	return r.GetUsageTrendWithFilters(
		ctx, startTime, endTime, granularity,
		filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID,
		filters.Model, filters.RequestType, filters.Stream, filters.BillingType,
	)
}

func (r *dashboardUsageRepoCacheProbe) GetUserUsageTrend(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	limit int,
) ([]usagestats.UserUsageTrendPoint, error) {
	r.usersTrendCalls.Add(1)
	return []usagestats.UserUsageTrendPoint{{
		Date:       "2026-03-11",
		UserID:     1,
		Email:      "cache@test.dev",
		Requests:   2,
		Tokens:     20,
		Cost:       2,
		ActualCost: 1,
	}}, nil
}

func (r *dashboardUsageRepoCacheProbe) GetModelStatsWithUsageFiltersBySource(
	_ context.Context,
	_, _ time.Time,
	_ usagestats.UsageLogFilters,
	_ string,
) ([]usagestats.ModelStat, error) {
	r.modelCalls.Add(1)
	return []usagestats.ModelStat{}, nil
}

func (r *dashboardUsageRepoCacheProbe) GetGroupStatsWithUsageFilters(
	_ context.Context,
	_, _ time.Time,
	_ usagestats.UsageLogFilters,
) ([]usagestats.GroupStat, error) {
	r.groupCalls.Add(1)
	return []usagestats.GroupStat{}, nil
}

func resetDashboardReadCachesForTest() {
	dashboardTrendCache = newSnapshotCache(30 * time.Second)
	dashboardUsersTrendCache = newSnapshotCache(30 * time.Second)
	dashboardAPIKeysTrendCache = newSnapshotCache(30 * time.Second)
	dashboardModelStatsCache = newSnapshotCache(30 * time.Second)
	dashboardGroupStatsCache = newSnapshotCache(30 * time.Second)
	dashboardSnapshotV2Cache = newSnapshotCache(30 * time.Second)
}

func TestDashboardHandler_GetUsageTrend_UsesCache(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()

	gin.SetMode(gin.TestMode)
	repo := &dashboardUsageRepoCacheProbe{}
	dashboardSvc := service.NewDashboardService(repo, nil, nil, nil)
	handler := NewDashboardHandler(dashboardSvc, nil)
	router := gin.New()
	router.GET("/admin/dashboard/trend", handler.GetUsageTrend)

	req1 := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?start_date=2026-03-01&end_date=2026-03-07&granularity=day", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)
	require.Equal(t, "miss", rec1.Header().Get("X-Snapshot-Cache"))

	req2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?start_date=2026-03-01&end_date=2026-03-07&granularity=day", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Equal(t, "hit", rec2.Header().Get("X-Snapshot-Cache"))
	require.Equal(t, int32(1), repo.trendCalls.Load())
}

func TestDashboardHandler_GetUserUsageTrend_UsesCache(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()

	gin.SetMode(gin.TestMode)
	repo := &dashboardUsageRepoCacheProbe{}
	dashboardSvc := service.NewDashboardService(repo, nil, nil, nil)
	handler := NewDashboardHandler(dashboardSvc, nil)
	router := gin.New()
	router.GET("/admin/dashboard/users-trend", handler.GetUserUsageTrend)

	req1 := httptest.NewRequest(http.MethodGet, "/admin/dashboard/users-trend?start_date=2026-03-01&end_date=2026-03-07&granularity=day&limit=8", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)
	require.Equal(t, "miss", rec1.Header().Get("X-Snapshot-Cache"))

	req2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard/users-trend?start_date=2026-03-01&end_date=2026-03-07&granularity=day&limit=8", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Equal(t, "hit", rec2.Header().Get("X-Snapshot-Cache"))
	require.Equal(t, int32(1), repo.usersTrendCalls.Load())
}

func TestDashboardHandler_SnapshotModelAndGroupCachesIncludeModelFilter(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()

	gin.SetMode(gin.TestMode)
	repo := &dashboardUsageRepoCacheProbe{}
	dashboardSvc := service.NewDashboardService(repo, nil, nil, nil)
	handler := NewDashboardHandler(dashboardSvc, nil)
	router := gin.New()
	router.GET("/admin/dashboard/snapshot-v2", handler.GetSnapshotV2)

	for _, model := range []string{"claude-opus-4-6", "gpt-5.4"} {
		req := httptest.NewRequest(http.MethodGet,
			"/admin/dashboard/snapshot-v2?include_stats=false&include_trend=false&include_model_stats=true&include_group_stats=true&model="+model,
			nil,
		)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	require.Equal(t, int32(2), repo.modelCalls.Load())
	require.Equal(t, int32(2), repo.groupCalls.Load())
}

func TestDashboardHandler_SnapshotCachesDistinguishBillingMode(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()

	gin.SetMode(gin.TestMode)
	repo := &dashboardUsageRepoCacheProbe{}
	handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	router := gin.New()
	router.GET("/admin/dashboard/snapshot-v2", handler.GetSnapshotV2)

	for _, mode := range []string{"", "token", "image"} {
		path := "/admin/dashboard/snapshot-v2?include_stats=false&include_trend=true&include_model_stats=true&include_group_stats=true&model=gpt-5.6-sol"
		if mode != "" {
			path += "&billing_mode=" + mode
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, mode)
		require.Equal(t, "miss", rec.Header().Get("X-Snapshot-Cache"), mode)
	}
	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: "upstream", want: "miss"},
		{source: "requested", want: "hit"},
	} {
		path := "/admin/dashboard/snapshot-v2?include_stats=false&include_trend=true&include_model_stats=true&include_group_stats=true&model=gpt-5.6-sol&model_source=" + tc.source
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, tc.source)
		require.Equal(t, tc.want, rec.Header().Get("X-Snapshot-Cache"), tc.source)
	}

	require.Equal(t, int32(4), repo.trendCalls.Load())
	require.Equal(t, int32(4), repo.modelCalls.Load())
	require.Equal(t, int32(4), repo.groupCalls.Load())
}

func TestDashboardHandler_CacheKeysPreserveRawModelFilterSource(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	raw := usagestats.UsageLogFilters{
		Model:           "claude-opus-4-6",
		ExcludedUserIDs: []int64{9, 3},
	}
	requested := raw
	requested.ModelFilterSource = usagestats.ModelSourceRequested
	requested.ExcludedUserIDs = []int64{3, 9, 3}
	sameRequested := requested
	sameRequested.ExcludedUserIDs = []int64{9, 3}

	t.Run("trend", func(t *testing.T) {
		t.Cleanup(resetDashboardReadCachesForTest)
		resetDashboardReadCachesForTest()
		repo := &dashboardUsageRepoCacheProbe{}
		handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)

		_, firstHit, err := handler.getUsageTrendCached(context.Background(), start, end, "day", raw)
		require.NoError(t, err)
		require.False(t, firstHit)
		_, secondHit, err := handler.getUsageTrendCached(context.Background(), start, end, "day", requested)
		require.NoError(t, err)
		require.False(t, secondHit)
		_, normalizedHit, err := handler.getUsageTrendCached(context.Background(), start, end, "day", sameRequested)
		require.NoError(t, err)
		require.True(t, normalizedHit)
		require.Equal(t, int32(2), repo.trendCalls.Load())
	})

	t.Run("group", func(t *testing.T) {
		t.Cleanup(resetDashboardReadCachesForTest)
		resetDashboardReadCachesForTest()
		repo := &dashboardUsageRepoCacheProbe{}
		handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)

		_, firstHit, err := handler.getGroupStatsCached(context.Background(), start, end, raw)
		require.NoError(t, err)
		require.False(t, firstHit)
		_, secondHit, err := handler.getGroupStatsCached(context.Background(), start, end, requested)
		require.NoError(t, err)
		require.False(t, secondHit)
		_, normalizedHit, err := handler.getGroupStatsCached(context.Background(), start, end, sameRequested)
		require.NoError(t, err)
		require.True(t, normalizedHit)
		require.Equal(t, int32(2), repo.groupCalls.Load())
	})
}

func TestDashboardHandler_RegularCachesDistinguishModelAndBillingMode(t *testing.T) {
	start := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	base := usagestats.UsageLogFilters{
		Model:             "gpt-5.6-sol",
		ModelFilterSource: usagestats.ModelSourceRequested,
	}
	cases := []usagestats.UsageLogFilters{
		base,
		func() usagestats.UsageLogFilters { f := base; f.BillingMode = "image"; return f }(),
		func() usagestats.UsageLogFilters { f := base; f.Model = "gpt-5.4"; return f }(),
		func() usagestats.UsageLogFilters {
			f := base
			f.ModelFilterSource = usagestats.ModelSourceUpstream
			return f
		}(),
	}

	t.Run("trend", func(t *testing.T) {
		t.Cleanup(resetDashboardReadCachesForTest)
		resetDashboardReadCachesForTest()
		repo := &dashboardUsageRepoCacheProbe{}
		handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
		for _, filters := range cases {
			_, hit, err := handler.getUsageTrendCached(context.Background(), start, end, "day", filters)
			require.NoError(t, err)
			require.False(t, hit)
		}
		_, hit, err := handler.getUsageTrendCached(context.Background(), start, end, "day", cases[0])
		require.NoError(t, err)
		require.True(t, hit)
		require.Equal(t, int32(len(cases)), repo.trendCalls.Load())
	})

	t.Run("model", func(t *testing.T) {
		t.Cleanup(resetDashboardReadCachesForTest)
		resetDashboardReadCachesForTest()
		repo := &dashboardUsageRepoCacheProbe{}
		handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
		for _, filters := range cases {
			_, hit, err := handler.getModelStatsCached(context.Background(), start, end, filters, filters.ModelFilterSource)
			require.NoError(t, err)
			require.False(t, hit)
		}
		_, hit, err := handler.getModelStatsCached(context.Background(), start, end, cases[0], cases[0].ModelFilterSource)
		require.NoError(t, err)
		require.True(t, hit)
		require.Equal(t, int32(len(cases)), repo.modelCalls.Load())
	})

	t.Run("group", func(t *testing.T) {
		t.Cleanup(resetDashboardReadCachesForTest)
		resetDashboardReadCachesForTest()
		repo := &dashboardUsageRepoCacheProbe{}
		handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
		for _, filters := range cases {
			_, hit, err := handler.getGroupStatsCached(context.Background(), start, end, filters)
			require.NoError(t, err)
			require.False(t, hit)
		}
		_, hit, err := handler.getGroupStatsCached(context.Background(), start, end, cases[0])
		require.NoError(t, err)
		require.True(t, hit)
		require.Equal(t, int32(len(cases)), repo.groupCalls.Load())
	})
}

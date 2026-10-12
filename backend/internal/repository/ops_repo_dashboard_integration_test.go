//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsOutputTPSDistribution(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "output-tps", Platform: service.PlatformAnthropic})
	group := mustCreateGroup(t, client, &service.Group{Name: "output-tps", Platform: service.PlatformOpenAI})
	otherGroup := mustCreateGroup(t, client, &service.Group{Name: "other-output-tps", Platform: service.PlatformOpenAI})
	start := time.Date(2021, 1, 17, 0, 30, 0, 0, time.UTC)
	end := start.Add(6 * time.Hour)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_metrics_hourly WHERE bucket_start >= $1 AND bucket_start < $2", start.Truncate(time.Hour), end)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, otherGroup.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	insert := func(at time.Time, groupID *int64, output int, duration *int, requestType service.RequestType, imageCount, imageTokens int, billingMode string, videoCounts ...int) {
		t.Helper()
		videoCount := 0
		if len(videoCounts) > 0 {
			videoCount = videoCounts[0]
		}
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
   (user_id, api_key_id, account_id, group_id, request_id, model, input_tokens, output_tokens, duration_ms, request_type, image_count, image_output_tokens, billing_mode, created_at, image_size, video_count)
   VALUES ($1,$2,$3,$4,$5,'gpt-test',100000,$6,$7,$8,$9,$10,$11,$12,CASE WHEN $9 > 0 THEN '1K' ELSE NULL END,$13)`,
			user.ID, key.ID, account.ID, groupID, fmt.Sprintf("output-tps-%d-%d", user.ID, time.Now().UnixNano()), output, duration, requestType, imageCount, imageTokens, billingMode, at, videoCount)
		require.NoError(t, err)
	}
	// Rates 1, 10, 100, 1000: unequal durations catch ratio-of-sums mistakes.
	// Put samples in the raw head, stable hourly buckets, and raw tail.
	for _, sample := range []struct {
		output, duration int
		kind             service.RequestType
		at               time.Time
	}{
		{1, 1000, service.RequestTypeSync, start},
		{20, 2000, service.RequestTypeStream, start.Add(time.Hour)},
		{100, 1000, service.RequestTypeWSV2, start.Add(2 * time.Hour)},
		{500, 500, service.RequestTypeCyberBlocked, end.Add(-time.Minute)},
	} {
		insert(sample.at, &group.ID, sample.output, &sample.duration, sample.kind, 0, 0, "token")
	}
	positive, zero, negative := 1000, 0, -1
	for _, d := range []*int{nil, &zero, &negative} {
		insert(start, &group.ID, 9999, d, service.RequestTypeSync, 0, 0, "token")
	}
	for _, n := range []int{0, -1} {
		insert(start, &group.ID, n, &positive, service.RequestTypeSync, 0, 0, "token")
	}
	for _, kind := range []service.RequestType{service.RequestTypeLive, service.RequestTypeUnknown} {
		insert(start, &group.ID, 9999, &positive, kind, 0, 0, "token")
	}
	insert(start, &group.ID, 9999, &positive, service.RequestTypeSync, 1, 0, "token")
	insert(start, &group.ID, 9999, &positive, service.RequestTypeSync, 0, 1, "token")
	insert(start, &group.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "image")
	insert(start, &group.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "video")
	insert(start, &group.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "token", 1)
	insert(start.Add(-time.Second), &group.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "token")
	insert(end, &group.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "token")
	insert(start, &otherGroup.ID, 9999, &positive, service.RequestTypeSync, 0, 0, "token")
	insert(start, nil, 50, &positive, service.RequestTypeSync, 0, 0, "token")
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end, Platform: " OpenAI ", GroupID: &group.ID}
	assertStats := func(stats *service.OpsOutputTPS) {
		t.Helper()
		require.NotNil(t, stats)
		require.EqualValues(t, 4, stats.SampleCount)
		require.NotNil(t, stats.P5)
		require.NotNil(t, stats.P10)
		require.NotNil(t, stats.P50)
		require.NotNil(t, stats.Avg)
		require.InDelta(t, 2.35, *stats.P5, 1e-9)
		require.InDelta(t, 3.7, *stats.P10, 1e-9)
		require.InDelta(t, 55, *stats.P50, 1e-9)
		require.InDelta(t, 277.75, *stats.Avg, 1e-9)
	}
	// Auto first exercises fallback before hourly rows exist.
	for _, mode := range []service.OpsQueryMode{service.OpsQueryModeRaw, service.OpsQueryModeAuto} {
		filter.QueryMode = mode
		overview, err := repo.GetDashboardOverview(ctx, filter)
		require.NoError(t, err)
		assertStats(overview.OutputTPS)
	}
	require.NoError(t, repo.UpsertHourlyMetrics(ctx, start.Truncate(time.Hour), end))
	for _, mode := range []service.OpsQueryMode{service.OpsQueryModePreagg, service.OpsQueryModeAuto} {
		filter.QueryMode = mode
		overview, err := repo.GetDashboardOverview(ctx, filter)
		require.NoError(t, err)
		assertStats(overview.OutputTPS)
	}
	filter.Platform = service.PlatformAnthropic
	empty, err := repo.queryOutputTPS(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, &service.OpsOutputTPS{}, empty)
	// Ungrouped logs use the account platform; group platform takes precedence.
	filter.GroupID = nil
	fallback, err := repo.queryOutputTPS(ctx, filter)
	require.NoError(t, err)
	require.EqualValues(t, 1, fallback.SampleCount)
	require.Equal(t, 50.0, *fallback.P50)
}

func TestOpsOutputTPSQueryDeadline(t *testing.T) {
	tx := testTx(t)
	_, err := tx.ExecContext(context.Background(), "LOCK TABLE usage_logs IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	now := time.Now().UTC()
	stats, err := repo.queryOutputTPS(ctx, &service.OpsDashboardFilter{StartTime: now.Add(-time.Hour), EndTime: now})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, stats)
}

func TestOpsOutputTPSSeedanceEndpoints(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "seedance-output-tps", Platform: service.PlatformOpenAI})
	group := mustCreateGroup(t, client, &service.Group{Name: "seedance-output-tps", Platform: service.PlatformOpenAI})
	start := time.Date(2021, 3, 17, 0, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Hour)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_metrics_hourly WHERE bucket_start >= $1 AND bucket_start < $2", start, end)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	insert := func(id string, output, duration int, imageCount any, billingMode, inbound, upstream any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
   (user_id, api_key_id, account_id, group_id, request_id, model, output_tokens, duration_ms, request_type, image_count, image_output_tokens, video_count, billing_mode, inbound_endpoint, upstream_endpoint, created_at)
   VALUES ($1,$2,$3,$4,$5,'public-video-alias',$6,$7,$8,$9,0,0,$10,$11,$12,$13)`,
			user.ID, key.ID, account.ID, group.ID, fmt.Sprintf("seedance-output-tps-%d-%s", user.ID, id), output, duration, service.RequestTypeSync, imageCount, billingMode, inbound, upstream, start.Add(time.Hour))
		require.NoError(t, err)
	}
	// Seedance's completion_tokens retain token pricing and zero video_count.
	// Either persisted canonical endpoint identifies the asynchronous video task.
	const seedanceEndpoint = "/api/v3/contents/generations/tasks"
	insert("inbound", 12345, 60000, 0, "token", seedanceEndpoint, nil)
	insert("upstream", 12345, 60000, 0, "token", nil, seedanceEndpoint)
	insert("inbound-null-billing", 12345, 60000, 0, nil, seedanceEndpoint, "/v1/responses")
	insert("upstream-null-billing", 12345, 60000, 0, nil, "/v1/responses", seedanceEndpoint)
	// image_count is nullable in historical rows; video_count is NOT NULL.
	insert("historical-null-image-count", 12345, 60000, nil, "token", nil, seedanceEndpoint)

	repo := NewOpsRepository(integrationDB).(*opsRepository)
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end, Platform: service.PlatformOpenAI, GroupID: &group.ID}
	checkModes := func(wantStats *service.OpsOutputTPS, wantCount, wantTokens int64) {
		t.Helper()
		for _, mode := range []service.OpsQueryMode{service.OpsQueryModeRaw, service.OpsQueryModeAuto, service.OpsQueryModePreagg} {
			filter.QueryMode = mode
			overview, err := repo.GetDashboardOverview(ctx, filter)
			require.NoError(t, err, "mode %s", mode)
			require.Equal(t, wantStats, overview.OutputTPS, "mode %s", mode)
			// Classification affects output-rate samples, not usage/accounting totals.
			require.Equal(t, wantCount, overview.SuccessCount, "mode %s", mode)
			require.Equal(t, wantCount, overview.RequestCountTotal, "mode %s", mode)
			require.Equal(t, wantTokens, overview.TokenConsumed, "mode %s", mode)
		}
	}
	filter.QueryMode = service.OpsQueryModeAuto
	fallback, err := repo.GetDashboardOverview(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, &service.OpsOutputTPS{}, fallback.OutputTPS)
	require.NoError(t, repo.UpsertHourlyMetrics(ctx, start, end))
	checkModes(&service.OpsOutputTPS{}, 5, 61725)

	// A model alias alone never classifies media. Missing and empty endpoints
	// also remain eligible for normal text output rates.
	insert("text-null-endpoints", 1, 1000, 0, nil, nil, nil)
	insert("text-empty-endpoints", 20, 2000, 0, "", "", "")
	insert("text-responses", 100, 1000, 0, "token", "/v1/responses", "/v1/responses")
	require.NoError(t, repo.UpsertHourlyMetrics(ctx, start, end))
	p5, p10, p50, avg := 1.9, 2.8, 10.0, 37.0
	checkModes(&service.OpsOutputTPS{P5: &p5, P10: &p10, P50: &p50, Avg: &avg, SampleCount: 3}, 8, 61846)
}

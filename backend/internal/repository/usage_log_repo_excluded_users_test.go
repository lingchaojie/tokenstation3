package repository

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type excludedUserIDsArgument struct{}

func (excludedUserIDsArgument) Match(value driver.Value) bool {
	return fmt.Sprint(value) == "{2,7}"
}

func TestUsageLogRepositoryListWithFiltersExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	filters := usagestats.UsageLogFilters{
		ExcludedUserIDs: []int64{7, 2, 7},
		ExactTotal:      true,
	}

	predicate := `\(user_id IS NULL OR NOT \(user_id = ANY\(\$1\)\)\)`
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM usage_logs WHERE ` + predicate).
		WithArgs(excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery(`SELECT .* FROM usage_logs WHERE `+predicate+` ORDER BY id DESC LIMIT \$2 OFFSET \$3`).
		WithArgs(excludedUserIDsArgument{}, 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, filters)
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetStatsWithFiltersExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	filters := usagestats.UsageLogFilters{ExcludedUserIDs: []int64{7, 2, 7}}

	predicate := `\(user_id IS NULL OR NOT \(user_id = ANY\(\$1\)\)\)`
	mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE ` + predicate).
		WithArgs(excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens",
			"total_cache_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
			"total_cost", "total_actual_cost", "total_account_cost", "avg_duration_ms",
		}).AddRow(int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0, 0.0, 0.0))

	for _, prefix := range []string{
		`SELECT COALESCE\(NULLIF\(TRIM\(inbound_endpoint\)`,
		`SELECT COALESCE\(NULLIF\(TRIM\(upstream_endpoint\)`,
		`SELECT CONCAT\(`,
	} {
		mock.ExpectQuery(`(?s)`+prefix+`.*`+`\(user_id IS NULL OR NOT \(user_id = ANY\(\$3\)\)\)`).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), excludedUserIDsArgument{}).
			WillReturnRows(sqlmock.NewRows([]string{"endpoint", "requests", "total_tokens", "cost", "actual_cost"}))
	}

	stats, err := repo.GetStatsWithFilters(context.Background(), filters)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetUsageTrendWithUsageFiltersExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	filters := usagestats.UsageLogFilters{ExcludedUserIDs: []int64{7, 2, 7}}

	mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE created_at >= \$1 AND created_at < \$2.*\(user_id IS NULL OR NOT \(user_id = ANY\(\$3\)\)\).*GROUP BY date`).
		WithArgs(start, end, excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}))

	trend, err := repo.GetUsageTrendWithUsageFilters(context.Background(), start, end, "day", filters)
	require.NoError(t, err)
	require.Empty(t, trend)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestShouldUsePreaggregatedTrendExcludedUsers(t *testing.T) {
	require.False(t, shouldUsePreaggregatedTrend("day", 0, 0, 0, 0, "", nil, nil, nil, "", []int64{7, 2, 7}))
}

func TestUsageLogRepositoryGetModelStatsWithUsageFiltersExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	filters := usagestats.UsageLogFilters{ExcludedUserIDs: []int64{7, 2, 7}}

	mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE created_at >= \$1 AND created_at < \$2.*\(user_id IS NULL OR NOT \(user_id = ANY\(\$3\)\)\).*GROUP BY`).
		WithArgs(start, end, excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}))

	stats, err := repo.GetModelStatsWithUsageFiltersBySource(context.Background(), start, end, filters, usagestats.ModelSourceRequested)
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetGroupStatsWithUsageFiltersExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	filters := usagestats.UsageLogFilters{ExcludedUserIDs: []int64{7, 2, 7}}

	mock.ExpectQuery(`(?s)FROM usage_logs ul.*WHERE ul.created_at >= \$1 AND ul.created_at < \$2.*\(ul.user_id IS NULL OR NOT \(ul.user_id = ANY\(\$3\)\)\).*GROUP BY`).
		WithArgs(start, end, excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}))

	stats, err := repo.GetGroupStatsWithUsageFilters(context.Background(), start, end, filters)
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetUserBreakdownStatsExcludedUsers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	dim := usagestats.UserBreakdownDimension{ExcludedUserIDs: []int64{7, 2, 7}}

	mock.ExpectQuery(`(?s)FROM usage_logs ul.*WHERE ul.created_at >= \$1 AND ul.created_at < \$2.*\(ul.user_id IS NULL OR NOT \(ul.user_id = ANY\(\$3\)\)\).*GROUP BY`).
		WithArgs(start, end, excludedUserIDsArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "email", "requests", "input_tokens", "output_tokens", "cache_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}))

	stats, err := repo.GetUserBreakdownStats(context.Background(), start, end, dim, 10)
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

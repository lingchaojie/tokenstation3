//go:build unit

package repository

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageAggregationQueriesApplyCompleteObservabilityFilters(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	native := false
	filters := UsageLogFilters{
		UserID:                   11,
		AccountID:                31,
		ChannelID:                41,
		GroupID:                  51,
		Model:                    "requested-model",
		ModelFilterSource:        usagestats.ModelSourceRequested,
		ServiceTier:              "priority",
		ReasoningEffort:          "xhigh",
		RequestedReasoningEffort: "max",
		InboundEndpoint:          "/v1/responses",
		UpstreamEndpoint:         "/v1/chat/completions",
		NativeCompactionV2:       &native,
	}
	wantArgs := []driver.Value{
		start, end, int64(11), int64(31), int64(41), int64(51), "requested-model",
		"priority", "xhigh", "max", "/v1/responses", "/v1/chat/completions", false,
	}

	tests := []struct {
		name      string
		fragments []string
		rows      *sqlmock.Rows
		invoke    func(*usageLogRepository) error
	}{
		{
			name: "trend",
			fragments: []string{
				"user_id = $3", "account_id = $4", "channel_id = $5", "group_id = $6",
				"COALESCE(NULLIF(TRIM(requested_model), ''), model) = $7",
				"service_tier = $8", "reasoning_effort = $9",
				"COALESCE(NULLIF(TRIM(requested_reasoning_effort), ''), reasoning_effort) = $10",
				"inbound_endpoint = $11", "upstream_endpoint = $12", "native_compaction_v2 = $13",
			},
			rows: sqlmock.NewRows([]string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}),
			invoke: func(repo *usageLogRepository) error {
				_, err := repo.GetUsageTrendWithUsageFilters(t.Context(), start, end, "minute", filters)
				return err
			},
		},
		{
			name: "model",
			fragments: []string{
				"user_id = $3", "account_id = $4", "channel_id = $5", "group_id = $6",
				"COALESCE(NULLIF(TRIM(requested_model), ''), model) = $7",
				"service_tier = $8", "reasoning_effort = $9",
				"COALESCE(NULLIF(TRIM(requested_reasoning_effort), ''), reasoning_effort) = $10",
				"inbound_endpoint = $11", "upstream_endpoint = $12", "native_compaction_v2 = $13",
			},
			rows: sqlmock.NewRows([]string{"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}),
			invoke: func(repo *usageLogRepository) error {
				_, err := repo.GetModelStatsWithUsageFiltersBySource(t.Context(), start, end, filters, usagestats.ModelSourceRequested)
				return err
			},
		},
		{
			name: "group",
			fragments: []string{
				"ul.user_id = $3", "ul.account_id = $4", "ul.channel_id = $5", "ul.group_id = $6",
				"COALESCE(NULLIF(TRIM(ul.requested_model), ''), ul.model) = $7",
				"ul.service_tier = $8", "ul.reasoning_effort = $9",
				"COALESCE(NULLIF(TRIM(ul.requested_reasoning_effort), ''), ul.reasoning_effort) = $10",
				"ul.inbound_endpoint = $11", "ul.upstream_endpoint = $12", "ul.native_compaction_v2 = $13",
			},
			rows: sqlmock.NewRows([]string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}),
			invoke: func(repo *usageLogRepository) error {
				_, err := repo.GetGroupStatsWithUsageFilters(t.Context(), start, end, filters)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(orderedSQLFragmentsMatcher(tt.fragments)))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectQuery("complete observability filters").WithArgs(wantArgs...).WillReturnRows(tt.rows)
			require.NoError(t, tt.invoke(&usageLogRepository{sql: db}))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageTrendNewObservabilityFilterBypassesPreaggregate(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(orderedSQLFragmentsMatcher([]string{
		"FROM usage_logs", "service_tier = $3",
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("filtered raw trend").WithArgs(start, end, "priority").WillReturnRows(
		sqlmock.NewRows([]string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}),
	)
	_, err = (&usageLogRepository{sql: db}).GetUsageTrendWithUsageFilters(t.Context(), start, end, "hour", UsageLogFilters{ServiceTier: "priority"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func orderedSQLFragmentsMatcher(fragments []string) sqlmock.QueryMatcher {
	return sqlmock.QueryMatcherFunc(func(_, actual string) error {
		normalized := strings.Join(strings.Fields(actual), " ")
		cursor := 0
		for _, fragment := range fragments {
			idx := strings.Index(normalized[cursor:], fragment)
			if idx < 0 {
				return fmt.Errorf("SQL missing ordered fragment %q after offset %d: %s", fragment, cursor, normalized)
			}
			cursor += idx + len(fragment)
		}
		return nil
	})
}

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogInsertColumnContractIncludesLocalAndUpstreamFields(t *testing.T) {
	wantColumns := []string{
		"user_id", "api_key_id", "account_id", "request_id", "model",
		"requested_model", "upstream_model", "upstream_response_model", "upstream_model_mismatch",
		"group_id", "subscription_id", "input_tokens", "output_tokens", "cache_creation_tokens",
		"cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens",
		"image_output_tokens", "image_output_cost", "image_input_tokens", "image_input_cost",
		"input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost",
		"actual_cost", "rate_multiplier", "account_rate_multiplier", "billing_type", "request_type",
		"stream", "openai_ws_mode", "duration_ms", "first_token_ms", "user_agent", "ip_address",
		"image_count", "image_size", "image_input_size", "image_output_size", "image_size_source",
		"image_size_breakdown", "video_count", "video_resolution", "video_duration_seconds",
		"service_tier", "reasoning_effort", "inbound_endpoint", "upstream_endpoint",
		"cache_ttl_overridden", "long_context_billing_applied", "channel_id", "model_mapping_chain",
		"billing_tier", "billing_mode", "account_stats_cost", "kiro_credits", "session_id", "created_at",
	}
	wantTypes := []string{
		"bigint", "bigint", "bigint", "text", "text", "text", "text", "text", "boolean",
		"bigint", "bigint", "integer", "integer", "integer", "integer", "integer", "integer",
		"integer", "numeric", "integer", "numeric", "numeric", "numeric", "numeric", "numeric",
		"numeric", "numeric", "numeric", "numeric", "smallint", "smallint", "boolean", "boolean",
		"integer", "integer", "text", "text", "integer", "text", "text", "text", "text", "jsonb",
		"integer", "text", "integer", "text", "text", "text", "text", "boolean", "boolean",
		"bigint", "text", "text", "text", "numeric", "numeric", "text", "timestamptz",
	}

	gotSelectColumns := splitUsageContractCSV(usageLogSelectColumns)
	require.Len(t, gotSelectColumns, 61)
	require.Equal(t, "id", gotSelectColumns[0])
	require.Equal(t, wantColumns, gotSelectColumns[1:])
	require.Equal(t, wantTypes, usageLogInsertArgTypes[:])

	kiroCredits := 1.25
	sessionID := "session-contract"
	createdAt := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	log := &service.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "req-contract", Model: "gpt-5.6",
		RequestedModel: "requested", SessionID: &sessionID, CreatedAt: createdAt,
	}
	kiroField := reflect.ValueOf(log).Elem().FieldByName("KiroCredits")
	require.True(t, kiroField.IsValid(), "local KiroCredits usage field must be preserved")
	kiroField.Set(reflect.ValueOf(&kiroCredits))
	prepared := prepareUsageLogInsert(log)
	require.Len(t, prepared.args, len(wantColumns))
	preparedKiroCredits, ok := prepared.args[57].(*float64)
	require.True(t, ok)
	require.NotNil(t, preparedKiroCredits)
	require.Equal(t, kiroCredits, *preparedKiroCredits)
	require.Equal(t, sql.NullString{String: sessionID, Valid: true}, prepared.args[58])
	require.Equal(t, createdAt, prepared.args[59])

	t.Run("single", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
			func(_, actual string) error { return validateStaticUsageContract(actual, wantColumns) },
		)))
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("usage contract").WithArgs(contractDriverValues(prepared.args)...).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(10), createdAt))
		repo := &usageLogRepository{sql: db}
		inserted, err := repo.createSingle(context.Background(), db, log)
		require.NoError(t, err)
		require.True(t, inserted)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fallback", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
			func(_, actual string) error { return validateStaticUsageContract(actual, wantColumns) },
		)))
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectExec("usage contract").WithArgs(contractDriverValues(prepared.args)...).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, execUsageLogInsertNoResult(context.Background(), db, prepared))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("queued_batch", func(t *testing.T) {
		key := usageLogBatchKey(log.RequestID, log.APIKeyID)
		query, args := buildUsageLogBatchInsertQuery([]string{key, key}, map[string]usageLogInsertPrepared{key: prepared})
		require.Len(t, args, 2*(len(wantColumns)+1))
		requireContinuousUsageContractPlaceholders(t, query, len(args))
		require.GreaterOrEqual(t, countOrderedUsageContractColumns(query, wantColumns), 3)
	})

	t.Run("best_effort_batch", func(t *testing.T) {
		query, args := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared, prepared})
		require.Len(t, args, 2*len(wantColumns))
		requireContinuousUsageContractPlaceholders(t, query, len(args))
		require.GreaterOrEqual(t, countOrderedUsageContractColumns(query, wantColumns), 3)
	})
}

func validateStaticUsageContract(query string, wantColumns []string) error {
	match := regexp.MustCompile(`(?is)INSERT\s+INTO\s+usage_logs\s*\((.*?)\)\s*VALUES\s*\((.*?)\)`).FindStringSubmatch(query)
	if len(match) != 3 {
		return fmt.Errorf("usage log INSERT shape not found")
	}
	if got := splitUsageContractCSV(match[1]); !equalUsageContractStrings(got, wantColumns) {
		return fmt.Errorf("columns mismatch: got %v want %v", got, wantColumns)
	}
	values := splitUsageContractCSV(match[2])
	if len(values) != len(wantColumns) {
		return fmt.Errorf("values=%d want=%d", len(values), len(wantColumns))
	}
	for i, value := range values {
		if want := "$" + strconv.Itoa(i+1); value != want {
			return fmt.Errorf("placeholder %d=%q want=%q", i+1, value, want)
		}
	}
	return nil
}

func requireContinuousUsageContractPlaceholders(t *testing.T, query string, wantCount int) {
	t.Helper()
	matches := regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(query, -1)
	require.Len(t, matches, wantCount)
	for i, match := range matches {
		require.Equal(t, strconv.Itoa(i+1), match[1], "placeholder at offset %d", i)
	}
}

func countOrderedUsageContractColumns(query string, columns []string) int {
	normalize := func(raw string) string { return strings.Join(strings.Fields(raw), "") }
	want := normalize(strings.Join(columns, ","))
	return strings.Count(normalize(query), want)
}

func splitUsageContractCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func equalUsageContractStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contractDriverValues(values []any) []driver.Value {
	out := make([]driver.Value, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}

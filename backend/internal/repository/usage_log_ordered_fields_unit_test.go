//go:build unit

package repository

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type usageLogOrderedFieldFixture struct {
	name    string
	argPos  int
	sqlType string
	want    any
}

func TestUsageLogOrderedFields_NormalAndBatchInsertContract(t *testing.T) {
	serviceTier := "priority"
	reasoningEffort := "xhigh"
	requestedReasoningEffort := "max"
	inboundEndpoint := "/v1/responses"
	upstreamEndpoint := "/v1/chat/completions"
	channelID := int64(41)
	sessionID := "session-ordered-fields"
	createdAt := time.Date(2026, 9, 1, 8, 9, 10, 0, time.UTC)
	log := &service.UsageLog{
		UserID:                   11,
		APIKeyID:                 21,
		AccountID:                31,
		RequestID:                "req-ordered-fields",
		Model:                    "gpt-5.6-sol",
		ServiceTier:              &serviceTier,
		ReasoningEffort:          &reasoningEffort,
		RequestedReasoningEffort: &requestedReasoningEffort,
		InboundEndpoint:          &inboundEndpoint,
		UpstreamEndpoint:         &upstreamEndpoint,
		ChannelID:                &channelID,
		SessionID:                &sessionID,
		NativeCompactionV2:       true,
		CreatedAt:                createdAt,
	}

	prepared := prepareUsageLogInsert(log)
	require.Len(t, usageLogInsertArgTypes, 62, "all local and approved upstream usage fields must have one typed argument")
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	ordered := []usageLogOrderedFieldFixture{
		{name: "service_tier", argPos: 46, sqlType: "text", want: sql.NullString{String: serviceTier, Valid: true}},
		{name: "reasoning_effort", argPos: 47, sqlType: "text", want: sql.NullString{String: reasoningEffort, Valid: true}},
		{name: "requested_reasoning_effort", argPos: 48, sqlType: "text", want: sql.NullString{String: requestedReasoningEffort, Valid: true}},
		{name: "inbound_endpoint", argPos: 49, sqlType: "text", want: sql.NullString{String: inboundEndpoint, Valid: true}},
		{name: "upstream_endpoint", argPos: 50, sqlType: "text", want: sql.NullString{String: upstreamEndpoint, Valid: true}},
		{name: "channel_id", argPos: 53, sqlType: "bigint", want: sql.NullInt64{Int64: channelID, Valid: true}},
		{name: "account_id", argPos: 2, sqlType: "bigint", want: int64(31)},
		{name: "session_id", argPos: 59, sqlType: "text", want: sql.NullString{String: sessionID, Valid: true}},
		{name: "native_compaction_v2", argPos: 60, sqlType: "boolean", want: true},
		{name: "created_at", argPos: 61, sqlType: "timestamptz", want: createdAt},
	}
	for _, field := range ordered {
		t.Run(field.name, func(t *testing.T) {
			require.Equal(t, field.sqlType, usageLogInsertArgTypes[field.argPos])
			require.Equal(t, field.want, prepared.args[field.argPos])
		})
	}

	key := usageLogBatchKey(log.RequestID, log.APIKeyID)
	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: prepared})
	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Len(t, batchArgs, len(prepared.args)+1)
	require.Len(t, bestEffortArgs, len(prepared.args))
	for _, field := range ordered {
		require.Contains(t, batchQuery, field.name)
		require.Contains(t, bestEffortQuery, field.name)
	}
	for _, query := range []string{batchQuery, bestEffortQuery} {
		require.Less(t, strings.Index(query, "requested_reasoning_effort"), strings.Index(query, "inbound_endpoint"))
		require.Less(t, strings.Index(query, "session_id"), strings.Index(query, "native_compaction_v2"))
	}
}

func TestUsageLogOrderedFields_NullabilityContract(t *testing.T) {
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID:    1,
		APIKeyID:  2,
		AccountID: 3,
		Model:     "gpt-5.6-sol",
		CreatedAt: time.Date(2026, 9, 1, 9, 10, 11, 0, time.UTC),
	})

	require.Equal(t, sql.NullString{}, prepared.args[46])
	require.Equal(t, sql.NullString{}, prepared.args[47])
	require.Equal(t, sql.NullString{}, prepared.args[48])
	require.Equal(t, sql.NullString{}, prepared.args[49])
	require.Equal(t, sql.NullString{}, prepared.args[50])
	require.Equal(t, sql.NullInt64{}, prepared.args[53])
	require.Equal(t, sql.NullString{}, prepared.args[59])
	require.Equal(t, false, prepared.args[60], "native_compaction_v2 is non-null and defaults false")
}

func TestUsageLogOrderedFields_Literal63ValueScanContract(t *testing.T) {
	createdAt := time.Date(2026, 9, 1, 12, 34, 56, 789, time.UTC)
	log, err := scanUsageLog(usageLogScannerStub{values: []any{
		int64(1001), // id
		int64(2001), // user_id
		int64(2101), // api_key_id
		int64(3001), // account_id
		sql.NullString{String: "request-4001", Valid: true}, // request_id
		"model-5001", // model
		sql.NullString{String: "requested-5002", Valid: true}, // requested_model
		sql.NullString{String: "upstream-5003", Valid: true},  // upstream_model
		sql.NullString{String: "response-5004", Valid: true},  // upstream_response_model
		sql.NullBool{Bool: true, Valid: true},                 // upstream_model_mismatch
		sql.NullInt64{Int64: 5101, Valid: true},               // group_id
		sql.NullInt64{Int64: 5201, Valid: true},               // subscription_id
		6101,                                                  // input_tokens
		6102,                                                  // output_tokens
		6103,                                                  // cache_creation_tokens
		6104,                                                  // cache_read_tokens
		6105,                                                  // cache_creation_5m_tokens
		6106,                                                  // cache_creation_1h_tokens
		6201,                                                  // image_output_tokens
		6202.25,                                               // image_output_cost
		6203,                                                  // image_input_tokens
		6204.25,                                               // image_input_cost
		6301.25,                                               // input_cost
		6302.25,                                               // output_cost
		6303.25,                                               // cache_creation_cost
		6304.25,                                               // cache_read_cost
		6305.25,                                               // total_cost
		6306.25,                                               // actual_cost
		6307.25,                                               // rate_multiplier
		sql.NullFloat64{Float64: 6308.25, Valid: true},    // account_rate_multiplier
		int16(service.BillingTypeSubscription),            // billing_type
		int16(service.RequestTypeWSV2),                    // request_type
		true,                                              // stream
		true,                                              // openai_ws_mode
		sql.NullInt64{Int64: 6401, Valid: true},           // duration_ms
		sql.NullInt64{Int64: 6402, Valid: true},           // first_token_ms
		sql.NullString{String: "agent-6501", Valid: true}, // user_agent
		sql.NullString{String: "192.0.2.65", Valid: true}, // ip_address
		6601, // image_count
		sql.NullString{String: "size-6602", Valid: true},          // image_size
		sql.NullString{String: "input-6603", Valid: true},         // image_input_size
		sql.NullString{String: "output-6604", Valid: true},        // image_output_size
		sql.NullString{String: "source-6605", Valid: true},        // image_size_source
		sql.NullString{String: `{"size-6602":6601}`, Valid: true}, // image_size_breakdown
		6701, // video_count
		sql.NullString{String: "resolution-6702", Valid: true}, // video_resolution
		sql.NullInt64{Int64: 6703, Valid: true},                // video_duration_seconds
		sql.NullString{String: "tier-7101", Valid: true},       // service_tier
		sql.NullString{String: "effective-7201", Valid: true},  // reasoning_effort
		sql.NullString{String: "requested-7301", Valid: true},  // requested_reasoning_effort
		sql.NullString{String: "/inbound/7401", Valid: true},   // inbound_endpoint
		sql.NullString{String: "/upstream/7501", Valid: true},  // upstream_endpoint
		true,                                    // cache_ttl_overridden
		true,                                    // long_context_billing_applied
		sql.NullInt64{Int64: 7601, Valid: true}, // channel_id
		sql.NullString{String: "mapping-7701", Valid: true},      // model_mapping_chain
		sql.NullString{String: "billing-tier-7801", Valid: true}, // billing_tier
		sql.NullString{String: "billing-mode-7901", Valid: true}, // billing_mode
		sql.NullFloat64{Float64: 8001.25, Valid: true},           // account_stats_cost
		sql.NullFloat64{Float64: 8101.25, Valid: true},           // kiro_credits
		sql.NullString{String: "session-8201", Valid: true},      // session_id
		true,      // native_compaction_v2
		createdAt, // created_at
	}})
	require.NoError(t, err)
	require.Equal(t, "tier-7101", *log.ServiceTier)
	require.Equal(t, "effective-7201", *log.ReasoningEffort)
	require.Equal(t, "requested-7301", *log.RequestedReasoningEffort)
	require.Equal(t, "/inbound/7401", *log.InboundEndpoint)
	require.Equal(t, "/upstream/7501", *log.UpstreamEndpoint)
	require.Equal(t, int64(7601), *log.ChannelID)
	require.Equal(t, int64(3001), log.AccountID)
	require.Equal(t, "session-8201", *log.SessionID)
	require.True(t, log.NativeCompactionV2)
	require.Equal(t, createdAt, log.CreatedAt)
}

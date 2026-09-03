//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFullFieldRoundTrip_SingleBatchBestEffortAndAdminList(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("task5-roundtrip-%s@example.com", uuid.NewString())})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-task5-roundtrip-" + uuid.NewString(), Name: "task5-roundtrip"})
	account := mustCreateAccount(t, client, &service.Account{Name: "task5-roundtrip-" + uuid.NewString()})
	createdAt := time.Date(2026, 9, 1, 3, 4, 5, 0, time.UTC)

	single := task5FullFieldUsageLog(user.ID, apiKey.ID, account.ID, "", 9101, createdAt)
	inserted, err := repo.Create(ctx, single)
	require.NoError(t, err)
	require.True(t, inserted)
	gotSingle, err := repo.GetByID(ctx, single.ID)
	require.NoError(t, err)
	assertTask5FullFieldUsageLog(t, gotSingle, account.ID, 9101, createdAt)

	listed, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, UsageLogFilters{
		UserID: user.ID, AccountID: account.ID, ChannelID: 9101,
		ServiceTier: "tier-9101", ReasoningEffort: "effective-9101", RequestedReasoningEffort: "requested-9101",
		InboundEndpoint: "/inbound/9101", UpstreamEndpoint: "/upstream/9101", NativeCompactionV2: task5BoolPtr(true),
		ExactTotal: true,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assertTask5FullFieldUsageLog(t, &listed[0], account.ID, 9101, createdAt)

	batch := task5FullFieldUsageLog(user.ID, apiKey.ID, account.ID, "task5-batch-"+uuid.NewString(), 9201, createdAt.Add(time.Second))
	batchReq := usageLogCreateRequest{log: batch, prepared: prepareUsageLogInsert(batch), resultCh: make(chan usageLogCreateResult, 1)}
	repo.flushCreateBatch(integrationDB, []usageLogCreateRequest{batchReq})
	batchResult := <-batchReq.resultCh
	require.NoError(t, batchResult.err)
	require.True(t, batchResult.inserted)
	gotBatch, err := repo.GetByRequestIDAndAPIKeyID(ctx, batch.RequestID, apiKey.ID)
	require.NoError(t, err)
	assertTask5FullFieldUsageLog(t, gotBatch, account.ID, 9201, createdAt.Add(time.Second))

	bestEffort := task5FullFieldUsageLog(user.ID, apiKey.ID, account.ID, "task5-best-effort-"+uuid.NewString(), 9301, createdAt.Add(2*time.Second))
	require.NoError(t, repo.CreateBestEffort(ctx, bestEffort))
	gotBestEffort, err := repo.GetByRequestIDAndAPIKeyID(ctx, bestEffort.RequestID, apiKey.ID)
	require.NoError(t, err)
	assertTask5FullFieldUsageLog(t, gotBestEffort, account.ID, 9301, createdAt.Add(2*time.Second))
}

func TestUsageAggregationCompleteObservabilityFilters_PostgreSQL(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("task5-aggregation-%s@example.com", uuid.NewString())})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-task5-aggregation-" + uuid.NewString(), Name: "task5-aggregation"})
	account := mustCreateAccount(t, client, &service.Account{Name: "task5-aggregation-" + uuid.NewString()})
	group := mustCreateGroup(t, client, &service.Group{Name: "task5-aggregation-" + uuid.NewString(), Status: service.StatusActive})
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	channelID := int64(9401)
	groupID := group.ID
	matching := task5FullFieldUsageLog(user.ID, apiKey.ID, account.ID, "task5-aggregate-match-"+uuid.NewString(), channelID, start.Add(time.Minute))
	matching.GroupID = &groupID
	matching.Model = "aggregate-model"
	matching.RequestedModel = "aggregate-model"
	matching.InputTokens = 1
	matching.OutputTokens = 1
	requireTask5CreateSingle(t, repo, matching)

	mutations := []func(*service.UsageLog){
		func(log *service.UsageLog) { log.ChannelID = task5Int64Ptr(9402) },
		func(log *service.UsageLog) { log.ServiceTier = task5StringPtr("tier-other") },
		func(log *service.UsageLog) { log.ReasoningEffort = task5StringPtr("effective-other") },
		func(log *service.UsageLog) { log.RequestedReasoningEffort = task5StringPtr("requested-other") },
		func(log *service.UsageLog) { log.InboundEndpoint = task5StringPtr("/inbound/other") },
		func(log *service.UsageLog) { log.UpstreamEndpoint = task5StringPtr("/upstream/other") },
		func(log *service.UsageLog) { log.NativeCompactionV2 = false },
	}
	for i, mutate := range mutations {
		decoy := *matching
		decoy.ID = 0
		decoy.RequestID = fmt.Sprintf("task5-aggregate-decoy-%d-%s", i, uuid.NewString())
		decoy.InputTokens = 100 + i
		mutate(&decoy)
		requireTask5CreateSingle(t, repo, &decoy)
	}

	filters := usagestats.UsageLogFilters{
		UserID: user.ID, AccountID: account.ID, ChannelID: channelID, GroupID: groupID, Model: "aggregate-model",
		ModelFilterSource: usagestats.ModelSourceRequested,
		ServiceTier:       "tier-9401", ReasoningEffort: "effective-9401", RequestedReasoningEffort: "requested-9401",
		InboundEndpoint: "/inbound/9401", UpstreamEndpoint: "/upstream/9401", NativeCompactionV2: task5BoolPtr(true),
	}
	trend, err := repo.GetUsageTrendWithUsageFilters(ctx, start, start.Add(time.Hour), "minute", filters)
	require.NoError(t, err)
	require.Len(t, trend, 1)
	require.Equal(t, int64(1), trend[0].Requests)

	models, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, start.Add(time.Hour), filters, usagestats.ModelSourceRequested)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, int64(1), models[0].Requests)

	groups, err := repo.GetGroupStatsWithUsageFilters(ctx, start, start.Add(time.Hour), filters)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, int64(1), groups[0].Requests)
}

func task5FullFieldUsageLog(userID, apiKeyID, accountID int64, requestID string, sentinel int64, createdAt time.Time) *service.UsageLog {
	return &service.UsageLog{
		UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, RequestID: requestID,
		Model: "model-" + fmt.Sprint(sentinel), RequestedModel: "requested-model-" + fmt.Sprint(sentinel),
		ServiceTier: task5StringPtr("tier-" + fmt.Sprint(sentinel)), ReasoningEffort: task5StringPtr("effective-" + fmt.Sprint(sentinel)),
		RequestedReasoningEffort: task5StringPtr("requested-" + fmt.Sprint(sentinel)),
		InboundEndpoint:          task5StringPtr("/inbound/" + fmt.Sprint(sentinel)), UpstreamEndpoint: task5StringPtr("/upstream/" + fmt.Sprint(sentinel)),
		ChannelID: task5Int64Ptr(sentinel), SessionID: task5StringPtr("session-" + fmt.Sprint(sentinel)), NativeCompactionV2: true,
		InputTokens: int(sentinel), OutputTokens: int(sentinel + 1), TotalCost: float64(sentinel) + 0.25,
		ActualCost: float64(sentinel) + 0.5, CreatedAt: createdAt,
	}
}

func assertTask5FullFieldUsageLog(t *testing.T, log *service.UsageLog, accountID, sentinel int64, createdAt time.Time) {
	t.Helper()
	require.Equal(t, "tier-"+fmt.Sprint(sentinel), *log.ServiceTier)
	require.Equal(t, "effective-"+fmt.Sprint(sentinel), *log.ReasoningEffort)
	require.Equal(t, "requested-"+fmt.Sprint(sentinel), *log.RequestedReasoningEffort)
	require.Equal(t, "/inbound/"+fmt.Sprint(sentinel), *log.InboundEndpoint)
	require.Equal(t, "/upstream/"+fmt.Sprint(sentinel), *log.UpstreamEndpoint)
	require.Equal(t, sentinel, *log.ChannelID)
	require.Equal(t, accountID, log.AccountID)
	require.Equal(t, "session-"+fmt.Sprint(sentinel), *log.SessionID)
	require.True(t, log.NativeCompactionV2)
	require.Equal(t, createdAt, log.CreatedAt.UTC())
}

func requireTask5CreateSingle(t *testing.T, repo *usageLogRepository, log *service.UsageLog) {
	t.Helper()
	inserted, err := repo.createSingle(t.Context(), integrationDB, log)
	require.NoError(t, err)
	require.True(t, inserted)
}

func task5StringPtr(value string) *string { return &value }
func task5Int64Ptr(value int64) *int64    { return &value }
func task5BoolPtr(value bool) *bool       { return &value }

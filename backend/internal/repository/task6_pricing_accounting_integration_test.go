//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TestTask6PricingAccountingPersistsParityAndDeduplicatesReplay exercises the
// real file-backed pricing parser, billing calculator, PostgreSQL billing
// transaction/dedup claim, and usage-log persistence. This schema deliberately
// has no amount column in usage_billing_dedup; usage_logs.total_cost is the
// authoritative persisted billable amount against which account_stats_cost is
// reconciled.
func TestTask6PricingAccountingPersistsParityAndDeduplicatesReplay(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)

	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "model_pricing.json"), []byte(`{
		"task6-pg-model": {
			"input_cost_per_token": 0.000001,
			"input_cost_per_image_token": 0.000005,
			"output_cost_per_token": 0.000002,
			"cache_creation_input_token_cost": 0.000003,
			"cache_creation_input_token_cost_above_1hr": 0.000004,
			"cache_read_input_token_cost": 0.0000005,
			"output_cost_per_image_token": 0.000006,
			"supports_prompt_caching": true,
			"litellm_provider": "anthropic",
			"mode": "chat"
		}
	}`), 0o600))
	cfg := &config.Config{Pricing: config.PricingConfig{DataDir: dataDir}}
	pricingService := service.NewPricingService(cfg, nil)
	require.NoError(t, pricingService.Initialize())
	t.Cleanup(pricingService.Stop)
	billingService := service.NewBillingService(cfg, pricingService)

	user := mustCreateUser(t, client, &service.User{
		Email:   fmt.Sprintf("task6-pricing-accounting-%s@example.com", uuid.NewString()),
		Balance: 100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-task6-pricing-accounting-" + uuid.NewString(),
		Name:   "task6-pricing-accounting",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "task6-pricing-accounting-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
	})
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, err := integrationDB.ExecContext(cleanupCtx, "DELETE FROM usage_logs WHERE api_key_id = $1", apiKey.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, "DELETE FROM usage_billing_dedup WHERE api_key_id = $1", apiKey.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, "DELETE FROM usage_billing_dedup_archive WHERE api_key_id = $1", apiKey.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, "DELETE FROM api_keys WHERE id = $1", apiKey.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, "DELETE FROM accounts WHERE id = $1", account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
	})

	billingRepo := NewUsageBillingRepository(client, integrationDB)
	usageRepo := NewUsageLogRepository(client, integrationDB)
	startedAt := time.Now().UTC().Add(-time.Second)

	cases := []struct {
		name   string
		tokens service.UsageTokens
	}{
		{name: "image-input-output", tokens: service.UsageTokens{InputTokens: 11, ImageInputTokens: 7, OutputTokens: 13, ImageOutputTokens: 5}},
		{name: "cache-5m", tokens: service.UsageTokens{InputTokens: 17, OutputTokens: 19, CacheCreationTokens: 23, CacheCreation5mTokens: 23, CacheReadTokens: 29}},
		{name: "cache-1h", tokens: service.UsageTokens{InputTokens: 31, OutputTokens: 37, CacheCreationTokens: 41, CacheCreation1hTokens: 41, CacheReadTokens: 43}},
	}

	var expectedTotal float64
	for _, tc := range cases {
		requestID := "task6-pg-" + tc.name + "-" + uuid.NewString()
		customerCost, err := billingService.CalculateCost("task6-pg-model", tc.tokens, 1)
		require.NoError(t, err)
		require.NotNil(t, customerCost)
		// The production account-stats model-file path delegates to the same
		// service-tier-aware billing calculator with multiplier 1.
		statsCost, err := billingService.CalculateCostWithServiceTier("task6-pg-model", tc.tokens, 1, "")
		require.NoError(t, err)
		require.NotNil(t, statsCost)
		require.InDelta(t, customerCost.TotalCost, statsCost.TotalCost, 1e-12)

		cmd := &service.UsageBillingCommand{
			RequestID:           requestID,
			RequestPayloadHash:  "task6-payload-" + tc.name,
			APIKeyID:            apiKey.ID,
			UserID:              user.ID,
			AccountID:           account.ID,
			AccountPlatform:     account.Platform,
			AccountType:         account.Type,
			Model:               "task6-pg-model",
			BillingType:         service.BillingTypeBalance,
			InputTokens:         tc.tokens.InputTokens + tc.tokens.ImageInputTokens,
			OutputTokens:        tc.tokens.OutputTokens + tc.tokens.ImageOutputTokens,
			CacheCreationTokens: tc.tokens.CacheCreationTokens,
			CacheReadTokens:     tc.tokens.CacheReadTokens,
			BalanceCost:         customerCost.ActualCost,
		}
		applied, err := billingRepo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.NotNil(t, applied)
		require.True(t, applied.Applied)

		accountStatsCost := statsCost.TotalCost
		inserted, err := usageRepo.Create(ctx, &service.UsageLog{
			UserID:                user.ID,
			APIKeyID:              apiKey.ID,
			AccountID:             account.ID,
			RequestID:             requestID,
			Model:                 "task6-pg-model",
			InputTokens:           tc.tokens.InputTokens,
			OutputTokens:          tc.tokens.OutputTokens,
			CacheCreationTokens:   tc.tokens.CacheCreationTokens,
			CacheReadTokens:       tc.tokens.CacheReadTokens,
			CacheCreation5mTokens: tc.tokens.CacheCreation5mTokens,
			CacheCreation1hTokens: tc.tokens.CacheCreation1hTokens,
			ImageInputTokens:      tc.tokens.ImageInputTokens,
			ImageInputCost:        customerCost.ImageInputCost,
			ImageOutputTokens:     tc.tokens.ImageOutputTokens,
			ImageOutputCost:       customerCost.ImageOutputCost,
			InputCost:             customerCost.InputCost,
			OutputCost:            customerCost.OutputCost,
			CacheCreationCost:     customerCost.CacheCreationCost,
			CacheReadCost:         customerCost.CacheReadCost,
			TotalCost:             customerCost.TotalCost,
			ActualCost:            customerCost.ActualCost,
			RateMultiplier:        1,
			AccountStatsCost:      &accountStatsCost,
			BillingType:           service.BillingTypeBalance,
			RequestType:           service.RequestTypeSync,
			CreatedAt:             time.Now().UTC(),
		})
		require.NoError(t, err)
		require.True(t, inserted)
		expectedTotal += customerCost.TotalCost
	}

	// A retry/failover replay carries the same billing identity and fingerprint.
	// Only an Applied claim is allowed to create a usage row.
	replayID := "task6-pg-replay-" + uuid.NewString()
	replayTokens := service.UsageTokens{InputTokens: 47, OutputTokens: 53, CacheCreationTokens: 59, CacheCreation1hTokens: 59}
	replayCost, err := billingService.CalculateCost("task6-pg-model", replayTokens, 1)
	require.NoError(t, err)
	require.NotNil(t, replayCost)
	replayCmd := service.UsageBillingCommand{
		RequestID: replayID, RequestPayloadHash: "same-logical-request", APIKeyID: apiKey.ID,
		UserID: user.ID, AccountID: account.ID, AccountPlatform: account.Platform,
		AccountType: account.Type, Model: "task6-pg-model", BillingType: service.BillingTypeBalance,
		InputTokens: replayTokens.InputTokens, OutputTokens: replayTokens.OutputTokens,
		CacheCreationTokens: replayTokens.CacheCreationTokens, BalanceCost: replayCost.ActualCost,
	}
	for attempt := 0; attempt < 2; attempt++ {
		cmd := replayCmd
		result, applyErr := billingRepo.Apply(ctx, &cmd)
		require.NoError(t, applyErr)
		require.NotNil(t, result)
		if !result.Applied {
			continue
		}
		accountStatsCost := replayCost.TotalCost
		inserted, createErr := usageRepo.Create(ctx, &service.UsageLog{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			RequestID: replayID, Model: "task6-pg-model",
			InputTokens: replayTokens.InputTokens, OutputTokens: replayTokens.OutputTokens,
			CacheCreationTokens:   replayTokens.CacheCreationTokens,
			CacheCreation1hTokens: replayTokens.CacheCreation1hTokens,
			InputCost:             replayCost.InputCost, OutputCost: replayCost.OutputCost,
			CacheCreationCost: replayCost.CacheCreationCost,
			TotalCost:         replayCost.TotalCost, ActualCost: replayCost.ActualCost,
			RateMultiplier: 1, AccountStatsCost: &accountStatsCost,
			BillingType: service.BillingTypeBalance, RequestType: service.RequestTypeSync,
			CreatedAt: time.Now().UTC(),
		})
		require.NoError(t, createErr)
		require.True(t, inserted)
		expectedTotal += replayCost.TotalCost
	}

	endedAt := time.Now().UTC().Add(time.Second)
	var rowCount int
	var persistedBillableTotal, persistedAccountStatsTotal float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(total_cost), 0), COALESCE(SUM(account_stats_cost), 0)
		FROM usage_logs
		WHERE user_id = $1 AND api_key_id = $2 AND account_id = $3
		  AND created_at >= $4 AND created_at < $5
		  AND request_id LIKE 'task6-pg-%'
	`, user.ID, apiKey.ID, account.ID, startedAt, endedAt).Scan(
		&rowCount, &persistedBillableTotal, &persistedAccountStatsTotal,
	))
	require.Equal(t, len(cases)+1, rowCount)
	require.InDelta(t, expectedTotal, persistedBillableTotal, 1e-8)
	require.InDelta(t, persistedBillableTotal, persistedAccountStatsTotal, 1e-8)

	var replayRows, replayClaims int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM usage_logs WHERE request_id = $1 AND api_key_id = $2", replayID, apiKey.ID,
	).Scan(&replayRows))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", replayID, apiKey.ID,
	).Scan(&replayClaims))
	require.Equal(t, 1, replayRows)
	require.Equal(t, 1, replayClaims)
}

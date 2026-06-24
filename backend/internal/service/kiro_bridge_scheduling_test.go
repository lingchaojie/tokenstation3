package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type kiroBridgeAccountRepo struct {
	AccountRepository
	accounts []Account
}

type kiroBridgeGroupRepo struct {
	GroupRepository
	group *Group
}

func (r kiroBridgeGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	if r.group != nil && r.group.ID == id {
		return r.group, nil
	}
	return nil, ErrGroupNotFound
}

func (r kiroBridgeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return r.GetByID(ctx, id)
}

func (r kiroBridgeAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, ErrAccountNotFound
}

func (r kiroBridgeAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	return r.listByPlatforms(&groupID, []string{platform}), nil
}

func (r kiroBridgeAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]Account, error) {
	return r.listByPlatforms(nil, []string{platform}), nil
}

func (r kiroBridgeAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]Account, error) {
	return r.listByPlatforms(nil, []string{platform}), nil
}

func (r kiroBridgeAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, platforms []string) ([]Account, error) {
	return r.listByPlatforms(&groupID, platforms), nil
}

func (r kiroBridgeAccountRepo) ListSchedulableByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	return r.listByPlatforms(nil, platforms), nil
}

func (r kiroBridgeAccountRepo) ListSchedulableUngroupedByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	return r.listByPlatforms(nil, platforms), nil
}

func (r kiroBridgeAccountRepo) listByPlatforms(groupID *int64, platforms []string) []Account {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	out := make([]Account, 0, len(r.accounts))
	for _, acc := range r.accounts {
		if _, ok := platformSet[acc.Platform]; !ok {
			continue
		}
		if !acc.IsSchedulable() {
			continue
		}
		if groupID != nil && !openAIStickyAccountMatchesGroup(&acc, groupID) {
			continue
		}
		out = append(out, acc)
	}
	return out
}

func kiroBridgeGroup(groupID int64) []AccountGroup {
	return []AccountGroup{{GroupID: groupID}}
}

func TestOpenAICompatibleScheduling_OpenAIGroupIncludesKiroChatCandidates(t *testing.T) {
	groupID := int64(1201)
	openAI := Account{
		ID:            1,
		Platform:      PlatformOpenAI,
		Type:          AccountTypeAPIKey,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      2,
		AccountGroups: kiroBridgeGroup(groupID),
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
		},
	}
	kiro := Account{
		ID:            2,
		Platform:      PlatformKiro,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      1,
		AccountGroups: kiroBridgeGroup(groupID),
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo: kiroBridgeAccountRepo{accounts: []Account{openAI, kiro}},
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	ctx := WithOpenAICompatiblePlatform(context.Background(), PlatformOpenAI)
	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gpt-5.1", nil)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, PlatformKiro, account.Platform)
	require.Equal(t, int64(2), account.ID, "Kiro should compete with OpenAI accounts by priority")
	require.True(t, isOpenAIAccountEligibleForRequest(ctx, account, "gpt-5.1", false, OpenAIEndpointCapabilityChatCompletions))
	require.False(t, isOpenAIAccountEligibleForRequest(ctx, account, "gpt-5.1", false, OpenAIEndpointCapabilityEmbeddings))
}

func TestGatewayScheduling_AnthropicGroupIncludesKiroForClaudeOnly(t *testing.T) {
	groupID := int64(2201)
	anthropic := Account{
		ID:            10,
		Platform:      PlatformAnthropic,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      2,
		AccountGroups: kiroBridgeGroup(groupID),
		Credentials: map[string]any{
			"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
	}
	kiro := Account{
		ID:            11,
		Platform:      PlatformKiro,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      1,
		AccountGroups: kiroBridgeGroup(groupID),
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-sonnet-4-5": "claude-sonnet-4.5",
				"deepseek-chat":     "deepseek-v3",
			},
		},
	}
	svc := &GatewayService{
		accountRepo: kiroBridgeAccountRepo{accounts: []Account{anthropic, kiro}},
		groupRepo:   kiroBridgeGroupRepo{group: &Group{ID: groupID, Platform: PlatformAnthropic}},
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	claudeAccount, err := svc.selectAccountWithMixedScheduling(context.Background(), &groupID, "", "claude-sonnet-4-5", nil, PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, claudeAccount)
	require.Equal(t, PlatformKiro, claudeAccount.Platform, "Kiro should compete in Anthropic groups for Claude-family models")

	nonClaudeAccount, err := svc.selectAccountWithMixedScheduling(context.Background(), &groupID, "", "deepseek-chat", nil, PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, nonClaudeAccount)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestResolveKiroModelID_PreservesExplicitNonClaudeUpstreamModels(t *testing.T) {
	require.Equal(t, "deepseek-v3", ResolveKiroModelID("deepseek-v3"))
	require.Equal(t, "kimi-k2", ResolveKiroModelID("kimi-k2"))
	require.Equal(t, "glm-4.6", ResolveKiroModelID("glm-4.6"))
}

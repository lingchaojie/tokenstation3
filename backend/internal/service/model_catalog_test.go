package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelCatalogAccountRepoStub struct {
	AccountRepository
	accounts []Account
	err      error
	calls    int
}

func (s *modelCatalogAccountRepoStub) ListModelAvailabilityCandidates(
	_ context.Context,
	_ *int64,
	_ []string,
	_ bool,
) ([]Account, error) {
	s.calls++
	return append([]Account(nil), s.accounts...), s.err
}

func TestGatewayService_GetConfiguredModelCatalog_AggregatesConcretePublicKeys(t *testing.T) {
	repo := &modelCatalogAccountRepoStub{accounts: []Account{
		{
			Platform: PlatformAnthropic,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"claude-public": "claude-upstream",
				"claude-*":      "claude-wildcard-upstream",
				"   ":           "blank-upstream",
			}},
		},
		{
			Platform: PlatformAnthropic,
			Credentials: map[string]any{"model_mapping": map[string]any{
				" claude-public ": "duplicate-upstream",
			}},
		},
		{
			Platform: PlatformKiro,
			Extra:    map[string]any{"mixed_scheduling": true},
			Credentials: map[string]any{"model_mapping": map[string]any{
				"kiro-public": "kiro-upstream",
			}},
		},
		{
			Platform: PlatformKiro,
			Extra:    map[string]any{"mixed_scheduling": false},
			Credentials: map[string]any{"model_mapping": map[string]any{
				"kiro-disabled": "kiro-disabled-upstream",
			}},
		},
		{
			Platform: PlatformAntigravity,
			Extra:    map[string]any{"mixed_scheduling": false},
			Credentials: map[string]any{"model_mapping": map[string]any{
				"antigravity-disabled": "antigravity-disabled-upstream",
			}},
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

	require.NoError(t, err)
	require.Equal(t, []string{"claude-public", "kiro-public"}, models)
	require.NotContains(t, models, "claude-upstream")
	require.NotContains(t, models, "claude-*")
	require.Equal(t, 1, repo.calls)
}

func TestGatewayService_GetConfiguredModelCatalog_OpenAIPassthroughContributesNoStaleMapping(t *testing.T) {
	repo := &modelCatalogAccountRepoStub{accounts: []Account{
		{
			Platform: PlatformOpenAI,
			Extra:    map[string]any{"openai_passthrough": true},
			Credentials: map[string]any{"model_mapping": map[string]any{
				"stale-public": "stale-upstream",
			}},
		},
		{
			Platform: PlatformOpenAI,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"gpt-public": "gpt-upstream",
			}},
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 6, Platform: PlatformOpenAI})

	require.NoError(t, err)
	require.Equal(t, []string{"gpt-public"}, models)
	require.NotContains(t, models, "stale-public")
}

func TestGatewayService_GetConfiguredModelCatalog_EmptyCatalogIsValid(t *testing.T) {
	repo := &modelCatalogAccountRepoStub{}
	svc := &GatewayService{accountRepo: repo}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

	require.NoError(t, err)
	require.Equal(t, []string{}, models)
}

func TestGatewayService_GetConfiguredModelCatalog_CustomListOnlyIntersectsAvailableModels(t *testing.T) {
	repo := &modelCatalogAccountRepoStub{accounts: []Account{
		{
			Platform: PlatformAnthropic,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"claude-a": "upstream-a",
				"claude-b": "upstream-b",
			}},
		},
	}}
	svc := &GatewayService{accountRepo: repo}
	group := &Group{
		ID:       3,
		Platform: PlatformAnthropic,
		ModelsListConfig: GroupModelsListConfig{
			Enabled: true,
			Models:  []string{" unavailable ", " claude-b ", "claude-b", ""},
		},
	}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), group)

	require.NoError(t, err)
	require.Equal(t, []string{"claude-b"}, models)
}

func TestGatewayService_GetConfiguredModelCatalog_PropagatesRepositoryError(t *testing.T) {
	repoErr := errors.New("database unavailable")
	repo := &modelCatalogAccountRepoStub{err: repoErr}
	svc := &GatewayService{accountRepo: repo}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

	require.Nil(t, models)
	require.ErrorIs(t, err, repoErr)
}

type modelCatalogCacheStub struct {
	models []string
	getErr error
	setErr error

	getCalls    int
	setCalls    int
	setGroupID  int64
	setPlatform string
	setModels   []string
	setTTL      time.Duration
}

func (s *modelCatalogCacheStub) GetModelCatalog(context.Context, int64, string) ([]string, error) {
	s.getCalls++
	return append([]string(nil), s.models...), s.getErr
}

func (s *modelCatalogCacheStub) SetModelCatalog(
	_ context.Context,
	groupID int64,
	platform string,
	models []string,
	ttl time.Duration,
) error {
	s.setCalls++
	s.setGroupID = groupID
	s.setPlatform = platform
	s.setModels = append([]string(nil), models...)
	s.setTTL = ttl
	return s.setErr
}

func TestGatewayService_GetConfiguredModelCatalog_UsesCacheHitIncludingEmpty(t *testing.T) {
	for _, tt := range []struct {
		name string
		got  []string
	}{
		{name: "models", got: []string{"claude-cached"}},
		{name: "empty", got: []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &modelCatalogAccountRepoStub{err: errors.New("repository must not be called")}
			cache := &modelCatalogCacheStub{models: tt.got}
			svc := &GatewayService{accountRepo: repo, modelCatalogCache: cache}

			models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

			require.NoError(t, err)
			require.Equal(t, tt.got, models)
			require.Equal(t, 1, cache.getCalls)
			require.Zero(t, cache.setCalls)
			require.Zero(t, repo.calls)
		})
	}
}

func TestGatewayService_GetConfiguredModelCatalog_CachesMissForTenMinutes(t *testing.T) {
	repo := &modelCatalogAccountRepoStub{accounts: []Account{{
		Platform: PlatformAnthropic,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"claude-configured": "claude-upstream",
		}},
	}}}
	cache := &modelCatalogCacheStub{getErr: ErrModelCatalogCacheMiss}
	svc := &GatewayService{accountRepo: repo, modelCatalogCache: cache}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: " anthropic "})

	require.NoError(t, err)
	require.Equal(t, []string{"claude-configured"}, models)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, 1, cache.getCalls)
	require.Equal(t, 1, cache.setCalls)
	require.Equal(t, int64(3), cache.setGroupID)
	require.Equal(t, PlatformAnthropic, cache.setPlatform)
	require.Equal(t, []string{"claude-configured"}, cache.setModels)
	require.Equal(t, 10*time.Minute, cache.setTTL)
}

func TestGatewayService_GetConfiguredModelCatalog_FallsBackFromCacheErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		getErr error
		setErr error
	}{
		{name: "read error", getErr: errors.New("redis read failed")},
		{name: "write error", getErr: ErrModelCatalogCacheMiss, setErr: errors.New("redis write failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &modelCatalogAccountRepoStub{accounts: []Account{{
				Platform: PlatformAnthropic,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"claude-configured": "claude-upstream",
				}},
			}}}
			cache := &modelCatalogCacheStub{getErr: tt.getErr, setErr: tt.setErr}
			svc := &GatewayService{accountRepo: repo, modelCatalogCache: cache}

			models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

			require.NoError(t, err)
			require.Equal(t, []string{"claude-configured"}, models)
			require.Equal(t, 1, repo.calls)
			require.Equal(t, 1, cache.getCalls)
			require.Equal(t, 1, cache.setCalls)
		})
	}
}

func TestGatewayService_GetConfiguredModelCatalog_CachesMissPropagatesRepositoryError(t *testing.T) {
	repoErr := errors.New("database unavailable")
	repo := &modelCatalogAccountRepoStub{err: repoErr}
	cache := &modelCatalogCacheStub{getErr: ErrModelCatalogCacheMiss}
	svc := &GatewayService{accountRepo: repo, modelCatalogCache: cache}

	models, err := svc.GetConfiguredModelCatalog(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic})

	require.Nil(t, models)
	require.ErrorIs(t, err, repoErr)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, 1, cache.getCalls)
	require.Zero(t, cache.setCalls)
}

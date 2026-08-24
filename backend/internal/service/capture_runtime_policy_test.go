package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultCaptureRuntimePolicyKeepsOpenAIOff(t *testing.T) {
	got := DefaultCaptureRuntimePolicy()
	require.Equal(t, 1, got.Version)
	require.False(t, got.Enabled)
	require.True(t, got.Platforms.Anthropic)
	require.True(t, got.Platforms.Kiro)
	require.False(t, got.Platforms.OpenAI)
	require.True(t, got.Platforms.Gemini)
	require.True(t, got.Platforms.Antigravity)
	require.True(t, got.Platforms.Grok)
	require.True(t, got.Platforms.Cursor)
	require.True(t, got.Outcomes.Success)
	require.True(t, got.Outcomes.TerminalError)
	require.True(t, got.Content.RawRequest)
	require.True(t, got.Content.RawResponse)
	require.True(t, got.Content.RequestHeaders)
	require.True(t, got.Content.ResponseHeaders)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, got.ModelAllowlists.Anthropic)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, got.ModelAllowlists.Kiro)
}

func TestCursorCaptureRuntimePolicyLegacyStoredValueKeepsCursorOff(t *testing.T) {
	policy, err := DecodeCaptureRuntimePolicy([]byte(`{
      "version":1,"enabled":true,
      "platforms":{"anthropic":true,"kiro":true,"openai":false,"gemini":true,"antigravity":true,"grok":true},
      "outcomes":{"success":true,"terminal_error":true},
      "content":{"raw_request":true,"raw_response":true,"request_headers":true,"response_headers":true},
      "group_ids":[],"user_ids":[]
    }`))
	require.NoError(t, err)
	require.False(t, policy.Platforms.Cursor, "an already-stored version-one policy must not silently enable Cursor")

	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	_, ok := compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 1, nil)
	require.False(t, ok)
}

func TestCursorCaptureRuntimePolicyIndependentSwitchKeepsAllFilters(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = false
	policy.Platforms.Anthropic = false
	policy.UserIDs = []int64{9}
	policy.GroupIDs = []int64{7}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	group := int64(7)
	_, ok := compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 9, &group)
	require.True(t, ok, "Cursor must not alias disabled OpenAI or Anthropic switches")
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 8, &group)
	require.False(t, ok, "the existing user filter remains authoritative")
	wrongGroup := int64(8)
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 9, &wrongGroup)
	require.False(t, ok, "the existing group filter remains authoritative")

	policy.Content.RawRequest = false
	policy.Content.ResponseHeaders = false
	compiled, err = CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	content, ok := compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 9, &group)
	require.True(t, ok)
	require.False(t, content.RawRequest)
	require.False(t, content.ResponseHeaders)

	policy.Outcomes.Success = false
	compiled, err = CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 9, &group)
	require.False(t, ok, "the existing outcome filter remains authoritative")
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeTerminalError, 9, &group)
	require.True(t, ok)

	policy.Enabled = false
	compiled, err = CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeTerminalError, 9, &group)
	require.False(t, ok, "the master switch remains authoritative")

	policy.Enabled = true
	policy.Outcomes.Success = true
	policy.Platforms.Cursor = false
	compiled, err = CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	_, ok = compiled.Decide(PlatformCursor, CaptureOutcomeSuccess, 9, &group)
	require.False(t, ok, "the independent Cursor switch is authoritative")
}

func TestNormalizeCaptureRuntimePolicySortsAndDeduplicatesIDs(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.GroupIDs = []int64{9, 2, 9, 3}
	policy.UserIDs = []int64{8, 1, 8}

	got, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3, 9}, got.GroupIDs)
	require.Equal(t, []int64{1, 8}, got.UserIDs)
}

func TestNormalizeCaptureRuntimePolicySortsAndDeduplicatesModelAllowlists(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.ModelAllowlists = CaptureModelAllowlistPolicy{
		Anthropic: []string{" Claude-Fable-5 ", "claude-opus-5", "claude-fable-5", ""},
		Kiro:      []string{"claude-opus-5", " CLAUDE-FABLE-5 "},
	}

	got, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, got.ModelAllowlists.Anthropic)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, got.ModelAllowlists.Kiro)
}

func TestNormalizeCaptureRuntimePolicyRejectsInvalidVersionAndIDs(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Version = 2
	_, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	require.ErrorContains(t, err, "version")

	policy = DefaultCaptureRuntimePolicy()
	policy.UserIDs = []int64{0}
	_, err = ValidateAndNormalizeCaptureRuntimePolicy(policy)
	require.ErrorContains(t, err, "user_ids")
}

func TestDecodeCaptureRuntimePolicyRejectsUnknownFields(t *testing.T) {
	_, err := DecodeCaptureRuntimePolicy([]byte(`{
      "version":1,"enabled":false,
      "platforms":{"anthropic":true,"kiro":true,"openai":false},
      "outcomes":{"success":true,"terminal_error":true},
      "content":{"raw_request":true,"raw_response":true,"request_headers":true,"response_headers":true},
      "group_ids":[],"user_ids":[],"unexpected":true
    }`))
	require.Error(t, err)
}

func TestDecodeCaptureRuntimePolicyAppliesModelDefaultsToLegacySettings(t *testing.T) {
	policy, err := DecodeCaptureRuntimePolicy([]byte(`{
      "version":1,"enabled":true,
      "platforms":{"anthropic":true,"kiro":true,"openai":false,"gemini":true,"antigravity":true,"grok":true},
      "outcomes":{"success":true,"terminal_error":true},
      "content":{"raw_request":true,"raw_response":true,"request_headers":true,"response_headers":true},
      "group_ids":[],"user_ids":[]
    }`))
	require.NoError(t, err)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, policy.ModelAllowlists.Anthropic)
	require.Equal(t, []string{"claude-fable-5", "claude-opus-5"}, policy.ModelAllowlists.Kiro)
}

func TestCompiledCapturePolicyRequiresBothConfiguredFilters(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.GroupIDs = []int64{7}
	policy.UserIDs = []int64{9}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	group := int64(7)
	require.True(t, compiled.Match("openai", CaptureOutcomeSuccess, 9, &group))
	require.False(t, compiled.Match("openai", CaptureOutcomeSuccess, 8, &group))
	otherGroup := int64(6)
	require.False(t, compiled.Match("openai", CaptureOutcomeSuccess, 9, &otherGroup))
	require.False(t, compiled.Match("openai", CaptureOutcomeSuccess, 9, nil))
	require.False(t, compiled.Match("unknown", CaptureOutcomeSuccess, 9, &group))
}

func TestCompiledCapturePolicyMatchesOutcomeAndReturnsContentPolicy(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.Outcomes.TerminalError = false
	policy.Content.RawResponse = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	content, ok := compiled.Decide("openai", CaptureOutcomeSuccess, 1, nil)
	require.True(t, ok)
	require.False(t, content.RawResponse)
	_, ok = compiled.Decide("openai", CaptureOutcomeTerminalError, 1, nil)
	require.False(t, ok)
}

func TestCompiledCapturePolicyClientDisconnectIgnoresOutcomeTogglesOnly(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	content, ok := compiled.DecideForModel(
		PlatformAnthropic,
		"claude-opus-5",
		captureOutcomeClientDisconnect,
		9,
		nil,
	)
	require.True(t, ok)
	require.Equal(t, policy.Content, content)

	otherGroup := int64(8)
	tests := []struct {
		name      string
		configure func(*CaptureRuntimePolicy)
		platform  string
		model     string
		userID    int64
		groupID   *int64
	}{
		{
			name:     "platform filter",
			platform: PlatformOpenAI,
			model:    "claude-opus-5",
			userID:   9,
		},
		{
			name:     "model allowlist",
			platform: PlatformAnthropic,
			model:    "claude-haiku-4-5-20251001",
			userID:   9,
		},
		{
			name: "user filter",
			configure: func(policy *CaptureRuntimePolicy) {
				policy.UserIDs = []int64{10}
			},
			platform: PlatformAnthropic,
			model:    "claude-opus-5",
			userID:   9,
		},
		{
			name: "group filter",
			configure: func(policy *CaptureRuntimePolicy) {
				policy.GroupIDs = []int64{7}
			},
			platform: PlatformAnthropic,
			model:    "claude-opus-5",
			userID:   9,
			groupID:  &otherGroup,
		},
		{
			name: "master policy",
			configure: func(policy *CaptureRuntimePolicy) {
				policy.Enabled = false
			},
			platform: PlatformAnthropic,
			model:    "claude-opus-5",
			userID:   9,
		},
		{
			name:     "unknown platform",
			platform: "unknown",
			model:    "claude-opus-5",
			userID:   9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPolicy := policy
			if tt.configure != nil {
				tt.configure(&testPolicy)
			}
			compiled, err := CompileCaptureRuntimePolicy(testPolicy)
			require.NoError(t, err)

			_, ok := compiled.DecideForModel(tt.platform, tt.model, captureOutcomeClientDisconnect, tt.userID, tt.groupID)
			require.False(t, ok)
		})
	}
}

func TestCompiledCapturePolicyAppliesModelAllowlistsOnlyToConfiguredPlatforms(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.ModelAllowlists = CaptureModelAllowlistPolicy{
		Anthropic: []string{"claude-opus-5", "claude-fable-5"},
		Kiro:      []string{"claude-opus-5", "claude-fable-5"},
	}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	for _, platform := range []string{"anthropic", "kiro"} {
		t.Run(platform, func(t *testing.T) {
			_, ok := compiled.DecideForModel(platform, " CLAUDE-OPUS-5 ", CaptureOutcomeSuccess, 1, nil)
			require.True(t, ok)
			_, ok = compiled.DecideForModel(platform, "claude-haiku-4-5-20251001", CaptureOutcomeSuccess, 1, nil)
			require.False(t, ok)
		})
	}

	_, ok := compiled.DecideForModel("openai", "gpt-5.6-sol", CaptureOutcomeSuccess, 1, nil)
	require.True(t, ok)
}

func TestCompiledCapturePolicyMatchesEveryLocallySupportedPlatform(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	for _, platform := range []string{"anthropic", "kiro", "openai", "gemini", "antigravity", "grok", "cursor"} {
		t.Run(platform, func(t *testing.T) {
			_, ok := compiled.Decide(platform, CaptureOutcomeSuccess, 1, nil)
			require.True(t, ok)
		})
	}
}

type capturePolicyRepoStub struct {
	mu         sync.Mutex
	value      string
	getErr     error
	getCalls   int
	setCalls   int
	getStarted chan struct{}
	getRelease chan struct{}
	startOnce  sync.Once
}

func (r *capturePolicyRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *capturePolicyRepoStub) GetValue(ctx context.Context, _ string) (string, error) {
	r.mu.Lock()
	r.getCalls++
	value := r.value
	getErr := r.getErr
	started := r.getStarted
	release := r.getRelease
	r.mu.Unlock()
	if started != nil {
		r.startOnce.Do(func() { close(started) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if getErr != nil {
		return "", getErr
	}
	if value == "" {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *capturePolicyRepoStub) Set(_ context.Context, _ string, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCalls++
	r.value = value
	return nil
}

func (r *capturePolicyRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (r *capturePolicyRepoStub) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *capturePolicyRepoStub) GetAll(context.Context) (map[string]string, error)    { return nil, nil }
func (r *capturePolicyRepoStub) Delete(context.Context, string) error                 { return nil }

func (r *capturePolicyRepoStub) calls() (gets, sets int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getCalls, r.setCalls
}

func TestCaptureRuntimePolicyMissingSettingUsesDefault(t *testing.T) {
	repo := &capturePolicyRepoStub{}
	svc := NewSettingService(repo, nil)

	got, err := svc.GetCaptureRuntimePolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultCaptureRuntimePolicy(), got)
}

func TestCaptureRuntimePolicyDBFailureIsCachedFailClosed(t *testing.T) {
	repo := &capturePolicyRepoStub{getErr: errors.New("database unavailable")}
	svc := NewSettingService(repo, nil)

	first := svc.GetCompiledCaptureRuntimePolicy(context.Background())
	second := svc.GetCompiledCaptureRuntimePolicy(context.Background())
	require.False(t, first.Enabled())
	require.False(t, second.Enabled())
	gets, _ := repo.calls()
	require.Equal(t, 1, gets)
}

func TestCaptureRuntimePolicyConcurrentMissUsesSingleflight(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	encoded, err := json.Marshal(policy)
	require.NoError(t, err)
	repo := &capturePolicyRepoStub{value: string(encoded)}
	svc := NewSettingService(repo, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.GetCaptureRuntimePolicy(context.Background())
		}()
	}
	wg.Wait()
	gets, _ := repo.calls()
	require.Equal(t, 1, gets)
}

func TestCaptureRuntimePolicySaveRefreshesCacheImmediately(t *testing.T) {
	repo := &capturePolicyRepoStub{}
	svc := NewSettingService(repo, nil)
	_, err := svc.GetCaptureRuntimePolicy(context.Background())
	require.NoError(t, err)

	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	got, err := svc.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)
	require.True(t, got.Enabled)

	compiled := svc.GetCompiledCaptureRuntimePolicy(context.Background())
	require.True(t, compiled.Enabled())
	gets, sets := repo.calls()
	require.Equal(t, 1, gets)
	require.Equal(t, 1, sets)
}

func TestCaptureRuntimePolicyCorruptStoredJSONFailsClosed(t *testing.T) {
	repo := &capturePolicyRepoStub{value: `{"version":1,"enabled":true,"unexpected":true}`}
	svc := NewSettingService(repo, nil)

	_, err := svc.GetCaptureRuntimePolicy(context.Background())
	require.Error(t, err)
	require.False(t, svc.GetCompiledCaptureRuntimePolicy(context.Background()).Enabled())
}

func TestCaptureRuntimePolicyBackgroundRefreshCannotOverwriteNewerAdminSave(t *testing.T) {
	oldPolicy := DefaultCaptureRuntimePolicy()
	oldEncoded, err := json.Marshal(oldPolicy)
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	repo := &capturePolicyRepoStub{value: string(oldEncoded), getStarted: started, getRelease: release}
	svc := NewSettingService(repo, nil)

	_ = svc.GetCompiledCaptureRuntimePolicyHot()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}

	newPolicy := DefaultCaptureRuntimePolicy()
	newPolicy.Enabled = true
	newPolicy.Platforms.OpenAI = true
	_, err = svc.UpdateCaptureRuntimePolicy(context.Background(), newPolicy)
	require.NoError(t, err)
	close(release)
	require.Eventually(t, func() bool {
		return !svc.captureRuntimePolicyRefreshing.Load()
	}, time.Second, time.Millisecond)

	got := svc.GetCompiledCaptureRuntimePolicy(context.Background())
	require.True(t, got.Enabled())
	content, ok := got.Decide(PlatformOpenAI, CaptureOutcomeSuccess, 1, nil)
	require.True(t, ok)
	require.Equal(t, newPolicy.Content, content)
}

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
	require.True(t, got.Outcomes.Success)
	require.True(t, got.Outcomes.TerminalError)
	require.True(t, got.Content.RawRequest)
	require.True(t, got.Content.RawResponse)
	require.True(t, got.Content.RequestHeaders)
	require.True(t, got.Content.ResponseHeaders)
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

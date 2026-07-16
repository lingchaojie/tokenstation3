package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

type stubGroupResolver struct{ ids map[string]int64 }

func (s stubGroupResolver) GetDefaultAPIKeyGroupID(_ context.Context, keyType string) (*int64, error) {
	if id, ok := s.ids[NormalizeAPIKeyType(keyType)]; ok {
		return &id, nil
	}
	return nil, nil
}

type stubAccountLister struct{ byGroup map[int64][]Account }

func (s stubAccountLister) ListByGroup(_ context.Context, g int64) ([]Account, error) {
	return s.byGroup[g], nil
}

func acctWithMapping(platform string, keys ...string) Account {
	m := map[string]any{}
	for _, k := range keys {
		m[k] = k
	}
	return Account{Platform: platform, Status: StatusActive, Credentials: map[string]any{"model_mapping": m}}
}

type countingGroupResolver struct {
	mu    sync.Mutex
	ids   map[string]int64
	calls int
}

func (s *countingGroupResolver) GetDefaultAPIKeyGroupID(_ context.Context, keyType string) (*int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if id, ok := s.ids[NormalizeAPIKeyType(keyType)]; ok {
		return &id, nil
	}
	return nil, nil
}

func (s *countingGroupResolver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type countingAccountLister struct {
	mu      sync.Mutex
	byGroup map[int64][]Account
	delay   time.Duration
	calls   int
}

func (s *countingAccountLister) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	if s.delay > 0 {
		timer := time.NewTimer(s.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.byGroup[groupID], nil
}

func (s *countingAccountLister) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestWebChatServiceListModelsCachesDynamicCatalogWithinTTL(t *testing.T) {
	groups := &countingGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 3, APIKeyTypeOpenAI: 6}}
	accounts := &countingAccountLister{byGroup: map[int64][]Account{
		3: {acctWithMapping(PlatformAnthropic, "claude-sonnet-4")},
		6: {acctWithMapping(PlatformOpenAI, "gpt-5.5")},
	}}
	svc := &WebChatService{defaultGroups: groups, accountLister: accounts}

	first, err := svc.ListModels(context.Background(), 42)
	if err != nil {
		t.Fatalf("first ListModels error: %v", err)
	}
	second, err := svc.ListModels(context.Background(), 42)
	if err != nil {
		t.Fatalf("second ListModels error: %v", err)
	}

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected two models from both calls, got %d and %d", len(first), len(second))
	}
	if got := groups.callCount(); got != 2 {
		t.Fatalf("default group lookups=%d want 2 (one catalog build)", got)
	}
	if got := accounts.callCount(); got != 2 {
		t.Fatalf("account group lookups=%d want 2 (one catalog build)", got)
	}
}

func TestWebChatServiceListModelsSingleflightCoalescesConcurrentMiss(t *testing.T) {
	groups := &countingGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 3, APIKeyTypeOpenAI: 6}}
	accounts := &countingAccountLister{
		byGroup: map[int64][]Account{
			3: {acctWithMapping(PlatformAnthropic, "claude-sonnet-4")},
			6: {acctWithMapping(PlatformOpenAI, "gpt-5.5")},
		},
		delay: 20 * time.Millisecond,
	}
	svc := &WebChatService{defaultGroups: groups, accountLister: accounts}

	const requests = 8
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, err := svc.ListModels(context.Background(), 42)
			if err != nil {
				errs <- err
				return
			}
			if len(models) != 2 {
				errs <- context.Canceled
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ListModels error: %v", err)
	}

	if got := groups.callCount(); got != 2 {
		t.Fatalf("default group lookups=%d want 2 (one singleflight catalog build)", got)
	}
	if got := accounts.callCount(); got != 2 {
		t.Fatalf("account group lookups=%d want 2 (one singleflight catalog build)", got)
	}
}

func TestResolveWebChatCatalog_UnionAndProviderAndDedup(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 3, APIKeyTypeOpenAI: 6}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		3: {acctWithMapping("kiro", "claude-opus-4-8", "claude-opus-4-8-thinking")},
		6: {acctWithMapping("openai", "gpt-5.5", "claude-opus-4-8")}, // cross-mapped claude
	}}
	got, err := resolveWebChatCatalog(context.Background(), gr, al)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	prov := map[string]string{}
	for _, c := range got {
		prov[c.Model] = c.Provider
	}
	if prov["claude-opus-4-8"] != "anthropic" {
		t.Fatalf("claude-opus-4-8 provider=%q want anthropic", prov["claude-opus-4-8"])
	}
	if prov["gpt-5.5"] != "openai" {
		t.Fatalf("gpt-5.5 provider=%q want openai", prov["gpt-5.5"])
	}
	if _, dup := prov["claude-opus-4-8-thinking"]; dup {
		t.Fatalf("-thinking variant must collapse, not appear as its own model")
	}
}

func TestResolveWebChatCatalog_SkipsInactiveAndUnconfigured(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 3}} // openai not configured
	inactive := acctWithMapping("kiro", "claude-opus-4-7")
	inactive.Status = "error"
	al := stubAccountLister{byGroup: map[int64][]Account{3: {inactive}}}
	got, err := resolveWebChatCatalog(context.Background(), gr, al)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty catalog (inactive acct + no openai group), got %d", len(got))
	}
}

func TestResolveWebChatCatalog_DatedKeyRoutingPreserved(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 3}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		3: {acctWithMapping("kiro", "claude-opus-4-5-20251101", "claude-opus-4-5-20251101-thinking")},
	}}
	got, err := resolveWebChatCatalog(context.Background(), gr, al)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 model, got %d: %+v", len(got), got)
	}
	// caps.Model must be the real mapping key the account recognizes (dated,
	// non-thinking), NOT the normalized "claude-opus-4-5" — otherwise account
	// selection (SupportsModelInMapping) fails with no-available-accounts.
	if got[0].Model != "claude-opus-4-5-20251101" {
		t.Fatalf("routing key not preserved: Model=%q want claude-opus-4-5-20251101", got[0].Model)
	}
	if !got[0].SupportsThinking {
		t.Fatalf("claude family should support thinking")
	}
}

func TestResolveWebChatCatalog_SortsModelsByReleaseDateWithinProvider(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeOpenAI: 6}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		6: {acctWithMapping(PlatformOpenAI, "gpt-5.4", "gpt-5.6-sol", "gpt-5.6-custom")},
	}}

	got, err := resolveWebChatCatalog(context.Background(), gr, al)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 models, got %d: %+v", len(got), got)
	}
	if got[0].Model != "gpt-5.6-sol" || got[0].ReleasedAt != "2026-07-09" {
		t.Fatalf("first model=%s released_at=%s, want gpt-5.6-sol 2026-07-09", got[0].Model, got[0].ReleasedAt)
	}
	if got[1].Model != "gpt-5.4" || got[1].ReleasedAt != "2026-05-01" {
		t.Fatalf("second model=%s released_at=%s, want gpt-5.4 2026-05-01", got[1].Model, got[1].ReleasedAt)
	}
	if got[2].Model != "gpt-5.6-custom" || got[2].ReleasedAt != "" {
		t.Fatalf("unknown model=%s released_at=%s, want gpt-5.6-custom with empty release", got[2].Model, got[2].ReleasedAt)
	}
}

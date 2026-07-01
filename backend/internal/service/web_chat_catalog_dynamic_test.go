package service

import (
	"context"
	"testing"
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

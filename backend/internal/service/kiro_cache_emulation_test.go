package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

func TestKiroCacheEmulationGroupDefaultsAndNonKiro(t *testing.T) {
	kiro := &Group{Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 0.5}
	if !kiro.EffectiveKiroCacheEmulationEnabled() {
		t.Fatal("kiro group should enable cache emulation")
	}
	if got := kiro.EffectiveKiroCacheEmulationRatio(); got != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", got)
	}
	nonKiro := &Group{Platform: PlatformAnthropic, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}
	NormalizeGroupRuntimeFields(nonKiro)
	if nonKiro.KiroCacheEmulationEnabled || nonKiro.KiroCacheEmulationRatio != 0 {
		t.Fatalf("non-kiro fields were not normalized: %+v", nonKiro)
	}
}

func TestKiroCacheEmulationUsesSnapshotGroupWithoutRepo(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 34, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	first := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if second == nil || second.CacheReadInputTokens != 2000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second usage: %+v", second)
	}
}

func TestKiroCacheEmulationRatioScalesTokens(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 78, Platform: PlatformKiro}
	usage := svc.buildKiroCacheEmulationUsage(context.Background(), account, kiroCacheGroup(0.5), kiroCacheRequestBody("ratio", false), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 1000 || usage.InputTokens != 1000 {
		t.Fatalf("unexpected scaled usage: %+v", usage)
	}
	disabled := kiroCacheGroup(1)
	disabled.KiroCacheEmulationEnabled = false
	if got := svc.buildKiroCacheEmulationUsage(context.Background(), account, disabled, kiroCacheRequestBody("disabled", false), "claude-sonnet-4-6", 2000); got != nil {
		t.Fatalf("disabled group should skip cache emulation, got %+v", got)
	}
}

func TestKiroCacheEmulationAccountIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("account isolation", false)
	first := svc.buildKiroCacheEmulationUsage(context.Background(), kiroCacheAccount(1, "refresh-a", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	otherAccount := svc.buildKiroCacheEmulationUsage(context.Background(), kiroCacheAccount(2, "refresh-b", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if otherAccount == nil || otherAccount.CacheCreationInputTokens != 2000 || otherAccount.CacheReadInputTokens != 0 {
		t.Fatalf("cache should be isolated by account: %+v", otherAccount)
	}
}

func TestKiroCacheEmulationStableCredentialIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("credential isolation", false)
	first := svc.buildKiroCacheEmulationUsage(context.Background(), kiroCacheAccount(7, "refresh-same", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	rotatedAccessToken := svc.buildKiroCacheEmulationUsage(context.Background(), kiroCacheAccount(7, "refresh-same", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if rotatedAccessToken == nil || rotatedAccessToken.CacheReadInputTokens != 2000 || rotatedAccessToken.CacheCreationInputTokens != 0 {
		t.Fatalf("access token rotation should not break cache: %+v", rotatedAccessToken)
	}
	differentCredential := svc.buildKiroCacheEmulationUsage(context.Background(), kiroCacheAccount(7, "refresh-other", "access-c"), group, body, "claude-sonnet-4-6", 2000)
	if differentCredential == nil || differentCredential.CacheReadInputTokens != 0 || differentCredential.CacheCreationInputTokens != 2000 {
		t.Fatalf("different stable credential should not share cache: %+v", differentCredential)
	}
}

func TestKiroCacheEmulationContentChangeMisses(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 3, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	_ = svc.buildKiroCacheEmulationUsage(context.Background(), account, group, kiroCacheRequestBody("before", false), "claude-sonnet-4-6", 2000)
	changed := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, kiroCacheRequestBody("after", false), "claude-sonnet-4-6", 2000)
	if changed == nil || changed.CacheCreationInputTokens != 2000 || changed.CacheReadInputTokens != 0 {
		t.Fatalf("changed content should miss: %+v", changed)
	}
}

func TestKiroCacheEmulationTTLExpiry(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 4, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("ttl", false)
	_ = svc.buildKiroCacheEmulationUsage(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	globalKiroCacheTracker.mu.Lock()
	for accountID, entries := range globalKiroCacheTracker.entries {
		for fp, entry := range entries {
			entry.expiresAt = time.Now().Add(-time.Second)
			globalKiroCacheTracker.entries[accountID][fp] = entry
		}
	}
	globalKiroCacheTracker.mu.Unlock()
	afterExpiry := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	if afterExpiry == nil || afterExpiry.CacheCreationInputTokens != 2000 || afterExpiry.CacheReadInputTokens != 0 {
		t.Fatalf("expired cache should be recreated: %+v", afterExpiry)
	}
}

func TestKiroCacheEmulationOneHourBucket(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	usage := svc.buildKiroCacheEmulationUsage(context.Background(), &Account{ID: 5, Platform: PlatformKiro}, kiroCacheGroup(1), kiroCacheRequestBody("1h", true), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 2000 || usage.CacheCreation1hInputTokens != 2000 || usage.CacheCreation5mInputTokens != 0 {
		t.Fatalf("unexpected 1h bucket usage: %+v", usage)
	}
}

func TestKiroCacheEmulationPrefixPartialHit(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 6, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheMultiMessageBody("cached prefix", "tail one")
	secondBody := kiroCacheMultiMessageBody("cached prefix", "tail two")
	first := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, firstBody, "claude-sonnet-4-6", 6000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, secondBody, "claude-sonnet-4-6", 6000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheReadInputTokens >= first.CacheCreationInputTokens || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected partial prefix hit: %+v", second)
	}
}

func TestKiroInputTokenEstimateIgnoresClientMetadata(t *testing.T) {
	bodyWithoutMetadata := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello world"}]}`)
	bodyWithMetadata := []byte(`{"model":"claude-sonnet-4-6","metadata":{"input_tokens":999999},"messages":[{"role":"user","content":"hello world"}]}`)
	withoutMetadata := estimateKiroInputTokens(context.Background(), bodyWithoutMetadata)
	withMetadata := estimateKiroInputTokens(context.Background(), bodyWithMetadata)
	if withMetadata == 999999 {
		t.Fatal("client metadata.input_tokens must not be trusted")
	}
	if withMetadata <= 0 || withoutMetadata <= 0 || withMetadata > withoutMetadata*2 {
		t.Fatalf("unexpected estimates without=%d with=%d", withoutMetadata, withMetadata)
	}
}

func TestKiroInputTokenEstimateIgnoresMediaBase64Length(t *testing.T) {
	buildBody := func(data string) []byte {
		return []byte(fmt.Sprintf(
			"{\"model\":\"claude-sonnet-4-6\",\"messages\":[{\"role\":\"user\",\"content\":["+
				"{\"type\":\"text\",\"text\":\"describe this\"},"+
				"{\"type\":\"image\",\"source\":{\"type\":\"base64\",\"media_type\":\"image/png\",\"data\":%q}}]}]}",
			data,
		))
	}
	small := estimateKiroInputTokens(context.Background(), buildBody("AAAA"))
	large := estimateKiroInputTokens(context.Background(), buildBody(strings.Repeat("A", 16<<20)))
	if large != small {
		t.Fatalf("image base64 changed estimate: small=%d large=%d", small, large)
	}
}

func TestResolveKiroInputTokensPrefersTranslatedEstimate(t *testing.T) {
	body := []byte("{\"messages\":[{\"role\":\"user\",\"content\":\"raw fallback\"}]}")
	got := resolveKiroInputTokens(context.Background(), body, kiropkg.KiroRequestContext{EstimatedInputTokens: 321})
	if got != 321 {
		t.Fatalf("resolved input tokens = %d, want translated estimate 321", got)
	}
}

func TestKiroInputTokenEstimateMatchesTranslatedPayload(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"system":[{"type":"text","text":"You are helpful."}],
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"lookup","input":{"city":"Shanghai"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"text","text":"sunny"}]}]}
		],
		"tools":[{"name":"lookup","description":"Look up weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]
	}`)
	buildResult, err := kiropkg.BuildKiroPayloadWithContext(body, kiropkg.MapModel("claude-sonnet-4-6"), "", "AI_EDITOR", nil)
	if err != nil {
		t.Fatal(err)
	}

	got := estimateKiroInputTokens(context.Background(), body)
	want := buildResult.Context.EstimatedInputTokens
	if got != want {
		t.Fatalf("Claude-body estimate = %d, translated KiroPayload estimate = %d", got, want)
	}
}

func TestKiroTokenCountersMatchReferenceRules(t *testing.T) {
	if got := anthropictokenizer.CountTokens("abc def"); got != 1 {
		t.Fatalf("english tokens = %d, want 1", got)
	}
	if got := anthropictokenizer.CountTokens("你好世界"); got != 1 {
		t.Fatalf("cjk tokens = %d, want 1", got)
	}
	if kiroTokensPerTool != 150 {
		t.Fatalf("tool tokens = %d, want 150", kiroTokensPerTool)
	}
	if got := countKiroMessageContentTokens(context.Background(), map[string]any{"thinking": "abc def"}); got != 1 {
		t.Fatalf("thinking tokens = %d, want 1", got)
	}
	if got := countKiroMessageContentTokens(context.Background(), map[string]any{"input": map[string]any{"path": "/tmp/a.txt"}}); got <= 0 {
		t.Fatalf("tool input tokens should be positive, got %d", got)
	}
	if got := countKiroMessageContentTokens(context.Background(), map[string]any{"content": []any{map[string]any{"text": "abc"}, map[string]any{"text": "你好"}}}); got != 2 {
		t.Fatalf("tool result content tokens = %d, want 2", got)
	}
}

func TestKiroInputTokenEstimateSeparatesVisualTokensFromBase64(t *testing.T) {
	dataURL := kiroPNGDataURL(t, 512, 512, color.RGBA{R: 37, G: 89, B: 151, A: 255})
	body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}`, strings.TrimPrefix(dataURL, "data:image/png;base64,")))
	textOnlyBody := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"describe"}]}]}`)

	imageTokens := 350
	got := estimateKiroInputTokens(context.Background(), body)
	textOnly := estimateKiroInputTokens(context.Background(), textOnlyBody)
	if got-textOnly != imageTokens {
		t.Fatalf("visual token increment = %d, want %d (total=%d text-only=%d)", got-textOnly, imageTokens, got, textOnly)
	}
	if got >= len(dataURL)/2 {
		t.Fatalf("base64 payload dominated input estimate: got=%d encoded=%d", got, len(dataURL))
	}
}

func TestKiroInputTokenEstimateImageFailureUsesFallback(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://169.254.169.254/latest/meta-data"}}]}]}`)
	got := estimateKiroInputTokens(context.Background(), body)
	buildResult, err := kiropkg.BuildKiroPayloadWithRequestContext(context.Background(), body, kiropkg.MapModel("claude-sonnet-4-6"), "", "AI_EDITOR", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != buildResult.Context.EstimatedInputTokens {
		t.Fatalf("failed image estimate = %d, translated payload estimate = %d", got, buildResult.Context.EstimatedInputTokens)
	}
}

func TestKiroImageTokenSourcesSupportAnthropicAndOpenAIShapes(t *testing.T) {
	dataURL := kiroPNGDataURL(t, 200, 200, color.RGBA{A: 255})
	base64Data := strings.TrimPrefix(dataURL, "data:image/png;base64,")
	tests := []map[string]any{
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64Data}},
		{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
		{"type": "input_image", "image_url": dataURL},
	}
	for _, block := range tests {
		if got := countKiroMessageContentTokens(context.Background(), block); got != 54 {
			t.Fatalf("image block %#v tokens = %d, want 54", block, got)
		}
	}
}

func TestKiroCacheEmulationIncludesImageTokensAndKeepsImageFingerprint(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheAccount(91, "refresh-image", "access-image")
	group := kiroCacheGroup(1)
	prefix := strings.Repeat("cacheable visual prompt ", 700)
	body := kiroCacheImageRequestBody(t, prefix, color.RGBA{R: 1, A: 255})
	inputTokens := estimateKiroInputTokens(context.Background(), body)
	profile, ok := buildKiroCacheProfile(context.Background(), body, "claude-sonnet-4-6", 0)
	if !ok || profile.totalInputTokens != inputTokens {
		t.Fatalf("cache fallback total = %+v, want translated estimate %d", profile, inputTokens)
	}

	first := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, body, "claude-sonnet-4-6", inputTokens)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("unexpected first image cache usage: %+v", first)
	}
	if first.InputTokens+first.CacheCreationInputTokens+first.CacheReadInputTokens != inputTokens {
		t.Fatalf("first image cache token totals do not balance: usage=%+v total=%d", first, inputTokens)
	}

	second := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, body, "claude-sonnet-4-6", inputTokens)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("same image should hit cache: %+v", second)
	}

	changedBody := kiroCacheImageRequestBody(t, prefix, color.RGBA{G: 1, A: 255})
	changedTokens := estimateKiroInputTokens(context.Background(), changedBody)
	changed := svc.buildKiroCacheEmulationUsage(context.Background(), account, group, changedBody, "claude-sonnet-4-6", changedTokens)
	if changed == nil || changed.CacheReadInputTokens != 0 || changed.CacheCreationInputTokens <= 0 {
		t.Fatalf("different image must miss cache: %+v", changed)
	}
}

func resetKiroCacheTracker() {
	globalKiroCacheTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
}

func kiroPNGDataURL(t *testing.T, width, height int, fill color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func kiroCacheImageRequestBody(t *testing.T, text string, fill color.RGBA) []byte {
	t.Helper()
	dataURL := kiroPNGDataURL(t, 200, 200, fill)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q},"cache_control":{"type":"ephemeral"}}]}]}`, text, strings.TrimPrefix(dataURL, "data:image/png;base64,")))
}

func kiroCacheGroup(ratio float64) *Group {
	return &Group{ID: 12, Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: ratio}
}

func kiroCacheAccount(id int64, refreshToken string, accessToken string) *Account {
	return &Account{ID: id, Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"client_id":     "client-id",
		"refresh_token": refreshToken,
		"access_token":  accessToken,
	}}
}

func kiroCacheRequestBody(label string, oneHour bool) []byte {
	ttl := ""
	if oneHour {
		ttl = `,"ttl":"1h"`
	}
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"%s}}]}]}`, strings.Repeat("cacheable prompt chunk "+label+" ", 512), ttl))
}

func kiroCacheMultiMessageBody(prefixLabel, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 160)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":%q}]}]}`, prefix, tail))
}

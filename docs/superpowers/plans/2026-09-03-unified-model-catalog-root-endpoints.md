# Unified Model Catalog and Root Gateway Endpoints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a unified API key return one stable, permission-aware model catalog and make generated gateway client configurations use the bare `https://www.linx2.ai` origin without changing POST routing.

**Architecture:** Resolve the unified key owner's existing Anthropic and OpenAI effective groups without mutating authentication state, then aggregate concrete public `model_mapping` keys from persistently eligible accounts. Cache each group catalog in Redis for ten minutes and merge it only in the unified-key model-list handler; keep forwarding and scheduler code untouched. Add route aliases that delegate to existing handlers and update generated client configurations to consume those aliases.

**Tech Stack:** Go 1.26, Gin, go-redis, miniredis, testify, Vue 3, TypeScript, Vitest, pnpm.

**Spec:** `docs/superpowers/specs/2026-09-03-unified-model-catalog-root-endpoints-design.md`

## Global Constraints

- Work only in `/home/alvin/tokenstation3/.worktrees/unified-model-catalog-root-endpoints` on branch `feat/unified-model-catalog-root-endpoints`.
- Do not modify the original `/home/alvin/tokenstation3` `dev` worktree.
- Do not change model-family selection, account selection, scheduling, failover, request translation, billing, or upstream forwarding behavior.
- Do not add Kimi, Zhipu/GLM, or DeepSeek as new unified-key routing targets.
- Do not contact or modify production, production data, or provider accounts.
- Preserve every existing `/v1` endpoint and every required Gemini `/v1beta` endpoint.
- Treat the three baseline `golangci-lint v2.9` QF1001 findings in the two OpenAI Anthropic-native files as approved pre-existing findings; do not edit those files.
- Follow red-green-refactor: no production behavior change before its focused test has failed for the expected missing behavior.

---

### Task 1: Resolve all displayable effective groups for a unified key

**Files:**
- Modify: `backend/internal/service/api_key_service.go:504-545`
- Modify: `backend/internal/service/api_key_service_provider_routing_test.go`

**Interfaces:**
- Consumes: existing `(*APIKeyService).resolveProviderGroup(ctx, user, keyType)` and errors `ErrDefaultAPIKeyGroupMissing`, `ErrDefaultAPIKeyGroupInvalid`, and `ErrGroupNotAllowed`.
- Produces: `func (s *APIKeyService) ResolveModelCatalogGroups(ctx context.Context, apiKey *APIKey) ([]*Group, error)`.

- [ ] **Step 1: Add failing tests for unified, partial, static, and infrastructure-error resolution**

Extend the group repository test stub so it can return distinct groups and errors:

```go
type modelCatalogGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
	errs   map[int64]error
}

func (s *modelCatalogGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if err := s.errs[id]; err != nil {
		return nil, err
	}
	group := s.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	clone := *group
	return &clone, nil
}
```

Add tests with these exact behavioral assertions:

```go
func TestAPIKeyService_ResolveModelCatalogGroups_UnifiedUsesAuthorizedProviderRoutes(t *testing.T) {
	userID := int64(42)
	anthropicID := int64(3)
	openAIID := int64(6)
	svc := &APIKeyService{groupRepo: &modelCatalogGroupRepoStub{groups: map[int64]*Group{
		anthropicID: {ID: anthropicID, Platform: PlatformAnthropic, Status: StatusActive},
		openAIID:    {ID: openAIID, Platform: PlatformOpenAI, Status: StatusActive},
	}}}
	svc.SetProviderRouting(apiKeyProviderRouteRepoStub{routes: map[string]*UserAPIKeyRoute{
		providerRouteKey(userID, APIKeyTypeOpenAI): {UserID: userID, KeyType: APIKeyTypeOpenAI, GroupID: openAIID},
	}}, &defaultAPIKeyGroupSettingsStub{ids: map[string]*int64{APIKeyTypeAnthropic: &anthropicID}})

	groups, err := svc.ResolveModelCatalogGroups(context.Background(), &APIKey{
		UserID: userID,
		User: &User{ID: userID, Status: StatusActive},
		GroupBindingMode: APIKeyGroupBindingModeAuto,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{anthropicID, openAIID}, []int64{groups[0].ID, groups[1].ID})
}
```

Also add:

- `TestAPIKeyService_ResolveModelCatalogGroups_UnifiedSkipsMissingProviderDefault`, asserting one valid group and no error;
- `TestAPIKeyService_ResolveModelCatalogGroups_UnifiedSkipsInvalidOrForbiddenProviderGroup`, asserting invalid and unauthorized provider groups never enter the display catalog;
- `TestAPIKeyService_ResolveModelCatalogGroups_StaticReturnsCurrentGroupOnly`, asserting no provider settings lookup;
- `TestAPIKeyService_ResolveModelCatalogGroups_PropagatesRepositoryFailure`, using a sentinel error from `GetByID` and `require.ErrorIs`.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestAPIKeyService_ResolveModelCatalogGroups' -count=1
```

Expected: compilation fails because `ResolveModelCatalogGroups` does not exist.

- [ ] **Step 3: Implement the display-only resolver**

Add this method next to `resolveProviderGroup`:

```go
func (s *APIKeyService) ResolveModelCatalogGroups(ctx context.Context, apiKey *APIKey) ([]*Group, error) {
	if apiKey == nil {
		return []*Group{}, nil
	}
	if apiKey.GroupBindingMode != APIKeyGroupBindingModeAuto {
		if apiKey.Group == nil {
			return []*Group{}, nil
		}
		return []*Group{apiKey.Group}, nil
	}
	if apiKey.User == nil {
		return nil, errors.New("model catalog requires api key user")
	}

	groups := make([]*Group, 0, 2)
	seen := make(map[int64]struct{}, 2)
	for _, keyType := range []string{APIKeyTypeAnthropic, APIKeyTypeOpenAI} {
		_, group, err := s.resolveProviderGroup(ctx, apiKey.User, keyType)
		if err != nil {
			if errors.Is(err, ErrDefaultAPIKeyGroupMissing) ||
				errors.Is(err, ErrDefaultAPIKeyGroupInvalid) ||
				errors.Is(err, ErrGroupNotAllowed) {
				continue
			}
			return nil, err
		}
		if group == nil {
			continue
		}
		if _, exists := seen[group.ID]; exists {
			continue
		}
		seen[group.ID] = struct{}{}
		groups = append(groups, group)
	}
	return groups, nil
}
```

Do not assign to `apiKey.Group` or `apiKey.GroupID` in this method.

- [ ] **Step 4: Run the focused tests and verify GREEN**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestAPIKeyService_(ResolveModelCatalogGroups|ResolveProviderGroup)' -count=1
```

Expected: all selected tests pass.

- [ ] **Step 5: Commit the resolver**

```bash
git add backend/internal/service/api_key_service.go backend/internal/service/api_key_service_provider_routing_test.go
git commit -m "feat: resolve unified model catalog groups"
```

---

### Task 2: Build and cache stable per-group configured catalogs

**Files:**
- Create: `backend/internal/service/model_catalog.go`
- Create: `backend/internal/service/model_catalog_test.go`
- Create: `backend/internal/repository/gateway_model_catalog_cache.go`
- Create: `backend/internal/repository/gateway_model_catalog_cache_test.go`
- Modify: `backend/internal/service/gateway_service.go:900-1015`

**Interfaces:**
- Consumes: `AccountRepository.ListModelAvailabilityCandidates`, `mixedSchedulingPlatforms`, `accountEligibleForMixedPlatform`, `Account.GetModelMapping`, `Account.IsOpenAIPassthroughEnabled`, and the existing `GatewayCache` constructor argument.
- Produces:
  - `var ErrModelCatalogCacheMiss error`;
  - `type ModelCatalogCache interface { GetModelCatalog(context.Context, int64, string) ([]string, error); SetModelCatalog(context.Context, int64, string, []string, time.Duration) error }`;
  - `func (s *GatewayService) GetConfiguredModelCatalog(ctx context.Context, group *Group) ([]string, error)`;
  - Redis implementations on `*gatewayCache`.

- [ ] **Step 1: Write failing service tests for mapping aggregation and eligibility**

Create a repository stub that records calls to the stable query:

```go
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
```

Add `TestGatewayService_GetConfiguredModelCatalog_AggregatesConcretePublicKeys` with:

- an Anthropic mapping `claude-public -> claude-upstream`;
- a duplicate `claude-public` on another Anthropic account;
- a wildcard `claude-*` that must be absent;
- a Kiro account with `Extra["mixed_scheduling"] = true` whose concrete mapping must be present;
- a Kiro account with the flag false whose mapping must be absent;
- an Antigravity account with the flag false whose mapping must be absent.

Assert the exact sorted result contains public keys, not mapping values:

```go
require.Equal(t, []string{"claude-public", "kiro-public"}, models)
require.NotContains(t, models, "claude-upstream")
require.NotContains(t, models, "claude-*")
```

Add these focused tests:

- `TestGatewayService_GetConfiguredModelCatalog_OpenAIPassthroughContributesNoStaleMapping`;
- `TestGatewayService_GetConfiguredModelCatalog_EmptyCatalogIsValid`;
- `TestGatewayService_GetConfiguredModelCatalog_CustomListOnlyIntersectsAvailableModels`;
- `TestGatewayService_GetConfiguredModelCatalog_PropagatesRepositoryError`.

- [ ] **Step 2: Run service tests and verify RED**

Run:

```bash
cd backend
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog' -count=1
```

Expected: compilation fails because `GetConfiguredModelCatalog` does not exist.

- [ ] **Step 3: Implement the uncached catalog builder**

Create `model_catalog.go` with:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const modelCatalogCacheTTL = 10 * time.Minute

var ErrModelCatalogCacheMiss = errors.New("model catalog cache miss")

type ModelCatalogCache interface {
	GetModelCatalog(ctx context.Context, groupID int64, platform string) ([]string, error)
	SetModelCatalog(ctx context.Context, groupID int64, platform string, models []string, ttl time.Duration) error
}

func (s *GatewayService) GetConfiguredModelCatalog(ctx context.Context, group *Group) ([]string, error) {
	if s == nil || s.accountRepo == nil || group == nil || group.ID <= 0 {
		return []string{}, nil
	}

	models, err := s.loadConfiguredModelCatalog(ctx, group)
	if err != nil {
		return nil, err
	}
	return filterConfiguredCatalogByCustomList(models, group), nil
}

func (s *GatewayService) loadConfiguredModelCatalog(ctx context.Context, group *Group) ([]string, error) {
	platform := strings.TrimSpace(group.Platform)
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		&group.ID,
		mixedSchedulingPlatforms(platform),
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("list model catalog candidates: %w", err)
	}

	set := make(map[string]struct{})
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform && !accountEligibleForMixedPlatform(account, platform) {
			continue
		}
		if platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
			continue
		}
		for rawID := range account.GetModelMapping() {
			id := strings.TrimSpace(rawID)
			if id == "" || strings.Contains(id, "*") {
				continue
			}
			set[id] = struct{}{}
		}
	}

	models := make([]string, 0, len(set))
	for id := range set {
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

func filterConfiguredCatalogByCustomList(models []string, group *Group) []string {
	if group == nil || !group.CustomModelsListEnabled() {
		return append([]string(nil), models...)
	}
	available := make(map[string]struct{}, len(models))
	for _, id := range models {
		available[id] = struct{}{}
	}
	selected := make(map[string]struct{}, len(group.ModelsListConfig.Models))
	for _, rawID := range group.ModelsListConfig.Models {
		if id := strings.TrimSpace(rawID); id != "" {
			if _, ok := available[id]; ok {
				selected[id] = struct{}{}
			}
		}
	}
	filtered := make([]string, 0, len(selected))
	for id := range selected {
		filtered = append(filtered, id)
	}
	sort.Strings(filtered)
	return filtered
}
```

The first GREEN can ignore Redis; add caching only after its own failing tests.

- [ ] **Step 4: Run service aggregation tests and verify GREEN**

Run:

```bash
cd backend
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog' -count=1
```

Expected: all catalog aggregation tests pass.

- [ ] **Step 5: Write failing cache-aside service tests**

Add a `modelCatalogCacheStub` implementing `ModelCatalogCache`. Add tests that assert:

- a cache hit, including `[]string{}`, makes zero repository calls;
- `ErrModelCatalogCacheMiss` recomputes once and stores with exactly `10*time.Minute`;
- cache read and write errors do not prevent a successful database result;
- a database error after a cache miss is returned.

Construct `GatewayService` directly in the same package so the cache is injectable:

```go
svc := &GatewayService{
	accountRepo: repo,
	modelCatalogCache: cache,
}
```

- [ ] **Step 6: Run cache-aside service tests and verify RED**

Run:

```bash
cd backend
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog_(UsesCache|CachesMiss|FallsBack)' -count=1
```

Expected: compilation fails because `GatewayService.modelCatalogCache` does not exist, or assertions show the cache is never called.

- [ ] **Step 7: Add the optional cache field and cache-aside flow**

Add this field to `GatewayService`:

```go
modelCatalogCache ModelCatalogCache
```

In `NewGatewayService`, after constructing `svc`, connect the existing Redis-backed cache only when it implements the optional interface:

```go
if catalogCache, ok := cache.(ModelCatalogCache); ok {
	svc.modelCatalogCache = catalogCache
}
```

At the start of `loadConfiguredModelCatalog`, read the cache. Treat only
`ErrModelCatalogCacheMiss` as an ordinary miss; log other cache errors and
continue. After the sorted database result is built, write it with
`modelCatalogCacheTTL`; log write errors and still return the database result.
Use the existing package logger and never return a Redis error to the handler.

- [ ] **Step 8: Run cache-aside service tests and verify GREEN**

Run:

```bash
cd backend
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog' -count=1
```

Expected: all aggregation and cache-aside tests pass.

- [ ] **Step 9: Write failing miniredis tests for persistence, empty values, isolation, and TTL**

Create `gateway_model_catalog_cache_test.go` with a miniredis server and assert:

```go
cache, ok := NewGatewayCache(client).(service.ModelCatalogCache)
require.True(t, ok)

require.ErrorIs(t, mustGetCatalog(cache, 3, service.PlatformAnthropic), service.ErrModelCatalogCacheMiss)
require.NoError(t, cache.SetModelCatalog(ctx, 3, service.PlatformAnthropic, []string{}, 10*time.Minute))
require.Equal(t, []string{}, mustCatalog(cache.GetModelCatalog(ctx, 3, service.PlatformAnthropic)))
require.NoError(t, cache.SetModelCatalog(ctx, 6, service.PlatformOpenAI, []string{"gpt-5.5"}, 10*time.Minute))
require.Equal(t, []string{"gpt-5.5"}, mustCatalog(cache.GetModelCatalog(ctx, 6, service.PlatformOpenAI)))
```

Inspect `buildModelCatalogKey` through the same-package test and use
`miniredis.TTL` to assert a ten-minute TTL. The helpers must fail the test on
unexpected errors rather than swallowing them.

- [ ] **Step 10: Run repository tests and verify RED**

Run:

```bash
cd backend
go test ./internal/repository -run 'TestGatewayModelCatalogCache' -count=1
```

Expected: compilation fails because `*gatewayCache` does not implement `ModelCatalogCache`.

- [ ] **Step 11: Implement the Redis adapter**

Create `gateway_model_catalog_cache.go` using versioned keys and JSON arrays:

```go
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const modelCatalogPrefix = "gateway:model_catalog:v1:"

func buildModelCatalogKey(groupID int64, platform string) string {
	return fmt.Sprintf("%sgroup:%d:platform:%s", modelCatalogPrefix, groupID, strings.TrimSpace(platform))
}

func (c *gatewayCache) GetModelCatalog(ctx context.Context, groupID int64, platform string) ([]string, error) {
	payload, err := c.rdb.Get(ctx, buildModelCatalogKey(groupID, platform)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, service.ErrModelCatalogCacheMiss
	}
	if err != nil {
		return nil, err
	}
	var models []string
	if err := json.Unmarshal(payload, &models); err != nil {
		return nil, fmt.Errorf("decode model catalog: %w", err)
	}
	if models == nil {
		models = []string{}
	}
	return models, nil
}

func (c *gatewayCache) SetModelCatalog(ctx context.Context, groupID int64, platform string, models []string, ttl time.Duration) error {
	if models == nil {
		models = []string{}
	}
	payload, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("encode model catalog: %w", err)
	}
	return c.rdb.Set(ctx, buildModelCatalogKey(groupID, platform), payload, ttl).Err()
}

var _ service.ModelCatalogCache = (*gatewayCache)(nil)
```

Follow existing `gatewayCache` nil-receiver conventions if the repository
tests expose a nil-client panic; do not expand the public `GatewayCache`
interface.

- [ ] **Step 12: Run all Task 2 tests and verify GREEN**

Run:

```bash
cd backend
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog' -count=1
go test ./internal/repository -run 'TestGatewayModelCatalogCache' -count=1
```

Expected: both commands pass.

- [ ] **Step 13: Commit the catalog and Redis cache**

```bash
git add backend/internal/service/model_catalog.go backend/internal/service/model_catalog_test.go backend/internal/service/gateway_service.go backend/internal/repository/gateway_model_catalog_cache.go backend/internal/repository/gateway_model_catalog_cache_test.go
git commit -m "feat: cache configured model catalogs"
```

---

### Task 3: Serve the unified list and Codex manifest from one catalog

**Files:**
- Create: `backend/internal/handler/gateway_unified_models.go`
- Modify: `backend/internal/handler/gateway_handler.go:66-145,1203-1265`
- Modify: `backend/internal/handler/gateway_models_test.go`
- Modify: `backend/internal/server/routes/gateway.go:45-70`
- Modify: `backend/internal/server/routes/gateway_codex_models_test.go`

**Interfaces:**
- Consumes: Task 1 `ResolveModelCatalogGroups` and Task 2 `GetConfiguredModelCatalog`.
- Produces: unified-key branch in `(*GatewayHandler).Models`; local Codex response `{"models":[{"slug":"..."}]}`; ordinary OpenAI-compatible response using `writeOpenAIModelsList`.

- [ ] **Step 1: Write failing handler tests for union, deduplication, empty results, and manifest shape**

Define an injectable interface in the test's desired API:

```go
type modelCatalogGroupResolverStub struct {
	groups []*service.Group
	err    error
}

func (s modelCatalogGroupResolverStub) ResolveModelCatalogGroups(context.Context, *service.APIKey) ([]*service.Group, error) {
	return s.groups, s.err
}
```

Extend `gatewayModelsAccountRepoStub` with
`ListModelAvailabilityCandidates`, keyed by group ID. Add:

- `TestGatewayModels_UnifiedKeyMergesEffectiveGroups`, with duplicate model IDs in groups 3 and 6, asserting exact sorted `data[].id` values and OpenAI list shape;
- `TestGatewayModels_UnifiedKeyReturnsConfiguredEmptyListWithoutFallback`;
- `TestGatewayModels_UnifiedCodexManifestUsesSameCatalog`, requesting `/models?client_version=0.144.0` and asserting exact sorted `models[].slug` values;
- `TestGatewayModels_UnifiedCatalogFailureReturnsInternalError`.

Set the API key context explicitly:

```go
c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
	UserID: 42,
	User: &service.User{ID: 42, Status: service.StatusActive},
	GroupBindingMode: service.APIKeyGroupBindingModeAuto,
})
```

- [ ] **Step 2: Run handler tests and verify RED**

Run:

```bash
cd backend
go test ./internal/handler -run 'TestGatewayModels_Unified' -count=1
```

Expected: tests fail because auto keys still use only the request's current group and hard-coded fallbacks.

- [ ] **Step 3: Add the handler dependency and unified response implementation**

In `gateway_unified_models.go`, define:

```go
package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelCatalogGroupResolver interface {
	ResolveModelCatalogGroups(context.Context, *service.APIKey) ([]*service.Group, error)
}

func (h *GatewayHandler) writeUnifiedModelCatalog(c *gin.Context, apiKey *service.APIKey) {
	groups, err := h.modelCatalogGroupResolver.ResolveModelCatalogGroups(c.Request.Context(), apiKey)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to resolve model catalog groups")
		return
	}
	set := make(map[string]struct{})
	for _, group := range groups {
		models, err := h.gatewayService.GetConfiguredModelCatalog(c.Request.Context(), group)
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to load model catalog")
			return
		}
		for _, id := range models {
			set[id] = struct{}{}
		}
	}
	models := make([]string, 0, len(set))
	for id := range set {
		models = append(models, id)
	}
	sort.Strings(models)
	if c.Query("client_version") != "" {
		manifest := make([]gin.H, 0, len(models))
		for _, id := range models {
			manifest = append(manifest, gin.H{"slug": id})
		}
		c.JSON(http.StatusOK, gin.H{"models": manifest})
		return
	}
	writeOpenAIModelsList(c, models)
}
```

Add `modelCatalogGroupResolver modelCatalogGroupResolver` to `GatewayHandler`,
set it to `apiKeyService` in `NewGatewayHandler`, and guard nil dependencies
with the same internal-error response before dereferencing them.

At the start of `Models`, branch only for
`GroupBindingMode == APIKeyGroupBindingModeAuto`; leave the rest of the method
unchanged.

- [ ] **Step 4: Prevent auto Codex requests from entering the upstream manifest handler**

Before the existing `client_version` platform switch in `modelsHandler`, add:

```go
if apiKey, ok := middleware.GetAPIKeyFromContext(c); ok &&
	apiKey.GroupBindingMode == service.APIKeyGroupBindingModeAuto {
	h.Gateway.Models(c)
	return
}
```

This is model discovery only; do not change any POST handler.

- [ ] **Step 5: Run handler and route tests and verify GREEN**

Run:

```bash
cd backend
go test ./internal/handler -run 'TestGatewayModels_(Unified|Gemini|Kiro|Grok|Custom|OpenAI)' -count=1
go test ./internal/server/routes -run 'TestGatewayRoutesCodexModelsManifestPathIsRegistered' -count=1
```

Expected: unified tests pass and existing typed/static model-list tests remain green.

- [ ] **Step 6: Commit unified model responses**

```bash
git add backend/internal/handler/gateway_unified_models.go backend/internal/handler/gateway_handler.go backend/internal/handler/gateway_models_test.go backend/internal/server/routes/gateway.go backend/internal/server/routes/gateway_codex_models_test.go
git commit -m "feat: serve unified configured model catalog"
```

---

### Task 4: Add missing bare-path aliases without changing handlers

**Files:**
- Modify: `backend/internal/server/routes/gateway.go:40-380`
- Modify: `backend/internal/server/routes/gateway_test.go`

**Interfaces:**
- Consumes: existing `GatewayHandler.Messages`, `GatewayHandler.CountTokens`, `OpenAIGatewayHandler.Messages`, `useOpenAICompatibleGateway`, and existing middleware stacks.
- Produces: `POST /messages`, `POST /antigravity/messages`, and `POST /antigravity/messages/count_tokens`.

- [ ] **Step 1: Write failing route-registration equivalence tests**

Add a helper that maps method and path to the registered final handler name,
then assert:

```go
func TestGatewayRoutesBareMessagesAliasesAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	routes := registeredRouteHandlers(router)

	require.NotEmpty(t, routes["POST /messages"])
	require.Equal(t, routes["POST /v1/messages"], routes["POST /messages"])
	require.NotEmpty(t, routes["POST /antigravity/messages"])
	require.Equal(t, routes["POST /antigravity/v1/messages"], routes["POST /antigravity/messages"])
	require.NotEmpty(t, routes["POST /antigravity/messages/count_tokens"])
	require.Equal(t, routes["POST /antigravity/v1/messages/count_tokens"], routes["POST /antigravity/messages/count_tokens"])
}
```

- [ ] **Step 2: Run the route test and verify RED**

Run:

```bash
cd backend
go test ./internal/server/routes -run 'TestGatewayRoutesBareMessagesAliasesAreRegistered' -count=1
```

Expected: all three bare routes are absent.

- [ ] **Step 3: Reuse the existing messages handler and register aliases**

Extract the existing `/v1/messages` closure into one local variable:

```go
messagesHandler := func(c *gin.Context) {
	if useOpenAICompatibleGateway(c, h.OpenAIGateway.Messages) {
		h.OpenAIGateway.Messages(c)
		return
	}
	h.Gateway.Messages(c)
}
```

Register both `/v1/messages` and `/messages` with this exact handler. Register
the two Antigravity bare aliases with the same middleware order as the
`/antigravity/v1` group, including `ForcePlatform(PlatformAntigravity)`, and
the same final `h.Gateway.Messages` or `h.Gateway.CountTokens` method.

- [ ] **Step 4: Run route tests and verify GREEN**

Run:

```bash
cd backend
go test ./internal/server/routes -run 'TestGatewayRoutes(BareMessagesAliasesAreRegistered|GrokAllowsCLICompatibilityEntrypoints|OpenAICountTokensPathIsRegistered)' -count=1
```

Expected: all selected route tests pass.

- [ ] **Step 5: Commit route aliases**

```bash
git add backend/internal/server/routes/gateway.go backend/internal/server/routes/gateway_test.go
git commit -m "feat: add bare messages gateway aliases"
```

---

### Task 5: Remove `/v1` from generated client endpoint examples

**Files:**
- Modify: `frontend/src/components/keys/clientConfigFiles.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue:530-660,900-925`
- Modify: `frontend/src/utils/ccswitchImport.ts`
- Modify: `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts`
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Modify: `frontend/src/utils/__tests__/ccswitchImport.spec.ts`
- Verify: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`

**Interfaces:**
- Consumes: Task 4 bare HTTP aliases and already-existing bare OpenAI-compatible routes.
- Produces: generated configurations normalized to a bare gateway root, except required `/v1beta` URLs and `ccswitch://v1/import`.

- [ ] **Step 1: Change expected client-config outputs and verify RED**

Update `clientConfigFiles.spec.ts` so all of these exact expectations are bare:

```ts
base_url = "https://gateway.example.com"
```

```ts
expect(parsed.provider.openai.options.baseURL).toBe('https://gateway.example.com')
expect(parsed.provider.anthropic.options.baseURL).toBe('https://gateway.example.com')
```

```text
Endpoint: https://gateway.example.com
```

Add an Antigravity OpenCode assertion for
`https://gateway.example.com/antigravity`. Replace the old normalization test
with one asserting that an input ending in `/v1/` produces no gateway `/v1`
suffix in Claude, Codex, OpenCode, or CC Switch content.

Run:

```bash
cd frontend
pnpm exec vitest run src/components/keys/__tests__/clientConfigFiles.spec.ts
```

Expected: Codex, OpenCode, and CC Switch assertions fail because current output adds `/v1`.

- [ ] **Step 2: Make `clientConfigFiles.ts` use only the bare root and verify GREEN**

Replace `gatewayRoots` with:

```ts
function gatewayRoot(baseUrl: string): string {
  return baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
}
```

Use `bare` for Codex `base_url`, every OpenCode provider, and every CC Switch
Codex endpoint. Use `${bare}/antigravity` for Antigravity Claude. Do not alter
model IDs, keys, `wire_api`, or auth fields.

Run the same focused Vitest command. Expected: all tests pass.

- [ ] **Step 3: Change UseKeyModal expectations and verify RED**

In `UseKeyModal.spec.ts`, update gateway endpoint expectations to:

- OpenAI/OpenCode/SDK/Grok base: `https://example.com`;
- Antigravity Claude base: `https://example.com/antigravity`;
- WorkBuddy URL: `https://example.com/chat/completions`;
- Gemini base: `https://example.com/v1beta`;
- Antigravity Gemini base: `https://example.com/antigravity/v1beta`.

Keep explicit assertions that Anthropic SDK never receives `/v1`, Gemini keeps
`/v1beta`, and Codex output still contains `wire_api = "responses"`.

Run:

```bash
cd frontend
pnpm exec vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: OpenAI, OpenCode, WorkBuddy, and Grok expectations fail against current `/v1` output.

- [ ] **Step 4: Normalize all modal-generated gateway endpoints to the bare root**

In `currentFiles`, retain `baseRoot` and delete `ensureV1`. Set:

```ts
const apiBase = baseRoot
const chatCompletionsUrl = `${baseRoot}/chat/completions`
const antigravityBase = `${baseRoot}/antigravity`
```

Keep the existing `geminiBase` and `antigravityGeminiBase` `/v1beta` logic.
Pass `apiBase` to OpenAI SDK, image SDK, OpenCode, and Grok generators. Update
the Grok Build comments to say `POST /responses` and `/chat/completions`.

Run the focused UseKeyModal test. Expected: all tests pass.

- [ ] **Step 5: Change CC Switch Grok expectations and verify RED**

Replace the parameterized `/v1` expectation with a bare-root expectation for
all four inputs:

```ts
expect(params.get('endpoint')).toBe('https://api.example.com')
expect(deeplink.startsWith('ccswitch://v1/import?')).toBe(true)
```

Run:

```bash
cd frontend
pnpm exec vitest run src/utils/__tests__/ccswitchImport.spec.ts
```

Expected: Grok endpoint assertions fail because `withV1Endpoint` adds `/v1`.

- [ ] **Step 6: Normalize CC Switch endpoints and verify GREEN**

Replace `withV1Endpoint` with:

```ts
function gatewayRoot(baseUrl: string): string {
  return baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
}
```

At the start of `resolveCcSwitchImportConfig`, compute `const root =
gatewayRoot(baseUrl)` and use `root` for all endpoints; append only
`/antigravity` where required. Leave the deeplink return value beginning with
`ccswitch://v1/import`.

Run the focused CC Switch test. Expected: all tests pass.

- [ ] **Step 7: Run the complete affected frontend test set**

Run:

```bash
cd frontend
pnpm exec vitest run \
  src/components/keys/__tests__/clientConfigFiles.spec.ts \
  src/components/keys/__tests__/UseKeyModal.spec.ts \
  src/utils/__tests__/ccswitchImport.spec.ts \
  src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: all affected tests pass.

- [ ] **Step 8: Audit remaining suffixes in generated-client code**

Run:

```bash
rg -n '/v1' \
  frontend/src/components/keys/clientConfigFiles.ts \
  frontend/src/components/keys/UseKeyModal.vue \
  frontend/src/utils/ccswitchImport.ts
```

Expected remaining matches are limited to `/v1beta`, comments explaining SDK
behavior, and `ccswitch://v1/import`. No generated Codex, OpenCode, OpenAI SDK,
WorkBuddy, Grok Build, or CC Switch gateway endpoint contains `/v1`.

- [ ] **Step 9: Commit generated-client changes**

```bash
git add frontend/src/components/keys/clientConfigFiles.ts frontend/src/components/keys/UseKeyModal.vue frontend/src/utils/ccswitchImport.ts frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts frontend/src/components/keys/__tests__/UseKeyModal.spec.ts frontend/src/utils/__tests__/ccswitchImport.spec.ts frontend/src/views/public/__tests__/GettingStartedView.spec.ts
git commit -m "fix: use bare gateway URLs in client examples"
```

---

### Task 6: Verify scope, compatibility, builds, and known baseline lint

**Files:**
- Verify: all files changed since `f7ed1ba92e7e89ed307c36ed09bde0adf483941d`
- Verify: `docs/superpowers/specs/2026-09-03-unified-model-catalog-root-endpoints-design.md`

**Interfaces:**
- Consumes: all preceding task outputs.
- Produces: fresh verification evidence and a branch ready for the finishing workflow.

- [ ] **Step 1: Format and inspect the exact changed-file set**

Run:

```bash
gofmt -w \
  backend/internal/service/api_key_service.go \
  backend/internal/service/api_key_service_provider_routing_test.go \
  backend/internal/service/model_catalog.go \
  backend/internal/service/model_catalog_test.go \
  backend/internal/service/gateway_service.go \
  backend/internal/repository/gateway_model_catalog_cache.go \
  backend/internal/repository/gateway_model_catalog_cache_test.go \
  backend/internal/handler/gateway_unified_models.go \
  backend/internal/handler/gateway_handler.go \
  backend/internal/handler/gateway_models_test.go \
  backend/internal/server/routes/gateway.go \
  backend/internal/server/routes/gateway_test.go \
  backend/internal/server/routes/gateway_codex_models_test.go
git diff --check f7ed1ba92e7e89ed307c36ed09bde0adf483941d..HEAD
git diff --name-status f7ed1ba92e7e89ed307c36ed09bde0adf483941d..HEAD
```

Expected: no whitespace errors and no forwarding, scheduler, billing, or
production deployment files in the changed list.

- [ ] **Step 2: Run focused backend regression suites**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestAPIKeyService_(ResolveModelCatalogGroups|ResolveProviderGroup)' -count=1
go test ./internal/service -run 'TestGatewayService_GetConfiguredModelCatalog' -count=1
go test ./internal/repository -run 'TestGatewayModelCatalogCache' -count=1
go test ./internal/handler -run 'TestGatewayModels_' -count=1
go test ./internal/server/routes -run 'TestGatewayRoutes' -count=1
```

Expected: every command passes.

- [ ] **Step 3: Run focused frontend regression suites**

Run:

```bash
cd frontend
pnpm exec vitest run \
  src/components/keys/__tests__/clientConfigFiles.spec.ts \
  src/components/keys/__tests__/UseKeyModal.spec.ts \
  src/utils/__tests__/ccswitchImport.spec.ts \
  src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: all selected tests pass.

- [ ] **Step 4: Run full backend and frontend verification**

Run:

```bash
cd backend
go test ./...
go build ./cmd/server
cd ../frontend
pnpm run lint:check
pnpm run typecheck
pnpm run test:run
pnpm run build
```

Expected: tests, type checking, and both builds pass. Existing noisy frontend
test warnings may remain, but there must be no failed test.

- [ ] **Step 5: Run new-code lint and compare the approved full-lint baseline**

Run:

```bash
cd backend
/tmp/tokenstation3-golangci-v2.9-bin/golangci-lint run --new-from-rev f7ed1ba92e7e89ed307c36ed09bde0adf483941d ./...
/tmp/tokenstation3-golangci-v2.9-bin/golangci-lint run ./...
```

Expected: new-code lint exits successfully. Full lint reports only the three
approved QF1001 findings at the two pre-existing Anthropic-native OpenAI files;
record that non-zero baseline result exactly and do not edit those files.

- [ ] **Step 6: Verify design requirements line by line**

Inspect the final diff and confirm all of these facts with source and test
evidence:

- unified model discovery resolves only authorized Anthropic/OpenAI effective groups;
- mapping values, wildcard keys, inaccessible mixed accounts, and passthrough stale mappings are absent;
- Redis has a ten-minute cache and failures fall back to persistent configuration;
- `/models` and `/v1/models` share the unified catalog;
- Codex `client_version` uses the same IDs without an upstream probe;
- static-key model discovery remains unchanged;
- all three root aliases exist and old `/v1` routes remain;
- frontend gateway examples are bare while `/v1beta` and `ccswitch://v1/import` remain;
- no POST routing or scheduler implementation changed.

- [ ] **Step 7: Verify repository state and invoke the finishing workflow**

Run:

```bash
git status --short
git log --oneline --decorate f7ed1ba92e7e89ed307c36ed09bde0adf483941d..HEAD
```

Expected: the worktree is clean and the log contains the design, resolver,
catalog/cache, unified response, aliases, and client-example commits. Then use
`superpowers:finishing-a-development-branch` and present its integration
options without merging, pushing, or deleting anything automatically.

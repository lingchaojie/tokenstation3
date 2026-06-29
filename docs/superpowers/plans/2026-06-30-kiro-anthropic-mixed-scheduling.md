# Kiro ↔ Anthropic 混合调度 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 kiro 账号通过 `mixed_scheduling` 开关加入 anthropic 分组、与 anthropic 账号混合调度，用户用原 key 无感命中 kiro。

**Architecture:** 推广已有的 Antigravity 混合调度机制到 Kiro：调度准入放行 mixed kiro 账号进 anthropic 池；kiro 专属配置（endpoint/cache/auto-sticky/TTL）改为账号级 resolver（组优先、账号兜底）保真；auto-sticky 稳定 hash 门控放宽到含 mixed kiro 的混合组。转发链路与选号算法本身不动。

**Tech Stack:** Go (backend, `internal/service`、`internal/repository`)、Vue 3 + TS（frontend）。测试用 `//go:build unit` + `testify/require`。

**Spec:** `docs/superpowers/specs/2026-06-30-kiro-anthropic-mixed-scheduling-design.md`

**约束：** kiro 只混入 anthropic（绝不 gemini）；强制平台模式不走混合调度；原生 kiro 组行为零变化。

**全局验证命令：**
- 后端单测：`cd backend && go test -tags unit ./internal/service/... ./internal/repository/...`
- 后端编译：`cd backend && go build ./...`
- 前端：`cd frontend && pnpm typecheck`

---

## Section 1 — 调度准入

### Task 1: 账号级 kiro 混合调度开关 + 目标平台感知判定

**Files:**
- Modify: `backend/internal/service/account.go` (在 `IsMixedSchedulingEnabled` 附近，约 1381-1396 行之后)
- Test: `backend/internal/service/account_kiro_mixed_scheduling_test.go` (Create)

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/account_kiro_mixed_scheduling_test.go`:

```go
//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsKiroMixedSchedulingEnabled(t *testing.T) {
	kiroOn := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	kiroOff := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": false}}
	kiroNone := &Account{Platform: PlatformKiro}
	anthropic := &Account{Platform: PlatformAnthropic, Extra: map[string]any{"mixed_scheduling": true}}

	require.True(t, kiroOn.IsKiroMixedSchedulingEnabled())
	require.False(t, kiroOff.IsKiroMixedSchedulingEnabled())
	require.False(t, kiroNone.IsKiroMixedSchedulingEnabled())
	require.False(t, anthropic.IsKiroMixedSchedulingEnabled())
}

func TestAccountEligibleForMixedPlatform(t *testing.T) {
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	anti := &Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}

	require.True(t, accountEligibleForMixedPlatform(kiro, PlatformAnthropic))
	require.False(t, accountEligibleForMixedPlatform(kiro, PlatformGemini)) // kiro 绝不进 gemini
	require.True(t, accountEligibleForMixedPlatform(anti, PlatformAnthropic))
	require.True(t, accountEligibleForMixedPlatform(anti, PlatformGemini))
	require.False(t, accountEligibleForMixedPlatform(&Account{Platform: PlatformOpenAI}, PlatformAnthropic))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestIsKiroMixedSchedulingEnabled|TestAccountEligibleForMixedPlatform' -v`
Expected: FAIL（`IsKiroMixedSchedulingEnabled`/`accountEligibleForMixedPlatform` undefined）

- [ ] **Step 3: 实现**

在 `account.go` 的 `IsMixedSchedulingEnabled()` 之后新增：

```go
// IsKiroMixedSchedulingEnabled 检查 kiro 账户是否启用混合调度（可参与 anthropic 分组）。
func (a *Account) IsKiroMixedSchedulingEnabled() bool {
	if a == nil || a.Platform != PlatformKiro || a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["mixed_scheduling"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// accountEligibleForMixedPlatform 判定账号能否在 targetPlatform 的混合池中被调度。
// antigravity → anthropic+gemini；kiro → 仅 anthropic。
func accountEligibleForMixedPlatform(acc *Account, targetPlatform string) bool {
	if acc == nil {
		return false
	}
	switch acc.Platform {
	case PlatformAntigravity:
		return acc.IsMixedSchedulingEnabled()
	case PlatformKiro:
		return targetPlatform == PlatformAnthropic && acc.IsKiroMixedSchedulingEnabled()
	default:
		return false
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestIsKiroMixedSchedulingEnabled|TestAccountEligibleForMixedPlatform' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/account.go backend/internal/service/account_kiro_mixed_scheduling_test.go
git commit -m "feat: add kiro mixed-scheduling eligibility helpers"
```

### Task 2: 混合候选平台列表 + 准入判定（gateway 路径）

**Files:**
- Modify: `backend/internal/service/gateway_service.go` (2635-2677 `listSchedulableAccounts` 的 useMixed 分支；2726-2737 `isAccountAllowedForPlatform`)
- Test: `backend/internal/service/account_kiro_mixed_scheduling_test.go` (追加)

引入纯函数 `mixedSchedulingPlatforms(platform)`，gateway 与 snapshot 共用（DRY）。

- [ ] **Step 1: 写失败测试**（追加到 `account_kiro_mixed_scheduling_test.go`）

```go
func TestMixedSchedulingPlatforms(t *testing.T) {
	require.Equal(t, []string{PlatformAnthropic, PlatformAntigravity, PlatformKiro}, mixedSchedulingPlatforms(PlatformAnthropic))
	require.Equal(t, []string{PlatformGemini, PlatformAntigravity}, mixedSchedulingPlatforms(PlatformGemini))
}

func TestIsAccountAllowedForPlatform_Kiro(t *testing.T) {
	s := &GatewayService{}
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	// useMixed=true, anthropic 池：放行
	require.True(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, true))
	// gemini 池：拒绝
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformGemini, true))
	// 非混合（强制平台）：拒绝
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, false))
	// 未开 mixed 的 kiro：拒绝
	kiroOff := &Account{Platform: PlatformKiro}
	require.False(t, s.isAccountAllowedForPlatform(kiroOff, PlatformAnthropic, true))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestMixedSchedulingPlatforms|TestIsAccountAllowedForPlatform_Kiro' -v`
Expected: FAIL（`mixedSchedulingPlatforms` undefined；kiro 当前被拒）

- [ ] **Step 3: 实现**

(a) 在 `account.go` 的 `accountEligibleForMixedPlatform` 之后新增纯函数：

```go
// mixedSchedulingPlatforms 返回 targetPlatform 的混合候选平台列表。
// anthropic → [anthropic, antigravity, kiro]；gemini → [gemini, antigravity]。
func mixedSchedulingPlatforms(platform string) []string {
	if platform == PlatformAnthropic {
		return []string{PlatformAnthropic, PlatformAntigravity, PlatformKiro}
	}
	return []string{platform, PlatformAntigravity}
}
```

(b) `gateway_service.go` 的 `listSchedulableAccounts` useMixed 分支（约 2637 行）：

将
```go
		platforms := []string{platform, PlatformAntigravity}
```
改为
```go
		platforms := mixedSchedulingPlatforms(platform)
```

并将过滤循环（约 2654-2660 行）：
```go
		filtered := make([]Account, 0, len(accounts))
		for _, acc := range accounts {
			if acc.Platform == PlatformAntigravity && !acc.IsMixedSchedulingEnabled() {
				continue
			}
			filtered = append(filtered, acc)
		}
```
改为
```go
		filtered := make([]Account, 0, len(accounts))
		for _, acc := range accounts {
			if (acc.Platform == PlatformAntigravity || acc.Platform == PlatformKiro) && !accountEligibleForMixedPlatform(&acc, platform) {
				continue
			}
			filtered = append(filtered, acc)
		}
```

(c) `isAccountAllowedForPlatform`（2726-2737 行）整体替换为：
```go
func (s *GatewayService) isAccountAllowedForPlatform(account *Account, platform string, useMixed bool) bool {
	if account == nil {
		return false
	}
	if account.Platform == platform {
		return true
	}
	if !useMixed {
		return false
	}
	return accountEligibleForMixedPlatform(account, platform)
}
```

- [ ] **Step 4: 跑测试确认通过 + 全量编译**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestMixedSchedulingPlatforms|TestIsAccountAllowedForPlatform_Kiro' -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/account.go backend/internal/service/gateway_service.go backend/internal/service/account_kiro_mixed_scheduling_test.go
git commit -m "feat: admit mixed kiro accounts into anthropic scheduling pool"
```

### Task 3: 快照路径准入 + 重建（生产路径）

**Files:**
- Modify: `backend/internal/service/scheduler_snapshot_service.go` (679-700 `loadAccountsFromDB` useMixed 分支；499-510 `rebuildByAccount`)
- Test: `backend/internal/service/scheduler_snapshot_kiro_mixed_test.go` (Create)

> 注：`loadAccountsFromDB`/`rebuildByAccount` 依赖 `accountRepo`/重建管线，纯函数 `mixedSchedulingPlatforms` 已在 Task 2 测过。本任务测试聚焦"重建平台集合包含 anthropic"的判定纯逻辑，重逻辑由 Section 4 集成测试覆盖。

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/scheduler_snapshot_kiro_mixed_test.go`:

```go
//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mixed kiro 账号变更时需重建的平台桶集合：必须含 anthropic、不含 gemini。
func TestRebuildPlatformsForMixedAccount(t *testing.T) {
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	require.Equal(t, []string{PlatformAnthropic}, rebuildPlatformsForMixedAccount(kiro))

	anti := &Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}
	require.Equal(t, []string{PlatformAnthropic, PlatformGemini}, rebuildPlatformsForMixedAccount(anti))

	require.Nil(t, rebuildPlatformsForMixedAccount(&Account{Platform: PlatformKiro})) // 未开 mixed
	require.Nil(t, rebuildPlatformsForMixedAccount(&Account{Platform: PlatformOpenAI}))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run TestRebuildPlatformsForMixedAccount -v`
Expected: FAIL（`rebuildPlatformsForMixedAccount` undefined）

- [ ] **Step 3: 实现**

(a) 在 `scheduler_snapshot_service.go` 顶部（`rebuildByAccount` 之前）新增纯函数：

```go
// rebuildPlatformsForMixedAccount 返回混合调度账号变更时需额外重建的平台桶。
// antigravity → anthropic+gemini；kiro → 仅 anthropic；否则 nil。
func rebuildPlatformsForMixedAccount(account *Account) []string {
	if account == nil {
		return nil
	}
	switch account.Platform {
	case PlatformAntigravity:
		if account.IsMixedSchedulingEnabled() {
			return []string{PlatformAnthropic, PlatformGemini}
		}
	case PlatformKiro:
		if account.IsKiroMixedSchedulingEnabled() {
			return []string{PlatformAnthropic}
		}
	}
	return nil
}
```

(b) `rebuildByAccount`（499-510 行）将现有 antigravity 专属块：
```go
	if account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled() {
		if err := s.rebuildBucketsForPlatform(ctx, PlatformAnthropic, groupIDs, reason, seen); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := s.rebuildBucketsForPlatform(ctx, PlatformGemini, groupIDs, reason, seen); err != nil && firstErr == nil {
			firstErr = err
		}
	}
```
替换为：
```go
	for _, mixedPlatform := range rebuildPlatformsForMixedAccount(account) {
		if err := s.rebuildBucketsForPlatform(ctx, mixedPlatform, groupIDs, reason, seen); err != nil && firstErr == nil {
			firstErr = err
		}
	}
```

(c) `loadAccountsFromDB` useMixed 分支（约 680 行）：
将
```go
		platforms := []string{bucket.Platform, PlatformAntigravity}
```
改为
```go
		platforms := mixedSchedulingPlatforms(bucket.Platform)
```
并将过滤循环（约 693-699 行）：
```go
		filtered := make([]Account, 0, len(accounts))
		for _, acc := range accounts {
			if acc.Platform == PlatformAntigravity && !acc.IsMixedSchedulingEnabled() {
				continue
			}
			filtered = append(filtered, acc)
		}
```
改为：
```go
		filtered := make([]Account, 0, len(accounts))
		for _, acc := range accounts {
			if (acc.Platform == PlatformAntigravity || acc.Platform == PlatformKiro) && !accountEligibleForMixedPlatform(&acc, bucket.Platform) {
				continue
			}
			filtered = append(filtered, acc)
		}
```

- [ ] **Step 4: 跑测试确认通过 + 编译**

Run: `cd backend && go test -tags unit ./internal/service/ -run TestRebuildPlatformsForMixedAccount -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/scheduler_snapshot_service.go backend/internal/service/scheduler_snapshot_kiro_mixed_test.go
git commit -m "feat: rebuild anthropic snapshot bucket for mixed kiro accounts"
```

## Section 2 — 账号级 Kiro 配置（保真）

### Task 4: 账号级 kiro 配置读取方法

**Files:**
- Modify: `backend/internal/service/account.go` (在 `IsKiroMixedSchedulingEnabled` 附近)
- Test: `backend/internal/service/account_kiro_config_test.go` (Create)

复用 group 既有的 normalize 函数（`normalizeKiroStickySessionTTLSeconds`、`normalizeKiroCacheEmulationRatio`、endpoint mode 取值常量），保证账号级与组级语义一致（DRY）。

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/account_kiro_config_test.go`:

```go
//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountKiroConfigReaders(t *testing.T) {
	acc := &Account{Platform: PlatformKiro, Extra: map[string]any{
		"kiro_endpoint_mode":             "krs",
		"kiro_cache_emulation_enabled":   true,
		"kiro_cache_emulation_ratio":     json.Number("0.5"),
		"kiro_auto_sticky_enabled":       true,
		"kiro_sticky_session_ttl_seconds": json.Number("1800"),
	}}
	require.Equal(t, KiroEndpointModeKRS, acc.KiroEndpointMode())
	require.True(t, acc.KiroCacheEmulationEnabled())
	require.Equal(t, 0.5, acc.KiroCacheEmulationRatio())
	require.True(t, acc.KiroAutoStickyEnabled())
	require.Equal(t, 1800, acc.KiroStickySessionTTLSeconds())
}

func TestAccountKiroConfigDefaults(t *testing.T) {
	acc := &Account{Platform: PlatformKiro} // Extra 为 nil
	require.Equal(t, KiroEndpointModeQ, acc.KiroEndpointMode())            // 默认 q
	require.False(t, acc.KiroCacheEmulationEnabled())
	require.Equal(t, float64(0), acc.KiroCacheEmulationRatio())
	require.False(t, acc.KiroAutoStickyEnabled())
	require.Equal(t, DefaultKiroStickySessionTTLSeconds, acc.KiroStickySessionTTLSeconds()) // 默认 3600

	// 未知 endpoint mode 兜底 q
	bad := &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_endpoint_mode": "xyz"}}
	require.Equal(t, KiroEndpointModeQ, bad.KiroEndpointMode())
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestAccountKiroConfig' -v`
Expected: FAIL（方法 undefined）

- [ ] **Step 3: 实现**（在 `account.go` 的 `IsKiroMixedSchedulingEnabled` 之后新增）

```go
// kiroExtraBool 从 Extra 读 bool。
func (a *Account) kiroExtraBool(key string) bool {
	if a == nil || a.Extra == nil {
		return false
	}
	if v, ok := a.Extra[key].(bool); ok {
		return v
	}
	return false
}

// kiroExtraFloat 从 Extra 读 float（兼容 json.Number / float64 / int）。
func (a *Account) kiroExtraFloat(key string) float64 {
	if a == nil || a.Extra == nil {
		return 0
	}
	switch v := a.Extra[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// kiroExtraInt 从 Extra 读 int（兼容 json.Number / float64 / int）。
func (a *Account) kiroExtraInt(key string) int {
	return int(a.kiroExtraFloat(key))
}

// KiroEndpointMode 返回账号级 Kiro endpoint 模式，默认 q，未知值兜底 q。
func (a *Account) KiroEndpointMode() string {
	if a != nil && a.Extra != nil {
		if s, ok := a.Extra["kiro_endpoint_mode"].(string); ok && s == KiroEndpointModeKRS {
			return KiroEndpointModeKRS
		}
	}
	return KiroEndpointModeQ
}

// KiroCacheEmulationEnabled 账号级缓存模拟开关（需 ratio>0 才真正生效，与 group 语义一致）。
func (a *Account) KiroCacheEmulationEnabled() bool {
	return a.kiroExtraBool("kiro_cache_emulation_enabled") && a.KiroCacheEmulationRatio() > 0
}

// KiroCacheEmulationRatio 账号级缓存模拟比率，归一化到 [0,1]。
func (a *Account) KiroCacheEmulationRatio() float64 {
	if !a.kiroExtraBool("kiro_cache_emulation_enabled") {
		return 0
	}
	return normalizeKiroCacheEmulationRatio(a.kiroExtraFloat("kiro_cache_emulation_ratio"))
}

// KiroAutoStickyEnabled 账号级 auto-sticky 开关。
func (a *Account) KiroAutoStickyEnabled() bool {
	return a.kiroExtraBool("kiro_auto_sticky_enabled")
}

// KiroStickySessionTTLSeconds 账号级粘性 TTL，归一化（0 → 默认 3600，含上下限）。
func (a *Account) KiroStickySessionTTLSeconds() int {
	return normalizeKiroStickySessionTTLSeconds(a.kiroExtraInt("kiro_sticky_session_ttl_seconds"))
}
```

需确认 `account.go` 已 import `encoding/json`（kiro credit price 校验已用，通常已 import；若无则补）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestAccountKiroConfig' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/account.go backend/internal/service/account_kiro_config_test.go
git commit -m "feat: add account-level kiro config readers"
```

### Task 5: kiro 配置 resolver（组优先、账号兜底）+ 接入消费点

**Files:**
- Create: `backend/internal/service/kiro_config_resolver.go`
- Modify: `backend/internal/service/kiro_runtime.go:551-559` (`kiroEndpointModeForRequest`)
- Modify: `backend/internal/service/kiro_cache_emulation.go:53-57` (`buildKiroCacheEmulationUsage`)
- Test: `backend/internal/service/kiro_config_resolver_test.go` (Create)

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/kiro_config_resolver_test.go`:

```go
//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveKiroEndpointMode(t *testing.T) {
	kiroGroup := &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_endpoint_mode": "krs",
	}}
	anthropicAcct := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	// 原生 kiro 组：取组配置（行为不变）
	require.Equal(t, KiroEndpointModeKRS, resolveKiroEndpointMode(mixedKiroAcct, kiroGroup))
	// kiro 账号混入 anthropic 组：取账号配置
	require.Equal(t, KiroEndpointModeKRS, resolveKiroEndpointMode(mixedKiroAcct, anthropicGroup))
	// anthropic 账号：永远 q（短路）
	require.Equal(t, KiroEndpointModeQ, resolveKiroEndpointMode(anthropicAcct, anthropicGroup))
}

func TestResolveKiroCacheEmulation(t *testing.T) {
	kiroGroup := &Group{Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": 0.5,
	}}

	enabled, ratio := resolveKiroCacheEmulation(mixedKiroAcct, kiroGroup)
	require.True(t, enabled)
	require.Equal(t, float64(1), ratio) // 原生组取组配置

	enabled, ratio = resolveKiroCacheEmulation(mixedKiroAcct, anthropicGroup)
	require.True(t, enabled)
	require.Equal(t, 0.5, ratio) // 混入组取账号配置

	enabled, _ = resolveKiroCacheEmulation(&Account{Platform: PlatformAnthropic}, anthropicGroup)
	require.False(t, enabled) // anthropic 账号短路
}

func TestResolveKiroStickyTTL(t *testing.T) {
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	kiroGroup := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: 1800}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_sticky_session_ttl_seconds": 1200,
	}}
	require.Equal(t, 1200, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, anthropicGroup)) // 混入组取账号
	require.Equal(t, 1800, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, kiroGroup))      // 原生组取组
	require.Equal(t, 0, resolveKiroStickySessionTTLSeconds(&Account{Platform: PlatformAnthropic}, anthropicGroup)) // 短路
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestResolveKiro' -v`
Expected: FAIL（resolver undefined）

- [ ] **Step 3: 实现 resolver**

Create `backend/internal/service/kiro_config_resolver.go`:

```go
package service

// kiro 配置 resolver：组优先、账号兜底。
// - 原生 kiro 组（isKiroGroup）：取 group 配置，行为与改动前完全一致。
// - kiro direct 账号混入非 kiro 组：取账号自身 Extra 配置。
// - 其它（如 anthropic 账号）：返回安全默认（短路）。

func resolveKiroEndpointMode(account *Account, group *Group) string {
	if isKiroGroup(group) {
		return group.EffectiveKiroEndpointMode()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroEndpointMode()
	}
	return KiroEndpointModeQ
}

func resolveKiroCacheEmulation(account *Account, group *Group) (enabled bool, ratio float64) {
	if isKiroGroup(group) {
		return group.EffectiveKiroCacheEmulationEnabled(), group.EffectiveKiroCacheEmulationRatio()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroCacheEmulationEnabled(), account.KiroCacheEmulationRatio()
	}
	return false, 0
}

func resolveKiroStickySessionTTLSeconds(account *Account, group *Group) int {
	if isKiroGroup(group) {
		return group.EffectiveKiroStickySessionTTLSeconds()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroStickySessionTTLSeconds()
	}
	return 0
}
```

> 本计划的 auto-sticky 门控由 Task 6 的分组级标记驱动（hash 阶段无 account）；endpoint/cache 由本任务的 resolver 驱动；TTL 由 Task 8 复用 `resolveKiroStickySessionTTLSeconds`。故此处不单列 auto-sticky resolver。

- [ ] **Step 4: 接入消费点**

(a) `kiro_runtime.go` `kiroEndpointModeForRequest`（551-559 行）末行：
```go
	return parsed.Group.EffectiveKiroEndpointMode()
```
改为
```go
	return resolveKiroEndpointMode(account, parsed.Group)
```
(API Key 强制 Q 的前置判断保留不动。)

(b) `kiro_cache_emulation.go` `buildKiroCacheEmulationUsage`（53-72 行）。当前：
```go
	NormalizeGroupRuntimeFields(group)
	if group == nil || !group.EffectiveKiroCacheEmulationEnabled() || account == nil || account.ID <= 0 || len(body) == 0 {
		return nil
	}
	...
	ratio := group.EffectiveKiroCacheEmulationRatio()
```
改为：
```go
	NormalizeGroupRuntimeFields(group)
	cacheEnabled, cacheRatio := resolveKiroCacheEmulation(account, group)
	if !cacheEnabled || account == nil || account.ID <= 0 || len(body) == 0 {
		return nil
	}
	...
	ratio := cacheRatio
```
（即用 resolver 取代两处 `group.EffectiveKiroCacheEmulation*()` 调用；保留 `NormalizeGroupRuntimeFields(group)` 与其余逻辑。）

- [ ] **Step 5: 跑测试 + 编译**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestResolveKiro' -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 6: 提交**

```bash
git add backend/internal/service/kiro_config_resolver.go backend/internal/service/kiro_config_resolver_test.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_cache_emulation.go
git commit -m "feat: resolve kiro runtime config from account when mixed-scheduled"
```

## Section 3 — auto-sticky 稳定 hash（方案 A）

> 设计：分组级标记 `HasMixedKiroAutoStickyAccount` = "该组存在可调度、`mixed_scheduling=true` 且 `kiro_auto_sticky_enabled=true` 的 kiro 账号"。把 auto-sticky 折进标记，使 hash 阶段无需账号即可判定。在 auth-snapshot 构建时算一次（cache miss 才查 DB），bounded-stale 但 benign（仅影响裸客户端 auto-sticky 优化，自愈、不影响正确性）。

### Task 6: 分组级 `HasMixedKiroAutoStickyAccount` 标记 + 仓储查询 + 快照透传

**Files:**
- Modify: `backend/internal/service/group.go` (Group 结构体新增字段)
- Modify: `backend/internal/service/group_service.go` (`GroupRepository` 接口新增方法)
- Modify: `backend/internal/repository/group_repo.go` (实现)
- Modify: `backend/internal/service/api_key_auth_cache.go` (`APIKeyAuthGroupSnapshot` 新增字段)
- Modify: `backend/internal/service/api_key_auth_cache_impl.go` (build 时计算 + restore 时透传)
- Test: `backend/internal/repository/group_mixed_kiro_test.go` (Create, integration)

- [ ] **Step 1: Group 结构体新增字段**

`group.go` 在 `AccountGroups []AccountGroup` 之前新增（约 84 行附近）：
```go
	// HasMixedKiroAutoStickyAccount 标记该组是否存在可调度、启用 mixed_scheduling
	// 且 kiro_auto_sticky_enabled 的 kiro 账号。仅用于 auto-sticky 稳定 hash 门控。
	// 在 auth-snapshot 构建时计算；bounded-stale（随 auth 缓存 TTL）。
	HasMixedKiroAutoStickyAccount bool
```

- [ ] **Step 2: 接口新增方法**

`group_service.go` 的 `GroupRepository` 接口（`GetAccountCount` 行之后）新增：
```go
	// HasSchedulableMixedKiroStickyAccount 报告分组内是否存在可调度、启用 mixed_scheduling
	// 且 kiro_auto_sticky_enabled 的 kiro 账号。
	HasSchedulableMixedKiroStickyAccount(ctx context.Context, groupID int64) (bool, error)
```

- [ ] **Step 3: 写失败测试**

Create `backend/internal/repository/group_mixed_kiro_test.go`（参照同目录现有 integration 测试的 suite 风格，如 `gateway_routing_integration_test.go`；用真实 DB fixture）：
```go
//go:build integration

package repository_test

// TestHasSchedulableMixedKiroStickyAccount:
//   - 组内有 kiro 账号 extra{mixed_scheduling:true, kiro_auto_sticky_enabled:true}, schedulable → true
//   - 仅 mixed_scheduling:true 但 auto_sticky 缺失 → false
//   - kiro 账号 schedulable=false → false
//   - 组内只有 anthropic 账号 → false
// 断言 r.HasSchedulableMixedKiroStickyAccount(ctx, groupID) 返回值。
// （按现有 integration suite 的建表/插入 helper 编写具体用例。）
```

- [ ] **Step 4: 实现仓储方法**

`group_repo.go` 在 `GetAccountCount` 之后新增（参照其 SQL 风格）：
```go
func (r *groupRepository) HasSchedulableMixedKiroStickyAccount(ctx context.Context, groupID int64) (bool, error) {
	var cnt int64
	err := scanSingleRow(ctx, r.sql,
		`SELECT COUNT(*)
		 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
		 WHERE ag.group_id = $1
		   AND a.deleted_at IS NULL
		   AND a.platform = 'kiro'
		   AND a.status = 'active'
		   AND a.schedulable = true
		   AND COALESCE((a.extra->>'mixed_scheduling')::boolean, false) = true
		   AND COALESCE((a.extra->>'kiro_auto_sticky_enabled')::boolean, false) = true`,
		[]any{groupID}, &cnt)
	return cnt > 0, err
}
```
> 注：`'kiro'`/`'active'` 字面量与 `domain.PlatformKiro`/`StatusActive` 同值；如同文件已有平台常量引用风格，按其约定替换。

- [ ] **Step 5: 快照结构体新增字段**

`api_key_auth_cache.go` 的 `APIKeyAuthGroupSnapshot`（`KiroEndpointMode` 字段附近，约 105 行）新增：
```go
	HasMixedKiroAutoStickyAccount bool `json:"has_mixed_kiro_auto_sticky_account"`
```

- [ ] **Step 6: build 时计算 + restore 时透传**

`api_key_auth_cache_impl.go` build（`if apiKey.Group != nil {` 块内，`KiroEndpointMode:` 行之后，约 299 行）新增：
```go
				HasMixedKiroAutoStickyAccount: s.computeHasMixedKiroAutoSticky(ctx, apiKey.Group),
```
并在该文件新增 helper：
```go
// computeHasMixedKiroAutoSticky 仅对 anthropic 分组查询（kiro 只混入 anthropic）；
// 其它平台或查询失败返回 false（benign：退回 fallback hash）。
func (s *APIKeyService) computeHasMixedKiroAutoSticky(ctx context.Context, group *Group) bool {
	if group == nil || group.Platform != PlatformAnthropic || group.ID <= 0 || s.groupRepo == nil {
		return false
	}
	ok, err := s.groupRepo.HasSchedulableMixedKiroStickyAccount(ctx, group.ID)
	if err != nil {
		return false
	}
	return ok
}
```
restore（`snapshotToAPIKey` 的 `apiKey.Group = &Group{` 块内，`KiroEndpointMode:` 行之后）新增：
```go
			HasMixedKiroAutoStickyAccount: snapshot.Group.HasMixedKiroAutoStickyAccount,
```

- [ ] **Step 7: 编译 + integration 测试**

Run: `cd backend && go build ./... && go test -tags integration ./internal/repository/ -run TestHasSchedulableMixedKiroStickyAccount -v`
Expected: 编译通过；integration PASS（如本地无 DB，至少 `go build ./...` + `go vet` 通过，集成测试在 CI 跑）

- [ ] **Step 8: 提交**

```bash
git add backend/internal/service/group.go backend/internal/service/group_service.go backend/internal/repository/group_repo.go backend/internal/service/api_key_auth_cache.go backend/internal/service/api_key_auth_cache_impl.go backend/internal/repository/group_mixed_kiro_test.go
git commit -m "feat: expose group-level mixed-kiro auto-sticky flag via auth snapshot"
```

### Task 7: 放宽 `GenerateSessionHash` 稳定 hash 门控

**Files:**
- Modify: `backend/internal/service/gateway_service.go:809-838` (`GenerateSessionHash` 档位 2.5)
- Test: `backend/internal/service/gateway_session_hash_mixed_kiro_test.go` (Create)

把档位 2.5 的门控从 `isKiroGroup(parsed.Group)` 放宽为"原生 kiro 组 **或** 含 mixed kiro auto-sticky 账号的组"。注意语义差异：原生 kiro 组关闭 auto-sticky 时返回 `""`（维持现状）；混合组未命中则**继续往下走档位 3**（不能 return）。

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/gateway_session_hash_mixed_kiro_test.go`（复用同包既有 helper `anthropicSessionBody`/`msg`，定义于 `generate_session_hash_test.go`）:

```go
//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func mustParseMixedKiroRequest(t *testing.T, userMsg string, hasMixedKiro bool) *ParsedRequest {
	t.Helper()
	body := anthropicSessionBody("You are helpful", []any{msg("user", userMsg)}, "")
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformAnthropic, HasMixedKiroAutoStickyAccount: hasMixedKiro}
	parsed.SessionContext = &SessionContext{APIKeyID: 7}
	return parsed
}

// 含 mixed kiro auto-sticky 账号的 anthropic 组 + 裸客户端 → system-prompt 稳定 hash（跨轮一致）。
func TestGenerateSessionHash_MixedKiroStableHash(t *testing.T) {
	s := &GatewayService{}
	h1 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn one", true))
	h2 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn two", true))
	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2)
}

// 纯 anthropic 组（无 mixed kiro）裸客户端 → 档位 3 fallback（含全部 messages，逐轮变化，行为不变）。
func TestGenerateSessionHash_PlainAnthropicUnchanged(t *testing.T) {
	s := &GatewayService{}
	h1 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn one", false))
	h2 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn two different", false))
	require.NotEmpty(t, h1)
	require.NotEqual(t, h1, h2)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestGenerateSessionHash_MixedKiro|TestGenerateSessionHash_PlainAnthropic' -v`
Expected: `MixedKiro` FAIL（当前 isKiroGroup=false，混合组裸客户端走档位 3，h1≠h2）；`PlainAnthropic` PASS

- [ ] **Step 3: 实现**

`gateway_service.go` 档位 2.5（809-838 行）。当前：
```go
	if isKiroGroup(parsed.Group) {
		if !parsed.Group.EffectiveKiroAutoStickyEnabled() {
			slog.Info("sticky.hash_source", "source", "kiro_auto_sticky_disabled")
			return ""
		}
		stableSeed := extractTextFromSystemRaw(parsed.SystemRaw())
		source := "kiro_system_prompt"
		if stableSeed == "" {
			stableSeed = extractFirstUserMessageTextFromRaw(parsed.MessagesRaw())
			source = "kiro_first_user_message"
		}
		if stableSeed != "" {
			var sb strings.Builder
			if parsed.SessionContext != nil {
				_, _ = sb.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
				_, _ = sb.WriteString("|")
			}
			_, _ = sb.WriteString(stableSeed)
			hash := s.hashContent(sb.String())
			slog.Info("sticky.hash_source", "source", source, "hash", hash, "seed_len", len(stableSeed))
			return hash
		}
		return ""
	}
```
替换为：
```go
	// 原生 kiro 组：维持原语义（关闭 auto-sticky 时返回 ""）。
	// 含 mixed kiro auto-sticky 账号的 anthropic 组：尝试稳定 hash；未命中则继续往下走档位 3。
	nativeKiro := isKiroGroup(parsed.Group)
	mixedKiroSticky := parsed.Group != nil && parsed.Group.HasMixedKiroAutoStickyAccount
	if nativeKiro || mixedKiroSticky {
		autoStickyOn := mixedKiroSticky || (nativeKiro && parsed.Group.EffectiveKiroAutoStickyEnabled())
		if autoStickyOn {
			stableSeed := extractTextFromSystemRaw(parsed.SystemRaw())
			source := "kiro_system_prompt"
			if stableSeed == "" {
				stableSeed = extractFirstUserMessageTextFromRaw(parsed.MessagesRaw())
				source = "kiro_first_user_message"
			}
			if stableSeed != "" {
				var sb strings.Builder
				if parsed.SessionContext != nil {
					_, _ = sb.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
					_, _ = sb.WriteString("|")
				}
				_, _ = sb.WriteString(stableSeed)
				hash := s.hashContent(sb.String())
				slog.Info("sticky.hash_source", "source", source, "hash", hash, "seed_len", len(stableSeed))
				return hash
			}
		}
		// 原生 kiro 组（无论开关/seed 是否命中）维持原行为：返回 ""，不落档位 3。
		if nativeKiro {
			if !autoStickyOn {
				slog.Info("sticky.hash_source", "source", "kiro_auto_sticky_disabled")
			}
			return ""
		}
		// 混合组未命中稳定 hash → 继续往下走档位 3 fallback。
	}
```

- [ ] **Step 4: 跑测试确认通过 + 回归**

Run: `cd backend && go test -tags unit ./internal/service/ -run 'TestGenerateSessionHash' -v && go build ./...`
Expected: 全部 PASS（含原有 `GenerateSessionHash` 测试不回归）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/gateway_service.go backend/internal/service/gateway_session_hash_mixed_kiro_test.go
git commit -m "feat: extend kiro stable session hash to mixed anthropic groups"
```

### Task 8: 账号级粘性 TTL（bind 时按 resolver 解析）

**Files:**
- Modify: `backend/internal/service/gateway_service.go:897-916` (`BindStickySessionForGroup` + `stickySessionTTLForGroup`)
- Modify: `backend/internal/handler/gateway_handler.go` (bind 调用点，约 737、~455 行)
- Test: `backend/internal/service/gateway_sticky_ttl_test.go` (Create)

混合 kiro 账号在 anthropic 组里的粘性 TTL 应取账号配置。bind 时已持有 `account`，新增 account-aware 重载。

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/gateway_sticky_ttl_test.go`:

```go
//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStickySessionTTLForAccountGroup(t *testing.T) {
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	kiroGroup := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: 1800}
	mixedKiro := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_sticky_session_ttl_seconds": 1200,
	}}
	anthropicAcct := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	// mixed kiro 混入 anthropic 组 → 账号 TTL 1200s
	require.Equal(t, 1200*time.Second, stickySessionTTLForAccountGroup(mixedKiro, anthropicGroup))
	// 原生 kiro 组 → 组 TTL 1800s
	require.Equal(t, 1800*time.Second, stickySessionTTLForAccountGroup(mixedKiro, kiroGroup))
	// anthropic 账号 → 默认 stickySessionTTL
	require.Equal(t, stickySessionTTL, stickySessionTTLForAccountGroup(anthropicAcct, anthropicGroup))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test -tags unit ./internal/service/ -run TestStickySessionTTLForAccountGroup -v`
Expected: FAIL（`stickySessionTTLForAccountGroup` undefined）

- [ ] **Step 3: 实现**

(a) `gateway_service.go` 在 `stickySessionTTLForGroup`（911-916 行）之后新增（复用 Task 5 的 `resolveKiroStickySessionTTLSeconds`，DRY）：
```go
// stickySessionTTLForAccountGroup 解析粘性 TTL：原生 kiro 组取组配置；
// mixed kiro 账号混入非 kiro 组取账号配置；其它回退默认 stickySessionTTL。
func stickySessionTTLForAccountGroup(account *Account, group *Group) time.Duration {
	if seconds := resolveKiroStickySessionTTLSeconds(account, group); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return stickySessionTTL
}
```

(b) 新增 account-aware bind 重载（在 `BindStickySessionForGroup` 之后）：
```go
// BindStickySessionForAccountGroup 按所选账号+分组解析 TTL 后绑定粘性会话。
func (s *GatewayService) BindStickySessionForAccountGroup(ctx context.Context, groupID *int64, sessionHash string, account *Account, group *Group) error {
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	return s.BindStickySessionWithTTL(ctx, groupID, sessionHash, accountID, stickySessionTTLForAccountGroup(account, group))
}
```

(c) `gateway_handler.go` 主路径 bind 调用点（约 737 行）：
```go
			if err := h.gatewayService.BindStickySessionForGroup(c.Request.Context(), currentAPIKey.GroupID, sessionKey, account.ID, currentAPIKey.Group); err != nil {
```
改为
```go
			if err := h.gatewayService.BindStickySessionForAccountGroup(c.Request.Context(), currentAPIKey.GroupID, sessionKey, account, currentAPIKey.Group); err != nil {
```
> 仅替换 anthropic 主路径（非 gemini）的 bind 点。gemini 块（约 455 行）维持 `BindStickySessionForGroup` 不变（kiro 不进 gemini）。用 `grep -n "BindStickySessionForGroup" internal/handler/gateway_handler.go` 确认行号后改主路径那一处。

- [ ] **Step 4: 跑测试 + 编译**

Run: `cd backend && go test -tags unit ./internal/service/ -run TestStickySessionTTLForAccountGroup -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/gateway_service.go backend/internal/handler/gateway_handler.go backend/internal/service/gateway_sticky_ttl_test.go
git commit -m "feat: resolve sticky TTL from account for mixed kiro scheduling"
```

## Section 4 — 前端

> 前端无单测 harness 要求，验证靠 `pnpm typecheck` + 手动核对。每个 step 给出确切锚点与 `extra` 键。

### Task 9: 账号弹窗暴露 kiro mixed 开关 + 4 项 kiro 配置

**Files:**
- Modify: `frontend/src/components/account/CreateAccountModal.vue` (toggle 区 ~3173；`buildAntigravityExtra` ~4025；reset ~3918)
- Modify: `frontend/src/components/account/EditAccountModal.vue` (toggle 区 ~2481；extra 写入 ~4314；load ~3225)

参照 `GroupsView.vue` 的 kiro 字段（`kiro_endpoint_mode` 用 `kiroEndpointModeOptions`、`kiro_cache_emulation_enabled`+`kiro_cache_emulation_ratio`、`kiro_auto_sticky_enabled`、`kiro_sticky_session_ttl_seconds`）。

- [ ] **Step 1: CreateAccountModal — 显示开关与配置**

将 mixed scheduling toggle 的 `v-if="form.platform === 'antigravity'"`（3174 行）改为 `v-if="form.platform === 'antigravity' || form.platform === 'kiro'"`。

在该 toggle 块之后新增（仅 `v-if="form.platform === 'kiro' && mixedScheduling"`）4 个 kiro 配置输入：`kiroEndpointMode`(select q/krs)、`kiroCacheEmulationEnabled`(checkbox) + `kiroCacheEmulationRatio`(number, 仅 enabled 时显示)、`kiroAutoStickyEnabled`(checkbox)、`kiroStickyTtlSeconds`(number)。markup 复制 GroupsView.vue 596-655 行的结构，`v-model` 绑到新增的 `ref`。

新增 refs（`mixedScheduling` 附近 ~3918）：
```ts
const kiroEndpointMode = ref<'q' | 'krs'>('q')
const kiroCacheEmulationEnabled = ref(false)
const kiroCacheEmulationRatio = ref(1)
const kiroAutoStickyEnabled = ref(true)
const kiroStickyTtlSeconds = ref(3600)
```

- [ ] **Step 2: CreateAccountModal — 写入 extra**

`buildAntigravityExtra`（4025 行）改名/扩展为按平台构建。新增 `buildKiroMixedExtra`：
```ts
function buildKiroMixedExtra(): Record<string, unknown> | undefined {
  if (!mixedScheduling.value) return undefined
  return {
    mixed_scheduling: true,
    kiro_endpoint_mode: kiroEndpointMode.value,
    kiro_cache_emulation_enabled: kiroCacheEmulationEnabled.value,
    kiro_cache_emulation_ratio: kiroCacheEmulationRatio.value,
    kiro_auto_sticky_enabled: kiroAutoStickyEnabled.value,
    kiro_sticky_session_ttl_seconds: kiroStickyTtlSeconds.value
  }
}
```
在组装 kiro 账号 payload 处（现有 `kiro_credit_unit_price_usd` 写入附近）合并 `buildKiroMixedExtra()` 的键到 `extra`（保留已有 `kiro_credit_unit_price_usd`）。

- [ ] **Step 3: EditAccountModal — 镜像 + 回填**

- toggle 区（2481 行）同样放宽到 kiro，并新增 4 项配置输入（同 Step 1）。
- 回填（`mixedScheduling.value = extra?.mixed_scheduling === true` 附近 ~3225）新增：
```ts
kiroEndpointMode.value = (extra?.kiro_endpoint_mode as 'q' | 'krs') ?? 'q'
kiroCacheEmulationEnabled.value = extra?.kiro_cache_emulation_enabled === true
kiroCacheEmulationRatio.value = typeof extra?.kiro_cache_emulation_ratio === 'number' ? extra.kiro_cache_emulation_ratio : 1
kiroAutoStickyEnabled.value = extra?.kiro_auto_sticky_enabled !== false
kiroStickyTtlSeconds.value = typeof extra?.kiro_sticky_session_ttl_seconds === 'number' ? extra.kiro_sticky_session_ttl_seconds : 3600
```
- 写入（`newExtra.mixed_scheduling` 附近 ~4318）：当平台是 kiro 且 mixedScheduling 开启，写入 Step 2 同样的 5 个键；关闭时 `delete` 这些键。

- [ ] **Step 4: typecheck**

Run: `cd frontend && pnpm typecheck`
Expected: 通过

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue
git commit -m "feat: expose kiro mixed-scheduling config in account modals"
```

### Task 10: GroupSelector 放行 kiro+mixed 选 anthropic 组 + i18n

**Files:**
- Modify: `frontend/src/components/common/GroupSelector.vue:91-103`
- Modify: `frontend/src/components/account/CreateAccountModal.vue` / `EditAccountModal.vue` (传 `:platform` + `:mixed-scheduling` 给 GroupSelector 的位置，~3236 / ~2541)
- Modify: `frontend/src/i18n/locales/zh.ts` / `en.ts` (按需补 kiro 文案)

- [ ] **Step 1: GroupSelector 过滤逻辑**

`filteredGroups`（91-103 行）的平台过滤分支改为：
```ts
  if (props.platform) {
    if (props.platform === 'antigravity' && props.mixedScheduling) {
      result = result.filter(
        (g) => g.platform === 'antigravity' || g.platform === 'anthropic' || g.platform === 'gemini'
      )
    } else if (props.platform === 'kiro' && props.mixedScheduling) {
      // kiro 混合调度只可选 anthropic 组（绝不含 gemini）
      result = result.filter((g) => g.platform === 'kiro' || g.platform === 'anthropic')
    } else {
      result = result.filter((g) => g.platform === props.platform)
    }
  }
```

- [ ] **Step 2: 确认 modal 已把 `mixedScheduling` 传给 GroupSelector**

`CreateAccountModal.vue` ~3236 与 `EditAccountModal.vue` ~2541 已有 `:mixed-scheduling="mixedScheduling"`（antigravity 用）。确认 kiro 平台时该值也生效（mixedScheduling ref 共用即可，无需改）。用 `grep -n ':mixed-scheduling' frontend/src/components/account/*.vue` 确认。

- [ ] **Step 3: i18n（如新增 kiro 专属说明文案）**

若 Task 9 的 kiro 配置项需要独立标题/提示，在 `zh.ts` / `en.ts` 的 `admin.accounts` 下复用现有 `mixedScheduling*` 文案；kiro endpoint mode 等可复用 GroupsView 已有的 `admin.groups.*` kiro 文案键。无新增逻辑则跳过。

- [ ] **Step 4: typecheck + 提交**

Run: `cd frontend && pnpm typecheck`
```bash
git add frontend/src/components/common/GroupSelector.vue frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts
git commit -m "feat: allow mixed kiro accounts to bind anthropic groups in selector"
```

## Section 5 — 集成验证与回归

### Task 11: 端到端回归 + 全量验证

**Files:**
- Test: `backend/internal/service/gateway_mixed_kiro_integration_test.go` (Create, `//go:build unit` 用 stub repo/snapshot)

- [ ] **Step 1: 关键回归测试 — 混合组选中 anthropic 账号不带 kiro 头**

复用同包既有 stub（`mockAccountRepoForGemini` 等，见 `account_kiro_credit_unit_price_test.go` / `gateway_multiplatform_test.go`）构造一个 anthropic 分组含 1 anthropic + 1 mixed kiro 账号，断言：
- 选号能同时产出两类账号（候选池含 kiro）。
- 选中 anthropic 账号时，`isKiroDirectModeAccount(account)` 为 false（即不会进 kiro 转发）。
- 选中 kiro 账号时 `isKiroDirectModeAccount` 为 true。

```go
//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMixedPool_AnthropicAccountNotKiroRouted(t *testing.T) {
	anthropic := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive}
	kiro := &Account{ID: 2, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive,
		Extra: map[string]any{"mixed_scheduling": true}}

	// 准入：anthropic 池里 mixed kiro 放行、anthropic 直放行；非混合时 kiro 被拒
	s := &GatewayService{}
	require.True(t, s.isAccountAllowedForPlatform(anthropic, PlatformAnthropic, true))
	require.True(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, true))
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, false))

	// 转发分流：anthropic 不进 kiro 路径，kiro 进
	require.False(t, isKiroDirectModeAccount(anthropic))
	require.True(t, isKiroDirectModeAccount(kiro))
}
```

- [ ] **Step 2: 跑该测试**

Run: `cd backend && go test -tags unit ./internal/service/ -run TestMixedPool_AnthropicAccountNotKiroRouted -v`
Expected: PASS

- [ ] **Step 3: 源码文本断言测试不回归**

`kiro_reference_alignment_test.go` 等基于源码字符串断言的测试可能因改动 `kiroEndpointModeForRequest` 等措辞而失败。

Run: `cd backend && go test -tags unit ./internal/handler/ ./internal/service/ -run 'Alignment|Reference' -v`
Expected: PASS；若 FAIL 因本次合理改动导致，更新该断言文本使其匹配新实现（不得放宽断言意图）。

- [ ] **Step 4: 全量验证**

Run:
```bash
cd backend && go build ./... && go test -tags unit ./internal/service/... ./internal/repository/... && go vet ./internal/service/... ./internal/handler/...
cd ../frontend && pnpm typecheck
```
Expected: 全绿。若某预存失败用例与本次无关（见项目已知 gotchas），记录并跳过。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/gateway_mixed_kiro_integration_test.go
git commit -m "test: regression for mixed kiro/anthropic scheduling isolation"
```

---

## 自检清单（实现完成后）

- [ ] mixed kiro 账号能被 anthropic 分组选中（Task 1-3）。
- [ ] kiro 绝不进 gemini 池（Task 1/2/3/10 均有断言/约束）。
- [ ] 原生 kiro 组（id8）行为零变化：resolver 第一分支取组配置（Task 5/8），auto-sticky 维持 return ""（Task 7）。
- [ ] kiro 账号混入 anthropic 组保留 KRS + 缓存模拟（Task 4/5）。
- [ ] anthropic 账号在混合组：转发/UA/指纹/选号算法零改动（Task 11 回归断言）。
- [ ] auto-sticky 稳定 hash 只对含 mixed kiro 的组生效，纯 anthropic 组档位 3 不变（Task 7）。
- [ ] 前端可配置 kiro mixed + 4 项配置，GroupSelector 只放行 anthropic 组（Task 9/10）。

# 可配置滚动公告栏 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 首页顶部公告栏从单条硬编码文案改为管理员可配置的多条滚动公告(每条分 zh/en 文案),切换间隔管理员可配,前端淡入淡出轮播;空列表回退到内置默认文案。

**Architecture:** 复用项目两个成熟模式 —— `custom_menu_items`(JSON 数组字符串设置,全链路 21 个接入点)与 `table_default_page_size`(int 标量字符串设置,17 个接入点)。新增两个设置键 `announcement_banners`(数组)与 `announcement_banner_interval_ms`(int,后端 clamp `[1000,60000]`,默认 3000)。数据经公开设置(SSR 注入 + `/api/v1/settings/public`)下发到前端 `appStore.cachedPublicSettings`,`HomeView.vue` 渲染轮播。

**Tech Stack:** Go (Gin + ent) 后端;Vue 3 + TypeScript + Pinia + vue-i18n 前端;Vitest / Go testing。

**关键安全网:** ① Go 编译器抓类型不匹配;② `TestPublicSettingsInjectionPayload_SchemaDoesNotDrift`(`backend/internal/handler/dto/public_settings_injection_schema_test.go`)强制 `PublicSettingsInjectionPayload` 覆盖 `dto.PublicSettings` 的每个 JSON 字段 —— 漏写注入 payload 会红。

**参考对照:** 实现每个接入点时,直接 grep 对应的兄弟字段行,照抄一行。banners 无 URL,**不改** CSP / `collectFrameSrcOrigins`。banners 也无 per-item visibility,注入用 `safeRawJSONArray`(同 `custom_endpoints`),**不用** `filterUserVisibleMenuItems`。

---

## Phase 1 — 后端数据层(常量 / 结构体 / 解析 / 归一化 + 单测)

### Task 1: 新增设置键常量

**Files:**
- Modify: `backend/internal/service/domain_constants.go:287-294`

- [ ] **Step 1: 加两个常量**

在 `SettingKeyCustomMenuItems` 那一组常量里(约 L294 之后)追加:

```go
	SettingKeyAnnouncementBanners         = "announcement_banners"          // 顶部滚动公告(JSON 数组)
	SettingKeyAnnouncementBannerIntervalMs = "announcement_banner_interval_ms" // 公告切换间隔(毫秒)
```

- [ ] **Step 2: 编译**

Run: `cd backend && go build ./internal/service/`
Expected: 通过(常量暂未使用不会报错,因为是包级常量)。

- [ ] **Step 3: Commit**

```bash
git add backend/internal/service/domain_constants.go
git commit -m "feat(settings): 新增公告栏设置键常量"
```

### Task 2: DTO —— AnnouncementBanner 结构体 + 解析函数(TDD)

**Files:**
- Modify: `backend/internal/handler/dto/settings.go` (struct near L11-26, parse fn near L540)
- Create: `backend/internal/handler/dto/announcement_banners_test.go`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/handler/dto/announcement_banners_test.go`:

```go
package dto

import (
	"reflect"
	"testing"
)

func TestParseAnnouncementBanners(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []AnnouncementBanner
	}{
		{"empty", "", []AnnouncementBanner{}},
		{"emptyArray", "[]", []AnnouncementBanner{}},
		{"invalid", "not-json", []AnnouncementBanner{}},
		{"valid", `[{"id":"a","text_zh":"你好","text_en":"hi"}]`,
			[]AnnouncementBanner{{ID: "a", TextZH: "你好", TextEN: "hi"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAnnouncementBanners(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseAnnouncementBanners(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/handler/dto/ -run TestParseAnnouncementBanners`
Expected: 编译失败(`AnnouncementBanner` / `ParseAnnouncementBanners` 未定义)。

- [ ] **Step 3: 实现结构体**

在 `dto/settings.go` 的 `CustomEndpoint` 结构体之后(约 L26)加:

```go
// AnnouncementBanner represents an admin-configured rolling top-bar announcement.
type AnnouncementBanner struct {
	ID     string `json:"id"`
	TextZH string `json:"text_zh"`
	TextEN string `json:"text_en"`
}
```

- [ ] **Step 4: 实现解析函数**

在 `ParseCustomEndpoints` 附近(约 L562 之后)加,镜像 `ParseCustomMenuItems`:

```go
// ParseAnnouncementBanners parses a JSON string into a slice of AnnouncementBanner.
// Returns empty slice on empty/invalid input.
func ParseAnnouncementBanners(raw string) []AnnouncementBanner {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []AnnouncementBanner{}
	}
	var items []AnnouncementBanner
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []AnnouncementBanner{}
	}
	return items
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd backend && go test ./internal/handler/dto/ -run TestParseAnnouncementBanners`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/handler/dto/settings.go backend/internal/handler/dto/announcement_banners_test.go
git commit -m "feat(settings): AnnouncementBanner DTO + 解析"
```

### Task 3: 间隔归一化 helper(TDD)

**Files:**
- Modify: `backend/internal/service/setting_service.go` (helper near `normalizeTablePreferences`)
- Create: `backend/internal/service/announcement_interval_test.go`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/service/announcement_interval_test.go`:

```go
package service

import "testing"

func TestNormalizeAnnouncementInterval(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero_defaults", 0, 3000},
		{"negative_defaults", -5, 3000},
		{"below_min_clamps", 500, 1000},
		{"above_max_clamps", 999999, 60000},
		{"valid_passthrough", 5000, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeAnnouncementInterval(tc.in); got != tc.want {
				t.Fatalf("normalizeAnnouncementInterval(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/service/ -run TestNormalizeAnnouncementInterval`
Expected: 编译失败(`normalizeAnnouncementInterval` 未定义)。

- [ ] **Step 3: 实现 helper**

在 `setting_service.go` 靠近 `normalizeTablePreferences` 处加:

```go
const (
	defaultAnnouncementIntervalMs = 3000
	minAnnouncementIntervalMs     = 1000
	maxAnnouncementIntervalMs     = 60000
)

// normalizeAnnouncementInterval clamps the banner rotation interval (ms) into a
// sane range; 0/invalid/out-of-range falls back to the default.
func normalizeAnnouncementInterval(v int) int {
	if v <= 0 {
		return defaultAnnouncementIntervalMs
	}
	if v < minAnnouncementIntervalMs {
		return minAnnouncementIntervalMs
	}
	if v > maxAnnouncementIntervalMs {
		return maxAnnouncementIntervalMs
	}
	return v
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/service/ -run TestNormalizeAnnouncementInterval`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/setting_service.go backend/internal/service/announcement_interval_test.go
git commit -m "feat(settings): 公告间隔归一化 helper"
```

---

## Phase 2 — 后端 service 层接入(结构体字段 / builder / 保存 / 默认 / 注入)

### Task 4: service 层结构体加字段

**Files:**
- Modify: `backend/internal/service/settings_view.go` (`SystemSettings` L142 区、`PublicSettings` L278 区)
- Modify: `backend/internal/service/setting_service.go:1456-1458` (`PublicSettingsInjectionPayload`)

- [ ] **Step 1: `service.SystemSettings` 加字段**

在 `settings_view.go` 的 `SystemSettings` 结构体内、`CustomMenuItems string` 那行(约 L142)附近加:

```go
	AnnouncementBanners          string // JSON array of announcement banners
	AnnouncementBannerIntervalMs int
```

- [ ] **Step 2: `service.PublicSettings` 加字段**

在 `settings_view.go` 的 `PublicSettings` 结构体内、`CustomMenuItems string` 那行(约 L278)附近加同样两行:

```go
	AnnouncementBanners          string // JSON array of announcement banners
	AnnouncementBannerIntervalMs int
```

- [ ] **Step 3: 注入 payload 加字段**

在 `setting_service.go` 的 `PublicSettingsInjectionPayload` 结构体内、`CustomMenuItems json.RawMessage`(L1458)附近加:

```go
	AnnouncementBanners          json.RawMessage `json:"announcement_banners"`
	AnnouncementBannerIntervalMs int             `json:"announcement_banner_interval_ms"`
```

- [ ] **Step 4: 编译**

Run: `cd backend && go build ./internal/service/`
Expected: 通过。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/settings_view.go backend/internal/service/setting_service.go
git commit -m "feat(settings): service 结构体加公告栏字段"
```

### Task 5: 公开设置 key 列表 + 两个 builder + 保存 + 默认

**Files:**
- Modify: `backend/internal/service/setting_service.go` (key list ~L795-801, public builder ~L924-926, admin builder ~L3343, save ~L2136, defaults ~L3111)

- [ ] **Step 1: 加入公开设置 key 列表**

在 `setting_service.go` 约 L801 `SettingKeyCustomMenuItems,` 一行下面加:

```go
		SettingKeyAnnouncementBanners,
		SettingKeyAnnouncementBannerIntervalMs,
```

- [ ] **Step 2: 公开 builder 映射(约 L926)**

在返回 `&PublicSettings{...}` 里 `CustomMenuItems: settings[SettingKeyCustomMenuItems],`(L926)下面加:

```go
		AnnouncementBanners:          settings[SettingKeyAnnouncementBanners],
		AnnouncementBannerIntervalMs: normalizeAnnouncementInterval(atoiOrZero(settings[SettingKeyAnnouncementBannerIntervalMs])),
```

如果包内没有 `atoiOrZero` 这类 helper,用内联解析代替(在 return 之前加一行局部变量):

```go
	announcementInterval := normalizeAnnouncementInterval(0)
	if v, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyAnnouncementBannerIntervalMs])); err == nil {
		announcementInterval = normalizeAnnouncementInterval(v)
	}
```

然后映射写 `AnnouncementBannerIntervalMs: announcementInterval,`。(先 grep `func atoiOrZero` 确认;没有就用内联版。)

- [ ] **Step 3: admin builder 映射(约 L3343)**

在第二个 builder 的 `CustomMenuItems: settings[SettingKeyCustomMenuItems],`(L3343)下面加,间隔同样用局部变量归一化(与 Step 2 相同写法),banners 直接透传字符串:

```go
		AnnouncementBanners:          settings[SettingKeyAnnouncementBanners],
		AnnouncementBannerIntervalMs: announcementInterval, // 用同段局部变量
```

> 注:该 builder 末尾用 `result.TableDefaultPageSize, ... = parseTablePreferences(...)` 的后置赋值风格。间隔也可照此在 `return result` 之前写 `result.AnnouncementBanners = settings[SettingKeyAnnouncementBanners]` + `result.AnnouncementBannerIntervalMs = normalizeAnnouncementInterval(...)`,避免在结构体字面量里放局部变量。二选一,保证编译通过即可。

- [ ] **Step 4: 保存 updates(约 L2136)**

在 `updates[SettingKeyCustomMenuItems] = settings.CustomMenuItems`(L2136)下面加:

```go
	updates[SettingKeyAnnouncementBanners] = settings.AnnouncementBanners
	updates[SettingKeyAnnouncementBannerIntervalMs] = strconv.Itoa(normalizeAnnouncementInterval(settings.AnnouncementBannerIntervalMs))
```

- [ ] **Step 5: 默认值(约 L3111)**

在默认值 map 里 `SettingKeyCustomMenuItems: "[]",`(L3111)下面加:

```go
		SettingKeyAnnouncementBanners:                       "[]",
		SettingKeyAnnouncementBannerIntervalMs:              "3000",
```

- [ ] **Step 6: 注入 payload 映射**

在 `GetPublicSettingsForInjection` 里 `CustomMenuItems: filterUserVisibleMenuItems(settings.CustomMenuItems),`(L1527)下面加(banners 用 `safeRawJSONArray`,间隔直接透传已归一化的 int):

```go
		AnnouncementBanners:          safeRawJSONArray(settings.AnnouncementBanners),
		AnnouncementBannerIntervalMs: settings.AnnouncementBannerIntervalMs,
```

- [ ] **Step 7: 编译 + 跑 service 测试**

Run: `cd backend && go build ./... && go test ./internal/service/ -run 'Announcement|PublicSettings'`
Expected: 通过。

- [ ] **Step 8: Commit**

```bash
git add backend/internal/service/setting_service.go
git commit -m "feat(settings): 公告栏接入公开设置/保存/默认/注入"
```

### Task 6: 公开设置默认值测试

**Files:**
- Modify: `backend/internal/service/setting_service_public_test.go` (near L69-78)

- [ ] **Step 1: 写测试**

在 `setting_service_public_test.go` 已有的 public settings 断言测试里(约 L78 附近,与 `TablePageSizeOptions` 断言同一个 test),追加对新字段的断言。先在 setting map 里给 `SettingKeyAnnouncementBanners` / `SettingKeyAnnouncementBannerIntervalMs` 赋值(参考 L69-70 的写法),再断言:

```go
			SettingKeyAnnouncementBanners:          `[{"id":"a","text_zh":"你好","text_en":"hi"}]`,
			SettingKeyAnnouncementBannerIntervalMs: "5000",
```

```go
	require.Equal(t, `[{"id":"a","text_zh":"你好","text_en":"hi"}]`, settings.AnnouncementBanners)
	require.Equal(t, 5000, settings.AnnouncementBannerIntervalMs)
```

- [ ] **Step 2: 跑测试确认通过**

Run: `cd backend && go test ./internal/service/ -run TestGetPublicSettings`
Expected: PASS(若函数名不同,先 grep 该文件里的 `func Test` 取实际名)。

- [ ] **Step 3: Commit**

```bash
git add backend/internal/service/setting_service_public_test.go
git commit -m "test(settings): 公开设置含公告栏字段"
```

---

## Phase 3 — 后端 handler 层接入(DTO 响应 / 请求 / 校验 / 审计)

### Task 7: DTO 响应结构体加字段 + 公开/管理端映射

**Files:**
- Modify: `backend/internal/handler/dto/settings.go` (`SystemSettings` L142, `PublicSettings` L310)
- Modify: `backend/internal/handler/setting_handler.go:79-82` (公开端点映射)
- Modify: `backend/internal/handler/admin/setting_handler.go` (GET 映射 L224-227、响应映射 L2142-2145)

- [ ] **Step 1: `dto.SystemSettings` 加字段(admin 响应)**

在 `dto/settings.go` 的 `SystemSettings` 里 `CustomMenuItems []CustomMenuItem`(L142)下面加:

```go
	AnnouncementBanners          []AnnouncementBanner `json:"announcement_banners"`
	AnnouncementBannerIntervalMs int                  `json:"announcement_banner_interval_ms"`
```

- [ ] **Step 2: `dto.PublicSettings` 加字段(public 响应)**

在 `dto/settings.go` 的 `PublicSettings` 里 `CustomMenuItems []CustomMenuItem`(L310)下面加同样两行。

- [ ] **Step 3: 公开端点映射**

在 `setting_handler.go` 的 `CustomMenuItems: dto.ParseUserVisibleMenuItems(settings.CustomMenuItems),`(L81)下面加:

```go
		AnnouncementBanners:          dto.ParseAnnouncementBanners(settings.AnnouncementBanners),
		AnnouncementBannerIntervalMs: settings.AnnouncementBannerIntervalMs,
```

- [ ] **Step 4: admin GET 映射(约 L226)**

在 `admin/setting_handler.go` 的 `CustomMenuItems: dto.ParseCustomMenuItems(settings.CustomMenuItems),`(L226)下面加:

```go
		AnnouncementBanners:                    dto.ParseAnnouncementBanners(settings.AnnouncementBanners),
		AnnouncementBannerIntervalMs:           settings.AnnouncementBannerIntervalMs,
```

- [ ] **Step 5: admin 保存后响应映射(约 L2144)**

在 `admin/setting_handler.go` 的 `CustomMenuItems: dto.ParseCustomMenuItems(updatedSettings.CustomMenuItems),`(L2144)下面加:

```go
		AnnouncementBanners:                    dto.ParseAnnouncementBanners(updatedSettings.AnnouncementBanners),
		AnnouncementBannerIntervalMs:           updatedSettings.AnnouncementBannerIntervalMs,
```

- [ ] **Step 6: 编译 + 跑注入漂移测试**

Run: `cd backend && go build ./... && go test ./internal/handler/dto/ -run TestPublicSettingsInjectionPayload_SchemaDoesNotDrift`
Expected: PASS(证明 dto 与注入 payload 字段对齐)。

- [ ] **Step 7: Commit**

```bash
git add backend/internal/handler/dto/settings.go backend/internal/handler/setting_handler.go backend/internal/handler/admin/setting_handler.go
git commit -m "feat(settings): 公告栏 DTO 响应 + 公开/管理端映射"
```

### Task 8: admin 请求结构体 + 校验/序列化 + req→service + 审计

**Files:**
- Modify: `backend/internal/handler/admin/setting_handler.go` (请求结构体 L520, 校验/marshal ~L1310-1397, req→service ~L1654, 审计 ~L2653)

- [ ] **Step 1: 请求结构体加字段**

在请求结构体里 `CustomMenuItems *[]dto.CustomMenuItem` ... `CustomEndpoints *[]dto.CustomEndpoint`(L520-521)下面加:

```go
	AnnouncementBanners          *[]dto.AnnouncementBanner `json:"announcement_banners"`
	AnnouncementBannerIntervalMs *int                      `json:"announcement_banner_interval_ms"`
```

> 用指针以区分「未传(保留原值)」与「传空数组(清空)」,与 `CustomMenuItems` 一致。

- [ ] **Step 2: 校验 + 序列化为 JSON 字符串**

在自定义端点校验块之前(约 L1398,`customMenuJSON = string(menuBytes)` 结束、`// 自定义端点验证` 之前)插入公告校验块:

```go
	// 公告栏验证
	const (
		maxAnnouncementBanners = 20
		maxBannerTextLen       = 200
	)
	announcementBannersJSON := previousSettings.AnnouncementBanners
	if req.AnnouncementBanners != nil {
		banners := *req.AnnouncementBanners
		if len(banners) > maxAnnouncementBanners {
			response.BadRequest(c, "Too many announcement banners (max 20)")
			return
		}
		seen := make(map[string]struct{}, len(banners))
		for i, b := range banners {
			if strings.TrimSpace(b.TextZH) == "" && strings.TrimSpace(b.TextEN) == "" {
				response.BadRequest(c, "Announcement banner must have at least one non-empty text")
				return
			}
			if len(b.TextZH) > maxBannerTextLen || len(b.TextEN) > maxBannerTextLen {
				response.BadRequest(c, "Announcement banner text is too long (max 200 characters)")
				return
			}
			if strings.TrimSpace(b.ID) == "" {
				id, err := generateMenuItemID()
				if err != nil {
					response.Error(c, http.StatusInternalServerError, "Failed to generate banner ID")
					return
				}
				banners[i].ID = id
			} else if !menuItemIDPattern.MatchString(b.ID) {
				response.BadRequest(c, "Announcement banner ID contains invalid characters")
				return
			}
			if _, dup := seen[banners[i].ID]; dup {
				response.BadRequest(c, "Duplicate announcement banner ID: "+banners[i].ID)
				return
			}
			seen[banners[i].ID] = struct{}{}
		}
		bytes, err := json.Marshal(banners)
		if err != nil {
			response.BadRequest(c, "Failed to serialize announcement banners")
			return
		}
		announcementBannersJSON = string(bytes)
	}

	announcementIntervalMs := previousSettings.AnnouncementBannerIntervalMs
	if req.AnnouncementBannerIntervalMs != nil {
		announcementIntervalMs = *req.AnnouncementBannerIntervalMs
	}
```

> 复用现成的 `generateMenuItemID` 与 `menuItemIDPattern`(同文件已定义,供 custom menu 用)。

- [ ] **Step 3: req→service 赋值(约 L1654)**

在构造 `service.SystemSettings{...}` 里 `CustomMenuItems: customMenuJSON,`(L1654)下面加:

```go
		AnnouncementBanners:          announcementBannersJSON,
		AnnouncementBannerIntervalMs: announcementIntervalMs,
```

- [ ] **Step 4: 审计 diff(约 L2653)**

在 `if before.CustomMenuItems != after.CustomMenuItems {...}`(L2653)块下面加:

```go
	if before.AnnouncementBanners != after.AnnouncementBanners {
		changed = append(changed, "announcement_banners")
	}
	if before.AnnouncementBannerIntervalMs != after.AnnouncementBannerIntervalMs {
		changed = append(changed, "announcement_banner_interval_ms")
	}
```

- [ ] **Step 5: 编译 + 全后端测试**

Run: `cd backend && go build ./... && go test ./internal/handler/... ./internal/service/...`
Expected: 全 PASS。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/handler/admin/setting_handler.go
git commit -m "feat(settings): admin 公告栏请求校验/序列化/审计"
```

### Task 9: admin handler 公告栏校验测试

**Files:**
- Create or Modify: `backend/internal/handler/admin/setting_handler_announcement_test.go`

- [ ] **Step 1: 写测试(往返 + 上限 + 空文案拒绝)**

先 grep 现有 setting handler 测试的构造方式(`grep -rn "func Test.*Setting" backend/internal/handler/admin/*_test.go`),复用其 router/service mock 脚手架,新增 3 个用例:
1. 提交 2 条合法 banner + interval=5000 → 200,再 GET 断言返回 2 条且 interval=5000;
2. 提交 21 条 banner → 400 "Too many announcement banners";
3. 提交一条 `text_zh` 与 `text_en` 均为空 → 400 "at least one non-empty text"。

> 若该目录缺乏可复用的 handler-level 脚手架,则降级为纯 service-level 测试:直接调用 `SettingService.UpdateSystemSettings` 存 `announcement_banners` 原始 JSON,再 `GetPublicSettings` 断言 round-trip 与默认 `3000`。二选一,以能实际运行为准。

- [ ] **Step 2: 跑测试确认通过**

Run: `cd backend && go test ./internal/handler/admin/ -run Announcement`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add backend/internal/handler/admin/setting_handler_announcement_test.go
git commit -m "test(settings): admin 公告栏校验用例"
```

---

## Phase 4 — 前端类型 + Pinia store

### Task 10: 前端类型定义

**Files:**
- Modify: `frontend/src/types/index.ts` (`CustomMenuItem` near L172, `PublicSettings` near L215-221)
- Modify: `frontend/src/api/admin/settings.ts` (response near L457, request near L732)

- [ ] **Step 1: 新增 `AnnouncementBanner` 类型 + PublicSettings 字段**

在 `types/index.ts` 的 `CustomEndpoint` 之后(约 L186)加:

```ts
export interface AnnouncementBanner {
  id: string
  text_zh: string
  text_en: string
}
```

在 `PublicSettings` 里 `home_content: string`(L215)附近加:

```ts
  announcement_banners: AnnouncementBanner[]
  announcement_banner_interval_ms: number
```

- [ ] **Step 2: admin API 类型**

在 `api/admin/settings.ts` 顶部已 import `CustomMenuItem` 处补 import `AnnouncementBanner`(与 `CustomMenuItem` 同源,约 L9)。在 response 类型 `custom_menu_items: CustomMenuItem[];`(L457)下面加:

```ts
  announcement_banners: AnnouncementBanner[];
  announcement_banner_interval_ms: number;
```

在 request 类型 `custom_menu_items?: CustomMenuItem[];`(L732)下面加:

```ts
  announcement_banners?: AnnouncementBanner[];
  announcement_banner_interval_ms?: number;
```

- [ ] **Step 3: 类型检查**

Run: `cd frontend && npx vue-tsc --noEmit`
Expected: 报错集中在「`app.ts` fallback 对象缺 `announcement_banners` / `announcement_banner_interval_ms`」—— 下一 Task 修复。其余无新错。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/types/index.ts frontend/src/api/admin/settings.ts
git commit -m "feat(settings): 前端公告栏类型"
```

### Task 11: app store fallback 默认值

**Files:**
- Modify: `frontend/src/stores/app.ts` (fallback object near L363-390)

- [ ] **Step 1: 补 fallback 默认值**

在 `fetchPublicSettings` 的 fallback 返回对象里 `home_content: '',`(L363)下面加:

```ts
        announcement_banners: [],
        announcement_banner_interval_ms: 3000,
```

- [ ] **Step 2: 类型检查 + store 测试**

Run: `cd frontend && npx vue-tsc --noEmit && npx vitest run src/stores/__tests__/app.spec.ts`
Expected: 类型无新错;store 测试 PASS。

- [ ] **Step 3: Commit**

```bash
git add frontend/src/stores/app.ts
git commit -m "feat(settings): app store 公告栏 fallback 默认值"
```

---

## Phase 5 — 前端管理端编辑器

### Task 12: 管理端 form 默认值 + 增删改处理函数

**Files:**
- Modify: `frontend/src/views/admin/SettingsView.vue` (form defaults near L7910, handlers near L8586)

- [ ] **Step 1: form 默认值**

在 `form` 默认对象里 `custom_menu_items: [] as Array<{...}>` 块之后(约 L7920,`custom_menu_items` 定义结束处)加:

```ts
  announcement_banners: [] as Array<{
    id: string;
    text_zh: string;
    text_en: string;
  }>,
  announcement_banner_interval_ms: 3000,
```

- [ ] **Step 2: 增删改 + 上下移处理函数**

在 `moveMenuItem` 之后(约 L8586)加,镜像 custom-menu 的处理函数:

```ts
// Announcement banner management
function addAnnouncementBanner() {
  form.announcement_banners.push({ id: "", text_zh: "", text_en: "" });
}

function removeAnnouncementBanner(index: number) {
  form.announcement_banners.splice(index, 1);
}

function moveAnnouncementBanner(index: number, direction: -1 | 1) {
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= form.announcement_banners.length) return;
  const items = form.announcement_banners;
  const temp = items[index];
  items[index] = items[targetIndex];
  items[targetIndex] = temp;
}

// 间隔以秒为单位的双向绑定(存储用 ms)
const announcementIntervalSeconds = computed({
  get: () => Math.round(form.announcement_banner_interval_ms / 1000),
  set: (v: number) => {
    const n = Math.floor(Number(v));
    form.announcement_banner_interval_ms = Number.isFinite(n) ? n * 1000 : 3000;
  },
});
```

> `computed` 已在该文件 import(它是 Vue 组件)。若未 import,在顶部 `import { computed } from "vue"` 补上(先 grep 确认)。

- [ ] **Step 3: 类型检查**

Run: `cd frontend && npx vue-tsc --noEmit`
Expected: 无新错(模板尚未用到函数,但定义合法)。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/views/admin/SettingsView.vue
git commit -m "feat(settings): 管理端公告栏 form + 处理函数"
```

### Task 13: 管理端编辑器模板 + load/save 接线

**Files:**
- Modify: `frontend/src/views/admin/SettingsView.vue` (template near L5316 customMenu 卡片之后, save near L9147, load 自动生效)

- [ ] **Step 1: 模板 —— 公告栏编辑卡片**

在 customMenu 卡片(约 L5316 起、以 `</div>` 闭合的 `card`)之后插入新卡片。放在同一父容器内、customMenu 卡片下方:

```vue
          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.announcementBanners.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.announcementBanners.description") }}
              </p>
            </div>
            <div class="space-y-4 p-6">
              <!-- 切换间隔(秒) -->
              <div>
                <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.settings.announcementBanners.intervalSeconds") }}
                </label>
                <input
                  v-model.number="announcementIntervalSeconds"
                  type="number"
                  min="1"
                  max="60"
                  class="input w-32"
                />
              </div>
              <!-- 公告条目 -->
              <div
                v-for="(item, index) in form.announcement_banners"
                :key="item.id || index"
                class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="mb-3 flex items-center justify-between">
                  <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t("admin.settings.announcementBanners.itemLabel", { n: index + 1 }) }}
                  </span>
                  <div class="flex items-center gap-2">
                    <button
                      v-if="index > 0"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.announcementBanners.moveUp')"
                      @click="moveAnnouncementBanner(index, -1)"
                    >↑</button>
                    <button
                      v-if="index < form.announcement_banners.length - 1"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.announcementBanners.moveDown')"
                      @click="moveAnnouncementBanner(index, 1)"
                    >↓</button>
                    <button
                      type="button"
                      class="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.announcementBanners.remove')"
                      @click="removeAnnouncementBanner(index)"
                    >✕</button>
                  </div>
                </div>
                <div class="space-y-3">
                  <div>
                    <label class="mb-1 block text-xs text-gray-500 dark:text-gray-400">中文</label>
                    <input v-model="item.text_zh" type="text" maxlength="200" class="input w-full" />
                  </div>
                  <div>
                    <label class="mb-1 block text-xs text-gray-500 dark:text-gray-400">English</label>
                    <input v-model="item.text_en" type="text" maxlength="200" class="input w-full" />
                  </div>
                </div>
              </div>
              <button
                type="button"
                class="btn-secondary"
                @click="addAnnouncementBanner"
              >
                {{ t("admin.settings.announcementBanners.add") }}
              </button>
            </div>
          </div>
```

> 用 `↑↓✕` 占位文本按钮而非 SVG,保持 diff 小;如需与 customMenu 视觉完全一致,可复制其 `<svg>` 图标块替换文本。`.input` / `.btn-secondary` / `.card` 均为该页已用的既有样式类(grep 确认)。

- [ ] **Step 2: save payload 接线(约 L9147)**

在 `saveSettings` 的 payload 里 `custom_menu_items: form.custom_menu_items,`(L9147)下面加:

```ts
      announcement_banners: form.announcement_banners,
      announcement_banner_interval_ms: form.announcement_banner_interval_ms,
```

- [ ] **Step 3: load 接线确认**

`loadSettings`(L8749-8752)有通用循环 `for (const [key, value] of Object.entries(settings)) { if (value != null) form[key] = value }`,会自动把 `announcement_banners` 与 `announcement_banner_interval_ms` 灌入 form —— **无需**额外代码。`announcementIntervalSeconds` computed 从 ms 派生,自动同步。无需改动,此步仅确认。

- [ ] **Step 4: 类型检查 + 构建**

Run: `cd frontend && npx vue-tsc --noEmit && npm run build`
Expected: 通过。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/views/admin/SettingsView.vue
git commit -m "feat(settings): 管理端公告栏编辑器 UI + 存取接线"
```

### Task 14: 管理端 i18n 文案

**Files:**
- Modify: `frontend/src/i18n/locales/zh.ts` (customMenu near L6623)
- Modify: `frontend/src/i18n/locales/en.ts` (对应 customMenu 处)

- [ ] **Step 1: zh 文案**

在 `zh.ts` 的 `admin.settings` 下、`customMenu: {...}`(约 L6623)块之后加:

```ts
      announcementBanners: {
        title: "顶部滚动公告",
        description: "配置首页顶部滚动展示的公告条目,支持中英文;留空则显示默认文案。",
        intervalSeconds: "切换间隔(秒)",
        itemLabel: "公告 {n}",
        add: "添加公告",
        remove: "删除",
        moveUp: "上移",
        moveDown: "下移",
      },
```

- [ ] **Step 2: en 文案**

在 `en.ts` 对应位置加:

```ts
      announcementBanners: {
        title: "Top Rolling Announcements",
        description: "Configure rolling banners on the homepage top bar (zh/en). Empty falls back to the default copy.",
        intervalSeconds: "Interval (seconds)",
        itemLabel: "Banner {n}",
        add: "Add banner",
        remove: "Remove",
        moveUp: "Move up",
        moveDown: "Move down",
      },
```

- [ ] **Step 3: locale 测试(若有)+ 构建**

Run: `cd frontend && npx vitest run src/i18n && npm run build`
Expected: PASS / 构建通过(若无 i18n 结构测试,仅构建)。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts
git commit -m "feat(settings): 公告栏管理端 i18n 文案"
```

---

## Phase 6 — 前端 HomeView 轮播

### Task 15: HomeView 轮播逻辑(script)

**Files:**
- Modify: `frontend/src/views/HomeView.vue` (script: near L711 imports, near L759 showAnnouncement, near L1074 copy, near L1155 onMounted)

- [ ] **Step 1: import 补 onBeforeUnmount**

`HomeView.vue` L711 现为 `import { ref, computed, onMounted, watch } from 'vue'`。改为:

```ts
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
```

- [ ] **Step 2: 轮播状态 + 计算属性**

在 `const showAnnouncement = ref(true)`(L759)之后加:

```ts
const DEFAULT_BANNER_INTERVAL_MS = 3000

// 每条 banner 取当前 locale 文案,缺失回退另一语言;两者皆空则丢弃。
const banners = computed<string[]>(() => {
  const raw = appStore.cachedPublicSettings?.announcement_banners ?? []
  const zh = localeCode.value === 'zh'
  const texts = raw
    .map((b) => {
      const primary = zh ? b.text_zh : b.text_en
      const fallback = zh ? b.text_en : b.text_zh
      return (primary || fallback || '').trim()
    })
    .filter((s) => s.length > 0)
  // 空列表回退到内置默认双语文案
  return texts.length > 0 ? texts : [copy.value.announcement]
})

const bannerIntervalMs = computed(() => {
  const v = appStore.cachedPublicSettings?.announcement_banner_interval_ms
  return typeof v === 'number' && v > 0 ? v : DEFAULT_BANNER_INTERVAL_MS
})

const currentBannerIndex = ref(0)
const currentBannerText = computed(
  () => banners.value[currentBannerIndex.value] ?? banners.value[0] ?? '',
)

let bannerTimer: ReturnType<typeof setInterval> | null = null

function stopBannerRotation() {
  if (bannerTimer !== null) {
    clearInterval(bannerTimer)
    bannerTimer = null
  }
}

function startBannerRotation() {
  stopBannerRotation()
  if (!showAnnouncement.value) return
  if (banners.value.length <= 1) return
  bannerTimer = setInterval(() => {
    currentBannerIndex.value = (currentBannerIndex.value + 1) % banners.value.length
  }, bannerIntervalMs.value)
}
```

> `copy`(L1074)在 script 中定义于此段之后,但因 `banners` 是 computed(惰性求值,渲染时才读),此处引用 `copy.value` 不会有初始化顺序问题。

- [ ] **Step 3: 关闭按钮停表**

把关闭逻辑从 `@click="showAnnouncement = false"`(模板 L29)改为调用一个函数。在 script 加:

```ts
function dismissAnnouncement() {
  showAnnouncement.value = false
  stopBannerRotation()
}
```

（模板改动在 Task 16。）

- [ ] **Step 4: 生命周期 + watch 重建定时器**

在 `onMounted(() => {...})`(L1155)内、`initTheme()` 等之后追加 `startBannerRotation()`。并在该 `onMounted` 块之后加:

```ts
watch([() => banners.value.length, bannerIntervalMs, showAnnouncement], () => {
  if (currentBannerIndex.value >= banners.value.length) {
    currentBannerIndex.value = 0
  }
  startBannerRotation()
})

onBeforeUnmount(() => {
  stopBannerRotation()
})
```

> `onMounted` 里调用 `startBannerRotation()` 时公开设置可能尚未加载完(异步),`banners` 会先返回默认单条、不启动定时器;设置到位后 `watch` 因 `banners.length` 变化重建定时器 —— 覆盖异步加载场景。

- [ ] **Step 5: 类型检查**

Run: `cd frontend && npx vue-tsc --noEmit`
Expected: 无新错。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/HomeView.vue
git commit -m "feat(home): 公告栏轮播逻辑"
```

### Task 16: HomeView 轮播模板(fade 过渡)

**Files:**
- Modify: `frontend/src/views/HomeView.vue` (template L20-33)
- Modify: `frontend/src/views/HomeView.vue` (`<style scoped>` near L1170)

- [ ] **Step 1: 替换公告文案 span 为 fade 轮播**

把 L25 的 `<span>{{ copy.announcement }}</span>` 替换为:

```vue
      <Transition name="banner-fade" mode="out-in">
        <span :key="currentBannerIndex">{{ currentBannerText }}</span>
      </Transition>
```

并把 L29 关闭按钮的 `@click="showAnnouncement = false"` 改为 `@click="dismissAnnouncement"`。

- [ ] **Step 2: 加 fade 过渡样式**

在 `<style scoped>`(L1170 区)内追加:

```css
.banner-fade-enter-active,
.banner-fade-leave-active {
  transition: opacity 0.4s ease;
}
.banner-fade-enter-from,
.banner-fade-leave-to {
  opacity: 0;
}
```

- [ ] **Step 3: 构建**

Run: `cd frontend && npm run build`
Expected: 通过。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/views/HomeView.vue
git commit -m "feat(home): 公告栏 fade 轮播模板"
```

### Task 17: HomeView 轮播单元测试

**Files:**
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts` (extend appState type L8-14, add cases)

- [ ] **Step 1: 扩展 mock 类型**

把 `HomeView.spec.ts` L8-14 的 `cachedPublicSettings` 内联类型补上两个字段:

```ts
    cachedPublicSettings: null as null | {
      site_name?: string
      site_logo?: string
      site_subtitle?: string
      doc_url?: string
      home_content?: string
      announcement_banners?: Array<{ id: string; text_zh: string; text_en: string }>
      announcement_banner_interval_ms?: number
    },
```

- [ ] **Step 2: 写测试用例**

在 `HomeView.spec.ts` 的 `describe('HomeView landing page', ...)` 内新增用例。**复用文件已有的 `mountHome()` helper**(不要自造 mount 配置)。测试 i18n mock 固定 `locale.value = 'zh'`(见 L346),故 `localeCode` 为 `'zh'`,断言用**中文**文案:banner 用 `text_zh`(甲/乙),空列表回退用内置默认中文 `'统一 Claude Code · Codex 官方原生通道,国内稳定直连'`。`beforeEach` 已把 `cachedPublicSettings` 置 null,每个用例内自行赋值。

```ts
it('空列表回退到默认公告文案', async () => {
  appState.cachedPublicSettings = {
    announcement_banners: [],
    announcement_banner_interval_ms: 3000,
  }
  const wrapper = mountHome()
  await flushPromises()
  expect(wrapper.text()).toContain('统一 Claude Code')
})

it('多条 banner 按间隔轮换', async () => {
  vi.useFakeTimers()
  appState.cachedPublicSettings = {
    announcement_banners: [
      { id: 'a', text_zh: '甲', text_en: 'Alpha' },
      { id: 'b', text_zh: '乙', text_en: 'Beta' },
    ],
    announcement_banner_interval_ms: 3000,
  }
  const wrapper = mountHome()
  await flushPromises()
  expect(wrapper.text()).toContain('甲')
  vi.advanceTimersByTime(3000)
  await flushPromises()
  expect(wrapper.text()).toContain('乙')
  vi.useRealTimers()
})

it('单条 banner 不轮换', async () => {
  vi.useFakeTimers()
  appState.cachedPublicSettings = {
    announcement_banners: [{ id: 'a', text_zh: '甲', text_en: 'Alpha' }],
    announcement_banner_interval_ms: 3000,
  }
  const wrapper = mountHome()
  await flushPromises()
  vi.advanceTimersByTime(9000)
  await flushPromises()
  expect(wrapper.text()).toContain('甲')
  vi.useRealTimers()
})
```

> 注意:如果 `mountHome()` 在 `useFakeTimers()` 之前已被别的 setup 调用,确保在**赋值 `cachedPublicSettings` 之后**再 `mountHome()`,这样 `onMounted` 里的 `startBannerRotation` 能读到 banners。若时序上仍读到默认单条,靠 Task 15 的 `watch` 在 `flushPromises` 后重建定时器兜底。

- [ ] **Step 3: 跑测试确认通过**

Run: `cd frontend && npx vitest run src/views/__tests__/HomeView.spec.ts`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/views/__tests__/HomeView.spec.ts
git commit -m "test(home): 公告栏轮播/回退用例"
```

---

## Phase 7 — 端到端验证

### Task 18: 全量验证 + 手动冒烟

- [ ] **Step 1: 后端全测**

Run: `cd backend && go build ./... && go test ./...`
Expected: 全 PASS。

- [ ] **Step 2: 前端全测 + 构建**

Run: `cd frontend && npx vue-tsc --noEmit && npx vitest run && npm run build`
Expected: 全 PASS,构建成功。

- [ ] **Step 3: 手动冒烟(使用 verify skill 或本地起服务)**

流程:
1. 起后端 + 前端(或用项目 `/run` 方式)。
2. 管理端 → 设置 → 找到「顶部滚动公告」,添加 2 条中英文公告,间隔设 3 秒,保存。
3. 打开首页,确认顶部栏每 3 秒淡入淡出切换;切换语言确认文案随之切换。
4. 删除全部公告并保存,确认首页回退显示默认双语文案。
5. 点关闭按钮,确认栏消失且不再切换。

- [ ] **Step 4: 最终提交(如有零散改动)**

```bash
git add -A
git commit -m "chore(settings): 可配置滚动公告栏收尾"
```

---

## 自检对照(spec 覆盖)

- 多条公告、分 zh/en 文案 → Task 2(DTO)/10(前端类型)/13(编辑器)/15(banners computed 按 locale)。✅
- 管理员可配置 → Phase 3(后端请求/校验)+ Phase 5(编辑器 UI)。✅
- 切换间隔可配置 + 默认 3s + clamp `[1000,60000]` → Task 3(normalize)/5(存取)/8(请求)/12(秒↔ms)/15(bannerIntervalMs)。✅
- 纯文本、无链接、不改 CSP → 数据模型仅 `text_zh/text_en`;计划未触碰 CSP。✅
- 空列表回退默认文案 → Task 15 `banners` computed 尾部回退 `copy.announcement`。✅
- fade 过渡、每 Nms 切换 → Task 15(定时器)/16(`<Transition>` + CSS)。✅
- 测试:parse / normalize / 公开默认 / admin 校验 / HomeView 轮播 → Task 2/3/6/9/17。✅
- 不做 per-item 开关 / 链接 / 关闭持久化 → 计划未包含,符合范围约定。✅

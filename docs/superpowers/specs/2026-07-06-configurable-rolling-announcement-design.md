# 管理员可配置的滚动公告栏 — 设计文档

- 日期: 2026-07-06
- 状态: 已批准,待实现
- 范围: 首页顶部固定公告栏,从单条硬编码文案改为管理员可配置的多条滚动公告,切换间隔可配置

## 背景与目标

首页 (`frontend/src/views/HomeView.vue`) 顶部有一个固定公告栏,当前渲染单条硬编码的双语 i18n 文案
(`copy.announcement`,zh/en 随语言自动切换),带一个仅当前会话生效的关闭按钮。

目标:让管理员在后台配置多条公告,前端在顶部栏内循环滚动展示,每条停留固定时长后切换,
切换间隔也由管理员配置。参考 https://zenmux.ai 顶部滚动栏的交互形态。

## 已确认的需求(来自澄清)

- **多语言**: 每条 banner 分语言存储 `text_zh` / `text_en`,前端按当前 locale 显示。
- **链接**: 纯文本,banner 不可点击,无 URL(因此不涉及 CSP frame-src 改动)。
- **空列表回退**: 管理员未配置任何 banner 时,顶部栏回退显示当前内置的双语默认文案
  (`copy.announcement`),保证栏永远有内容、升级后不会突然消失。
- **过渡动画**: 淡入淡出(fade),克制、干净,契合现有 Linear 风格。
- **切换间隔**: 管理员可配置(全局标量,非 per-item),默认 3 秒。

## 架构总览

复用项目已有的 `custom_menu_items` 模式(JSON 字符串存储的对象数组设置)与
`table_default_page_size` 模式(字符串存储的 int 标量设置),全链路对齐,不引入新机制。

数据流:后端 setting → 公开设置(public settings,免登录)→ 前端 `appStore.cachedPublicSettings`
→ `HomeView.vue` 渲染。管理员通过 `admin/SettingsView.vue` 编辑并保存。

公告随**公开设置**下发(与 `home_content` 一致),因此落地页无需登录即可渲染。

## 数据模型

### 1. 公告列表 `announcement_banners`

新增设置键 `announcement_banners`,JSON 数组字符串,默认 `"[]"`,存储方式与 `custom_menu_items` 完全一致。

```go
// backend/internal/handler/dto/settings.go
type AnnouncementBanner struct {
    ID     string `json:"id"`      // 客户端生成,用于稳定的 v-for :key 与重排序
    TextZH string `json:"text_zh"`
    TextEN string `json:"text_en"`
}
```

- 数组顺序即滚动顺序,管理员用上移/下移调整(对齐 custom-menu 编辑器)。
- 不设 per-item `enabled` 开关(YAGNI — 删除即停用)。
- 无链接/URL 字段。

### 2. 切换间隔 `announcement_banner_interval_ms`

新增设置键 `announcement_banner_interval_ms`,int 标量,以字符串存储(同 `table_default_page_size`)。

- 默认 `"3000"`(3 秒)。
- 读取时解析并 **clamp 到 `[1000, 60000]` ms**,空值/非法值回退 `3000`,避免过快闪烁或坏定时器。
- 管理员 UI 以**秒**为单位输入(如 `3`),在 API 边界换算为 ms;存 ms 让前端 `setInterval` 直接可用。

## 后端改动(逐点对齐 `custom_menu_items` / `table_default_page_size`)

`backend/internal/service/domain_constants.go`
- 新增 `SettingKeyAnnouncementBanners = "announcement_banners"`
- 新增 `SettingKeyAnnouncementBannerIntervalMs = "announcement_banner_interval_ms"`

`backend/internal/handler/dto/settings.go`
- 新增 `AnnouncementBanner` 结构体
- 新增 `ParseAnnouncementBanners(raw string) []AnnouncementBanner`(空/非法 → `[]`,镜像 `ParseCustomMenuItems`)
- `SystemSettings`(admin 响应)新增 `AnnouncementBanners json.RawMessage` 与 `AnnouncementBannerIntervalMs int`
- `PublicSettingsInjectionPayload` 新增同样两个字段

`backend/internal/service/setting_service.go`
- 公开设置键列表(~L795)加入两个新键
- 公开 `SystemSettings` 视图(~L920)映射两个字段;间隔用 `normalizeAnnouncementInterval` 解析+clamp
- 注入 payload(~L1527)映射两个字段(banners 走 `safeRawJSONArray` 同 `custom_endpoints`)
- 管理员保存 `updates` map(~L2136):`updates[SettingKeyAnnouncementBanners] = settings.AnnouncementBanners`;
  `updates[SettingKeyAnnouncementBannerIntervalMs] = strconv.Itoa(normalizeAnnouncementInterval(...))`
- 默认值 map(~L3111):`SettingKeyAnnouncementBanners: "[]"`,`SettingKeyAnnouncementBannerIntervalMs: "3000"`
- 第二个 builder(~L3343)同步映射
- 新增小助手 `normalizeAnnouncementInterval(v int) int`(空/越界 → clamp/默认)

注:banners 无 URL,**不需要**改 `collectFrameSrcOrigins` / CSP 相关逻辑。

## 前端改动

### 类型与 API

`frontend/src/types/index.ts`
- `PublicSettings` 新增 `announcement_banners: AnnouncementBanner[]` 与 `announcement_banner_interval_ms: number`
- 新增 `AnnouncementBanner` 类型 `{ id: string; text_zh: string; text_en: string }`

`frontend/src/api/admin/settings.ts`
- admin 请求/响应类型新增上述两字段(对齐 `custom_menu_items` 的声明位置)

### 管理员编辑器 `frontend/src/views/admin/SettingsView.vue`

- 在站点/品牌区块新增一个公告列表编辑器:添加 / 删除 / 上移 / 下移,每行含 `中文` 与 `English`
  两个文本输入。克隆现有 `custom_menu_items` 编辑器块及其 add/remove/move 处理函数。
- 新增一个数字输入 **"切换间隔(秒)"**(min 1,max 60),绑定到秒值,load 时 ms→秒、save 时秒→ms。
- `form` 新增 `announcement_banners`(数组)与 `announcement_banner_interval_ms`(或以秒缓存,保存时换算)。
- 保存 payload(~L9141 附近)带上两字段。
- 新增 i18n 键 `admin.settings.announcementBanners.*`(title / description / textZh / textEn /
  add / remove / moveUp / moveDown / intervalSeconds …)zh + en 两套。

### 顶部滚动栏 `frontend/src/views/HomeView.vue`

替换 L20-33 的单条 `{{ copy.announcement }}` span:

- `banners` computed:读 `cachedPublicSettings.announcement_banners`;每条按当前 locale 取文案,
  当前语言为空则回退另一语言;两语言都空的条目丢弃。
- **空 → 回退**:`banners` 为空时用 `[copy.announcement]`(现有内置双语文案),栏永远有内容。
- `intervalMs` computed:读 `cachedPublicSettings.announcement_banner_interval_ms`,回退 `3000`。
- `currentIndex` ref + `setInterval(intervalMs)`,仅当 `banners.length > 1` 时推进;卸载时清除;
  用 `watch` 监听 banners 长度与 intervalMs 变化重建定时器,并对越界 index 做守卫。
- **淡入淡出**:文本用 Vue `<Transition mode="out-in">` 按 `currentIndex` 作 key,opacity 过渡。
- 关闭按钮不变(`showAnnouncement=false`,仅会话内),关闭时停止定时器。

## 测试

后端
- `ParseAnnouncementBanners` 表驱动测试:空 / 非法 JSON / 合法数组。
- `normalizeAnnouncementInterval` 表驱动测试:空 / 非法 / 低于下限 / 高于上限 / 合法 → clamp 结果。
- 扩展 `setting_service_public_test.go`(或 dto 测试):公开设置含两个新键,默认 `"[]"` / `3000`。

前端
- `HomeView` 测试(fake timers):
  - 多条 banner 按配置间隔轮换 currentIndex;
  - 空列表回退到内置默认文案;
  - 单条 banner 不轮换;
  - 当前 locale 文案缺失时回退另一语言;
  - 未配置间隔时回退 3000。
- app-store 测试:两个字段经 `cachedPublicSettings` 正确 round-trip。

## 显式的范围约定

- 内置 `copy.announcement` 双语字符串**保留**作为空列表时的回退默认,不迁入数据库、不作为
  预置的第一条可配置 banner。管理员配置的 banner 覆盖式叠加于其上,空则回退。
- 不做 per-item 启用开关、不做链接/点击、不做关闭状态持久化(维持现状仅会话内)。
- 不引入新的存储/下发机制,全部复用现有 setting 管线。

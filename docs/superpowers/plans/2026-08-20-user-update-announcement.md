# User Update Announcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reuse the existing announcement system to deliver the approved branded multi-item popup to ordinary users and consolidate rolling-banner configuration into the existing admin announcement page.

**Architecture:** Keep `/admin/announcements`, all announcement APIs, tables, targeting, schedules, and read records unchanged. Add one focused rolling-banner card to the existing admin page, refactor the Pinia queue to separate “read and advance” from “snooze,” and restyle the shared popup with current site branding. Rolling-banner writes use the existing partial settings PUT and send only their two owned fields.

**Tech Stack:** Vue 3 `<script setup>`, TypeScript 5.6, Pinia, Vue Router, vue-i18n, Tailwind CSS, Vitest, Vue Test Utils, Go/Gin regression tests.

**Spec:** `docs/superpowers/specs/2026-08-20-user-update-announcement-design.md`

## Global Constraints

- Preserve `/admin/announcements`, `/api/v1/admin/announcements*`, `/api/v1/announcements*`, `announcements`, and `announcement_reads` unchanged.
- Do not modify administrator onboarding, its `driver.js` configuration, storage keys, steps, CSS, or the administrator compliance flow.
- Preserve `announcement_banners` and `announcement_banner_interval_ms` formats; no migration is allowed.
- The rolling-banner card must PUT only `announcement_banners` and `announcement_banner_interval_ms`.
- Keep loading announcements for administrators so their announcement bell works; only automatic popup enqueueing is disabled for administrators.
- Continue parsing with `marked` and sanitizing with DOMPurify.
- Use existing `Icon` names and site branding; add no image or icon dependency.
- Preserve unrelated dirty-worktree changes and stage only files named by the active task.

## File Structure

- `frontend/src/components/admin/announcements/AnnouncementBannerSettingsCard.vue` — rolling-banner load, validation, editing, preview, and partial save.
- `frontend/src/views/admin/AnnouncementsView.vue` — composes existing CRUD UI with the new card.
- `frontend/src/views/admin/SettingsView.vue` — stops owning rolling-banner fields.
- `frontend/src/stores/announcements.ts` — popup batch position, read/advance, snooze, and fetch eligibility.
- `frontend/src/App.vue` — supplies role-based automatic-popup eligibility while retaining data fetches.
- `frontend/src/components/common/AnnouncementPopup.vue` — approved branded popup presentation.
- Co-located Vitest files specify each boundary independently.

---

### Task 1: Rename the Existing Admin Entry Without Changing Its Route

**Files:**
- Modify: `frontend/src/components/layout/AppSidebar.vue:861`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- Modify: `frontend/src/i18n/locales/zh/common.ts:168`
- Modify: `frontend/src/i18n/locales/en/common.ts:168`
- Modify: `frontend/src/i18n/locales/zh/admin/resources.ts:387`
- Modify: `frontend/src/i18n/locales/en/admin/resources.ts:390`

**Interfaces:**
- Consumes: existing `/admin/announcements` route and `BellIcon`.
- Produces: `nav.announcementSettings`; updated page title and description.

- [ ] **Step 1: Write the failing sidebar test**

Add to `AppSidebar.spec.ts`:

```ts
describe('AppSidebar announcement settings navigation', () => {
  it.each([
    ['standard admin', { admin: true, simple: false }],
    ['simple-mode admin', { admin: true, simple: true }],
  ] as const)('keeps the route and settings label for %s', (_label, options) => {
    const wrapper = mountSidebar(options)
    const link = wrapper.get('[data-route="/admin/announcements"]')
    expect(link.text()).toContain('nav.announcementSettings')
    expect(link.text()).not.toContain('nav.announcements')
  })

  it('hides the admin route from ordinary users', () => {
    const wrapper = mountSidebar({ admin: false, simple: false })
    expect(wrapper.find('[data-route="/admin/announcements"]').exists()).toBe(false)
  })
})
```

- [ ] **Step 2: Run the test and confirm failure**

```bash
cd frontend && pnpm test:run src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: FAIL because the link still renders `nav.announcements`.

- [ ] **Step 3: Add the admin-specific key and update page copy**

Change only the admin item:

```ts
{ path: '/admin/announcements', label: t('nav.announcementSettings'), icon: BellIcon },
```

Add beside the shared announcement key:

```ts
// zh/common.ts
announcementSettings: '公告设置',

// en/common.ts
announcementSettings: 'Announcement settings',
```

Update the existing `admin.announcements` headings while preserving all child keys:

```ts
// zh/admin/resources.ts
title: '公告设置',
description: '统一管理登录弹窗与站内顶部滚动公告',

// en/admin/resources.ts
title: 'Announcement settings',
description: 'Manage login popups and top rolling announcements in one place',
```

- [ ] **Step 4: Run focused tests and typecheck**

```bash
cd frontend && pnpm test:run src/components/layout/__tests__/AppSidebar.spec.ts && pnpm typecheck
```

Expected: PASS and exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/i18n/locales/zh/common.ts frontend/src/i18n/locales/en/common.ts frontend/src/i18n/locales/zh/admin/resources.ts frontend/src/i18n/locales/en/admin/resources.ts
git commit -m "feat(admin): clarify announcement settings navigation"
```

---

### Task 2: Build the Rolling-Announcement Settings Card

**Files:**
- Create: `frontend/src/components/admin/announcements/AnnouncementBannerSettingsCard.vue`
- Create: `frontend/src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/settings.ts:3`
- Modify: `frontend/src/i18n/locales/en/admin/settings.ts:3`

**Interfaces:**
- Consumes: `adminAPI.settings.getSettings()`, `adminAPI.settings.updateSettings()`, `useAppStore().fetchPublicSettings(true)`, and `AnnouncementBanner`.
- Produces: a zero-prop card whose save request owns exactly two fields.

- [ ] **Step 1: Write tests for load, reorder, validation, and partial save**

Create `AnnouncementBannerSettingsCard.spec.ts`:

```ts
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AnnouncementBannerSettingsCard from '../AnnouncementBannerSettingsCard.vue'

const mocks = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), fetchPublicSettings: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(),
}))

vi.mock('@/api', () => ({ adminAPI: { settings: {
  getSettings: mocks.getSettings, updateSettings: mocks.updateSettings,
} } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({
  fetchPublicSettings: mocks.fetchPublicSettings,
  showSuccess: mocks.showSuccess, showError: mocks.showError,
}) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AnnouncementBannerSettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getSettings.mockResolvedValue({
      announcement_banners: [
        { id: 'first', text_zh: '第一条', text_en: 'First' },
        { id: 'second', text_zh: '第二条', text_en: 'Second' },
      ],
      announcement_banner_interval_ms: 3000,
    })
    mocks.updateSettings.mockImplementation(async (payload) => payload)
    mocks.fetchPublicSettings.mockResolvedValue(null)
  })

  it('loads existing settings', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()
    expect(wrapper.get('[data-testid="announcement-banner-zh-0"]').element).toHaveProperty('value', '第一条')
    expect(wrapper.get('[data-testid="announcement-banner-interval"]').element).toHaveProperty('value', '3')
  })

  it('reorders and saves only owned fields', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()
    await wrapper.get('[data-testid="announcement-banner-down-0"]').trigger('click')
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()
    expect(mocks.updateSettings).toHaveBeenCalledWith({
      announcement_banners: [
        { id: 'second', text_zh: '第二条', text_en: 'Second' },
        { id: 'first', text_zh: '第一条', text_en: 'First' },
      ],
      announcement_banner_interval_ms: 3000,
    })
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
  })

  it('rejects a fully empty item', async () => {
    mocks.getSettings.mockResolvedValue({ announcement_banners: [], announcement_banner_interval_ms: 3000 })
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()
    await wrapper.get('[data-testid="announcement-banner-add"]').trigger('click')
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    expect(mocks.updateSettings).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.settings.announcementBanners.validationTextRequired')
  })
})
```

- [ ] **Step 2: Run the missing-component test**

```bash
cd frontend && pnpm test:run src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts
```

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Implement state, validation, and the exact save boundary**

Create `AnnouncementBannerSettingsCard.vue` around this complete behavior contract:

```ts
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type { AnnouncementBanner } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const banners = ref<AnnouncementBanner[]>([])
const intervalSeconds = ref(3)
const loading = ref(true)
const saving = ref(false)
const previewIndex = ref(0)

function addBanner() {
  if (banners.value.length < 20) banners.value.push({ id: '', text_zh: '', text_en: '' })
}
function removeBanner(index: number) {
  banners.value.splice(index, 1)
  previewIndex.value = Math.min(previewIndex.value, Math.max(0, banners.value.length - 1))
}
function moveBanner(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= banners.value.length) return
  const [item] = banners.value.splice(index, 1)
  banners.value.splice(target, 0, item)
  previewIndex.value = target
}
function validate(): string | null {
  if (!Number.isFinite(intervalSeconds.value) || intervalSeconds.value < 1 || intervalSeconds.value > 60)
    return t('admin.settings.announcementBanners.validationInterval')
  if (banners.value.some((item) => !item.text_zh.trim() && !item.text_en.trim()))
    return t('admin.settings.announcementBanners.validationTextRequired')
  if (banners.value.some((item) => item.text_zh.length > 200 || item.text_en.length > 200))
    return t('admin.settings.announcementBanners.validationTextLength')
  return null
}
async function load() {
  loading.value = true
  try {
    const settings = await adminAPI.settings.getSettings()
    banners.value = (settings.announcement_banners ?? []).map((item) => ({ ...item }))
    intervalSeconds.value = Math.min(60, Math.max(1,
      Math.round((settings.announcement_banner_interval_ms || 3000) / 1000)))
  } catch {
    appStore.showError(t('admin.settings.announcementBanners.loadFailed'))
  } finally { loading.value = false }
}
async function save() {
  const error = validate()
  if (error) return appStore.showError(error)
  saving.value = true
  try {
    const updated = await adminAPI.settings.updateSettings({
      announcement_banners: banners.value.map((item) => ({ ...item })),
      announcement_banner_interval_ms: Math.round(intervalSeconds.value * 1000),
    })
    banners.value = (updated.announcement_banners ?? banners.value).map((item) => ({ ...item }))
    await appStore.fetchPublicSettings(true)
    appStore.showSuccess(t('admin.settings.announcementBanners.saved'))
  } catch {
    appStore.showError(t('admin.settings.announcementBanners.saveFailed'))
  } finally { saving.value = false }
}
onMounted(load)
```

Use this concrete template shape, retaining the repository's `.card`, `.input`, `.btn`, and `Icon` styling:

```vue
<section class="card" data-testid="announcement-banner-settings-card">
  <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
    <h2>{{ t('admin.settings.announcementBanners.title') }}</h2>
    <p>{{ t('admin.settings.announcementBanners.description') }}</p>
  </header>
  <div class="space-y-4 p-6">
    <input v-model.number="intervalSeconds" data-testid="announcement-banner-interval" type="number" min="1" max="60" class="input w-32" />
    <div v-for="(item, index) in banners" :key="item.id || `new-${index}`" class="rounded-xl border p-4">
      <button v-if="index > 0" type="button" :data-testid="`announcement-banner-up-${index}`" @click="moveBanner(index, -1)"><Icon name="arrowUp" size="sm" /></button>
      <button v-if="index < banners.length - 1" type="button" :data-testid="`announcement-banner-down-${index}`" @click="moveBanner(index, 1)"><Icon name="arrowDown" size="sm" /></button>
      <button type="button" :data-testid="`announcement-banner-remove-${index}`" @click="removeBanner(index)"><Icon name="trash" size="sm" /></button>
      <input v-model="item.text_zh" :data-testid="`announcement-banner-zh-${index}`" maxlength="200" class="input" />
      <input v-model="item.text_en" :data-testid="`announcement-banner-en-${index}`" maxlength="200" class="input" />
    </div>
    <button data-testid="announcement-banner-add" type="button" class="btn btn-secondary" :disabled="banners.length >= 20" @click="addBanner">{{ t('admin.settings.announcementBanners.add') }}</button>
    <div v-if="banners[previewIndex]" data-testid="announcement-banner-preview" class="rounded-lg bg-gray-950 p-3 text-white">{{ banners[previewIndex].text_zh || banners[previewIndex].text_en }}</div>
    <button data-testid="announcement-banner-save" type="button" class="btn btn-primary" :disabled="loading || saving" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
  </div>
</section>
```

- [ ] **Step 4: Add complete bilingual messages**

Extend `admin.settings.announcementBanners` in both locales with natural translations for these exact keys:

```ts
textZh: '中文', textEn: 'English', preview: '滚动条预览',
saved: '滚动公告设置已保存', loadFailed: '加载滚动公告设置失败',
saveFailed: '保存滚动公告设置失败',
validationInterval: '切换间隔必须为 1–60 秒',
validationTextRequired: '每条滚动公告至少填写一种语言',
validationTextLength: '单条滚动公告每种语言最多 200 个字符',
```

```ts
textZh: 'Chinese', textEn: 'English', preview: 'Banner preview',
saved: 'Rolling announcement settings saved', loadFailed: 'Failed to load rolling announcement settings',
saveFailed: 'Failed to save rolling announcement settings',
validationInterval: 'The interval must be between 1 and 60 seconds',
validationTextRequired: 'Each rolling announcement requires at least one language',
validationTextLength: 'Each language is limited to 200 characters per rolling announcement',
```

- [ ] **Step 5: Run tests and typecheck**

```bash
cd frontend && pnpm test:run src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts && pnpm typecheck
```

Expected: PASS and exit 0.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/admin/announcements/AnnouncementBannerSettingsCard.vue frontend/src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts frontend/src/i18n/locales/zh/admin/settings.ts frontend/src/i18n/locales/en/admin/settings.ts
git commit -m "feat(admin): add rolling announcement settings card"
```

---

### Task 3: Move Rolling-Announcement Ownership to the Announcement Page

**Files:**
- Modify: `frontend/src/views/admin/AnnouncementsView.vue`
- Modify: `frontend/src/views/admin/SettingsView.vue`
- Create: `frontend/src/views/admin/__tests__/AnnouncementSettingsMigration.spec.ts`

**Interfaces:**
- Consumes: `AnnouncementBannerSettingsCard` from Task 2.
- Produces: exactly one rolling-announcement editor under existing announcement CRUD.

- [ ] **Step 1: Write the failing ownership test**

```ts
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const announcementsSource = readFileSync(resolve(process.cwd(), 'src/views/admin/AnnouncementsView.vue'), 'utf8')
const settingsSource = readFileSync(resolve(process.cwd(), 'src/views/admin/SettingsView.vue'), 'utf8')

describe('announcement settings ownership', () => {
  it('renders the rolling settings card from the announcement page', () => {
    expect(announcementsSource).toContain('import AnnouncementBannerSettingsCard')
    expect(announcementsSource).toContain('<AnnouncementBannerSettingsCard')
  })
  it('removes rolling announcement writes from generic settings', () => {
    expect(settingsSource).not.toContain('announcement_banners')
    expect(settingsSource).not.toContain('announcement_banner_interval_ms')
    expect(settingsSource).not.toContain('announcementIntervalSeconds')
    expect(settingsSource).not.toContain('addAnnouncementBanner')
    expect(settingsSource).not.toContain('moveAnnouncementBanner')
    expect(settingsSource).not.toContain('removeAnnouncementBanner')
  })
})
```

- [ ] **Step 2: Run and confirm both ownership assertions fail**

```bash
cd frontend && pnpm test:run src/views/admin/__tests__/AnnouncementSettingsMigration.spec.ts
```

Expected: FAIL because the card is absent and `SettingsView` still owns the fields.

- [ ] **Step 3: Mount the card below existing CRUD**

Import in `AnnouncementsView.vue`:

```ts
import AnnouncementBannerSettingsCard from '@/components/admin/announcements/AnnouncementBannerSettingsCard.vue'
```

Render after `</TablePageLayout>` and before dialogs:

```vue
<AnnouncementBannerSettingsCard class="mt-6" />
```

- [ ] **Step 4: Remove old banner ownership from SettingsView**

Delete the old “Announcement Banners” template block, the two form fields, their load assignments, computed seconds adapter, add/remove/move functions, and both fields from `saveSettings()`:

```ts
announcement_banners
announcement_banner_interval_ms
announcementIntervalSeconds
addAnnouncementBanner
removeAnnouncementBanner
moveAnnouncementBanner
```

Do not remove shared frontend API types or backend DTO fields.

- [ ] **Step 5: Run migration and settings regression tests**

```bash
cd frontend && pnpm test:run src/views/admin/__tests__/AnnouncementSettingsMigration.spec.ts src/views/admin/__tests__/SettingsView.spec.ts src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts && pnpm typecheck
```

Expected: all PASS and no unresolved banner form references.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/admin/AnnouncementsView.vue frontend/src/views/admin/SettingsView.vue frontend/src/views/admin/__tests__/AnnouncementSettingsMigration.spec.ts
git commit -m "refactor(admin): consolidate announcement settings page"
```

---

### Task 4: Specify and Implement Popup Batch Semantics

**Files:**
- Create: `frontend/src/stores/__tests__/announcements.spec.ts`
- Modify: `frontend/src/stores/announcements.ts`

**Interfaces:**
- Produces: `FetchAnnouncementOptions`, `fetchAnnouncements(options?)`, `popupPosition`, `popupTotal`, `advancePopup(): Promise<boolean>`, and `snoozePopupBatch(): void`.
- Preserves: `announcements`, `unreadCount`, `currentPopup`, `markAsRead`, `markAllAsRead`, and `reset` for the bell.

- [ ] **Step 1: Write failing store tests**

Create `announcements.spec.ts`:

```ts
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { announcementsAPI } from '@/api'
import { useAnnouncementStore } from '../announcements'

vi.mock('@/api', () => ({ announcementsAPI: { list: vi.fn(), markRead: vi.fn() } }))

const first = { id: 2, title: 'New', content: 'New body', notify_mode: 'popup' as const,
  created_at: '2026-08-20T02:00:00Z', updated_at: '2026-08-20T02:00:00Z' }
const second = { id: 1, title: 'Older', content: 'Older body', notify_mode: 'popup' as const,
  created_at: '2026-08-19T02:00:00Z', updated_at: '2026-08-19T02:00:00Z' }

describe('announcement popup batches', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks(); vi.useFakeTimers()
    vi.mocked(announcementsAPI.list).mockResolvedValue([first, second])
    vi.mocked(announcementsAPI.markRead).mockResolvedValue({ message: 'ok' })
  })
  afterEach(() => vi.useRealTimers())

  it('loads admin data without auto-enqueueing', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: false })
    expect(store.announcements).toHaveLength(2)
    expect(store.currentPopup).toBeNull()
  })

  it('marks only the current item read and advances', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    expect(store.currentPopup?.id).toBe(2)
    expect(store.popupPosition).toBe(1)
    expect(store.popupTotal).toBe(2)
    await expect(store.advancePopup()).resolves.toBe(true)
    expect(announcementsAPI.markRead).toHaveBeenCalledWith(2)
    await vi.advanceTimersByTimeAsync(300)
    expect(store.currentPopup?.id).toBe(1)
    expect(store.popupPosition).toBe(2)
  })

  it('snoozes the whole batch without reading', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    store.snoozePopupBatch()
    expect(store.currentPopup).toBeNull()
    expect(announcementsAPI.markRead).not.toHaveBeenCalled()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    expect(store.currentPopup).toBeNull()
  })

  it('advances but reports failed persistence', async () => {
    vi.mocked(announcementsAPI.markRead).mockRejectedValueOnce(new Error('offline'))
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    await expect(store.advancePopup()).resolves.toBe(false)
    expect(store.announcements.find((item) => item.id === 2)?.read_at).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run and confirm the contract mismatch**

```bash
cd frontend && pnpm test:run src/stores/__tests__/announcements.spec.ts
```

Expected: FAIL because the store accepts a boolean and exposes `dismissPopup`.

- [ ] **Step 3: Add typed options and counters**

```ts
export interface FetchAnnouncementOptions { force?: boolean; autoPopup?: boolean }
const popupPosition = ref(0)
const popupTotal = ref(0)

async function fetchAnnouncements(options: FetchAnnouncementOptions | boolean = {}) {
  const normalized = typeof options === 'boolean' ? { force: options } : options
  const { force = false, autoPopup = true } = normalized
  const now = Date.now()
  if (!force && lastFetchTime.value > 0 && now - lastFetchTime.value < THROTTLE_MS) return
  lastFetchTime.value = now
  try {
    loading.value = true
    announcements.value = (await announcementsAPI.list(false)).slice(0, 20)
    if (autoPopup) enqueueNewPopups()
  } catch (error) {
    lastFetchTime.value = 0
    console.error('Failed to fetch announcements:', error)
  } finally { loading.value = false }
}
```

Replace queue construction and selection with exact counter updates:

```ts
function enqueueNewPopups() {
  const candidates = announcements.value.filter((item) =>
    item.notify_mode === 'popup' && !item.read_at && !shownPopupIds.has(item.id))
  for (const item of candidates) {
    const alreadyQueued = popupQueue.value.some((queued) => queued.id === item.id)
    if (!alreadyQueued && currentPopup.value?.id !== item.id) {
      popupQueue.value.push(item)
      popupTotal.value += 1
    }
  }
  if (!currentPopup.value) showNextPopup()
}

function showNextPopup() {
  const next = popupQueue.value.shift()
  if (!next) { currentPopup.value = null; return }
  currentPopup.value = next
  shownPopupIds.add(next.id)
  popupPosition.value += 1
}
```

Keep a temporary compatibility alias so this commit remains type-safe before Task 6 replaces the current popup caller:

```ts
async function dismissPopup(): Promise<boolean> {
  return advancePopup()
}
```

- [ ] **Step 4: Implement explicit advance/read and snooze**

```ts
async function markAsRead(id: number): Promise<void> {
  await announcementsAPI.markRead(id)
  const item = announcements.value.find((announcement) => announcement.id === id)
  if (item) item.read_at = new Date().toISOString()
}

async function advancePopup(): Promise<boolean> {
  if (!currentPopup.value) return true
  const id = currentPopup.value.id
  currentPopup.value = null
  if (popupQueue.value.length > 0) window.setTimeout(showNextPopup, 300)
  else { popupPosition.value = 0; popupTotal.value = 0 }
  try { await markAsRead(id); return true }
  catch (error) { console.error('Failed to mark announcement as read:', error); return false }
}

function snoozePopupBatch(): void {
  if (currentPopup.value) shownPopupIds.add(currentPopup.value.id)
  for (const item of popupQueue.value) shownPopupIds.add(item.id)
  currentPopup.value = null
  popupQueue.value = []
  popupPosition.value = 0
  popupTotal.value = 0
}
```

Make `reset()` clear both counters and all queue/dedup state. Export the new contract. Keep `markAsRead` rejecting on API error so `AnnouncementBell`'s existing catch works.

- [ ] **Step 5: Run tests**

```bash
cd frontend && pnpm test:run src/stores/__tests__/announcements.spec.ts && pnpm typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/stores/announcements.ts frontend/src/stores/__tests__/announcements.spec.ts
git commit -m "refactor(announcements): separate popup advance and snooze"
```

---

### Task 5: Retain Admin Data While Disabling Admin Auto-Popups

**Files:**
- Modify: `frontend/src/App.vue:53-108`
- Create: `frontend/src/__tests__/App.announcements.spec.ts`
- Modify: `frontend/src/__tests__/App.branding.spec.ts`

**Interfaces:**
- Consumes: `fetchAnnouncements({ force?: boolean; autoPopup?: boolean })`.
- Produces: all authenticated roles fetch; only non-admins set `autoPopup: true`.

- [ ] **Step 1: Write role-sensitive fetch tests**

Create `App.announcements.spec.ts` with this complete harness (the same external boundaries as `App.branding.spec.ts`):

```ts
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ auth: null as any, fetchAnnouncements: vi.fn() }))

vi.mock('@/stores', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  state.auth = reactive({ isAuthenticated: false, isAdmin: false })
  return {
    useAppStore: () => ({ siteLogo: '', siteName: 'LINX2.AI', cachedPublicSettings: null, fetchPublicSettings: vi.fn().mockResolvedValue(null) }),
    useAuthStore: () => state.auth,
    useSubscriptionStore: () => ({ fetchActiveSubscriptions: vi.fn().mockResolvedValue(undefined), startPolling: vi.fn(), clear: vi.fn() }),
    useAnnouncementStore: () => ({ fetchAnnouncements: state.fetchAnnouncements, reset: vi.fn() }),
    useAdminComplianceStore: () => ({ fetchStatus: vi.fn().mockResolvedValue(undefined), requireAcknowledgement: vi.fn(), reset: vi.fn() }),
    useAdminSettingsStore: () => ({ customMenuItems: [] }),
  }
})
vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: vi.fn(), afterEach: vi.fn() }),
  useRoute: () => ({ path: '/', fullPath: '/', name: 'Home', params: {}, meta: {} }),
  RouterView: { template: '<div />' },
}))
vi.mock('@/api/setup', () => ({ getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false }) }))

import App from '@/App.vue'

function mountApp() {
  return mount(App, { global: { stubs: {
    RouterView: true, Toast: true, NavigationProgress: true,
    AnnouncementPopup: true, AdminComplianceDialog: true,
  } } })
}

describe('App announcement popup eligibility', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    state.fetchAnnouncements.mockReset()
    state.auth.isAuthenticated = false
    state.auth.isAdmin = false
  })
  afterEach(() => vi.useRealTimers())

it.each([
  ['ordinary user', false, true],
  ['administrator', true, false],
] as const)('fetches for %s with the correct popup flag', async (_label, isAdmin, expected) => {
  const wrapper = mountApp()
  state.auth.isAdmin = isAdmin
  state.auth.isAuthenticated = true
  await nextTick()
  await vi.advanceTimersByTimeAsync(3000)
  expect(state.fetchAnnouncements).toHaveBeenCalledWith({ force: true, autoPopup: expected })
  wrapper.unmount()
})
})
```

- [ ] **Step 2: Run and confirm old boolean calls fail**

```bash
cd frontend && pnpm test:run src/__tests__/App.announcements.spec.ts src/__tests__/App.branding.spec.ts
```

Expected: FAIL because App still calls `fetchAnnouncements(true)`.

- [ ] **Step 3: Pass explicit options at every fetch site**

```ts
function announcementFetchOptions(force = false) {
  return { force, autoPopup: !authStore.isAdmin }
}
```

Use these two call shapes in visibility changes, authentication restoration, delayed new login, and route changes:

```ts
announcementStore.fetchAnnouncements(announcementFetchOptions())
announcementStore.fetchAnnouncements(announcementFetchOptions(true))
```

Do not skip administrator fetches. After all call sites use option objects, narrow the store signature from `FetchAnnouncementOptions | boolean` to `FetchAnnouncementOptions` and remove the boolean normalization branch.

- [ ] **Step 4: Run App tests and typecheck**

```bash
cd frontend && pnpm test:run src/__tests__/App.announcements.spec.ts src/__tests__/App.branding.spec.ts && pnpm typecheck
```

Expected: PASS and exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.vue frontend/src/__tests__/App.announcements.spec.ts frontend/src/__tests__/App.branding.spec.ts
git commit -m "fix(announcements): limit automatic popups to ordinary users"
```

---

### Task 6: Implement the Approved Branded Popup

**Files:**
- Modify: `frontend/src/components/common/AnnouncementPopup.vue`
- Modify: `frontend/src/components/common/__tests__/AnnouncementPopup.spec.ts`
- Modify: `frontend/src/i18n/locales/zh/misc.ts:89`
- Modify: `frontend/src/i18n/locales/en/misc.ts:91`

**Interfaces:**
- Consumes: `currentPopup`, `popupPosition`, `popupTotal`, `advancePopup()`, `snoozePopupBatch()`, plus app-store branding and `showError`.
- Produces: test IDs `announcement-popup`, `announcement-popup-logo`, `announcement-popup-brand`, `announcement-popup-close`, `announcement-popup-snooze`, `announcement-popup-advance`, and `announcement-popup-progress`.

- [ ] **Step 1: Replace old dismissal expectations with approved interactions**

Before importing the component, add the exact app-store mock:

```ts
const appState = vi.hoisted(() => ({
  siteName: 'LINX2.AI',
  siteLogo: '',
  showError: vi.fn(),
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => appState }))
```

Update the existing Vue Test Utils import to `import { flushPromises, mount } from '@vue/test-utils'`. Keep the DOMPurify/Markdown and preview tests, then add:

```ts
it('renders site branding and batch progress', async () => {
  const store = useAnnouncementStore()
  store.currentPopup = announcement
  store.popupPosition = 1
  store.popupTotal = 3
  appState.siteName = 'Example Station'
  appState.siteLogo = 'https://example.com/logo.png'
  const wrapper = mount(AnnouncementPopup)
  await wrapper.vm.$nextTick()
  expect(document.body.querySelector('[data-testid="announcement-popup-brand"]')?.textContent).toContain('Example Station')
  expect(document.body.querySelector<HTMLImageElement>('[data-testid="announcement-popup-logo"]')?.src).toBe('https://example.com/logo.png')
  expect(document.body.querySelector('[data-testid="announcement-popup-progress"]')?.textContent).toContain('1 / 3')
  expect(document.body.querySelector('[data-testid="announcement-popup-advance"]')?.textContent).toContain('announcements.next')
})

it('snoozes without reading', async () => {
  const store = useAnnouncementStore()
  store.currentPopup = announcement
  const snooze = vi.spyOn(store, 'snoozePopupBatch')
  const advance = vi.spyOn(store, 'advancePopup')
  const wrapper = mount(AnnouncementPopup)
  document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-snooze"]')?.click()
  await wrapper.vm.$nextTick()
  expect(snooze).toHaveBeenCalledOnce()
  expect(advance).not.toHaveBeenCalled()
})

it('warns when read persistence fails', async () => {
  const store = useAnnouncementStore()
  store.currentPopup = announcement
  store.popupPosition = 1
  store.popupTotal = 2
  vi.spyOn(store, 'advancePopup').mockResolvedValue(false)
  mount(AnnouncementPopup)
  document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-advance"]')?.click()
  await flushPromises()
  expect(appState.showError).toHaveBeenCalledWith('announcements.readSaveFailed')
})

it('uses the acknowledgement label on the last item', () => {
  const store = useAnnouncementStore()
  store.currentPopup = announcement
  store.popupPosition = 2
  store.popupTotal = 2
  mount(AnnouncementPopup)
  expect(document.body.querySelector('[data-testid="announcement-popup-advance"]')?.textContent)
    .toContain('announcements.acknowledge')
})
```

Reset `appState` and `showError` in `beforeEach`. Add a close-button case calling `snoozePopupBatch`, and retain body overflow cleanup.

- [ ] **Step 2: Run and confirm old UI failure**

```bash
cd frontend && pnpm test:run src/components/common/__tests__/AnnouncementPopup.spec.ts
```

Expected: FAIL because the new selectors and actions do not exist.

- [ ] **Step 3: Add safe brand and batch computed values**

```ts
import { useAppStore } from '@/stores/app'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()
const siteName = computed(() => appStore.siteName || 'LINX2.AI')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', {
  allowRelative: true,
  allowDataUrl: true,
}) || '/linx2-icon.png')
const position = computed(() => props.preview ? 1 : announcementStore.popupPosition)
const total = computed(() => props.preview ? 1 : announcementStore.popupTotal)
const isLast = computed(() => position.value >= total.value)
```

Use `<img :src="siteLogo" :alt="siteName">`, existing `Icon`, neutral/primary site colors, responsive max height, dark mode, Teleport, transition, and shared Markdown styles. Do not invent a category field.

- [ ] **Step 4: Implement separate close and advance handlers**

```ts
function handleClose() {
  if (props.preview) return emit('close')
  announcementStore.snoozePopupBatch()
}

async function handleAdvance() {
  if (props.preview) return emit('close')
  const saved = await announcementStore.advancePopup()
  if (!saved) appStore.showError(t('announcements.readSaveFailed'))
}
```

Use this semantic structure:

```vue
<div data-testid="announcement-popup" role="dialog" aria-modal="true">
  <header data-testid="announcement-popup-brand">
    <img data-testid="announcement-popup-logo" :src="siteLogo" :alt="siteName" />
    <span>{{ siteName }}</span>
    <button data-testid="announcement-popup-close" type="button" @click="handleClose"><Icon name="x" size="sm" /></button>
  </header>
  <section>
    <h2>{{ displayedAnnouncement.title }}</h2>
    <time>{{ formatRelativeWithDateTime(displayedAnnouncement.created_at) }}</time>
    <div class="markdown-body prose prose-sm max-w-none dark:prose-invert" v-html="renderedContent"></div>
  </section>
  <footer>
    <span data-testid="announcement-popup-progress">{{ position }} / {{ total }}</span>
    <button v-if="!preview" data-testid="announcement-popup-snooze" type="button" class="btn btn-secondary" @click="handleClose">{{ t('announcements.later') }}</button>
    <button data-testid="announcement-popup-advance" type="button" class="btn btn-primary" @click="handleAdvance">
      {{ preview ? t('common.close') : isLast ? t('announcements.acknowledge') : t('announcements.next') }}
    </button>
  </footer>
</div>
```

Set `document.body.style.overflow = ''` whenever the popup becomes null and in `onBeforeUnmount`.

- [ ] **Step 5: Add bilingual popup copy**

```ts
// zh/misc.ts announcements
officialUpdate: '官方更新',
later: '稍后阅读',
next: '下一条',
acknowledge: '知道了',
readSaveFailed: '已读状态保存失败，下次可能再次提醒',

// en/misc.ts announcements
officialUpdate: 'Official update',
later: 'Read later',
next: 'Next',
acknowledge: 'Got it',
readSaveFailed: 'Could not save the read status; this announcement may appear again',
```

- [ ] **Step 6: Remove the obsolete caller and run focused tests**

Delete all `dismissPopup` references, then run:

```bash
rg -n "dismissPopup" frontend/src
cd frontend && pnpm test:run src/components/common/__tests__/AnnouncementPopup.spec.ts src/stores/__tests__/announcements.spec.ts && pnpm typecheck
```

Expected: `rg` has no matches; tests PASS; typecheck exits 0.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/common/AnnouncementPopup.vue frontend/src/components/common/__tests__/AnnouncementPopup.spec.ts frontend/src/i18n/locales/zh/misc.ts frontend/src/i18n/locales/en/misc.ts frontend/src/stores/announcements.ts
git commit -m "feat(announcements): add branded multi-item user popup"
```

---

### Task 7: Full Regression and Compatibility Verification

**Files:**
- Test only; modify only feature files if a failing test identifies an in-scope defect.

**Interfaces:**
- Consumes: all prior outputs.
- Produces: evidence that frontend build and existing backend contracts remain intact.

- [ ] **Step 1: Run all focused frontend suites together**

```bash
cd frontend && pnpm test:run \
  src/components/layout/__tests__/AppSidebar.spec.ts \
  src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts \
  src/views/admin/__tests__/AnnouncementSettingsMigration.spec.ts \
  src/views/admin/__tests__/SettingsView.spec.ts \
  src/stores/__tests__/announcements.spec.ts \
  src/__tests__/App.announcements.spec.ts \
  src/__tests__/App.branding.spec.ts \
  src/components/common/__tests__/AnnouncementPopup.spec.ts \
  src/composables/__tests__/useAnnouncementBanner.spec.ts
```

Expected: all focused suites PASS.

- [ ] **Step 2: Run the full frontend quality gate**

```bash
cd frontend && pnpm lint:check && pnpm typecheck && pnpm test:run && pnpm build
```

Expected: every command exits 0. Record an unrelated dirty-worktree failure precisely; do not edit unrelated code.

- [ ] **Step 3: Run backend contract regressions**

```bash
cd backend && go test ./internal/service -run Announcement -count=1
cd backend && go test ./internal/repository -run Announcement -count=1
cd backend && go test -tags=unit ./internal/handler/admin -run 'Announcement|PartialPayload' -count=1
```

Expected: PASS, proving route, targeting, read-status, and partial-update compatibility.

- [ ] **Step 4: Inspect forbidden paths**

Tasks 1–6 each create one commit. Before any optional verification-fix commit, run:

```bash
git diff --stat HEAD~6..HEAD
git diff HEAD~6..HEAD -- backend/internal/server/routes backend/migrations frontend/src/components/Guide frontend/src/composables/useOnboardingTour.ts frontend/src/components/admin/AdminComplianceDialog.vue
```

Expected: the second command is empty.

- [ ] **Step 5: Perform browser smoke testing**

Run `cd frontend && pnpm dev`, then verify:

1. “公告设置” opens `/admin/announcements` in both admin modes.
2. Existing CRUD, preview, targeting, schedules, and read-status still work.
3. Existing rolling-banner values load, reorder, save independently, and update the top bar.
4. System settings no longer contains the rolling-banner panel.
5. An ordinary user can advance through the branded popup and read items stay dismissed after refresh.
6. Close / “稍后阅读” leaves the bell unread count unchanged.
7. An administrator still has bell data but receives no automatic user popup.
8. Administrator onboarding and compliance dialogs are unchanged.

- [ ] **Step 6: Commit only an in-scope verification fix**

If needed:

```bash
git add frontend/src/App.vue frontend/src/components/common/AnnouncementPopup.vue frontend/src/components/common/__tests__/AnnouncementPopup.spec.ts frontend/src/stores/announcements.ts frontend/src/stores/__tests__/announcements.spec.ts frontend/src/views/admin/AnnouncementsView.vue frontend/src/views/admin/SettingsView.vue frontend/src/components/admin/announcements/AnnouncementBannerSettingsCard.vue frontend/src/components/admin/announcements/__tests__/AnnouncementBannerSettingsCard.spec.ts
git commit -m "fix(announcements): address integration regressions"
```

If no fix is needed, create no empty commit; report the passing commands in the handoff.

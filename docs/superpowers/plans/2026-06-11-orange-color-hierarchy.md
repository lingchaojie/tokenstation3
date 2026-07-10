# Orange Color Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Distinguish primary orange actions, secondary orange accents, and user identity avatar color across the homepage and logged-in UI.

**Architecture:** Add small semantic color utility classes in `frontend/src/style.css` so components can express intent without repeating raw Tailwind color stacks. Apply those classes only to the current collision points: homepage accents/theme/CTA/avatar initials, app header avatar fallback, sidebar theme icon, and profile avatar fallback. Keep primary buttons orange, move decorative signals to amber/copper, and move default avatars to a deeper burnt-orange identity gradient.

**Tech Stack:** Vue 3 SFCs, Tailwind CSS utility classes, Vitest, Vue Test Utils, source-level component contract tests.

---

## File Structure

- Modify `frontend/src/style.css`
  - Add semantic component classes under existing `@layer components`: `.ui-accent-dot`, `.ui-accent-badge`, `.ui-theme-toggle`, `.ui-theme-icon-accent`, `.ui-avatar-identity`, `.ui-avatar-identity-sm`, `.ui-avatar-identity-md`, `.ui-avatar-identity-lg`.
- Modify `frontend/src/views/HomeView.vue`
  - Keep primary CTA as `bg-primary-500`.
  - Replace homepage decorative dots and pricing/route badges with accent classes.
  - Replace homepage authenticated user initials chip with identity avatar class.
  - Change homepage theme toggle from a generic surface button to low-emphasis `ui-theme-toggle` with accent icon class.
- Modify `frontend/src/components/layout/AppHeader.vue`
  - Replace user dropdown fallback avatar primary-orange class stack with identity avatar classes.
- Modify `frontend/src/components/layout/AppSidebar.vue`
  - Replace theme Sun icon `text-amber-500` with semantic `ui-theme-icon-accent`.
- Modify `frontend/src/components/user/profile/ProfileInfoCard.vue`
  - Replace profile hero fallback avatar primary gradient with identity avatar classes.
- Modify `frontend/src/components/user/profile/ProfileAvatarCard.vue`
  - Replace profile avatar editor fallback avatar primary gradient with identity avatar classes for embedded and full modes.
- Modify tests:
  - `frontend/src/views/__tests__/HomeView.spec.ts`
  - `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
  - `frontend/src/components/user/profile/__tests__/ProfileInfoCard.spec.ts`
  - `frontend/src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts`
- Create `frontend/src/components/layout/__tests__/AppHeaderColorContract.spec.ts`
  - Source-level contract test because no AppHeader mount test exists and AppHeader depends on stores/router/layout integrations.

---

### Task 1: Add semantic color utilities

**Files:**
- Modify: `frontend/src/style.css:66-150`
- Test: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`

- [ ] **Step 1: Write the failing style contract test**

Append this test to `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`:

```ts
describe('Orange semantic color utilities', () => {
  it('defines separate accent and identity avatar classes', () => {
    expect(styleSource).toContain('.ui-accent-dot')
    expect(styleSource).toContain('@apply bg-amber-400')
    expect(styleSource).toContain('.ui-accent-badge')
    expect(styleSource).toContain('@apply border-amber-400/30 bg-amber-500/10 text-amber-300')
    expect(styleSource).toContain('.ui-theme-toggle')
    expect(styleSource).toContain('.ui-theme-icon-accent')
    expect(styleSource).toContain('.ui-avatar-identity')
    expect(styleSource).toContain('@apply bg-gradient-to-br from-orange-700 via-orange-600 to-rose-600')
    expect(styleSource).toContain('.ui-avatar-identity-sm')
    expect(styleSource).toContain('.ui-avatar-identity-md')
    expect(styleSource).toContain('.ui-avatar-identity-lg')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: FAIL because `styleSource` does not contain `.ui-accent-dot` and the other new semantic classes.

- [ ] **Step 3: Add the semantic classes**

In `frontend/src/style.css`, inside the existing `@layer components {` block, add this block after the `.btn-icon` definition:

```css
  .ui-accent-dot {
    @apply bg-amber-400;
  }

  .ui-accent-badge {
    @apply border-amber-400/30 bg-amber-500/10 text-amber-300;
  }

  .ui-theme-toggle {
    @apply rounded-lg border border-linear-hairline bg-linear-surface-1/70 p-2 text-linear-ink-subtle transition-colors;
    @apply hover:border-amber-400/30 hover:bg-amber-500/10 hover:text-amber-200;
  }

  .ui-theme-icon-accent {
    @apply text-amber-400;
  }

  .ui-avatar-identity {
    @apply bg-gradient-to-br from-orange-700 via-orange-600 to-rose-600 text-white shadow-lg shadow-orange-900/20;
  }

  .ui-avatar-identity-sm {
    @apply ui-avatar-identity flex h-5 w-5 items-center justify-center rounded-md text-[10px] font-semibold;
  }

  .ui-avatar-identity-md {
    @apply ui-avatar-identity flex h-8 w-8 items-center justify-center rounded-lg text-sm font-medium;
  }

  .ui-avatar-identity-lg {
    @apply ui-avatar-identity flex h-20 w-20 items-center justify-center rounded-[1.75rem] text-2xl font-bold;
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: PASS, including the new `Orange semantic color utilities` test.

---

### Task 2: Apply color hierarchy to homepage

**Files:**
- Modify: `frontend/src/views/HomeView.vue:20-90,94-100,160-285`
- Test: `frontend/src/views/__tests__/HomeView.spec.ts`

- [ ] **Step 1: Write the failing HomeView color hierarchy assertions**

In the main test `renders the dark-orange LINX2 landing shell with USD model pricing by default`, after the existing CTA assertions around `headerCta`, add:

```ts
    expect(headerCta.classes()).toContain('bg-primary-500')
    expect(headerCta.classes()).not.toContain('ui-theme-toggle')

    const themeToggle = wrapper.get('[data-testid="homepage-theme-toggle"]')
    expect(themeToggle.classes()).toContain('ui-theme-toggle')
    expect(themeToggle.classes()).not.toContain('bg-primary-500')

    const accentBadges = wrapper.findAll('.ui-accent-badge')
    expect(accentBadges.length).toBeGreaterThanOrEqual(6)

    const accentDots = wrapper.findAll('.ui-accent-dot')
    expect(accentDots.length).toBeGreaterThanOrEqual(2)
```

In the admin CTA test `routes authenticated admin users to the dashboard CTA`, after `const headerCta = wrapper.get('header a[href="/admin/dashboard"]')`, add:

```ts
    const userInitial = headerCta.get('.ui-avatar-identity-sm')
    expect(userInitial.text()).toBe('A')
    expect(userInitial.classes()).not.toContain('bg-white/15')
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: FAIL because `homepage-theme-toggle`, `.ui-accent-badge`, `.ui-accent-dot`, and `.ui-avatar-identity-sm` are not yet present.

- [ ] **Step 3: Update homepage classes**

In `frontend/src/views/HomeView.vue`, make these replacements:

```vue
<span class="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-primary-400"></span>
```

becomes:

```vue
<span class="ui-accent-dot h-1.5 w-1.5 flex-shrink-0 rounded-full"></span>
```

```vue
<button
  @click="toggleTheme"
  class="rounded-lg border border-linear-hairline bg-linear-surface-1 p-2 text-linear-ink-subtle transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
  :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
>
  <Icon v-if="isDark" name="sun" size="md" />
  <Icon v-else name="moon" size="md" />
</button>
```

becomes:

```vue
<button
  data-testid="homepage-theme-toggle"
  @click="toggleTheme"
  class="ui-theme-toggle"
  :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
>
  <Icon v-if="isDark" name="sun" size="md" class="ui-theme-icon-accent" />
  <Icon v-else name="moon" size="md" class="ui-theme-icon-accent" />
</button>
```

```vue
<span
  v-if="isAuthenticated && userInitial"
  class="flex h-5 w-5 items-center justify-center rounded-md bg-white/15 text-[10px]"
>
```

becomes:

```vue
<span
  v-if="isAuthenticated && userInitial"
  class="ui-avatar-identity-sm"
>
```

```vue
<span class="h-1.5 w-1.5 rounded-full bg-primary-400"></span>
```

becomes:

```vue
<span class="ui-accent-dot h-1.5 w-1.5 rounded-full"></span>
```

Every route badge span currently using:

```vue
class="font-mono-brand rounded-full border border-linear-hairline bg-linear-canvas px-2 py-0.5 text-[10px] uppercase tracking-wider text-primary-300"
```

becomes:

```vue
class="font-mono-brand ui-accent-badge rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider"
```

Every pricing Latest span currently using:

```vue
class="font-mono-brand rounded-full border border-primary-400/30 bg-primary-500/10 px-2 py-0.5 text-[10px] uppercase tracking-wider text-primary-300"
```

becomes:

```vue
class="font-mono-brand ui-accent-badge rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider"
```

Do not change the main CTA class; it must keep `bg-primary-500`.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: PASS with 8 tests passing.

---

### Task 3: Apply identity color to app header user avatar

**Files:**
- Modify: `frontend/src/components/layout/AppHeader.vue:70-85`
- Create: `frontend/src/components/layout/__tests__/AppHeaderColorContract.spec.ts`

- [ ] **Step 1: Write the failing AppHeader source contract test**

Create `frontend/src/components/layout/__tests__/AppHeaderColorContract.spec.ts` with:

```ts
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const componentSource = readFileSync(componentPath, 'utf8')

describe('AppHeader color hierarchy contract', () => {
  it('uses the identity avatar treatment for user fallback initials', () => {
    expect(componentSource).toContain('ui-avatar-identity-md overflow-hidden')
    expect(componentSource).not.toContain('border border-primary-400/30 bg-primary-500')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppHeaderColorContract.spec.ts
```

Expected: FAIL because AppHeader still contains `border border-primary-400/30 bg-primary-500` and does not contain `ui-avatar-identity-md overflow-hidden`.

- [ ] **Step 3: Update AppHeader avatar fallback class**

In `frontend/src/components/layout/AppHeader.vue`, replace:

```vue
<div class="flex h-8 w-8 items-center justify-center overflow-hidden rounded-lg border border-primary-400/30 bg-primary-500 text-sm font-medium text-white">
```

with:

```vue
<div class="ui-avatar-identity-md overflow-hidden">
```

Keep the nested `<img>` and fallback `<span>` unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppHeaderColorContract.spec.ts
```

Expected: PASS.

---

### Task 4: Apply accent theme icon to sidebar

**Files:**
- Modify: `frontend/src/components/layout/AppSidebar.vue:145-153`
- Test: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`

- [ ] **Step 1: Write the failing Sidebar assertion**

Append this test to `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`:

```ts
describe('AppSidebar theme toggle color hierarchy', () => {
  it('uses the semantic accent class for the sun icon', () => {
    expect(componentSource).toContain('class="h-5 w-5 flex-shrink-0 ui-theme-icon-accent"')
    expect(componentSource).not.toContain('text-amber-500')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: FAIL because `text-amber-500` is still present and `ui-theme-icon-accent` is not used in AppSidebar.

- [ ] **Step 3: Update Sidebar Sun icon class**

In `frontend/src/components/layout/AppSidebar.vue`, replace:

```vue
<SunIcon v-if="isDark" class="h-5 w-5 flex-shrink-0 text-amber-500" />
```

with:

```vue
<SunIcon v-if="isDark" class="h-5 w-5 flex-shrink-0 ui-theme-icon-accent" />
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: PASS.

---

### Task 5: Apply identity color to profile avatars

**Files:**
- Modify: `frontend/src/components/user/profile/ProfileInfoCard.vue:8-19`
- Modify: `frontend/src/components/user/profile/ProfileAvatarCard.vue:15-20`
- Test: `frontend/src/components/user/profile/__tests__/ProfileInfoCard.spec.ts`
- Test: `frontend/src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts`

- [ ] **Step 1: Write failing ProfileInfoCard assertion**

In `frontend/src/components/user/profile/__tests__/ProfileInfoCard.spec.ts`, inside `renders the approved overview hero and two-column content shell`, after the existing `profile-overview-hero` assertion, add:

```ts
    const heroAvatar = wrapper.get('[data-testid="profile-overview-avatar"]')
    expect(heroAvatar.classes()).toContain('ui-avatar-identity-lg')
    expect(heroAvatar.classes()).not.toContain('from-primary-500')
```

- [ ] **Step 2: Write failing ProfileAvatarCard assertion**

Append this test to `frontend/src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts`:

```ts
  it('uses identity avatar colors for fallback initials in full and embedded modes', () => {
    authStoreState.user = createUser()

    const full = mount(ProfileAvatarCard, {
      props: { user: authStoreState.user },
      global: { stubs: { Icon: true } }
    })
    const fullAvatar = full.get('[data-testid="profile-avatar-shell"]')
    expect(fullAvatar.classes()).toContain('ui-avatar-identity')
    expect(fullAvatar.classes()).not.toContain('from-primary-500')

    const embedded = mount(ProfileAvatarCard, {
      props: { user: authStoreState.user, embedded: true },
      global: { stubs: { Icon: true } }
    })
    const embeddedAvatar = embedded.get('[data-testid="profile-avatar-shell"]')
    expect(embeddedAvatar.classes()).toContain('ui-avatar-identity')
    expect(embeddedAvatar.classes()).not.toContain('from-primary-500')
  })
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/user/profile/__tests__/ProfileInfoCard.spec.ts src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts
```

Expected: FAIL because the data-testids and identity classes are not present yet.

- [ ] **Step 4: Update ProfileInfoCard avatar shell**

In `frontend/src/components/user/profile/ProfileInfoCard.vue`, replace:

```vue
<div
  class="flex h-20 w-20 shrink-0 items-center justify-center overflow-hidden rounded-[1.75rem] bg-gradient-to-br from-primary-500 to-primary-600 text-2xl font-bold text-white shadow-lg shadow-primary-500/20"
>
```

with:

```vue
<div
  data-testid="profile-overview-avatar"
  class="ui-avatar-identity-lg shrink-0 overflow-hidden"
>
```

Keep the nested `<img>` and fallback `<span>` unchanged.

- [ ] **Step 5: Update ProfileAvatarCard avatar shell**

In `frontend/src/components/user/profile/ProfileAvatarCard.vue`, replace:

```vue
<div
  :class="props.embedded
    ? 'flex h-16 w-16 shrink-0 items-center justify-center overflow-hidden rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 text-xl font-bold text-white shadow-lg shadow-primary-500/20'
    : 'flex h-24 w-24 shrink-0 items-center justify-center overflow-hidden rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 text-3xl font-bold text-white shadow-lg shadow-primary-500/20'"
>
```

with:

```vue
<div
  data-testid="profile-avatar-shell"
  :class="[
    'ui-avatar-identity shrink-0 overflow-hidden rounded-2xl',
    props.embedded
      ? 'flex h-16 w-16 items-center justify-center text-xl font-bold'
      : 'flex h-24 w-24 items-center justify-center text-3xl font-bold'
  ]"
>
```

Keep the nested `<img>` and fallback `<span>` unchanged.

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/user/profile/__tests__/ProfileInfoCard.spec.ts src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts
```

Expected: PASS.

---

### Task 6: Final verification and browser check

**Files:**
- Verify all files touched above.

- [ ] **Step 1: Run focused test suite**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/components/layout/__tests__/AppHeaderColorContract.spec.ts src/components/user/profile/__tests__/ProfileInfoCard.spec.ts src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts
```

Expected: all listed test files pass.

- [ ] **Step 2: Run lint**

Run:

```bash
pnpm --dir frontend run lint:check
```

Expected: exit 0.

- [ ] **Step 3: Rebuild local dev service**

Run:

```bash
POSTGRES_PASSWORD=sub2api ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=admin123 JWT_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef TOTP_ENCRYPTION_KEY=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789 docker compose -f deploy/docker-compose.dev.yml --project-name tokenstation3-dev up --build -d
```

Expected: compose rebuilds and starts `sub2api-dev`.

- [ ] **Step 4: Verify service health**

Run:

```bash
curl -fsS http://127.0.0.1:8080/health
```

Expected:

```json
{"status":"ok"}
```

- [ ] **Step 5: Browser verify homepage and logged-in UI color hierarchy**

Use Playwright at `http://127.0.0.1:8080/home` and, if already authenticated, the profile/dashboard UI. Verify:

- Homepage main CTA still reads as primary orange.
- Homepage theme toggle is lower-emphasis and not the same orange fill as the CTA.
- Homepage accent dots, route badges, and `Latest` labels use amber/copper accent, not primary action orange.
- Header authenticated initials chip uses the deeper identity avatar treatment.
- App header user fallback avatar and profile fallback avatar use deeper identity gradient.
- Sidebar theme Sun icon uses the accent class.

- [ ] **Step 6: Review git diff**

Run:

```bash
git diff -- frontend/src/style.css frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts frontend/src/components/layout/AppHeader.vue frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppHeaderColorContract.spec.ts frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/components/user/profile/ProfileInfoCard.vue frontend/src/components/user/profile/ProfileAvatarCard.vue frontend/src/components/user/profile/__tests__/ProfileInfoCard.spec.ts frontend/src/components/user/profile/__tests__/ProfileAvatarCard.spec.ts
```

Expected: only semantic color hierarchy changes and matching tests.

---

## Self-Review Notes

- Spec coverage: Primary action, accent signal, identity avatar, homepage theme toggle, user header avatar, profile avatars, sidebar theme icon, and tests are all covered.
- Placeholder scan: no TBD/TODO/fill-in steps remain.
- Type consistency: all semantic class names are consistent across tasks: `ui-accent-dot`, `ui-accent-badge`, `ui-theme-toggle`, `ui-theme-icon-accent`, `ui-avatar-identity`, `ui-avatar-identity-sm`, `ui-avatar-identity-md`, `ui-avatar-identity-lg`.

import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

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

describe('AppSidebar theme toggle color hierarchy', () => {
  it('uses the semantic accent class for the sun icon', () => {
    expect(componentSource).toContain('class="h-5 w-5 flex-shrink-0 ui-theme-icon-accent"')
    expect(componentSource).not.toContain('text-amber-500')
  })
})

describe('AppSidebar admin personal dashboard navigation', () => {
  it('reuses the self dashboard item but remaps it into the admin my-account route namespace', () => {
    const adminPersonalBuilder = componentSource.match(
      /function buildAdminPersonalNavItems\(\): NavItem\[] \{[\s\S]*?\n\}/,
    )?.[0]

    expect(adminPersonalBuilder).toContain('buildSelfNavItems(true)')
    expect(adminPersonalBuilder).toContain("'/dashboard': '/admin/my-account/dashboard'")
    expect(componentSource).toContain('finalizeNav(buildAdminPersonalNavItems())')
  })
})

describe('AppSidebar model marketplace navigation', () => {
  it('keeps the model marketplace route in self navigation for authenticated users', () => {
    expect(componentSource).not.toContain('管理员灰度入口')
    expect(componentSource).toContain("path: '/dashboard/models'")
    expect(componentSource).toContain("t('nav.modelMarketplace')")
  })
})

describe('AppSidebar web chat navigation', () => {
  it('keeps the model marketplace route before the chat route in self navigation', () => {
    expect(componentSource).toContain("path: '/chat'")
    expect(componentSource).toContain("t('nav.chat')")
    expect(componentSource.indexOf("path: '/dashboard/models'")).toBeLessThan(componentSource.indexOf("path: '/chat'"))
  })
})

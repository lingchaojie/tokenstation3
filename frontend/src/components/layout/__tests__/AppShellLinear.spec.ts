import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const layoutDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const stylePath = resolve(layoutDir, '..', '..', 'style.css')
const appLayoutSource = readFileSync(resolve(layoutDir, 'AppLayout.vue'), 'utf8')
const sidebarSource = readFileSync(resolve(layoutDir, 'AppSidebar.vue'), 'utf8')
const headerSource = readFileSync(resolve(layoutDir, 'AppHeader.vue'), 'utf8')
const tablePageLayoutSource = readFileSync(resolve(layoutDir, 'TablePageLayout.vue'), 'utf8')
const styleSource = readFileSync(stylePath, 'utf8')

describe('Linear app shell source contract', () => {
  it('uses the near-black Linear canvas instead of the old mesh background', () => {
    expect(appLayoutSource).toContain('linx-shell-bg')
    expect(appLayoutSource).not.toContain('bg-mesh-gradient')
  })

  it('uses compact Linear sidebar and header surfaces', () => {
    expect(styleSource).toContain('.sidebar {')
    expect(styleSource).toContain('dark:bg-linear-canvas')
    expect(styleSource).toContain('dark:border-linear-hairline')
    expect(sidebarSource).not.toContain('shadow-glow')
    expect(headerSource).toContain('border-linear-hairline')
    expect(headerSource).toContain('bg-linear-canvas')
  })

  it('keeps table pages inside a shared Linear panel contract', () => {
    expect(tablePageLayoutSource).toContain('linx-panel')
    expect(tablePageLayoutSource).toContain('linx-page')
  })
})

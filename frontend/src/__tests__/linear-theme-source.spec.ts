import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')
const tailwindSource = readFileSync(resolve(root, 'tailwind.config.js'), 'utf8')
const styleSource = readFileSync(resolve(root, 'src/style.css'), 'utf8')

function cssBlock(selector: string): string {
  const escaped = selector.replace('.', '\\.')
  return styleSource.match(new RegExp(`${escaped}\\s*\\{[\\s\\S]*?\\n  \\}`))?.[0] ?? ''
}

describe('LINX2 Linear-inspired theme contract', () => {
  it('keeps LINX2 orange primary tokens and maps Linear tokens through theme variables', () => {
    expect(tailwindSource).toContain("500: '#f97316'")
    expect(tailwindSource).toContain('linear: {')
    expect(tailwindSource).toContain("canvas: 'rgb(var(--linear-canvas) / <alpha-value>)'")
    expect(tailwindSource).toContain("surface: {")
    expect(tailwindSource).toContain("1: 'rgb(var(--linear-surface-1) / <alpha-value>)'")
    expect(tailwindSource).toContain("hairline: 'rgb(var(--linear-hairline) / <alpha-value>)'")
    expect(tailwindSource).toContain("ink: {")
    expect(tailwindSource).toContain("DEFAULT: 'rgb(var(--linear-ink) / <alpha-value>)'")
    expect(styleSource).toContain('--linear-canvas: 249 250 251')
    expect(styleSource).toContain('.dark {')
    expect(styleSource).toContain('--linear-canvas: 1 1 2')
  })

  it('uses restrained primary buttons without gradient or glow-heavy shadow', () => {
    const block = cssBlock('.btn-primary')
    expect(block).toContain('@apply bg-primary-500 text-white')
    expect(block).toContain('hover:bg-primary-400')
    expect(block).not.toContain('bg-gradient-to-r')
    expect(block).not.toContain('shadow-primary')
  })

  it('defines reusable Linear helper classes for pages, panels, and code surfaces', () => {
    expect(styleSource).toContain('.linx-page')
    expect(styleSource).toContain('.linx-panel')
    expect(styleSource).toContain('.linx-panel-strong')
    expect(styleSource).toContain('.linx-section-kicker')
    expect(styleSource).toContain('.linx-code-panel')
    expect(styleSource).toContain('.linx-data-row')
  })

  it('keeps sidebar header overflow visible so version badge menus are not clipped', () => {
    const block = cssBlock('.sidebar-header')
    expect(block).not.toContain('@apply overflow-hidden')
  })
})

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

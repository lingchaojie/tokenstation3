import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')
const mainSource = readFileSync(resolve(root, 'src/main.ts'), 'utf8')

describe('promotion channel bootstrap contract', () => {
  it('initializes promotion attribution between theme and analytics before mount', () => {
    const bootstrapIndex = mainSource.indexOf('async function bootstrap()')
    const themeIndex = mainSource.indexOf('initThemeClass()', bootstrapIndex)
    const promotionIndex = mainSource.indexOf(
      'initializePromotionChannelAttribution()',
      bootstrapIndex
    )
    const analyticsIndex = mainSource.indexOf('init51laAnalytics()', bootstrapIndex)
    const mountIndex = mainSource.indexOf("app.mount('#app')", bootstrapIndex)

    expect(bootstrapIndex).toBeGreaterThan(-1)
    expect(themeIndex).toBeGreaterThan(bootstrapIndex)
    expect(promotionIndex).toBeGreaterThan(themeIndex)
    expect(analyticsIndex).toBeGreaterThan(promotionIndex)
    expect(mountIndex).toBeGreaterThan(analyticsIndex)
  })
})

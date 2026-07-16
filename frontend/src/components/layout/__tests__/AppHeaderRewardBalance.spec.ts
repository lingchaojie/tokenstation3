import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const componentSource = readFileSync(componentPath, 'utf8')

describe('AppHeader reward balance integration', () => {
  it('keeps the displayed total unchanged and reuses the breakdown on desktop and mobile', () => {
    expect(componentSource).toContain('formatHeaderMoney(availableBalance)')
    expect(componentSource).not.toContain('availableBalance.value + reward')
    expect(componentSource.match(/<RewardBalanceBreakdown/g)).toHaveLength(2)
    expect(componentSource).toContain(':summary="user.reward_balances"')
  })
})

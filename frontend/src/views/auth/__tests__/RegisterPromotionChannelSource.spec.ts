import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const authDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = readFileSync(resolve(authDir, 'RegisterView.vue'), 'utf8')

describe('RegisterView promotion channel contract', () => {
  it('keeps the affiliate field visible but read-only on promotion hosts', () => {
    expect(source).toContain(':readonly="promotionAffiliateLocked"')
    expect(source).toContain(':aria-readonly="promotionAffiliateLocked"')
  })

  it('uses promotion-aware resolution for query sync and submission', () => {
    expect(source).toContain(
      'resolvePromotionAffiliateCode([route.query.aff, route.query.aff_code])'
    )
    expect(source).toContain(
      'resolvePromotionAffiliateCode([formData.aff_code, loadAffiliateReferralCode()])'
    )
    expect(source.match(/\.\.\.\(affCode \? \{ aff_code: affCode \} : \{\}\)/g)).toHaveLength(2)
  })
})

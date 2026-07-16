import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getCurrentPromotionChannel,
  getPromotionOAuthOrigin,
  initializePromotionChannelAttribution,
  resolvePromotionAffiliateCode,
  resolvePromotionChannel
} from '@/utils/promotionChannel'
import {
  loadAffiliateReferralCode,
  storeAffiliateReferralCode
} from '@/utils/oauthAffiliate'

describe('promotionChannel', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    vi.useRealTimers()
  })

  it('matches only the exact YUNDU promotion hostname', () => {
    expect(resolvePromotionChannel('yundu.linx2.ai')).toEqual({
      affiliateCode: 'YUNDU',
      oauthOrigin: 'https://www.linx2.ai'
    })

    for (const hostname of [
      'preview.yundu.linx2.ai',
      'other.linx2.ai',
      'linx2.ai',
      'www.linx2.ai',
      'localhost',
      '127.0.0.1'
    ]) {
      expect(resolvePromotionChannel(hostname)).toBeNull()
    }
  })

  it('normalizes case, whitespace, and one trailing dot before matching', () => {
    expect(resolvePromotionChannel(' YUNDU.LINX2.AI. ')).toEqual({
      affiliateCode: 'YUNDU',
      oauthOrigin: 'https://www.linx2.ai'
    })
  })

  it('uses the runtime hostname for the current channel', () => {
    expect(getCurrentPromotionChannel()).toBeNull()
  })

  it('keeps YUNDU authoritative over stored and fallback affiliate codes', () => {
    storeAffiliateReferralCode('STALE')

    expect(resolvePromotionAffiliateCode(['OTHER'], 'yundu.linx2.ai')).toBe('YUNDU')
    expect(loadAffiliateReferralCode()).toBe('YUNDU')
  })

  it('preserves existing referral resolution outside promotion hosts', () => {
    expect(resolvePromotionAffiliateCode([' AFF123 '], 'www.linx2.ai')).toBe('AFF123')
    expect(loadAffiliateReferralCode()).toBe('AFF123')
  })

  it('initializes YUNDU attribution through the 30-day referral storage contract', () => {
    const now = Date.UTC(2026, 0, 1)
    vi.useFakeTimers()
    vi.setSystemTime(now)

    expect(initializePromotionChannelAttribution('yundu.linx2.ai')).toEqual({
      affiliateCode: 'YUNDU',
      oauthOrigin: 'https://www.linx2.ai'
    })
    expect(loadAffiliateReferralCode(now + 30 * 24 * 60 * 60 * 1000 - 1)).toBe('YUNDU')
    expect(loadAffiliateReferralCode(now + 30 * 24 * 60 * 60 * 1000 + 1)).toBe('')
  })

  it('returns the canonical OAuth origin only for the YUNDU promotion host', () => {
    expect(getPromotionOAuthOrigin('yundu.linx2.ai')).toBe('https://www.linx2.ai')
    expect(getPromotionOAuthOrigin('www.linx2.ai')).toBe('')
  })
})

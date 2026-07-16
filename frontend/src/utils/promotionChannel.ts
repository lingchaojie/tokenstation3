import {
  resolveAffiliateReferralCode,
  storeAffiliateReferralCode
} from '@/utils/oauthAffiliate'

export interface PromotionChannel {
  affiliateCode: string
  oauthOrigin: string
}

const PROMOTION_CHANNELS: Readonly<Record<string, PromotionChannel>> = Object.freeze({
  'yundu.linx2.ai': Object.freeze({
    affiliateCode: 'YUNDU',
    oauthOrigin: 'https://www.linx2.ai'
  })
})

function normalizeHostname(hostname: string): string {
  return hostname.trim().toLowerCase().replace(/\.$/, '')
}

function runtimeHostname(): string {
  return typeof window === 'undefined' ? '' : window.location.hostname
}

export function resolvePromotionChannel(hostname: string): PromotionChannel | null {
  return PROMOTION_CHANNELS[normalizeHostname(hostname)] ?? null
}

export function getCurrentPromotionChannel(): PromotionChannel | null {
  return resolvePromotionChannel(runtimeHostname())
}

export function initializePromotionChannelAttribution(
  hostname = runtimeHostname()
): PromotionChannel | null {
  const channel = resolvePromotionChannel(hostname)
  if (channel) {
    storeAffiliateReferralCode(channel.affiliateCode)
  }
  return channel
}

export function resolvePromotionAffiliateCode(
  fallbackValues: readonly unknown[] = [],
  hostname = runtimeHostname()
): string {
  const channel = resolvePromotionChannel(hostname)
  if (channel) {
    storeAffiliateReferralCode(channel.affiliateCode)
    return channel.affiliateCode
  }
  return resolveAffiliateReferralCode(...fallbackValues)
}

export function getPromotionOAuthOrigin(hostname = runtimeHostname()): string {
  return resolvePromotionChannel(hostname)?.oauthOrigin ?? ''
}

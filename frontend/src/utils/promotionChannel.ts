import {
  resolveAffiliateReferralCode,
  storeAffiliateReferralCode
} from '@/utils/oauthAffiliate'

export interface PromotionChannel {
  readonly affiliateCode: string
  readonly oauthOrigin: string
}

const PROMOTION_CHANNELS = Object.freeze({
  'yundu.linx2.ai': Object.freeze({
    affiliateCode: 'YUNDU',
    oauthOrigin: 'https://www.linx2.ai'
  } satisfies PromotionChannel)
})

type PromotionHostname = keyof typeof PROMOTION_CHANNELS

function normalizeHostname(hostname: string): string {
  return hostname.trim().toLowerCase().replace(/\.$/, '')
}

function runtimeHostname(): string {
  return typeof window === 'undefined' ? '' : window.location.hostname
}

function isPromotionHostname(hostname: string): hostname is PromotionHostname {
  return Object.prototype.hasOwnProperty.call(PROMOTION_CHANNELS, hostname)
}

export function resolvePromotionChannel(hostname: string): PromotionChannel | null {
  const normalizedHostname = normalizeHostname(hostname)
  return isPromotionHostname(normalizedHostname)
    ? PROMOTION_CHANNELS[normalizedHostname]
    : null
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

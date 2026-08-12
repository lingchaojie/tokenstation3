import { sanitizeUrl } from '@/utils/url'

export const DEFAULT_BRAND_LOGO_URL = '/linx2-icon.png'

export function resolveBrandLogoUrl(logoUrl: string): string {
  return (
    sanitizeUrl(logoUrl, {
      allowRelative: true,
      allowDataUrl: true,
    }) || DEFAULT_BRAND_LOGO_URL
  )
}

export function updateFavicon(logoUrl: string): void {
  const sanitizedLogoUrl = resolveBrandLogoUrl(logoUrl)

  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }

  link.type = sanitizedLogoUrl.endsWith('.svg') ? 'image/svg+xml' : 'image/x-icon'
  link.href = sanitizedLogoUrl
}

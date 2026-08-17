import { beforeEach, describe, expect, it } from 'vitest'
import { resolveBrandLogoUrl, updateFavicon } from '@/utils/branding'

describe('resolveBrandLogoUrl', () => {
  it('keeps the LINX2 icon as the public fallback', () => {
    expect(resolveBrandLogoUrl('')).toBe('/linx2-icon.png')
    expect(resolveBrandLogoUrl('javascript:alert(1)')).toBe('/linx2-icon.png')
  })

  it('keeps a configured safe logo URL', () => {
    expect(resolveBrandLogoUrl('https://example.com/custom-logo.png')).toBe(
      'https://example.com/custom-logo.png',
    )
  })
})

describe('updateFavicon', () => {
  beforeEach(() => {
    document.head.innerHTML = '<link rel="icon" href="/linx2-icon.png">'
  })

  it('replaces the default favicon with the configured logo', () => {
    updateFavicon('https://example.com/custom-logo.png')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.href).toBe('https://example.com/custom-logo.png')
  })

  it('ignores unsafe logo URLs', () => {
    updateFavicon('javascript:alert(1)')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/linx2-icon.png')
  })
})

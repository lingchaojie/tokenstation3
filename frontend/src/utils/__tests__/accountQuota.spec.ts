import { describe, expect, it } from 'vitest'
import { isOfficialGeminiBaseUrl } from '../accountQuota'

describe('isOfficialGeminiBaseUrl', () => {
  it('treats empty and the official host as official', () => {
    expect(isOfficialGeminiBaseUrl('')).toBe(true)
    expect(isOfficialGeminiBaseUrl(undefined)).toBe(true)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com')).toBe(true)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com/')).toBe(true)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com:443/')).toBe(true)
  })

  it('treats relays, path prefixes and invalid urls as custom', () => {
    expect(isOfficialGeminiBaseUrl('https://relay.example.com')).toBe(false)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com/gw')).toBe(false)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com/proxy/..')).toBe(false)
    expect(isOfficialGeminiBaseUrl('https://generativelanguage.googleapis.com:8443/')).toBe(false)
    expect(isOfficialGeminiBaseUrl('https://relay@generativelanguage.googleapis.com/')).toBe(false)
    expect(isOfficialGeminiBaseUrl('http://generativelanguage.googleapis.com')).toBe(false)
    expect(isOfficialGeminiBaseUrl('not a url')).toBe(false)
  })
})

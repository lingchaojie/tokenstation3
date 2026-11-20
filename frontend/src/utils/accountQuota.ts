import type { AccountType } from '@/types'

/** 支持账号配额（总/日/周上限、quota_used 累加、超限停调度）的账号类型，与后端 AccountTypeSupportsQuota 一致。 */
export function supportsAccountQuota(type?: AccountType | null): boolean {
  return type === 'apikey' || type === 'bedrock' || type === 'service_account'
}

const OFFICIAL_GEMINI_API_URL = 'https://generativelanguage.googleapis.com'

/** Gemini API Key 账号的 base_url 是否为官方地址；留空视为官方地址。 */
export function isOfficialGeminiBaseUrl(raw: string | null | undefined): boolean {
  const value = (raw ?? '').trim()
  if (!value) return true
  // Preserve the entered path so a normalized relay URL cannot appear official here.
  const endpoint = value.replace(/\/+$/, '').toLowerCase()
  return endpoint === OFFICIAL_GEMINI_API_URL || endpoint === `${OFFICIAL_GEMINI_API_URL}:443`
}

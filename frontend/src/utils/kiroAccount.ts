import type { Account } from '@/types'

function readBaseUrl(account: Pick<Account, 'credentials'> | null | undefined): string {
  if (!account?.credentials) return ''
  const raw = (account.credentials as Record<string, unknown>).base_url
  return typeof raw === 'string' ? raw.trim() : ''
}

export function isKiroRelayAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'> | null | undefined): boolean {
  if (!account || account.platform !== 'kiro' || account.type !== 'apikey') return false
  return readBaseUrl(account) !== ''
}

export function isKiroDirectApiKeyAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'> | null | undefined): boolean {
  if (!account || account.platform !== 'kiro' || account.type !== 'apikey') return false
  return readBaseUrl(account) === ''
}

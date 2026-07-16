import type { Account } from '@/types'

export const DEFAULT_KIRO_API_REGION = 'us-east-1'

export const KIRO_API_REGIONS = ['us-east-1', 'eu-central-1'] as const

export interface KiroAPIRegionOption {
  value: string
  label: string
  disabled?: boolean
}

export function resolveKiroAPIRegion(value: unknown): string {
  if (typeof value !== 'string') return DEFAULT_KIRO_API_REGION
  return value.trim() || DEFAULT_KIRO_API_REGION
}

export function buildKiroAPIRegionOptions(
  currentValue: unknown,
  labelFor: (region: string, legacy: boolean) => string
): KiroAPIRegionOption[] {
  const options: KiroAPIRegionOption[] = KIRO_API_REGIONS.map(region => ({
    value: region,
    label: labelFor(region, false)
  }))
  const currentRegion = resolveKiroAPIRegion(currentValue)

  if (!KIRO_API_REGIONS.some(region => region === currentRegion)) {
    options.push({
      value: currentRegion,
      label: labelFor(currentRegion, true),
      disabled: true
    })
  }

  return options
}

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

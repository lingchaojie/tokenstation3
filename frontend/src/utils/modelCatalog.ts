import type { PublicModelCatalogModel } from '@/api/settings'
import type { WebChatModel } from '@/api/chat'

export type ModelCatalogSortKey = 'default' | 'newest' | 'provider'

export interface ModelCatalogFilters {
  query: string
  provider: string
  modality: string
}

export interface ModelCatalogProviderOption {
  value: string
  label: string
  count: number
}

const MODEL_PROVIDER_ORDER = ['anthropic', 'openai', 'gemini', 'qwen', 'glm', 'deepseek', 'minimax', 'kimi']

function providerRank(provider: string): number {
  const rank = MODEL_PROVIDER_ORDER.indexOf(provider)
  return rank === -1 ? MODEL_PROVIDER_ORDER.length : rank
}

function releaseDate(model: PublicModelCatalogModel): string {
  return model.released_at || model.updated_at || ''
}

export function formatModelCatalogAmount(value: number): string {
  return `$${Number(value.toFixed(6)).toString()}`
}

export function formatContextWindow(value?: number): string {
  if (!value || value <= 0) return ''
  const oneMi = 1024 * 1024
  if (value >= 1000000) {
    if (value % oneMi === 0) return `${value / oneMi}M`
    return `${Number((value / 1000000).toFixed(2)).toString()}M`
  }
  if (value >= 1024 && value % 1024 === 0) return `${value / 1024}K`
  if (value >= 1000) return `${Number((value / 1000).toFixed(0)).toString()}K`
  return value.toString()
}

export function filterModelCatalog(
  models: PublicModelCatalogModel[],
  filters: ModelCatalogFilters,
): PublicModelCatalogModel[] {
  const query = filters.query.trim().toLowerCase()
  return models.filter((model) => {
    const providerMatch = filters.provider === 'all' || model.provider === filters.provider
    const modalityMatch = filters.modality === 'all' || model.modalities.includes(filters.modality)
    if (!providerMatch || !modalityMatch) return false
    if (!query) return true
    const searchText = [
      model.provider_name,
      model.model_name,
      model.display_name,
      model.description,
      ...model.features,
      ...model.modalities,
    ]
      .join(' ')
      .toLowerCase()
    return searchText.includes(query)
  })
}

export function sortModelCatalog(
  models: PublicModelCatalogModel[],
  sortKey: ModelCatalogSortKey,
): PublicModelCatalogModel[] {
  const copy = [...models]
  if (sortKey === 'newest') {
    return copy.sort((a, b) => releaseDate(b).localeCompare(releaseDate(a)) || a.display_name.localeCompare(b.display_name))
  }
  if (sortKey === 'provider') {
    return copy.sort((a, b) => providerRank(a.provider) - providerRank(b.provider) || a.display_name.localeCompare(b.display_name))
  }
  return copy.sort(
    (a, b) =>
      providerRank(a.provider) - providerRank(b.provider) ||
      releaseDate(b).localeCompare(releaseDate(a)) ||
      a.display_name.localeCompare(b.display_name),
  )
}

export function buildModelCatalogProviderOptions(models: PublicModelCatalogModel[]): ModelCatalogProviderOption[] {
  const counts = new Map<string, { label: string; count: number }>()
  for (const model of models) {
    const current = counts.get(model.provider) ?? { label: model.provider_name, count: 0 }
    current.count += 1
    counts.set(model.provider, current)
  }
  return [
    { value: 'all', label: 'All providers', count: models.length },
    ...Array.from(counts.entries())
      .map(([value, item]) => ({ value, label: item.label, count: item.count }))
      .sort((a, b) => providerRank(a.value) - providerRank(b.value) || a.label.localeCompare(b.label)),
  ]
}

export function modelCatalogAvailabilityKey(provider: string, model: string): string {
  const normalizedProvider = provider.trim().toLowerCase()
  const normalizedModel = model.trim()
  return normalizedProvider && normalizedModel ? `${normalizedProvider}\x00${normalizedModel}` : ''
}

export function filterModelCatalogByWebChatModels(
  catalogModels: PublicModelCatalogModel[],
  chatModels: WebChatModel[],
): PublicModelCatalogModel[] {
  const available = new Set(
    chatModels
      .map((model) => modelCatalogAvailabilityKey(model.provider, model.model))
      .filter((key) => key.length > 0),
  )
  return catalogModels.filter((model) =>
    available.has(modelCatalogAvailabilityKey(model.provider, model.model_name)),
  )
}

export function providerIconModel(providerKey: string): string {
  const iconModels: Record<string, string> = {
    anthropic: 'claude',
    openai: 'gpt-5',
    gemini: 'gemini',
    qwen: 'qwen',
    glm: 'glm',
    deepseek: 'deepseek',
    minimax: 'minimax',
    kimi: 'kimi',
  }
  return iconModels[providerKey] ?? providerKey
}

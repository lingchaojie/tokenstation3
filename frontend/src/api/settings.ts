import { apiClient } from './client'

export interface PublicModelPricingModel {
  name: string
  model: string
  input_per_million: number
  output_per_million: number
  cache_read_per_million: number
}

export interface PublicModelPricingProvider {
  provider: string
  accent_color: string
  models: PublicModelPricingModel[]
}

export interface PublicModelPricingResponse {
  providers: PublicModelPricingProvider[]
}

export async function getPublicModelPricing(): Promise<PublicModelPricingResponse> {
  const { data } = await apiClient.get<PublicModelPricingResponse>('/settings/model-pricing')
  return data
}

export interface PublicModelCatalogProvider {
  key: string
  name: string
  accent_color: string
  model_count: number
}

export interface PublicModelCatalogPriceLine {
  label: string
  amount: number
  unit: string
}

export interface PublicModelCatalogPricing {
  currency: string
  unit: string
  input_per_million?: number
  output_per_million?: number
  cache_read_per_million?: number
  price_lines?: PublicModelCatalogPriceLine[]
  note?: string
}

export interface PublicModelCatalogModel {
  provider: string
  provider_name: string
  model_name: string
  display_name: string
  modalities: string[]
  description: string
  context_window?: number
  features: string[]
  pricing: PublicModelCatalogPricing
  price_status: 'confirmed' | 'unverified'
  source_url?: string
  updated_at: string
}

export interface PublicModelCatalogResponse {
  updated_at: string
  providers: PublicModelCatalogProvider[]
  models: PublicModelCatalogModel[]
}

export async function getPublicModelCatalog(): Promise<PublicModelCatalogResponse> {
  const { data } = await apiClient.get<PublicModelCatalogResponse>('/settings/model-catalog')
  return data
}

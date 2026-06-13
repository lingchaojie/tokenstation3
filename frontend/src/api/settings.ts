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

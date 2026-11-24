import { apiClient } from '../client'

export interface CacheDiagnosticConfig {
  user_id: number
  model: string
  minutes: number
  max_samples: number
}

export interface CacheDiagnosticBody {
  bytes: number
  digest?: string
  cache_key_digest?: string
}

export interface CacheDiagnosticSample {
  started_at: string
  protocol: string
  ingress: CacheDiagnosticBody
  http_egress?: { account_id: number; body?: CacheDiagnosticBody }[]
  forward_attempts: number
  last_account_id?: number
  outcome: string
  first_token_ms?: number
  forward_usage?: { input_tokens: number; output_tokens: number; cache_read_input_tokens?: number }
  cache_read_known: boolean
  usage_unavailable: boolean
}

export interface CacheDiagnosticStatus {
  active: boolean
  config?: CacheDiagnosticConfig
  expires_at?: string
  admitted: number
  samples?: CacheDiagnosticSample[]
}

const endpoint = '/admin/accounts/user-cache-diagnostics'
export async function getCacheDiagnostics(): Promise<CacheDiagnosticStatus> {
  const { data } = await apiClient.get<CacheDiagnosticStatus>(endpoint)
  return data
}
export async function startCacheDiagnostics(config: CacheDiagnosticConfig): Promise<CacheDiagnosticStatus> {
  const { data } = await apiClient.put<CacheDiagnosticStatus>(endpoint, config)
  return data
}
export async function stopCacheDiagnostics(): Promise<CacheDiagnosticStatus> {
  const { data } = await apiClient.delete<CacheDiagnosticStatus>(endpoint)
  return data
}

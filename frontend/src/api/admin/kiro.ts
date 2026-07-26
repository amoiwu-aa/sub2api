/**
 * Admin Kiro (Amazon Q Developer) API endpoints.
 *
 * Kiro has no server-initiated OAuth flow — credentials are pasted from the
 * operator's local `~/.aws/sso/cache/kiro-auth-token.json`.
 */

import { apiClient } from '../client'
import type { Account } from '@/types'

export interface KiroTokenInfo {
  access_token: string
  refresh_token?: string
  expires_at: string
  auth_method: string
  profile_arn: string
  provider?: string
  /** Region used for the IdC OIDC refresh chain. */
  region?: string
  /** Region used to reach the Amazon Q endpoint, derived from profile_arn. */
  q_region: string
  client_id?: string
  client_secret?: string
  machine_id?: string
  kiro_version?: string
  /** True when the import performed a live refresh to validate the credentials. */
  refreshed: boolean
}

export interface KiroImportRequest {
  /** Raw contents of kiro-auth-token.json. */
  token_json: string
  /** `{ clientId, clientSecret }` — required for IdC accounts. */
  client_registration_json?: string
  proxy_id?: number | null
}

export interface KiroImportResponse {
  token_info: KiroTokenInfo
  /** Ready to hand to the generic account create endpoint. */
  credentials: Record<string, unknown>
}

export async function importToken(payload: KiroImportRequest): Promise<KiroImportResponse> {
  const { data } = await apiClient.post<KiroImportResponse>('/admin/kiro/import', payload)
  return data
}

export async function refreshAccountToken(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/kiro/accounts/${id}/refresh`)
  return data
}

export default { importToken, refreshAccountToken }

/**
 * Admin Cursor API endpoints.
 *
 * Two ways to add an account: open the browser login URL and poll, or paste an
 * existing WorkosCursorSessionToken cookie. Both end up with a `type=session`
 * JWT — a `type=web` token cannot drive the Agent.
 */

import { apiClient } from '../client'
import type { Account } from '@/types'

export interface CursorTokenInfo {
  access_token: string
  refresh_token?: string
  user_id?: string
  email?: string
  /** "session" once the token is usable by the Agent. */
  token_type?: string
  expires_at?: string
  /** Which branch of the auth chain produced this token. */
  source?: string
  agent_profile?: 'ide' | 'sand'
  machine_id?: string
  client_version?: string
  sand_namespace?: string
}

export interface CursorLoginStartRequest {
  proxy_id?: number | null
  agent_profile?: 'ide' | 'sand'
}

export interface CursorLoginStartResponse {
  login_url: string
  session_id: string
  uuid: string
}

export interface CursorLoginPollResponse {
  /** True while the browser side has not finished signing in. */
  pending: boolean
  token_info?: CursorTokenInfo
  credentials?: Record<string, unknown>
}

export interface CursorImportRequest {
  /** WorkosCursorSessionToken cookie or a raw JWT. */
  token?: string
  access_token?: string
  refresh_token?: string
  machine_id?: string
  client_version?: string
  sand_namespace?: string
  agent_profile?: 'ide' | 'sand'
  proxy_id?: number | null
  selected_team_id?: string
}

export interface CursorImportResponse {
  token_info: CursorTokenInfo
  credentials: Record<string, unknown>
}

export async function startLogin(
  payload: CursorLoginStartRequest
): Promise<CursorLoginStartResponse> {
  const { data } = await apiClient.post<CursorLoginStartResponse>('/admin/cursor/oauth/start', payload)
  return data
}

export async function pollLogin(sessionId: string): Promise<CursorLoginPollResponse> {
  const { data } = await apiClient.post<CursorLoginPollResponse>('/admin/cursor/oauth/poll', {
    session_id: sessionId
  })
  return data
}

export async function importToken(payload: CursorImportRequest): Promise<CursorImportResponse> {
  const { data } = await apiClient.post<CursorImportResponse>('/admin/cursor/import', payload)
  return data
}

export async function refreshAccountToken(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/cursor/accounts/${id}/refresh`)
  return data
}

export default { startLogin, pollLogin, importToken, refreshAccountToken }

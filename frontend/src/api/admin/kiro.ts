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

export default { importToken, refreshAccountToken, startWebLogin, completeWebLogin }

export interface KiroWebLoginStartResponse {
  /** 在浏览器里打开这个地址完成登录 */
  login_url: string
  session_id: string
  /** 登录完成后浏览器会跳到这个开头的地址（本机没监听，打不开是正常的） */
  callback_prefix: string
}

export async function startWebLogin(payload: {
  proxy_id?: number | null
}): Promise<KiroWebLoginStartResponse> {
  const { data } = await apiClient.post<KiroWebLoginStartResponse>('/admin/kiro/oauth/start', payload)
  return data
}

export interface KiroProfile {
  arn: string
  name?: string
}

/**
 * 一次「推进一步」的结果。
 *
 * Google / GitHub 账号一次就到 `completed`；企业（IAM Identity Center）账号
 * 的第一次回调只是交接，会返回 `idc_required` 和第二段登录链接——那次回调由
 * Kiro portal 发出，不带授权码，真正的授权码在随后 AWS 那次回调里。
 */
export interface KiroWebLoginResult {
  status: 'completed' | 'idc_required'
  session_id: string

  /** status=completed */
  token_info?: KiroTokenInfo
  credentials?: Record<string, unknown>
  /** 账号在 Amazon Q 上的可用 profile，多于一个时已取第一个 */
  profiles?: KiroProfile[]

  /** status=idc_required：需要在浏览器里再登录一次 */
  next_login_url?: string
  callback_prefix?: string
  /** Enterprise / BuilderId / Internal */
  provider?: string
}

/**
 * 把网页登录推进一步。
 *
 * 企业账号要调两次，第二次带**同一个** session_id。
 */
export async function completeWebLogin(payload: {
  session_id: string
  /** 浏览器地址栏里那条打不开的回调地址，或只有其中的 code */
  callback: string
}): Promise<KiroWebLoginResult> {
  const { data } = await apiClient.post<KiroWebLoginResult>('/admin/kiro/oauth/complete', payload)
  return data
}

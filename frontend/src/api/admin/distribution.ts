/**
 * Affiliate-admin distribution center API.
 * Scoped to the current distribution administrator; do not call super-admin dashboard/usage endpoints.
 */

import { apiClient } from '../client'
import type { PaginatedResponse, UsageLog } from '@/types'

export interface DistributionDashboardSnapshot {
  customer_count: number
  active_customer_count: number
  disabled_customer_count: number
  today_requests: number
  today_tokens: number
  today_cost: number
  available_balance: number
  frozen_balance: number
  total_transferred: number
  invite_count: number
  registration_count: number
}

export interface DistributionUsageQuery {
  start_date?: string
  end_date?: string
  granularity?: 'day' | 'hour'
  user_id?: number
  timezone?: string
}

export interface DistributionUsageTrendPoint {
  date: string
  requests: number
  tokens: number
  cost: number
}

export interface DistributionUsageTrendResponse {
  trend: DistributionUsageTrendPoint[]
  start_date: string
  end_date: string
  granularity: string
}

export interface DistributionUsageModel {
  model: string
  requests: number
  tokens: number
  cost: number
}

export interface DistributionUsageModelsResponse {
  models: DistributionUsageModel[]
}

export interface DistributionUsageError {
  status_code: number
  message?: string
  count: number
  last_seen_at?: string | null
}

export interface DistributionUsageErrorsResponse {
  errors: DistributionUsageError[]
}

export interface DistributionUserRankingItem {
  user_id: number
  email: string
  username: string
  requests: number
  tokens: number
  cost: number
}

export interface DistributionUserRankingResponse {
  items: DistributionUserRankingItem[]
}

export interface DistributionUserUsageSummary {
  user_id: number
  total_requests: number
  total_tokens: number
  total_cost: number
  today_requests: number
  today_tokens: number
  today_cost: number
}

export interface DistributionBalanceSummary {
  available_balance: number
  frozen_balance: number
  total_transferred: number
  customer_balance_total: number
}

export interface DistributionBalanceTransfer {
  id: number
  user_id: number
  email: string
  username: string
  amount: number
  notes: string
  created_at: string
}

export interface ListDistributionTransfersParams {
  page?: number
  page_size?: number
  user_id?: number
  start_at?: string
  end_at?: string
}

export interface CreateDistributionBalanceTransferRequest {
  amount: number
  notes?: string
  idempotency_key?: string
}

export interface DistributionGroup {
  id: number
  name: string
  status: 'active' | 'inactive'
}

export interface UpdateDistributionUserGroupsRequest {
  group_ids: number[]
}

export interface DistributionInviteProfile {
  invite_code: string
  invite_link: string
  aff_code?: string
  register_path?: string
  registration_count: number
  enabled: boolean
  default_group_ids?: number[]
}

export interface UpdateDistributionInviteSettingsRequest {
  enabled?: boolean
  default_group_ids?: number[]
}

export interface DistributionUserSubscription {
  plan_name: string
  status: string
  expires_at: string | null
}

export interface DistributionPermissions {
  can_publish_announcements: boolean
}

export interface DistributionInviteRegistration {
  id: number
  user_id: number
  email: string
  username: string
  created_at: string
}

export interface ListDistributionRegistrationsParams {
  page?: number
  page_size?: number
  search?: string
}

export async function getDashboardSnapshot(params?: {
  timezone?: string
}): Promise<DistributionDashboardSnapshot> {
  const { data } = await apiClient.get<DistributionDashboardSnapshot>(
    '/admin/distribution/dashboard/snapshot',
    { params }
  )
  return data
}

export async function getUsageTrend(
  params?: DistributionUsageQuery
): Promise<DistributionUsageTrendResponse> {
  const { data } = await apiClient.get<DistributionUsageTrendResponse>(
    '/admin/distribution/usage/trend',
    { params }
  )
  return data
}

export async function getUsageModels(
  params?: DistributionUsageQuery
): Promise<DistributionUsageModelsResponse> {
  const { data } = await apiClient.get<DistributionUsageModelsResponse>(
    '/admin/distribution/usage/models',
    { params }
  )
  return data
}

export async function getUsageErrors(
  params?: DistributionUsageQuery
): Promise<DistributionUsageErrorsResponse> {
  const { data } = await apiClient.get<DistributionUsageErrorsResponse>(
    '/admin/distribution/usage/errors',
    { params }
  )
  return data
}

export async function getUserRanking(
  params?: DistributionUsageQuery
): Promise<DistributionUserRankingResponse> {
  const { data } = await apiClient.get<DistributionUserRankingResponse>(
    '/admin/distribution/users/ranking',
    { params }
  )
  return data
}

export async function getUserUsageSummary(
  userId: number,
  params?: DistributionUsageQuery
): Promise<DistributionUserUsageSummary> {
  const { data } = await apiClient.get<DistributionUserUsageSummary>(
    `/admin/distribution/users/${userId}/usage/summary`,
    { params }
  )
  return data
}

export async function getUserUsageTrend(
  userId: number,
  params?: DistributionUsageQuery
): Promise<DistributionUsageTrendResponse> {
  const { data } = await apiClient.get<DistributionUsageTrendResponse>(
    `/admin/distribution/users/${userId}/usage/trend`,
    { params }
  )
  return data
}

export async function getUserUsageLogs(
  userId: number,
  params?: { page?: number; page_size?: number }
): Promise<PaginatedResponse<UsageLog>> {
  const { data } = await apiClient.get<PaginatedResponse<UsageLog>>(
    `/admin/distribution/users/${userId}/usage/logs`,
    { params }
  )
  return data
}

export async function getBalanceSummary(): Promise<DistributionBalanceSummary> {
  const { data } = await apiClient.get<DistributionBalanceSummary>(
    '/admin/distribution/balance/summary'
  )
  return data
}

export async function listBalanceTransfers(
  params?: ListDistributionTransfersParams
): Promise<PaginatedResponse<DistributionBalanceTransfer>> {
  const { data } = await apiClient.get<PaginatedResponse<DistributionBalanceTransfer>>(
    '/admin/distribution/balance/transfers',
    { params }
  )
  return data
}

export async function createUserBalanceTransfer(
  userId: number,
  payload: CreateDistributionBalanceTransferRequest
): Promise<DistributionBalanceTransfer> {
  const { data } = await apiClient.post<DistributionBalanceTransfer>(
    `/admin/distribution/users/${userId}/balance-transfers`,
    payload
  )
  return data
}

export async function listGroups(): Promise<DistributionGroup[]> {
  const { data } = await apiClient.get<DistributionGroup[]>('/admin/distribution/groups')
  return data
}

export async function updateUserGroups(
  userId: number,
  payload: UpdateDistributionUserGroupsRequest
): Promise<{ group_ids: number[] }> {
  const { data } = await apiClient.put<{ group_ids: number[] }>(
    `/admin/distribution/users/${userId}/groups`,
    payload
  )
  return data
}

export async function getInviteProfile(): Promise<DistributionInviteProfile> {
  const { data } = await apiClient.get<DistributionInviteProfile>(
    '/admin/distribution/invites/profile'
  )
  return data
}

export async function updateInviteSettings(
  payload: UpdateDistributionInviteSettingsRequest
): Promise<DistributionInviteProfile> {
  const { data } = await apiClient.put<DistributionInviteProfile>(
    '/admin/distribution/invites/settings',
    payload
  )
  return data
}

export async function rotateInviteCode(): Promise<DistributionInviteProfile> {
  const { data } = await apiClient.post<DistributionInviteProfile>(
    '/admin/distribution/invites/rotate-code'
  )
  return data
}

export async function listInviteRegistrations(
  params?: ListDistributionRegistrationsParams
): Promise<PaginatedResponse<DistributionInviteRegistration>> {
  const { data } = await apiClient.get<PaginatedResponse<DistributionInviteRegistration>>(
    '/admin/distribution/invites/registrations',
    { params }
  )
  return data
}

export async function listUserSubscriptions(
  userId: number,
  page: number = 1,
  pageSize: number = 20
): Promise<PaginatedResponse<DistributionUserSubscription>> {
  const { data } = await apiClient.get<PaginatedResponse<DistributionUserSubscription>>(
    `/admin/distribution/users/${userId}/subscriptions`,
    { params: { page, page_size: pageSize } }
  )
  return data
}

export async function getMyPermissions(): Promise<DistributionPermissions> {
  const { data } = await apiClient.get<DistributionPermissions>(
    '/admin/distribution/permissions'
  )
  return data
}

export const distributionAPI = {
  getDashboardSnapshot,
  getUsageTrend,
  getUsageModels,
  getUsageErrors,
  getUserRanking,
  getUserUsageSummary,
  getUserUsageTrend,
  getUserUsageLogs,
  getBalanceSummary,
  listBalanceTransfers,
  createUserBalanceTransfer,
  listGroups,
  updateUserGroups,
  getInviteProfile,
  updateInviteSettings,
  rotateInviteCode,
  listInviteRegistrations,
  listUserSubscriptions,
  getMyPermissions,
}

export default distributionAPI

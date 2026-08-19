import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (overrides: Partial<DashboardStats> = {}): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  total_account_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  today_account_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0,
  ...overrides
})

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
  })

  it('uses last 24 hours as default dashboard range', async () => {
    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))
  })

  it('shows provider cache read coverage, observability, and separate billing adjustments', async () => {
    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats({
        today_input_tokens: 100,
        today_cache_creation_tokens: 100,
        today_cache_read_tokens: 900,
        today_provider_cache_read_tokens: 300,
        today_forced_cache_read_tokens: 600,
        today_reported_requests: 1,
        today_estimated_requests: 1,
        today_unavailable_requests: 1,
        total_input_tokens: 400,
        total_cache_creation_tokens: 200,
        total_cache_read_tokens: 1000,
        total_provider_cache_read_tokens: 400,
        total_forced_cache_read_tokens: 600,
        total_reported_requests: 8,
        total_estimated_requests: 1,
        total_unavailable_requests: 1
      }),
      trend: [],
      models: []
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.dashboard.todayCacheReadCoverage')
    expect(wrapper.get('[data-testid="today-cache-coverage-value"]').text()).toBe(
      'admin.dashboard.cachePartiallyObservable'
    )
    expect(wrapper.text()).toContain('admin.dashboard.totalCacheReadCoverage')
    expect(wrapper.get('[data-testid="total-cache-coverage-value"]').text()).toBe(
      'admin.dashboard.cachePartiallyObservable'
    )
    expect(wrapper.get('[data-testid="today-cache-observability"]').text()).toContain(
      '1/2 (50.0%)'
    )
    expect(wrapper.get('[data-testid="today-cache-billing-adjustment"]').text()).toContain('600')
  })

  it('shows reported-subset coverage when mixed traffic includes reported token buckets', async () => {
    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats({
        today_requests: 3,
        today_input_tokens: 100,
        today_cache_creation_tokens: 100,
        today_cache_read_tokens: 900,
        today_provider_cache_read_tokens: 300,
        today_forced_cache_read_tokens: 600,
        today_reported_input_tokens: 100,
        today_reported_cache_creation_tokens: 0,
        today_reported_forced_cache_read_tokens: 300,
        today_reported_requests: 1,
        today_estimated_requests: 1,
        today_unavailable_requests: 1,
        total_requests: 10,
        total_input_tokens: 400,
        total_cache_creation_tokens: 200,
        total_cache_read_tokens: 1000,
        total_provider_cache_read_tokens: 400,
        total_forced_cache_read_tokens: 600,
        total_reported_input_tokens: 200,
        total_reported_cache_creation_tokens: 100,
        total_reported_forced_cache_read_tokens: 300,
        total_reported_requests: 8,
        total_estimated_requests: 1,
        total_unavailable_requests: 1
      }),
      trend: [],
      models: []
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.get('[data-testid="today-cache-coverage-value"]').text()).toBe('42.9%')
    expect(wrapper.get('[data-testid="today-cache-observability"]').text()).toContain(
      '1/2 (50.0%)'
    )
    expect(wrapper.get('[data-testid="total-cache-coverage-value"]').text()).toBe('40.0%')
    expect(wrapper.get('[data-testid="today-cache-billing-adjustment"]').text()).toContain('300')
  })

  it('falls back to legacy cache reads minus forced adjustments', async () => {
    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats({
        today_input_tokens: 100,
        today_cache_creation_tokens: 100,
        today_cache_read_tokens: 500,
        today_forced_cache_read_tokens: 200
      }),
      trend: [],
      models: []
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.get('[data-testid="today-cache-coverage-value"]').text()).toBe('42.9%')
  })

  it('shows cache as unobservable instead of warning zero coverage', async () => {
    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats({
        today_cache_read_tokens: 500,
        today_provider_cache_read_tokens: 0,
        today_forced_cache_read_tokens: 500,
        today_reported_requests: 0,
        today_estimated_requests: 2,
        today_unavailable_requests: 1
      }),
      trend: [],
      models: []
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const coverageValue = wrapper.get('[data-testid="today-cache-coverage-value"]')
    expect(coverageValue.text()).toBe('admin.dashboard.cacheUnobservable')
    expect(coverageValue.text()).not.toContain('0.0%')
    const observability = wrapper.get('[data-testid="today-cache-observability"]')
    expect(observability.text()).toContain('0/1')
    expect(observability.text()).not.toContain('0.0%')
  })

  it('loads cache statistics for a selected user without changing global dashboard stats', async () => {
    const CacheHitRateChartStub = {
      emits: ['user-change'],
      template:
        '<button data-testid="select-cache-user" @click="$emit(\'user-change\', { id: 42, email: \'alice@example.com\', deleted: false })">select</button>'
    }

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          CacheHitRateChart: CacheHitRateChartStub,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()
    getSnapshotV2.mockClear()

    await wrapper.get('[data-testid="select-cache-user"]').trigger('click')
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      user_id: 42,
      include_stats: false,
      include_trend: false,
      include_model_stats: true
    }))
  })
})

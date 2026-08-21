import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DashboardView from '../DashboardView.vue'

const {
  getDashboardStats,
  getDashboardTrend,
  getDashboardModels,
  getByDateRange,
  getMyPlatformQuotas
} = vi.hoisted(() => ({
  getDashboardStats: vi.fn(),
  getDashboardTrend: vi.fn(),
  getDashboardModels: vi.fn(),
  getByDateRange: vi.fn(),
  getMyPlatformQuotas: vi.fn()
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: false,
    user: { balance: 0 },
    refreshUser: vi.fn().mockResolvedValue(undefined)
  })
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats,
    getDashboardTrend,
    getDashboardModels,
    getByDateRange
  }
}))

vi.mock('@/api/user', () => ({
  getMyPlatformQuotas
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      searchUsers: vi.fn()
    }
  }
}))

vi.mock('vue-chartjs', () => ({
  Doughnut: {
    props: ['data', 'options'],
    template: '<div class="chart-data" />'
  }
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

describe('user DashboardView', () => {
  beforeEach(() => {
    getDashboardStats.mockReset()
    getDashboardTrend.mockReset()
    getDashboardModels.mockReset()
    getByDateRange.mockReset()
    getMyPlatformQuotas.mockReset()

    getDashboardStats.mockResolvedValue({
      total_api_keys: 0,
      active_api_keys: 0,
      total_requests: 0,
      total_input_tokens: 200,
      total_output_tokens: 0,
      total_cache_creation_tokens: 200,
      total_cache_read_tokens: 600,
      total_tokens: 1000,
      total_cost: 0,
      total_actual_cost: 0,
      today_requests: 0,
      today_input_tokens: 700,
      today_output_tokens: 0,
      today_cache_creation_tokens: 100,
      today_cache_read_tokens: 200,
      today_tokens: 1000,
      today_cost: 0,
      today_actual_cost: 0,
      average_duration_ms: 0,
      rpm: 0,
      tpm: 0
    })
    getDashboardTrend.mockResolvedValue({ trend: [] })
    getDashboardModels.mockResolvedValue({ models: [] })
    getByDateRange.mockResolvedValue({ items: [] })
    getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
  })

  it('shows the personal cache coverage chart without admin user search', async () => {
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          UserDashboardStats: true,
          UserDashboardCharts: true,
          UserDashboardRecentUsage: true,
          Doughnut: { template: '<div />' }
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('[data-testid="cache-hit-rate-chart"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="cache-user-search"]').exists()).toBe(false)
  })
})

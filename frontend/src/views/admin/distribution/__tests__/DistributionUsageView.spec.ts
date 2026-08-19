import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DistributionUsageView from '../DistributionUsageView.vue'

const { getUsageTrend, getUsageModels, getUsageErrors, getUserRanking } = vi.hoisted(() => ({
  getUsageTrend: vi.fn(),
  getUsageModels: vi.fn(),
  getUsageErrors: vi.fn(),
  getUserRanking: vi.fn()
}))

vi.mock('@/api/admin/distribution', () => ({
  getUsageTrend,
  getUsageModels,
  getUsageErrors,
  getUserRanking
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
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

describe('DistributionUsageView', () => {
  beforeEach(() => {
    getUsageTrend.mockReset()
    getUsageModels.mockReset()
    getUsageErrors.mockReset()
    getUserRanking.mockReset()
    getUsageTrend.mockResolvedValue({
      trend: [{ date: '2026-08-19', requests: 3, tokens: 30, cost: 1.1 }],
      start_date: '2026-08-13',
      end_date: '2026-08-19',
      granularity: 'day'
    })
    getUsageModels.mockResolvedValue({
      models: [{ model: 'claude-sonnet', requests: 3, tokens: 30, cost: 1.1 }]
    })
    getUsageErrors.mockResolvedValue({
      errors: [{ status_code: 0, message: 'failed_or_unbilled', count: 2 }]
    })
    getUserRanking.mockResolvedValue({
      items: [{ user_id: 9, email: 'rank@example.com', username: 'rank', requests: 3, tokens: 30, cost: 1.1 }]
    })
  })

  it('renders scoped usage tables from the distribution API', async () => {
    const wrapper = mount(DistributionUsageView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true
        }
      }
    })

    await flushPromises()

    expect(getUsageTrend).toHaveBeenCalled()
    expect(wrapper.get('[data-test="usage-trend"]').text()).toContain('2026-08-19')
    expect(wrapper.get('[data-test="usage-ranking"]').text()).toContain('rank@example.com')
    expect(wrapper.text()).toContain('claude-sonnet')
    expect(wrapper.text()).toContain('failed_or_unbilled')
  })
})

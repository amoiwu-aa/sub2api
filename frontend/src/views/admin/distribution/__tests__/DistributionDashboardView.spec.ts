import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DistributionDashboardView from '../DistributionDashboardView.vue'

const { getDashboardSnapshot, getUserRanking } = vi.hoisted(() => ({
  getDashboardSnapshot: vi.fn(),
  getUserRanking: vi.fn()
}))

vi.mock('@/api/admin/distribution', () => ({
  getDashboardSnapshot,
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

describe('DistributionDashboardView', () => {
  beforeEach(() => {
    getDashboardSnapshot.mockReset()
    getUserRanking.mockReset()
    getDashboardSnapshot.mockResolvedValue({
      customer_count: 12,
      active_customer_count: 8,
      disabled_customer_count: 1,
      today_requests: 40,
      today_tokens: 1000,
      today_cost: 3.5,
      available_balance: 88.2,
      frozen_balance: 0,
      total_transferred: 10,
      invite_count: 1,
      registration_count: 5
    })
    getUserRanking.mockResolvedValue({
      items: [{ user_id: 9, email: 'a@example.com', username: 'a', requests: 4, tokens: 20, cost: 1.2 }]
    })
  })

  it('renders snapshot metrics from the distribution API', async () => {
    const wrapper = mount(DistributionDashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true
        }
      }
    })

    await flushPromises()

    expect(getDashboardSnapshot).toHaveBeenCalled()
    expect(getUserRanking).toHaveBeenCalled()
    expect(wrapper.get('[data-test="snapshot-customers"]').text()).toContain('12')
    expect(wrapper.get('[data-test="snapshot-invites"]').text()).toContain('5')
    expect(wrapper.text()).toContain('a@example.com')
  })
})

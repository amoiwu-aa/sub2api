import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DistributionBalanceView from '../DistributionBalanceView.vue'

const { getBalanceSummary, listBalanceTransfers } = vi.hoisted(() => ({
  getBalanceSummary: vi.fn(),
  listBalanceTransfers: vi.fn()
}))

vi.mock('@/api/admin/distribution', () => ({
  getBalanceSummary,
  listBalanceTransfers
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

describe('DistributionBalanceView', () => {
  beforeEach(() => {
    getBalanceSummary.mockReset()
    listBalanceTransfers.mockReset()
    getBalanceSummary.mockResolvedValue({
      available_balance: 20,
      frozen_balance: 1,
      total_transferred: 7,
      customer_balance_total: 0
    })
    listBalanceTransfers.mockResolvedValue({
      items: [
        {
          id: 1,
          user_id: 9,
          email: 'user@example.com',
          username: 'user',
          amount: 3,
          notes: 'welcome',
          created_at: '2026-08-19T00:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
  })

  it('renders quota summary and transfer rows', async () => {
    const wrapper = mount(DistributionBalanceView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Pagination: true
        }
      }
    })

    await flushPromises()

    expect(getBalanceSummary).toHaveBeenCalled()
    expect(listBalanceTransfers).toHaveBeenCalledWith({ page: 1, page_size: 20 })
    expect(wrapper.get('[data-test="balance-available"]').text()).toContain('20')
    expect(wrapper.get('[data-test="balance-transfers"]').text()).toContain('user@example.com')
    expect(wrapper.get('[data-test="balance-transfers"]').text()).toContain('welcome')
  })
})

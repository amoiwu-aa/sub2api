import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DistributionInvitesView from '../DistributionInvitesView.vue'

const {
  getInviteProfile,
  listGroups,
  listInviteRegistrations,
  updateInviteSettings,
  rotateInviteCode
} = vi.hoisted(() => ({
  getInviteProfile: vi.fn(),
  listGroups: vi.fn(),
  listInviteRegistrations: vi.fn(),
  updateInviteSettings: vi.fn(),
  rotateInviteCode: vi.fn()
}))

vi.mock('@/api/admin/distribution', () => ({
  getInviteProfile,
  listGroups,
  listInviteRegistrations,
  updateInviteSettings,
  rotateInviteCode
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn()
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

describe('DistributionInvitesView', () => {
  beforeEach(() => {
    getInviteProfile.mockReset()
    listGroups.mockReset()
    listInviteRegistrations.mockReset()
    getInviteProfile.mockResolvedValue({
      invite_code: 'AFF123',
      invite_link: '/register?aff=AFF123',
      registration_count: 4,
      enabled: true,
      default_group_ids: [2]
    })
    listGroups.mockResolvedValue([
      { id: 2, name: 'Standard', status: 'active' }
    ])
    listInviteRegistrations.mockResolvedValue({
      items: [
        {
          id: 11,
          user_id: 11,
          email: 'new@example.com',
          username: 'new',
          created_at: '2026-08-19T00:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
  })

  it('renders invite profile and registrations', async () => {
    const wrapper = mount(DistributionInvitesView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Pagination: true,
          ConfirmDialog: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.get('[data-test="invite-code"]').text()).toContain('AFF123')
    expect(wrapper.get('[data-test="invite-count"]').text()).toContain('4')
    expect(wrapper.get('[data-test="invite-registrations"]').text()).toContain('new@example.com')
    expect(wrapper.text()).toContain('Standard')
  })
})

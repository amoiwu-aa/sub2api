import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppLayout from '@/components/layout/AppLayout.vue'
import { markNewUserOnboardingPending } from '@/utils/onboarding'

const {
  authState,
  replayTourMock,
  markAsSeenMock,
  setReplayCallbackMock
} = vi.hoisted(() => ({
  authState: {
    user: {
      id: 77,
      email: 'new-user@example.com',
      username: 'new-user',
      role: 'user' as const,
      balance: 0,
      concurrency: 5,
      status: 'active' as const,
      allowed_groups: null,
      balance_notify_enabled: true,
      balance_notify_threshold: null,
      balance_notify_extra_emails: [],
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString()
    },
    isSimpleMode: false
  },
  replayTourMock: vi.fn(),
  markAsSeenMock: vi.fn(),
  setReplayCallbackMock: vi.fn()
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ sidebarCollapsed: false })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({
    replayTour: replayTourMock,
    markAsSeen: markAsSeenMock
  })
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    setReplayCallback: setReplayCallbackMock
  })
}))

const OnboardingDialogStub = {
  props: ['show'],
  emits: ['start', 'skip'],
  template: `
    <div v-if="show">
      <button data-test="start" @click="$emit('start')">start</button>
      <button data-test="skip" @click="$emit('skip')">skip</button>
    </div>
  `
}

async function mountLayout() {
  const wrapper = mount(AppLayout, {
    global: {
      stubs: {
        AppSidebar: true,
        AppHeader: true,
        NewUserOnboardingDialog: OnboardingDialogStub
      }
    }
  })
  await nextTick()
  return wrapper
}

describe('AppLayout new user onboarding choice', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    replayTourMock.mockReset()
    markAsSeenMock.mockReset()
    setReplayCallbackMock.mockReset()
    authState.user.created_at = new Date().toISOString()
    markNewUserOnboardingPending(authState.user.id)
  })

  it('starts the user guide after the new user opts in', async () => {
    const wrapper = await mountLayout()

    await wrapper.get('[data-test="start"]').trigger('click')

    expect(replayTourMock).toHaveBeenCalledTimes(1)
    expect(markAsSeenMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="start"]').exists()).toBe(false)
  })

  it('records a skip and keeps the guide available for manual replay', async () => {
    const wrapper = await mountLayout()

    await wrapper.get('[data-test="skip"]').trigger('click')

    expect(markAsSeenMock).toHaveBeenCalledTimes(1)
    expect(replayTourMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="skip"]').exists()).toBe(false)
    expect(setReplayCallbackMock).toHaveBeenCalledWith(replayTourMock)
  })
})

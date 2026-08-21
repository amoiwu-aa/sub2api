import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import NewUserOnboardingDialog from '@/components/Guide/NewUserOnboardingDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const BaseDialogStub = {
  props: ['show', 'title'],
  template: `
    <section v-if="show">
      <h1>{{ title }}</h1>
      <slot />
      <slot name="footer" />
    </section>
  `
}

describe('NewUserOnboardingDialog', () => {
  it('offers explicit start and skip actions', async () => {
    const wrapper = mount(NewUserOnboardingDialog, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub
        }
      }
    })

    expect(wrapper.text()).toContain('onboarding.prompt.title')
    expect(wrapper.text()).toContain('onboarding.prompt.items.key.title')
    expect(wrapper.text()).toContain('onboarding.prompt.items.client.title')
    expect(wrapper.text()).toContain('onboarding.prompt.items.usage.title')

    await wrapper.get('[data-test="onboarding-start"]').trigger('click')
    await wrapper.get('[data-test="onboarding-skip"]').trigger('click')

    expect(wrapper.emitted('start')).toHaveLength(1)
    expect(wrapper.emitted('skip')).toHaveLength(1)
  })

  it('renders nothing while hidden', () => {
    const wrapper = mount(NewUserOnboardingDialog, {
      props: { show: false },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub
        }
      }
    })

    expect(wrapper.text()).toBe('')
  })
})


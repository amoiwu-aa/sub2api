import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import CustomerServiceDialog from '../CustomerServiceDialog.vue'

const copyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'common.customerService.title': '客服售后',
      'common.customerService.description': '扫码加入微信群，或复制微信号联系售后客服。',
      'common.customerService.groupQrAlt': '微信群二维码',
      'common.customerService.wechatId': '微信号',
      'common.customerService.otherContact': '其他联系方式',
      'common.customerService.copyWechatId': '复制微信号',
      'common.customerService.wechatIdCopied': '微信号已复制'
    })[key] || key
  })
}))

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: `
    <section v-if="show">
      <h2>{{ title }}</h2>
      <slot />
      <button data-test="close" @click="$emit('close')">close</button>
    </section>
  `
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(CustomerServiceDialog, {
    props: {
      show: true,
      ...props
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Icon: true
      }
    }
  })
}

describe('CustomerServiceDialog', () => {
  it('renders configured support details and copies the WeChat ID', async () => {
    copyToClipboard.mockReset()
    const wrapper = mountDialog({
      title: 'RingStar 售后',
      description: '工作时间 09:00-22:00',
      wechatId: ' RingStarSupport ',
      qrImage: 'data:image/png;base64,ZmFrZQ==',
      contactInfo: 'support@example.com'
    })

    expect(wrapper.get('h2').text()).toBe('RingStar 售后')
    expect(wrapper.text()).toContain('工作时间 09:00-22:00')
    expect(wrapper.text()).toContain('RingStarSupport')
    expect(wrapper.text()).toContain('support@example.com')
    expect(wrapper.get('img').attributes('src')).toBe('data:image/png;base64,ZmFrZQ==')

    await wrapper.get('button[aria-label="复制微信号"]').trigger('click')

    expect(copyToClipboard).toHaveBeenCalledWith('RingStarSupport', '微信号已复制')
  })

  it('uses localized fallbacks and rejects unsafe QR image URLs', () => {
    const wrapper = mountDialog({
      qrImage: 'javascript:alert(1)'
    })

    expect(wrapper.get('h2').text()).toBe('客服售后')
    expect(wrapper.text()).toContain('扫码加入微信群')
    expect(wrapper.find('img').exists()).toBe(false)
  })

  it('forwards the close action', async () => {
    const wrapper = mountDialog()

    await wrapper.get('[data-test="close"]').trigger('click')

    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})

import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  previewChatGPTCookie,
  reimportChatGPTCookie,
  getById,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  previewChatGPTCookie: vi.fn(),
  reimportChatGPTCookie: vi.fn(),
  getById: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
    showWarning: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      previewChatGPTCookie,
      reimportChatGPTCookie,
      getById
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    showChatgptCookieOption: Boolean,
    chatgptCookiePreview: Object,
    chatgptCookieAction: String,
    chatgptCookieMode: String
  },
  data: () => ({
    inputMethod: 'chatgpt_cookie',
    authCode: '',
    oauthState: '',
    projectId: '',
    sessionKey: ''
  }),
  emits: [
    'preview-chatgpt-cookie',
    'import-chatgpt-cookie',
    'clear-chatgpt-cookie-preview'
  ],
  methods: {
    reset() {}
  },
  template: `
    <div>
      <button data-testid="preview-cookie" @click="$emit('preview-chatgpt-cookie', { content: 'cookie-secret', userAgent: 'Test Browser' })">preview</button>
      <button data-testid="reimport-cookie" @click="$emit('import-chatgpt-cookie', { content: 'cookie-secret', userAgent: 'Test Browser' })">reimport</button>
    </div>
  `
})

describe('ReAuthAccountModal ChatGPT cookie flow', () => {
  const account = {
    id: 42,
    name: 'Cookie account',
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    proxy_id: 9,
    credentials: {},
    extra: {}
  }

  beforeEach(() => {
    previewChatGPTCookie.mockReset().mockResolvedValue({
      input_format: 'Header String',
      cookie_count: 1,
      endpoint_host: 'chatgpt.com',
      email: 'user@example.com',
      expires_at: '2026-08-03T20:00:00Z'
    })
    reimportChatGPTCookie.mockReset().mockResolvedValue({
      total: 1,
      created: 0,
      updated: 1,
      skipped: 0,
      failed: 0,
      items: []
    })
    getById.mockReset().mockResolvedValue(account)
    showSuccess.mockReset()
    showError.mockReset()
  })

  it('previews safely and updates only the selected account', async () => {
    const wrapper = mount(ReAuthAccountModal, {
      props: {
        show: true,
        account
      } as any,
      global: {
        stubs: {
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>'
          },
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          Icon: true
        }
      }
    })

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showChatgptCookieOption')).toBe(true)
    expect(flow.props('chatgptCookieMode')).toBe('reauthorize')

    await wrapper.get('[data-testid="preview-cookie"]').trigger('click')
    await flushPromises()
    expect(previewChatGPTCookie).toHaveBeenCalledWith({
      content: 'cookie-secret',
      user_agent: 'Test Browser',
      proxy_id: 9
    })
    expect(flow.props('chatgptCookiePreview')).toMatchObject({
      endpoint_host: 'chatgpt.com'
    })

    await wrapper.get('[data-testid="reimport-cookie"]').trigger('click')
    await flushPromises()
    expect(reimportChatGPTCookie).toHaveBeenCalledWith(42, {
      content: 'cookie-secret',
      user_agent: 'Test Browser'
    })
    expect(getById).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('reauthorized')?.[0]?.[0]).toMatchObject({ id: 42 })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})

import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

describe('OAuthAuthorizationFlow ChatGPT cookie input', () => {
  it('shows the OpenAI-only method and emits trimmed sensitive input on demand', async () => {
    const wrapper = mount(OAuthAuthorizationFlow, {
      props: {
        addMethod: 'oauth',
        platform: 'openai',
        showChatgptCookieOption: true,
        showManualOption: true
      },
      global: {
        plugins: [createPinia()],
        stubs: {
          Icon: { template: '<span />' }
        }
      }
    })

    await wrapper.get('input[value="chatgpt_cookie"]').setValue()
    const content = wrapper.get<HTMLTextAreaElement>('#chatgpt-cookie-content')
    expect(wrapper.get<HTMLInputElement>('#chatgpt-cookie-user-agent').element.value).toBe(
      navigator.userAgent
    )
    await content.setValue('  [{"name":"session","value":"secret"}]  ')
    await wrapper.get('#chatgpt-cookie-user-agent').setValue('  Test Browser  ')

    const previewButton = wrapper
      .findAll('button')
      .find((button) =>
        button.text().includes('admin.accounts.oauth.openai.chatgptCookiePreviewButton')
      )
    await previewButton?.trigger('click')
    expect(wrapper.emitted('preview-chatgpt-cookie')).toEqual([
      [
        {
          content: '[{"name":"session","value":"secret"}]',
          userAgent: 'Test Browser'
        }
      ]
    ])

    await wrapper.setProps({
      chatgptCookiePreview: {
        input_format: 'Cookie-Editor JSON',
        cookie_count: 3,
        endpoint_host: 'chatgpt.com',
        email: 'user@example.com',
        expires_at: '2026-08-03T20:00:00Z'
      }
    })
    expect(wrapper.get('[data-testid="chatgpt-cookie-preview"]').text()).toContain(
      'user@example.com'
    )

    const importButton = wrapper
      .findAll('button')
      .find((button) =>
        button.text().includes('admin.accounts.oauth.openai.chatgptCookieImportAndCreate')
      )
    expect(importButton).toBeDefined()
    await importButton?.trigger('click')

    expect(wrapper.emitted('import-chatgpt-cookie')).toEqual([
      [
        {
          content: '[{"name":"session","value":"secret"}]',
          userAgent: 'Test Browser'
        }
      ]
    ])
  })

  it('does not render the method unless explicitly enabled', () => {
    const wrapper = mount(OAuthAuthorizationFlow, {
      props: {
        addMethod: 'oauth',
        platform: 'openai',
        showManualOption: true
      },
      global: {
        plugins: [createPinia()],
        stubs: {
          Icon: { template: '<span />' }
        }
      }
    })

    expect(wrapper.find('input[value="chatgpt_cookie"]').exists()).toBe(false)
    expect(wrapper.find('#chatgpt-cookie-content').exists()).toBe(false)
  })
})

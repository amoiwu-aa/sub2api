import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get: vi.fn(),
    post,
    put: vi.fn(),
    delete: vi.fn()
  }
}))

import {
  importChatGPTCookie,
  previewChatGPTCookie,
  reimportChatGPTCookie
} from '@/api/admin/accounts'

describe('admin ChatGPT cookie import API', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('posts the browser session only to the dedicated conversion endpoint', async () => {
    const payload = {
      content: '[{"name":"__Secure-next-auth.session-token","value":"secret"}]',
      user_agent: 'Test Browser',
      proxy_id: 9,
      update_existing: true
    }
    const result = {
      total: 1,
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      items: []
    }
    post.mockResolvedValueOnce({ data: result })

    await expect(importChatGPTCookie(payload)).resolves.toEqual(result)
    expect(post).toHaveBeenCalledOnce()
    expect(post).toHaveBeenCalledWith(
      '/admin/accounts/import/chatgpt-cookie',
      payload,
      { timeout: 120000 }
    )
  })

  it('uses separate safe-preview and target-account re-import endpoints', async () => {
    const previewPayload = {
      content: 'Cookie: __Secure-next-auth.session-token=secret',
      user_agent: 'Test Browser',
      proxy_id: 9
    }
    const preview = {
      input_format: 'Header String',
      cookie_count: 1,
      endpoint_host: 'chatgpt.com',
      email: 'user@example.com',
      expires_at: '2026-08-03T20:00:00Z'
    }
    const reimportPayload = {
      content: previewPayload.content,
      user_agent: previewPayload.user_agent
    }
    const result = {
      total: 1,
      created: 0,
      updated: 1,
      skipped: 0,
      failed: 0,
      items: []
    }
    post.mockResolvedValueOnce({ data: preview }).mockResolvedValueOnce({ data: result })

    await expect(previewChatGPTCookie(previewPayload)).resolves.toEqual(preview)
    await expect(reimportChatGPTCookie(42, reimportPayload)).resolves.toEqual(result)

    expect(post).toHaveBeenNthCalledWith(
      1,
      '/admin/accounts/import/chatgpt-cookie/preview',
      previewPayload,
      { timeout: 120000 }
    )
    expect(post).toHaveBeenNthCalledWith(
      2,
      '/admin/accounts/42/reimport/chatgpt-cookie',
      reimportPayload,
      { timeout: 120000 }
    )
  })
})

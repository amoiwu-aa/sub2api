import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UserDashboardStats from '../UserDashboardStats.vue'

const messages: Record<string, string> = {
  'dashboard.todayCacheHitRate': '今日缓存命中率',
  'dashboard.totalCacheHitRate': '累计缓存命中率',
  'dashboard.cacheReadShort': '读取'
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key
    })
  }
})

const stats = {
  total_api_keys: 0,
  active_api_keys: 0,
  total_requests: 0,
  total_input_tokens: 200,
  total_output_tokens: 0,
  total_cache_creation_tokens: 200,
  total_cache_read_tokens: 600,
  total_tokens: 1000,
  total_cost: 0,
  total_actual_cost: 0,
  today_requests: 0,
  today_input_tokens: 700,
  today_output_tokens: 0,
  today_cache_creation_tokens: 100,
  today_cache_read_tokens: 200,
  today_tokens: 1000,
  today_cost: 0,
  today_actual_cost: 0,
  average_duration_ms: 0,
  rpm: 0,
  tpm: 0
}

describe('UserDashboardStats', () => {
  it('shows today and total cache hit rates from the user dashboard statistics', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 0,
        isSimple: false,
        platformQuotas: []
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('今日缓存命中率')
    expect(wrapper.text()).toContain('20.0%')
    expect(wrapper.text()).toContain('累计缓存命中率')
    expect(wrapper.text()).toContain('60.0%')
  })
})

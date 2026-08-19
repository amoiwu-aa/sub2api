import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'

import UserDashboardStats from '../UserDashboardStats.vue'
import zh from '../../../../i18n/locales/zh'

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
    const i18n = createI18n({
      legacy: false,
      locale: 'zh',
      messages: { zh }
    })
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 0,
        isSimple: false,
        platformQuotas: []
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('今日缓存命中率')
    expect(wrapper.text()).toContain('20.0%')
    expect(wrapper.text()).toContain('累计缓存命中率')
    expect(wrapper.text()).toContain('60.0%')
    expect(wrapper.text()).toContain('缓存读取: 200')
    expect(wrapper.text()).not.toContain('dashboard.')
  })
})

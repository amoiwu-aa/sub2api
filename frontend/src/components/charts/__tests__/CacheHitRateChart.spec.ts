import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import Select from '@/components/common/Select.vue'
import type { DashboardStats } from '@/types'
import CacheHitRateChart from '../CacheHitRateChart.vue'

const { searchUsers } = vi.hoisted(() => ({
  searchUsers: vi.fn()
}))

const messages: Record<string, string> = {
  'admin.dashboard.cacheHitChartTitle': '缓存命中率统计',
  'admin.dashboard.cacheReadCoverageChartTitle': '缓存读取覆盖率统计',
  'admin.dashboard.todayPeriod': '今日',
  'admin.dashboard.totalPeriod': '累计',
  'admin.dashboard.currentRange': '当前范围',
  'admin.dashboard.allModels': '全部模型',
  'admin.dashboard.modelFilter': '按模型筛选缓存统计',
  'admin.dashboard.cacheUserFilter': '按用户查看缓存统计',
  'admin.dashboard.cacheUserSearchPlaceholder': '搜索用户邮箱或 ID',
  'admin.dashboard.clearCacheUserFilter': '清除用户筛选',
  'admin.dashboard.cacheHitRate': '缓存命中率',
  'admin.dashboard.cacheReadCoverage': '缓存读取覆盖率',
  'admin.dashboard.cacheReadCoverageTooltip':
    '缓存读取 Token /（普通输入 + 缓存创建 + 上游缓存读取），不是请求命中率。',
  'admin.dashboard.cacheHitTokens': '缓存命中',
  'admin.dashboard.providerCacheReadTokens': '上游缓存读取',
  'admin.dashboard.cacheCreationTokens': '缓存创建',
  'admin.dashboard.uncachedInputTokens': '普通输入',
  'admin.dashboard.promptTokensTotal': '提示词 Token',
  'admin.dashboard.observableRequests': '可观测请求',
  'admin.dashboard.cachePartiallyObservable': '缓存部分可观测',
  'admin.dashboard.cacheUnobservable': '缓存不可观测',
  'admin.dashboard.billingAdjustmentTokens': '账务调整',
  'admin.dashboard.billingAdjustmentTooltip': '账务调整 Token 单独展示，不计入真实缓存读取覆盖率。'
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

vi.mock('vue-chartjs', () => ({
  Doughnut: {
    props: ['data', 'options'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>'
  }
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      searchUsers
    }
  }
}))

const stats: DashboardStats = {
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 200,
  total_output_tokens: 0,
  total_cache_creation_tokens: 200,
  total_cache_read_tokens: 600,
  total_tokens: 1000,
  total_cost: 0,
  total_actual_cost: 0,
  total_account_cost: 0,
  today_requests: 0,
  today_input_tokens: 700,
  today_output_tokens: 0,
  today_cache_creation_tokens: 100,
  today_cache_read_tokens: 200,
  today_tokens: 1000,
  today_cost: 0,
  today_actual_cost: 0,
  today_account_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
}

describe('CacheHitRateChart', () => {
  it('shows today cache composition and switches to total metrics', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: { stats }
    })

    expect(wrapper.text()).toContain('20.0%')
    let chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['上游缓存读取', '缓存创建', '普通输入'])
    expect(chartData.datasets[0].data).toEqual([200, 100, 700])

    await wrapper.get('[data-period="total"]').trigger('click')

    expect(wrapper.text()).toContain('60.0%')
    chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.datasets[0].data).toEqual([600, 200, 200])
  })

  it('filters cache composition by an individual model', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        modelStats: [
          {
            model: 'gpt-5.6',
            requests: 10,
            input_tokens: 100,
            output_tokens: 50,
            cache_creation_tokens: 100,
            cache_read_tokens: 800,
            total_tokens: 1050,
            cost: 0.1,
            actual_cost: 0.05
          },
          {
            model: 'gpt-5.4',
            requests: 5,
            input_tokens: 600,
            output_tokens: 50,
            cache_creation_tokens: 200,
            cache_read_tokens: 200,
            total_tokens: 1050,
            cost: 0.1,
            actual_cost: 0.05
          }
        ]
      }
    })

    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'gpt-5.6')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('80.0%')
    expect(wrapper.text()).toContain('当前范围')
    expect(wrapper.find('[data-period="today"]').exists()).toBe(false)

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.datasets[0].data).toEqual([800, 100, 100])
  })

  it('uses the selected user model statistics for the current dashboard range', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        selectedUser: {
          id: 12,
          email: 'alice@example.com',
          deleted: false
        },
        userModelStats: [
          {
            model: 'gpt-5.6',
            requests: 10,
            input_tokens: 100,
            output_tokens: 0,
            cache_creation_tokens: 100,
            cache_read_tokens: 300,
            total_tokens: 500,
            cost: 0,
            actual_cost: 0
          },
          {
            model: 'gpt-5.4',
            requests: 5,
            input_tokens: 300,
            output_tokens: 0,
            cache_creation_tokens: 100,
            cache_read_tokens: 200,
            total_tokens: 600,
            cost: 0,
            actual_cost: 0
          }
        ]
      }
    })

    expect(wrapper.get<HTMLInputElement>('[data-testid="cache-user-search"]').element.value).toBe(
      'alice@example.com'
    )
    expect(wrapper.text()).toContain('45.5%')
    expect(wrapper.text()).toContain('当前范围')
    expect(wrapper.find('[data-period="today"]').exists()).toBe(false)

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.datasets[0].data).toEqual([500, 200, 400])
  })

  it('uses provider cache reads and keeps forced billing adjustments outside coverage', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        modelStats: [
          {
            model: 'gpt-5.6',
            requests: 3,
            reported_requests: 1,
            estimated_requests: 1,
            unavailable_requests: 1,
            input_tokens: 100,
            output_tokens: 0,
            cache_creation_tokens: 100,
            cache_read_tokens: 900,
            provider_cache_read_tokens: 300,
            forced_cache_read_tokens: 600,
            total_tokens: 1100,
            cost: 0,
            actual_cost: 0
          }
        ]
      }
    })

    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'gpt-5.6')
    await wrapper.vm.$nextTick()

    expect(wrapper.get('[data-testid="cache-coverage-value"]').text()).toBe('缓存部分可观测')
    expect(wrapper.text()).toContain('缓存读取覆盖率')
    expect(wrapper.text()).not.toContain('缓存命中率')
    expect(wrapper.get('[data-testid="cache-observability"]').text()).toContain('1/3 (33.3%)')
    expect(wrapper.get('[data-testid="cache-billing-adjustment"]').text()).toContain('600')
    expect(wrapper.get('[data-testid="cache-billing-adjustment"]').attributes('title')).toContain(
      '不计入真实缓存读取覆盖率'
    )

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['上游缓存读取', '缓存创建', '普通输入'])
    expect(chartData.datasets[0].data).toEqual([300, 100, 700])
  })

  it('falls back to legacy cache reads minus forced adjustments', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        modelStats: [
          {
            model: 'legacy-model',
            requests: 1,
            input_tokens: 100,
            output_tokens: 0,
            cache_creation_tokens: 100,
            cache_read_tokens: 500,
            forced_cache_read_tokens: 200,
            total_tokens: 700,
            cost: 0,
            actual_cost: 0
          }
        ]
      }
    })

    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'legacy-model')
    await wrapper.vm.$nextTick()

    expect(wrapper.get('[data-testid="cache-coverage-value"]').text()).toBe('42.9%')
    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.datasets[0].data).toEqual([300, 100, 300])
  })

  it('shows cache as unobservable instead of a warning zero coverage', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        modelStats: [
          {
            model: 'opaque-model',
            requests: 2,
            reported_requests: 0,
            estimated_requests: 1,
            unavailable_requests: 1,
            input_tokens: 0,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 500,
            provider_cache_read_tokens: 0,
            forced_cache_read_tokens: 500,
            total_tokens: 500,
            cost: 0,
            actual_cost: 0
          }
        ]
      }
    })

    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'opaque-model')
    await wrapper.vm.$nextTick()

    const coverageValue = wrapper.get('[data-testid="cache-coverage-value"]')
    expect(coverageValue.text()).toBe('缓存不可观测')
    expect(coverageValue.text()).not.toContain('0.0%')
    expect(wrapper.get('[data-testid="cache-observability"]').text()).toContain('0/2')
    expect(wrapper.get('[data-testid="cache-observability"]').text()).not.toContain('0.0%')
  })

  it('includes legacy unknown requests in the observability denominator', async () => {
    const wrapper = mount(CacheHitRateChart, {
      props: {
        stats,
        modelStats: [
          {
            model: 'mixed-history',
            requests: 100,
            reported_requests: 1,
            estimated_requests: 0,
            unavailable_requests: 0,
            input_tokens: 100,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 50,
            provider_cache_read_tokens: 50,
            forced_cache_read_tokens: 0,
            total_tokens: 150,
            cost: 0,
            actual_cost: 0
          }
        ]
      }
    })

    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'mixed-history')
    await wrapper.vm.$nextTick()

    expect(wrapper.get('[data-testid="cache-coverage-value"]').text()).toBe('缓存部分可观测')
    expect(wrapper.get('[data-testid="cache-observability"]').text()).toContain('1/100 (1.0%)')
  })
})

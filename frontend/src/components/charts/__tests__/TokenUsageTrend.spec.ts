import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import TokenUsageTrend from '../TokenUsageTrend.vue'

const messages: Record<string, string> = {
  'admin.dashboard.tokenUsageTrend': 'Token Usage Trend',
  'admin.dashboard.noDataAvailable': 'No data available',
  'admin.dashboard.input': '输入',
  'admin.dashboard.output': '输出',
  'admin.dashboard.cacheCreateShort': '创建',
  'admin.dashboard.cacheReadShort': '读取',
  'admin.dashboard.cacheHitRate': '缓存命中率',
  'admin.dashboard.providerCacheReadShort': '上游缓存读取',
  'admin.dashboard.cacheReadCoverage': '缓存读取覆盖率',
  'admin.dashboard.observableRequests': '可观测请求',
  'admin.dashboard.cachePartiallyObservable': '缓存部分可观测',
  'admin.dashboard.cacheUnobservable': '缓存不可观测',
  'admin.dashboard.billingAdjustmentTokens': '账务调整',
  'admin.dashboard.actual': '实际',
  'admin.dashboard.standard': '标准',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-chartjs', () => ({
  Line: {
    props: ['data', 'options'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>',
  },
}))

describe('TokenUsageTrend', () => {
  it('calculates legacy cache read coverage against all prompt tokens', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 500,
            output_tokens: 100,
            cache_creation_tokens: 0,
            cache_read_tokens: 1500,
            cost: 0.01,
            actual_cost: 0.005,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )
    expect(chartData.datasets.map((ds: any) => ds.label)).toEqual([
      '输入',
      '输出',
      '创建',
      '上游缓存读取',
      '账务调整',
      '缓存读取覆盖率',
    ])
    // Coverage = 1500 / (500 + 1500 + 0) * 100 = 75%
    expect(hitRateDataset.data[0]).toBe(75)
  })

  it('returns 0 coverage for legacy data when all prompt tokens are zero', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 0,
            input_tokens: 0,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            cost: 0,
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )
    expect(hitRateDataset.data[0]).toBe(0)
  })

  it('includes cache_creation_tokens in denominator for Anthropic models', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 200,
            output_tokens: 50,
            cache_creation_tokens: 300,
            cache_read_tokens: 500,
            cost: 0.02,
            actual_cost: 0.01,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )
    // Coverage = 500 / (200 + 500 + 300) * 100 = 50%
    expect(hitRateDataset.data[0]).toBe(50)
  })

  it('uses provider cache reads and charts forced billing adjustments separately', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 4,
            reported_requests: 1,
            estimated_requests: 1,
            unavailable_requests: 2,
            input_tokens: 100,
            output_tokens: 50,
            cache_creation_tokens: 100,
            cache_read_tokens: 800,
            provider_cache_read_tokens: 200,
            forced_cache_read_tokens: 600,
            total_tokens: 1050,
            cost: 0.02,
            actual_cost: 0.01,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const providerReadDataset = chartData.datasets.find(
      (ds: any) => ds.label === '上游缓存读取'
    )
    const adjustmentDataset = chartData.datasets.find((ds: any) => ds.label === '账务调整')
    const coverageDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )

    expect(providerReadDataset.data).toEqual([200])
    expect(adjustmentDataset.data).toEqual([600])
    expect(coverageDataset.data).toEqual([null])
    expect(wrapper.get('[data-testid="cache-observability-summary"]').text()).toContain(
      '1/3 (33.3%)'
    )
  })

  it('plots reported-subset coverage when mixed traffic includes reported token buckets', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 4,
            reported_requests: 1,
            estimated_requests: 1,
            unavailable_requests: 2,
            input_tokens: 100,
            output_tokens: 50,
            cache_creation_tokens: 100,
            cache_read_tokens: 800,
            provider_cache_read_tokens: 200,
            forced_cache_read_tokens: 600,
            reported_input_tokens: 100,
            reported_cache_creation_tokens: 0,
            reported_forced_cache_read_tokens: 300,
            total_tokens: 1050,
            cost: 0.02,
            actual_cost: 0.01
          }
        ]
      },
      global: {
        stubs: {
          LoadingSpinner: true
        }
      }
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const coverageDataset = chartData.datasets.find(
      (ds: { label: string }) => ds.label === '缓存读取覆盖率'
    )
    // 200 / (100 + 300 + 0 + 200) = 33.3%
    expect(coverageDataset.data[0]).toBeCloseTo(33.333, 2)
    expect(wrapper.get('[data-testid="cache-observability-summary"]').text()).toContain(
      '1/3 (33.3%)'
    )
  })

  it('falls back to cache reads minus forced adjustments when provider reads are absent', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 100,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 500,
            forced_cache_read_tokens: 400,
            total_tokens: 600,
            cost: 0,
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const coverageDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )
    expect(coverageDataset.data[0]).toBeCloseTo(16.67, 2)
  })

  it('uses an unobservable state instead of plotting a warning zero coverage', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
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
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const coverageDataset = chartData.datasets.find(
      (ds: any) => ds.label === '缓存读取覆盖率'
    )

    expect(coverageDataset.data).toEqual([null])
    const observability = wrapper.get('[data-testid="cache-observability-summary"]')
    expect(observability.text()).toContain('缓存不可观测')
    expect(observability.text()).toContain('0/1')
    expect(observability.text()).not.toContain('0.0%')
  })
})

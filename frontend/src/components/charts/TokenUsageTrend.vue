<template>
  <div class="card p-4">
    <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.tokenUsageTrend') }}
      </h3>
      <div class="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
        <span
          v-if="observabilitySummary.available"
          data-testid="cache-observability-summary"
          class="text-xs text-gray-500 dark:text-dark-400"
          :title="t('admin.dashboard.cacheObservabilityTooltip')"
        >
          {{
            observabilitySummary.unobservable
              ? t('admin.dashboard.cacheUnobservable')
              : observabilitySummary.partiallyObservable
                ? t('admin.dashboard.cachePartiallyObservable')
                : t('admin.dashboard.observableRequests')
          }}
          {{ formatCacheObservability(observabilitySummary) }}
        </span>
        <span
          v-if="forcedAdjustmentTotal > 0"
          class="text-xs text-gray-500 dark:text-dark-400"
          :title="t('admin.dashboard.billingAdjustmentTooltip')"
        >
          {{ t('admin.dashboard.billingAdjustmentTokens') }}
          {{ formatTokens(forcedAdjustmentTotal) }}
        </span>
      </div>
    </div>
    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <div v-else-if="trendData.length > 0 && chartData" class="h-48">
      <Line :data="chartData" :options="lineOptions" />
    </div>
    <div v-else class="h-48">
      <ChartEmptyState
        icon="chart"
        :title="t('admin.dashboard.noDataAvailable')"
        :hint="t('admin.dashboard.noDataTrendHint')"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartEmptyState from '@/components/common/ChartEmptyState.vue'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { TrendDataPoint } from '@/types'
import {
  formatCacheObservability,
  getCacheCoverageMetrics,
  getCacheObservability
} from '@/utils/cacheCoverage'

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
)

const { t } = useI18n()

const props = defineProps<{
  trendData: TrendDataPoint[]
  loading?: boolean
}>()

const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

const chartColors = computed(() => ({
  text: isDarkMode.value ? '#94a3b8' : '#6e6e73',
  grid: isDarkMode.value ? 'rgba(148, 163, 184, 0.12)' : 'rgba(15, 23, 42, 0.05)',
  input: '#3b82f6',
  output: '#10b981',
  cacheCreation: '#f59e0b',
  cacheRead: '#06b6d4',
  cacheAdjustment: '#64748b',
  cacheCoverage: '#8b5cf6'
}))

const trendMetrics = computed(() => props.trendData.map((point) => getCacheCoverageMetrics(point)))
const forcedAdjustmentTotal = computed(() =>
  trendMetrics.value.reduce((total, metrics) => total + metrics.forcedAdjustment, 0)
)

const observabilitySummary = computed(() => {
  const hasObservationFields = props.trendData.some((point) =>
    [point.reported_requests, point.estimated_requests, point.unavailable_requests].some(
      (value) => value !== undefined && value !== null
    )
  )
  const totals = trendMetrics.value.reduce(
    (result, metrics) => {
      result.reported += metrics.observability.reported
      result.estimated += metrics.observability.estimated
      result.unavailable += metrics.observability.unavailable
      return result
    },
    { reported: 0, estimated: 0, unavailable: 0 }
  )

  return getCacheObservability({
    requests: props.trendData.reduce((total, point) => total + point.requests, 0),
    reported_requests: hasObservationFields ? totals.reported : undefined,
    estimated_requests: hasObservationFields ? totals.estimated : undefined,
    unavailable_requests: hasObservationFields ? totals.unavailable : undefined
  })
})

const chartData = computed(() => {
  if (!props.trendData?.length) return null

  return {
    labels: props.trendData.map((d) => d.date),
    datasets: [
      {
        label: t('admin.dashboard.input'),
        data: props.trendData.map((d) => d.input_tokens),
        borderColor: chartColors.value.input,
        backgroundColor: `${chartColors.value.input}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: t('admin.dashboard.output'),
        data: props.trendData.map((d) => d.output_tokens),
        borderColor: chartColors.value.output,
        backgroundColor: `${chartColors.value.output}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: t('admin.dashboard.cacheCreateShort'),
        data: props.trendData.map((d) => d.cache_creation_tokens),
        borderColor: chartColors.value.cacheCreation,
        backgroundColor: `${chartColors.value.cacheCreation}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: t('admin.dashboard.providerCacheReadShort'),
        data: trendMetrics.value.map((metrics) => metrics.providerRead),
        borderColor: chartColors.value.cacheRead,
        backgroundColor: `${chartColors.value.cacheRead}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: t('admin.dashboard.billingAdjustmentTokens'),
        data: trendMetrics.value.map((metrics) => metrics.forcedAdjustment),
        borderColor: chartColors.value.cacheAdjustment,
        backgroundColor: `${chartColors.value.cacheAdjustment}20`,
        borderDash: [3, 3],
        fill: false,
        tension: 0.3
      },
      {
        label: t('admin.dashboard.cacheReadCoverage'),
        data: trendMetrics.value.map((metrics) =>
          metrics.coverageAvailable ? metrics.coverage : null
        ),
        borderColor: chartColors.value.cacheCoverage,
        backgroundColor: `${chartColors.value.cacheCoverage}20`,
        borderDash: [5, 5],
        fill: false,
        tension: 0.3,
        yAxisID: 'yPercent'
      }
    ]
  }
})

const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      callbacks: {
        label: (context: any) => {
          if (context.dataset.yAxisID === 'yPercent') {
            if (context.raw === null || context.raw === undefined) {
              const observability = trendMetrics.value[context.dataIndex]?.observability
              return `${context.dataset.label}: ${
                observability?.partiallyObservable
                  ? t('admin.dashboard.cachePartiallyObservable')
                  : t('admin.dashboard.cacheUnobservable')
              }`
            }
            return `${context.dataset.label}: ${context.raw.toFixed(1)}%`
          }
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        },
        footer: (tooltipItems: any) => {
          const dataIndex = tooltipItems[0]?.dataIndex
          if (dataIndex !== undefined && props.trendData[dataIndex]) {
            const data = props.trendData[dataIndex]
            const metrics = trendMetrics.value[dataIndex]
            const lines = [
              `${t('admin.dashboard.actual')}: $${formatCost(data.actual_cost)} | ${t('admin.dashboard.standard')}: $${formatCost(data.cost)}`
            ]
            if (metrics.observability.available) {
              lines.push(
                `${
                  metrics.observability.partiallyObservable
                    ? t('admin.dashboard.cachePartiallyObservable')
                    : metrics.observability.unobservable
                      ? t('admin.dashboard.cacheUnobservable')
                      : t('admin.dashboard.observableRequests')
                }: ${formatCacheObservability(metrics.observability)}`
              )
            }
            if (metrics.forcedAdjustment > 0) {
              lines.push(
                `${t('admin.dashboard.billingAdjustmentTokens')}: ${formatTokens(metrics.forcedAdjustment)}`
              )
            }
            return lines
          }
          return ''
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    },
    yPercent: {
      position: 'right' as const,
      min: 0,
      max: 100,
      grid: {
        drawOnChartArea: false
      },
      ticks: {
        color: chartColors.value.cacheCoverage,
        font: {
          size: 10
        },
        callback: (value: string | number) => `${value}%`
      }
    }
  }
}))

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const formatCost = (value: number): string => {
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}
</script>

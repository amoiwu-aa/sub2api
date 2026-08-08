<template>
  <section class="card p-4" data-testid="cache-hit-rate-chart">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.cacheHitChartTitle') }}
      </h3>

      <div class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
        <button
          v-for="option in periodOptions"
          :key="option.value"
          type="button"
          :data-period="option.value"
          class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors"
          :class="
            period === option.value
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
          "
          @click="period = option.value"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div class="mt-4 grid items-center gap-5 md:grid-cols-[minmax(220px,320px)_minmax(0,1fr)]">
      <div class="relative mx-auto h-56 w-full max-w-80">
        <Doughnut v-if="metrics.total > 0" :data="chartData" :options="chartOptions" />
        <div
          v-else
          class="absolute inset-5 rounded-full border-[18px] border-gray-100 dark:border-dark-800"
        />

        <div class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <span class="text-3xl font-semibold tabular-nums text-gray-900 dark:text-white">
            {{ formatPercent(metrics.hitRate) }}
          </span>
          <span class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.dashboard.cacheHitRate') }}
          </span>
        </div>
      </div>

      <div class="divide-y divide-gray-100 dark:divide-dark-700">
        <div
          v-for="item in legendItems"
          :key="item.key"
          class="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-4 py-3"
        >
          <div class="flex min-w-0 items-center gap-2">
            <span
              class="h-2.5 w-2.5 flex-shrink-0 rounded-full"
              :style="{ backgroundColor: item.color }"
            />
            <span class="truncate text-sm text-gray-700 dark:text-dark-200">
              {{ item.label }}
            </span>
          </div>
          <span class="text-sm font-medium tabular-nums text-gray-900 dark:text-white">
            {{ formatTokens(item.value) }}
          </span>
          <span class="w-14 text-right text-xs tabular-nums text-gray-500 dark:text-dark-400">
            {{ formatPercent(item.ratio) }}
          </span>
        </div>

        <div class="flex items-center justify-between gap-4 pt-3">
          <span class="text-sm text-gray-500 dark:text-dark-400">
            {{ t('admin.dashboard.promptTokensTotal') }}
          </span>
          <span class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">
            {{ formatTokens(metrics.total) }}
          </span>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArcElement,
  Chart as ChartJS,
  Legend,
  Tooltip,
  type ChartOptions
} from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import type { DashboardStats } from '@/types'

ChartJS.register(ArcElement, Tooltip, Legend)

const props = defineProps<{
  stats: DashboardStats
}>()

const { t } = useI18n()
const period = ref<'today' | 'total'>('today')

const colors = {
  hit: '#06b6d4',
  creation: '#f59e0b',
  input: '#94a3b8'
} as const

const periodOptions = computed(() => [
  { value: 'today' as const, label: t('admin.dashboard.todayPeriod') },
  { value: 'total' as const, label: t('admin.dashboard.totalPeriod') }
])

const toTokenCount = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? Math.max(numberValue, 0) : 0
}

const metrics = computed(() => {
  const isToday = period.value === 'today'
  const input = toTokenCount(
    isToday ? props.stats.today_input_tokens : props.stats.total_input_tokens
  )
  const creation = toTokenCount(
    isToday
      ? props.stats.today_cache_creation_tokens
      : props.stats.total_cache_creation_tokens
  )
  const hit = toTokenCount(
    isToday ? props.stats.today_cache_read_tokens : props.stats.total_cache_read_tokens
  )
  const total = input + creation + hit

  return {
    input,
    creation,
    hit,
    total,
    hitRate: total > 0 ? (hit / total) * 100 : 0
  }
})

const chartData = computed(() => ({
  labels: [
    t('admin.dashboard.cacheHitTokens'),
    t('admin.dashboard.cacheCreationTokens'),
    t('admin.dashboard.uncachedInputTokens')
  ],
  datasets: [
    {
      data: [metrics.value.hit, metrics.value.creation, metrics.value.input],
      backgroundColor: [colors.hit, colors.creation, colors.input],
      borderWidth: 0,
      hoverOffset: 4
    }
  ]
}))

const chartOptions = computed<ChartOptions<'doughnut'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  cutout: '68%',
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      callbacks: {
        label: (context) => {
          const value = Number(context.raw) || 0
          const ratio = metrics.value.total > 0 ? (value / metrics.value.total) * 100 : 0
          return `${context.label}: ${formatTokens(value)} (${formatPercent(ratio)})`
        }
      }
    }
  }
}))

const legendItems = computed(() => [
  {
    key: 'hit',
    label: t('admin.dashboard.cacheHitTokens'),
    value: metrics.value.hit,
    ratio: metrics.value.hitRate,
    color: colors.hit
  },
  {
    key: 'creation',
    label: t('admin.dashboard.cacheCreationTokens'),
    value: metrics.value.creation,
    ratio: metrics.value.total > 0 ? (metrics.value.creation / metrics.value.total) * 100 : 0,
    color: colors.creation
  },
  {
    key: 'input',
    label: t('admin.dashboard.uncachedInputTokens'),
    value: metrics.value.input,
    ratio: metrics.value.total > 0 ? (metrics.value.input / metrics.value.total) * 100 : 0,
    color: colors.input
  }
])

const formatPercent = (value: number): string => `${value.toFixed(1)}%`

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`
  return value.toLocaleString()
}
</script>

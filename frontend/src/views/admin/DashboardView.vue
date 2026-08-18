<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- 今日概览：回答「现在服务怎么样」的五个生命体征组成一条 stat band。
             原来 10 张等权卡片没有主角——大数字直接排版在一张表面上，
             细分隔线负责分组，数字本身是唯一的视觉焦点。 -->
        <section class="card dash-hero" :aria-label="t('admin.dashboard.todayPeriod')">
          <div class="dash-hero__grid">
            <div class="dash-hero__metric">
              <span class="dash-hero__label">{{ t('admin.dashboard.todayRequests') }}</span>
              <span class="dash-hero__value">{{ formatNumber(stats.today_requests) }}</span>
              <span class="dash-hero__foot">{{ t('common.total') }} {{ formatNumber(stats.total_requests) }}</span>
            </div>

            <div class="dash-hero__metric">
              <span class="dash-hero__label">{{ t('admin.dashboard.todayTokens') }}</span>
              <span class="dash-hero__value">{{ formatTokens(stats.today_tokens) }}</span>
              <span class="dash-hero__foot">
                <CostTriplet
                  :actual="formatCost(stats.today_actual_cost)"
                  :account="formatCost(stats.today_account_cost)"
                  :standard="formatCost(stats.today_cost)"
                />
              </span>
            </div>

            <div class="dash-hero__metric">
              <span
                class="dash-hero__label"
                :title="t('admin.dashboard.cacheReadCoverageTooltip')"
              >
                {{ t('admin.dashboard.todayCacheReadCoverage') }}
              </span>
              <span
                data-testid="today-cache-coverage-value"
                class="dash-hero__value"
                :title="t('admin.dashboard.cacheReadCoverageTooltip')"
              >
                {{ formatCacheCoverage(todayCacheMetrics) }}
              </span>
              <span class="dash-hero__foot">
                {{ t('admin.dashboard.providerCacheReadShort') }}
                {{ formatTokens(todayCacheMetrics.providerRead) }}
                · {{ t('admin.dashboard.cacheCreateShort') }} {{ formatTokens(todayCacheMetrics.creation) }}
              </span>
              <span
                v-if="todayCacheMetrics.observability.available"
                data-testid="today-cache-observability"
                class="dash-hero__foot"
                :title="t('admin.dashboard.cacheObservabilityTooltip')"
              >
                {{
                  todayCacheMetrics.observability.unobservable
                    ? t('admin.dashboard.cacheUnobservable')
                    : todayCacheMetrics.observability.partiallyObservable
                      ? t('admin.dashboard.cachePartiallyObservable')
                      : t('admin.dashboard.observableRequests')
                }}
                {{ formatCacheObservability(todayCacheMetrics.observability) }}
              </span>
              <span
                v-if="todayCacheMetrics.forcedAdjustment > 0"
                data-testid="today-cache-billing-adjustment"
                class="dash-hero__foot"
                :title="t('admin.dashboard.billingAdjustmentTooltip')"
              >
                {{ t('admin.dashboard.billingAdjustmentTokens') }}
                {{ formatTokens(todayCacheMetrics.forcedAdjustment) }}
              </span>
            </div>

            <div class="dash-hero__metric">
              <span class="dash-hero__label">{{ t('admin.dashboard.performance') }}</span>
              <span class="dash-hero__value">{{ formatTokens(stats.rpm) }}<span class="dash-hero__unit">RPM</span></span>
              <span class="dash-hero__foot">{{ formatTokens(stats.tpm) }} TPM</span>
            </div>

            <div class="dash-hero__metric">
              <span class="dash-hero__label">{{ t('admin.dashboard.avgResponse') }}</span>
              <span class="dash-hero__value">{{ formatDuration(stats.average_duration_ms) }}</span>
              <span class="dash-hero__foot">{{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}</span>
            </div>
          </div>
        </section>

        <!-- 库存与累计：变化缓慢的参考数字，不需要卡片。一行文字扫过即可，
             唯一保留彩色的是异常账号数——它是这行里唯一需要立刻处理的信息。 -->
        <section class="dash-inventory" :aria-label="t('admin.dashboard.totalPeriod')">
          <div class="dash-inventory__group">
            <span class="dash-inventory__label">{{ t('admin.dashboard.apiKeys') }}</span>
            <span class="dash-inventory__value">{{ formatNumber(stats.total_api_keys) }}</span>
            <span class="dash-inventory__meta" :class="stats.active_api_keys > 0 ? 'text-emerald-600 dark:text-emerald-400' : ''">
              {{ stats.active_api_keys }} {{ t('common.active') }}
            </span>
          </div>

          <div class="dash-inventory__group">
            <span class="dash-inventory__label">{{ t('admin.dashboard.accounts') }}</span>
            <span class="dash-inventory__value">{{ formatNumber(stats.total_accounts) }}</span>
            <span class="dash-inventory__meta" :class="stats.normal_accounts > 0 ? 'text-emerald-600 dark:text-emerald-400' : ''">
              {{ stats.normal_accounts }} {{ t('common.active') }}
            </span>
            <span v-if="stats.error_accounts > 0" class="dash-inventory__meta font-medium text-red-600 dark:text-red-400">
              {{ stats.error_accounts }} {{ t('common.error') }}
            </span>
          </div>

          <div class="dash-inventory__group">
            <span class="dash-inventory__label">{{ t('admin.dashboard.users') }}</span>
            <span class="dash-inventory__value">{{ formatNumber(stats.total_users) }}</span>
            <span v-if="stats.today_new_users > 0" class="dash-inventory__meta">
              +{{ formatNumber(stats.today_new_users) }} {{ t('admin.dashboard.todayPeriod') }}
            </span>
          </div>

          <div class="dash-inventory__group">
            <span class="dash-inventory__label">{{ t('admin.dashboard.totalTokens') }}</span>
            <span class="dash-inventory__value">{{ formatTokens(stats.total_tokens) }}</span>
            <span class="dash-inventory__meta">
              <CostTriplet
                :actual="formatCost(stats.total_actual_cost)"
                :account="formatCost(stats.total_account_cost)"
                :standard="formatCost(stats.total_cost)"
              />
            </span>
          </div>

          <div class="dash-inventory__group">
            <span
              class="dash-inventory__label"
              :title="t('admin.dashboard.cacheReadCoverageTooltip')"
            >
              {{ t('admin.dashboard.totalCacheReadCoverage') }}
            </span>
            <span
              data-testid="total-cache-coverage-value"
              class="dash-inventory__value"
              :title="t('admin.dashboard.cacheReadCoverageTooltip')"
            >
              {{ formatCacheCoverage(totalCacheMetrics) }}
            </span>
            <span class="dash-inventory__meta">
              {{ t('admin.dashboard.providerCacheReadShort') }}
              {{ formatTokens(totalCacheMetrics.providerRead) }}
              · {{ t('admin.dashboard.cacheCreateShort') }} {{ formatTokens(totalCacheMetrics.creation) }}
            </span>
            <span
              v-if="totalCacheMetrics.observability.available"
              data-testid="total-cache-observability"
              class="dash-inventory__meta"
              :title="t('admin.dashboard.cacheObservabilityTooltip')"
            >
              {{
                totalCacheMetrics.observability.unobservable
                  ? t('admin.dashboard.cacheUnobservable')
                  : totalCacheMetrics.observability.partiallyObservable
                    ? t('admin.dashboard.cachePartiallyObservable')
                    : t('admin.dashboard.observableRequests')
              }}
              {{ formatCacheObservability(totalCacheMetrics.observability) }}
            </span>
            <span
              v-if="totalCacheMetrics.forcedAdjustment > 0"
              data-testid="total-cache-billing-adjustment"
              class="dash-inventory__meta"
              :title="t('admin.dashboard.billingAdjustmentTooltip')"
            >
              {{ t('admin.dashboard.billingAdjustmentTokens') }}
              {{ formatTokens(totalCacheMetrics.forcedAdjustment) }}
            </span>
          </div>
        </section>

        <CacheHitRateChart
          :stats="stats"
          :model-stats="modelStats"
          :selected-user="cacheHitRateUser"
          :user-model-stats="cacheHitRateUserModelStats"
          :user-loading="cacheHitRateUserLoading"
          @user-change="onCacheHitRateUserChange"
        />

        <!-- Charts Section：控制条不再包一层卡片，直接排在图表上方 -->
        <div class="space-y-6">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-3">
            <div class="flex items-center gap-2">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-400"
                >{{ t('admin.dashboard.timeRange') }}</span
              >
              <DateRangePicker
                v-model:start-date="startDate"
                v-model:end-date="endDate"
                @change="onDateRangeChange"
              />
            </div>
            <button @click="loadDashboardStats" :disabled="chartsLoading" class="btn btn-secondary btn-sm">
              {{ t('common.refresh') }}
            </button>
            <div class="ml-auto flex items-center gap-2">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-400"
                >{{ t('admin.dashboard.granularity') }}</span
              >
              <div class="w-28">
                <Select
                  v-model="granularity"
                  :options="granularityOptions"
                  @change="loadChartData"
                />
              </div>
            </div>
          </div>

          <!-- Charts Grid -->
          <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ModelDistributionChart
              :model-stats="modelStats"
              :enable-ranking-view="true"
              :ranking-items="rankingItems"
              :ranking-total-actual-cost="rankingTotalActualCost"
              :ranking-total-requests="rankingTotalRequests"
              :ranking-total-tokens="rankingTotalTokens"
              :loading="chartsLoading"
              :ranking-loading="rankingLoading"
              :ranking-error="rankingError"
              :start-date="startDate"
              :end-date="endDate"
              @ranking-click="goToUserUsage"
            />
            <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" />
          </div>

          <!-- User Usage Trend (Full Width) -->
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.recentUsage') }} (Top 12)
            </h3>
            <div class="h-64">
              <div v-if="userTrendLoading" class="flex h-full items-center justify-center">
                <LoadingSpinner size="md" />
              </div>
              <Line v-else-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
              <ChartEmptyState
                v-else
                icon="users"
                :title="t('admin.dashboard.noDataAvailable')"
                :hint="t('admin.dashboard.noDataTrendHint')"
              />
            </div>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type { SimpleUser } from '@/api/admin/usage'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  UserUsageTrendPoint,
  UserSpendingRankingItem
} from '@/types'
import {
  formatCacheObservability,
  getCacheCoverageMetrics,
  type CacheCoverageMetrics
} from '@/utils/cacheCoverage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ChartEmptyState from '@/components/common/ChartEmptyState.vue'
import CostTriplet from '@/components/dashboard/CostTriplet.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import CacheHitRateChart from '@/components/charts/CacheHitRateChart.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
)

const appStore = useAppStore()
const router = useRouter()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const cacheHitRateUser = ref<SimpleUser | null>(null)
const cacheHitRateUserModelStats = ref<ModelStat[]>([])
const cacheHitRateUserLoading = ref(false)
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
let cacheHitRateUserLoadSeq = 0
const rankingLimit = 12

const getDashboardCacheMetrics = (
  dashboardStats: DashboardStats | null,
  period: 'today' | 'total'
): CacheCoverageMetrics => {
  if (!dashboardStats) {
    return getCacheCoverageMetrics({})
  }

  const isToday = period === 'today'
  return getCacheCoverageMetrics({
    requests: isToday ? dashboardStats.today_requests : dashboardStats.total_requests,
    input_tokens: isToday
      ? dashboardStats.today_input_tokens
      : dashboardStats.total_input_tokens,
    cache_creation_tokens: isToday
      ? dashboardStats.today_cache_creation_tokens
      : dashboardStats.total_cache_creation_tokens,
    cache_read_tokens: isToday
      ? dashboardStats.today_cache_read_tokens
      : dashboardStats.total_cache_read_tokens,
    provider_cache_read_tokens: isToday
      ? dashboardStats.today_provider_cache_read_tokens
      : dashboardStats.total_provider_cache_read_tokens,
    forced_cache_read_tokens: isToday
      ? dashboardStats.today_forced_cache_read_tokens
      : dashboardStats.total_forced_cache_read_tokens,
    reported_input_tokens: isToday
      ? dashboardStats.today_reported_input_tokens
      : dashboardStats.total_reported_input_tokens,
    reported_cache_creation_tokens: isToday
      ? dashboardStats.today_reported_cache_creation_tokens
      : dashboardStats.total_reported_cache_creation_tokens,
    reported_forced_cache_read_tokens: isToday
      ? dashboardStats.today_reported_forced_cache_read_tokens
      : dashboardStats.total_reported_forced_cache_read_tokens,
    reported_requests: isToday
      ? dashboardStats.today_reported_requests
      : dashboardStats.total_reported_requests,
    estimated_requests: isToday
      ? dashboardStats.today_estimated_requests
      : dashboardStats.total_estimated_requests,
    unavailable_requests: isToday
      ? dashboardStats.today_unavailable_requests
      : dashboardStats.total_unavailable_requests
  })
}

const todayCacheMetrics = computed(() => getDashboardCacheMetrics(stats.value, 'today'))
const totalCacheMetrics = computed(() => getDashboardCacheMetrics(stats.value, 'total'))

// Helper function to format date in local timezone
const formatLocalDate = (date: Date): string => {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

const getLast24HoursRangeDates = (): { start: string; end: string } => {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end)
  }
}

// Date range
const granularity = ref<'day' | 'hour'>('hour')
const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// Granularity options for Select component
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') }
])

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors：网格线降到 5%/12% 透明度，只在需要读数时能被看见
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#94a3b8' : '#6e6e73',
  grid: isDarkMode.value ? 'rgba(148, 163, 184, 0.12)' : 'rgba(15, 23, 42, 0.05)'
}))

// Line chart options (for user trend chart)
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
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
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
    }
  }
}))

// User trend chart data
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by user_id to avoid merging different users with the same display name
  const userGroups = new Map<number, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#14b8a6',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7'
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3
  }))

  return {
    labels: sortedDates,
    datasets
  }
})

// Format helpers
const formatTokens = (value: number | undefined): string => {
  if (value === undefined || value === null) return '0'
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatNumber = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
}

const formatCacheCoverage = (metrics: CacheCoverageMetrics): string =>
  metrics.observability.unobservable
    ? t('admin.dashboard.cacheUnobservable')
    : metrics.coverageAvailable
      ? `${metrics.coverage.toFixed(1)}%`
      : t('admin.dashboard.cachePartiallyObservable')

const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value
    }
  })
}

// Date range change handler
const onDateRangeChange = (range: {
  startDate: string
  endDate: string
  preset: string | null
}) => {
  // Auto-select granularity based on date range
  const start = new Date(range.startDate)
  const end = new Date(range.endDate)
  const daysDiff = Math.ceil((end.getTime() - start.getTime()) / (1000 * 60 * 60 * 24))

  // If range is 1 day, use hourly granularity
  if (daysDiff <= 1) {
    granularity.value = 'hour'
  } else {
    granularity.value = 'day'
  }

  loadChartData()
}

// Load data
const loadDashboardSnapshot = async (includeStats: boolean) => {
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  chartsLoading.value = true
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

const loadCacheHitRateUserModelStats = async () => {
  const user = cacheHitRateUser.value
  const currentSeq = ++cacheHitRateUserLoadSeq

  if (!user) {
    cacheHitRateUserModelStats.value = []
    cacheHitRateUserLoading.value = false
    return
  }

  cacheHitRateUserModelStats.value = []
  cacheHitRateUserLoading.value = true
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      user_id: user.id,
      include_stats: false,
      include_trend: false,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== cacheHitRateUserLoadSeq) return
    cacheHitRateUserModelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== cacheHitRateUserLoadSeq) return
    cacheHitRateUserModelStats.value = []
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading selected user cache statistics:', error)
  } finally {
    if (currentSeq === cacheHitRateUserLoadSeq) {
      cacheHitRateUserLoading.value = false
    }
  }
}

const onCacheHitRateUserChange = (user: SimpleUser | null) => {
  cacheHitRateUser.value = user
  void loadCacheHitRateUserModelStats()
}

const loadUsersTrend = async () => {
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12
    })
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    userTrend.value = []
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

const loadUserSpendingRanking = async () => {
  const currentSeq = ++rankingLoadSeq
  rankingLoading.value = true
  rankingError.value = false
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit
    })
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    rankingItems.value = []
    rankingTotalActualCost.value = 0
    rankingTotalRequests.value = 0
    rankingTotalTokens.value = 0
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const loadDashboardStats = async () => {
  await Promise.all([
    loadDashboardSnapshot(true),
    loadCacheHitRateUserModelStats(),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardSnapshot(false),
    loadCacheHitRateUserModelStats(),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

onMounted(() => {
  loadDashboardStats()
})
</script>

<style scoped>
/* ===== 今日 stat band =====
 * 数字是唯一的主角：label 压低到 xs 中灰，footnote 再降半档。
 * 分隔只用 1px 发丝线，且只在 xl 单行排列时出现。 */
.dash-hero {
  padding: 1.25rem 0.5rem;
}

.dash-hero__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.dash-hero__metric {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.35rem 1.25rem;
}

@media (min-width: 1024px) {
  .dash-hero__grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (min-width: 1280px) {
  .dash-hero__grid {
    grid-template-columns: repeat(5, minmax(0, 1fr));
  }

  .dash-hero__metric + .dash-hero__metric {
    border-left: 1px solid var(--separator);
  }
}

.dash-hero__label {
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-muted);
}

.dash-hero__value {
  overflow: hidden;
  font-size: 1.7rem;
  font-weight: 600;
  letter-spacing: -0.02em;
  line-height: 1.2;
  color: var(--text-strong);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dash-hero__unit {
  margin-left: 0.3em;
  font-size: 0.8rem;
  font-weight: 500;
  letter-spacing: 0.02em;
  color: var(--text-muted);
}

.dash-hero__foot {
  overflow: hidden;
  font-size: 0.72rem;
  line-height: 1.3;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ===== 库存行 =====
 * 参考性数字不包卡片：一条安静的文字行，分隔靠间距和发丝线。 */
.dash-inventory {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: 1.75rem;
  row-gap: 0.5rem;
  padding: 0 0.25rem;
}

.dash-inventory__group {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 0.45rem;
}

.dash-inventory__label {
  font-size: 0.75rem;
  color: var(--text-faint);
}

.dash-inventory__value {
  font-size: 0.875rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--text-body);
  font-variant-numeric: tabular-nums;
}

.dash-inventory__meta {
  font-size: 0.72rem;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}
</style>

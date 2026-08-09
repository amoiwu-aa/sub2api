<template>
  <section class="card p-4" data-testid="cache-hit-rate-chart">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.cacheHitChartTitle') }}
      </h3>

      <div class="flex flex-wrap items-center justify-end gap-2">
        <div class="relative w-60">
          <input
            v-model="userSearchQuery"
            data-testid="cache-user-search"
            type="search"
            class="input h-9 w-full pr-8 text-sm"
            :placeholder="t('admin.dashboard.cacheUserSearchPlaceholder')"
            :aria-label="t('admin.dashboard.cacheUserFilter')"
            @focus="showUserResults = userSearchQuery.trim() !== selectedUser?.email"
          />
          <button
            v-if="selectedUser"
            type="button"
            class="absolute inset-y-0 right-0 flex w-8 items-center justify-center text-gray-400 hover:text-gray-700 dark:hover:text-white"
            :aria-label="t('admin.dashboard.clearCacheUserFilter')"
            @click="clearSelectedUser"
          >
            ×
          </button>

          <div
            v-if="showUserResults && (userSearchLoading || userSearchResults.length > 0 || userSearchQuery.trim())"
            class="absolute right-0 z-20 mt-1 max-h-64 w-full overflow-y-auto rounded-lg border border-gray-200 bg-white py-1 shadow-lg dark:border-dark-700 dark:bg-dark-800"
          >
            <div v-if="userSearchLoading" class="px-3 py-2 text-xs text-gray-500 dark:text-dark-400">
              {{ t('common.loading') }}
            </div>
            <template v-else-if="userSearchResults.length">
              <button
                v-for="user in userSearchResults"
                :key="user.id"
                type="button"
                class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-gray-50 dark:hover:bg-dark-700"
                @click="selectUser(user)"
              >
                <span class="truncate text-gray-800 dark:text-dark-100">{{ user.email }}</span>
                <span class="flex-shrink-0 text-xs text-gray-400">#{{ user.id }}</span>
              </button>
            </template>
            <div v-else class="px-3 py-2 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.dashboard.noDataAvailable') }}
            </div>
          </div>
        </div>

        <Select
          v-model="selectedModel"
          class="w-56"
          :options="modelOptions"
          searchable
          :aria-label="t('admin.dashboard.modelFilter')"
        />

        <div v-if="!showCurrentRange" class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
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

        <span
          v-else
          class="rounded-md bg-gray-100 px-3 py-2 text-xs font-medium text-gray-600 dark:bg-dark-800 dark:text-dark-300"
        >
          {{ userLoading ? t('common.loading') : t('admin.dashboard.currentRange') }}
        </span>
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
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArcElement,
  Chart as ChartJS,
  Legend,
  Tooltip,
  type ChartOptions
} from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import { adminAPI } from '@/api/admin'
import Select from '@/components/common/Select.vue'
import type { SimpleUser } from '@/api/admin/usage'
import type { DashboardStats, ModelStat } from '@/types'

ChartJS.register(ArcElement, Tooltip, Legend)

const props = defineProps<{
  stats: DashboardStats
  modelStats?: ModelStat[]
  selectedUser?: SimpleUser | null
  userModelStats?: ModelStat[]
  userLoading?: boolean
}>()

const emit = defineEmits<{
  'user-change': [user: SimpleUser | null]
}>()

const { t } = useI18n()
const period = ref<'today' | 'total'>('today')
const selectedModel = ref('')
const userSearchQuery = ref('')
const userSearchResults = ref<SimpleUser[]>([])
const userSearchLoading = ref(false)
const showUserResults = ref(false)
let userSearchTimer: ReturnType<typeof setTimeout> | undefined
let userSearchSequence = 0

const colors = {
  hit: '#06b6d4',
  creation: '#f59e0b',
  input: '#94a3b8'
} as const

const periodOptions = computed(() => [
  { value: 'today' as const, label: t('admin.dashboard.todayPeriod') },
  { value: 'total' as const, label: t('admin.dashboard.totalPeriod') }
])

const scopedModelStats = computed(() =>
  props.selectedUser ? props.userModelStats || [] : props.modelStats || []
)

const modelOptions = computed(() => [
  { value: '', label: t('admin.dashboard.allModels') },
  ...[...scopedModelStats.value]
    .sort((a, b) => a.model.localeCompare(b.model))
    .map((item) => ({ value: item.model, label: item.model }))
])

const selectedModelStats = computed(
  () => scopedModelStats.value.find((item) => item.model === selectedModel.value) || null
)

watch(
  scopedModelStats,
  () => {
    if (selectedModel.value && !selectedModelStats.value) {
      selectedModel.value = ''
    }
  },
  { deep: true }
)

watch(
  () => props.selectedUser,
  (user) => {
    userSearchQuery.value = user?.email || ''
    showUserResults.value = false
  },
  { immediate: true }
)

watch(userSearchQuery, (query) => {
  if (userSearchTimer) {
    clearTimeout(userSearchTimer)
  }

  const keyword = query.trim()
  if (!keyword || keyword === props.selectedUser?.email) {
    userSearchResults.value = []
    userSearchLoading.value = false
    return
  }

  userSearchTimer = setTimeout(async () => {
    const currentSequence = ++userSearchSequence
    userSearchLoading.value = true
    try {
      const users = await adminAPI.usage.searchUsers(keyword)
      if (currentSequence === userSearchSequence) {
        userSearchResults.value = users
      }
    } catch (error) {
      if (currentSequence === userSearchSequence) {
        userSearchResults.value = []
        console.error('Failed to search dashboard cache user:', error)
      }
    } finally {
      if (currentSequence === userSearchSequence) {
        userSearchLoading.value = false
      }
    }
  }, 250)
})

onBeforeUnmount(() => {
  if (userSearchTimer) {
    clearTimeout(userSearchTimer)
  }
})

const toTokenCount = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? Math.max(numberValue, 0) : 0
}

const metrics = computed(() => {
  if (selectedModelStats.value) {
    const input = toTokenCount(selectedModelStats.value.input_tokens)
    const creation = toTokenCount(selectedModelStats.value.cache_creation_tokens)
    const hit = toTokenCount(selectedModelStats.value.cache_read_tokens)
    const total = input + creation + hit

    return {
      input,
      creation,
      hit,
      total,
      hitRate: total > 0 ? (hit / total) * 100 : 0
    }
  }

  if (props.selectedUser) {
    const totals = scopedModelStats.value.reduce(
      (result, item) => ({
        input: result.input + toTokenCount(item.input_tokens),
        creation: result.creation + toTokenCount(item.cache_creation_tokens),
        hit: result.hit + toTokenCount(item.cache_read_tokens)
      }),
      { input: 0, creation: 0, hit: 0 }
    )
    const total = totals.input + totals.creation + totals.hit

    return {
      ...totals,
      total,
      hitRate: total > 0 ? (totals.hit / total) * 100 : 0
    }
  }

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

const showCurrentRange = computed(() => Boolean(selectedModel.value || props.selectedUser))

const selectUser = (user: SimpleUser) => {
  emit('user-change', user)
  userSearchResults.value = []
  showUserResults.value = false
}

const clearSelectedUser = () => {
  if (userSearchTimer) {
    clearTimeout(userSearchTimer)
  }
  userSearchSequence += 1
  userSearchQuery.value = ''
  userSearchResults.value = []
  showUserResults.value = false
  emit('user-change', null)
}

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

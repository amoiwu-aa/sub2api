<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.distribution.usage.description') }}
        </p>
        <div class="inline-flex rounded-lg border border-gray-200 p-1 dark:border-dark-600">
          <button
            v-for="option in rangeOptions"
            :key="option.value"
            type="button"
            class="rounded-md px-3 py-1.5 text-sm"
            :class="preset === option.value
              ? 'bg-primary-600 text-white'
              : 'text-gray-600 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-700'"
            :data-test="`range-${option.value}`"
            @click="changePreset(option.value)"
          >
            {{ option.label }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="flex justify-center py-16">
        <LoadingSpinner />
      </div>

      <section v-else class="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <article class="card p-6 lg:col-span-2">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.distribution.usage.trend') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.distribution.usage.trendHint') }}
          </p>
          <div class="mt-4 overflow-x-auto">
            <table class="min-w-full text-left text-sm" data-test="usage-trend">
              <thead>
                <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:text-gray-400">
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.date') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.requests') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.tokens') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.cost') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="trend.length === 0">
                  <td colspan="4" class="px-3 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="point in trend"
                  :key="point.date"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-3 py-2 text-gray-900 dark:text-white">{{ point.date }}</td>
                  <td class="px-3 py-2">{{ formatNumber(point.requests) }}</td>
                  <td class="px-3 py-2">{{ formatTokensK(point.tokens) }}</td>
                  <td class="px-3 py-2">{{ formatCurrency(point.cost) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.distribution.usage.models') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.distribution.usage.modelsHint') }}
          </p>
          <div class="mt-4 overflow-x-auto">
            <table class="min-w-full text-left text-sm">
              <thead>
                <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:text-gray-400">
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.model') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.requests') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.cost') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="models.length === 0">
                  <td colspan="3" class="px-3 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="item in models"
                  :key="item.model"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-3 py-2 text-gray-900 dark:text-white">{{ item.model }}</td>
                  <td class="px-3 py-2">{{ formatNumber(item.requests) }}</td>
                  <td class="px-3 py-2">{{ formatCurrency(item.cost) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.distribution.usage.errors') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.distribution.usage.errorsHint') }}
          </p>
          <div class="mt-4 overflow-x-auto">
            <table class="min-w-full text-left text-sm">
              <thead>
                <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:text-gray-400">
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.status') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.count') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="errors.length === 0">
                  <td colspan="2" class="px-3 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="(item, index) in errors"
                  :key="`${item.status_code}-${index}`"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-3 py-2 text-gray-900 dark:text-white">
                    {{ item.message || item.status_code }}
                  </td>
                  <td class="px-3 py-2">{{ formatNumber(item.count) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card p-6 lg:col-span-2">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.distribution.usage.ranking') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.distribution.usage.rankingHint') }}
          </p>
          <div class="mt-4 overflow-x-auto">
            <table class="min-w-full text-left text-sm" data-test="usage-ranking">
              <thead>
                <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:text-gray-400">
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.user') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.requests') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.tokens') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('admin.distribution.cost') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="ranking.length === 0">
                  <td colspan="4" class="px-3 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="item in ranking"
                  :key="item.user_id"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-3 py-2 text-gray-900 dark:text-white">
                    {{ item.email || item.username || item.user_id }}
                  </td>
                  <td class="px-3 py-2">{{ formatNumber(item.requests) }}</td>
                  <td class="px-3 py-2">{{ formatTokensK(item.tokens) }}</td>
                  <td class="px-3 py-2">{{ formatCurrency(item.cost) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </article>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import {
  getUsageErrors,
  getUsageModels,
  getUsageTrend,
  getUserRanking,
  type DistributionUsageError,
  type DistributionUsageModel,
  type DistributionUsageTrendPoint,
  type DistributionUserRankingItem
} from '@/api/admin/distribution'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  browserTimeZone,
  distributionDateRange,
  type DistributionRangePreset
} from '@/utils/distributionRange'
import { formatCurrency, formatNumber, formatTokensK } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const preset = ref<DistributionRangePreset>('7d')
const trend = ref<DistributionUsageTrendPoint[]>([])
const models = ref<DistributionUsageModel[]>([])
const errors = ref<DistributionUsageError[]>([])
const ranking = ref<DistributionUserRankingItem[]>([])

const rangeOptions = computed(() => [
  { value: '7d' as const, label: t('admin.distribution.last7Days') },
  { value: '30d' as const, label: t('admin.distribution.last30Days') }
])

const query = () => ({
  ...distributionDateRange(preset.value),
  timezone: browserTimeZone(),
  granularity: 'day' as const
})

const load = async () => {
  loading.value = true
  try {
    const params = query()
    const [trendRes, modelRes, errorRes, rankRes] = await Promise.all([
      getUsageTrend(params),
      getUsageModels(params),
      getUsageErrors(params),
      getUserRanking(params)
    ])
    trend.value = trendRes.trend || []
    models.value = modelRes.models || []
    errors.value = errorRes.errors || []
    ranking.value = rankRes.items || []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    loading.value = false
  }
}

const changePreset = async (next: DistributionRangePreset) => {
  if (preset.value === next) return
  preset.value = next
  await load()
}

onMounted(load)
</script>

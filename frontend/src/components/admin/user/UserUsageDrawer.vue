<template>
  <BaseDialog :show="show" :title="t('admin.distribution.userUsage.title')" width="wide" @close="$emit('close')">
    <div v-if="user" class="space-y-5">
      <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
        <p class="mt-1 text-sm text-gray-500">{{ user.username || '—' }}</p>
      </div>

      <div v-if="loading" class="flex justify-center py-10">
        <LoadingSpinner />
      </div>

      <template v-else>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
            <p class="text-xs text-gray-500">{{ t('admin.distribution.userUsage.today') }}</p>
            <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
              {{ formatCurrency(summary?.today_cost || 0) }}
            </p>
            <p class="mt-1 text-xs text-gray-400">
              {{ formatNumber(summary?.today_requests || 0) }} / {{ formatTokensK(summary?.today_tokens || 0) }}
            </p>
          </div>
          <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-600 sm:col-span-2">
            <p class="text-xs text-gray-500">{{ t('admin.distribution.userUsage.period') }}</p>
            <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
              {{ formatCurrency(summary?.total_cost || 0) }}
            </p>
            <p class="mt-1 text-xs text-gray-400">
              {{ formatNumber(summary?.total_requests || 0) }} / {{ formatTokensK(summary?.total_tokens || 0) }}
            </p>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table class="min-w-full text-left text-sm">
            <thead>
              <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600">
                <th class="px-3 py-2 font-medium">{{ t('admin.distribution.date') }}</th>
                <th class="px-3 py-2 font-medium">{{ t('admin.distribution.requests') }}</th>
                <th class="px-3 py-2 font-medium">{{ t('admin.distribution.tokens') }}</th>
                <th class="px-3 py-2 font-medium">{{ t('admin.distribution.cost') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="trend.length === 0">
                <td colspan="4" class="px-3 py-8 text-center text-gray-500">
                  {{ t('admin.distribution.userUsage.noLogs') }}
                </td>
              </tr>
              <tr
                v-for="point in trend"
                :key="point.date"
                class="border-b border-gray-100 last:border-0 dark:border-dark-700"
              >
                <td class="px-3 py-2">{{ point.date }}</td>
                <td class="px-3 py-2">{{ formatNumber(point.requests) }}</td>
                <td class="px-3 py-2">{{ formatTokensK(point.tokens) }}</td>
                <td class="px-3 py-2">{{ formatCurrency(point.cost) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  getUserUsageSummary,
  getUserUsageTrend,
  type DistributionUsageTrendPoint,
  type DistributionUserUsageSummary
} from '@/api/admin/distribution'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { browserTimeZone, distributionDateRange } from '@/utils/distributionRange'
import { formatCurrency, formatNumber, formatTokensK } from '@/utils/format'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const summary = ref<DistributionUserUsageSummary | null>(null)
const trend = ref<DistributionUsageTrendPoint[]>([])

const load = async () => {
  if (!props.user) return
  loading.value = true
  try {
    const params = { ...distributionDateRange('7d'), timezone: browserTimeZone() }
    const [nextSummary, nextTrend] = await Promise.all([
      getUserUsageSummary(props.user.id, params),
      getUserUsageTrend(props.user.id, params)
    ])
    summary.value = nextSummary
    trend.value = nextTrend.trend || []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.user?.id],
  ([visible]) => {
    if (visible) void load()
  }
)
</script>

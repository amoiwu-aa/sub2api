<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-16">
        <LoadingSpinner />
      </div>

      <template v-else>
        <section
          class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4"
          :aria-label="t('admin.distribution.dashboard.snapshotTitle')"
        >
          <article
            v-for="metric in snapshotMetrics"
            :key="metric.key"
            class="card p-5"
            :data-test="`snapshot-${metric.key}`"
          >
            <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
              {{ metric.label }}
            </p>
            <p class="mt-2 text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">
              {{ metric.value }}
            </p>
            <p v-if="metric.hint" class="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {{ metric.hint }}
            </p>
          </article>
        </section>

        <section class="card p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.distribution.dashboard.rankingTitle') }}
          </h2>
          <div class="mt-4 overflow-x-auto">
            <table class="min-w-full text-left text-sm">
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
                  <td colspan="4" class="px-3 py-10 text-center text-gray-500 dark:text-gray-400">
                    {{ t('common.noData') }}
                  </td>
                </tr>
                <tr
                  v-for="item in ranking"
                  :key="item.user_id"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-3 py-2 text-gray-900 dark:text-white">
                    {{ item.email || item.username || item.user_id }}
                  </td>
                  <td class="px-3 py-2 text-gray-700 dark:text-gray-300">{{ formatNumber(item.requests) }}</td>
                  <td class="px-3 py-2 text-gray-700 dark:text-gray-300">{{ formatTokensK(item.tokens) }}</td>
                  <td class="px-3 py-2 text-gray-700 dark:text-gray-300">{{ formatCurrency(item.cost) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import {
  getDashboardSnapshot,
  getUserRanking,
  type DistributionDashboardSnapshot,
  type DistributionUserRankingItem
} from '@/api/admin/distribution'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { browserTimeZone, distributionTodayRange } from '@/utils/distributionRange'
import { formatCurrency, formatNumber, formatTokensK } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const snapshot = ref<DistributionDashboardSnapshot | null>(null)
const ranking = ref<DistributionUserRankingItem[]>([])

const snapshotMetrics = computed(() => {
  const data = snapshot.value
  return [
    {
      key: 'customers',
      label: t('admin.distribution.dashboard.customers'),
      value: data ? formatNumber(data.customer_count) : t('admin.distribution.placeholder'),
      hint: data
        ? t('admin.distribution.dashboard.activeCustomers') + ': ' + formatNumber(data.active_customer_count)
        : ''
    },
    {
      key: 'usage',
      label: t('admin.distribution.dashboard.todayUsage'),
      value: data ? formatCurrency(data.today_cost) : t('admin.distribution.placeholder'),
      hint: data
        ? t('admin.distribution.dashboard.todayRequests') + ': ' + formatNumber(data.today_requests)
        : ''
    },
    {
      key: 'quota',
      label: t('admin.distribution.dashboard.quotaPool'),
      value: data ? formatCurrency(data.available_balance) : t('admin.distribution.placeholder'),
      hint: ''
    },
    {
      key: 'invites',
      label: t('admin.distribution.dashboard.invites'),
      value: data ? formatNumber(data.registration_count) : t('admin.distribution.placeholder'),
      hint: ''
    }
  ]
})

const load = async () => {
  loading.value = true
  try {
    const timezone = browserTimeZone()
    const range = {
      ...distributionTodayRange(),
      timezone
    }
    const [snap, rank] = await Promise.all([
      getDashboardSnapshot({ timezone }),
      getUserRanking(range)
    ])
    snapshot.value = snap
    ranking.value = rank.items || []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

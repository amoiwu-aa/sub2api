<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-16">
        <LoadingSpinner />
      </div>

      <template v-else>
        <section
          class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
          :aria-label="t('admin.distribution.balance.summary')"
        >
          <article
            v-for="metric in summaryMetrics"
            :key="metric.key"
            class="card p-5"
            :data-test="`balance-${metric.key}`"
          >
            <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
              {{ metric.label }}
            </p>
            <p class="mt-2 text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">
              {{ metric.value }}
            </p>
          </article>
        </section>

        <section class="card overflow-hidden p-0">
          <div class="border-b border-gray-200 px-6 py-4 dark:border-dark-600">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.distribution.balance.transfers') }}
            </h2>
          </div>
          <div class="overflow-x-auto">
            <table class="min-w-full text-left text-sm" data-test="balance-transfers">
              <thead>
                <tr class="border-b border-gray-200 bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-400">
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.user') }}</th>
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.amount') }}</th>
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.notes') }}</th>
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.time') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="transfers.length === 0">
                  <td colspan="4" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="row in transfers"
                  :key="row.id"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-4 py-3 text-gray-900 dark:text-white">
                    {{ row.email || row.username || row.user_id }}
                  </td>
                  <td class="px-4 py-3 font-medium text-emerald-600 dark:text-emerald-400">
                    {{ formatCurrency(row.amount) }}
                  </td>
                  <td class="px-4 py-3 text-gray-600 dark:text-gray-400">{{ row.notes || '—' }}</td>
                  <td class="px-4 py-3 text-gray-500">{{ formatDateTime(row.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <Pagination
            v-if="total > 0"
            :page="page"
            :total="total"
            :page-size="pageSize"
            @update:page="changePage"
          />
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
import Pagination from '@/components/common/Pagination.vue'
import {
  getBalanceSummary,
  listBalanceTransfers,
  type DistributionBalanceSummary,
  type DistributionBalanceTransfer
} from '@/api/admin/distribution'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatCurrency, formatDateTime } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const summary = ref<DistributionBalanceSummary | null>(null)
const transfers = ref<DistributionBalanceTransfer[]>([])
const page = ref(1)
const pageSize = 20
const total = ref(0)

const summaryMetrics = computed(() => {
  const data = summary.value
  return [
    {
      key: 'available',
      label: t('admin.distribution.balance.available'),
      value: data ? formatCurrency(data.available_balance) : t('admin.distribution.placeholder')
    },
    {
      key: 'frozen',
      label: t('admin.distribution.balance.frozen'),
      value: data ? formatCurrency(data.frozen_balance) : t('admin.distribution.placeholder')
    },
    {
      key: 'transferred',
      label: t('admin.distribution.balance.transferred'),
      value: data ? formatCurrency(data.total_transferred) : t('admin.distribution.placeholder')
    }
  ]
})

const load = async () => {
  loading.value = true
  try {
    const [sum, list] = await Promise.all([
      getBalanceSummary(),
      listBalanceTransfers({ page: page.value, page_size: pageSize })
    ])
    summary.value = sum
    transfers.value = list.items || []
    total.value = list.total || 0
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    loading.value = false
  }
}

const changePage = async (next: number) => {
  page.value = next
  await load()
}

onMounted(load)
</script>

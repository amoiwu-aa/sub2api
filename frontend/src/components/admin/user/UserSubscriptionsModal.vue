<template>
  <BaseDialog :show="show" :title="t('admin.distribution.userSubscriptions.title')" width="wide" @close="$emit('close')">
    <div v-if="user" class="space-y-4">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ user.email }}</p>
      <div v-if="loading" class="flex justify-center py-10">
        <LoadingSpinner />
      </div>
      <div v-else class="overflow-x-auto">
        <table class="min-w-full text-left text-sm">
          <thead>
            <tr class="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600">
              <th class="px-3 py-2 font-medium">{{ t('admin.distribution.userSubscriptions.plan') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('admin.distribution.status') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('admin.distribution.userSubscriptions.expires') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="items.length === 0">
              <td colspan="3" class="px-3 py-8 text-center text-gray-500">
                {{ t('admin.distribution.userSubscriptions.empty') }}
              </td>
            </tr>
            <tr
              v-for="(item, index) in items"
              :key="`${item.plan_name}-${index}`"
              class="border-b border-gray-100 last:border-0 dark:border-dark-700"
            >
              <td class="px-3 py-2 text-gray-900 dark:text-white">{{ item.plan_name || '—' }}</td>
              <td class="px-3 py-2">{{ item.status || '—' }}</td>
              <td class="px-3 py-2 text-gray-500">
                {{ item.expires_at ? formatDateTime(item.expires_at) : '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { listUserSubscriptions, type DistributionUserSubscription } from '@/api/admin/distribution'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const items = ref<DistributionUserSubscription[]>([])

const load = async () => {
  if (!props.user) return
  loading.value = true
  try {
    const result = await listUserSubscriptions(props.user.id, 1, 50)
    items.value = result.items || []
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

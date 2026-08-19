<template>
  <BaseDialog :show="show" :title="t('admin.distribution.transfer.title')" width="narrow" @close="$emit('close')">
    <form v-if="user" id="distribution-transfer-form" class="space-y-5" @submit.prevent="handleSubmit">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100">
          <span class="text-lg font-medium text-primary-700">{{ user.email.charAt(0).toUpperCase() }}</span>
        </div>
        <div class="flex-1">
          <p class="font-medium text-gray-900 dark:text-gray-100">{{ user.email }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.distribution.transfer.userBalance') }}: {{ formatCurrency(user.balance) }}
          </p>
        </div>
      </div>
      <p class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.distribution.transfer.hint') }}
      </p>
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.distribution.transfer.available') }}:
        <span class="font-medium">{{ formatCurrency(availableBalance) }}</span>
      </p>
      <div>
        <label class="input-label">{{ t('admin.distribution.transfer.amount') }}</label>
        <div class="relative">
          <div class="absolute left-3 top-1/2 -translate-y-1/2 font-medium text-gray-500">$</div>
          <input v-model.number="form.amount" type="number" step="any" min="0" required class="input pl-8" />
        </div>
      </div>
      <div>
        <label class="input-label">{{ t('admin.distribution.transfer.notes') }}</label>
        <textarea v-model="form.notes" rows="3" class="input"></textarea>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button
          class="btn btn-primary"
          form="distribution-transfer-form"
          type="submit"
          :disabled="submitting || !form.amount"
        >
          {{ submitting ? t('common.saving') : t('common.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { createUserBalanceTransfer, getBalanceSummary } from '@/api/admin/distribution'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { newIdempotencyKey } from '@/utils/distributionRange'
import { formatCurrency } from '@/utils/format'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close', 'success'])
const { t } = useI18n()
const appStore = useAppStore()

const submitting = ref(false)
const availableBalance = ref(0)
const idempotencyKey = ref('')
const form = reactive({ amount: 0, notes: '' })

const loadSummary = async () => {
  try {
    const summary = await getBalanceSummary()
    availableBalance.value = summary.available_balance || 0
  } catch {
    availableBalance.value = 0
  }
}

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    form.amount = 0
    form.notes = ''
    idempotencyKey.value = newIdempotencyKey()
    void loadSummary()
  }
)

const handleSubmit = async () => {
  if (!props.user) return
  if (!form.amount || form.amount <= 0) {
    appStore.showError(t('admin.users.amountRequired'))
    return
  }
  if (form.amount > availableBalance.value) {
    appStore.showError(t('admin.distribution.transfer.insufficient'))
    return
  }
  submitting.value = true
  try {
    await createUserBalanceTransfer(props.user.id, {
      amount: form.amount,
      notes: form.notes,
      idempotency_key: idempotencyKey.value || newIdempotencyKey()
    })
    appStore.showSuccess(t('admin.distribution.transfer.success'))
    emit('success')
    emit('close')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.createUser')"
    width="normal"
    @close="$emit('close')"
  >
    <form id="create-user-form" @submit.prevent="submit" class="space-y-5">
      <div>
        <label class="input-label">{{ t('admin.users.email') }}</label>
        <input v-model="form.email" type="email" required class="input" :placeholder="t('admin.users.enterEmail')" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.password') }}</label>
        <div class="flex gap-2">
          <div class="relative flex-1">
            <input v-model="form.password" type="text" required class="input pr-10" :placeholder="t('admin.users.enterPassword')" />
          </div>
          <button type="button" @click="generateRandomPassword" class="btn btn-secondary px-3">
            <Icon name="refresh" size="md" />
          </button>
        </div>
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.username') }}</label>
        <input v-model="form.username" type="text" class="input" :placeholder="t('admin.users.enterUsername')" />
      </div>
      <div v-if="!isAffiliateAdmin">
        <label class="input-label">{{ t('admin.users.form.roleLabel') }}</label>
        <select v-model="form.role" class="input">
          <option value="user">{{ t('admin.users.roles.user') }}</option>
          <option value="affiliate_admin">{{ t('admin.users.roles.affiliate_admin') }}</option>
          <option value="admin">{{ t('admin.users.roles.admin') }}</option>
        </select>
      </div>
      <div v-if="!isAffiliateAdmin" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label class="input-label">{{ t('admin.users.columns.balance') }}</label>
          <input v-model="form.balance" type="number" step="any" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.users.columns.concurrency') }}</label>
          <input v-model.number="form.concurrency" type="number" class="input" />
        </div>
      </div>
      <div v-if="exclusiveGroups.length > 0">
        <label class="input-label">{{ t('admin.users.exclusiveGroups') }}</label>
        <div class="mt-2 max-h-48 space-y-2 overflow-y-auto rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <label
            v-for="group in exclusiveGroups"
            :key="group.id"
            class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"
          >
            <input
              type="checkbox"
              class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="form.allowed_groups.includes(group.id)"
              @change="toggleAllowedGroup(group.id)"
            />
            <span>{{ group.name }}</span>
          </label>
        </div>
      </div>
      <div v-if="!isAffiliateAdmin">
        <label class="input-label">{{ t('admin.users.form.rpmLimit') }}</label>
        <input
          v-model.number="form.rpm_limit"
          type="number"
          min="0"
          step="1"
          class="input"
          :placeholder="t('admin.users.form.rpmLimitPlaceholder')"
        />
        <p class="input-hint">{{ t('admin.users.form.rpmLimitHint') }}</p>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button @click="$emit('close')" type="button" class="btn btn-secondary">{{ t('common.cancel') }}</button>
        <button type="submit" form="create-user-form" :disabled="loading" class="btn btn-primary">
          {{ loading ? t('admin.users.creating') : t('common.create') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <!-- 创建管理员账号时后端要求 step-up 2FA，弹出 TOTP 验证后自动重试 -->
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'; import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { AdminGroup } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits(['close', 'success']); const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAffiliateAdmin = computed(() => authStore.isAffiliateAdmin)

const form = reactive({ email: '', password: '', username: '', notes: '', role: 'user' as 'user' | 'admin' | 'affiliate_admin', balance: '', concurrency: 1, rpm_limit: 0, allowed_groups: [] as number[] })
const exclusiveGroups = ref<AdminGroup[]>([])

const toggleAllowedGroup = (groupId: number) => {
  const idx = form.allowed_groups.indexOf(groupId)
  if (idx >= 0) {
    form.allowed_groups.splice(idx, 1)
  } else {
    form.allowed_groups.push(groupId)
  }
}

const loadExclusiveGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    exclusiveGroups.value = groups.filter((g) => g.is_exclusive && g.subscription_type === 'standard' && g.status === 'active')
  } catch {
    exclusiveGroups.value = []
  }
}

const stepUp = useStepUp()
const loading = ref(false)

const submit = async () => {
  if (loading.value) return
  loading.value = true
  try {
    const { balance: rawBalance, ...rest } = { ...form }
    const payload: Record<string, unknown> = isAffiliateAdmin.value
      ? { email: form.email, password: form.password, username: form.username, notes: form.notes, role: 'user', allowed_groups: form.allowed_groups }
      : { ...rest, allowed_groups: form.allowed_groups }
    if (!isAffiliateAdmin.value) {
      const balance = String(rawBalance).trim()
      if (balance !== '') {
        payload.balance = Number(balance)
      }
    }
    // 创建管理员属敏感操作：后端返回 STEP_UP_REQUIRED 时弹 TOTP 验证并重试
    await stepUp.run(() => adminAPI.users.create(payload as Parameters<typeof adminAPI.users.create>[0]))
    appStore.showSuccess(t('admin.users.userCreated'))
    emit('success'); emit('close')
  } catch (e: any) {
    if (isStepUpCancelled(e)) {
      // 用户主动取消二次验证：静默返回，表单保持打开。
    } else if (isStepUpBlocked(e)) {
      appStore.showError(
        stepUpBlockReason(e) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled')
      )
    } else {
      appStore.showError(e?.message || t('admin.users.failedToCreate'))
    }
  } finally { loading.value = false }
}

watch(() => props.show, (v) => {
  if (v) {
    Object.assign(form, { email: '', password: '', username: '', notes: '', role: 'user', balance: '', concurrency: 1, rpm_limit: 0, allowed_groups: [] })
    void loadExclusiveGroups()
  }
})

const generateRandomPassword = () => {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%^&*'
  let p = ''; for (let i = 0; i < 16; i++) p += chars.charAt(Math.floor(Math.random() * chars.length))
  form.password = p
}
</script>

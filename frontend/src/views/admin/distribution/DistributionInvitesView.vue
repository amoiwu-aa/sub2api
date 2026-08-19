<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-16">
        <LoadingSpinner />
      </div>

      <template v-else>
        <section class="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <article class="card p-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.distribution.invites.profile') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">
              {{ t('admin.distribution.invites.profileHint') }}
            </p>
            <dl class="mt-6 space-y-4 text-sm">
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.distribution.invites.code') }}</dt>
                <dd class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center">
                  <code
                    class="min-w-0 break-all rounded-lg bg-gray-50 px-3 py-2 font-semibold text-gray-900 dark:bg-dark-800 dark:text-white"
                    data-test="invite-code"
                  >{{ profile?.invite_code || t('admin.distribution.placeholder') }}</code>
                  <button class="btn btn-secondary btn-sm" type="button" @click="copyCode">
                    <Icon name="copy" size="sm" />
                    <span>{{ t('admin.distribution.invites.copyCode') }}</span>
                  </button>
                </dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.distribution.invites.link') }}</dt>
                <dd class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center">
                  <code class="min-w-0 break-all rounded-lg bg-gray-50 px-3 py-2 text-gray-700 dark:bg-dark-800 dark:text-gray-300">
                    {{ inviteLink || t('admin.distribution.placeholder') }}
                  </code>
                  <button class="btn btn-secondary btn-sm" type="button" @click="copyLink">
                    <Icon name="link" size="sm" />
                    <span>{{ t('admin.distribution.invites.copyLink') }}</span>
                  </button>
                </dd>
              </div>
              <div class="flex items-center justify-between">
                <dt class="text-gray-500 dark:text-gray-400">
                  {{ t('admin.distribution.invites.registrationsCount') }}
                </dt>
                <dd class="font-medium text-gray-900 dark:text-white" data-test="invite-count">
                  {{ profile ? formatNumber(profile.registration_count) : t('admin.distribution.placeholder') }}
                </dd>
              </div>
            </dl>
          </article>

          <article class="card p-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.distribution.invites.settings') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">
              {{ t('admin.distribution.invites.settingsHint') }}
            </p>
            <div class="mt-6 space-y-5">
              <label class="flex items-center gap-3 text-sm text-gray-800 dark:text-gray-200">
                <input v-model="enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300" />
                {{ t('admin.distribution.invites.enabled') }}
              </label>
              <div>
                <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.distribution.invites.defaultGroups') }}
                </p>
                <p v-if="groups.length === 0" class="mt-2 text-sm text-gray-500">
                  {{ t('admin.distribution.invites.noGroups') }}
                </p>
                <div v-else class="mt-3 space-y-2">
                  <label
                    v-for="group in groups"
                    :key="group.id"
                    class="flex items-center gap-3 text-sm text-gray-800 dark:text-gray-200"
                  >
                    <input
                      type="checkbox"
                      class="h-4 w-4 rounded border-gray-300"
                      :checked="defaultGroupIds.includes(group.id)"
                      @change="toggleDefaultGroup(group.id)"
                    />
                    {{ group.name }}
                  </label>
                </div>
              </div>
              <div class="flex flex-wrap gap-2">
                <button class="btn btn-primary" type="button" :disabled="saving" @click="saveSettings">
                  {{ saving ? t('common.saving') : t('admin.distribution.invites.saveSettings') }}
                </button>
                <button class="btn btn-secondary" type="button" :disabled="rotating" @click="showRotateDialog = true">
                  {{ t('admin.distribution.invites.rotate') }}
                </button>
              </div>
            </div>
          </article>
        </section>

        <section class="card overflow-hidden p-0">
          <div class="border-b border-gray-200 px-6 py-4 dark:border-dark-600">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.distribution.invites.registrations') }}
            </h2>
          </div>
          <div class="overflow-x-auto">
            <table class="min-w-full text-left text-sm" data-test="invite-registrations">
              <thead>
                <tr class="border-b border-gray-200 bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-400">
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.user') }}</th>
                  <th class="px-4 py-3 font-medium">{{ t('admin.distribution.time') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="registrations.length === 0">
                  <td colspan="2" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td>
                </tr>
                <tr
                  v-for="row in registrations"
                  :key="row.id"
                  class="border-b border-gray-100 last:border-0 dark:border-dark-700"
                >
                  <td class="px-4 py-3 text-gray-900 dark:text-white">
                    {{ row.email || row.username || row.user_id }}
                  </td>
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

    <ConfirmDialog
      :show="showRotateDialog"
      :title="t('admin.distribution.invites.rotate')"
      :message="t('admin.distribution.invites.rotateConfirm')"
      :danger="true"
      @confirm="rotateCode"
      @cancel="showRotateDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  getInviteProfile,
  listGroups,
  listInviteRegistrations,
  rotateInviteCode,
  updateInviteSettings,
  type DistributionGroup,
  type DistributionInviteProfile,
  type DistributionInviteRegistration
} from '@/api/admin/distribution'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { extractApiErrorMessage } from '@/utils/apiError'
import { resolveInviteLink } from '@/utils/distributionRange'
import { formatDateTime, formatNumber } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const loading = ref(true)
const saving = ref(false)
const rotating = ref(false)
const showRotateDialog = ref(false)
const profile = ref<DistributionInviteProfile | null>(null)
const groups = ref<DistributionGroup[]>([])
const registrations = ref<DistributionInviteRegistration[]>([])
const enabled = ref(true)
const defaultGroupIds = ref<number[]>([])
const page = ref(1)
const pageSize = 20
const total = ref(0)

const inviteLink = computed(() => {
  if (!profile.value) return ''
  return resolveInviteLink(profile.value.invite_link || profile.value.register_path || '')
})

const applyProfile = (next: DistributionInviteProfile, catalog: DistributionGroup[] = groups.value) => {
  profile.value = next
  enabled.value = next.enabled
  const allowed = new Set(catalog.map((group) => group.id))
  defaultGroupIds.value = (next.default_group_ids || []).filter((id) => allowed.has(id))
}

const load = async () => {
  loading.value = true
  try {
    const [nextProfile, nextGroups, list] = await Promise.all([
      getInviteProfile(),
      listGroups(),
      listInviteRegistrations({ page: page.value, page_size: pageSize })
    ])
    groups.value = nextGroups || []
    applyProfile(nextProfile, groups.value)
    registrations.value = list.items || []
    total.value = list.total || 0
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    loading.value = false
  }
}

const toggleDefaultGroup = (groupId: number) => {
  if (defaultGroupIds.value.includes(groupId)) {
    defaultGroupIds.value = defaultGroupIds.value.filter((id) => id !== groupId)
    return
  }
  defaultGroupIds.value = [...defaultGroupIds.value, groupId]
}

const saveSettings = async () => {
  saving.value = true
  try {
    const next = await updateInviteSettings({
      enabled: enabled.value,
      default_group_ids: defaultGroupIds.value
    })
    applyProfile(next)
    appStore.showSuccess(t('admin.distribution.invites.settingsSaved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    saving.value = false
  }
}

const rotateCode = async () => {
  rotating.value = true
  try {
    const next = await rotateInviteCode()
    applyProfile(next)
    showRotateDialog.value = false
    appStore.showSuccess(t('admin.distribution.invites.rotateSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.distribution.loadFailed')))
  } finally {
    rotating.value = false
  }
}

const copyCode = async () => {
  if (!profile.value?.invite_code) return
  await copyToClipboard(profile.value.invite_code, t('common.copied'))
}

const copyLink = async () => {
  if (!inviteLink.value) return
  await copyToClipboard(inviteLink.value, t('common.copied'))
}

const changePage = async (next: number) => {
  page.value = next
  await load()
}

onMounted(load)
</script>

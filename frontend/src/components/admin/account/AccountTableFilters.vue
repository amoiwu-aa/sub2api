<template>
  <div class="w-full space-y-2.5">
    <!-- 第一行：搜索是查找账号的主要方式，与页面级操作（trailing 插槽）同排 -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <SearchInput
        :model-value="searchQuery"
        :placeholder="t('admin.accounts.searchAccounts')"
        class="w-full sm:w-72"
        @update:model-value="$emit('update:searchQuery', $event)"
        @search="$emit('change')"
      />
      <div v-if="$slots.trailing" class="flex flex-wrap items-center gap-2">
        <slot name="trailing" />
      </div>
    </div>

    <!-- 第二行：高频筛选常驻，低频筛选收进「更多筛选」，启用后以可清除标签外显 -->
    <div class="flex flex-wrap items-center gap-2">
      <Select :model-value="filters.platform" class="w-36" :options="pOpts" @update:model-value="updatePlatform" @change="$emit('change')" />
      <Select :model-value="filters.type" class="w-36" :options="tOpts" @update:model-value="updateType" @change="$emit('change')" />
      <Select :model-value="filters.status" class="w-36" :options="sOpts" @update:model-value="updateStatus" @change="$emit('change')" />

      <div ref="moreFiltersRef" class="relative">
        <button
          type="button"
          class="btn btn-secondary gap-1.5 px-3 py-2.5"
          :aria-expanded="showMoreFilters"
          @click="showMoreFilters = !showMoreFilters"
        >
          <span>{{ t('admin.accounts.moreFilters') }}</span>
          <span
            v-if="collapsedActiveCount > 0"
            class="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-700 px-1 text-[10px] font-medium text-white"
          >
            {{ collapsedActiveCount }}
          </span>
          <Icon
            name="chevronDown"
            size="xs"
            :class="['transition-transform duration-150', showMoreFilters ? 'rotate-180' : '']"
          />
        </button>
        <div
          v-if="showMoreFilters"
          class="absolute left-0 z-50 mt-2 w-72 rounded-xl border border-gray-200 bg-white p-3 shadow-lg dark:border-dark-700 dark:bg-dark-800"
        >
          <div class="space-y-3">
            <div>
              <label class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.privacyFilterLabel') }}
              </label>
              <Select :model-value="filters.privacy_mode" class="w-full" :options="privacyOpts" @update:model-value="updatePrivacyMode" @change="$emit('change')" />
            </div>
            <div>
              <label class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.groupFilterLabel') }}
              </label>
              <Select :model-value="filters.group" class="w-full" :options="gOpts" @update:model-value="updateGroup" @change="$emit('change')" />
            </div>
            <div v-if="collapsedActiveCount > 0" class="border-t border-gray-100 pt-2 dark:border-dark-700">
              <button
                type="button"
                class="text-xs font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"
                @click="clearCollapsedFilters"
              >
                {{ t('admin.accounts.clearFilters') }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <button
        v-if="filters.privacy_mode"
        type="button"
        class="filter-chip"
        :title="t('admin.accounts.clearFilters')"
        @click="clearPrivacyMode"
      >
        <span class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.privacyFilterLabel') }}:</span>
        <span>{{ privacyChipLabel }}</span>
        <Icon name="x" size="xs" />
      </button>
      <button
        v-if="filters.group"
        type="button"
        class="filter-chip"
        :title="t('admin.accounts.clearFilters')"
        @click="clearGroup"
      >
        <span class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.groupFilterLabel') }}:</span>
        <span>{{ groupChipLabel }}</span>
        <Icon name="x" size="xs" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AdminGroup } from '@/types'

const props = defineProps<{ searchQuery: string; filters: Record<string, any>; groups?: AdminGroup[] }>()
const emit = defineEmits(['update:searchQuery', 'update:filters', 'change'])
const { t } = useI18n()

const showMoreFilters = ref(false)
const moreFiltersRef = ref<HTMLElement | null>(null)

// 内部 Select 的下拉面板 teleport 到 body，点击选项时不能误判为「点击了弹层外」
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!showMoreFilters.value) return
  if (moreFiltersRef.value?.contains(target)) return
  if (target.closest('.select-dropdown-portal')) return
  showMoreFilters.value = false
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onUnmounted(() => document.removeEventListener('click', handleClickOutside))

const updatePlatform = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, platform: value }) }
const updateType = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, type: value }) }
const updateStatus = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, status: value }) }
const updatePrivacyMode = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, privacy_mode: value }) }
const updateGroup = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, group: value }) }

const clearPrivacyMode = () => {
  emit('update:filters', { ...props.filters, privacy_mode: '' })
  emit('change')
}
const clearGroup = () => {
  emit('update:filters', { ...props.filters, group: '' })
  emit('change')
}
const clearCollapsedFilters = () => {
  emit('update:filters', { ...props.filters, privacy_mode: '', group: '' })
  emit('change')
}

const collapsedActiveCount = computed(() => {
  let count = 0
  if (props.filters.privacy_mode) count++
  if (props.filters.group) count++
  return count
})

const pOpts = computed(() => [{ value: '', label: t('admin.accounts.allPlatforms') }, { value: 'anthropic', label: 'Anthropic' }, { value: 'openai', label: 'OpenAI' }, { value: 'gemini', label: 'Gemini' }, { value: 'antigravity', label: 'Antigravity' }, { value: 'grok', label: 'Grok' }, { value: 'cursor', label: 'Cursor' }, { value: 'kiro', label: 'Kiro' }])
const tOpts = computed(() => [{ value: '', label: t('admin.accounts.allTypes') }, { value: 'oauth', label: t('admin.accounts.oauthType') }, { value: 'setup-token', label: t('admin.accounts.setupToken') }, { value: 'apikey', label: t('admin.accounts.apiKey') }, { value: 'bedrock', label: 'AWS Bedrock' }])
const sOpts = computed(() => [{ value: '', label: t('admin.accounts.allStatus') }, { value: 'active', label: t('admin.accounts.status.active') }, { value: 'inactive', label: t('admin.accounts.status.inactive') }, { value: 'error', label: t('admin.accounts.status.error') }, { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') }, { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') }, { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }])
const privacyOpts = computed(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])
const gOpts = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups || []).map(g => ({ value: String(g.id), label: g.name }))
])

const privacyChipLabel = computed(() => {
  const match = privacyOpts.value.find(o => o.value === props.filters.privacy_mode)
  return match?.label || String(props.filters.privacy_mode)
})
const groupChipLabel = computed(() => {
  const match = gOpts.value.find(o => o.value === String(props.filters.group))
  return match?.label || String(props.filters.group)
})
</script>

<style scoped>
.filter-chip {
  @apply inline-flex items-center gap-1 rounded-full py-1 pl-2.5 pr-1.5;
  @apply border border-gray-200 bg-white text-xs font-medium text-gray-700;
  @apply transition-colors hover:border-gray-300 hover:bg-gray-50;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700;
}
</style>

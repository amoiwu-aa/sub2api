<template>
  <BaseDialog
    :show="show"
    :title="t('onboarding.prompt.title')"
    width="normal"
    :show-close-button="false"
    :close-on-escape="false"
  >
    <div class="space-y-5">
      <div
        class="inline-flex items-center gap-2 rounded-full bg-primary-50 px-3 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
      >
        <span aria-hidden="true">✨</span>
        {{ t('onboarding.prompt.badge') }}
      </div>

      <div>
        <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">
          {{ t('onboarding.prompt.description') }}
        </p>
        <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
          {{ t('onboarding.prompt.timeHint') }}
        </p>
      </div>

      <div class="grid gap-3 sm:grid-cols-3">
        <div
          v-for="item in items"
          :key="item.title"
          class="rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="text-lg" aria-hidden="true">{{ item.icon }}</div>
          <div class="mt-2 text-sm font-semibold text-gray-900 dark:text-white">
            {{ item.title }}
          </div>
          <div class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
            {{ item.description }}
          </div>
        </div>
      </div>

      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('onboarding.prompt.replayHint') }}
      </p>
    </div>

    <template #footer>
      <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <button
          type="button"
          data-test="onboarding-skip"
          class="btn btn-secondary"
          @click="emit('skip')"
        >
          {{ t('onboarding.prompt.skip') }}
        </button>
        <button
          type="button"
          data-test="onboarding-start"
          class="btn btn-primary"
          @click="emit('start')"
        >
          {{ t('onboarding.prompt.start') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'

defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  (event: 'start'): void
  (event: 'skip'): void
}>()

const { t } = useI18n()

const items = computed(() => [
  {
    icon: '🔑',
    title: t('onboarding.prompt.items.key.title'),
    description: t('onboarding.prompt.items.key.description')
  },
  {
    icon: '🧩',
    title: t('onboarding.prompt.items.client.title'),
    description: t('onboarding.prompt.items.client.description')
  },
  {
    icon: '📊',
    title: t('onboarding.prompt.items.usage.title'),
    description: t('onboarding.prompt.items.usage.description')
  }
])
</script>

<template>
  <BaseDialog
    :show="show"
    :title="resolvedTitle"
    width="narrow"
    :close-on-click-outside="true"
    @close="emit('close')"
  >
    <div class="space-y-5">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">
        {{ resolvedDescription }}
      </p>

      <div
        v-if="safeQrImage"
        class="mx-auto w-full max-w-[260px] overflow-hidden rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700"
      >
        <img
          :src="safeQrImage"
          :alt="t('common.customerService.groupQrAlt')"
          class="aspect-square h-auto w-full object-contain"
        >
      </div>

      <dl
        v-if="normalizedWeChatID || normalizedContactInfo"
        class="divide-y divide-gray-100 border-y border-gray-100 dark:divide-dark-700 dark:border-dark-700"
      >
        <div
          v-if="normalizedWeChatID"
          class="flex min-w-0 items-center gap-3 py-3"
        >
          <dt class="shrink-0 text-sm text-gray-500 dark:text-dark-400">
            {{ t('common.customerService.wechatId') }}
          </dt>
          <dd class="min-w-0 flex-1 break-all text-right font-mono text-sm font-medium text-gray-900 dark:text-white">
            {{ normalizedWeChatID }}
          </dd>
          <button
            type="button"
            class="btn-ghost btn-icon shrink-0"
            :title="t('common.customerService.copyWechatId')"
            :aria-label="t('common.customerService.copyWechatId')"
            @click="copyWeChatID"
          >
            <Icon name="copy" size="sm" />
          </button>
        </div>

        <div
          v-if="normalizedContactInfo"
          class="flex min-w-0 items-start justify-between gap-4 py-3"
        >
          <dt class="shrink-0 text-sm text-gray-500 dark:text-dark-400">
            {{ t('common.customerService.otherContact') }}
          </dt>
          <dd class="min-w-0 break-words text-right text-sm font-medium text-gray-900 dark:text-white">
            {{ normalizedContactInfo }}
          </dd>
        </div>
      </dl>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { sanitizeUrl } from '@/utils/url'

const props = defineProps<{
  show: boolean
  title?: string
  description?: string
  wechatId?: string
  qrImage?: string
  contactInfo?: string
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const resolvedTitle = computed(
  () => props.title?.trim() || t('common.customerService.title')
)
const resolvedDescription = computed(
  () => props.description?.trim() || t('common.customerService.description')
)
const normalizedWeChatID = computed(() => props.wechatId?.trim() || '')
const normalizedContactInfo = computed(() => props.contactInfo?.trim() || '')
const safeQrImage = computed(() =>
  sanitizeUrl(props.qrImage || '', {
    allowRelative: true,
    allowDataUrl: true
  })
)

function copyWeChatID() {
  void copyToClipboard(
    normalizedWeChatID.value,
    t('common.customerService.wechatIdCopied')
  )
}
</script>

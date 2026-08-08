<template>
  <div>
    <div
      v-if="bulkEditing"
      class="rounded-lg border border-primary-200 bg-primary-50/40 p-3 dark:border-primary-800 dark:bg-primary-950/20"
    >
      <textarea
        ref="bulkInputRef"
        v-model="bulkValue"
        rows="10"
        class="input min-h-52 resize-y font-mono text-sm leading-6"
        :placeholder="t('admin.channels.form.modelsPlaceholder')"
        data-testid="model-bulk-textarea"
        @keydown.ctrl.enter.prevent="applyBulkEdit"
        @keydown.meta.enter.prevent="applyBulkEdit"
      ></textarea>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.channels.form.modelBulkEditHint') }}
      </p>
      <div class="mt-2 flex justify-end gap-2">
        <button
          type="button"
          class="btn btn-secondary px-3 py-1.5 text-xs"
          data-testid="model-bulk-cancel"
          @click="cancelBulkEdit"
        >
          {{ t('admin.channels.form.cancelModelBulkEdit') }}
        </button>
        <button
          type="button"
          class="btn btn-primary px-3 py-1.5 text-xs"
          data-testid="model-bulk-apply"
          @click="applyBulkEdit"
        >
          {{ t('admin.channels.form.applyModelBulkEdit') }}
        </button>
      </div>
    </div>

    <template v-else>
      <!-- Tags display -->
      <div class="flex min-h-[2.5rem] flex-wrap gap-1.5 rounded-lg border border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-800">
        <span
          v-for="(model, idx) in models"
          :key="idx"
          class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-sm"
          :class="getPlatformTagClass(props.platform || '')"
          :data-model-index="idx"
        >
          {{ model }}
          <button
            type="button"
            :aria-label="`${t('common.delete')} ${model}`"
            class="ml-0.5 rounded-full p-0.5 hover:bg-primary-200 dark:hover:bg-primary-800"
            @click="removeModel(idx)"
          >
            <Icon name="x" size="xs" />
          </button>
        </span>
        <input
          ref="inputRef"
          v-model="inputValue"
          type="text"
          class="min-w-[120px] flex-1 border-none bg-transparent text-sm outline-none placeholder:text-gray-400 dark:text-white"
          :placeholder="models.length === 0 ? placeholder : ''"
          @keydown.enter.prevent="addModel"
          @keydown.tab.prevent="addModel"
          @keydown="handleInputKeydown"
          @paste="handlePaste"
          @blur="addModel"
        />
      </div>

      <div class="mt-1 flex items-center justify-between gap-3">
        <p class="text-xs text-gray-400">
          {{ t('admin.channels.form.modelInputHint', 'Press Enter to add, supports paste for batch import.') }}
        </p>
        <button
          type="button"
          class="shrink-0 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
          data-testid="model-bulk-edit"
          @click="startBulkEdit"
        >
          {{ t('admin.channels.form.modelBulkEdit') }}
        </button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getPlatformTagClass } from './types'

const { t } = useI18n()

const props = defineProps<{
  models: string[]
  placeholder?: string
  platform?: string
}>()

const emit = defineEmits<{
  'update:models': [models: string[]]
}>()

const inputValue = ref('')
const inputRef = ref<HTMLInputElement>()
const bulkEditing = ref(false)
const bulkValue = ref('')
const bulkInputRef = ref<HTMLTextAreaElement>()

function addModel() {
  const val = inputValue.value.trim()
  if (!val) return
  if (!props.models.includes(val)) {
    emit('update:models', [...props.models, val])
  }
  inputValue.value = ''
}

function removeModel(idx: number) {
  const newModels = [...props.models]
  newModels.splice(idx, 1)
  emit('update:models', newModels)
}

function handleInputKeydown(event: KeyboardEvent) {
  if (
    (event.key === 'Backspace' || event.key === 'Delete') &&
    inputValue.value === '' &&
    props.models.length > 0
  ) {
    event.preventDefault()
    removeModel(props.models.length - 1)
  }
}

function parseModels(text: string): string[] {
  const items = text
    .split(/[\r\n,;]+/)
    .map((item) => item.trim())
    .filter(Boolean)
  return [...new Set(items)]
}

async function startBulkEdit() {
  const pending = inputValue.value.trim()
  bulkValue.value = [...props.models, ...(pending ? [pending] : [])].join('\n')
  inputValue.value = ''
  bulkEditing.value = true
  await nextTick()
  bulkInputRef.value?.focus()
}

async function cancelBulkEdit() {
  bulkEditing.value = false
  bulkValue.value = ''
  await nextTick()
  inputRef.value?.focus()
}

async function applyBulkEdit() {
  emit('update:models', parseModels(bulkValue.value))
  bulkEditing.value = false
  bulkValue.value = ''
  await nextTick()
  inputRef.value?.focus()
}

function handlePaste(e: ClipboardEvent) {
  e.preventDefault()
  const text = e.clipboardData?.getData('text') || ''
  const items = parseModels(text)
  if (items.length === 0) return
  const unique = [...new Set([...props.models, ...items])]
  emit('update:models', unique)
  inputValue.value = ''
}
</script>

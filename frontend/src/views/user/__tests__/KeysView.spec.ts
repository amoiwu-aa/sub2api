import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/types'
import { CC_SWITCH_FALLBACK_DOWNLOADS } from '@/utils/ccswitchDownload'
import KeysView from '../KeysView.vue'

const {
  listKeys,
  getPublicSettings,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  showError,
  showSuccess,
  copyToClipboard,
  isCurrentStep,
  nextStep,
  loadCcSwitchDownloadLinksMock,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  getPublicSettings: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
  loadCcSwitchDownloadLinksMock: vi.fn(),
}))

const messages: Record<string, string> = {
  'common.actions': 'Actions',
  'common.cancel': 'Cancel',
  'common.name': 'Name',
  'common.refresh': 'Refresh',
  'common.status': 'Status',
  'keys.apiKey': 'API Key',
  'keys.allGroups': 'All Groups',
  'keys.allStatus': 'All Status',
  'keys.columnSettings': 'Column Settings',
  'keys.createKey': 'Create API Key',
  'keys.created': 'Created',
  'keys.expiresAt': 'Expires',
  'keys.group': 'Group',
  'keys.id': 'ID',
  'keys.importToCcSwitch': 'Import to CCS',
  'keys.currentConcurrency': 'Current Concurrency',
  'keys.lastUsedAt': 'Last Used',
  'keys.lastUsedIP': 'Last Used IP',
  'keys.rateLimitColumn': 'Rate Limit',
  'keys.searchPlaceholder': 'Search name or key...',
  'keys.status.active': 'Active',
  'keys.status.expired': 'Expired',
  'keys.status.inactive': 'Inactive',
  'keys.status.quota_exhausted': 'Quota exhausted',
  'keys.today': 'Today',
  'keys.total': 'Last 30d',
  'keys.tokens': 'tokens',
  'keys.tokenUsageHint': '{count} tokens total',
  'keys.usage': 'Usage',
  'keys.ccSwitchDownload.title': 'Open or Install CC-Switch',
  'keys.ccSwitchDownload.description': 'Install CC-Switch before importing.',
  'keys.ccSwitchDownload.windows': 'Windows',
  'keys.ccSwitchDownload.windowsDesc': 'Installer (.msi)',
  'keys.ccSwitchDownload.macos': 'macOS',
  'keys.ccSwitchDownload.macosDesc': 'Installer (.dmg)',
  'keys.ccSwitchDownload.linux': 'Linux',
  'keys.ccSwitchDownload.linuxDesc': 'AppImage',
  'keys.ccSwitchDownload.other': 'Other versions',
  'keys.ccSwitchDownload.otherDesc': 'Portable, ARM, and older releases',
  'keys.ccSwitchDownload.recommended': 'Recommended for this device',
  'keys.ccSwitchDownload.afterInstall': 'Return here after installing.',
  'keys.ccSwitchDownload.continueImport': 'Installed, continue import',
  'keys.ccsClientSelect.title': 'Select Import Tool',
  'keys.ccsClientSelect.description': 'Choose the import tool.',
  'keys.ccsClientSelect.claudeCode': 'Claude Code',
  'keys.ccsClientSelect.claudeCodeDesc': 'Import as Claude Code configuration',
  'keys.ccsClientSelect.codex': 'Codex',
  'keys.ccsClientSelect.codexDesc': 'Import as Codex configuration',
  'keys.ccsModelSelect.title': 'Select Import Model',
  'keys.ccsModelSelect.description': 'Select one or more models.',
  'keys.ccsModelSelect.searchPlaceholder': 'Search models...',
  'keys.ccsModelSelect.selectedCount': '{count} model(s) selected',
  'keys.ccsModelSelect.selectVisible': 'Select visible',
  'keys.ccsModelSelect.clear': 'Clear',
  'keys.ccsModelSelect.selected': 'Selected',
  'keys.ccsModelSelect.loading': 'Refreshing models...',
  'keys.ccsModelSelect.empty': 'No matching models',
  'keys.ccsModelSelect.continueImport': 'Continue ({count})',
  'keys.ccsBatchImport.title': 'Import Selected Models',
  'keys.ccsBatchImport.description': 'Import each model separately.',
  'keys.ccsBatchImport.progress': '{imported} of {total} configurations started',
  'keys.ccsBatchImport.import': 'Import',
  'keys.ccsBatchImport.imported': 'Started',
  'keys.ccsBatchImport.importNext': 'Import Next',
  'keys.ccsBatchImport.finish': 'Done',
}

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    toggleStatus: vi.fn(),
  },
  authAPI: {
    getPublicSettings,
  },
  usageAPI: {
    getDashboardApiKeysUsage,
  },
  userGroupsAPI: {
    getAvailable: getAvailableGroups,
    getUserGroupRates,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

vi.mock('@/utils/ccswitchDownload', async () => {
  const actual = await vi.importActual<typeof import('@/utils/ccswitchDownload')>(
    '@/utils/ccswitchDownload'
  )
  return {
    ...actual,
    loadCcSwitchDownloadLinks: loadCcSwitchDownloadLinksMock,
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        const value = messages[key] ?? key
        return Object.entries(params ?? {}).reduce(
          (text, [name, replacement]) => text.replace(`{${name}}`, String(replacement)),
          value
        )
      },
    }),
  }
})

const createApiKey = (overrides: Partial<ApiKey> = {}): ApiKey => ({
  id: 1,
  user_id: 1,
  key: 'sk-test-key',
  name: 'test-key',
  group_id: null,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-06-27T00:00:00Z',
  updated_at: '2026-06-27T00:00:00Z',
  current_concurrency: 3,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
  ...overrides,
})

const createApiKeyForPlatform = (platform: 'anthropic' | 'openai'): ApiKey =>
  createApiKey({
    group_id: 1,
    group: {
      id: 1,
      name: platform === 'openai' ? 'OpenAI Group' : 'Anthropic Group',
      platform,
      allow_messages_dispatch: false,
    } as NonNullable<ApiKey['group']>,
  })

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="actions" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data'],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-current-concurrency" @click="$emit('sort', 'current_concurrency', 'asc')">
        Sort Current Concurrency
      </button>
      <div v-for="row in data" :key="row.id">
        <div
          v-if="columns.some((col) => col.key === 'id')"
          data-test="key-id"
        >
          <slot name="cell-id" :value="row.id" :row="row" />
        </div>
        <slot name="cell-name" :value="row.name" :row="row" />
        <div data-test="current-concurrency">
          <slot name="cell-current_concurrency" :value="row.current_concurrency" :row="row" />
        </div>
        <div data-test="usage">
          <slot name="cell-usage" :row="row" />
        </div>
        <div data-test="actions">
          <slot name="cell-actions" :row="row" />
        </div>
        <div
          v-if="columns.some((col) => col.key === 'last_used_ip')"
          data-test="last-used-ip"
        >
          <slot name="cell-last_used_ip" :value="row.last_used_ip" :row="row" />
        </div>
      </div>
      <slot name="empty" />
    </div>
  `,
}

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"></select>',
}

const SearchInputStub = {
  name: 'SearchInput',
  props: ['modelValue'],
  emits: ['update:modelValue', 'search'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
}

const PaginationStub = {
  name: 'Pagination',
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div>
      <button data-test="page-size-50" @click="$emit('update:pageSize', 50)">50</button>
    </div>
  `,
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const BaseDialogStub = {
  name: 'BaseDialog',
  props: ['show', 'title'],
  emits: ['close'],
  template: `
    <section v-if="show" class="base-dialog" :data-title="title">
      <h2>{{ title }}</h2>
      <slot />
      <slot name="footer" />
    </section>
  `,
}

const mountView = async () => {
  const wrapper = mount(KeysView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: PaginationStub,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        SearchInput: SearchInputStub,
        Icon: IconStub,
        UseKeyModal: true,
        EndpointPopover: true,
        GroupBadge: true,
        GroupOptionItem: true,
        Teleport: true,
      },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const visibleColumnKeys = (wrapper: VueWrapper) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const visibleColumnMeta = (wrapper: VueWrapper): Array<{ key: string; sortable: boolean }> =>
  JSON.parse(wrapper.get('[data-test="columns-meta"]').text())

const getButtonByText = (wrapper: VueWrapper, text: string) => {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`Button not found: ${text}`)
  }
  return button
}

const openCcsModelPicker = async (wrapper: VueWrapper, clientLabel: string) => {
  await getButtonByText(wrapper, 'Import to CCS').trigger('click')
  await flushPromises()
  await getButtonByText(wrapper, 'Installed, continue import').trigger('click')
  await nextTick()
  await getButtonByText(wrapper, clientLabel).trigger('click')
  await flushPromises()
}

describe('user KeysView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()
    loadCcSwitchDownloadLinksMock.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
    loadCcSwitchDownloadLinksMock.mockResolvedValue(CC_SWITCH_FALLBACK_DOWNLOADS)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('uses the default API key columns with low-frequency columns hidden', async () => {
    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'group',
      'current_concurrency',
      'usage',
      'expires_at',
      'status',
      'created_at',
      'actions',
    ])
    expect(visibleColumnKeys(wrapper)).not.toContain('rate_limit')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_at')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
  })

  it('shows a hidden column when toggled and persists the preference', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Rate Limit').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('rate_limit')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['id', 'last_used_at', 'last_used_ip'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('shows the API key ID column when toggled', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'ID').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('id')
    expect(wrapper.get('[data-test="key-id"]').text()).toBe('#1')
    expect(visibleColumnMeta(wrapper).find((column) => column.key === 'id')?.sortable).toBe(true)
  })

  it('shows the last used IP column when toggled', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), last_used_ip: '203.0.113.10' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Last Used IP').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('last_used_ip')
    expect(wrapper.get('[data-test="last-used-ip"]').text()).toBe('203.0.113.10')
  })

  it('restores column preferences from localStorage on mount', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group', 'created_at']))
    localStorage.setItem('api-key-column-settings-version', '1')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'current_concurrency',
      'usage',
      'rate_limit',
      'expires_at',
      'status',
      'last_used_at',
      'actions',
    ])
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'created_at', 'last_used_ip', 'id'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('does not include always-visible columns in the toggleable menu', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await nextTick()

    const columnMenuText = wrapper.text()
    expect(columnMenuText).toContain('API Key')
    expect(columnMenuText).toContain('ID')
    expect(columnMenuText).toContain('Current Concurrency')
    expect(columnMenuText).toContain('Rate Limit')
    expect(columnMenuText).toContain('Last Used IP')
    expect(columnMenuText).not.toContain('Name')
    expect(columnMenuText).not.toContain('Actions')
  })

  it('renders the current concurrency value', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="current-concurrency"]').text()).toBe('3')
  })

  it('renders compact today and 30-day token usage beside cost', async () => {
    getDashboardApiKeysUsage.mockResolvedValueOnce({
      stats: {
        '1': {
          api_key_id: 1,
          today_actual_cost: 0.1234,
          total_actual_cost: 2.3456,
          today_tokens: 1_234_567,
          total_tokens: 9_876_543,
        },
      },
    })

    const wrapper = await mountView()
    const usage = wrapper.get('[data-test="usage"]')

    expect(usage.text()).toContain('Today: $0.1234 · 1.2M tokens')
    expect(usage.text()).toContain('Last 30d: $2.3456 · 9.9M tokens')
    expect(usage.findAll('[title]').map((item) => item.attributes('title'))).toEqual([
      '1,234,567 tokens total',
      '9,876,543 tokens total',
    ])
  })

  it('marks current concurrency as sortable', async () => {
    const wrapper = await mountView()

    const currentConcurrencyColumn = visibleColumnMeta(wrapper).find(
      (column) => column.key === 'current_concurrency'
    )
    expect(currentConcurrencyColumn?.sortable).toBe(true)
  })

  it('keeps filters and selected page size when sorting by current concurrency', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 42, name: 'OpenAI' }])
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-size-50"]').trigger('click')
    await flushPromises()

    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('update:modelValue', 'target')
    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()

    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 42)
    await flushPromises()
    await selects[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()

    listKeys.mockClear()

    await wrapper.get('[data-test="sort-current-concurrency"]').trigger('click')
    await flushPromises()

    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        sort_by: 'current_concurrency',
        sort_order: 'asc',
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('shows installer choices before invoking the CC-Switch protocol', async () => {
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Import to CCS').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Open or Install CC-Switch')
    expect(wrapper.text()).toContain('Recommended for this device')
    expect(openSpy).not.toHaveBeenCalled()

    await getButtonByText(wrapper, 'Installed, continue import').trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('Select Import Tool')
    expect(openSpy).not.toHaveBeenCalled()

    openSpy.mockRestore()
  })

  it.each([
    {
      platform: 'anthropic' as const,
      clientLabel: 'Claude Code',
      models: ['claude-sonnet-4-6', 'claude-opus-4-6'],
    },
    {
      platform: 'openai' as const,
      clientLabel: 'Codex',
      models: ['gpt-5.5', 'gpt-5.6'],
    },
  ])(
    'shows the real model picker for native $platform imports',
    async ({ platform, clientLabel, models }) => {
      listKeys.mockResolvedValueOnce({
        items: [createApiKeyForPlatform(platform)],
        total: 1,
        page: 1,
        page_size: 20,
        pages: 1,
      })
      getPublicSettings.mockResolvedValueOnce({
        api_base_url: 'https://api.example.com',
        site_name: 'RingStar',
      })
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          data: models.map((id) => ({ id })),
        }),
      })
      vi.stubGlobal('fetch', fetchMock)
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
      const wrapper = await mountView()

      await openCcsModelPicker(wrapper, clientLabel)

      expect(wrapper.text()).toContain('Select Import Model')
      for (const model of models) {
        expect(wrapper.find(`[data-model="${model}"]`).exists()).toBe(true)
      }
      expect(fetchMock).toHaveBeenCalledWith('https://api.example.com/v1/models', {
        headers: { Authorization: 'Bearer sk-test-key' },
      })
      expect(openSpy).not.toHaveBeenCalled()
    }
  )

  it('imports multiple selected models as separate CC-Switch providers', async () => {
    listKeys.mockResolvedValueOnce({
      items: [createApiKeyForPlatform('openai')],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValueOnce({
      api_base_url: 'https://api.example.com',
      site_name: 'RingStar',
    })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          data: [{ id: 'gpt-5.5' }, { id: 'gpt-5.6' }],
        }),
      })
    )
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const wrapper = await mountView()

    await openCcsModelPicker(wrapper, 'Codex')
    await wrapper.get('[data-model="gpt-5.6"]').trigger('click')
    await wrapper.get('[data-test="ccs-model-confirm"]').trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('Import Selected Models')
    await wrapper.get('[data-test="ccs-batch-import-next"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="ccs-batch-import-next"]').trigger('click')
    await nextTick()

    expect(openSpy).toHaveBeenCalledTimes(2)
    const imported = openSpy.mock.calls.map(([deeplink]) => {
      const query = String(deeplink).split('?')[1] || ''
      const params = new URLSearchParams(query)
      return {
        app: params.get('app'),
        model: params.get('model'),
        name: params.get('name'),
      }
    })
    expect(imported).toEqual([
      {
        app: 'codex',
        model: 'gpt-5.5',
        name: 'RingStar · gpt-5.5',
      },
      {
        app: 'codex',
        model: 'gpt-5.6',
        name: 'RingStar · gpt-5.6',
      },
    ])
  })
})

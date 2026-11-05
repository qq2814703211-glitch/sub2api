import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey, Group } from '@/types'
import type { BatchApiKeysUsageResponse } from '@/api/usage'
import KeysView from '../KeysView.vue'

enableAutoUnmount(afterEach)

const {
  listKeys,
  createKey,
  updateKey,
  deleteKey,
  deleteManyKeys,
  toggleStatus,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  fetchPublicSettings,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  createKey: vi.fn(),
  updateKey: vi.fn(),
  deleteKey: vi.fn(),
  deleteManyKeys: vi.fn(),
  toggleStatus: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  fetchPublicSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

const messages: Record<string, string> = {
  'common.actions': 'Actions',
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
  'keys.currentConcurrency': 'Current Concurrency',
  'keys.lastUsedAt': 'Last Used',
  'keys.lastUsedIP': 'Last Used IP',
  'keys.rateLimitColumn': 'Rate Limit',
  'keys.searchPlaceholder': 'Search name or key...',
  'keys.status.active': 'Active',
  'keys.status.expired': 'Expired',
  'keys.status.inactive': 'Inactive',
  'keys.status.quota_exhausted': 'Quota exhausted',
  'keys.usage': 'Usage',
  'keys.today': 'Today',
  'keys.total': 'Last 30d',
  'keys.usageLoading': 'Loading usage…',
  'keys.usageFailed': 'Usage unavailable',
  'keys.retryUsage': 'Retry usage',
}

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: createKey,
    update: updateKey,
    delete: deleteKey,
    deleteMany: deleteManyKeys,
    toggleStatus,
  },
  authAPI: {
    getPublicSettings: vi.fn(),
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
    fetchPublicSettings,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep: () => false,
    nextStep: vi.fn(),
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const createApiKey = (overrides: Partial<ApiKey> = {}): ApiKey => ({
  id: 1,
  user_id: 1,
  key: 'sk-test-key',
  name: 'test-key',
  group_id: 1,
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
  current_concurrency: 0,
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

const group = (id: number, name: string): Group => ({
  id,
  name,
  platform: 'anthropic',
  rate_multiplier: 1,
  subscription_type: 'standard',
} as Group)

const AppLayoutStub = { template: '<div><slot /></div>' }
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
  props: {
    columns: Array,
    data: Array,
    selectedKeys: Array,
    selectable: Boolean,
    loading: Boolean,
  },
  emits: ['sort', 'update:selectedKeys'],
  template: `
    <div>
      <div v-if="loading" data-test="table-loading">Loading keys</div>
      <div
        v-for="row in (loading ? [] : data)"
        :key="row.id"
        :data-test="'row-' + row.id"
      >
        <span data-test="row-name">{{ row.name }}</span>
        <span data-test="row-status">{{ row.status }}</span>
        <div :data-test="'usage-' + row.id">
          <slot name="cell-usage" :row="row" />
        </div>
        <slot name="cell-group" :row="row" />
        <slot name="cell-actions" :row="row" />
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
      <span data-test="pagination-state">{{ page }}/{{ total }}</span>
      <button data-test="page-2" @click="$emit('update:page', 2)">Page 2</button>
    </div>
  `,
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const ConfirmDialogStub = {
  name: 'ConfirmDialog',
  props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger', 'loading'],
  emits: ['confirm', 'cancel'],
  template: `
    <div v-if="show" data-test="confirm-dialog">
      <button data-test="confirm-dialog-confirm" :disabled="loading" @click="$emit('confirm')">confirm</button>
      <button data-test="confirm-dialog-cancel" @click="$emit('cancel')">cancel</button>
      <slot />
    </div>
  `,
}

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" role="dialog"><slot /><slot name="footer" /></div>',
}

const GroupOptionItemStub = {
  props: ['name'],
  template: '<span>{{ name }}</span>',
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
        ConfirmDialog: ConfirmDialogStub,
        EmptyState: true,
        Select: SelectStub,
        SearchInput: SearchInputStub,
        Icon: IconStub,
        UseKeyModal: true,
        BulkEditKeysModal: true,
        EndpointPopover: true,
        GroupBadge: true,
        GroupOptionItem: GroupOptionItemStub,
        Teleport: true,
      },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const deferred = <T,>() => {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const table = (wrapper: VueWrapper) => wrapper.findComponent({ name: 'DataTable' })
const paginationTotal = (wrapper: VueWrapper) => {
  const pagination = wrapper.findComponent({ name: 'Pagination' })
  return pagination.exists() ? pagination.props('total') : 0
}
const rows = (wrapper: VueWrapper) =>
  wrapper.findAll('[data-test^="row-"]')
    .map((row) => row.attributes('data-test'))
    .filter((value): value is string => /^row-\d+$/.test(value ?? ''))

const selectRows = async (wrapper: VueWrapper, ids: number[]) => {
  table(wrapper).vm.$emit('update:selectedKeys', ids)
  await nextTick()
}

const visibleDialog = (wrapper: VueWrapper, title: string) => {
  const dialog = wrapper.findAllComponents({ name: 'ConfirmDialog' })
    .find((item) => item.props('title') === title && item.props('show'))
  if (!dialog) throw new Error(`Visible dialog not found: ${title}`)
  return dialog
}

const confirmDialog = async (wrapper: VueWrapper, title: string) => {
  await visibleDialog(wrapper, title).get('[data-test="confirm-dialog-confirm"]').trigger('click')
  await nextTick()
}

describe('user KeysView row actions', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('table-page-size', '20')

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteManyKeys.mockReset()
    toggleStatus.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    fetchPublicSettings.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getDashboardApiKeysUsage.mockImplementation((ids: number[]) => Promise.resolve({
      stats: Object.fromEntries(ids.map((id) => [id, {
        api_key_id: id,
        today_actual_cost: 0,
        total_actual_cost: 0,
      }])),
    } as BatchApiKeysUsageResponse))
    getAvailableGroups.mockResolvedValue([group(1, 'Group A'), group(2, 'Group B')])
    getUserGroupRates.mockResolvedValue({})
    fetchPublicSettings.mockResolvedValue({})
  })

  it('removes a deleted row optimistically and keeps the response page in place', async () => {
    listKeys.mockResolvedValueOnce({
      items: [createApiKey(), createApiKey({ id: 2, name: 'second-key' })],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const deletion = deferred<{ message: string }>()
    deleteKey.mockReturnValueOnce(deletion.promise)
    const wrapper = await mountView()

    await wrapper.get('[data-test="delete-key-1"]').trigger('click')
    await confirmDialog(wrapper, 'keys.deleteKey')

    expect(rows(wrapper)).toEqual(['row-2'])
    expect(paginationTotal(wrapper)).toBe(1)
    expect(listKeys).toHaveBeenCalledOnce()

    deletion.resolve({ message: 'ok' })
    await flushPromises()
    expect(showSuccess).toHaveBeenCalledWith('keys.keyDeletedSuccess')
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('restores a failed row at its original position and its selection', async () => {
    listKeys.mockResolvedValueOnce({
      items: [createApiKey(), createApiKey({ id: 2, name: 'second-key' })],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    deleteKey.mockRejectedValueOnce({ response: { data: { detail: 'Forbidden' } } })
    const wrapper = await mountView()
    await selectRows(wrapper, [1, 2])

    await wrapper.get('[data-test="delete-key-1"]').trigger('click')
    await confirmDialog(wrapper, 'keys.deleteKey')
    await flushPromises()

    expect(rows(wrapper)).toEqual(['row-1', 'row-2'])
    expect(wrapper.get('[data-test="row-1"] [data-test="row-name"]').text()).toBe('test-key')
    expect(paginationTotal(wrapper)).toBe(2)
    expect(table(wrapper).props('selectedKeys').sort()).toEqual([1, 2])
    expect(showError).toHaveBeenCalledWith('Forbidden')
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('bulk deletes selected keys with one per-item call and shows progress', async () => {
    listKeys.mockResolvedValueOnce({
      items: [createApiKey(), createApiKey({ id: 2, name: 'second-key' })],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const deletion = deferred<{ succeeded: number[]; failed: number[] }>()
    deleteManyKeys.mockReturnValueOnce(deletion.promise)
    const wrapper = await mountView()
    await selectRows(wrapper, [1, 2])

    await wrapper.get('[data-test="bulk-delete-keys"]').trigger('click')
    const dialog = visibleDialog(wrapper, 'keys.bulkDelete.title')
    expect(dialog.props('message')).toBe('keys.bulkDelete.confirmMessage')
    await confirmDialog(wrapper, 'keys.bulkDelete.title')

    expect(deleteManyKeys).toHaveBeenCalledWith([1, 2])
    expect(rows(wrapper)).toEqual([])
    expect(paginationTotal(wrapper)).toBe(0)
    expect(wrapper.get('[data-test="bulk-delete-progress"]').text()).toBe('keys.bulkDelete.deleting')

    deletion.resolve({ succeeded: [1, 2], failed: [] })
    await flushPromises()
    expect(showSuccess).toHaveBeenCalledWith('keys.bulkDelete.success')
    expect(wrapper.find('[data-test="bulk-delete-progress"]').exists()).toBe(false)
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('restores only failed bulk rows and leaves them selected', async () => {
    listKeys.mockResolvedValueOnce({
      items: [
        createApiKey(),
        createApiKey({ id: 2, name: 'second-key' }),
        createApiKey({ id: 3, name: 'third-key' }),
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    deleteManyKeys.mockResolvedValueOnce({ succeeded: [1], failed: [2] })
    const wrapper = await mountView()
    await selectRows(wrapper, [1, 2])

    await wrapper.get('[data-test="bulk-delete-keys"]').trigger('click')
    await confirmDialog(wrapper, 'keys.bulkDelete.title')
    await flushPromises()

    expect(rows(wrapper)).toEqual(['row-2', 'row-3'])
    expect(paginationTotal(wrapper)).toBe(2)
    expect(table(wrapper).props('selectedKeys')).toEqual([2])
    expect(showError).toHaveBeenCalledWith('keys.bulkDelete.partialFailure')
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('restores every row and the selection when all bulk deletes fail', async () => {
    listKeys.mockResolvedValueOnce({
      items: [createApiKey(), createApiKey({ id: 2, name: 'second-key' })],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    deleteManyKeys.mockResolvedValueOnce({ succeeded: [], failed: [1, 2] })
    const wrapper = await mountView()
    await selectRows(wrapper, [1, 2])

    await wrapper.get('[data-test="bulk-delete-keys"]').trigger('click')
    await confirmDialog(wrapper, 'keys.bulkDelete.title')
    await flushPromises()

    expect(rows(wrapper)).toEqual(['row-1', 'row-2'])
    expect(paginationTotal(wrapper)).toBe(2)
    expect(table(wrapper).props('selectedKeys').sort()).toEqual([1, 2])
    expect(showError).toHaveBeenCalledWith('keys.failedToDelete')
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('backs up one page after deleting the only row on the last page', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) =>
      createApiKey({ id: index + 1, name: `key-${index + 1}` }))
    listKeys
      .mockResolvedValueOnce({ items: firstPage, total: 21, page: 1, page_size: 20, pages: 2 })
      .mockResolvedValueOnce({
        items: [createApiKey({ id: 21, name: 'last-key' })],
        total: 21,
        page: 2,
        page_size: 20,
        pages: 2,
      })
      .mockResolvedValueOnce({ items: firstPage, total: 20, page: 1, page_size: 20, pages: 1 })
    deleteKey.mockResolvedValueOnce({ message: 'ok' })
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-2"]').trigger('click')
    await flushPromises()
    expect(rows(wrapper)).toEqual(['row-21'])

    await wrapper.get('[data-test="delete-key-21"]').trigger('click')
    await confirmDialog(wrapper, 'keys.deleteKey')
    await flushPromises()

    expect(listKeys).toHaveBeenCalledTimes(3)
    expect(listKeys.mock.calls[2][0]).toBe(1)
    expect(rows(wrapper)).toContain('row-1')
    expect(wrapper.findComponent({ name: 'Pagination' }).props('page')).toBe(1)
    wrapper.unmount()
  })

  it('removes a toggled row that no longer matches the status filter without reloading', async () => {
    const key = createApiKey()
    listKeys.mockResolvedValue({
      items: [key],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    toggleStatus.mockResolvedValueOnce({ ...key, status: 'inactive' })
    const wrapper = await mountView()

    await wrapper.findAllComponents({ name: 'Select' })[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()
    expect(listKeys).toHaveBeenCalledTimes(2)
    expect(listKeys.mock.calls[1][2]).toMatchObject({ status: 'active' })

    await wrapper.get('[data-test="toggle-key-1"]').trigger('click')
    await flushPromises()

    expect(rows(wrapper)).toEqual([])
    expect(paginationTotal(wrapper)).toBe(0)
    expect(listKeys).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('keys.keyDisabledSuccess')
    wrapper.unmount()
  })

  it('ignores a list response that was started before a bulk delete finished', async () => {
    const first = createApiKey()
    const second = createApiKey({ id: 2, name: 'second-key' })
    listKeys.mockResolvedValueOnce({
      items: [first, second],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const deletion = deferred<{ succeeded: number[]; failed: number[] }>()
    const staleList = deferred<{ items: ApiKey[]; total: number; page: number; page_size: number; pages: number }>()
    deleteManyKeys.mockReturnValueOnce(deletion.promise)
    listKeys.mockReturnValueOnce(staleList.promise)
    const wrapper = await mountView()
    await selectRows(wrapper, [1])

    await wrapper.get('[data-test="bulk-delete-keys"]').trigger('click')
    await confirmDialog(wrapper, 'keys.bulkDelete.title')
    await wrapper.get('button[title="Refresh"]').trigger('click')

    deletion.resolve({ succeeded: [1], failed: [] })
    await flushPromises()
    staleList.resolve({
      items: [first, second],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    await flushPromises()

    expect(rows(wrapper)).toEqual(['row-2'])
    expect(paginationTotal(wrapper)).toBe(1)
    expect(listKeys).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('patches an edited row from the server response without reloading', async () => {
    const key = createApiKey()
    listKeys.mockResolvedValueOnce({
      items: [key],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    updateKey.mockResolvedValueOnce({ ...key, name: 'Renamed key' })
    const wrapper = await mountView()

    await wrapper.get('[data-test="edit-key-1"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Renamed key')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-test="row-1"] [data-test="row-name"]').text()).toBe('Renamed key')
    expect(listKeys).toHaveBeenCalledOnce()
    expect(showSuccess).toHaveBeenCalledWith('keys.keyUpdatedSuccess')
    wrapper.unmount()
  })

  it('inserts a created key on the first page from the server response', async () => {
    const key = createApiKey()
    listKeys.mockResolvedValueOnce({
      items: [key],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    createKey.mockResolvedValueOnce(createApiKey({ id: 3, name: 'new-key', created_at: '2026-06-28T00:00:00Z' }))
    const wrapper = await mountView()

    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('new-key')
    wrapper.findComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 1)
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('[data-test="row-3"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="usage-3"]').text()).toContain('$0.0000')
    expect(wrapper.get('[data-test="usage-3"]').text()).not.toContain('Loading usage…')
    expect(paginationTotal(wrapper)).toBe(2)
    expect(listKeys).toHaveBeenCalledOnce()
    expect(showSuccess).toHaveBeenCalledWith('keys.keyCreatedSuccess')
    wrapper.unmount()
  })

  it('merges a changed group from the update response without reloading', async () => {
    const key = createApiKey({ group_id: 1, group: group(1, 'Group A') })
    listKeys.mockResolvedValueOnce({
      items: [key],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    updateKey.mockResolvedValueOnce({ ...key, group_id: 2 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-key-1"]').trigger('click')
    const option = wrapper.findAll('button').find((button) => button.text().includes('Group B'))
    expect(option).toBeTruthy()
    await option!.trigger('click')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, { group_id: 2 })
    expect(table(wrapper).props('data')[0].group_id).toBe(2)
    expect(table(wrapper).props('data')[0].group?.name).toBe('Group B')
    expect(listKeys).toHaveBeenCalledOnce()
    expect(showSuccess).toHaveBeenCalledWith('keys.groupChangedSuccess')
    wrapper.unmount()
  })
})

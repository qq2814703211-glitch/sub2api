import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/types'
import type { BatchApiKeysUsageResponse } from '@/api/usage'
import { keysAPI } from '@/api'
import KeysView from '../KeysView.vue'

enableAutoUnmount(afterEach)

const {
  listKeys,
  updateKey,
  getPublicSettings,
  fetchPublicSettings,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  showError,
  showSuccess,
  copyToClipboard,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  updateKey: vi.fn(),
  getPublicSettings: vi.fn(),
  fetchPublicSettings: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
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
    create: vi.fn(),
    update: updateKey,
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

vi.mock('@/api/keyQueue',()=>({getKeyQueue:vi.fn().mockResolvedValue([])}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    fetchPublicSettings,
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

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const createApiKey = (): ApiKey => ({
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
  props: { columns: Array, data: Array, selectedKeys: Array, selectable: Boolean, loading: Boolean },
  emits: ['sort', 'update:selectedKeys'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-current-concurrency" @click="$emit('sort', 'current_concurrency', 'asc')">
        Sort Current Concurrency
      </button>
      <div v-if="loading" data-test="table-loading">Loading keys</div>
      <div v-for="row in (loading ? [] : data)" :key="row.id">
        <div
          v-if="columns.some((col) => col.key === 'id')"
          data-test="key-id"
        >
          <slot name="cell-id" :value="row.id" :row="row" />
        </div>
        <slot name="cell-name" :value="row.name" :row="row" />
        <div v-if="columns.some((col) => col.key === 'usage')" :data-test="'usage-' + row.id">
          <slot name="cell-usage" :row="row" />
        </div>
        <slot name="cell-actions" :row="row" />
        <div data-test="current-concurrency">
          <slot name="cell-current_concurrency" :value="row.current_concurrency" :row="row" />
        </div>
        <div data-test="usage">
          <slot name="cell-usage" :row="row" />
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
      <button data-test="page-2" @click="$emit('update:page', 2)">Page 2</button>
    </div>
  `,
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const mountView = async () => {
  const wrapper = mount(KeysView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: PaginationStub,
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show" role="dialog"><button data-test="close-dialog" @click="$emit(\'close\')">Close</button><slot /><slot name="footer" /></div>',
        },
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        SearchInput: SearchInputStub,
        Icon: IconStub,
        UseKeyModal: true,
        KeyUsageDialog: {
          name: 'KeyUsageDialog',
          props: ['show', 'apiKey'],
          emits: ['close'],
          template: '<div v-if="show" data-test="key-usage-dialog-stub">{{ apiKey?.id }}:{{ apiKey?.name }}</div>',
        },
        BulkEditKeysModal: true,
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

const deferred = <T,>() => {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('user KeysView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    updateKey.mockReset()
    vi.mocked(keysAPI.create).mockReset()
    getPublicSettings.mockReset()
    fetchPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    fetchPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: { 1: { api_key_id: 1, today_actual_cost: 0, total_actual_cost: 0 } } })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

 it('loads existing limit and submits an explicit zero on edit',async()=>{
  listKeys.mockResolvedValueOnce({items:[{...createApiKey(),group_id:4,concurrency_limit:3}],total:1,page:1,page_size:20,pages:1})
  const wrapper=await mountView()
  await getButtonByText(wrapper, 'common.edit').trigger('click')
  expect((wrapper.get('#key-concurrency').element as HTMLInputElement).value).toBe('3')
  await wrapper.get('#key-concurrency').setValue('0');updateKey.mockResolvedValue({...createApiKey(),concurrency_limit:0})
  await wrapper.get('#key-form').trigger('submit');await flushPromises()
  expect(updateKey).toHaveBeenCalledWith(1,expect.objectContaining({concurrency_limit:0}),expect.objectContaining({signal:expect.any(AbortSignal)}))
 })

  it('renders keys immediately while usage is pending, then displays real zero amounts', async () => {
    const usage = deferred<BatchApiKeysUsageResponse>()
    getDashboardApiKeysUsage.mockReturnValueOnce(usage.promise)
    const wrapper = await mountView()

    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('test-key')
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Loading usage…')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$0.0000')

    usage.resolve({ stats: { 1: { api_key_id: 1, today_actual_cost: 0, total_actual_cost: 0 } } })
    await flushPromises()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Last 30d')
    expect(wrapper.get('[data-test="usage-1"]').text().match(/\$0\.0000/g)).toHaveLength(2)
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('Loading usage…')
    wrapper.unmount()
  })

  it('opens the usage dialog for the clicked key row', async () => {
    const wrapper = await mountView()
    expect(wrapper.find('[data-test="key-usage-dialog-stub"]').exists()).toBe(false)

    await wrapper.get('[data-testid="key-usage-open"]').trigger('click')

    const dialog = wrapper.get('[data-test="key-usage-dialog-stub"]')
    expect(dialog.text()).toBe('1:test-key')
    wrapper.unmount()
  })

  it('clears old amounts on refresh and retries failed usage without reloading keys', async () => {
    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats: { 1: { api_key_id: 1, today_actual_cost: 4.25, total_actual_cost: 12 } } })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$4.2500')

    const usage = deferred<never>()
    getDashboardApiKeysUsage.mockReturnValueOnce(usage.promise)
    await wrapper.get('button[title="Refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Loading usage…')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$4.2500')
    usage.reject(new Error('usage unavailable'))
    await flushPromises()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Usage unavailable')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$0.0000')

    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats: { 1: { api_key_id: 1, today_actual_cost: 0, total_actual_cost: 3.5 } } })
    await getButtonByText(wrapper, 'Retry usage').trigger('click')
    await flushPromises()
    expect(listKeys).toHaveBeenCalledTimes(2)
    expect(getDashboardApiKeysUsage).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$0.0000')
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$3.5000')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('Usage unavailable')
    wrapper.unmount()
  })

  it.each([
    {},
    { 1: { api_key_id: 1, today_actual_cost: null, total_actual_cost: 0 } },
    { 1: { api_key_id: 1, today_actual_cost: 0, total_actual_cost: Number.NaN } },
  ])('treats incomplete or invalid amounts as unavailable instead of zero: %j', async (stats) => {
    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Usage unavailable')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$0.0000')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('Loading usage…')
    wrapper.unmount()
  })

  it('skips hidden usage and loads the current page when shown again', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['usage']))
    localStorage.setItem('api-key-column-settings-version', '3')
    const wrapper = await mountView()
    expect(getDashboardApiKeysUsage).not.toHaveBeenCalled()

    listKeys.mockResolvedValueOnce({ items: [{ ...createApiKey(), id: 2, name: 'Second' }], total: 2, pages: 2 })
    await wrapper.get('[data-test="page-2"]').trigger('click')
    await flushPromises()
    expect(getDashboardApiKeysUsage).not.toHaveBeenCalled()

    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats: { 2: { api_key_id: 2, today_actual_cost: 1, total_actual_cost: 8 } } })
    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Usage').trigger('click')
    await flushPromises()
    expect(getDashboardApiKeysUsage).toHaveBeenCalledOnce()
    expect(getDashboardApiKeysUsage).toHaveBeenCalledWith([2], expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(wrapper.get('[data-test="usage-2"]').text()).toContain('$8.0000')
    expect(listKeys).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('cancels usage when hidden and ignores its late result after showing again', async () => {
    const oldUsage = deferred<BatchApiKeysUsageResponse>()
    getDashboardApiKeysUsage.mockReturnValueOnce(oldUsage.promise)
    const wrapper = await mountView()
    const oldSignal = getDashboardApiKeysUsage.mock.calls[0][1].signal as AbortSignal
    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Usage').trigger('click')
    expect(oldSignal.aborted).toBe(true)
    expect(wrapper.find('[data-test="usage-1"]').exists()).toBe(false)

    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats: { 1: { api_key_id: 1, today_actual_cost: 2, total_actual_cost: 6 } } })
    await getButtonByText(wrapper, 'Usage').trigger('click')
    await flushPromises()
    oldUsage.resolve({ stats: { 1: { api_key_id: 1, today_actual_cost: 99, total_actual_cost: 99 } } })
    await flushPromises()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$6.0000')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$99.0000')
    expect(listKeys).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it.each(['page', 'filter', 'sort'])('cancels old usage on %s changes and rejects late results', async (change) => {
    const oldUsage = deferred<BatchApiKeysUsageResponse>()
    getDashboardApiKeysUsage.mockReturnValueOnce(oldUsage.promise)
    const wrapper = await mountView()
    const oldSignal = getDashboardApiKeysUsage.mock.calls[0][1].signal as AbortSignal
    // Keep the same key ID to expose stale results even across overlapping filters.
    getDashboardApiKeysUsage.mockResolvedValueOnce({ stats: { 1: { api_key_id: 1, today_actual_cost: 2, total_actual_cost: 6 } } })
    if (change === 'page') await wrapper.get('[data-test="page-2"]').trigger('click')
    else if (change === 'filter') wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    else wrapper.findComponent({ name: 'DataTable' }).vm.$emit('sort', 'created_at', 'asc')
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    oldUsage.resolve({ stats: { 1: { api_key_id: 1, today_actual_cost: 99, total_actual_cost: 99 } } })
    await flushPromises()
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$6.0000')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$99.0000')
    wrapper.unmount()
  })

  it('aborts list and usage on leaving the page', async () => {
    const usage = deferred<never>()
    getDashboardApiKeysUsage.mockReturnValueOnce(usage.promise)
    const wrapper = await mountView()
    const listSignal = listKeys.mock.calls[0][3].signal as AbortSignal
    const usageSignal = getDashboardApiKeysUsage.mock.calls[0][1].signal as AbortSignal
    wrapper.unmount()
    expect(listSignal.aborted).toBe(true)
    expect(usageSignal.aborted).toBe(true)
    usage.reject(new Error('late usage failure'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
  })

  it('aborts a pending list on leaving and never starts usage for its late response', async () => {
    const list = deferred<{ items: ApiKey[]; total: number; pages: number }>()
    listKeys.mockReturnValueOnce(list.promise)
    const wrapper = await mountView()
    const signal = listKeys.mock.calls[0][3].signal as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    list.resolve({ items: [createApiKey()], total: 1, pages: 1 })
    await flushPromises()
    expect(getDashboardApiKeysUsage).not.toHaveBeenCalled()
  })

  it('loads a 1000-key page in serial batches of at most 100 without blocking the list', async () => {
    const wrapper = await mountView()
    getDashboardApiKeysUsage.mockClear()
    const keys = Array.from({ length: 1000 }, (_, index) => ({ ...createApiKey(), id: index + 1 }))
    listKeys.mockResolvedValueOnce({ items: keys, total: 1000, pages: 1 })
    const requests: Array<{ ids: number[]; result: ReturnType<typeof deferred<BatchApiKeysUsageResponse>> }> = []
    getDashboardApiKeysUsage.mockImplementation((ids: number[]) => {
      const result = deferred<BatchApiKeysUsageResponse>()
      requests.push({ ids, result })
      return result.promise
    })
    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:pageSize', 1000)
    await flushPromises()
    expect(listKeys.mock.calls[1][1]).toBe(1000)
    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test^="usage-"]')).toHaveLength(1000)
    expect(requests).toHaveLength(1)
    expect(requests[0].ids).toHaveLength(100)

    for (let index = 0; index < 10; index++) {
      expect(requests).toHaveLength(index + 1)
      requests[index].result.resolve({ stats: Object.fromEntries(requests[index].ids.map((id) => [id, {
        api_key_id: id, today_actual_cost: 0.25, total_actual_cost: 3.5,
      }])) })
      await flushPromises()
      if (index < 9) {
        expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Loading usage…')
        expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$0.2500')
      }
    }
    expect(requests.map((request) => request.ids.length)).toEqual([100, 100, 100, 100, 100, 100, 100, 100, 100, 100])
    expect(requests.flatMap((request) => request.ids)).toEqual(Array.from({ length: 1000 }, (_, index) => index + 1))
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$0.2500')
    expect(wrapper.get('[data-test="usage-1000"]').text()).toContain('$3.5000')
    wrapper.unmount()
  })

  it('marks the whole page unavailable if a batch fails and retries every batch explicitly', async () => {
    const keys = Array.from({ length: 201 }, (_, index) => ({ ...createApiKey(), id: index + 1 }))
    listKeys.mockResolvedValueOnce({ items: keys, total: 201, pages: 1 })
    const successfulBatch = (ids: number[]) => Promise.resolve({ stats: Object.fromEntries(ids.map((id) => [id, {
      api_key_id: id, today_actual_cost: 1.25, total_actual_cost: 5,
    }])) })
    getDashboardApiKeysUsage.mockImplementationOnce(successfulBatch)
    getDashboardApiKeysUsage.mockRejectedValueOnce(new Error('second batch failed'))
    const wrapper = await mountView()
    expect(getDashboardApiKeysUsage).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Usage unavailable')
    expect(wrapper.get('[data-test="usage-201"]').text()).toContain('Usage unavailable')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$1.2500')
    expect(wrapper.get('[data-test="usage-201"]').text()).not.toContain('$0.0000')

    getDashboardApiKeysUsage.mockImplementation(successfulBatch)
    await getButtonByText(wrapper, 'Retry usage').trigger('click')
    await flushPromises()
    expect(listKeys).toHaveBeenCalledOnce()
    expect(getDashboardApiKeysUsage).toHaveBeenCalledTimes(5)
    expect(getDashboardApiKeysUsage.mock.calls.map(([ids]) => ids.length)).toEqual([100, 100, 100, 100, 1])
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$1.2500')
    expect(wrapper.get('[data-test="usage-201"]').text()).toContain('$5.0000')
    wrapper.unmount()
  })

  it('keeps the newest list loading through rapid navigation and ignores old list failures', async () => {
    const wrapper = await mountView()
    const oldList = deferred<never>()
    const newList = deferred<{ items: ApiKey[]; total: number; pages: number }>()
    listKeys.mockReturnValueOnce(oldList.promise).mockReturnValueOnce(newList.promise)
    await wrapper.get('[data-test="page-2"]').trigger('click')
    const oldSignal = listKeys.mock.calls[1][3].signal as AbortSignal
    wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await nextTick()
    expect(oldSignal.aborted).toBe(true)
    oldList.reject(new Error('late list failure'))
    await flushPromises()
    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(true)
    expect(showError).not.toHaveBeenCalled()
    newList.resolve({ items: [{ ...createApiKey(), name: 'Latest key' }], total: 1, pages: 1 })
    await flushPromises()
    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Latest key')
    expect(getDashboardApiKeysUsage).toHaveBeenCalledTimes(2)
  })

  it('ignores stale list success after the newest page has rendered', async () => {
    const oldList = deferred<{ items: ApiKey[]; total: number; pages: number }>()
    listKeys.mockReturnValueOnce(oldList.promise)
    const wrapper = await mountView()
    wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()
    expect(wrapper.text()).toContain('test-key')
    oldList.resolve({ items: [{ ...createApiKey(), name: 'Stale key' }], total: 1, pages: 1 })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Stale key')
    expect(getDashboardApiKeysUsage).toHaveBeenCalledOnce()
  })

  it.each(['resolve', 'reject'])('stops remaining batches and ignores an obsolete batch %s after navigation', async (completion) => {
    const keys = Array.from({ length: 201 }, (_, index) => ({ ...createApiKey(), id: index + 1 }))
    listKeys.mockResolvedValueOnce({ items: keys, total: 201, pages: 1 })
    const oldUsage = deferred<BatchApiKeysUsageResponse>()
    getDashboardApiKeysUsage.mockReturnValueOnce(oldUsage.promise)
    const wrapper = await mountView()
    const oldSignal = getDashboardApiKeysUsage.mock.calls[0][1].signal as AbortSignal
    await wrapper.get('[data-test="page-2"]').trigger('click')
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    if (completion === 'resolve') {
      oldUsage.resolve({ stats: Object.fromEntries(keys.slice(0, 100).map((key) => [key.id, {
        api_key_id: key.id, today_actual_cost: 99, total_actual_cost: 99,
      }])) })
    } else oldUsage.reject(new Error('obsolete usage failure'))
    await flushPromises()
    expect(getDashboardApiKeysUsage).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('$0.0000')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('Usage unavailable')
    expect(showError).not.toHaveBeenCalled()
  })

  it('does not leave old rows showing pending usage if refreshing the list fails', async () => {
    const wrapper = await mountView()
    listKeys.mockRejectedValueOnce(new Error('list unavailable'))
    await wrapper.get('button[title="Refresh"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('keys.failedToLoad')
    expect(wrapper.get('[data-test="usage-1"]').text()).toContain('Usage unavailable')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('Loading usage…')
    expect(wrapper.get('[data-test="usage-1"]').text()).not.toContain('$0.0000')
  })

  it('submits the edited fallback order with its matching primary group', async () => {
    const key: ApiKey = { ...createApiKey(), group_id: 1, group_ids: [1, 2, 3] }
    listKeys.mockResolvedValueOnce({ items: [key], total: 1, pages: 1 })
    updateKey.mockResolvedValue({ ...key, group_id: 3, group_ids: [3, 1, 2] })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    const editor = wrapper.findComponent({ name: 'OrderedGroupRoutesEditor' })
    expect(editor.props('modelValue')).toEqual([1, 2, 3])
    editor.vm.$emit('update:modelValue', [3, 1, 2])
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(key.id, expect.objectContaining({ group_id: 3, group_ids: [3, 1, 2], concurrency_limit: 0 }), { signal: expect.any(AbortSignal) })
    wrapper.unmount()
  })

  it('uses cached public settings from the app store for endpoints', async () => {
    fetchPublicSettings.mockResolvedValue({ api_base_url: 'https://cached.example.test', custom_endpoints: [] })
    const wrapper = await mountView()
    expect(fetchPublicSettings).toHaveBeenCalledOnce()
    expect(getPublicSettings).not.toHaveBeenCalled()
    expect(wrapper.findComponent({ name: 'EndpointPopover' }).props('apiBaseUrl')).toBe('https://cached.example.test')
    wrapper.unmount()
  })

  it.each([
    { initialStatus: 'quota_exhausted', status: 'active', formStatus: 'active' },
    { initialStatus: 'inactive', status: 'inactive', formStatus: 'inactive' },
    { initialStatus: 'active', status: 'active', formStatus: 'inactive' },
  ] as const)('syncs quota reset from $initialStatus to $status with form status $formStatus', async ({ initialStatus, status, formStatus }) => {
    const key: ApiKey = {
      ...createApiKey(), group_id: 1, quota: 10, quota_used: 10,
      status: initialStatus,
    }
    listKeys.mockResolvedValueOnce({ items: [key], total: 1, page: 1, page_size: 20, pages: 1 })
    updateKey.mockResolvedValue({ ...key, status, quota_used: 0 })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Unsaved name')
    const statusSelect = wrapper.findAllComponents({ name: 'Select' })
      .find((select) => select.props('options').length === 2 &&
        select.props('options')[0].value === 'active')!
    statusSelect.vm.$emit('update:modelValue', 'inactive')
    await wrapper.get('button[title="keys.resetQuotaUsed"]').trigger('click')
    const confirmation = wrapper.findAllComponents({ name: 'ConfirmDialog' })
      .find((dialog) => dialog.props('title') === 'keys.resetQuotaTitle')!
    confirmation.vm.$emit('confirm')
    await flushPromises()

    expect(updateKey).toHaveBeenNthCalledWith(1, key.id, { reset_quota: true })
    expect(wrapper.findComponent({ name: 'DataTable' }).props('data')[0])
      .toMatchObject({ status, quota_used: 0 })
    expect(statusSelect.props('modelValue')).toBe(formStatus)
    expect((wrapper.get('[data-tour="key-form-name"]').element as HTMLInputElement).value)
      .toBe('Unsaved name')

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenNthCalledWith(2, key.id, expect.objectContaining({ name: 'Unsaved name', status: formStatus }), expect.objectContaining({signal:expect.any(AbortSignal)}))
    wrapper.unmount()
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

  it('opens bulk editing with only selected visible keys', async () => {
    const wrapper = await mountView()
    const table = wrapper.findComponent({ name: 'DataTable' })
    expect(table.props('selectable')).toBe(true)
    table.vm.$emit('update:selectedKeys', [1, 99])
    await nextTick()
    await wrapper.get('[data-test="bulk-edit-keys"]').trigger('click')
    const modal = wrapper.findComponent({ name: 'BulkEditKeysModal' })
    expect(modal.props('show')).toBe(true)
    expect(modal.props('selectedKeys').map((key: ApiKey) => key.id)).toEqual([1])
    wrapper.unmount()
  })

  it.each(['filter', 'page size', 'sort'])('clears selection on %s changes', async (change) => {
    const wrapper = await mountView()
    const table = wrapper.findComponent({ name: 'DataTable' })
    table.vm.$emit('update:selectedKeys', [1])
    await nextTick()
    if (change === 'filter') {
      wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    } else if (change === 'page size') {
      await wrapper.get('[data-test="page-size-50"]').trigger('click')
    } else {
      table.vm.$emit('sort', 'created_at', 'asc')
    }
    await flushPromises()
    expect(table.props('selectedKeys')).toEqual([])
    expect(wrapper.find('[data-test="bulk-edit-keys"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('removes successful keys from the selection and refreshes the table', async () => {
    listKeys.mockResolvedValue({
      items: [createApiKey(), { ...createApiKey(), id: 2, name: 'Second' }],
      total: 2, pages: 1
    })
    const wrapper = await mountView()
    const table = wrapper.findComponent({ name: 'DataTable' })
    table.vm.$emit('update:selectedKeys', [1, 2])
    await nextTick()
    await wrapper.get('[data-test="bulk-edit-keys"]').trigger('click')
    wrapper.findComponent({ name: 'BulkEditKeysModal' }).vm.$emit('updated', [1])
    await flushPromises()
    expect(listKeys).toHaveBeenCalledTimes(2)
    expect(table.props('selectedKeys')).toEqual([2])
    wrapper.unmount()
  })

  it('drops keys that are no longer visible after a refresh', async () => {
    const wrapper = await mountView()
    const table = wrapper.findComponent({ name: 'DataTable' })
    table.vm.$emit('update:selectedKeys', [1])
    await nextTick()
    listKeys.mockResolvedValue({ items: [], total: 0, pages: 0 })
    await wrapper.get('button[title="Refresh"]').trigger('click')
    await flushPromises()
    expect(table.props('selectedKeys')).toEqual([])
    wrapper.unmount()
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

    expect(wrapper.get('[data-test="current-concurrency"]').text()).toContain('3 / ∞')
  })

  it('marks current concurrency as sortable', async () => {
    const wrapper = await mountView()

    const currentConcurrencyColumn = visibleColumnMeta(wrapper).find(
      (column) => column.key === 'current_concurrency'
    )
    expect(currentConcurrencyColumn?.sortable).toBe(true)
  })

  it.each([
    { key: 'current_concurrency', order: 'asc' },
    { key: 'group', order: 'asc' },
    { key: 'group', order: 'desc' },
  ] as const)('keeps filters and resets pagination and selection when sorting $key $order', async ({ key, order }) => {
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

    await wrapper.get('[data-test="page-2"]').trigger('click')
    await flushPromises()
    const table = wrapper.findComponent({ name: 'DataTable' })
    table.vm.$emit('update:selectedKeys', [1])
    await nextTick()
    expect(table.props('selectedKeys')).toEqual([1])
    expect(visibleColumnMeta(wrapper).find((column) => column.key === key)?.sortable).toBe(true)
    listKeys.mockClear()

    table.vm.$emit('sort', key, order)
    await flushPromises()

    expect(table.props('selectedKeys')).toEqual([])
    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        sort_by: key,
        sort_order: order,
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  describe('create provider selection', () => {
    const platforms = ['anthropic', 'openai', 'kimi', 'zhipu', 'deepseek', 'minimax', 'gemini', 'grok', 'antigravity', 'composite', 'opencode_go', 'typesafe']
    const availableGroups = platforms.map((platform, index) => ({
      id: index + 1,
      // Deliberately ambiguous names: classification must follow the platform.
      name: `Shared group ${index + 1}`,
      platform,
      rate_multiplier: 1,
      subscription_type: 'standard',
    }))
    const groupSelect = (wrapper: VueWrapper) => wrapper.findComponent('[data-tour="key-form-group"]')
    const optionIds = (wrapper: VueWrapper) => groupSelect(wrapper).props('options').map((option: { value: number }) => option.value)
    const chooseProvider = (wrapper: VueWrapper, value: string) => wrapper.get(`input[name="key-provider"][value="${value}"]`).setValue()
    const openCreate = async () => {
      const wrapper = await mountView()
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      return wrapper
    }

    beforeEach(() => {
      getAvailableGroups.mockResolvedValue(availableGroups)
    })

    it('classifies all configured platforms and retains the complete table filter', async () => {
      const wrapper = await openCreate()
      expect(wrapper.findAll('input[name="key-provider"]')).toHaveLength(4)
      expect(optionIds(wrapper)).toEqual([1])
      await chooseProvider(wrapper, 'openai')
      expect(optionIds(wrapper)).toEqual([2])
      await chooseProvider(wrapper, 'domestic')
      expect(optionIds(wrapper)).toEqual([3, 4, 5, 6])
      await chooseProvider(wrapper, 'other')
      expect(optionIds(wrapper)).toEqual([7, 8, 9, 10, 11, 12])
      expect(wrapper.findAllComponents({ name: 'Select' })[0].props('options')).toHaveLength(14)
    })

    it('clears the previous group on provider change and submits only the newly selected group', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('My key')
      await groupSelect(wrapper).vm.$emit('update:modelValue', 1)
      await chooseProvider(wrapper, 'domestic')
      expect(groupSelect(wrapper).props('modelValue')).toBeNull()
      await wrapper.get('#key-form').trigger('submit')
      expect(keysAPI.create).not.toHaveBeenCalled()
      expect(showError).toHaveBeenCalledWith('keys.groupRequired')

      await groupSelect(wrapper).vm.$emit('update:modelValue', 5)
      vi.mocked(keysAPI.create).mockResolvedValue({ ...createApiKey(), group_id: 5 })
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
      expect(keysAPI.create).toHaveBeenCalledOnce()
      expect(vi.mocked(keysAPI.create).mock.calls[0].slice(0, 2)).toEqual(['My key', 5])
    })

    it('defaults to a provider with available groups and disables empty categories', async () => {
      getAvailableGroups.mockResolvedValue([availableGroups[5]])
      const wrapper = await openCreate()
      expect(wrapper.get<HTMLInputElement>('input[value="domestic"]').element.checked).toBe(true)
      expect(wrapper.get<HTMLInputElement>('input[value="anthropic"]').element.disabled).toBe(true)
      expect(optionIds(wrapper)).toEqual([6])
    })

    it('shows the empty state when no groups are available', async () => {
      getAvailableGroups.mockResolvedValue([])
      const wrapper = await openCreate()
      expect(wrapper.get('[data-tour="key-form-provider"]').text()).toContain('common.noGroupsAvailable')
      expect(optionIds(wrapper)).toEqual([])
      expect(wrapper.findAll<HTMLInputElement>('input[name="key-provider"]').every((input) => input.element.disabled)).toBe(true)
    })

    it('selects an available provider when groups arrive after opening', async () => {
      let resolveGroups!: (value: typeof availableGroups) => void
      getAvailableGroups.mockReturnValue(new Promise((resolve) => { resolveGroups = resolve }))
      const wrapper = await openCreate()
      resolveGroups([availableGroups[1]])
      await flushPromises()
      expect(wrapper.get<HTMLInputElement>('input[value="openai"]').element.checked).toBe(true)
      expect(optionIds(wrapper)).toEqual([2])
    })

    it('resets provider and group when reopening create, and preserves edit options', async () => {
      const wrapper = await openCreate()
      await chooseProvider(wrapper, 'domestic')
      await groupSelect(wrapper).vm.$emit('update:modelValue', 5)
      await wrapper.get('[data-test="close-dialog"]').trigger('click')
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      expect(optionIds(wrapper)).toEqual([1])
      expect(groupSelect(wrapper).props('modelValue')).toBeNull()
      await wrapper.get('[data-test="close-dialog"]').trigger('click')
      await getButtonByText(wrapper, 'common.edit').trigger('click')
      expect(wrapper.find('[data-tour="key-form-provider"]').exists()).toBe(false)
      expect(optionIds(wrapper)).toHaveLength(12)
    })
  })

})

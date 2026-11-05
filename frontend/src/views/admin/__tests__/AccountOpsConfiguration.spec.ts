import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import AccountOpsView from '../AccountOpsView.vue'
import OpsCenterView from '../OpsCenterView.vue'
import UpstreamMonitorView from '../UpstreamMonitorView.vue'
import en from '@/i18n/locales/en/admin'
import zh from '@/i18n/locales/zh/admin'
import type { AccountOpsConfig } from '@/api/admin/accountOps'

enableAutoUnmount(afterEach)
const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: http }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot/></div>' } }))
vi.mock('@/components/admin/operations/UpstreamHistoryPanel.vue', () => ({ default: { template: '<div/>' } }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { props: ['controller'], template: '<div v-if="controller.visible.value" data-testid="step-up"><button data-testid="step-up-cancel" @click="controller.onCancel()">Cancel</button><button data-testid="step-up-verify" @click="controller.onVerified()">Verify</button></div>' } }))
const makeI18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en, common: { confirm: 'Confirm', cancel: 'Cancel' } }, zh: { admin: zh, common: { confirm: '确认', cancel: '取消' } } } })
let i18n: ReturnType<typeof makeI18n>
const stubs = { AdminPageHeader: { template: '<header><slot name="actions"/></header>' }, RouterLink: { template: '<a><slot/></a>' }, ConfirmDialog: { props: ['show', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" role="dialog">{{ message }}<button data-testid="confirm-change" @click="$emit(\'confirm\')">Confirm</button><button data-testid="cancel-change" @click="$emit(\'cancel\')">Cancel</button></div>' } }
let config: AccountOpsConfig
const account = (id: number) => ({ account_id: id, account_name: `Account ${id}`, platform: 'openai', type: 'apikey', balance: 10, balance_status: 'ok', unit: 'USD', received_at: null, usage_windows: [] })
beforeEach(() => {
  vi.resetAllMocks()
  i18n = makeI18n()
  config = { enabled: false, monitor_enabled: false, sample_interval_minutes: 5, recipient: 'operator@example.test', email_name: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60, groups_initialized: true, ungrouped_account_ids: [2, 3], webhooks: [], groups: [{ id: 'manual-group001', name: 'Existing group', provider: 'sub2api', site: 'https://upstream.example.test', account_ids: [1] }], balance_thresholds: [], quota_thresholds: [] }
  http.get.mockImplementation(async (path: string) => {
    if (path.endsWith('/config')) return { data: { config: structuredClone(config), smtp_configured: true, encryption_key_configured: true, dropped_signals: 0, storage_failures: 0 } }
    if (path.endsWith('/threshold-accounts')) return { data: { items: [account(1), account(2), account(3)] } }
    if (path.endsWith('/groups')) return { data: { groups: (config.groups ?? []).map(g => ({ ...g, default_name: g.name, mode: 'manual' })) } }
    if (path.endsWith('/alerts')) return { data: { items: [], has_more: false } }
    if (path.endsWith('/settings')) return { data: { enabled: true, interval_minutes: 30 } }
    if (path.endsWith('/overview')) return { data: { total_accounts: 1, with_balance: 0, failed: 0, stale: 0, unsupported: 0, open_alerts: 0, currencies: [], attention: [] } }
    if (path.endsWith('/accounts') || path.endsWith('/rate-changes')) return { data: { items: [], total: 0, pages: 1 } }
    throw new Error(`Unexpected GET ${path}`)
  })
  http.put.mockImplementation(async (path: string, payload: object) => {
    if (path.endsWith('/groups')) Object.assign(config, payload)
    return { data: structuredClone(config) }
  })
})
const render = async () => { const w = mount(AccountOpsView, { global: { stubs, plugins: [i18n] } }); await flushPromises(); return w }

describe('account operations configuration forms through API client', () => {
  it('edits and saves persisted group membership with handler DTO fields', async () => {
    const w = await render()
    expect(w.text()).toContain('Group mapping')
    await w.get('[data-testid="group-name-0"]').setValue('Renamed group')
    await w.get('[data-testid="group-accounts-0"]').setValue(['1', '2'])
    await w.get('[data-testid="save-groups"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledWith('/admin/account-ops/groups', { groups: [{ id: 'manual-group001', name: 'Renamed group', provider: 'sub2api', site: 'https://upstream.example.test', account_ids: [1, 2] }] })
    expect(w.text()).toContain('Saved')
  })

  it('applies one selected group as a balance rule batch after confirmation', async () => {
    const w = await render()
    expect(w.text()).toContain('Batch threshold rules')
    await w.get('[data-testid="batch-group-0"]').setValue('manual-group001')
    await w.get('[data-testid="batch-value-0"]').setValue(7.5)
    await w.get('[data-testid="batch-recovery-0"]').setValue(false)
    await w.get('[data-testid="batch-form"]').trigger('submit')
    expect(http.put).not.toHaveBeenCalled()
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledWith('/admin/account-ops/rules/batch', { account_ids: [1], rule: { metric: 'balance', enabled: true, threshold: 7.5, unit: 'USD', notify_alert: true, notify_recovery: false } })
    expect(w.text()).toContain('Saved')
  })

  it('submits two disjoint rule groups in one transaction, including enable-only without threshold fields', async () => {
    const w = await render()
    await w.get('[data-testid="batch-accounts-0"]').setValue(['1'])
    await w.get('[data-testid="batch-enabled-only-0"]').setValue(true)
    await w.get('[data-testid="batch-enabled-0"]').setValue(false)
    await w.get('[data-testid="add-batch"]').trigger('click')
    await w.get('[data-testid="batch-accounts-1"]').setValue(['2', '3'])
    await w.get('[data-testid="batch-metric-1"]').setValue('quota')
    await w.get('[data-testid="batch-value-1"]').setValue(85)
    await w.get('[data-testid="batch-window-1"]').setValue('7d')
    await w.get('[data-testid="batch-alert-1"]').setValue(false)
    await w.get('[data-testid="batch-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledWith('/admin/account-ops/rules/batch', { groups: [
      { account_ids: [1], rule: { metric: 'balance', enabled: false, enabled_only: true } },
      { account_ids: [2, 3], rule: { metric: 'quota', enabled: true, threshold_percent: 85, window: '7d', notify_alert: false, notify_recovery: true } }
    ] })
    expect(http.put).toHaveBeenCalledTimes(1)
    expect(w.get('[data-testid="add-batch"]').attributes('disabled')).toBeDefined()
  })

  it('validates duplicate batch accounts and quota limits before issuing a request', async () => {
    const w = await render()
    await w.get('[data-testid="batch-accounts-0"]').setValue(['1'])
    await w.get('[data-testid="add-batch"]').trigger('click')
    await w.get('[data-testid="batch-accounts-1"]').setValue(['1'])
    await w.get('[data-testid="batch-form"]').trigger('submit')
    expect(w.get('[role="alert"]').text()).toContain('without duplicates')
    await w.get('[data-testid="batch-accounts-1"]').setValue(['2'])
    await w.get('[data-testid="batch-metric-1"]').setValue('quota')
    await w.get('[data-testid="batch-value-1"]').setValue(101)
    await w.get('[data-testid="batch-form"]').trigger('submit')
    expect(w.get('[role="alert"]').text()).toContain('at most 100')
    expect(http.put).not.toHaveBeenCalled()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
  })

  it('cancels a batch confirmation without saving and preserves editable values', async () => {
    const w = await render()
    await w.get('[data-testid="batch-accounts-0"]').setValue(['2'])
    await w.get('[data-testid="batch-value-0"]').setValue(19)
    await w.get('[data-testid="batch-form"]').trigger('submit')
    await w.get('[data-testid="cancel-change"]').trigger('click')
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect((w.get('[data-testid="batch-value-0"]').element as HTMLInputElement).value).toBe('19')
    expect(http.put).not.toHaveBeenCalled()
  })

  it('keeps batch selection on server failure and supports a deliberate retry', async () => {
    http.put.mockRejectedValueOnce(new Error('Batch unit mismatch'))
    const w = await render()
    await w.get('[data-testid="batch-accounts-0"]').setValue(['2'])
    await w.get('[data-testid="batch-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('Batch unit mismatch')
    expect(w.find('[role="status"]').exists()).toBe(false)
    expect((w.get('[data-testid="batch-accounts-0"]').element as HTMLSelectElement).selectedOptions[0].value).toBe('2')
    await w.get('[data-testid="batch-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(2)
    expect(w.get('[role="status"]').text()).toBe('Saved')
  })

  it('adds groups with editable provider/site, blocks duplicate memberships, and can clear all mappings', async () => {
    const w = await render()
    await w.get('[data-testid="add-group"]').trigger('click')
    await w.get('[data-testid="group-name-1"]').setValue('New group')
    await w.get('[data-testid="group-provider-1"]').setValue('new_api')
    await w.get('[data-testid="group-site-1"]').setValue('https://new.example.test')
    await w.get('[data-testid="group-accounts-1"]').setValue(['1'])
    await w.get('[data-testid="groups-form"]').trigger('submit')
    expect(w.get('[role="alert"]').text()).toContain('unique across groups')
    expect(http.put).not.toHaveBeenCalled()
    await w.get('[data-testid="group-accounts-1"]').setValue(['3'])
    await w.get('[data-testid="groups-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put.mock.calls[0][1].groups[1]).toEqual({ id: expect.stringMatching(/^manual-[A-Za-z0-9-]{8,64}$/), name: 'New group', provider: 'new_api', site: 'https://new.example.test', account_ids: [3] })
    await w.get('[data-testid="remove-group-1"]').trigger('click')
    await w.get('[data-testid="remove-group-0"]').trigger('click')
    await w.get('[data-testid="groups-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenLastCalledWith('/admin/account-ops/groups', { groups: [] })
    expect(w.text()).toContain('No manual groups.')
  })

  it('preserves unavailable mapped account IDs and retains group draft on failure', async () => {
    config.groups![0].account_ids = [1, 99]
    http.put.mockRejectedValueOnce(new Error('Group storage failed'))
    const w = await render()
    expect(w.get('[data-testid="group-accounts-0"]').text()).toContain('#99')
    await w.get('[data-testid="group-name-0"]').setValue('Retry me')
    await w.get('[data-testid="groups-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('Group storage failed')
    expect(http.put.mock.calls[0][1].groups[0].account_ids).toEqual([1, 99])
    expect((w.get('[data-testid="group-name-0"]').element as HTMLInputElement).value).toBe('Retry me')
  })

  it('saves complete config preserving saved rules/mapping state and redacted channels through step-up', async () => {
    config.webhooks = [{ id: 'robot', name: 'Ops', provider: 'feishu', enabled: true, url_configured: true, secret_configured: true, message_template: '' }]
    config.balance_thresholds = [{ account_id: 1, enabled: true, threshold: 10, unit: 'USD', notify_alert: false, notify_recovery: true }]
    http.put.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const w = await render()
    await w.get('[data-testid="recipient"]').setValue('new@example.test')
    await w.get('[data-testid="save-config"]').trigger('click')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="step-up"]').exists()).toBe(true)
    await w.get('[data-testid="step-up-verify"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(2)
    const [path, payload] = http.put.mock.calls[1]
    expect(path).toBe('/admin/account-ops/config')
    expect(payload).toEqual({ ...config, recipient: 'new@example.test', webhooks: [{ id: 'robot', name: 'Ops', provider: 'feishu', enabled: true, message_template: '' }] })
    expect(payload.webhooks[0]).not.toHaveProperty('url_configured')
    expect(payload.webhooks[0]).not.toHaveProperty('secret')
    expect(w.get('[role="status"]').text()).toBe('Saved')
  })

  it('treats step-up cancellation as a silent no-save and releases saving state', async () => {
    http.put.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const w = await render()
    await w.get('[data-testid="save-config"]').trigger('click')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    await w.get('[data-testid="step-up-cancel"]').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(1)
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect(w.find('[role="status"]').exists()).toBe(false)
    expect(w.get('fieldset').attributes('disabled')).toBeUndefined()
  })

  it('uses one AbortSignal for config, accounts, groups, alerts and shared probe settings and aborts on unmount', async () => {
    const w = await render()
    expect(http.get).toHaveBeenCalledTimes(5)
    const signals = http.get.mock.calls.map(call => call[1].signal as AbortSignal)
    expect(signals.every(signal => signal === signals[0] && !signal.aborted)).toBe(true)
    expect(http.get).toHaveBeenCalledWith('/admin/accounts/upstream-billing-probe/settings', { signal: signals[0] })
    w.unmount()
    expect(signals.every(signal => signal.aborted)).toBe(true)
  })

  it('refresh cancels previous reads and ignores stale config that arrives later', async () => {
    const original = http.get.getMockImplementation()!
    let finish!: (value: unknown) => void
    http.get.mockImplementation((path: string, options: unknown) => path.endsWith('/config') ? new Promise(resolve => { finish = resolve }) : original(path, options))
    const w = mount(AccountOpsView, { global: { stubs, plugins: [i18n] } })
    await flushPromises()
    const firstSignal = http.get.mock.calls[0][1].signal as AbortSignal
    http.get.mockImplementation(original)
    await w.get('header button').trigger('click')
    await flushPromises()
    finish({ data: { config: { ...config, recipient: 'late@example.test' } } })
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    expect((w.get('[data-testid="recipient"]').element as HTMLInputElement).value).toBe('operator@example.test')
    expect(w.find('[role="alert"]').exists()).toBe(false)
  })

  it('changes rendered form labels when the locale changes without remounting', async () => {
    const w = await render()
    expect(w.text()).toContain('Group mapping')
    i18n.global.locale.value = 'zh'
    await nextTick()
    expect(w.text()).toContain('分组映射')
    expect(w.text()).toContain('批量阈值规则')
    expect(w.text()).not.toContain('admin.accountOps.')
  })

  it('reports successful persistence separately when the follow-up group read fails', async () => {
    const w = await render()
    const original = http.get.getMockImplementation()!
    http.get.mockImplementation((path: string, options: unknown) => path.endsWith('/groups') ? Promise.reject(new Error('Read unavailable')) : original(path, options))
    await w.get('[data-testid="group-name-0"]').setValue('Saved group')
    await w.get('[data-testid="groups-form"]').trigger('submit')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(w.get('[role="status"]').text()).toBe('Saved')
    expect(w.get('[role="alert"]').text()).toContain('Configuration saved, but group/account refresh failed')
    expect(w.get('[role="alert"]').text()).toContain('Read unavailable')
    expect((w.get('[data-testid="group-name-0"]').element as HTMLInputElement).value).toBe('Saved group')
  })

  it('aborts pagination reads on refresh and prevents an old page appending to the new alerts', async () => {
    const original = http.get.getMockImplementation()!
    let finish!: (value: unknown) => void
    let pageSignal!: AbortSignal
    http.get.mockImplementation((path: string, options: { params?: { offset?: number }; signal: AbortSignal }) => {
      if (path.endsWith('/alerts')) {
        if (options.params?.offset === 1) {
          pageSignal = options.signal
          return new Promise(resolve => { finish = resolve })
        }
        return Promise.resolve({ data: { items: [{ id: 'first', account_id: 1, account_name: 'First event', kind: 'balance_low', state: 'pending', attempts: 0, first_seen: '', deliveries: {} }], has_more: true } })
      }
      return original(path, options)
    })
    const w = await render()
    const more = w.findAll('button').find(b => b.text() === 'Load more')!
    await more.trigger('click')
    expect(w.get('[role="status"]').text()).toBe('Loading…')
    await w.get('header button').trigger('click')
    await flushPromises()
    expect(pageSignal.aborted).toBe(true)
    finish({ data: { items: [{ id: 'late', account_id: 1, account_name: 'Late page' }], has_more: false } })
    await flushPromises()
    expect(w.text()).not.toContain('Late page')
    expect(w.text()).toContain('First event')
    expect(w.find('[role="alert"]').exists()).toBe(false)
  })

  it('shows loading and empty groups/accounts while keeping batch submission disabled', async () => {
    config.groups = []
    const original = http.get.getMockImplementation()!
    http.get.mockImplementation((path: string, options: unknown) => path.endsWith('/threshold-accounts') ? Promise.resolve({ data: { items: [] } }) : original(path, options))
    const w = mount(AccountOpsView, { global: { stubs, plugins: [i18n] } })
    await nextTick()
    expect(w.get('[role="status"]').text()).toBe('Loading…')
    await flushPromises()
    expect(w.text()).toContain('No manual groups.')
    expect(w.text()).toContain('No groups or accounts.')
    expect(w.get('[data-testid="save-batch"]').attributes('disabled')).toBeDefined()
    expect(w.find('[role="status"]').exists()).toBe(false)
  })

  it('unmounting during step-up prevents a retry even if verification is later signalled', async () => {
    http.put.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const w = await render()
    await w.get('[data-testid="save-config"]').trigger('click')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    const signals = http.get.mock.calls.map(call => call[1].signal as AbortSignal)
    w.unmount()
    await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(1)
    expect(signals.every(signal => signal.aborted)).toBe(true)
  })

  it('localizes an open confirmation when language changes and does not lose its payload', async () => {
    const w = await render()
    await w.get('[data-testid="batch-accounts-0"]').setValue(['3'])
    await w.get('[data-testid="batch-form"]').trigger('submit')
    expect(w.get('[role="dialog"]').text()).toContain('Apply batch rules')
    i18n.global.locale.value = 'zh'
    await nextTick()
    expect(w.get('[role="dialog"]').text()).toContain('确认将批量规则应用到所选账号')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(http.put.mock.calls[0][1].account_ids).toEqual([3])
  })

  it('renders and operates the real confirmation dialog buttons', async () => {
    const w = mount(AccountOpsView, { global: { plugins: [i18n], stubs: { ...stubs, ConfirmDialog: false, Teleport: true, Transition: false } } })
    await flushPromises()
    await w.get('[data-testid="groups-form"]').trigger('submit')
    const cancel = w.get('[role="dialog"]').findAll('button').find(b => b.text() === 'Cancel')!
    await cancel.trigger('click')
    expect(http.put).not.toHaveBeenCalled()
    await w.get('[data-testid="groups-form"]').trigger('submit')
    const confirm = w.get('[role="dialog"]').findAll('button').find(b => b.text() === 'Confirm')!
    await confirm.trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledWith('/admin/account-ops/groups', { groups: config.groups })
  })

  it('keeps config editable when full save is rejected by admin permissions', async () => {
    http.put.mockRejectedValueOnce({ message: 'Administrator access required', status: 403 })
    const w = await render()
    await w.get('[data-testid="recipient"]').setValue('retry@example.test')
    await w.get('[data-testid="save-config"]').trigger('click')
    await w.get('[data-testid="confirm-change"]').trigger('click')
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('Administrator access required')
    expect(w.find('[role="status"]').exists()).toBe(false)
    expect((w.get('[data-testid="recipient"]').element as HTMLInputElement).value).toBe('retry@example.test')
    expect(w.get('fieldset').attributes('disabled')).toBeUndefined()
  })

  it.each([
    { view: OpsCenterView, enLabel: 'Fresh balances / accounts', zhLabel: '新鲜余额 / 账号', requests: 3 },
    { view: UpstreamMonitorView, enLabel: 'Balances by currency', zhLabel: '币种余额', requests: 4 }
  ])('renders $enLabel with registered locales and cancels its complete load', async ({ view, enLabel, zhLabel, requests }) => {
    const w = mount(view, { global: { stubs, plugins: [i18n] } })
    await flushPromises()
    expect(w.text()).toContain(enLabel)
    i18n.global.locale.value = 'zh'
    await nextTick()
    expect(w.text()).toContain(zhLabel)
    expect(w.text()).not.toContain('admin.accountOps.')
    expect(http.get).toHaveBeenCalledTimes(requests)
    const signals = http.get.mock.calls.map(call => call[1].signal as AbortSignal)
    expect(signals.every(signal => signal === signals[0])).toBe(true)
    w.unmount()
    expect(signals.every(signal => signal.aborted)).toBe(true)
  })
})

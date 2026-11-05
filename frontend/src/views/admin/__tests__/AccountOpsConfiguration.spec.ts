import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import AccountOpsView from '../AccountOpsView.vue'
import en from '@/i18n/locales/en/admin'
import zh from '@/i18n/locales/zh/admin'
import type { AccountOpsConfig } from '@/api/admin/accountOps'

enableAutoUnmount(afterEach)
const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: http }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot/></div>' } }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div/>' } }))
const locale = ref('en')
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale, t: (key: string, params: Record<string, unknown> = {}) => {
  let value: unknown = { admin: locale.value === 'zh' ? zh : en }
  for (const part of key.split('.')) value = (value as Record<string, unknown>)?.[part]
  return String(value ?? key).replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? ''))
} }) }))
const stubs = { AdminPageHeader: { template: '<header><slot name="actions"/></header>' }, RouterLink: { template: '<a><slot/></a>' }, ConfirmDialog: { props: ['show', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" role="dialog">{{ message }}<button data-testid="confirm-change" @click="$emit(\'confirm\')">Confirm</button><button data-testid="cancel-change" @click="$emit(\'cancel\')">Cancel</button></div>' } }
let config: AccountOpsConfig
const account = (id: number) => ({ account_id: id, account_name: `Account ${id}`, platform: 'openai', type: 'apikey', balance: 10, balance_status: 'ok', unit: 'USD', received_at: null, usage_windows: [] })
beforeEach(() => {
  vi.resetAllMocks()
  locale.value = 'en'
  config = { enabled: false, monitor_enabled: false, sample_interval_minutes: 5, recipient: 'operator@example.test', email_name: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60, webhooks: [], groups: [{ id: 'manual-group001', name: 'Existing group', provider: 'sub2api', site: 'https://upstream.example.test', account_ids: [1] }], balance_thresholds: [], quota_thresholds: [] }
  http.get.mockImplementation(async (path: string) => {
    if (path.endsWith('/config')) return { data: { config: structuredClone(config), smtp_configured: true, encryption_key_configured: true, dropped_signals: 0, storage_failures: 0 } }
    if (path.endsWith('/threshold-accounts')) return { data: { items: [account(1), account(2), account(3)] } }
    if (path.endsWith('/groups')) return { data: { groups: [{ ...config.groups![0], default_name: 'Existing group', mode: 'manual' }] } }
    if (path.endsWith('/alerts')) return { data: { items: [], has_more: false } }
    if (path.endsWith('/settings')) return { data: { enabled: true, interval_minutes: 30 } }
    throw new Error(`Unexpected GET ${path}`)
  })
  http.put.mockImplementation(async (path: string, payload: object) => {
    if (path.endsWith('/groups')) Object.assign(config, payload)
    return { data: structuredClone(config) }
  })
})
const render = async () => { const w = mount(AccountOpsView, { global: { stubs } }); await flushPromises(); return w }

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
})

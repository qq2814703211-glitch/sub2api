import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { Account, OpenAIEndpointCapability } from '@/types'

const { create, update, bulkUpdate } = vi.hoisted(() => ({
  create: vi.fn(),
  update: vi.fn(),
  bulkUpdate: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn(), showWarning: vi.fn()
  })
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create, update, bulkUpdate,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      probeUpstreamBilling: vi.fn().mockResolvedValue({}),
      syncUpstreamModels: vi.fn().mockResolvedValue({ models: [], metadata: {} })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([])
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

import CreateAccountModal from '../CreateAccountModal.vue'
import EditAccountModal from '../EditAccountModal.vue'
import BulkEditAccountModal from '../BulkEditAccountModal.vue'

const capabilities: OpenAIEndpointCapability[] = [
  'chat_completions', 'embeddings', 'video_generations', 'seedance'
]
const defaults: OpenAIEndpointCapability[] = ['chat_completions', 'embeddings']
const cases: { name: string; selected: OpenAIEndpointCapability[]; legacy?: boolean }[] = [
  { name: 'only video', selected: ['video_generations'] },
  { name: 'video + Seedance', selected: ['video_generations', 'seedance'] },
  { name: 'chat + video', selected: ['chat_completions', 'video_generations'] },
  { name: 'embeddings + video', selected: ['embeddings', 'video_generations'] },
  { name: 'chat + Seedance', selected: ['chat_completions', 'seedance'] },
  { name: 'embeddings + Seedance', selected: ['embeddings', 'seedance'] },
  { name: 'all capabilities', selected: capabilities },
  { name: 'legacy default', selected: defaults, legacy: true }
]

// Mock persistence: JSON transport plus credential patch semantics; no database.
const wire = <T,>(value: T): T => JSON.parse(JSON.stringify(value))
const stored = new Map<number, Account>()
function fixture(id = 1): Account {
  return {
    id, name: `Synthetic media fixture ${id}`, platform: 'openai', type: 'apikey',
    credentials: { api_key: `synthetic-key-${id}`, base_url: 'https://example.invalid/v1', unrelated: 'keep' },
    extra: {}, concurrency: 1, priority: 1, rate_multiplier: 1, status: 'active',
    group_ids: [], expires_at: null, auto_pause_on_expired: false
  } as Account
}
const global = {
  stubs: {
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    Select: true, ConfirmDialog: true, Icon: true, PlatformIcon: true,
    ProxySelector: true, ProxyAdBanner: true, GroupSelector: true,
    ModelWhitelistSelector: true, QuotaLimitCard: true, OAuthAuthorizationFlow: true
  }
}
function editModal(account: Account) {
  return mount(EditAccountModal, { props: { show: true, account: wire(account), proxies: [], groups: [] }, global })
}
type FormWrapper = ReturnType<typeof editModal>
function assertSelection(wrapper: FormWrapper, selected: OpenAIEndpointCapability[]) {
  for (const capability of capabilities) {
    expect(wrapper.get<HTMLInputElement>(`[data-testid="openai-endpoint-capability-${capability}"]`).element.checked)
      .toBe(selected.includes(capability))
  }
}
async function selectCapabilities(wrapper: FormWrapper, selected: OpenAIEndpointCapability[], bulk = false) {
  const prefix = bulk ? 'bulk-edit-openai-endpoint-capability-' : 'openai-endpoint-capability-'
  // Add before removing so the existing at-least-one selection guard remains active.
  for (const capability of selected) {
    await wrapper.get(`[data-testid="${prefix}${capability}"]`).setValue(true)
  }
  for (const capability of capabilities.filter(value => !selected.includes(value))) {
    await wrapper.get(`[data-testid="${prefix}${capability}"]`).setValue(false)
  }
}
function assertStored(id: number, selected: OpenAIEndpointCapability[], legacy = false) {
  const account = stored.get(id)!
  if (legacy) expect(account.credentials).not.toHaveProperty('openai_capabilities')
  else expect(account.credentials.openai_capabilities).toEqual(selected)
  const reopened = editModal(account)
  assertSelection(reopened, selected)
  reopened.unmount()
}

describe('media endpoint capability mock persistence through real account forms', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stored.clear()
    stored.set(1, fixture(1))
    stored.set(2, fixture(2))
    create.mockImplementation(async payload => {
      const account = wire({ ...fixture(), ...wire(payload), id: 1 })
      stored.set(1, account)
      return wire(account)
    })
    update.mockImplementation(async (id, payload) => {
      const account = wire({ ...stored.get(id), ...wire(payload) })
      stored.set(id, account)
      return wire(account)
    })
    bulkUpdate.mockImplementation(async (ids: number[], payload) => {
      const patch = wire(payload)
      for (const id of ids) {
        const account = stored.get(id)!
        for (const [key, value] of Object.entries(patch.credentials ?? {})) {
          if (value === null) delete account.credentials[key]
          else account.credentials[key] = value
        }
        account.extra = { ...account.extra, ...patch.extra }
        stored.set(id, wire(account))
      }
      return { success: ids.length, failed: 0, results: [] }
    })
  })

  it.each(cases)('create saves and reloads $name', async ({ selected, legacy }) => {
    const wrapper = mount(CreateAccountModal, { props: { show: true, proxies: [], groups: [] }, global })
    await wrapper.findAll('button').find(button => button.text().includes('OpenAI'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('API Key'))!.trigger('click')
    assertSelection(wrapper as unknown as FormWrapper, defaults)
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Synthetic media fixture')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('synthetic-api-key')
    await selectCapabilities(wrapper as unknown as FormWrapper, selected)
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(create).toHaveBeenCalledTimes(1)
    const credentials = wire(create.mock.calls[0]![0]).credentials
    if (legacy) expect(credentials).not.toHaveProperty('openai_capabilities')
    else expect(credentials.openai_capabilities).toEqual(selected)
    assertStored(1, selected, legacy)
    wrapper.unmount()
  })

  it.each(cases)('edit preserves, changes, clears and reloads $name', async ({ selected, legacy }) => {
    const account = stored.get(1)!
    if (!legacy) account.credentials.openai_capabilities = selected
    const wrapper = editModal(account)
    assertSelection(wrapper, selected)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(update).toHaveBeenCalledTimes(1)
    assertStored(1, selected, legacy)
    expect(stored.get(1)!.credentials.unrelated).toBe('keep')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, account: wire(stored.get(1)!) })
    await selectCapabilities(wrapper, defaults)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    assertStored(1, defaults, true)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, account: wire(stored.get(1)!) })
    await selectCapabilities(wrapper, selected)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    assertStored(1, selected, legacy)
    wrapper.unmount()
  })

  it.each(cases)('bulk saves and reloads both accounts with $name', async ({ selected, legacy }) => {
    stored.get(1)!.credentials.openai_capabilities = ['video_generations']
    stored.get(2)!.credentials.openai_capabilities = ['seedance']
    const wrapper = mount(BulkEditAccountModal, {
      props: { show: true, accountIds: [1, 2], selectedPlatforms: ['openai'], selectedTypes: ['apikey'], proxies: [], groups: [] }, global
    })
    await wrapper.get('#bulk-edit-openai-endpoint-capabilities-enabled').setValue(true)
    await selectCapabilities(wrapper as unknown as FormWrapper, selected, true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(bulkUpdate).toHaveBeenCalledTimes(1)
    expect(wire(bulkUpdate.mock.calls[0]![1]).credentials.openai_capabilities).toEqual(legacy ? null : selected)
    for (const id of [1, 2]) {
      assertStored(id, selected, legacy)
      expect(stored.get(id)!.credentials.api_key).toBe(`synthetic-key-${id}`)
      expect(stored.get(id)!.credentials.unrelated).toBe('keep')
    }
    wrapper.unmount()
  })

  it('reads enabled video map entries without enabling false entries', async () => {
    const account = stored.get(1)!
    account.credentials.openai_capabilities = { video_generations: true, seedance: true, chat_completions: false, embeddings: false }
    const wrapper = editModal(account)
    assertSelection(wrapper, ['video_generations', 'seedance'])
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    assertStored(1, ['video_generations', 'seedance'])
    wrapper.unmount()
  })

  it.each([undefined, null, [], {}])('keeps legacy defaults when capabilities are %j', async raw => {
    const account = stored.get(1)!
    account.credentials.openai_capabilities = raw
    const wrapper = editModal(account)
    assertSelection(wrapper, defaults)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    assertStored(1, defaults, true)
    wrapper.unmount()
  })
})

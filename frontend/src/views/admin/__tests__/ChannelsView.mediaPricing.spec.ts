import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelsView from '../ChannelsView.vue'
import type { Channel, ChannelModelPricing } from '@/api/admin/channels'
import { mediaPricingFixture } from '@/__tests__/fixtures/mediaPricing'

const { list, update, create, getGroups, showError } = vi.hoisted(() => ({
  list: vi.fn(), update: vi.fn(), create: vi.fn(), getGroups: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: {
  channels: { list, update, create }, groups: { getAll: getGroups },
  settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

function pricing(): ChannelModelPricing {
  return { platform: 'openai', models: ['media-example'], billing_mode: 'image', input_price: null, output_price: null,
    cache_write_price: null, cache_read_price: null, image_input_price: null, image_output_price: null,
    per_request_price: null, intervals: [], time_pricing: null, media_pricing: mediaPricingFixture }
}
function channel(): Channel {
  return { id: 7, name: 'Media channel', description: '', status: 'active', billing_model_source: 'channel_mapped',
    restrict_models: false, group_ids: [42], model_pricing: [pricing()], model_mapping: {},
    apply_pricing_to_account_stats: true, account_stats_pricing_rules: [{ name: 'Stats', group_ids: [42], account_ids: [], pricing: [pricing()] }], created_at: '', updated_at: '' }
}
// Serialize at the mock API boundary, as axios does; do not share reactive form objects with stored values.
function wire<T>(value: T): T { return JSON.parse(JSON.stringify(value)) }
function mountView() {
  return mount(ChannelsView, { global: { stubs: {
    AppLayout: defineComponent({ template: '<main><slot /></main>' }),
    TablePageLayout: defineComponent({ template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' }),
    BaseDialog: defineComponent({ props: ['show'], template: '<section v-if="show"><slot /><slot name="footer" /></section>' }),
    DataTable: defineComponent({ props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' }),
    Select: true, Icon: true, PlatformIcon: true, Toggle: true, Pagination: true, ConfirmDialog: true, EmptyState: true,
  } } })
}
async function edit(wrapper: ReturnType<typeof mountView>) {
  await flushPromises()
  await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
  await flushPromises()
}

describe('ChannelsView media rule persistence (mock API)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    getGroups.mockResolvedValue([{ id: 42, name: 'Media group', platform: 'openai', status: 'active' }])
  })

  it('roundtrips, edits and clears independent channel and account-stats media pricing', async () => {
    let stored = wire(channel())
    list.mockImplementation(async () => ({ items: [wire(stored)], total: 1 }))
    update.mockImplementation(async (_id, request) => { stored = wire({ ...stored, ...request }); return wire(stored) })
    const wrapper = mountView()
    try {
      await edit(wrapper)
      const editors = () => wrapper.findAll<HTMLTextAreaElement>('[data-testid="media-pricing-json"]')
      expect(editors()).toHaveLength(2)
      expect(editors().map(e => JSON.parse(e.element.value))).toEqual([mediaPricingFixture, mediaPricingFixture])
      const channelRules = wire(mediaPricingFixture)
      channelRules.rules[0].price = 0.25
      const statsRules = wire(mediaPricingFixture)
      statsRules.rules[4].image_output_price = 0.00002
      await editors()[0].setValue(JSON.stringify(channelRules))
      await editors()[1].setValue(JSON.stringify(statsRules))
      await wrapper.get('#channel-form').trigger('submit')
      await flushPromises()
      expect(showError).not.toHaveBeenCalled()
      expect(update).toHaveBeenCalledOnce()
      expect(stored.model_pricing[0].media_pricing).toEqual(channelRules)
      expect(stored.account_stats_pricing_rules[0].pricing[0].media_pricing).toEqual(statsRules)
      expect(stored.model_pricing[0].per_request_price).toBeNull()
      await edit(wrapper)
      expect(editors().map(e => JSON.parse(e.element.value))).toEqual([channelRules, statsRules])
      await editors()[1].setValue('{')
      await wrapper.get('#channel-form').trigger('submit')
      expect(update).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenLastCalledWith(expect.stringContaining('mediaPricing.validation.json'))
      await editors()[0].setValue('null')
      await editors()[1].setValue('null')
      // Clearing media rules restores the legacy image-card price requirement.
      await wrapper.get('#channel-form').trigger('submit')
      expect(update).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenLastCalledWith('admin.channels.form.perRequestPriceRequired')
      const cards = wrapper.findAllComponents({ name: 'PricingEntryCard' })
      await cards[0].get('input[type="number"]').setValue('0.5')
      await wrapper.get('#channel-form').trigger('submit')
      await flushPromises()
      expect(update).toHaveBeenCalledTimes(2)
      expect(stored.model_pricing[0].media_pricing).toBeNull()
      expect(stored.account_stats_pricing_rules[0].pricing[0].media_pricing).toBeNull()
      await edit(wrapper)
      expect(editors().map(e => e.element.value)).toEqual(['', ''])
    } finally { wrapper.unmount() }
  })

  it('creates and reloads media-only image pricing without requiring a scalar price', async () => {
    let stored: Channel | null = null
    list.mockImplementation(async () => ({ items: stored ? [wire(stored)] : [], total: stored ? 1 : 0 }))
    create.mockImplementation(async request => { stored = wire({ ...channel(), ...request }); return wire(stored) })
    const wrapper = mountView()
    try {
      await flushPromises()
      await wrapper.findAll('button').find(b => b.text() === 'admin.channels.createChannel')!.trigger('click')
      await flushPromises()
      await wrapper.get('input[placeholder="admin.channels.form.namePlaceholder"]').setValue('New media channel')
      await wrapper.findAll('label').find(l => l.text() === 'admin.groups.platforms.openai')!.get('input').setValue(true)
      await wrapper.findAll('button.channel-tab').find(b => b.text() === 'admin.groups.platforms.openai')!.trigger('click')
      const groupLabel = wrapper.findAll('label').find(l => l.text().includes('Media group'))!
      await groupLabel.get('input[type="checkbox"]').setValue(true)
      const pricingLabel = wrapper.findAll('label').find(l => l.text() === 'admin.channels.form.modelPricing')!
      const pricingSection = wrapper.findAll('div').find(d => d.element === pricingLabel.element.parentElement?.parentElement)!
      await pricingSection.findAll('button').find(b => b.text() === '+ common.add')!.trigger('click')
      const card = wrapper.findComponent({ name: 'PricingEntryCard' })
      // Exercise the real card update listener with the same form entry the editor emits.
      card.vm.$emit('update', { ...card.props('entry'), models: ['media-example'], billing_mode: 'image', media_pricing: JSON.stringify(mediaPricingFixture) })
      await wrapper.vm.$nextTick()
      await wrapper.findAll('button').find(b => b.text() === '+ admin.channels.form.addRule')!.trigger('click')
      await wrapper.get('input[placeholder="admin.channels.form.ruleName"]').setValue('Stats media')
      const statsLabel = wrapper.findAll('label').find(l => l.text() === 'admin.channels.form.ruleModelPricing')!
      const statsSection = wrapper.findAll('div').find(d => d.element === statsLabel.element.parentElement?.parentElement)!
      await statsSection.findAll('button').find(b => b.text() === '+ common.add')!.trigger('click')
      const statsCard = wrapper.findAllComponents({ name: 'PricingEntryCard' })[1]
      expect(statsCard.props('entry').media_pricing).toBeNull()
      statsCard.vm.$emit('update', { ...statsCard.props('entry'), models: ['media-example'], billing_mode: 'video' })
      await wrapper.vm.$nextTick()
      const editors = wrapper.findAll<HTMLTextAreaElement>('[data-testid="media-pricing-json"]')
      await editors[1].setValue(JSON.stringify(mediaPricingFixture))
      await wrapper.get('#channel-form').trigger('submit')
      await flushPromises()
      expect(showError).not.toHaveBeenCalled()
      expect(create).toHaveBeenCalledWith(expect.objectContaining({
        model_pricing: [expect.objectContaining({ media_pricing: mediaPricingFixture, per_request_price: null })],
        account_stats_pricing_rules: [expect.objectContaining({ pricing: [expect.objectContaining({ media_pricing: mediaPricingFixture })] })],
      }))
      await edit(wrapper)
      expect(wrapper.findAll<HTMLTextAreaElement>('[data-testid="media-pricing-json"]').map(e => JSON.parse(e.element.value))).toEqual([mediaPricingFixture, mediaPricingFixture])
    } finally { wrapper.unmount() }
  })
})

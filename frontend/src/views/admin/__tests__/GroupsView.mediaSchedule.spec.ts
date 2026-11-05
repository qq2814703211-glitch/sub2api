import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'

const {
  listGroups,
  duplicateGroup,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  duplicateGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

const authState = vi.hoisted(() => ({ isSimpleMode: false }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      duplicate: duplicateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getAll: vi.fn(),
      create: vi.fn(),
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const sourceGroup: AdminGroup = {
  id: 42,
  name: 'Primary',
  description: null,
  platform: 'openai',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-16T00:00:00Z',
  updated_at: '2026-07-16T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 1,
  active_account_count: 1,
  rate_limited_account_count: 0,
  model_allowlist: undefined,
  sort_order: 10
}

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>'
})

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
    columns: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false }
  },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>'
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true
      }
    }
  })
}

describe('independent group saved media schedule review',()=>{
 beforeEach(()=>{
  vi.clearAllMocks();localStorage.clear();
  getModelAllowlistCandidates.mockResolvedValue([]);getUsageSummary.mockResolvedValue([]);getCapacitySummary.mockResolvedValue([]);getLiveCapability.mockResolvedValue({supported:false});
 })
 it.each(['image','video','per_request'] as const)('saves a valid existing %s media card with its persisted schedule',async mode=>{
  const fixtureRoot = process.env.PLAZA_FIXTURE_DIR || process.cwd()+'/src/__tests__/fixtures/plaza'
  const actual=JSON.parse(readFileSync(fixtureRoot+'/ui-scheduled-group-http-response.json','utf8')).data
  const stored={...sourceGroup,...actual,model_pricing:actual.model_pricing.map((p:any)=>({...p,billing_mode:mode}))}
  const media=stored.model_pricing[0].media_pricing
  listGroups.mockResolvedValue({items:[stored],total:1,page:1,page_size:20,pages:1})
  updateGroup.mockImplementation(async (_id, payload) => {
    const saved = JSON.parse(JSON.stringify(payload))
    Object.assign(stored, saved)
    return JSON.parse(JSON.stringify(stored))
  })
  const wrapper=mountView()
  try{
   await flushPromises()
   await wrapper.findAll('button').find(b=>b.text()==='common.edit')!.trigger('click')
   await flushPromises()
   expect(JSON.parse(wrapper.get<HTMLTextAreaElement>('[data-testid="media-pricing-json"]').element.value)).toEqual(media)
   await wrapper.get('#edit-group-form input[type="text"]').setValue('Changed description only')
   await wrapper.get('#edit-group-form').trigger('submit')
   await flushPromises()
   console.log('GROUP_SAVED_SCHEDULE',JSON.stringify({mode,apiCalls:updateGroup.mock.calls.length,errors:showError.mock.calls}))
   expect(updateGroup).toHaveBeenCalledOnce()
   expect(showError).not.toHaveBeenCalled()
   const payload = updateGroup.mock.calls[0]![1]
   expect(payload.model_pricing[0].media_pricing).toEqual(media)
   expect(payload.model_pricing[0].time_pricing).toEqual({weekdays_only:false,...actual.model_pricing[0].time_pricing})
   if (process.env.PLAZA_FIXTURE_DIR) writeFileSync(fixtureRoot+'/ui-save-'+mode+'.json', JSON.stringify(payload))
   // The first pass reopens JSON transport state. A second pass reads the real
   // HTTP PUT -> database -> GET response produced using this exact payload.
   const reloadPath = fixtureRoot+'/ui-reloaded-'+mode+'.json'
   if (process.env.PLAZA_FIXTURE_DIR && existsSync(reloadPath)) {
     Object.assign(stored, JSON.parse(readFileSync(reloadPath,'utf8')).data)
   }
   listGroups.mockResolvedValue({items:[stored],total:1,page:1,page_size:20,pages:1})
   await wrapper.findAll('button').find(b=>b.text()==='common.edit')!.trigger('click')
   await flushPromises()
   expect(JSON.parse(wrapper.get<HTMLTextAreaElement>('[data-testid="media-pricing-json"]').element.value)).toEqual(media)
   const card = wrapper.findComponent(PricingEntryCard)
   expect(Number(card.props('entry').time_pricing.periods[0].multiplier)).toBe(3)
   expect(card.findComponent({name:'TimePricingSection'}).exists()).toBe(true)

  }finally{wrapper.unmount()}
 })
})

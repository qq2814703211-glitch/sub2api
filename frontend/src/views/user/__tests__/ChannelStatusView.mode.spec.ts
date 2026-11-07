import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const isV1 = vi.fn(() => false)
const isPassive = vi.fn(() => false)
const getV3Status = vi.fn(async () => ({ enabled: true }))

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorV1Mode: () => isV1(),
  isChannelMonitorPassiveMode: () => isPassive(),
}))

// V3's view is chosen by V3's own switch, so the shell asks the V3 status endpoint
// rather than reading the V1/V2 mode setting (ADR 0008).
vi.mock('@/api/channelMonitorV3', () => ({
  getV3Status: () => getV3Status(),
}))

vi.mock('../ChannelStatusV1View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV1View', setup: () => () => h('div', { 'data-testid': 'v1' }) }),
}))
vi.mock('../ChannelStatusV2View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV2View', setup: () => () => h('div', { 'data-testid': 'v2' }) }),
}))
vi.mock('../ChannelStatusV3View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV3View', setup: () => () => h('div', { 'data-testid': 'v3' }) }),
}))

import ChannelStatusView from '../ChannelStatusView.vue'

/** Set the query string the way a browser navigation would. */
function setSearch(search: string) {
  window.history.replaceState({}, '', search ? `/?${search}` : '/')
}

async function mountResolved() {
  const wrapper = mount(ChannelStatusView)
  await flushPromises()
  return wrapper
}

describe('ChannelStatusView mode switch', () => {
  beforeEach(() => {
    isV1.mockReset()
    isPassive.mockReset()
    getV3Status.mockReset()
    isV1.mockReturnValue(false)
    isPassive.mockReturnValue(false)
    getV3Status.mockResolvedValue({ enabled: true })
    setSearch('')
  })

  it('renders V1 when in v1 mode without asking V3', async () => {
    isV1.mockReturnValue(true)
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(false)
    expect(getV3Status).not.toHaveBeenCalled()
  })

  it('renders V3 when V3 has its own switch on', async () => {
    isPassive.mockReturnValue(true)
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
  })

  it('renders V3 under the v3 hybrid mode too', async () => {
    // Both v2 and v3 ride the passive stack; only v1 is excluded.
    isPassive.mockReturnValue(true)
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
  })

  // The important new behaviour: the old shell chose V3 from the V1/V2 mode. Now a
  // deployment that never enabled V3 must not render a board that would 403.
  it('falls back to V2 when V3 turns its own switch off', async () => {
    isPassive.mockReturnValue(true)
    getV3Status.mockResolvedValue({ enabled: false })
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(false)
  })

  it('falls back to V2 when the V3 status endpoint is unavailable', async () => {
    isPassive.mockReturnValue(true)
    getV3Status.mockRejectedValue(new Error('network down'))
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(false)
  })

  it('renders the legacy V2 view when ?monitor_view=v2 is set', async () => {
    isPassive.mockReturnValue(true)
    setSearch('monitor_view=v2')
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(false)
  })

  it('keeps ?monitor_view=v2 working under the v3 hybrid', async () => {
    // Regression guard: v3 must not fall back to the plain V1 view nor break
    // the legacy rollback surface.
    isPassive.mockReturnValue(true)
    setSearch('monitor_view=v2')
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
  })

  it('ignores ?monitor_view=v2 when the passive stack is disabled and V3 is on', async () => {
    setSearch('monitor_view=v2')
    const wrapper = await mountResolved()
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })
})

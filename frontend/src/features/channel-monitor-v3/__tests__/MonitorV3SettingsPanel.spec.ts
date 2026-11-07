import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import MonitorV3SettingsPanel from '../MonitorV3SettingsPanel.vue'
import type { ChannelMonitorV3Config } from '@/api/channelMonitorV3'

/**
 * V3 的配置面板现在挂在 /admin/channels/monitor 的第三个页签上。它必须
 *   - 不受 V1/V2 的系统模式与 feature flag 影响（V3 只认自己的开关）；
 *   - V3 自己的 enabled=false 时渲染完整表单（能改、能存），而不是空白/报错；
 *   - 保存只写 channel_monitor_v3_config 的字段。
 */

const mocks = vi.hoisted(() => ({
  getV3Config: vi.fn(),
  listV3Groups: vi.fn(),
  updateV3Config: vi.fn(),
  probeV3Now: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/channelMonitorV3', () => ({
  getV3Config: mocks.getV3Config,
  listV3Groups: mocks.listV3Groups,
  updateV3Config: mocks.updateV3Config,
  probeV3Now: mocks.probeV3Now,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) }),
}))

const GROUP_ROWS = [
  { id: 7, name: 'claude-main', platform: 'anthropic', status: 'active' },
  { id: 9, name: 'gpt-main', platform: 'openai', status: 'active' },
]

function config(overrides: Partial<ChannelMonitorV3Config> = {}): ChannelMonitorV3Config {
  return {
    version: 1,
    enabled: true,
    cache_average_window_days: 30,
    groups: [{ group_id: 7, enabled: true, models: [], probe_enabled: true }],
    probe: {
      endpoint: 'https://api.example.com',
      provider: 'anthropic',
      primary_model: 'claude-sonnet-4',
      extra_models: ['claude-haiku'],
      interval_seconds: 60,
    },
    api_key_set: true,
    ...overrides,
  }
}

async function mountPanel(value: ChannelMonitorV3Config) {
  mocks.getV3Config.mockResolvedValue(value)
  mocks.listV3Groups.mockResolvedValue(GROUP_ROWS)
  const wrapper = mount(MonitorV3SettingsPanel, { global: { stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}

describe('MonitorV3SettingsPanel', () => {
  beforeEach(() => {
    for (const fn of Object.values(mocks)) fn.mockReset()
    mocks.updateV3Config.mockImplementation(async (payload) => config(payload))
  })

  it('loads V3 config and its own group candidates', async () => {
    const wrapper = await mountPanel(config())

    expect(mocks.getV3Config).toHaveBeenCalledTimes(1)
    expect(mocks.listV3Groups).toHaveBeenCalledTimes(1)
    // 配置里勾选的 7 号 + 未勾选的 9 号都要出现（勾选状态由开关表达）。
    const text = wrapper.text()
    expect(text).toContain('claude-main')
    expect(text).toContain('gpt-main')
    expect(text).toContain('channelMonitorV3.admin.groupsSelected:{"selected":1,"total":2}')
    wrapper.unmount()
  })

  it('still renders a fully editable form when V3 is disabled', async () => {
    const wrapper = await mountPanel(config({ enabled: false }))

    // 不是空白页：开关、缓存窗口、探针、分组四块都在。
    expect(wrapper.text()).toContain('channelMonitorV3.admin.enableTitle')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.cacheWindowTitle')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.probeTitle')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.groupsTitle')
    expect(wrapper.find('input[type="url"]').exists()).toBe(true)
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)

    // 关闭状态下「立即探测」不可点，但「保存」在改动后仍可用。
    const probeButton = wrapper.findAll('button').find((b) => b.text().includes('channelMonitorV3.admin.probeNow'))!
    expect(probeButton.attributes('disabled')).toBeDefined()

    const enableSwitch = wrapper.find('[role="switch"]')
    await enableSwitch.trigger('click')
    await flushPromises()
    const saveButton = wrapper.findAll('button').find((b) => b.text().includes('channelMonitorV3.admin.save'))!
    expect(saveButton.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('saves only V3 config fields, even when the global V1/V2 mode is not v3', async () => {
    // 这个面板不读任何 V1/V2 的 feature flag / mode：它的输入只有 V3 自己的接口。
    const wrapper = await mountPanel(config({ enabled: false }))

    await wrapper.find('[role="switch"]').trigger('click')
    const saveButton = wrapper.findAll('button').find((b) => b.text().includes('channelMonitorV3.admin.save'))!
    await saveButton.trigger('click')
    await flushPromises()

    expect(mocks.updateV3Config).toHaveBeenCalledTimes(1)
    const payload = mocks.updateV3Config.mock.calls[0][0] as Record<string, unknown>
    expect(payload.enabled).toBe(true)
    expect(payload.cache_average_window_days).toBe(30)
    expect(payload.probe).toMatchObject({
      endpoint: 'https://api.example.com',
      provider: 'anthropic',
      primary_model: 'claude-sonnet-4',
      extra_models: ['claude-haiku'],
      interval_seconds: 60,
    })
    expect(payload.groups).toEqual([
      { group_id: 7, enabled: true, models: [], probe_enabled: true },
    ])
    expect(mocks.showSuccess).toHaveBeenCalledWith('channelMonitorV3.admin.saveSuccess')
    wrapper.unmount()
  })

  it('surfaces a load error instead of rendering an empty page', async () => {
    mocks.getV3Config.mockRejectedValue(new Error('boom'))
    mocks.listV3Groups.mockResolvedValue(GROUP_ROWS)
    const wrapper = mount(MonitorV3SettingsPanel, { global: { stubs: { teleport: true } } })
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalled()
    wrapper.unmount()
  })
})

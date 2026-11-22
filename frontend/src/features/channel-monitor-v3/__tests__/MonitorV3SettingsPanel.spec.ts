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

// 候选列表只含有效分组（后端已按 deleted_at IS NULL 过滤）。
const GROUP_ROWS = [
  { id: 7, name: 'claude-main', platform: 'anthropic', status: 'active' },
  { id: 9, name: 'gpt-main', platform: 'openai', status: 'active' },
]

function config(overrides: Partial<ChannelMonitorV3Config> = {}): ChannelMonitorV3Config {
  return {
    version: 1,
    enabled: true,
    cache_average_window_days: 30,
    cache_average_window_value: 30,
    cache_average_window_unit: 'day',
    groups: [{ group_id: 7, enabled: true, models: [], probe_enabled: true,
      probe: { endpoint: 'https://api.example.com', provider: 'anthropic', primary_model: 'claude-sonnet-4', extra_models: ['claude-haiku'], interval_seconds: 60 },
      api_key_set: true }],
    ...overrides,
  }
}

async function mountPanel(value: ChannelMonitorV3Config, rows = GROUP_ROWS) {
  mocks.getV3Config.mockResolvedValue(value)
  mocks.listV3Groups.mockResolvedValue(rows)
  const wrapper = mount(MonitorV3SettingsPanel, { global: { stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}

function saveButton(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('button').find((b) => b.text().includes('channelMonitorV3.admin.save'))!
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

    expect(wrapper.text()).toContain('channelMonitorV3.admin.enableTitle')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.cacheWindowTitle')
    expect(wrapper.text()).not.toContain('channelMonitorV3.admin.probeTitle')
    await wrapper.get('[data-group-probe-toggle="7"]').trigger('click')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.groupsTitle')
    expect(wrapper.find('input[type="url"]').exists()).toBe(true)
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)

    // 关闭状态下「立即探测」不可点，但「保存」在改动后仍可用。
    const probeButton = wrapper.findAll('button').find((b) => b.text().includes('channelMonitorV3.admin.probeNow'))!
    expect(probeButton.attributes('disabled')).toBeDefined()

    const enableSwitch = wrapper.find('[role="switch"]')
    await enableSwitch.trigger('click')
    await flushPromises()
    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('saves only V3 config fields, even when the global V1/V2 mode is not v3', async () => {
    // 这个面板不读任何 V1/V2 的 feature flag / mode：它的输入只有 V3 自己的接口。
    const wrapper = await mountPanel(config({ enabled: false }))

    await wrapper.find('[role="switch"]').trigger('click')
    await saveButton(wrapper).trigger('click')
    await flushPromises()

    expect(mocks.updateV3Config).toHaveBeenCalledTimes(1)
    const payload = mocks.updateV3Config.mock.calls[0][0] as Record<string, unknown>
    expect(payload.enabled).toBe(true)
    expect(payload.cache_average_window_days).toBe(30)
    expect(payload.cache_average_window_value).toBe(30)
    expect(payload.cache_average_window_unit).toBe('day')
    expect(payload).not.toHaveProperty('probe')
    expect(payload).not.toHaveProperty('api_key')
    expect(payload.groups).toEqual([
      expect.objectContaining({ group_id: 7, enabled: true, probe_enabled: true }),
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

  // ── L1-1：残留的已删分组 ──────────────────────────────────────────────────

  it('keeps a stale entry for a deleted group, labelled and impossible to enable', async () => {
    // 配置里还挂着 42 号分组，但它已不在候选里（后端已过滤掉），且配置里是勾选状态。
    const wrapper = await mountPanel(
      config({
        groups: [
          config().groups[0],
          { group_id: 42, enabled: true, models: [], probe_enabled: true },
        ],
      }),
    )

    const stale = wrapper.find('[data-group-id="42"]')
    expect(stale.exists()).toBe(true)
    expect(stale.attributes('data-deleted')).toBe('true')
    // 名字带分组 id，多个已删分组不会都显示成同一行「已删除分组」。
    expect(stale.text()).toContain('channelMonitorV3.admin.deletedGroupNamed')
    expect(stale.text()).toContain('42')
    expect(stale.text()).toContain('channelMonitorV3.admin.deletedGroupHint')
    // 已删分组不能被勾选：没有 Toggle，只有一个危险色图标占位。
    expect(stale.find('[role="switch"]').exists()).toBe(false)
    // 有独立的「移除」按钮。
    expect(stale.find('[data-group-remove="42"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('drops a stale deleted-group entry from the saved config', async () => {
    const wrapper = await mountPanel(
      config({
        groups: [
          config().groups[0],
          { group_id: 42, enabled: true, models: [], probe_enabled: true },
        ],
      }),
    )

    // 勾选状态让它变脏，才能点保存。
    await wrapper.find('[role="switch"]').trigger('click')
    await saveButton(wrapper).trigger('click')
    await flushPromises()

    const payload = mocks.updateV3Config.mock.calls[0][0] as { groups: Array<{ group_id: number }> }
    expect(payload.groups.map((g) => g.group_id)).toEqual([7])
    wrapper.unmount()
  })

  it('lets the operator remove a stale entry explicitly', async () => {
    const wrapper = await mountPanel(
      config({
        groups: [
          config().groups[0],
          { group_id: 42, enabled: true, models: [], probe_enabled: true },
        ],
      }),
    )

    await wrapper.find('[data-group-remove="42"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-group-id="42"]').exists()).toBe(false)
    // 仍勾选的分组不受影响。
    expect(wrapper.find('[data-group-id="7"]').exists()).toBe(true)
    // 移除动作本身就该让表单变脏，否则点不了保存。
    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('distinguishes multiple deleted groups from each other', async () => {
    const wrapper = await mountPanel(
      config({
        groups: [
          config().groups[0],
          { group_id: 42, enabled: true, models: [], probe_enabled: true },
          { group_id: 43, enabled: true, models: [], probe_enabled: true },
        ],
      }),
    )

    const staleRows = wrapper.findAll('[data-deleted="true"]')
    expect(staleRows).toHaveLength(2)
    expect(staleRows[0].text()).toContain('42')
    expect(staleRows[1].text()).toContain('43')
    expect(staleRows[0].text()).not.toBe(staleRows[1].text())
    wrapper.unmount()
  })

  // ── L1-2：每组独立探针 ────────────────────────────────────────────────────

  it('opens the affected group when an active probe is missing', async () => {
    const wrapper = await mountPanel(config())
    await wrapper.get('[data-group-id="9"] [role="switch"]').trigger('click')
    await saveButton(wrapper).trigger('click')
    expect(mocks.updateV3Config).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith(expect.stringContaining('gpt-main'))
    expect(wrapper.get('[data-group-probe-panel="9"] input[type="url"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('saves a key-only rotation for the selected group', async () => {
    const wrapper = await mountPanel(config())
    await wrapper.get('[data-group-probe-toggle="7"]').trigger('click')
    await wrapper.get('input[type="password"]').setValue('rotated')
    await saveButton(wrapper).trigger('click')
    expect(mocks.updateV3Config).toHaveBeenCalledWith(expect.objectContaining({ group_api_keys: { '7': 'rotated' } }))
    wrapper.unmount()
  })

  it('lets each group configure its own probe identity and key', async () => {
    const wrapper = await mountPanel(config())

    // 只有勾选的分组才会写进配置，先启用 9 号。
    await wrapper.find('[data-group-id="9"]').find('[role="switch"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-group-probe-toggle="9"]').trigger('click')
    await flushPromises()

    const panel = wrapper.find('[data-group-probe-panel="9"]')
    expect(panel.exists()).toBe(true)

    const seededInputs = panel.findAll('input')
    const seededEndpoint = panel.get('input[type="url"]')
    expect((seededEndpoint.element as HTMLInputElement).value).toBe('')
    await panel.get('input[placeholder="claude-sonnet-4"]').setValue('gpt-test')
    await seededEndpoint.setValue('https://gpt.example.com')
    const passwordInput = seededInputs.find((i) => i.attributes('type') === 'password')!
    await passwordInput.setValue('gpt-group-key')
    await flushPromises()

    await saveButton(wrapper).trigger('click')
    await flushPromises()

    const payload = mocks.updateV3Config.mock.calls[0][0] as {
      groups: Array<{ group_id: number; probe: { endpoint: string } }>
      group_api_keys: Record<string, string>
    }
    const group9 = payload.groups.find((g) => g.group_id === 9)!
    expect(group9.probe.endpoint).toBe('https://gpt.example.com')
    // 分组 key 走单独的 map，绝不混进组对象里回传。
    expect(payload.group_api_keys).toEqual({ '9': 'gpt-group-key' })
    wrapper.unmount()
  })

  it('saves a group model override independently of its primary model', async () => {
    const wrapper = await mountPanel(config())

    await wrapper.find('[data-group-probe-toggle="7"]').trigger('click')
    await flushPromises()

    const panel = wrapper.find('[data-group-probe-panel="7"]')
    const modelsInput = panel
      .findAll('input')
      .find((i) => i.attributes('placeholder') === 'channelMonitorV3.admin.groupModelsPlaceholder')!
    await modelsInput.setValue('claude-opus-4, claude-haiku')
    await flushPromises()

    await saveButton(wrapper).trigger('click')
    await flushPromises()

    const payload = mocks.updateV3Config.mock.calls[0][0] as {
      groups: Array<{ group_id: number; models: string[] }>
    }
    expect(payload.groups.find((g) => g.group_id === 7)!.models).toEqual([
      'claude-opus-4',
      'claude-haiku',
    ])
    wrapper.unmount()
  })

  // ── L1-3：窗口单位可选分钟 / 天 ───────────────────────────────────────────

  it('renders a unit selector next to the cache window value', async () => {
    const wrapper = await mountPanel(config())

    // 数值输入 + 单位下拉（Select 组件的 trigger）。
    const numberInput = wrapper
      .findAll('input[type="number"]')
      .find((i) => i.attributes('aria-label') === 'channelMonitorV3.admin.cacheWindowTitle')!
    expect(numberInput.exists()).toBe(true)
    expect((numberInput.element as HTMLInputElement).value).toBe('30')
    expect(wrapper.text()).toContain('channelMonitorV3.admin.windowRangeHint')
    wrapper.unmount()
  })

  it('saves a minute window as value + unit so the backend can hit minute-granularity SQL', async () => {
    const wrapper = await mountPanel(config())

    const numberInput = wrapper
      .findAll('input[type="number"]')
      .find((i) => i.attributes('aria-label') === 'channelMonitorV3.admin.cacheWindowTitle')!
    await numberInput.setValue('30')

    // 切单位：数值不被静默重置。
    const unitSelect = wrapper.findComponent({ name: 'Select' })
    unitSelect.vm.$emit('update:modelValue', 'minute')
    await flushPromises()

    expect((numberInput.element as HTMLInputElement).value).toBe('30')

    await saveButton(wrapper).trigger('click')
    await flushPromises()

    const payload = mocks.updateV3Config.mock.calls[0][0] as Record<string, unknown>
    expect(payload.cache_average_window_value).toBe(30)
    expect(payload.cache_average_window_unit).toBe('minute')
    wrapper.unmount()
  })

  it('rejects a window above the physical limit instead of saving it', async () => {
    const wrapper = await mountPanel(config())

    const numberInput = wrapper
      .findAll('input[type="number"]')
      .find((i) => i.attributes('aria-label') === 'channelMonitorV3.admin.cacheWindowTitle')!
    // 天单位上限 90。
    await numberInput.setValue('91')
    await flushPromises()

    expect(wrapper.text()).toContain('channelMonitorV3.admin.windowInvalid')
    await saveButton(wrapper).trigger('click')
    await flushPromises()
    expect(mocks.updateV3Config).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('accepts a minute window up to 129600 and rejects one above it', async () => {
    const wrapper = await mountPanel(config())
    const unitSelect = wrapper.findComponent({ name: 'Select' })
    unitSelect.vm.$emit('update:modelValue', 'minute')
    await flushPromises()

    const numberInput = wrapper
      .findAll('input[type="number"]')
      .find((i) => i.attributes('aria-label') === 'channelMonitorV3.admin.cacheWindowTitle')!

    await numberInput.setValue('129600')
    await flushPromises()
    expect(wrapper.text()).not.toContain('channelMonitorV3.admin.windowInvalid')

    await numberInput.setValue('129601')
    await flushPromises()
    expect(wrapper.text()).toContain('channelMonitorV3.admin.windowInvalid')
    wrapper.unmount()
  })

  it('keeps the legacy days field consistent when a minute window is loaded', async () => {
    // 老配置只有 days（没有 value/unit）：UI 回落到「天」单位并按天解释。
    const wrapper = await mountPanel(config({
      cache_average_window_days: 14,
      cache_average_window_value: undefined,
      cache_average_window_unit: undefined,
    }))

    const numberInput = wrapper
      .findAll('input[type="number"]')
      .find((i) => i.attributes('aria-label') === 'channelMonitorV3.admin.cacheWindowTitle')!
    expect((numberInput.element as HTMLInputElement).value).toBe('14')
    wrapper.unmount()
  })
})

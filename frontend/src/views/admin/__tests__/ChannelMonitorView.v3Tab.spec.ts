import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelMonitorView from '@/views/admin/ChannelMonitorView.vue'

/**
 * V3 的配置界面已并入 /admin/channels/monitor，作为第三个页签与 V1/V2 并排。
 * 这里锁两件事：
 *   1. 页签顺序/文案由 V1、V2、V3 共用同一套 tabs chrome；
 *   2. 旧路径 /admin/channel-monitor-v3 通过 ?tab=v3 深链进来时默认选中 V3。
 */

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, string | string[] | undefined>,
}))
const routerReplace = vi.hoisted(() => vi.fn())

const { listMonitors } = vi.hoisted(() => ({ listMonitors: vi.fn() }))

const { mode } = vi.hoisted(() => ({ mode: { value: 'v1' as 'v1' | 'v2' | 'v3', enabled: true } }))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
  useRouter: () => ({ replace: routerReplace }),
}))

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorRouteEnabled: () => mode.enabled,
  isChannelMonitorV1Mode: () => mode.enabled && mode.value === 'v1',
  isChannelMonitorV2Mode: () => mode.enabled && mode.value === 'v2',
  isChannelMonitorPassiveMode: () => mode.enabled && mode.value !== 'v1',
  isChannelMonitorHybridMode: () => mode.enabled && mode.value === 'v3',
  getChannelMonitorMode: () => mode.value,
}))

vi.mock('@/features/channel-monitor-v2/MonitorSettingsPanel.vue', () => ({
  default: { name: 'MonitorSettingsPanel', template: '<div data-testid="v2-settings" />' },
}))

vi.mock('@/features/channel-monitor-v3/MonitorV3SettingsPanel.vue', () => ({
  default: { name: 'MonitorV3SettingsPanel', template: '<div data-testid="v3-settings" />' },
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channelMonitor: {
      list: listMonitors,
      duplicate: vi.fn(),
      update: vi.fn(),
      runNow: vi.fn(),
      del: vi.fn(),
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
})

function mountView() {
  return mount(ChannelMonitorView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: true,
        MonitorFiltersBar: true,
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        HelpTooltip: true,
        Toggle: true,
        MonitorFormDialog: true,
        MonitorTemplateManagerDialog: true,
        MonitorRunResultDialog: true,
        MonitorPrimaryModelCell: true,
      },
    },
  })
}

function tabLabels(wrapper: ReturnType<typeof mountView>): string[] {
  return wrapper.findAll('[role="tab"]').map((tab) => tab.text())
}

describe('ChannelMonitorView V3 tab', () => {
  beforeEach(() => {
    localStorage.clear()
    routeState.query = {}
    routerReplace.mockReset()
    listMonitors.mockReset()
    listMonitors.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    mode.value = 'v1'
    mode.enabled = true
  })

  it('renders V1, V2 and V3 in one consistent tab strip', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(tabLabels(wrapper)).toEqual([
      'channelMonitorV2.admin.tabV2',
      // v1 模式下 V1 页签写「主动探测」，否则写「历史」
      'channelMonitorV2.admin.tabV1Active',
      'channelMonitorV2.admin.tabV3',
    ])
    wrapper.unmount()
  })

  it('mounts the shared V3 settings panel when the V3 tab is selected', async () => {
    const wrapper = mountView()
    await flushPromises()

    const v3Tab = wrapper.findAll('[role="tab"]').find((tab) => tab.text() === 'channelMonitorV2.admin.tabV3')!
    await v3Tab.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="v3-settings"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2-settings"]').exists()).toBe(false)
    expect(routerReplace).toHaveBeenCalledWith({ query: { tab: 'v3' } })
    wrapper.unmount()
  })

  it('opens on the V3 tab when the retired route redirects in with ?tab=v3', async () => {
    routeState.query = { tab: 'v3' }
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="v3-settings"]').exists()).toBe(true)
    const active = wrapper.findAll('[role="tab"]').filter((tab) => tab.classes().includes('tab-active'))
    expect(active).toHaveLength(1)
    expect(active[0].text()).toBe('channelMonitorV2.admin.tabV3')
    wrapper.unmount()
  })

  it('defaults to the V3 tab when the system mode is v3', async () => {
    mode.value = 'v3'
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="v3-settings"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // ADR 0008：V3 不受 V1/V2 的门控影响。下面三条是本页签合并后的回归防线。
  it.each(['v1', 'v2'] as const)(
    'keeps the V3 tab visible and clickable while the system mode is %s',
    async (systemMode) => {
      mode.value = systemMode
      const wrapper = mountView()
      await flushPromises()

      const v3Tab = wrapper.findAll('[role="tab"]').find((tab) => tab.text() === 'channelMonitorV2.admin.tabV3')
      expect(v3Tab, 'V3 tab must exist under V1/V2 modes').toBeDefined()
      expect(v3Tab!.attributes('disabled')).toBeUndefined()

      await v3Tab!.trigger('click')
      await flushPromises()
      // 配置面板真的挂上了（它只调用 V3 自己的接口，不读 V1/V2 的 flag）。
      expect(wrapper.find('[data-testid="v3-settings"]').exists()).toBe(true)
      wrapper.unmount()
    },
  )

  it('still exposes the V3 tab when the global channel-monitor switch is off', async () => {
    // 总开关关闭时 V1/V2 的接口会 403，但 V3 只认自己的开关 —— 页面不能把 V3 也关掉，
    // 而且默认就该落在 V3，否则管理员打开是本页签不可用的 V1/V2 配置。
    mode.enabled = false
    mode.value = 'v2'
    const wrapper = mountView()
    await flushPromises()

    const v3Tab = wrapper.findAll('[role="tab"]').find((tab) => tab.text() === 'channelMonitorV2.admin.tabV3')
    expect(v3Tab).toBeDefined()
    // 默认落点：总开关关掉时默认选中 V3，不去渲染必然失败的 V1/V2 配置。
    expect(v3Tab!.classes()).toContain('tab-active')
    expect(wrapper.find('[data-testid="v3-settings"]').exists()).toBe(true)
    wrapper.unmount()
  })
})

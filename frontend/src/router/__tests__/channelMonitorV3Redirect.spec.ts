import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { RouteRecordRaw } from 'vue-router'

/**
 * V3 配置页已并入 /admin/channels/monitor。旧路径必须留成重定向（带 tab=v3），
 * 否则老书签会掉到 404。
 */

const captured = vi.hoisted(() => ({ routes: [] as unknown[] }))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn((options: { routes: unknown[] }) => {
    captured.routes = options.routes
    return {
      beforeEach: vi.fn(),
      afterEach: vi.fn(),
      onError: vi.fn(),
    }
  }),
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({}) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({}) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))
vi.mock('@/stores/adminCompliance', () => ({ useAdminComplianceStore: () => ({}) }))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
  useNavigationLoading: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))
vi.mock('@/api/setup', () => ({ getSetupStatus: vi.fn() }))

type RedirectFn = (to: { query: Record<string, unknown> }) => { path: string; query: Record<string, unknown> }

describe('channel monitor routes', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  const routes = () => captured.routes as RouteRecordRaw[]

  it('keeps one admin monitor page for V1, V2 and V3', () => {
    const monitorRoutes = routes().filter((route) => route.path === '/admin/channels/monitor')
    expect(monitorRoutes).toHaveLength(1)
    expect(monitorRoutes[0].name).toBe('AdminChannelMonitor')
  })

  it('redirects the retired V3 page to the monitor page with tab=v3', () => {
    const legacy = routes().find((route) => route.path === '/admin/channel-monitor-v3')
    expect(legacy).toBeDefined()
    expect(legacy!.component).toBeUndefined()
    expect(legacy!.name).toBeUndefined()

    const redirect = legacy!.redirect as RedirectFn
    expect(typeof redirect).toBe('function')
    expect(redirect({ query: {} })).toMatchObject({
      path: '/admin/channels/monitor',
      query: { tab: 'v3' },
    })
    // 保留原有 query（如 range / platform），只补 tab。
    expect(redirect({ query: { range: '24h' } }).query).toEqual({ range: '24h', tab: 'v3' })
  })
})

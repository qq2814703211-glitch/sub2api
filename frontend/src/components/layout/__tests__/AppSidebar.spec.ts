import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar collapsible groups', () => {
  it('lets the user collapse a group even while a child route is active', () => {
    // The expand state must come from the user's override first, falling back
    // to the active-route heuristic only when the user has not clicked yet.
    expect(componentSource).toContain('const groupExpandOverrides = ref<Map<string, boolean>>(new Map())')
    expect(componentSource).not.toContain('expandedGroups.value.has(item.path) || isGroupActive(item)')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

describe('AppSidebar subscription feature flag', () => {
  it('gates the My Subscriptions entry behind the subscription public-settings flag', () => {
    expect(componentSource).toContain('const flagSubscription = makeSidebarFlag(FeatureFlags.subscription)')
    expect(componentSource).toMatch(/path: '\/subscriptions'[^\n]*featureFlag: flagSubscription/)
  })

  it('also hides the admin Subscription Management entry on recharge-only sites', () => {
    expect(componentSource).toMatch(/path: '\/admin\/subscriptions'[^\n]*featureFlag: flagSubscription/)
  })

  it('derives the purchase entry label from the site billing mode', () => {
    expect(componentSource).toContain("import { resolveSiteBillingMode } from '@/utils/siteBillingMode'")
    expect(componentSource).toMatch(/case 'recharge_only':\s*return t\('nav\.recharge'\)/)
    expect(componentSource).toMatch(/case 'subscription_only':\s*return t\('nav\.subscribe'\)/)
    expect(componentSource).toMatch(/path: '\/purchase'[^\n]*label: purchaseNavLabel\.value/)
  })

  it('gates the purchase entry independently from the internal payment flag', () => {
    expect(componentSource).toContain('flagExternalCardPurchase')
    expect(componentSource).toContain('flagPayment() || flagExternalCardPurchase()')
  })
})

describe('AppSidebar MCP entry', () => {
  it('uses the MCP logo and shows the entry only when the deployment enables MCP', () => {
    expect(componentSource).toContain("render: () => h(Icon, { name: 'mcp' })")
    expect(componentSource).toContain('const flagMCP = makeSidebarFlag(FeatureFlags.mcp)')
    expect(componentSource).toMatch(/path: '\/settings\/mcp'[^\n]*icon: MCPIcon, featureFlag: flagMCP/)
    expect(componentSource).toMatch(/if \(flagMCP\(\)\) filtered\.push\(\{ path: '\/settings\/mcp'[^\n]*icon: MCPIcon/)
    expect(componentSource).not.toMatch(/path: '\/settings\/mcp'[^\n]*PluginIcon/)
  })
})

describe('AppSidebar connection diagnostics entry', () => {
  it('does not expose the removed connection diagnostics route', () => {
    // 连接诊断（admin/connections）整体移除，防止从上游 fork 合并时又被带回来。
    expect(componentSource).not.toContain("path: '/admin/connections'")
    expect(componentSource).not.toContain("t('nav.connectionDiagnostics')")
  })
})

describe('AppSidebar channel monitor entry', () => {
  it('has no separate Channel Monitor V3 entry', () => {
    // V3 的配置并入了 /admin/channels/monitor 的第三个页签，侧边栏不再单列一级入口。
    expect(componentSource).not.toContain("path: '/admin/channel-monitor-v3'")
    expect(componentSource).not.toContain("t('nav.channelMonitorV3')")
  })

  it('keeps the merged monitor entry reachable regardless of V1/V2 flags', () => {
    // V3 只认自己的开关（ADR 0008）：这一页现在同时承载 V3 配置，不能挂在
    // channel_monitor_enabled 的菜单过滤上，否则总开关关掉时 V3 入口会消失。
    expect(componentSource).toMatch(/path: '\/admin\/channels\/monitor', label: t\('nav\.channelMonitor'\), icon: SignalIcon \}/)
    expect(componentSource).not.toMatch(/path: '\/admin\/channels\/monitor'[^\n]*featureFlag: flagChannelMonitor/)
  })
})

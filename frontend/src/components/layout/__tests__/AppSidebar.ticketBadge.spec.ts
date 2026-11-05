import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import AppSidebar from '../AppSidebar.vue'
import { useAdminSettingsStore, useAuthStore } from '@/stores'
import { useTicketNotificationsStore } from '@/stores/ticketNotifications'

const api = vi.hoisted(() => ({
  userStats: vi.fn(),
  adminStats: vi.fn()
}))

vi.mock('@/api/tickets', () => ({
  ticketsAPI: (admin: boolean) => ({ stats: admin ? api.adminStats : api.userStats })
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({
    canUseBatchImage: { value: false },
    batchImageAccessLoaded: { value: true },
    batchImageAccessLoading: { value: false },
    refreshBatchImageAccess: async () => false
  })
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  missingWarn: false,
  fallbackWarn: false,
  messages: { en: { nav: { tickets: 'Tickets' }, tickets: { unread: 'Unread' } } }
})

beforeEach(() => {
  vi.resetAllMocks()
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  api.userStats.mockResolvedValue({ total: 4, unread: 3 })
  api.adminStats.mockResolvedValue({ total: 20, unread: 7 })
})

async function mountSidebar(role: 'user' | 'admin') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore()
  auth.token = 'test-token'
  auth.user = { id: 1, username: role, email: `${role}@example.test`, role, status: 'active' } as never
  vi.spyOn(useAdminSettingsStore(), 'fetch').mockResolvedValue(undefined)

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }]
  })
  await router.push('/tickets')
  await router.isReady()

  const wrapper = mount(AppSidebar, {
    global: {
      plugins: [pinia, router, i18n],
      stubs: { VersionBadge: true, Icon: true }
    }
  })
  return { wrapper, store: useTicketNotificationsStore() }
}

function link(wrapper: Awaited<ReturnType<typeof mountSidebar>>['wrapper'], path: string) {
  const found = wrapper.findAll('a').find(anchor => anchor.attributes('href') === path)
  if (!found) throw new Error(`sidebar link ${path} was not rendered`)
  return found
}

describe('AppSidebar ticket badges', () => {
  it('shows the user inbox badge and the admin queue badge from separate counters', async () => {
    const { wrapper, store } = await mountSidebar('admin')
    await vi.waitFor(() => expect(store.adminUnread).toBe(7))
    await flushPromises()

    expect(link(wrapper, '/tickets').text()).toContain('3')
    expect(link(wrapper, '/admin/tickets').text()).toContain('7')

    wrapper.unmount()
  })

  it('does not render an admin queue badge or request admin stats for a regular user', async () => {
    const { wrapper, store } = await mountSidebar('user')
    await vi.waitFor(() => expect(store.userUnread).toBe(3))
    await flushPromises()

    expect(link(wrapper, '/tickets').text()).toContain('3')
    expect(wrapper.findAll('a').some(anchor => anchor.attributes('href') === '/admin/tickets')).toBe(false)
    expect(api.adminStats).not.toHaveBeenCalled()

    wrapper.unmount()
  })

  it('repaints both badges when the store polls new counts', async () => {
    const { wrapper, store } = await mountSidebar('admin')
    await vi.waitFor(() => expect(store.adminUnread).toBe(7))

    api.userStats.mockResolvedValue({ total: 10, unread: 4 })
    api.adminStats.mockResolvedValue({ total: 21, unread: 9 })
    await store.refresh()
    await flushPromises()

    expect(link(wrapper, '/tickets').text()).toContain('4')
    expect(link(wrapper, '/admin/tickets').text()).toContain('9')

    wrapper.unmount()
  })
})

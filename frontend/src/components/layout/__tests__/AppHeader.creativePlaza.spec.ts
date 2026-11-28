import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import AppHeader from '../AppHeader.vue'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/components/common/LocaleSwitcher.vue', () => ({
  default: { name: 'LocaleSwitcher', template: '<div class="stub-locale-switcher" />' },
}))
vi.mock('@/components/common/SubscriptionProgressMini.vue', () => ({
  default: { name: 'SubscriptionProgressMini', template: '<div class="stub-sub-mini" />' },
}))
vi.mock('@/components/common/AnnouncementBell.vue', () => ({
  default: { name: 'AnnouncementBell', template: '<div class="stub-announcement-bell" />' },
}))
vi.mock('@/components/icons/Icon.vue', () => ({
  default: {
    name: 'Icon',
    props: { name: String, size: String },
    template: '<span class="stub-icon" :data-icon="name" />',
  },
}))

function createTestI18n(locale: 'zh' | 'en' = 'zh') {
  return createI18n({
    legacy: false,
    locale,
    messages: {
      zh: {
        common: {
          toggleMenu: '切换菜单',
          userMenu: '用户菜单',
          availableBalance: '可用余额',
          frozenBalance: '冻结金额',
          totalBalance: '总余额',
        },
        nav: {
          docs: '文档',
          modelPlaza: '模型广场',
          creativePlaza: '创意广场',
          profile: '个人资料',
          logout: '退出登录',
        },
        creative: { title: '创意广场' },
        dashboard: { title: '仪表盘', welcomeMessage: '欢迎' },
      },
      en: {
        common: {
          toggleMenu: 'Toggle Menu',
          userMenu: 'User Menu',
          availableBalance: 'Available',
          frozenBalance: 'Frozen',
          totalBalance: 'Total',
        },
        nav: {
          docs: 'Docs',
          modelPlaza: 'Model Plaza',
          creativePlaza: 'Creative Plaza',
          profile: 'Profile',
          logout: 'Logout',
        },
        creative: { title: 'Creative Plaza' },
        dashboard: { title: 'Dashboard', welcomeMessage: 'Welcome' },
      },
    },
  })
}

async function mountHeaderAt(path: string, locale: 'zh' | 'en' = 'zh') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const authStore = useAuthStore()
  authStore.user = {
    id: 1,
    email: 'demo@example.com',
    username: 'demo',
    role: 'user',
    balance: 100,
    frozen_balance: 0,
    status: 'active',
  } as never

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/creative-plaza',
        alias: ['/image-studio', '/imagegen'],
        name: 'CreativePlaza',
        component: { template: '<div />' },
        meta: { title: 'Creative Plaza', titleKey: 'creative.title' },
      },
      {
        path: '/dashboard',
        name: 'Dashboard',
        component: { template: '<div />' },
        meta: { title: 'Dashboard', titleKey: 'dashboard.title', descriptionKey: 'dashboard.welcomeMessage' },
      },
      {
        path: '/model-plaza',
        name: 'ModelPlaza',
        component: { template: '<div />' },
        meta: { title: 'Model Plaza' },
      },
      {
        path: '/profile',
        name: 'Profile',
        component: { template: '<div />' },
      },
      {
        path: '/login',
        name: 'Login',
        component: { template: '<div />' },
      },
    ],
  })

  await router.push(path)
  await router.isReady()

  const i18n = createTestI18n(locale)
  const wrapper = mount(AppHeader, {
    global: {
      plugins: [pinia, router, i18n],
    },
  })
  await flushPromises()
  return { wrapper, router, i18n }
}

function countExactLeafText(root: Element, expected: string): number {
  return Array.from(root.querySelectorAll('*')).filter(
    (el) => el.children.length === 0 && (el.textContent || '').trim() === expected,
  ).length
}

describe('AppHeader Creative Plaza brand deduplication', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it.each(['/creative-plaza', '/image-studio', '/imagegen'])(
    'renders exactly one Creative Plaza brand in header and hides duplicate nav entry on %s',
    async (path) => {
      const { wrapper } = await mountHeaderAt(path, 'zh')
      const brand = wrapper.find('[data-test="header-creative-brand"]')
      expect(brand.exists()).toBe(true)
      expect(brand.classes()).toContain('flex')
      expect(brand.classes()).not.toContain('hidden')
      expect(wrapper.find('[data-test="header-creative-entry"]').exists()).toBe(false)
      expect(countExactLeafText(wrapper.find('header').element, '创意广场')).toBe(1)
      wrapper.unmount()
    },
  )

  it('keeps single English Creative Plaza title when host locale is en', async () => {
    const { wrapper } = await mountHeaderAt('/creative-plaza', 'en')
    expect(wrapper.find('[data-test="header-creative-brand"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="header-creative-entry"]').exists()).toBe(false)
    expect(countExactLeafText(wrapper.find('header').element, 'Creative Plaza')).toBe(1)
    wrapper.unmount()
  })

  it('keeps the right-side Creative Plaza entry on non-Creative Plaza routes', async () => {
    const { wrapper } = await mountHeaderAt('/dashboard', 'zh')
    expect(wrapper.find('[data-test="header-creative-brand"]').exists()).toBe(false)
    const entry = wrapper.find('[data-test="header-creative-entry"]')
    expect(entry.exists()).toBe(true)
    expect(entry.attributes('href')).toBe('/creative-plaza')
    expect(countExactLeafText(wrapper.find('header').element, '创意广场')).toBe(1)
    wrapper.unmount()
  })
})

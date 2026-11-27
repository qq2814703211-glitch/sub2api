import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createI18n } from 'vue-i18n'
import CreativePlazaView from '@/views/CreativePlazaView.vue'
import zhCreative from '@/i18n/locales/zh/creative'
import enCreative from '@/i18n/locales/en/creative'

vi.mock('@/api', () => ({
  keysAPI: {
    list: vi.fn().mockResolvedValue({
      items: [
        {
          id: 1,
          name: 'Default Key',
          key: 'sk-test-demo',
          status: 'active',
          group: { id: 1, name: 'OpenAI', platform: 'openai' },
        },
      ],
    }),
  },
}))

vi.mock('@/views/ImageGenView.vue', () => ({
  default: {
    name: 'ImageGenView',
    props: { embedded: Boolean },
    template: '<div data-test="stub-imagegen-view">Quick Image Studio</div>',
  },
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: {
    name: 'AppLayout',
    template: '<div class="stub-app-layout"><slot /></div>',
  },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: {
    name: 'Icon',
    props: { name: String, size: String },
    template: '<span class="stub-icon" />',
  },
}))

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'zh',
    messages: {
      zh: zhCreative,
      en: enCreative,
    },
  })
}

async function mountAt(path: string) {
  setActivePinia(createPinia())
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/creative-plaza', alias: ['/image-studio', '/imagegen'], component: CreativePlazaView },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(CreativePlazaView, {
    global: {
      plugins: [router, createTestI18n()],
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('CreativePlazaView', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ data: [{ id: 'gpt-4o' }, { id: 'gpt-image-1' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
  })

  it('opens on the real Toonflow Web Studio iframe by default at /creative-plaza', async () => {
    const { wrapper } = await mountAt('/creative-plaza')
    const iframe = wrapper.find('[data-test="toonflow-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('src')).toBe('/toonflow/index.html#/home')
    expect(wrapper.find('[data-test="local-compute-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="toonflow-nav-trumanMap"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="stub-imagegen-view"]').exists()).toBe(false)
  })

  it('defaults legacy /image-studio bookmark directly to Quick Image Studio tab', async () => {
    const { wrapper } = await mountAt('/image-studio')
    expect(wrapper.find('[data-test="stub-imagegen-view"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="creative-tab-imagegen"]').attributes('aria-selected')).toBe('true')
  })

  it('toggles gateway key selector panel and switches tabs cleanly', async () => {
    const { wrapper } = await mountAt('/creative-plaza')
    expect(wrapper.find('[data-test="creative-key-panel"]').exists()).toBe(false)
    await wrapper.find('[data-test="toggle-key-panel"]').trigger('click')
    expect(wrapper.find('[data-test="creative-key-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-select-text"]').exists()).toBe(true)

    await wrapper.find('[data-test="creative-tab-imagegen"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="stub-imagegen-view"]').exists()).toBe(true)
  })
})

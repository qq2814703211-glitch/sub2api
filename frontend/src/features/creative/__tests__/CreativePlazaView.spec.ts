import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

import CreativePlazaView from '@/views/CreativePlazaView.vue'
import { resetMemoryStorageForTests } from '@/features/creative/storage'
import zh from '@/i18n/locales/zh'

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { name: 'AppLayout', template: '<div><slot /></div>' },
}))

vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: vi.fn(async () => ({
      items: [
        {
          id: 11,
          name: '创意密钥',
          key: 'sk-test-creative-key',
          status: 'active',
          group: { id: 1, name: 'Grok', platform: 'grok' },
        },
      ],
      total: 1,
    })),
    getById: vi.fn(async () => null),
  },
}))

function makeI18n() {
  return createI18n({
    legacy: false,
    locale: 'zh',
    fallbackLocale: 'zh',
    messages: { zh },
  })
}

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/creative-plaza',
        alias: ['/image-studio', '/imagegen'],
        name: 'CreativePlaza',
        component: CreativePlazaView,
      },
    ],
  })
  await router.push(path)
  await router.isReady()

  const wrapper = mount(CreativePlazaView, {
    global: {
      plugins: [makeI18n(), router],
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('CreativePlazaView', () => {
  let originalFetch: typeof globalThis.fetch

  beforeEach(() => {
    setActivePinia(createPinia())
    resetMemoryStorageForTests()
    originalFetch = globalThis.fetch
    globalThis.fetch = vi.fn(async () => {
      return new Response(JSON.stringify({ data: [], templates: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof globalThis.fetch
    if (!('indexedDB' in globalThis)) {
      Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: undefined })
    }
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('mounts on /creative-plaza with Toonflow infinite canvas, local compute badge, and starter workflow', async () => {
    const { wrapper } = await mountAt('/creative-plaza')

    expect(wrapper.find('[data-test="creative-plaza-view"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="local-compute-badge"]').text()).toContain(
      zh.creative.localComputeBadge,
    )
    expect(wrapper.find('[data-test="creative-canvas-stage"]').exists()).toBe(true)
    // Default starter short-drama workflow comes with 4 connected nodes
    expect(wrapper.find('[data-test="canvas-node-text"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="canvas-node-imageGen"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="canvas-node-videoGen"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="canvas-node-frameExtract"]').exists()).toBe(true)

    wrapper.unmount()
  })

  it('adds nodes from toolbar and switches across Canvas, Quick Image Studio, Storyboard, and Asset Library', async () => {
    const { wrapper } = await mountAt('/creative-plaza')

    const beforeTextCount = wrapper.findAll('[data-test="canvas-node-text"]').length
    await wrapper.find('[data-test="add-node-text"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="canvas-node-text"]').length).toBe(beforeTextCount + 1)

    // Switch to Storyboard tab
    await wrapper.find('[data-test="creative-tab-storyboard"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="creative-storyboard-workspace"]').exists()).toBe(true)

    // Switch to Local Asset Library tab
    await wrapper.find('[data-test="creative-tab-assets"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="creative-asset-library"]').exists()).toBe(true)

    // Switch to Quick Image Studio tab — fused existing ImageGenView renders inside
    await wrapper.find('[data-test="creative-tab-imagegen"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="generation-key-select"]').exists()).toBe(true)
    expect(wrapper.text()).toContain(zh.imagegen.promptLabel)

    wrapper.unmount()
  })

  it('opens legacy /image-studio route directly on the Quick Image Studio tab while keeping Creative Plaza tabs', async () => {
    const { wrapper } = await mountAt('/image-studio')

    expect(wrapper.find('[data-test="creative-tab-canvas"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="creative-tab-imagegen"]').attributes('aria-selected')).toBe(
      'true',
    )
    expect(wrapper.find('[data-test="generation-key-select"]').exists()).toBe(true)

    wrapper.unmount()
  })
})

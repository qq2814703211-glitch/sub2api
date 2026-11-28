import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createI18n } from 'vue-i18n'
import CreativePlazaView from '@/views/CreativePlazaView.vue'
import {
  CREATIVE_KEY_BINDINGS_STORAGE_KEY,
  TOONFLOW_BRIDGE_STORAGE_KEY,
} from '@/features/creative/toonflowHostBridge'
import zhCreative from '@/i18n/locales/zh/creative'
import enCreative from '@/i18n/locales/en/creative'

const mockKeysList = vi.fn()

vi.mock('@/api', () => ({
  keysAPI: {
    list: (...args: unknown[]) => mockKeysList(...args),
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
      { path: '/keys', component: { template: '<div>Keys</div>' } },
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
    document.documentElement.classList.remove('dark')
    document.documentElement.removeAttribute('data-universe-theme')
    mockKeysList.mockReset()
    mockKeysList.mockResolvedValue({
      items: [
        {
          id: 1,
          name: 'Default Key',
          key: 'sk-test-demo',
          status: 'active',
          group: { id: 1, name: 'OpenAI', platform: 'openai' },
        },
        {
          id: 2,
          name: 'Image Key',
          key: 'sk-test-image',
          status: 'active',
          group: { id: 2, name: 'Image', platform: 'openai' },
        },
      ],
    })
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [
            { id: 'gpt-4o', supports_reasoning_effort: true, reasoning_efforts: [{ value: 'low' }, { value: 'high' }] },
            { id: 'gpt-image-1' },
          ],
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        },
      ),
    )
  })

  it('mounts the unified Studio iframe cleanly at /creative-plaza without redundant host key panels or Toonflow copy', async () => {
    const { wrapper } = await mountAt('/creative-plaza')
    const iframe = wrapper.find('[data-test="studio-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('src')).toBe('/toonflow/index.html#/home')
    expect(wrapper.text()).not.toMatch(/toonflow/i)
    expect(wrapper.find('[data-test="creative-key-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="toggle-fullscreen"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="open-studio-standalone"]').attributes('href')).toBe('/toonflow/index.html#/home')

    const storedCtx = JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')
    expect(storedCtx.selectedTextKeyId).toBe(1)
    expect(storedCtx.selectedImageKeyId).toBe(1)
    expect(storedCtx.discoveredModels['1']).toEqual(['gpt-4o', 'gpt-image-1'])
    expect(storedCtx.discoveredModelMeta['1'][0]).toMatchObject({
      id: 'gpt-4o',
      supportsReasoningEffort: true,
    })
  })

  it('routes legacy /image-studio and /imagegen bookmarks directly to Studio Quick Image section', async () => {
    const { wrapper } = await mountAt('/image-studio')
    const iframe = wrapper.find('[data-test="studio-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('src')).toBe('/toonflow/index.html#/home?section=quickImage')
  })

  it('syncs key binding updates, key refreshes, and /keys navigation from Studio postMessage', async () => {
    const { wrapper, router } = await mountAt('/creative-plaza')
    const initialCalls = mockKeysList.mock.calls.length

    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: {
          source: 'toonflow-web',
          type: 'updateKeyBindings',
          payload: {
            textKeyId: 1,
            imageKeyId: 2,
            videoKeyId: 2,
            audioKeyId: 1,
            rewriteModel: 'gpt-4o',
            rewriteEffort: 'low',
          },
        },
      }),
    )
    await flushPromises()

    const savedBindings = JSON.parse(localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY) || '{}')
    expect(savedBindings).toMatchObject({
      textKeyId: 1,
      imageKeyId: 2,
      videoKeyId: 2,
      audioKeyId: 1,
      rewriteModel: 'gpt-4o',
      rewriteEffort: 'low',
    })
    const storedCtx = JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')
    expect(storedCtx.selectedImageKeyId).toBe(2)
    expect(storedCtx.rewriteModel).toBe('gpt-4o')
    expect(storedCtx.rewriteEffort).toBe('low')

    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: { source: 'toonflow-web', type: 'refreshKeys' },
      }),
    )
    await flushPromises()
    expect(mockKeysList.mock.calls.length).toBeGreaterThan(initialCalls)

    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: { source: 'toonflow-web', type: 'openSiteKeys' },
      }),
    )
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/keys')
    wrapper.unmount()
  })

  it('tracks both .dark class and data-universe-theme cosmos surface for host theme sync', async () => {
    document.documentElement.setAttribute('data-universe-theme', 'cosmos')
    const { wrapper } = await mountAt('/creative-plaza')
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}').theme).toBe('dark')

    document.documentElement.removeAttribute('data-universe-theme')
    await flushPromises()
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}').theme).toBe('light')

    document.documentElement.classList.add('dark')
    await flushPromises()
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}').theme).toBe('dark')

    document.documentElement.classList.remove('dark')
    await flushPromises()
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}').theme).toBe('light')
    wrapper.unmount()
  })
})

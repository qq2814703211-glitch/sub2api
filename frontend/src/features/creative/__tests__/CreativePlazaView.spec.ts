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
    props: { fullBleed: Boolean },
    template: '<div class="stub-app-layout" :data-full-bleed="String(Boolean(fullBleed))"><slot /></div>',
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

  it('mounts the unified full-bleed Studio iframe cleanly at /creative-plaza without fullscreen, standalone, or Toonflow copy', async () => {
    const { wrapper } = await mountAt('/creative-plaza')
    expect(wrapper.find('.stub-app-layout').attributes('data-full-bleed')).toBe('true')
    const iframe = wrapper.find('[data-test="studio-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('src')).toBe('/creative-studio/#/home')
    expect(iframe.attributes('src')).not.toMatch(/toonflow/i)
    expect(wrapper.text()).not.toMatch(/toonflow/i)
    expect(wrapper.find('[data-test="creative-key-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="toggle-fullscreen"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="open-studio-standalone"]').exists()).toBe(false)

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
    expect(iframe.attributes('src')).toBe('/creative-studio/#/home?section=quickImage')
    expect(wrapper.find('[data-test="open-studio-standalone"]').exists()).toBe(false)
  })

  it('syncs key binding updates, default models, discovered models, key refreshes, and /keys navigation from Studio postMessage', async () => {
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
            defaultTextModel: 'gpt-4o',
            defaultImageModel: 'nano-banana-pro',
            defaultVideoModel: 'veo-3-pro',
            defaultAudioModel: 'fish-speech-v2',
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
      defaultTextModel: 'gpt-4o',
      defaultImageModel: 'nano-banana-pro',
      defaultVideoModel: 'veo-3-pro',
      defaultAudioModel: 'fish-speech-v2',
      rewriteModel: 'gpt-4o',
      rewriteEffort: 'low',
    })
    const storedCtx = JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')
    expect(storedCtx.selectedImageKeyId).toBe(2)
    expect(storedCtx.defaultImageModel).toBe('nano-banana-pro')
    expect(storedCtx.defaultVideoModel).toBe('veo-3-pro')
    expect(storedCtx.defaultAudioModel).toBe('fish-speech-v2')
    expect(storedCtx.rewriteModel).toBe('gpt-4o')
    expect(storedCtx.rewriteEffort).toBe('low')

    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: {
          source: 'toonflow-web',
          type: 'updateDiscoveredModels',
          payload: {
            keyId: 2,
            models: ['veo-3-pro', 'nano-banana-pro'],
            meta: [{ id: 'veo-3-pro' }, { id: 'nano-banana-pro' }],
          },
        },
      }),
    )
    await flushPromises()
    const updatedCtx = JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')
    expect(updatedCtx.discoveredModels['2']).toEqual(['veo-3-pro', 'nano-banana-pro'])

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

  it('reconciles stale default models when switching to a key with a distinct /v1/models list', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
      const auth = new Headers(init?.headers).get('Authorization') || ''
      const data = auth.includes('sk-test-image')
        ? [{ id: 'flux-kontext-pro' }, { id: 'imagen-4' }]
        : [{ id: 'gpt-4o' }, { id: 'gpt-image-1' }]
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    const { wrapper } = await mountAt('/creative-plaza')
    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: {
          source: 'toonflow-web',
          type: 'updateKeyBindings',
          payload: { defaultImageModel: 'gpt-image-1' },
        },
      }),
    )
    await flushPromises()
    expect(JSON.parse(localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY) || '{}').defaultImageModel).toBe(
      'gpt-image-1',
    )

    window.dispatchEvent(
      new MessageEvent('message', {
        origin: window.location.origin,
        data: {
          source: 'toonflow-web',
          type: 'updateKeyBindings',
          payload: {
            textKeyId: 1,
            imageKeyId: 2,
            videoKeyId: 1,
            audioKeyId: 1,
            defaultTextModel: 'gpt-4o',
            defaultImageModel: 'gpt-image-1',
            defaultVideoModel: '',
            defaultAudioModel: '',
            rewriteModel: '',
            rewriteEffort: 'unset',
          },
        },
      }),
    )
    await flushPromises()

    const saved = JSON.parse(localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY) || '{}')
    expect(saved.imageKeyId).toBe(2)
    expect(saved.defaultImageModel).toBe('flux-kontext-pro')
    wrapper.unmount()
  })
})

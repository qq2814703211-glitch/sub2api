import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import {
  CREATIVE_KEY_BINDINGS_STORAGE_KEY,
  TOONFLOW_BRIDGE_STORAGE_KEY,
  discoverModelDetailsForKey,
  discoverModelsForKey,
  findInheritedImageGenKeyId,
  loadCreativeKeyBindings,
  postToonflowMessage,
  saveCreativeKeyBindings,
  serializeKeysForToonflow,
  writeToonflowHostContext,
} from '../toonflowHostBridge'
import type { ApiKey } from '@/types'

describe('toonflowHostBridge', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('persists and loads creative key bindings including default models and rewrite settings', () => {
    saveCreativeKeyBindings({
      textKeyId: 10,
      imageKeyId: 11,
      videoKeyId: 12,
      audioKeyId: 13,
      defaultTextModel: 'gpt-5-mini',
      defaultImageModel: 'nano-banana-pro',
      defaultVideoModel: 'veo-3-pro',
      defaultAudioModel: 'fish-speech-v2',
      rewriteModel: 'gpt-4o-mini',
      rewriteEffort: 'low',
    })
    expect(localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY)).toBeTruthy()
    expect(loadCreativeKeyBindings()).toEqual({
      textKeyId: 10,
      imageKeyId: 11,
      videoKeyId: 12,
      audioKeyId: 13,
      defaultTextModel: 'gpt-5-mini',
      defaultImageModel: 'nano-banana-pro',
      defaultVideoModel: 'veo-3-pro',
      defaultAudioModel: 'fish-speech-v2',
      rewriteModel: 'gpt-4o-mini',
      rewriteEffort: 'low',
    })
  })

  it('inherits legacy ig_rewriteConfig when creative key bindings are not yet stored', () => {
    localStorage.setItem(
      'ig_rewriteConfig',
      JSON.stringify({ keyId: 7, model: 'claude-sonnet-4-5', effort: 'medium' }),
    )
    expect(loadCreativeKeyBindings(1)).toEqual({
      textKeyId: 7,
      imageKeyId: 1,
      videoKeyId: 1,
      audioKeyId: 1,
      defaultTextModel: '',
      defaultImageModel: '',
      defaultVideoModel: '',
      defaultAudioModel: '',
      rewriteModel: 'claude-sonnet-4-5',
      rewriteEffort: 'medium',
    })
  })

  it('serializes active API keys with group metadata for Studio runtime', () => {
    const sample = [
      {
        id: 7,
        name: 'Studio Key',
        key: '  sk-test-123  ',
        status: 'active',
        group: { id: 1, name: 'OpenAI Group', platform: 'openai' },
      },
    ] as unknown as ApiKey[]

    expect(serializeKeysForToonflow(sample)).toEqual([
      {
        id: 7,
        name: 'Studio Key',
        key: 'sk-test-123',
        platform: 'openai',
        groupName: 'OpenAI Group',
      },
    ])
  })

  it('discovers unique sorted models and reasoning metadata from /v1/models', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(
        JSON.stringify({
          data: [
            {
              id: 'gpt-4o',
              supports_reasoning_effort: true,
              reasoning_effort: 'medium',
              reasoning_efforts: [{ value: 'low' }, { value: 'medium', default: true }, { value: 'high' }],
            },
            { id: 'claude-sonnet-4-5', supports_reasoning_effort: false },
            { id: 'gpt-4o' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )
    const details = await discoverModelDetailsForKey('sk-valid')
    expect(details.models).toEqual(['claude-sonnet-4-5', 'gpt-4o'])
    expect(details.meta).toEqual([
      { id: 'claude-sonnet-4-5', supportsReasoningEffort: false },
      {
        id: 'gpt-4o',
        supportsReasoningEffort: true,
        reasoningEffort: 'medium',
        reasoningEfforts: [{ value: 'low' }, { value: 'medium', default: true }, { value: 'high' }],
      },
    ])

    const models = await discoverModelsForKey('sk-valid')
    expect(models).toEqual(['claude-sonnet-4-5', 'gpt-4o'])
  })

  it('inherits previously selected Image Studio key by matching ig_apiKey', () => {
    localStorage.setItem('ig_apiKey', '  sk-studio-match  ')
    const keys = [
      { id: 1, name: 'First', key: 'sk-other', status: 'active' },
      { id: 9, name: 'Matched', key: 'sk-studio-match', status: 'active' },
    ] as unknown as ApiKey[]
    expect(findInheritedImageGenKeyId(keys)).toBe(9)
  })

  it('writes host context to localStorage and posts same-origin messages to iframe', () => {
    const ctx = {
      origin: 'http://localhost:3000',
      locale: 'zh-CN' as const,
      theme: 'dark' as const,
      siteName: '',
      siteLogo: '/logo.svg',
      user: { id: 1, email: 'creator@example.com', username: 'creator', role: 'user' },
      keys: [],
      selectedTextKeyId: null,
      selectedImageKeyId: null,
      selectedVideoKeyId: null,
      selectedAudioKeyId: null,
      rewriteModel: '',
      rewriteEffort: 'unset',
      discoveredModels: {},
      discoveredModelMeta: {},
    }
    writeToonflowHostContext(ctx)
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')).toEqual(ctx)

    const postMessage = vi.fn((data: unknown) => {
      // Simulate real browser Window.postMessage structured clone (throws DataCloneError on Vue Proxy)
      structuredClone(data)
    })
    const fakeFrame = { contentWindow: { postMessage } } as unknown as HTMLIFrameElement
    postToonflowMessage(fakeFrame, 'navigate', '#/home?section=quickImage')
    expect(postMessage).toHaveBeenCalledWith(
      { source: 'sub2api-host', type: 'navigate', payload: '#/home?section=quickImage' },
      window.location.origin,
    )

    const reactiveModels = reactive<Record<number, string[]>>({ 1: ['gpt-4o', 'gpt-image-1'] })
    postToonflowMessage(fakeFrame, 'syncContext', {
      ...ctx,
      theme: 'light',
      discoveredModels: { ...reactiveModels },
    })
    expect(postMessage).toHaveBeenLastCalledWith(
      {
        source: 'sub2api-host',
        type: 'syncContext',
        payload: {
          ...ctx,
          theme: 'light',
          discoveredModels: { 1: ['gpt-4o', 'gpt-image-1'] },
        },
      },
      window.location.origin,
    )
  })
})

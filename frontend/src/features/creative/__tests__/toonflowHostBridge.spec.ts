import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  CREATIVE_KEY_BINDINGS_STORAGE_KEY,
  TOONFLOW_BRIDGE_STORAGE_KEY,
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

  it('persists and loads creative key bindings', () => {
    saveCreativeKeyBindings({ textKeyId: 10, imageKeyId: 11, videoKeyId: 12, audioKeyId: 13 })
    expect(localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY)).toBeTruthy()
    expect(loadCreativeKeyBindings()).toEqual({
      textKeyId: 10,
      imageKeyId: 11,
      videoKeyId: 12,
      audioKeyId: 13,
    })
  })

  it('serializes active API keys with group metadata for Toonflow runtime', () => {
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

  it('discovers and sorts unique models from /v1/models', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [{ id: 'gpt-4o' }, { id: 'claude-sonnet-4-5' }, { id: 'gpt-4o' }],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )
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
      user: { id: 1, email: 'creator@example.com', username: 'creator', role: 'user', balance: 42 },
      keys: [],
      selectedTextKeyId: null,
      selectedImageKeyId: null,
      selectedVideoKeyId: null,
      selectedAudioKeyId: null,
      discoveredModels: {},
    }
    writeToonflowHostContext(ctx)
    expect(JSON.parse(localStorage.getItem(TOONFLOW_BRIDGE_STORAGE_KEY) || '{}')).toEqual(ctx)

    const postMessage = vi.fn()
    const fakeFrame = { contentWindow: { postMessage } } as unknown as HTMLIFrameElement
    postToonflowMessage(fakeFrame, 'navigate', '#/home?section=trumanMap')
    expect(postMessage).toHaveBeenCalledWith(
      { source: 'sub2api-host', type: 'navigate', payload: '#/home?section=trumanMap' },
      window.location.origin,
    )
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  parseStoryboardShotsFromText,
  requestCreativeText,
  requestCreativeVideo,
  resolveVideoProtocol,
} from '../api'
import type { VideoGenNodeData } from '../types'

describe('creative gateway API adapters', () => {
  let originalFetch: typeof globalThis.fetch

  beforeEach(() => {
    originalFetch = globalThis.fetch
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('resolves video wire protocol by explicit override, model name, or group platform', () => {
    expect(resolveVideoProtocol('seedance', 'anything', 'openai')).toBe('seedance')
    expect(resolveVideoProtocol('auto', 'doubao-seedance-1-0', 'openai')).toBe('seedance')
    expect(resolveVideoProtocol('auto', 'grok-imagine-video', 'composite')).toBe('grok')
    expect(resolveVideoProtocol('auto', 'custom-video', 'grok')).toBe('grok')
    expect(resolveVideoProtocol('auto', 'veo-3', 'openai')).toBe('compatible')
  })

  it('parses fenced JSON, wrapped object, and plain-text fallback storyboards', () => {
    const fenced = parseStoryboardShotsFromText(
      '```json\n[{"scene":"车站","title":"逆光登场","visualPrompt":"清晨云海浮岛车站","cameraMotion":"缓推","dialogue":"出发","durationSec":6}]\n```',
    )
    expect(fenced).toHaveLength(1)
    expect(fenced[0].scene).toBe('车站')
    expect(fenced[0].durationSec).toBe(6)

    const wrapped = parseStoryboardShotsFromText(
      JSON.stringify({
        shots: [
          { scene: '雨巷', title: '回眸', visualPrompt: '霓虹雨夜古巷特写', durationSec: 20 },
        ],
      }),
    )
    expect(wrapped).toHaveLength(1)
    // Clamped to [2, 15]
    expect(wrapped[0].durationSec).toBe(15)

    const fallback = parseStoryboardShotsFromText(
      '1. 少年推开老旧画室的木门，尘埃在阳光中飞舞\n2. 画布上的云海突然开始流动并溢出一阵微风',
    )
    expect(fallback).toHaveLength(2)
    expect(fallback[0].visualPrompt).toContain('画室')
  })

  it('dispatches creative text calls to Anthropic Messages and OpenAI Chat endpoints', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/messages')) {
        expect((init?.headers as Record<string, string>)['x-api-key']).toBe('sk-claude-123')
        return new Response(
          JSON.stringify({ content: [{ type: 'text', text: 'Anthropic expanded prompt' }] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(
        JSON.stringify({ choices: [{ message: { content: 'Chat expanded prompt' } }] }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch

    const claude = await requestCreativeText({
      apiKey: 'sk-claude-123',
      platform: 'anthropic',
      model: 'claude-sonnet-4-5',
      systemPrompt: 'sys',
      userPrompt: 'hello',
    })
    expect(claude.protocol).toBe('anthropic-messages')
    expect(claude.text).toBe('Anthropic expanded prompt')

    const compositeClaude = await requestCreativeText({
      apiKey: 'sk-claude-123',
      platform: 'composite',
      model: 'claude-opus-4-5',
      systemPrompt: 'sys',
      userPrompt: 'hello',
    })
    expect(compositeClaude.protocol).toBe('anthropic-messages')
    expect(compositeClaude.text).toBe('Anthropic expanded prompt')

    const openai = await requestCreativeText({
      apiKey: 'sk-openai-123',
      platform: 'openai',
      model: 'gpt-5',
      systemPrompt: 'sys',
      userPrompt: 'hello',
    })
    expect(openai.protocol).toBe('openai-chat')
    expect(openai.text).toBe('Chat expanded prompt')
  })

  it('submits and polls Seedance, compatible video, and Grok video endpoints', async () => {
    const baseNode: VideoGenNodeData = {
      prompt: 'camera dolly in',
      model: 'doubao-seedance-1-0',
      protocol: 'auto',
      duration: 5,
      aspectRatio: '16:9',
      resolution: '720p',
      taskId: '',
      taskStatus: '',
      outputVideoUrl: '',
    }

    const calls: string[] = []
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.endsWith('/api/v3/contents/generations/tasks') && init?.method === 'POST') {
        const parsed = JSON.parse(String(init.body)) as { content: Array<{ type: string }> }
        expect(parsed.content.map((c) => c.type)).toEqual(['text', 'image_url'])
        return new Response(JSON.stringify({ id: 'sd-task-99' }), { status: 200 })
      }
      if (url.endsWith('/api/v3/contents/generations/tasks/sd-task-99')) {
        return new Response(
          JSON.stringify({ status: 'succeeded', content: { video_url: 'https://cdn.example.com/sd.mp4' } }),
          { status: 200 },
        )
      }
      if (url.endsWith('/v1/videos/generations') && init?.method === 'POST') {
        return new Response(JSON.stringify({ request_id: 'grok-req-7' }), { status: 200 })
      }
      if (url.endsWith('/v1/videos/generations/grok-req-7')) {
        return new Response(JSON.stringify({ status: 'completed' }), { status: 200 })
      }
      if (url.endsWith('/v1/videos/generations/grok-req-7/content')) {
        return new Response(new Blob(['fake-mp4'], { type: 'video/mp4' }), { status: 200 })
      }
      if (url.endsWith('/v1/video/generations') && init?.method === 'POST') {
        return new Response(JSON.stringify({ id: 'compat-1' }), { status: 200 })
      }
      if (url.endsWith('/v1/video/generations/compat-1')) {
        return new Response(
          JSON.stringify({ status: 'completed', video_url: 'https://cdn.example.com/veo.mp4' }),
          { status: 200 },
        )
      }
      return new Response(JSON.stringify({ error: { message: 'unexpected' } }), { status: 400 })
    })
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch

    const originalCreateObjectURL = URL.createObjectURL
    const createObjectURLMock = vi.fn(() => 'blob:mock-video')
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      writable: true,
      value: createObjectURLMock,
    })

    try {
      const controller = new AbortController()
      const seedanceRes = await requestCreativeVideo({
        apiKey: 'sk-video-1',
        platform: 'openai',
        nodeData: baseNode,
        upstreamPrompt: 'floating station',
        upstreamImages: ['data:image/png;base64,AAA'],
        signal: controller.signal,
        pollIntervalMs: 1,
        maxPolls: 3,
      })
      expect(seedanceRes.protocol).toBe('seedance')
      expect(seedanceRes.videoUrl).toBe('https://cdn.example.com/sd.mp4')

      const grokRes = await requestCreativeVideo({
        apiKey: 'sk-grok-1',
        platform: 'grok',
        nodeData: { ...baseNode, model: 'grok-imagine-video' },
        upstreamPrompt: 'neon alley',
        upstreamImages: [],
        signal: controller.signal,
        pollIntervalMs: 1,
        maxPolls: 3,
      })
      expect(grokRes.protocol).toBe('grok')
      expect(grokRes.videoUrl).toBe('blob:mock-video')
      expect(createObjectURLMock).toHaveBeenCalledTimes(1)

      const compatRes = await requestCreativeVideo({
        apiKey: 'sk-veo-1',
        platform: 'openai',
        nodeData: { ...baseNode, model: 'veo-3' },
        upstreamPrompt: 'ocean drone shot',
        upstreamImages: [],
        signal: controller.signal,
        pollIntervalMs: 1,
        maxPolls: 3,
      })
      expect(compatRes.protocol).toBe('compatible')
      expect(compatRes.videoUrl).toBe('https://cdn.example.com/veo.mp4')

      expect(calls.some((c) => c.includes('/api/v3/contents/generations/tasks/sd-task-99'))).toBe(true)
      expect(calls.some((c) => c.includes('/v1/videos/generations/grok-req-7/content'))).toBe(true)
      expect(calls.some((c) => c.includes('/v1/video/generations/compat-1'))).toBe(true)
    } finally {
      Object.defineProperty(URL, 'createObjectURL', {
        configurable: true,
        writable: true,
        value: originalCreateObjectURL,
      })
    }
  })
})

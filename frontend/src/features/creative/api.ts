/**
 * Direct browser-to-Sub2API-gateway adapters for Creative Plaza (创意广场).
 *
 * Every AI call goes directly from the user's browser to the current Sub2API
 * gateway using the user's own selected API key (`sk-...`). No extra backend
 * service or server-side worker is required.
 */
import {
  ImageGenError,
  assertHeaderSafeKey,
  cleanApiKey,
  dataUrlToFile,
  gatewayUrl,
  requestImages,
} from '@/features/imagegen/api'
import {
  ANTHROPIC_VERSION,
  extractRewriteError,
  parseRewriteResponse,
  rewritePath,
} from '@/features/imagegen/rewriteApi'
import {
  detectRewriteProtocol,
  type RewriteProtocol,
} from '@/features/imagegen/rewriteConfig'
import { buildPromptPayload } from '@/features/imagegen/retouch'
import { createNodeId } from './graph'
import type {
  ImageGenNodeData,
  StoryboardShot,
  VideoGenNodeData,
  VideoProtocol,
} from './types'

export interface GatewayModelLists {
  all: string[]
  text: string[]
  image: string[]
  video: string[]
}

const IMAGE_MODEL_RE = /image|dall-?e|flux|midjourney|imagen|seedream|grok-2-image|grok-imagine-image|gpt-image/i
const VIDEO_MODEL_RE = /video|veo|sora|seedance|kling|runway|minimax-h3|vidu|wan|grok-imagine-video/i
const NON_TEXT_RE = /image|dall-?e|flux|midjourney|stable-diffusion|sd[-_]?xl|video|veo|sora|seedance|kling|tts|whisper|embed/i

/** Discovers models available to a given API key via `GET /v1/models`. */
export async function fetchKeyModels(
  rawKey: string,
  signal?: AbortSignal,
): Promise<GatewayModelLists> {
  const key = cleanApiKey(rawKey)
  if (!key) return { all: [], text: [], image: [], video: [] }
  try {
    const response = await fetch(gatewayUrl('/v1/models'), {
      method: 'GET',
      headers: { Authorization: `Bearer ${key}` },
      signal,
    })
    if (!response.ok) return { all: [], text: [], image: [], video: [] }
    const payload = (await response.json().catch(() => null)) as {
      data?: Array<{ id?: unknown; model?: unknown }>
    } | null
    const seen = new Set<string>()
    const all: string[] = []
    for (const entry of payload?.data ?? []) {
      const id = String(entry?.id ?? entry?.model ?? '').trim()
      if (!id || seen.has(id)) continue
      seen.add(id)
      all.push(id)
    }
    all.sort((a, b) => a.localeCompare(b))
    return {
      all,
      text: all.filter((id) => !NON_TEXT_RE.test(id)),
      image: all.filter((id) => IMAGE_MODEL_RE.test(id) && !VIDEO_MODEL_RE.test(id)),
      video: all.filter((id) => VIDEO_MODEL_RE.test(id)),
    }
  } catch {
    return { all: [], text: [], image: [], video: [] }
  }
}

export interface CreativeTextRequest {
  apiKey: string
  platform: string
  model: string
  systemPrompt: string
  userPrompt: string
  signal?: AbortSignal
}

/**
 * Calls the current Sub2API gateway's text endpoint directly from the browser,
 * automatically matching OpenAI Chat, OpenAI Responses, or Anthropic Messages
 * according to the selected key's group platform.
 */
export async function requestCreativeText(options: CreativeTextRequest): Promise<{
  text: string
  protocol: RewriteProtocol
}> {
  const key = cleanApiKey(options.apiKey)
  if (!key) throw new ImageGenError('请先在顶部选择用于文本 / 编导生成的站内密钥')
  assertHeaderSafeKey(key)

  const model = options.model.trim()
  if (!model) throw new ImageGenError('请先选择或填写文本模型名称')

  const protocol = detectRewriteProtocol(options.platform, model)
  const url = gatewayUrl(`/v1${rewritePath(protocol)}`)
  const systemText = options.systemPrompt.trim()
  const userText = options.userPrompt.trim()
  if (!userText) throw new ImageGenError('请输入文本内容或连接上游文本节点')

  let headers: Record<string, string>
  let body: Record<string, unknown>

  if (protocol === 'anthropic-messages') {
    headers = {
      'Content-Type': 'application/json',
      'x-api-key': key,
      'anthropic-version': ANTHROPIC_VERSION,
    }
    body = {
      model,
      max_tokens: 2048,
      ...(systemText ? { system: systemText } : {}),
      messages: [{ role: 'user', content: userText }],
    }
  } else if (protocol === 'openai-responses') {
    headers = {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${key}`,
    }
    const combined = systemText ? `${systemText}\n\n${userText}` : userText
    body = {
      model,
      input: combined,
    }
  } else {
    headers = {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${key}`,
    }
    body = {
      model,
      messages: [
        ...(systemText ? [{ role: 'system', content: systemText }] : []),
        { role: 'user', content: userText },
      ],
    }
  }

  const response = await fetch(url, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: options.signal,
  })
  const payload = (await response.json().catch(() => null)) as unknown
  if (!response.ok) {
    throw new ImageGenError(extractRewriteError(payload, response), response.status)
  }

  const text = parseRewriteResponse(protocol, payload).trim()
  if (!text) throw new ImageGenError('模型未返回有效文本内容')
  return { text, protocol }
}

/** Robustly parses JSON storyboard shots emitted by an AI Director model. */
export function parseStoryboardShotsFromText(rawText: string): StoryboardShot[] {
  const cleaned = rawText
    .trim()
    .replace(/^```(?:json)?\s*/i, '')
    .replace(/\s*```$/i, '')
    .trim()

  const jsonStart = cleaned.indexOf('[')
  const jsonEnd = cleaned.lastIndexOf(']')
  let parsed: unknown = null

  if (jsonStart !== -1 && jsonEnd > jsonStart) {
    try {
      parsed = JSON.parse(cleaned.slice(jsonStart, jsonEnd + 1))
    } catch {
      parsed = null
    }
  }

  if (!Array.isArray(parsed)) {
    const objStart = cleaned.indexOf('{')
    const objEnd = cleaned.lastIndexOf('}')
    if (objStart !== -1 && objEnd > objStart) {
      try {
        const obj = JSON.parse(cleaned.slice(objStart, objEnd + 1)) as { shots?: unknown }
        if (Array.isArray(obj.shots)) parsed = obj.shots
      } catch {
        parsed = null
      }
    }
  }

  if (!Array.isArray(parsed) || parsed.length === 0) {
    // Graceful line-by-line fallback if the model returned numbered paragraphs.
    const lines = rawText
      .split(/\n+/)
      .map((line) => line.replace(/^\s*(?:shot|分镜|镜头)?\s*\d+[.:：\-\s]*/i, '').trim())
      .filter((line) => line.length >= 6)
      .slice(0, 8)
    if (!lines.length) {
      throw new ImageGenError('AI 返回的分镜内容为空，请重试或补充剧情大纲')
    }
    return lines.map((line, idx) => ({
      id: createNodeId('shot'),
      index: idx + 1,
      scene: `场景 ${idx + 1}`,
      title: `分镜 ${idx + 1}`,
      visualPrompt: line,
      cameraMotion: '缓推电影运镜',
      dialogue: '',
      durationSec: 5,
      imageDataUrl: '',
      videoUrl: '',
    }))
  }

  return parsed
    .filter((entry): entry is Record<string, unknown> => Boolean(entry) && typeof entry === 'object')
    .slice(0, 12)
    .map((entry, idx) => {
      const duration = Number(entry.durationSec ?? entry.duration ?? 5)
      return {
        id: createNodeId('shot'),
        index: idx + 1,
        scene: String(entry.scene ?? `场景 ${idx + 1}`).trim() || `场景 ${idx + 1}`,
        title: String(entry.title ?? `分镜 ${idx + 1}`).trim() || `分镜 ${idx + 1}`,
        visualPrompt: String(entry.visualPrompt ?? entry.prompt ?? entry.visual ?? '').trim(),
        cameraMotion: String(entry.cameraMotion ?? entry.camera ?? '缓推镜头').trim(),
        dialogue: String(entry.dialogue ?? entry.narration ?? '').trim(),
        durationSec: Number.isFinite(duration) && duration > 0 ? Math.min(15, Math.max(2, Math.round(duration))) : 5,
        imageDataUrl: '',
        videoUrl: '',
      }
    })
    .filter((shot) => Boolean(shot.visualPrompt))
}

/** Asks the user's selected text model to break a story idea into structured storyboard shots. */
export async function generateStoryboardWithAI(options: {
  apiKey: string
  platform: string
  model: string
  synopsis: string
  shotCount?: number
  signal?: AbortSignal
}): Promise<StoryboardShot[]> {
  const count = Math.max(2, Math.min(10, Math.round(options.shotCount ?? 4)))
  const systemPrompt =
    '你是专业漫剧/短剧 AI 分镜导演。请把用户给的故事创意拆解为可直接用于生图和图生视频的分镜 JSON 数组，不要输出多余解释。每项包含字段：scene（场景名）、title（镜头标题）、visualPrompt（详细中文或双语画面生图提示词，包含主体、服装、环境、光影、画风）、cameraMotion（运镜与动作描述）、dialogue（旁白或台词，可为空）、durationSec（2到10之间的整数秒数）。'
  const userPrompt = `请为以下创意生成 ${count} 个连贯分镜（仅返回 JSON 数组）：\n\n${options.synopsis.trim()}`
  const result = await requestCreativeText({
    apiKey: options.apiKey,
    platform: options.platform,
    model: options.model,
    systemPrompt,
    userPrompt,
    signal: options.signal,
  })
  return parseStoryboardShotsFromText(result.text)
}

/** Executes an ImageGen canvas node via Sub2API `/v1/images/generations` or `/v1/images/edits`. */
export async function requestCanvasImage(options: {
  apiKey: string
  nodeData: ImageGenNodeData
  upstreamPrompt: string
  upstreamImages: string[]
  signal: AbortSignal
}): Promise<string[]> {
  const key = cleanApiKey(options.apiKey)
  if (!key) throw new ImageGenError('请先在顶部选择生图密钥')
  assertHeaderSafeKey(key)

  const rawPrompt = [options.upstreamPrompt.trim(), options.nodeData.prompt.trim()]
    .filter(Boolean)
    .join('\n')
  if (!rawPrompt) throw new ImageGenError('请填写提示词或连接上游文本节点')

  const model = options.nodeData.model.trim()
  if (!model) throw new ImageGenError('请填写图片模型名称')

  const prompt = buildPromptPayload(rawPrompt, options.nodeData.style, {
    enabled: false,
    selections: [],
    category: 'portrait',
    group: 'studio',
    preset: '',
    intensity: 'medium',
    subject: 'general',
  })

  const references: File[] = []
  for (let i = 0; i < Math.min(options.upstreamImages.length, 8); i++) {
    const src = options.upstreamImages[i]
    if (!src) continue
    try {
      const file = await dataUrlToFile(src, `canvas-ref-${i + 1}.png`)
      references.push(file)
    } catch {
      // Skip non-fetchable reference if remote CORS blocks conversion.
    }
  }

  return requestImages({
    key,
    payload: {
      model,
      prompt,
      size: options.nodeData.size || '1024x1024',
      n: Math.max(1, Math.min(4, Math.round(options.nodeData.count || 1))),
      quality: options.nodeData.quality || undefined,
      output_format: options.nodeData.format || 'png',
    },
    references,
    mask: null,
    format: options.nodeData.format || 'png',
    signal: options.signal,
  })
}

/** Resolves which Sub2API video wire protocol to use for a given model & key platform. */
export function resolveVideoProtocol(
  requested: VideoProtocol,
  model: string,
  platform: string,
): Exclude<VideoProtocol, 'auto'> {
  if (requested !== 'auto') return requested
  const lowerModel = model.trim().toLowerCase()
  const lowerPlatform = platform.trim().toLowerCase()
  if (lowerModel.includes('seedance') || lowerModel.includes('doubao')) return 'seedance'
  if (lowerPlatform === 'grok' || lowerModel.includes('grok')) return 'grok'
  return 'compatible'
}

export interface VideoTaskPollUpdate {
  taskId: string
  status: string
}

function extractVideoUrlFromPayload(payload: unknown): string {
  if (!payload || typeof payload !== 'object') return ''
  const record = payload as Record<string, unknown>

  const directCandidates = [
    record.video_url,
    record.url,
    record.output_url,
    (record.video as Record<string, unknown> | undefined)?.url,
    (record.content as Record<string, unknown> | undefined)?.video_url,
    (record.output as Record<string, unknown> | undefined)?.video_url,
    (record.output as Record<string, unknown> | undefined)?.url,
    (record.result as Record<string, unknown> | undefined)?.video_url,
    (record.result as Record<string, unknown> | undefined)?.url,
  ]
  for (const candidate of directCandidates) {
    if (typeof candidate === 'string' && candidate.trim()) return candidate.trim()
  }

  if (Array.isArray(record.data)) {
    for (const item of record.data) {
      if (item && typeof item === 'object') {
        const url = (item as Record<string, unknown>).url
        if (typeof url === 'string' && url.trim()) return url.trim()
      }
    }
  }
  return ''
}

function extractVideoStatus(payload: unknown): string {
  if (!payload || typeof payload !== 'object') return ''
  const record = payload as Record<string, unknown>
  const raw = record.status ?? record.state ?? ''
  return typeof raw === 'string' ? raw.trim().toLowerCase() : ''
}

function sleepWithAbort(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal.addEventListener('abort', onAbort, { once: true })
  })
}

/**
 * Submits and polls an asynchronous video generation task strictly from the
 * browser against Sub2API's existing gateway routes:
 *  - `grok`:       POST /v1/videos/generations -> GET /v1/videos/generations/:id (+ /content)
 *  - `compatible`: POST /v1/video/generations  -> GET /v1/video/generations/:id
 *  - `seedance`:   POST /api/v3/contents/generations/tasks -> GET /api/v3/contents/generations/tasks/:id
 */
export async function requestCreativeVideo(options: {
  apiKey: string
  platform: string
  nodeData: VideoGenNodeData
  upstreamPrompt: string
  upstreamImages: string[]
  signal: AbortSignal
  pollIntervalMs?: number
  maxPolls?: number
  onUpdate?: (update: VideoTaskPollUpdate) => void
}): Promise<{ taskId: string; videoUrl: string; protocol: Exclude<VideoProtocol, 'auto'> }> {
  const key = cleanApiKey(options.apiKey)
  if (!key) throw new ImageGenError('请先在顶部选择视频密钥（或生图密钥）')
  assertHeaderSafeKey(key)

  const model = options.nodeData.model.trim()
  if (!model) throw new ImageGenError('请填写视频模型名称')

  const prompt = [options.upstreamPrompt.trim(), options.nodeData.prompt.trim()]
    .filter(Boolean)
    .join('\n')
  if (!prompt && options.upstreamImages.length === 0) {
    throw new ImageGenError('请填写视频提示词或连接上游提示词 / 首帧图片节点')
  }

  const protocol = resolveVideoProtocol(options.nodeData.protocol, model, options.platform)
  const firstFrame = options.upstreamImages[0] || ''

  let createPath: string
  let createBody: Record<string, unknown>

  if (protocol === 'seedance') {
    createPath = '/api/v3/contents/generations/tasks'
    const content: Array<Record<string, unknown>> = []
    if (prompt) content.push({ type: 'text', text: prompt })
    if (firstFrame) {
      content.push({
        type: 'image_url',
        image_url: { url: firstFrame },
        role: 'first_frame',
      })
    }
    createBody = {
      model,
      content,
      duration: options.nodeData.duration || 5,
    }
  } else if (protocol === 'grok') {
    createPath = '/v1/videos/generations'
    createBody = {
      model,
      prompt,
      duration: options.nodeData.duration || 5,
      aspect_ratio: options.nodeData.aspectRatio || '16:9',
      resolution: options.nodeData.resolution || '720p',
      ...(firstFrame ? { image: { url: firstFrame } } : {}),
    }
  } else {
    createPath = '/v1/video/generations'
    createBody = {
      model,
      prompt,
      duration: options.nodeData.duration || 5,
      aspect_ratio: options.nodeData.aspectRatio || '16:9',
      resolution: options.nodeData.resolution || '720p',
      ...(firstFrame ? { image_url: firstFrame } : {}),
    }
  }

  const createRes = await fetch(gatewayUrl(createPath), {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${key}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(createBody),
    signal: options.signal,
  })
  const createPayload = (await createRes.json().catch(() => null)) as Record<string, unknown> | null
  if (!createRes.ok) {
    throw new ImageGenError(extractRewriteError(createPayload, createRes), createRes.status)
  }

  const immediateUrl = extractVideoUrlFromPayload(createPayload)
  const taskId = String(
    createPayload?.request_id ?? createPayload?.id ?? createPayload?.task_id ?? '',
  ).trim()

  if (immediateUrl) {
    return { taskId: taskId || 'sync', videoUrl: immediateUrl, protocol }
  }
  if (!taskId) {
    throw new ImageGenError('视频网关未返回任务 ID 或成片链接')
  }

  options.onUpdate?.({ taskId, status: 'submitted' })

  const pollPath =
    protocol === 'seedance'
      ? `/api/v3/contents/generations/tasks/${encodeURIComponent(taskId)}`
      : protocol === 'grok'
        ? `/v1/videos/generations/${encodeURIComponent(taskId)}`
        : `/v1/video/generations/${encodeURIComponent(taskId)}`

  const pollIntervalMs = options.pollIntervalMs ?? 3000
  const maxPolls = options.maxPolls ?? 120

  for (let attempt = 0; attempt < maxPolls; attempt++) {
    await sleepWithAbort(pollIntervalMs, options.signal)
    const pollRes = await fetch(gatewayUrl(pollPath), {
      method: 'GET',
      headers: { Authorization: `Bearer ${key}` },
      signal: options.signal,
    })
    const pollPayload = (await pollRes.json().catch(() => null)) as Record<string, unknown> | null
    if (!pollRes.ok) {
      throw new ImageGenError(extractRewriteError(pollPayload, pollRes), pollRes.status)
    }

    const status = extractVideoStatus(pollPayload)
    options.onUpdate?.({ taskId, status: status || 'processing' })

    const directUrl = extractVideoUrlFromPayload(pollPayload)
    if (directUrl) {
      return { taskId, videoUrl: directUrl, protocol }
    }

    if (['failed', 'error', 'cancelled', 'canceled', 'expired'].includes(status)) {
      throw new ImageGenError(
        extractRewriteError(pollPayload, pollRes) || `视频任务失败（${status}）`,
      )
    }

    if (['succeeded', 'completed', 'done', 'success'].includes(status)) {
      if (protocol === 'grok') {
        const contentRes = await fetch(
          gatewayUrl(`/v1/videos/generations/${encodeURIComponent(taskId)}/content`),
          {
            method: 'GET',
            headers: { Authorization: `Bearer ${key}` },
            signal: options.signal,
          },
        )
        if (!contentRes.ok) {
          throw new ImageGenError(`拉取视频内容失败（${contentRes.status}）`, contentRes.status)
        }
        const blob = await contentRes.blob()
        return { taskId, videoUrl: URL.createObjectURL(blob), protocol }
      }
      throw new ImageGenError('视频任务已完成，但上游响应中未包含视频 URL')
    }
  }

  throw new ImageGenError('视频生成轮询超时，请稍后重试')
}

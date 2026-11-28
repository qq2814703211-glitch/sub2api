import { keysAPI } from '@/api'
import { cleanApiKey, gatewayUrl } from '@/features/imagegen/api'
import {
  LEGACY_REWRITE_MODEL_KEY,
  REWRITE_CONFIG_STORAGE_KEY,
} from '@/features/imagegen/rewriteConfig'
import { safeGet } from '@/features/imagegen/storage'
import type { ApiKey } from '@/types'

export const TOONFLOW_BRIDGE_STORAGE_KEY = 'sub2api.creative.toonflow.hostContext.v1'
export const CREATIVE_KEY_BINDINGS_STORAGE_KEY = 'sub2api.creative.keyBindings.v1'
const IMAGEGEN_API_KEY_STORAGE = 'ig_apiKey'

export type CreativeKeyRole = 'text' | 'image' | 'video' | 'audio'

export interface CreativeKeyBindings {
  textKeyId: number | null
  imageKeyId: number | null
  videoKeyId: number | null
  audioKeyId: number | null
  rewriteModel?: string
  rewriteEffort?: string
}

export interface DiscoveredModelMeta {
  id: string
  supportsReasoningEffort?: boolean
  reasoningEffort?: string
  reasoningEfforts?: { value: string; label?: string; default?: boolean }[]
}

export interface QuickImageBridgePayload {
  prompt?: string
  model?: string
  size?: string
  imageDataUrl?: string
  title?: string
}

export interface Sub2ApiToonflowKeyInfo {
  id: number
  name: string
  key: string
  platform?: string
  groupName?: string
  models?: string[]
}

export interface Sub2ApiToonflowUserInfo {
  id: number
  email: string
  username?: string
  role?: string
}

export interface Sub2ApiToonflowContext {
  origin: string
  locale: 'zh-CN' | 'en'
  theme: 'dark' | 'light'
  siteName?: string
  siteLogo?: string
  user: Sub2ApiToonflowUserInfo | null
  keys: Sub2ApiToonflowKeyInfo[]
  selectedTextKeyId: number | null
  selectedImageKeyId: number | null
  selectedVideoKeyId: number | null
  selectedAudioKeyId: number | null
  rewriteModel?: string
  rewriteEffort?: string
  discoveredModels: Record<number, string[]>
  discoveredModelMeta?: Record<number, DiscoveredModelMeta[]>
}

function readLegacyRewriteSeed(): { rewriteModel: string; rewriteEffort: string; rewriteKeyId: number | null } {
  let rewriteModel = ''
  let rewriteEffort = 'unset'
  let rewriteKeyId: number | null = null
  try {
    const raw = safeGet(REWRITE_CONFIG_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as {
        model?: unknown
        effort?: unknown
        keyId?: unknown
      }
      if (typeof parsed.model === 'string') rewriteModel = parsed.model.trim()
      if (typeof parsed.effort === 'string' && parsed.effort.trim()) rewriteEffort = parsed.effort.trim()
      if (typeof parsed.keyId === 'number' && Number.isFinite(parsed.keyId) && parsed.keyId > 0) {
        rewriteKeyId = parsed.keyId
      }
    }
  } catch {
    // ignore malformed legacy config
  }
  if (!rewriteModel) {
    const legacy = safeGet(LEGACY_REWRITE_MODEL_KEY)
    if (legacy) rewriteModel = legacy.trim()
  }
  return { rewriteModel, rewriteEffort, rewriteKeyId }
}

export function findInheritedImageGenKeyId(keys: ApiKey[]): number | null {
  const storedKey = cleanApiKey(safeGet(IMAGEGEN_API_KEY_STORAGE) ?? '')
  if (!storedKey) return null
  const matched = keys.find((item) => cleanApiKey(item.key) === storedKey)
  return matched ? matched.id : null
}

export function loadCreativeKeyBindings(fallbackKeyId: number | null = null): CreativeKeyBindings {
  const legacy = readLegacyRewriteSeed()
  try {
    const raw = localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<CreativeKeyBindings>
      return {
        textKeyId: typeof parsed.textKeyId === 'number' ? parsed.textKeyId : legacy.rewriteKeyId ?? fallbackKeyId,
        imageKeyId: typeof parsed.imageKeyId === 'number' ? parsed.imageKeyId : fallbackKeyId,
        videoKeyId: typeof parsed.videoKeyId === 'number' ? parsed.videoKeyId : fallbackKeyId,
        audioKeyId: typeof parsed.audioKeyId === 'number' ? parsed.audioKeyId : fallbackKeyId,
        rewriteModel: typeof parsed.rewriteModel === 'string' ? parsed.rewriteModel : legacy.rewriteModel,
        rewriteEffort: typeof parsed.rewriteEffort === 'string' && parsed.rewriteEffort ? parsed.rewriteEffort : legacy.rewriteEffort,
      }
    }
  } catch {
    // ignore invalid JSON
  }
  return {
    textKeyId: legacy.rewriteKeyId ?? fallbackKeyId,
    imageKeyId: fallbackKeyId,
    videoKeyId: fallbackKeyId,
    audioKeyId: fallbackKeyId,
    rewriteModel: legacy.rewriteModel,
    rewriteEffort: legacy.rewriteEffort,
  }
}

export function saveCreativeKeyBindings(bindings: CreativeKeyBindings): void {
  try {
    localStorage.setItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY, JSON.stringify(bindings))
  } catch {
    // ignore storage errors
  }
}

export async function listActiveUserApiKeys(): Promise<ApiKey[]> {
  try {
    const resp = await keysAPI.list(1, 100)
    const items = Array.isArray(resp?.items) ? resp.items : []
    return items.filter((item) => item && item.status !== 'inactive' && Boolean(cleanApiKey(item.key)))
  } catch {
    return []
  }
}

function normalizeEffortEntries(
  raw: unknown,
): { value: string; label?: string; default?: boolean }[] | undefined {
  if (!Array.isArray(raw)) return undefined
  const list: { value: string; label?: string; default?: boolean }[] = []
  for (const item of raw) {
    if (typeof item === 'string' && item.trim()) {
      list.push({ value: item.trim() })
    } else if (item && typeof item === 'object') {
      const rec = item as { value?: unknown; label?: unknown; default?: unknown }
      const value = String(rec.value ?? '').trim()
      if (!value) continue
      list.push({
        value,
        ...(typeof rec.label === 'string' ? { label: rec.label } : {}),
        ...(rec.default === true ? { default: true } : {}),
      })
    }
  }
  return list.length ? list : undefined
}

export async function discoverModelDetailsForKey(
  rawKey: string,
  signal?: AbortSignal,
): Promise<{ models: string[]; meta: DiscoveredModelMeta[] }> {
  const key = cleanApiKey(rawKey)
  if (!key) return { models: [], meta: [] }
  try {
    const response = await fetch(gatewayUrl('/v1/models'), {
      method: 'GET',
      headers: { Authorization: `Bearer ${key}` },
      signal,
    })
    if (!response.ok) return { models: [], meta: [] }
    const payload = (await response.json().catch(() => null)) as {
      data?: Array<Record<string, unknown>>
    } | null
    const seen = new Set<string>()
    const meta: DiscoveredModelMeta[] = []
    for (const entry of payload?.data ?? []) {
      const id = String(entry?.id ?? entry?.model ?? '').trim()
      if (!id || seen.has(id)) continue
      seen.add(id)
      const supportsRaw = entry?.supports_reasoning_effort ?? entry?.supportsReasoningEffort
      const effortRaw = entry?.reasoning_effort ?? entry?.reasoningEffort
      const effortsRaw = entry?.reasoning_efforts ?? entry?.reasoningEfforts
      const reasoningEfforts = normalizeEffortEntries(effortsRaw)
      meta.push({
        id,
        ...(typeof supportsRaw === 'boolean' ? { supportsReasoningEffort: supportsRaw } : {}),
        ...(typeof effortRaw === 'string' && effortRaw.trim() ? { reasoningEffort: effortRaw.trim() } : {}),
        ...(reasoningEfforts ? { reasoningEfforts } : {}),
      })
    }
    meta.sort((a, b) => a.id.localeCompare(b.id))
    return {
      models: meta.map((item) => item.id),
      meta,
    }
  } catch {
    return { models: [], meta: [] }
  }
}

export async function discoverModelsForKey(rawKey: string, signal?: AbortSignal): Promise<string[]> {
  const result = await discoverModelDetailsForKey(rawKey, signal)
  return result.models
}

export function serializeKeysForToonflow(keys: ApiKey[]): Sub2ApiToonflowKeyInfo[] {
  return keys.map((item) => ({
    id: item.id,
    name: item.name || `#${item.id}`,
    key: cleanApiKey(item.key),
    platform: item.group?.platform || '',
    groupName: item.group?.name || '',
  }))
}

export function writeToonflowHostContext(context: Sub2ApiToonflowContext): void {
  try {
    localStorage.setItem(TOONFLOW_BRIDGE_STORAGE_KEY, JSON.stringify(context))
  } catch {
    // ignore storage errors
  }
}

export function postToonflowMessage(
  iframe: HTMLIFrameElement | null | undefined,
  type: 'syncContext' | 'importMedia' | 'navigate',
  payload: unknown,
): void {
  const win = iframe?.contentWindow
  if (!win) return
  try {
    win.postMessage({ source: 'sub2api-host', type, payload }, window.location.origin)
  } catch {
    // ignore cross-frame messaging errors during load
  }
}

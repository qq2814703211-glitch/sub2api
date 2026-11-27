import { keysAPI } from '@/api'
import { cleanApiKey, gatewayUrl } from '@/features/imagegen/api'
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
  balance?: number
}

export interface Sub2ApiToonflowContext {
  origin: string
  locale: 'zh-CN' | 'en'
  theme: 'dark' | 'light'
  user: Sub2ApiToonflowUserInfo | null
  keys: Sub2ApiToonflowKeyInfo[]
  selectedTextKeyId: number | null
  selectedImageKeyId: number | null
  selectedVideoKeyId: number | null
  selectedAudioKeyId: number | null
  discoveredModels: Record<number, string[]>
}

export function findInheritedImageGenKeyId(keys: ApiKey[]): number | null {
  const storedKey = cleanApiKey(safeGet(IMAGEGEN_API_KEY_STORAGE) ?? '')
  if (!storedKey) return null
  const matched = keys.find((item) => cleanApiKey(item.key) === storedKey)
  return matched ? matched.id : null
}

export function loadCreativeKeyBindings(fallbackKeyId: number | null = null): CreativeKeyBindings {
  try {
    const raw = localStorage.getItem(CREATIVE_KEY_BINDINGS_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<CreativeKeyBindings>
      return {
        textKeyId: typeof parsed.textKeyId === 'number' ? parsed.textKeyId : fallbackKeyId,
        imageKeyId: typeof parsed.imageKeyId === 'number' ? parsed.imageKeyId : fallbackKeyId,
        videoKeyId: typeof parsed.videoKeyId === 'number' ? parsed.videoKeyId : fallbackKeyId,
        audioKeyId: typeof parsed.audioKeyId === 'number' ? parsed.audioKeyId : fallbackKeyId,
      }
    }
  } catch {
    // ignore invalid JSON
  }
  return {
    textKeyId: fallbackKeyId,
    imageKeyId: fallbackKeyId,
    videoKeyId: fallbackKeyId,
    audioKeyId: fallbackKeyId,
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

export async function discoverModelsForKey(rawKey: string, signal?: AbortSignal): Promise<string[]> {
  const key = cleanApiKey(rawKey)
  if (!key) return []
  try {
    const response = await fetch(gatewayUrl('/v1/models'), {
      method: 'GET',
      headers: { Authorization: `Bearer ${key}` },
      signal,
    })
    if (!response.ok) return []
    const payload = (await response.json().catch(() => null)) as {
      data?: Array<{ id?: unknown; model?: unknown }>
    } | null
    const seen = new Set<string>()
    const models: string[] = []
    for (const entry of payload?.data ?? []) {
      const id = String(entry?.id ?? entry?.model ?? '').trim()
      if (!id || seen.has(id)) continue
      seen.add(id)
      models.push(id)
    }
    models.sort((a, b) => a.localeCompare(b))
    return models
  } catch {
    return []
  }
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

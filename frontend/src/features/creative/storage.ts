/**
 * Pure browser-local persistence for Creative Plaza (创意广场).
 *
 * All projects, infinite-canvas graphs, storyboard scripts, and local assets
 * are persisted exclusively inside the user's browser IndexedDB (with a safe
 * in-memory fallback for environments without IndexedDB such as jsdom tests or
 * strict private browsing). Nothing is ever uploaded to or stored on the
 * Sub2API server.
 */
import type {
  CreativeAsset,
  CreativeKeyBindings,
  CreativeProject,
  ToonflowExportBundle,
} from './types'
import { createNodeId } from './graph'

const DB_NAME = 'sub2apiCreativePlazaDB'
const DB_VERSION = 1
const PROJECTS_STORE = 'projects'
const ASSETS_STORE = 'assets'

const ACTIVE_PROJECT_KEY = 'cp_activeProjectId'
const KEY_BINDINGS_KEY = 'cp_keyBindings'

const memoryProjects = new Map<string, CreativeProject>()
const memoryAssets = new Map<string, CreativeAsset>()

function hasIndexedDb(): boolean {
  return typeof indexedDB !== 'undefined' && indexedDB !== null
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(PROJECTS_STORE)) {
        db.createObjectStore(PROJECTS_STORE, { keyPath: 'id' })
      }
      if (!db.objectStoreNames.contains(ASSETS_STORE)) {
        db.createObjectStore(ASSETS_STORE, { keyPath: 'id' })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

async function withStore<T>(
  storeName: string,
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openDb()
  return new Promise<T>((resolve, reject) => {
    const tx = db.transaction(storeName, mode)
    const request = run(tx.objectStore(storeName))
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

function safeLocalGet(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function safeLocalSet(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Ignore storage quota or private mode errors.
  }
}

export function loadActiveProjectId(): string | null {
  return safeLocalGet(ACTIVE_PROJECT_KEY)
}

export function saveActiveProjectId(id: string): void {
  safeLocalSet(ACTIVE_PROJECT_KEY, id)
}

export function loadKeyBindings(): CreativeKeyBindings {
  const raw = safeLocalGet(KEY_BINDINGS_KEY)
  if (!raw) return { imageKeyId: null, textKeyId: null, videoKeyId: null }
  try {
    const parsed = JSON.parse(raw) as Partial<CreativeKeyBindings>
    const parseId = (v: unknown): number | null =>
      typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : null
    return {
      imageKeyId: parseId(parsed.imageKeyId),
      textKeyId: parseId(parsed.textKeyId),
      videoKeyId: parseId(parsed.videoKeyId),
    }
  } catch {
    return { imageKeyId: null, textKeyId: null, videoKeyId: null }
  }
}

export function saveKeyBindings(bindings: CreativeKeyBindings): void {
  safeLocalSet(KEY_BINDINGS_KEY, JSON.stringify(bindings))
}

export async function listLocalProjects(): Promise<CreativeProject[]> {
  if (!hasIndexedDb()) {
    return [...memoryProjects.values()].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
  }
  try {
    const rows = await withStore<CreativeProject[]>(PROJECTS_STORE, 'readonly', (store) =>
      store.getAll() as IDBRequest<CreativeProject[]>,
    )
    return Array.isArray(rows)
      ? rows.sort((a, b) => String(b.updatedAt || '').localeCompare(String(a.updatedAt || '')))
      : []
  } catch {
    return [...memoryProjects.values()]
  }
}

export async function saveLocalProject(project: CreativeProject): Promise<void> {
  const snapshot = JSON.parse(JSON.stringify(project)) as CreativeProject
  memoryProjects.set(snapshot.id, snapshot)
  if (!hasIndexedDb()) return
  try {
    await withStore(PROJECTS_STORE, 'readwrite', (store) => store.put(snapshot))
  } catch {
    // Keep in-memory copy if browser blocks IndexedDB writes.
  }
}

export async function deleteLocalProject(projectId: string): Promise<void> {
  memoryProjects.delete(projectId)
  if (!hasIndexedDb()) return
  try {
    await withStore(PROJECTS_STORE, 'readwrite', (store) => store.delete(projectId))
  } catch {
    // Ignore delete failures.
  }
}

export async function listLocalAssets(): Promise<CreativeAsset[]> {
  if (!hasIndexedDb()) {
    return [...memoryAssets.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  }
  try {
    const rows = await withStore<CreativeAsset[]>(ASSETS_STORE, 'readonly', (store) =>
      store.getAll() as IDBRequest<CreativeAsset[]>,
    )
    return Array.isArray(rows)
      ? rows.sort((a, b) => String(b.createdAt || '').localeCompare(String(a.createdAt || '')))
      : []
  } catch {
    return [...memoryAssets.values()]
  }
}

export async function saveLocalAsset(asset: CreativeAsset): Promise<void> {
  const snapshot = JSON.parse(JSON.stringify(asset)) as CreativeAsset
  memoryAssets.set(snapshot.id, snapshot)
  if (!hasIndexedDb()) return
  try {
    await withStore(ASSETS_STORE, 'readwrite', (store) => store.put(snapshot))
  } catch {
    // Keep in-memory copy if browser blocks IndexedDB writes.
  }
}

export async function deleteLocalAsset(assetId: string): Promise<void> {
  memoryAssets.delete(assetId)
  if (!hasIndexedDb()) return
  try {
    await withStore(ASSETS_STORE, 'readwrite', (store) => store.delete(assetId))
  } catch {
    // Ignore delete failures.
  }
}

export async function clearAllLocalAssets(): Promise<void> {
  memoryAssets.clear()
  if (!hasIndexedDb()) return
  try {
    await withStore(ASSETS_STORE, 'readwrite', (store) => store.clear())
  } catch {
    // Ignore clear failures.
  }
}

/** Estimates browser local storage usage so users see zero-server-footprint transparency. */
export async function estimateLocalUsageBytes(
  projects: readonly CreativeProject[],
  assets: readonly CreativeAsset[],
): Promise<{ usedBytes: number; quotaBytes: number | null }> {
  const fallbackBytes =
    new Blob([JSON.stringify(projects)]).size + new Blob([JSON.stringify(assets)]).size
  try {
    if (typeof navigator !== 'undefined' && navigator.storage?.estimate) {
      const estimate = await navigator.storage.estimate()
      return {
        usedBytes: typeof estimate.usage === 'number' ? estimate.usage : fallbackBytes,
        quotaBytes: typeof estimate.quota === 'number' ? estimate.quota : null,
      }
    }
  } catch {
    // Ignore navigator.storage unavailability.
  }
  return { usedBytes: fallbackBytes, quotaBytes: null }
}

/** Formats byte counts into compact human-readable strings (KB / MB / GB). */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  if (bytes < 1024) return `${Math.round(bytes)} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

/** Builds a portable `.toonflow.json` bundle for local backup / sharing. */
export function buildProjectExportBundle(
  project: CreativeProject,
  assets: readonly CreativeAsset[] = [],
): ToonflowExportBundle {
  return {
    toonflowCanvas: true,
    format: 'sub2api-creative-plaza-v1',
    exportedAt: new Date().toISOString(),
    project: JSON.parse(JSON.stringify(project)) as CreativeProject,
    assets: JSON.parse(JSON.stringify(assets)) as CreativeAsset[],
  }
}

/**
 * Validates and imports a `.toonflow.json` project bundle (supports both our
 * Creative Plaza bundle and bare Toonflow canvas JSON with `nodes` / `edges`).
 */
export function parseProjectImportBundle(rawText: string): {
  project: CreativeProject
  assets: CreativeAsset[]
} {
  let parsed: unknown
  try {
    parsed = JSON.parse(rawText)
  } catch {
    throw new Error('工程文件不是有效的 JSON 格式')
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('工程文件结构不正确')
  }

  const record = parsed as Record<string, unknown>
  const now = new Date().toISOString()

  if (record.project && typeof record.project === 'object' && !Array.isArray(record.project)) {
    const rawProj = record.project as Partial<CreativeProject>
    const project: CreativeProject = {
      id: createNodeId('proj'),
      name: String(rawProj.name || '导入的创意工程').trim() || '导入的创意工程',
      description: String(rawProj.description || ''),
      createdAt: rawProj.createdAt || now,
      updatedAt: now,
      viewport: rawProj.viewport && typeof rawProj.viewport.zoom === 'number'
        ? rawProj.viewport
        : { x: 0, y: 0, zoom: 0.9 },
      nodes: Array.isArray(rawProj.nodes) ? rawProj.nodes : [],
      edges: Array.isArray(rawProj.edges) ? rawProj.edges : [],
      scriptSynopsis: String(rawProj.scriptSynopsis || ''),
      shots: Array.isArray(rawProj.shots) ? rawProj.shots : [],
    }
    const assets = Array.isArray(record.assets) ? (record.assets as CreativeAsset[]) : []
    return { project, assets }
  }

  // Compatibility with raw Toonflow canvas JSON (`{ toonflowCanvas: true, nodes, edges }`)
  if (Array.isArray(record.nodes)) {
    const project: CreativeProject = {
      id: createNodeId('proj'),
      name: String(record.name || 'Toonflow 导入画布'),
      description: '从 Toonflow JSON 导入的本地画布工程',
      createdAt: now,
      updatedAt: now,
      viewport: { x: 0, y: 0, zoom: 0.9 },
      nodes: record.nodes as CreativeProject['nodes'],
      edges: Array.isArray(record.edges) ? (record.edges as CreativeProject['edges']) : [],
      scriptSynopsis: '',
      shots: [],
    }
    return { project, assets: [] }
  }

  throw new Error('未识别到有效的 Toonflow / 创意广场工程数据')
}

export function resetMemoryStorageForTests(): void {
  memoryProjects.clear()
  memoryAssets.clear()
}

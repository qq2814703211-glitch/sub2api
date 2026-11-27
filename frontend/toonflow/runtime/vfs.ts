import { zipSync, unzipSync } from "fflate";

export interface VfsNode {
  path: string;
  kind: "file" | "directory";
  content?: Uint8Array;
  mimeType?: string;
  revision: string;
  updatedAt: number;
}

export interface WorkspaceEntry {
  name: string;
  path: string;
  type: "file" | "directory";
}

export interface AssetTreeEntry {
  name: string;
  path: string;
  type: "file" | "directory";
  children?: AssetTreeEntry[];
}

const DB_NAME = "toonflow-browser-vfs-v1";
const DB_VERSION = 1;
const STORE_FILES = "files";
const STORE_KV = "kv";

export const WORKSPACES_ROOT = "/workspaces";
export const SCENES_ROOT = "/scenes";
export const ASSETS_ROOT = "/assets";
export const DEFAULT_WORKSPACE_DIR = `${WORKSPACES_ROOT}/雨夜便利店·短剧分镜`;
export const DEFAULT_SCENE_DIR = `${SCENES_ROOT}/雨夜街角·白模片场`;

let dbPromise: Promise<IDBDatabase> | null = null;
let seededPromise: Promise<void> | null = null;
const memoryFiles = new Map<string, VfsNode>();
const memoryKv = new Map<string, unknown>();

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8");

export function normalizePath(input: string): string {
  const cleaned = input.replaceAll("\\", "/").replace(/\/+/g, "/").trim();
  if (!cleaned || cleaned === "/") return "/";
  const parts: string[] = [];
  for (const part of cleaned.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") parts.pop();
    else parts.push(part);
  }
  return "/" + parts.join("/");
}

export function joinPath(base: string, ...segments: string[]): string {
  const joined = [base, ...segments].filter(Boolean).join("/");
  return normalizePath(joined);
}

export function dirnamePath(p: string): string {
  const norm = normalizePath(p);
  if (norm === "/") return "/";
  const idx = norm.lastIndexOf("/");
  return idx <= 0 ? "/" : norm.slice(0, idx);
}

export function basenamePath(p: string): string {
  const norm = normalizePath(p);
  if (norm === "/") return "";
  const idx = norm.lastIndexOf("/");
  return norm.slice(idx + 1);
}

export function guessMimeType(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const table: Record<string, string> = {
    json: "application/json",
    canvas: "application/json",
    md: "text/markdown; charset=utf-8",
    markdown: "text/markdown; charset=utf-8",
    txt: "text/plain; charset=utf-8",
    yaml: "text/yaml; charset=utf-8",
    yml: "text/yaml; charset=utf-8",
    csv: "text/csv; charset=utf-8",
    js: "text/javascript; charset=utf-8",
    ts: "text/typescript; charset=utf-8",
    html: "text/html; charset=utf-8",
    css: "text/css; charset=utf-8",
    svg: "image/svg+xml",
    png: "image/png",
    jpg: "image/jpeg",
    jpeg: "image/jpeg",
    webp: "image/webp",
    gif: "image/gif",
    avif: "image/avif",
    mp4: "video/mp4",
    webm: "video/webm",
    mov: "video/quicktime",
    mp3: "audio/mpeg",
    wav: "audio/wav",
    ogg: "audio/ogg",
    opus: "audio/opus",
    m4a: "audio/mp4",
    zip: "application/zip",
    glb: "model/gltf-binary",
    gltf: "model/gltf+json",
  };
  return table[ext] ?? "application/octet-stream";
}

function makeRevision(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

function openDb(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === "undefined") return Promise.resolve(null);
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, DB_VERSION);
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(STORE_FILES)) {
          db.createObjectStore(STORE_FILES, { keyPath: "path" });
        }
        if (!db.objectStoreNames.contains(STORE_KV)) {
          db.createObjectStore(STORE_KV, { keyPath: "key" });
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }
  return dbPromise.catch(() => null);
}

export async function kvGet<T>(key: string, fallback: T): Promise<T> {
  const db = await openDb();
  if (!db) {
    return memoryKv.has(key) ? (memoryKv.get(key) as T) : fallback;
  }
  return new Promise((resolve) => {
    const tx = db.transaction(STORE_KV, "readonly");
    const req = tx.objectStore(STORE_KV).get(key);
    req.onsuccess = () => {
      const row = req.result as { key: string; value: T } | undefined;
      resolve(row ? row.value : fallback);
    };
    req.onerror = () => resolve(fallback);
  });
}

export async function kvSet<T>(key: string, value: T): Promise<void> {
  memoryKv.set(key, value);
  const db = await openDb();
  if (!db) return;
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_KV, "readwrite");
    tx.objectStore(STORE_KV).put({ key, value });
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function getNodeRaw(path: string): Promise<VfsNode | undefined> {
  const norm = normalizePath(path);
  const db = await openDb();
  if (!db) return memoryFiles.get(norm);
  return new Promise((resolve) => {
    const tx = db.transaction(STORE_FILES, "readonly");
    const req = tx.objectStore(STORE_FILES).get(norm);
    req.onsuccess = () => resolve(req.result as VfsNode | undefined);
    req.onerror = () => resolve(undefined);
  });
}

async function putNodeRaw(node: VfsNode): Promise<void> {
  const norm = normalizePath(node.path);
  const stored: VfsNode = { ...node, path: norm };
  memoryFiles.set(norm, stored);
  const db = await openDb();
  if (!db) return;
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_FILES, "readwrite");
    tx.objectStore(STORE_FILES).put(stored);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function deleteNodeRaw(path: string): Promise<void> {
  const norm = normalizePath(path);
  memoryFiles.delete(norm);
  const db = await openDb();
  if (!db) return;
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_FILES, "readwrite");
    tx.objectStore(STORE_FILES).delete(norm);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function getAllNodesRaw(): Promise<VfsNode[]> {
  const db = await openDb();
  if (!db) return [...memoryFiles.values()];
  return new Promise((resolve) => {
    const tx = db.transaction(STORE_FILES, "readonly");
    const req = tx.objectStore(STORE_FILES).getAll();
    req.onsuccess = () => resolve((req.result as VfsNode[]) ?? []);
    req.onerror = () => resolve([]);
  });
}

export async function ensureDir(path: string): Promise<void> {
  const norm = normalizePath(path);
  if (norm === "/") return;
  const parts = norm.split("/").filter(Boolean);
  let cur = "";
  for (const part of parts) {
    cur += "/" + part;
    const existing = await getNodeRaw(cur);
    if (!existing) {
      await putNodeRaw({
        path: cur,
        kind: "directory",
        revision: makeRevision(),
        updatedAt: Date.now(),
      });
    }
  }
}

export async function vfsExists(path: string): Promise<boolean> {
  await ensureSeeded();
  return Boolean(await getNodeRaw(path));
}

export async function vfsStat(path: string): Promise<VfsNode | undefined> {
  await ensureSeeded();
  return getNodeRaw(path);
}

export async function vfsReadBytes(path: string): Promise<{ bytes: Uint8Array; mimeType: string; revision: string }> {
  await ensureSeeded();
  const node = await getNodeRaw(path);
  if (!node || node.kind !== "file") {
    throw Object.assign(new Error(`文件不存在：${path}`), { status: 404, code: "ENOENT" });
  }
  const bytes = node.content instanceof Uint8Array ? node.content : new Uint8Array(node.content ?? []);
  return {
    bytes,
    mimeType: node.mimeType || guessMimeType(path),
    revision: node.revision,
  };
}

export async function vfsReadText(path: string): Promise<{ text: string; revision: string; encoding: "utf-8" }> {
  const { bytes, revision } = await vfsReadBytes(path);
  return { text: textDecoder.decode(bytes), revision, encoding: "utf-8" };
}

export async function vfsWriteBytes(
  path: string,
  data: Uint8Array | ArrayBuffer | string,
  options?: { exclusive?: boolean; expectedRevision?: string; mimeType?: string }
): Promise<{ revision: string; path: string }> {
  await ensureSeeded();
  const norm = normalizePath(path);
  const existing = await getNodeRaw(norm);
  if (options?.exclusive && existing) {
    throw Object.assign(new Error(`文件已存在：${norm}`), { status: 409, code: "EEXIST" });
  }
  if (options?.expectedRevision && existing && existing.revision !== options.expectedRevision) {
    throw Object.assign(new Error("文件已被修改，请刷新后重试"), { status: 409, code: "CONFLICT" });
  }
  await ensureDir(dirnamePath(norm));
  const bytes =
    typeof data === "string"
      ? textEncoder.encode(data)
      : data instanceof Uint8Array
        ? data
        : new Uint8Array(data);
  const revision = makeRevision();
  await putNodeRaw({
    path: norm,
    kind: "file",
    content: bytes,
    mimeType: options?.mimeType || guessMimeType(norm),
    revision,
    updatedAt: Date.now(),
  });
  return { revision, path: norm };
}

export async function vfsMkdir(path: string, options?: { exclusive?: boolean }): Promise<void> {
  await ensureSeeded();
  const norm = normalizePath(path);
  const existing = await getNodeRaw(norm);
  if (options?.exclusive && existing) {
    throw Object.assign(new Error(`目录已存在：${norm}`), { status: 409, code: "EEXIST" });
  }
  await ensureDir(norm);
}

export async function vfsListDir(dirPath: string): Promise<WorkspaceEntry[]> {
  await ensureSeeded();
  const norm = normalizePath(dirPath);
  const all = await getAllNodesRaw();
  const prefix = norm === "/" ? "/" : norm + "/";
  const direct = new Map<string, WorkspaceEntry>();
  for (const node of all) {
    if (node.path === norm) continue;
    if (!node.path.startsWith(prefix)) continue;
    const rel = node.path.slice(prefix.length);
    if (!rel) continue;
    const firstSlash = rel.indexOf("/");
    const name = firstSlash >= 0 ? rel.slice(0, firstSlash) : rel;
    const childPath = firstSlash >= 0 ? name : rel;
    const type: "file" | "directory" = firstSlash >= 0 ? "directory" : node.kind;
    if (!direct.has(name) || type === "directory") {
      direct.set(name, { name, path: childPath, type });
    }
  }
  return [...direct.values()].sort((a, b) => {
    if (a.type !== b.type) return a.type === "directory" ? -1 : 1;
    return a.name.localeCompare(b.name, "zh-CN");
  });
}

export async function vfsListWorkspaceRel(workspaceDir: string, subPath = ""): Promise<{
  directory: string;
  empty: boolean;
  entries: WorkspaceEntry[];
}> {
  await ensureSeeded();
  const root = normalizePath(workspaceDir);
  await ensureDir(root);
  const target = subPath ? joinPath(root, subPath) : root;
  const rawEntries = await vfsListDir(target);
  const relPrefix = subPath ? normalizePath("/" + subPath).slice(1) : "";
  const entries = rawEntries.map((entry) => ({
    ...entry,
    path: relPrefix ? `${relPrefix}/${entry.name}` : entry.name,
  }));
  return { directory: root, empty: entries.length === 0, entries };
}

export async function vfsRename(oldPath: string, newPath: string): Promise<void> {
  await ensureSeeded();
  const src = normalizePath(oldPath);
  const dst = normalizePath(newPath);
  if (src === dst) return;
  const all = await getAllNodesRaw();
  const matching = all.filter((n) => n.path === src || n.path.startsWith(src + "/"));
  if (!matching.length) {
    throw Object.assign(new Error(`路径不存在：${src}`), { status: 404, code: "ENOENT" });
  }
  await ensureDir(dirnamePath(dst));
  for (const node of matching) {
    const suffix = node.path.slice(src.length);
    const targetPath = normalizePath(dst + suffix);
    await putNodeRaw({ ...node, path: targetPath, revision: makeRevision(), updatedAt: Date.now() });
    await deleteNodeRaw(node.path);
  }
}

export async function vfsCopy(oldPath: string, newPath: string): Promise<void> {
  await ensureSeeded();
  const src = normalizePath(oldPath);
  const dst = normalizePath(newPath);
  const all = await getAllNodesRaw();
  const matching = all.filter((n) => n.path === src || n.path.startsWith(src + "/"));
  if (!matching.length) {
    throw Object.assign(new Error(`路径不存在：${src}`), { status: 404, code: "ENOENT" });
  }
  await ensureDir(dirnamePath(dst));
  for (const node of matching) {
    const suffix = node.path.slice(src.length);
    const targetPath = normalizePath(dst + suffix);
    const clonedBytes = node.content ? new Uint8Array(node.content) : undefined;
    await putNodeRaw({
      ...node,
      path: targetPath,
      content: clonedBytes,
      revision: makeRevision(),
      updatedAt: Date.now(),
    });
  }
}

export async function vfsRemove(path: string, recursive = true): Promise<void> {
  await ensureSeeded();
  const norm = normalizePath(path);
  const all = await getAllNodesRaw();
  const matching = all.filter((n) => n.path === norm || (recursive && n.path.startsWith(norm + "/")));
  for (const node of matching) {
    await deleteNodeRaw(node.path);
  }
}

export async function vfsListAssetTree(subPath = ""): Promise<AssetTreeEntry[]> {
  await ensureSeeded();
  const root = subPath ? joinPath(ASSETS_ROOT, subPath) : ASSETS_ROOT;
  await ensureDir(root);
  const all = await getAllNodesRaw();

  const build = (currentAbs: string, currentRel: string): AssetTreeEntry[] => {
    const curPrefix = currentAbs === "/" ? "/" : currentAbs + "/";
    const childMap = new Map<string, { kind: "file" | "directory"; abs: string; rel: string }>();
    for (const node of all) {
      if (!node.path.startsWith(curPrefix)) continue;
      const tail = node.path.slice(curPrefix.length);
      if (!tail) continue;
      const slashIdx = tail.indexOf("/");
      const name = slashIdx >= 0 ? tail.slice(0, slashIdx) : tail;
      const kind: "file" | "directory" = slashIdx >= 0 ? "directory" : node.kind;
      const abs = joinPath(currentAbs, name);
      const rel = currentRel ? `${currentRel}/${name}` : name;
      if (!childMap.has(name) || kind === "directory") {
        childMap.set(name, { kind, abs, rel });
      }
    }
    return [...childMap.entries()]
      .map(([name, info]): AssetTreeEntry => ({
        name,
        path: info.rel,
        type: info.kind,
        ...(info.kind === "directory" ? { children: build(info.abs, info.rel) } : {}),
      }))
      .sort((a, b) => {
        if (a.type !== b.type) return a.type === "directory" ? -1 : 1;
        return a.name.localeCompare(b.name, "zh-CN");
      });
  };
  return build(root, subPath ? normalizePath("/" + subPath).slice(1) : "");
}

export async function vfsExportZip(workspaceDir: string): Promise<Uint8Array> {
  await ensureSeeded();
  const root = normalizePath(workspaceDir);
  const all = await getAllNodesRaw();
  const prefix = root + "/";
  const entries: Record<string, Uint8Array> = {};
  for (const node of all) {
    if (node.kind !== "file" || !node.path.startsWith(prefix)) continue;
    const rel = node.path.slice(prefix.length);
    if (!rel) continue;
    entries[rel] = node.content instanceof Uint8Array ? node.content : new Uint8Array(node.content ?? []);
  }
  return zipSync(entries, { level: 6 });
}

export async function vfsImportZip(workspaceDir: string, zipBytes: Uint8Array): Promise<void> {
  await ensureSeeded();
  const root = normalizePath(workspaceDir);
  await ensureDir(root);
  const files = unzipSync(zipBytes);
  for (const [relPath, bytes] of Object.entries(files)) {
    if (!relPath || relPath.endsWith("/")) continue;
    await vfsWriteBytes(joinPath(root, relPath), bytes);
  }
}

export async function vfsSearchText(workspaceDir: string, query: string): Promise<{ path: string; line: number; text: string }[]> {
  await ensureSeeded();
  const root = normalizePath(workspaceDir);
  const prefix = root + "/";
  const needle = query.toLowerCase();
  const all = await getAllNodesRaw();
  const results: { path: string; line: number; text: string }[] = [];
  for (const node of all) {
    if (node.kind !== "file" || !node.path.startsWith(prefix)) continue;
    const mime = node.mimeType || guessMimeType(node.path);
    if (!mime.startsWith("text/") && mime !== "application/json") continue;
    const text = textDecoder.decode(node.content ?? new Uint8Array());
    const lines = text.split("\n");
    for (let i = 0; i < lines.length; i++) {
      if (lines[i]!.toLowerCase().includes(needle)) {
        results.push({ path: node.path.slice(prefix.length), line: i + 1, text: lines[i]!.slice(0, 200) });
        if (results.length >= 100) return results;
      }
    }
  }
  return results;
}

export async function vfsUsageStats(): Promise<{ fileCount: number; totalBytes: number }> {
  await ensureSeeded();
  const all = await getAllNodesRaw();
  let fileCount = 0;
  let totalBytes = 0;
  for (const node of all) {
    if (node.kind === "file") {
      fileCount++;
      totalBytes += node.content?.byteLength ?? 0;
    }
  }
  return { fileCount, totalBytes };
}

const STARTER_SCRIPT_MD =
  "# 雨夜便利店（短剧分镜）\n\n**镜头 1（全景 / 4秒）**：午夜暴雨，霓虹倒映在湿滑柏油路，便利店暖白灯光成为唯一港湾。\n\n**镜头 2（中近景 / 4秒）**：女主林知微收起透明雨伞推门而入，风铃清脆作响，发梢滴落水珠。\n\n**镜头 3（特写 / 3秒）**：柜台台灯照亮她手里的旧怀表，指针停在 23:59。\n";

function buildStarterCanvas() {
  return {
    toonflowCanvas: true,
    nodes: [
      {
        id: "node-script-1",
        type: "remote-textNode",
        position: { x: 60, y: 120 },
        data: {
          label: "第一集 · 剧本大纲",
          textPath: "assets/node-script-1/content.md",
          textSnapshot: STARTER_SCRIPT_MD,
        },
      },
      {
        id: "node-img-gen-1",
        type: "remote-imageGenerationNode",
        position: { x: 480, y: 60 },
        data: {
          label: "镜头 1 · 关键帧生图",
          prompt: "赛博雨夜街头便利店，暖白色玻璃橱窗与冷蓝色霓虹雨夜形成强烈冷暖对比，电影级 35mm 镜头，16:9",
        },
      },
      {
        id: "node-video-gen-1",
        type: "remote-videoGenerationNode",
        position: { x: 920, y: 60 },
        data: {
          label: "镜头 1 · 图生视频",
          prompt: "镜头缓慢向前推进穿过雨幕，霓虹灯在积水路面轻轻摇曳，电影级稳定运镜",
        },
      },
      {
        id: "node-truman-1",
        type: "remote-trumanMap",
        position: { x: 480, y: 420 },
        data: {
          label: "3D 白模预演 · 机位调度",
        },
      },
    ],
    edges: [
      {
        id: "edge-script-to-img",
        source: "node-script-1",
        sourceHandle: "text",
        target: "node-img-gen-1",
        targetHandle: "in",
      },
      {
        id: "edge-img-to-video",
        source: "node-img-gen-1",
        sourceHandle: "image",
        target: "node-video-gen-1",
        targetHandle: "in",
      },
      {
        id: "edge-script-to-truman",
        source: "node-script-1",
        sourceHandle: "text",
        target: "node-truman-1",
        targetHandle: "in",
      },
    ],
    viewport: { x: 80, y: 60, zoom: 0.85 },
  };
}

export async function ensureSeeded(): Promise<void> {
  if (!seededPromise) {
    seededPromise = (async () => {
      const initialized = await kvGet<boolean>("vfs.seeded.v1", false);
      if (initialized) return;

      await ensureDir(WORKSPACES_ROOT);
      await ensureDir(SCENES_ROOT);
      await ensureDir(ASSETS_ROOT);
      await ensureDir(joinPath(ASSETS_ROOT, "角色设定"));
      await ensureDir(joinPath(ASSETS_ROOT, "场景背景"));
      await ensureDir(joinPath(ASSETS_ROOT, "分镜关键帧"));
      await ensureDir(joinPath(ASSETS_ROOT, "快捷生图导入"));

      // Starter workspace with real Toonflow canvas + markdown script
      await ensureDir(DEFAULT_WORKSPACE_DIR);
      await ensureDir(joinPath(DEFAULT_WORKSPACE_DIR, "assets"));
      await ensureDir(joinPath(DEFAULT_WORKSPACE_DIR, "assets/node-script-1"));
      const canvasJson = JSON.stringify(buildStarterCanvas(), null, 2) + "\n";
      await putNodeRaw({
        path: joinPath(DEFAULT_WORKSPACE_DIR, "主画布.json"),
        kind: "file",
        content: textEncoder.encode(canvasJson),
        mimeType: "application/json",
        revision: makeRevision(),
        updatedAt: Date.now(),
      });
      await putNodeRaw({
        path: joinPath(DEFAULT_WORKSPACE_DIR, "assets/node-script-1/content.md"),
        kind: "file",
        content: textEncoder.encode(STARTER_SCRIPT_MD),
        mimeType: "text/markdown; charset=utf-8",
        revision: makeRevision(),
        updatedAt: Date.now(),
      });
      await putNodeRaw({
        path: joinPath(DEFAULT_WORKSPACE_DIR, "剧本与分镜.md"),
        kind: "file",
        content: textEncoder.encode(
          "# 雨夜便利店 · 短剧脚本\n\n> 本工作区运行在你的浏览器本机 IndexedDB 中，不占用服务器算力与磁盘。\n\n## 场次 1：午夜便利店\n- **场景**：都市老街 24 小时便利店，室外大雨\n- **人物**：林知微（24岁，风衣、透明雨伞）\n- **镜头建议**：先用「3D 白模预演」确定全景机位与走位，再一键生成关键帧与图生视频。\n"
        ),
        mimeType: "text/markdown; charset=utf-8",
        revision: makeRevision(),
        updatedAt: Date.now(),
      });

      // Starter 3D TrumanMap scene folder
      await ensureDir(DEFAULT_SCENE_DIR);

      await kvSet("vfs.seeded.v1", true);
    })();
  }
  await seededPromise;
}

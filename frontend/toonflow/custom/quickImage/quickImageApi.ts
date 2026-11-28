import { translate } from "@toonflow/i18n/vue";
import {
  getHostContext,
  inferTextProtocol,
  resolveSub2ApiKey,
  type DiscoveredModelMeta,
  type Sub2ApiKeyInfo,
} from "../../runtime/sub2apiBridge";
import {
  ASSETS_ROOT,
  DEFAULT_WORKSPACE_DIR,
  ensureDir,
  ensureSeeded,
  joinPath,
  vfsListWorkspaceRel,
  vfsReadText,
  vfsWriteBytes,
} from "../../runtime/vfs";
import {
  ANTHROPIC_REPLY_HEADROOM_TOKENS,
  ANTHROPIC_THINKING_BUDGETS,
  detectRewriteProtocol,
  effortOptionsForModel,
  type RewriteEffort,
  type RewriteProtocol,
} from "@host/features/imagegen/rewriteConfig";
import type { ImageRequestPayload } from "@host/features/imagegen/types";

export { effortOptionsForModel, detectRewriteProtocol };
export type { RewriteEffort, RewriteProtocol };

function buildRewriteInstruction(original: string, instruction: string): string {
  return `现在你是一名专业的AI提示词工程师，将以下生图内容提示词修改为用户所需要的新提示词，原提示词:"${original}",用户修改期待:"${instruction}"。直接且只能输出修改后的提示词，严禁输入非修改后提示词内容`;
}

function rewritePath(protocol: RewriteProtocol): string {
  if (protocol === "openai-responses") return "/responses";
  if (protocol === "anthropic-messages") return "/messages";
  return "/chat/completions";
}

function parseRewriteResponse(protocol: RewriteProtocol, payload: unknown): string {
  if (!payload || typeof payload !== "object") return "";
  const record = payload as Record<string, unknown>;
  if (protocol === "anthropic-messages") {
    const content = Array.isArray(record.content) ? record.content : [];
    return content
      .filter((b): b is { type?: string; text?: string } => Boolean(b && typeof b === "object"))
      .filter((b) => b.type === "text" && typeof b.text === "string" && b.text.trim())
      .map((b) => b.text!)
      .join("\n");
  }
  if (protocol === "openai-responses") {
    const output = Array.isArray(record.output) ? record.output : [];
    const parts: string[] = [];
    for (const item of output) {
      if (!item || typeof item !== "object") continue;
      const content = (item as { content?: unknown }).content;
      if (!Array.isArray(content)) continue;
      for (const block of content) {
        if (!block || typeof block !== "object") continue;
        const b = block as { type?: string; text?: string };
        if ((b.type === "output_text" || b.type === "text") && typeof b.text === "string" && b.text.trim()) {
          parts.push(b.text);
        }
      }
    }
    if (parts.length) return parts.join("\n");
    return typeof record.output_text === "string" ? record.output_text : "";
  }
  const choices = Array.isArray(record.choices) ? record.choices : [];
  const first = choices[0] as { message?: { content?: unknown } } | undefined;
  const content = first?.message?.content;
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .filter((b): b is { text?: string } => Boolean(b && typeof b === "object" && typeof (b as { text?: unknown }).text === "string"))
      .map((b) => b.text!)
      .join("\n");
  }
  return "";
}

function extractRewriteError(payload: unknown, response: Response): string {
  const record = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : {};
  const error = record.error && typeof record.error === "object" ? (record.error as Record<string, unknown>) : {};
  const msg =
    (typeof error.message === "string" && error.message) ||
    (typeof record.message === "string" && record.message) ||
    `${response.status} ${response.statusText}`;
  return msg;
}

function getSiteOrigin(): string {
  const host = getHostContext();
  return (host.origin || (typeof window !== "undefined" ? window.location.origin : "")).replace(/\/+$/, "");
}

export function siteGatewayUrl(path: string): string {
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${getSiteOrigin()}${suffix}`;
}

export function getActiveImageKey(): Sub2ApiKeyInfo | null {
  return resolveSub2ApiKey("image");
}

export function getActiveTextKey(): Sub2ApiKeyInfo | null {
  return resolveSub2ApiKey("text") ?? resolveSub2ApiKey("image");
}

export function getDiscoveredImageModels(keyId: number | null): string[] {
  const host = getHostContext();
  const resolvedId = keyId ?? resolveSub2ApiKey("image")?.id ?? null;
  if (!resolvedId) return [];
  const list = host.discoveredModels[resolvedId] ?? [];
  const imageModels = list.filter((id) => /image|imagen|flux|sdxl|dall-e|seedream|ideogram|recraft|gpt-image/i.test(id));
  return imageModels.length ? imageModels : list;
}

export function getDiscoveredTextModelMetas(keyId: number | null): DiscoveredModelMeta[] {
  const host = getHostContext();
  const resolvedId = keyId ?? resolveSub2ApiKey("text")?.id ?? null;
  if (!resolvedId) return [];
  const nonText = /image|dall-?e|flux|midjourney|stable-diffusion|sd[-_]?xl|video|veo|sora|tts|whisper|embed/i;
  const metaList = host.discoveredModelMeta?.[resolvedId];
  if (Array.isArray(metaList) && metaList.length) {
    return metaList.filter((m) => !nonText.test(m.id));
  }
  const raw = host.discoveredModels[resolvedId] ?? [];
  return raw.filter((id) => !nonText.test(id)).map((id) => ({ id }));
}

export async function verifySiteKey(
  rawKey: string,
  role: "image" | "text",
  signal?: AbortSignal
): Promise<{ ok: boolean; modelCount: number }> {
  const key = rawKey.trim();
  if (!key) return { ok: false, modelCount: 0 };
  const resp = await fetch(siteGatewayUrl("/v1/models"), {
    method: "GET",
    headers: { Authorization: `Bearer ${key}` },
    signal,
  });
  if (!resp.ok) return { ok: false, modelCount: 0 };
  const payload = (await resp.json().catch(() => ({}))) as { data?: { id?: string; model?: string }[] };
  const models = Array.isArray(payload.data)
    ? payload.data.map((entry) => String(entry?.id || entry?.model || "").trim()).filter(Boolean)
    : [];
  if (role === "image") {
    const imgModels = models.filter((m) => /image|imagen|flux|dall-e|seedream|ideogram|recraft/i.test(m));
    return { ok: imgModels.length > 0, modelCount: imgModels.length };
  }
  const nonText = /image|dall-?e|flux|midjourney|stable-diffusion|sd[-_]?xl|video|veo|sora|tts|whisper|embed/i;
  const textModels = models.filter((m) => !nonText.test(m));
  return { ok: textModels.length > 0, modelCount: textModels.length };
}

export async function requestSiteImages(options: {
  key: string;
  payload: ImageRequestPayload;
  references: File[];
  mask: File | null;
  format: string;
  signal: AbortSignal;
}): Promise<string[]> {
  const { key, payload, references, mask, format, signal } = options;
  const editing = references.length > 0;
  const url = siteGatewayUrl(editing ? "/v1/images/edits" : "/v1/images/generations");

  let response: Response;
  if (editing) {
    const body = new FormData();
    for (const [field, value] of Object.entries(payload)) {
      if (value !== undefined && value !== null) body.append(field, String(value));
    }
    for (const file of references) body.append("image[]", file);
    if (mask) body.append("mask", mask);
    response = await fetch(url, {
      method: "POST",
      headers: { Authorization: `Bearer ${key}` },
      body,
      signal,
    });
  } else {
    response = await fetch(url, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(payload),
      signal,
    });
  }

  const data = (await response.json().catch(() => ({}))) as {
    data?: { b64_json?: string; url?: string }[];
    error?: { message?: string; type?: string; code?: string; param?: string };
    message?: string;
  };

  if (!response.ok) {
    const error = data.error ?? {};
    const detail = [
      error.message || data.message || `${response.status} ${response.statusText}`,
      error.type,
      error.code,
      error.param && `param:${error.param}`,
    ]
      .filter(Boolean)
      .join(" | ");
    throw new Error(detail);
  }

  const images = (data.data ?? [])
    .map((entry) => (entry.b64_json ? `data:image/${format};base64,${entry.b64_json}` : entry.url))
    .filter((src): src is string => Boolean(src));

  if (!images.length) throw new Error(translate("未返回图片数据"));
  return images;
}

export async function requestSiteRewrite(options: {
  original: string;
  instruction: string;
  model: string;
  effort: RewriteEffort;
  signal: AbortSignal;
}): Promise<string> {
  const keyInfo = getActiveTextKey();
  if (!keyInfo?.key) {
    throw new Error(translate("请先在「设置 → 密钥与模型」中选择站内 API 密钥"));
  }
  const model = options.model.trim() || "gpt-4o-mini";
  const inferred = inferTextProtocol(keyInfo.platform);
  const protocol: RewriteProtocol =
    inferred === "anthropic-messages"
      ? "anthropic-messages"
      : detectRewriteProtocol(keyInfo.platform || "", model);
  const url = siteGatewayUrl(`/v1${rewritePath(protocol)}`);
  const promptText = buildRewriteInstruction(options.original, options.instruction);

  const headers: Record<string, string> = { "Content-Type": "application/json" };
  let body: Record<string, unknown>;

  if (protocol === "anthropic-messages") {
    headers["x-api-key"] = keyInfo.key;
    headers["anthropic-version"] = "2023-06-01";
    const budget =
      options.effort === "unset" || options.effort === "off"
        ? null
        : ANTHROPIC_THINKING_BUDGETS[options.effort];
    body = {
      model,
      max_tokens: budget ? budget + ANTHROPIC_REPLY_HEADROOM_TOKENS : ANTHROPIC_REPLY_HEADROOM_TOKENS,
      messages: [{ role: "user", content: promptText }],
      ...(budget ? { thinking: { type: "enabled", budget_tokens: budget } } : {}),
    };
  } else if (protocol === "openai-responses") {
    headers.Authorization = `Bearer ${keyInfo.key}`;
    const wireEffort = options.effort === "off" ? "none" : options.effort === "unset" ? null : options.effort;
    body = {
      model,
      input: promptText,
      ...(wireEffort ? { reasoning: { effort: wireEffort } } : {}),
    };
  } else {
    headers.Authorization = `Bearer ${keyInfo.key}`;
    const wireEffort = options.effort === "off" ? "none" : options.effort === "unset" ? null : options.effort;
    body = {
      model,
      messages: [{ role: "user", content: promptText }],
      ...(wireEffort ? { reasoning_effort: wireEffort } : {}),
    };
  }

  const response = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    signal: options.signal,
  });

  const payload = (await response.json().catch(() => null)) as unknown;
  if (!response.ok) {
    throw new Error(extractRewriteError(payload, response));
  }
  const text = parseRewriteResponse(protocol, payload).trim();
  if (!text) throw new Error(translate("未返回改写结果"));
  return text;
}

export async function saveImageToStudioAssets(src: string, title = "快捷生图"): Promise<string> {
  await ensureSeeded();
  const resp = await fetch(src);
  const bytes = new Uint8Array(await resp.arrayBuffer());
  const mimeType = resp.headers.get("content-type") || "image/png";
  const ext = mimeType.includes("webp") ? "webp" : mimeType.includes("jpeg") ? "jpg" : "png";
  const safeTitle = title.replace(/[\\/:*?"<>|]/g, "_").slice(0, 40) || "image";
  const rel = `快捷生图导入/${safeTitle}-${Date.now().toString(36)}.${ext}`;
  const fullPath = joinPath(ASSETS_ROOT, rel);
  await ensureDir(joinPath(ASSETS_ROOT, "快捷生图导入"));
  await vfsWriteBytes(fullPath, bytes, { mimeType });
  return rel;
}

export async function sendImageToWorkspaceCanvas(
  src: string,
  options: { directory?: string; title?: string; prompt?: string }
): Promise<{ directory: string; canvasFile: string }> {
  await ensureSeeded();
  const dir = options.directory || DEFAULT_WORKSPACE_DIR;
  await ensureDir(dir);
  await ensureDir(joinPath(dir, "assets"));

  const resp = await fetch(src);
  const bytes = new Uint8Array(await resp.arrayBuffer());
  const mimeType = resp.headers.get("content-type") || "image/png";
  const ext = mimeType.includes("webp") ? "webp" : mimeType.includes("jpeg") ? "jpg" : "png";
  const nodeId = `node-quick-${Date.now().toString(36)}`;
  const assetDir = `assets/${nodeId}`;
  const assetRel = `${assetDir}/image.${ext}`;
  await ensureDir(joinPath(dir, assetDir));
  await vfsWriteBytes(joinPath(dir, assetRel), bytes, { mimeType });

  // Also keep a copy in shared assets library
  await saveImageToStudioAssets(src, options.title || "快捷生图");

  const listing = await vfsListWorkspaceRel(dir);
  const canvasFile =
    listing.entries.find((e) => e.type === "file" && e.name.endsWith(".json") && !e.name.startsWith("."))?.name ||
    "主画布.json";
  const canvasPath = joinPath(dir, canvasFile);
  let canvas: {
    toonflowCanvas?: boolean;
    nodes?: Record<string, unknown>[];
    edges?: Record<string, unknown>[];
    viewport?: { x: number; y: number; zoom: number };
  } = {
    toonflowCanvas: true,
    nodes: [],
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
  };
  try {
    const raw = await vfsReadText(canvasPath);
    const parsed = JSON.parse(raw.text) as typeof canvas;
    if (parsed && Array.isArray(parsed.nodes)) {
      canvas = parsed;
    }
  } catch {
    // create fresh canvas
  }
  const existingNodes = Array.isArray(canvas.nodes) ? canvas.nodes : [];
  const offset = existingNodes.length * 40;
  existingNodes.push({
    id: nodeId,
    type: "remote-imageNode",
    position: { x: 120 + offset, y: 120 + offset },
    data: {
      label: options.title || "快捷生图",
      title: options.title || "快捷生图",
      url: assetRel,
      src: assetRel,
      file: assetRel,
      prompt: options.prompt || "",
    },
  });
  canvas.toonflowCanvas = true;
  canvas.nodes = existingNodes;
  await vfsWriteBytes(canvasPath, JSON.stringify(canvas, null, 2) + "\n", { mimeType: "application/json" });
  return { directory: dir, canvasFile };
}

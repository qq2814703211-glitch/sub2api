export interface Sub2ApiKeyInfo {
  id: number;
  name: string;
  key: string;
  platform?: string;
  groupName?: string;
  models?: string[];
}

export interface DiscoveredModelMeta {
  id: string;
  supportsReasoningEffort?: boolean;
  reasoningEffort?: string;
  reasoningEfforts?: { value: string; label?: string; default?: boolean }[];
}

export interface Sub2ApiUserInfo {
  id: number;
  email: string;
  username?: string;
  role?: string;
}

export interface Sub2ApiHostContext {
  origin: string;
  locale: "zh-CN" | "en";
  theme: "dark" | "light";
  siteName?: string;
  siteLogo?: string;
  user: Sub2ApiUserInfo | null;
  keys: Sub2ApiKeyInfo[];
  selectedTextKeyId: number | null;
  selectedImageKeyId: number | null;
  selectedVideoKeyId: number | null;
  selectedAudioKeyId: number | null;
  defaultTextModel?: string;
  defaultImageModel?: string;
  defaultVideoModel?: string;
  defaultAudioModel?: string;
  rewriteModel?: string;
  rewriteEffort?: string;
  discoveredModels: Record<number, string[]>;
  discoveredModelMeta?: Record<number, DiscoveredModelMeta[]>;
}

export interface StudioKeyBindingsPatch {
  textKeyId?: number | null;
  imageKeyId?: number | null;
  videoKeyId?: number | null;
  audioKeyId?: number | null;
  defaultTextModel?: string;
  defaultImageModel?: string;
  defaultVideoModel?: string;
  defaultAudioModel?: string;
  rewriteModel?: string;
  rewriteEffort?: string;
}

export const SUB2API_BRIDGE_STORAGE_KEY = "sub2api.creative.toonflow.hostContext.v1";

const DEFAULT_CONTEXT: Sub2ApiHostContext = {
  origin: typeof window !== "undefined" ? window.location.origin : "",
  locale: "zh-CN",
  theme: "dark",
  siteName: "",
  siteLogo: "",
  user: null,
  keys: [],
  selectedTextKeyId: null,
  selectedImageKeyId: null,
  selectedVideoKeyId: null,
  selectedAudioKeyId: null,
  defaultTextModel: "",
  defaultImageModel: "",
  defaultVideoModel: "",
  defaultAudioModel: "",
  rewriteModel: "",
  rewriteEffort: "unset",
  discoveredModels: {},
  discoveredModelMeta: {},
};

let currentContext: Sub2ApiHostContext = loadStoredContext();
let bridgeInitialized = false;
const listeners = new Set<(ctx: Sub2ApiHostContext) => void>();
const incomingMediaListeners = new Set<
  (payload: { src: string; prompt?: string; title?: string; target?: "canvas" | "assets" }) => void
>();

function loadStoredContext(): Sub2ApiHostContext {
  try {
    const raw = localStorage.getItem(SUB2API_BRIDGE_STORAGE_KEY);
    if (!raw) return { ...DEFAULT_CONTEXT };
    const parsed = JSON.parse(raw) as Partial<Sub2ApiHostContext>;
    return {
      ...DEFAULT_CONTEXT,
      ...parsed,
      origin: parsed.origin || (typeof window !== "undefined" ? window.location.origin : ""),
      keys: Array.isArray(parsed.keys) ? parsed.keys : [],
      discoveredModels:
        parsed.discoveredModels && typeof parsed.discoveredModels === "object" ? parsed.discoveredModels : {},
      discoveredModelMeta:
        parsed.discoveredModelMeta && typeof parsed.discoveredModelMeta === "object"
          ? parsed.discoveredModelMeta
          : {},
    };
  } catch {
    return { ...DEFAULT_CONTEXT };
  }
}

export function getHostContext(): Sub2ApiHostContext {
  return currentContext;
}

export function updateHostContext(
  patch: Partial<Sub2ApiHostContext>,
  options?: { persist?: boolean },
): void {
  const prevDefaultTextModel = currentContext.defaultTextModel ?? "";
  const prevDefaultImageModel = currentContext.defaultImageModel ?? "";
  const prevDefaultVideoModel = currentContext.defaultVideoModel ?? "";
  const prevDefaultAudioModel = currentContext.defaultAudioModel ?? "";
  const prevKeysSig = JSON.stringify([
    currentContext.keys.map((k) => `${k.id}:${k.key}`),
    currentContext.selectedTextKeyId,
    currentContext.selectedImageKeyId,
    currentContext.selectedVideoKeyId,
    currentContext.selectedAudioKeyId,
    currentContext.defaultTextModel,
    currentContext.defaultImageModel,
    currentContext.defaultVideoModel,
    currentContext.defaultAudioModel,
    currentContext.rewriteModel,
    currentContext.discoveredModels,
  ]);
  currentContext = {
    ...currentContext,
    ...patch,
    keys: Array.isArray(patch.keys) ? patch.keys : currentContext.keys,
    discoveredModels: patch.discoveredModels ?? currentContext.discoveredModels,
    discoveredModelMeta: patch.discoveredModelMeta ?? currentContext.discoveredModelMeta,
  };
  if (options?.persist !== false) {
    try {
      localStorage.setItem(SUB2API_BRIDGE_STORAGE_KEY, JSON.stringify(currentContext));
    } catch {
      // ignore storage quota errors
    }
  }
  const nextKeysSig = JSON.stringify([
    currentContext.keys.map((k) => `${k.id}:${k.key}`),
    currentContext.selectedTextKeyId,
    currentContext.selectedImageKeyId,
    currentContext.selectedVideoKeyId,
    currentContext.selectedAudioKeyId,
    currentContext.defaultTextModel,
    currentContext.defaultImageModel,
    currentContext.defaultVideoModel,
    currentContext.defaultAudioModel,
    currentContext.rewriteModel,
    currentContext.discoveredModels,
  ]);
  if (typeof window !== "undefined") {
    if (prevKeysSig !== nextKeysSig) {
      window.dispatchEvent(new Event("toonflow:provider-config-changed"));
    }
    const nextDefaultTextModel = currentContext.defaultTextModel ?? "";
    const nextDefaultImageModel = currentContext.defaultImageModel ?? "";
    const nextDefaultVideoModel = currentContext.defaultVideoModel ?? "";
    const nextDefaultAudioModel = currentContext.defaultAudioModel ?? "";
    if (nextDefaultTextModel !== prevDefaultTextModel) {
      window.dispatchEvent(new CustomEvent("toonflow:default-text-model-changed", { detail: nextDefaultTextModel }));
    }
    if (nextDefaultImageModel !== prevDefaultImageModel) {
      window.dispatchEvent(new CustomEvent("toonflow:default-media-model-changed", { detail: { mediaType: "image", modelId: nextDefaultImageModel } }));
    }
    if (nextDefaultVideoModel !== prevDefaultVideoModel) {
      window.dispatchEvent(new CustomEvent("toonflow:default-media-model-changed", { detail: { mediaType: "video", modelId: nextDefaultVideoModel } }));
    }
    if (nextDefaultAudioModel !== prevDefaultAudioModel) {
      window.dispatchEvent(new CustomEvent("toonflow:default-media-model-changed", { detail: { mediaType: "audio", modelId: nextDefaultAudioModel } }));
    }
  }
  for (const listener of listeners) {
    try {
      listener(currentContext);
    } catch {
      // ignore listener errors
    }
  }
}

export function subscribeHostContext(listener: (ctx: Sub2ApiHostContext) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function subscribeIncomingMedia(
  listener: (payload: { src: string; prompt?: string; title?: string; target?: "canvas" | "assets" }) => void
): () => void {
  incomingMediaListeners.add(listener);
  return () => {
    incomingMediaListeners.delete(listener);
  };
}

export function resolveSub2ApiKey(role: "text" | "image" | "video" | "audio"): Sub2ApiKeyInfo | null {
  const ctx = currentContext;
  if (!ctx.keys.length) return null;
  const preferredId =
    role === "text"
      ? ctx.selectedTextKeyId
      : role === "image"
        ? ctx.selectedImageKeyId
        : role === "video"
          ? ctx.selectedVideoKeyId
          : ctx.selectedAudioKeyId;
  if (preferredId) {
    const exact = ctx.keys.find((k) => k.id === preferredId);
    if (exact) return exact;
  }
  return ctx.keys[0] ?? null;
}

export function inferTextProtocol(platform?: string): "openai-completions" | "openai-responses" | "anthropic-messages" {
  const lower = (platform || "").toLowerCase();
  if (lower.includes("anthropic") || lower.includes("claude")) return "anthropic-messages";
  if (lower.includes("codex") || lower.includes("responses")) return "openai-responses";
  return "openai-completions";
}

export function postToSub2ApiHost(type: string, payload?: unknown): void {
  if (typeof window === "undefined" || window.parent === window) return;
  try {
    window.parent.postMessage({ source: "toonflow-web", type, payload }, window.location.origin);
  } catch {
    // ignore postMessage errors
  }
}

export function updateKeyBindingsFromStudio(patch: StudioKeyBindingsPatch): void {
  const nextPatch: Partial<Sub2ApiHostContext> = {};
  if (patch.textKeyId !== undefined) nextPatch.selectedTextKeyId = patch.textKeyId;
  if (patch.imageKeyId !== undefined) nextPatch.selectedImageKeyId = patch.imageKeyId;
  if (patch.videoKeyId !== undefined) nextPatch.selectedVideoKeyId = patch.videoKeyId;
  if (patch.audioKeyId !== undefined) nextPatch.selectedAudioKeyId = patch.audioKeyId;
  if (patch.defaultTextModel !== undefined) nextPatch.defaultTextModel = patch.defaultTextModel;
  if (patch.defaultImageModel !== undefined) nextPatch.defaultImageModel = patch.defaultImageModel;
  if (patch.defaultVideoModel !== undefined) nextPatch.defaultVideoModel = patch.defaultVideoModel;
  if (patch.defaultAudioModel !== undefined) nextPatch.defaultAudioModel = patch.defaultAudioModel;
  if (patch.rewriteModel !== undefined) nextPatch.rewriteModel = patch.rewriteModel;
  if (patch.rewriteEffort !== undefined) nextPatch.rewriteEffort = patch.rewriteEffort;
  updateHostContext(nextPatch);
  postToSub2ApiHost("updateKeyBindings", {
    textKeyId: currentContext.selectedTextKeyId,
    imageKeyId: currentContext.selectedImageKeyId,
    videoKeyId: currentContext.selectedVideoKeyId,
    audioKeyId: currentContext.selectedAudioKeyId,
    defaultTextModel: currentContext.defaultTextModel ?? "",
    defaultImageModel: currentContext.defaultImageModel ?? "",
    defaultVideoModel: currentContext.defaultVideoModel ?? "",
    defaultAudioModel: currentContext.defaultAudioModel ?? "",
    rewriteModel: currentContext.rewriteModel ?? "",
    rewriteEffort: currentContext.rewriteEffort ?? "unset",
  });
}

export function updateDiscoveredModelsFromStudio(
  keyId: number,
  models: string[],
  meta?: DiscoveredModelMeta[],
): void {
  const cleanModels = [...new Set(models.map((m) => String(m || "").trim()).filter(Boolean))];
  const cleanMeta = Array.isArray(meta) && meta.length ? meta : cleanModels.map((id) => ({ id }));
  updateHostContext({
    discoveredModels: {
      ...currentContext.discoveredModels,
      [keyId]: cleanModels,
    },
    discoveredModelMeta: {
      ...(currentContext.discoveredModelMeta ?? {}),
      [keyId]: cleanMeta,
    },
  });
  postToSub2ApiHost("updateDiscoveredModels", {
    keyId,
    models: cleanModels,
    meta: cleanMeta,
  });
}

export function requestRefreshKeysFromHost(): void {
  postToSub2ApiHost("refreshKeys");
}

export function requestOpenSiteKeys(): void {
  if (typeof window !== "undefined" && window.parent === window) {
    window.location.href = "/keys";
    return;
  }
  postToSub2ApiHost("openSiteKeys");
}

export function initSub2ApiBridge(): void {
  if (typeof window === "undefined") return;
  if (bridgeInitialized) {
    postToSub2ApiHost("ready");
    return;
  }
  bridgeInitialized = true;
  window.addEventListener("storage", (event: StorageEvent) => {
    if (event.key !== SUB2API_BRIDGE_STORAGE_KEY || !event.newValue) return;
    try {
      const parsed = JSON.parse(event.newValue) as Partial<Sub2ApiHostContext>;
      if (parsed && typeof parsed === "object") {
        updateHostContext(parsed, { persist: false });
      }
    } catch {
      // ignore malformed storage updates
    }
  });
  window.addEventListener("message", (event: MessageEvent) => {
    if (event.origin !== window.location.origin) return;
    const msg = event.data as { source?: string; type?: string; payload?: unknown } | null;
    if (!msg || msg.source !== "sub2api-host") return;
    if (msg.type === "syncContext" && msg.payload && typeof msg.payload === "object") {
      updateHostContext(msg.payload as Partial<Sub2ApiHostContext>);
    } else if (msg.type === "importMedia" && msg.payload && typeof msg.payload === "object") {
      const payload = msg.payload as { src: string; prompt?: string; title?: string; target?: "canvas" | "assets" };
      for (const listener of incomingMediaListeners) {
        try {
          listener(payload);
        } catch {
          // ignore
        }
      }
    } else if (msg.type === "navigate" && typeof msg.payload === "string") {
      window.location.hash = msg.payload;
    }
  });
  postToSub2ApiHost("ready");
}

export interface Sub2ApiKeyInfo {
  id: number;
  name: string;
  key: string;
  platform?: string;
  groupName?: string;
  models?: string[];
}

export interface Sub2ApiUserInfo {
  id: number;
  email: string;
  username?: string;
  role?: string;
  balance?: number;
}

export interface Sub2ApiHostContext {
  origin: string;
  locale: "zh-CN" | "en";
  theme: "dark" | "light";
  user: Sub2ApiUserInfo | null;
  keys: Sub2ApiKeyInfo[];
  selectedTextKeyId: number | null;
  selectedImageKeyId: number | null;
  selectedVideoKeyId: number | null;
  selectedAudioKeyId: number | null;
  discoveredModels: Record<number, string[]>;
}

export const SUB2API_BRIDGE_STORAGE_KEY = "sub2api.creative.toonflow.hostContext.v1";

const DEFAULT_CONTEXT: Sub2ApiHostContext = {
  origin: typeof window !== "undefined" ? window.location.origin : "",
  locale: "zh-CN",
  theme: "dark",
  user: null,
  keys: [],
  selectedTextKeyId: null,
  selectedImageKeyId: null,
  selectedVideoKeyId: null,
  selectedAudioKeyId: null,
  discoveredModels: {},
};

let currentContext: Sub2ApiHostContext = loadStoredContext();
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
      discoveredModels: parsed.discoveredModels && typeof parsed.discoveredModels === "object" ? parsed.discoveredModels : {},
    };
  } catch {
    return { ...DEFAULT_CONTEXT };
  }
}

export function getHostContext(): Sub2ApiHostContext {
  return currentContext;
}

export function updateHostContext(patch: Partial<Sub2ApiHostContext>): void {
  currentContext = {
    ...currentContext,
    ...patch,
    keys: Array.isArray(patch.keys) ? patch.keys : currentContext.keys,
    discoveredModels: patch.discoveredModels ?? currentContext.discoveredModels,
  };
  try {
    localStorage.setItem(SUB2API_BRIDGE_STORAGE_KEY, JSON.stringify(currentContext));
  } catch {
    // ignore storage quota errors
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
  if (lower.includes("openai") || lower.includes("codex") || lower.includes("responses")) return "openai-completions";
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

export function initSub2ApiBridge(): void {
  if (typeof window === "undefined") return;
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

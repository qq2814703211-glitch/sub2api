import { getHostContext, inferTextProtocol, resolveSub2ApiKey, type Sub2ApiKeyInfo } from "./sub2apiBridge";
import { joinPath, kvGet, kvSet, vfsWriteBytes } from "./vfs";

export interface CustomTextProvider {
  id: string;
  label: string;
  apiUrl: string;
  protocol: "openai-completions" | "openai-responses" | "anthropic-messages";
  apiKey: string;
  headers?: Record<string, string>;
  models: {
    id: string;
    label: string;
    reasoning?: boolean;
    reasoningEfforts?: string[];
    contextWindow?: number;
    maxOutputTokens?: number;
    input?: ("text" | "image")[];
  }[];
}

export interface ResolvedAiModel {
  providerId: string;
  providerLabel: string;
  modelId: string;
  id: string;
  label: string;
  protocol: "openai-completions" | "openai-responses" | "anthropic-messages";
  reasoning: boolean;
  reasoningLevels: ("minimal" | "low" | "medium" | "high" | "xhigh")[];
  reasoningEfforts: string[];
  contextWindow: number;
  maxOutputTokens: number;
  input: ("text" | "image")[];
}

export interface MediaProviderRecord {
  id: string;
  name: string;
  description: string;
  builtin?: boolean;
  enabled: boolean;
  apiUrl?: string;
  apiKey?: string;
  models: MediaModelDescriptor[];
}

export interface MediaModelDescriptor {
  providerId: string;
  providerLabel: string;
  providerName: string;
  modelId: string;
  id: string;
  label: string;
  type: "image" | "video" | "audio";
  capabilities?: string[];
  imageRatios?: string[];
  imageSizes?: string[];
  aspectRatios?: string[];
  resolutions?: string[];
  durations?: number[];
  durationResolutionMap?: { duration: number[]; resolution: string[]; ratio?: string[] }[];
  voices?: { title: string; voice: string }[];
  rules?: Record<string, unknown>[];
  formRules?: Record<string, unknown>[];
}

export interface MediaTaskRecord {
  taskId: string;
  directory: string;
  mediaType: "image" | "video" | "audio";
  status: "pending" | "running" | "completed" | "failed" | "cancelled";
  progress?: number;
  message?: string;
  result?: { code: number; data?: { path: string; mimeType: string; mediaType: string }[]; message?: string };
  controller?: AbortController;
}

const mediaTasks = new Map<string, MediaTaskRecord>();

const DEFAULT_TEXT_MODELS = [
  "gpt-4o",
  "gpt-4.1",
  "claude-sonnet-4-5-20250929",
  "gemini-2.5-pro",
  "deepseek-chat",
];

const DEFAULT_IMAGE_MODELS = [
  { id: "gpt-image-1", label: "GPT Image 1 (站内网关)" },
  { id: "gemini-2.5-flash-image-preview", label: "Gemini 2.5 Flash Image (站内网关)" },
  { id: "flux-pro", label: "FLUX Pro (站内网关)" },
];

const DEFAULT_VIDEO_MODELS = [
  { id: "grok-imagine-video", label: "Grok Imagine Video (站内网关)" },
  { id: "sora-2", label: "Sora 2 / OpenAI Video (站内网关)" },
  { id: "doubao-seedance-1-0-pro-250528", label: "Seedance Video (站内网关)" },
];

const DEFAULT_AUDIO_MODELS = [
  { id: "gpt-4o-mini-tts", label: "GPT-4o Mini TTS (站内网关)" },
  { id: "tts-1-hd", label: "OpenAI TTS-1 HD (站内网关)" },
];

export async function getStoredSettings(): Promise<Record<string, unknown>> {
  return kvGet<Record<string, unknown>>("toonflow.settings", {});
}

export async function listAllTextProviders(): Promise<CustomTextProvider[]> {
  const host = getHostContext();
  const settings = await getStoredSettings();
  const providers: CustomTextProvider[] = [];

  // Auto-provision Sub2API built-in gateway providers per user API key
  if (host.keys.length) {
    const primary = resolveSub2ApiKey("text") ?? host.keys[0]!;
    const discovered = host.discoveredModels[primary.id] ?? primary.models ?? [];
    const textIds = discovered.length
      ? discovered.filter((m) => !/image|dall-e|sora|video|tts|whisper|embedding/i.test(m))
      : DEFAULT_TEXT_MODELS;
    const modelIds = textIds.length ? textIds : DEFAULT_TEXT_MODELS;
    providers.push({
      id: "sub2api-gateway",
      label: `Sub2API 站内网关 (${primary.name || `#${primary.id}`})`,
      apiUrl: `${host.origin || window.location.origin}/v1`,
      protocol: inferTextProtocol(primary.platform),
      apiKey: primary.key,
      models: modelIds.map((id) => ({
        id,
        label: id,
        reasoning: /o1|o3|o4|reasoner|r1|claude|gemini-2\.5|gpt-5/i.test(id),
        reasoningEfforts: /o1|o3|o4|gpt-5|claude/i.test(id) ? ["low", "medium", "high"] : [],
        contextWindow: 128000,
        maxOutputTokens: 8192,
        input: ["text", "image"],
      })),
    });
  } else {
    // Provide a visible placeholder gateway entry so UI model selectors are populated even before key sync
    providers.push({
      id: "sub2api-gateway",
      label: "Sub2API 站内网关",
      apiUrl: `${typeof window !== "undefined" ? window.location.origin : ""}/v1`,
      protocol: "openai-completions",
      apiKey: "",
      models: DEFAULT_TEXT_MODELS.map((id) => ({
        id,
        label: id,
        reasoning: /claude|gemini-2\.5/i.test(id),
        reasoningEfforts: [],
        contextWindow: 128000,
        maxOutputTokens: 8192,
        input: ["text", "image"],
      })),
    });
  }

  const custom = Array.isArray(settings.customProviders)
    ? (settings.customProviders as CustomTextProvider[])
    : [];
  for (const p of custom) {
    if (p && typeof p.id === "string" && p.id !== "sub2api-gateway" && p.id.toLowerCase() !== "toonflow") {
      providers.push(p);
    }
  }
  return providers;
}

export async function listAiModels(): Promise<ResolvedAiModel[]> {
  const providers = await listAllTextProviders();
  const out: ResolvedAiModel[] = [];
  for (const provider of providers) {
    for (const model of provider.models ?? []) {
      const levels = (model.reasoningEfforts ?? []).filter((lvl): lvl is "minimal" | "low" | "medium" | "high" | "xhigh" =>
        lvl === "minimal" || lvl === "low" || lvl === "medium" || lvl === "high" || lvl === "xhigh"
      );
      out.push({
        providerId: provider.id,
        providerLabel: provider.label,
        modelId: model.id,
        id: model.id,
        label: model.label || model.id,
        protocol: provider.protocol || "openai-completions",
        reasoning: Boolean(model.reasoning),
        reasoningLevels: levels,
        reasoningEfforts: levels,
        contextWindow: model.contextWindow ?? 128000,
        maxOutputTokens: model.maxOutputTokens ?? 8192,
        input: model.input ?? ["text", "image"],
      });
    }
  }
  return out;
}

export async function listMediaProviders(): Promise<MediaProviderRecord[]> {
  const host = getHostContext();
  const imageKey = resolveSub2ApiKey("image");
  const videoKey = resolveSub2ApiKey("video");
  const audioKey = resolveSub2ApiKey("audio");

  const filterModels = (key: Sub2ApiKeyInfo | null, pattern: RegExp) => {
    if (!key) return [];
    const list = host.discoveredModels[key.id] ?? key.models ?? [];
    return list.filter((id) => pattern.test(id));
  };

  const discoveredImages = filterModels(imageKey, /image|imagen|flux|sdxl|dall-e|seedream|ideogram|recraft/i);
  const discoveredVideos = filterModels(videoKey, /video|sora|veo|kling|seedance|runway|hailuo|minimax|wan|vidu|luma/i);
  const discoveredAudios = filterModels(audioKey, /tts|speech|audio|voice|minimax-speech/i);

  const imageRules: Record<string, unknown>[] = [
    {
      type: "select",
      field: "size",
      title: "画面比例",
      value: "16:9",
      options: ["16:9", "9:16", "1:1", "4:3", "3:4"].map((v) => ({ label: v, value: v })),
    },
    {
      type: "inputNumber",
      field: "n",
      title: "生成张数",
      value: 1,
      props: { min: 1, max: 4 },
    },
  ];
  const imageModels = (
    discoveredImages.length ? discoveredImages.map((id) => ({ id, label: `${id} (站内网关)` })) : DEFAULT_IMAGE_MODELS
  ).map(
    (m): MediaModelDescriptor => ({
      providerId: "sub2api-media",
      providerLabel: "Sub2API 站内媒体网关",
      providerName: "Sub2API 站内媒体网关",
      modelId: m.id,
      id: m.id,
      label: m.label,
      type: "image",
      imageRatios: ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"],
      imageSizes: ["1K", "2K"],
      aspectRatios: ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"],
      resolutions: ["1K", "2K"],
      rules: imageRules,
      formRules: imageRules,
    })
  );

  const videoRules: Record<string, unknown>[] = [
    {
      type: "select",
      field: "ratio",
      title: "视频比例",
      value: "16:9",
      options: ["16:9", "9:16", "1:1"].map((v) => ({ label: v, value: v })),
    },
    {
      type: "select",
      field: "duration",
      title: "时长（秒）",
      value: 5,
      options: [4, 5, 6, 8, 10].map((v) => ({ label: `${v}s`, value: v })),
    },
  ];
  const videoModels = (
    discoveredVideos.length ? discoveredVideos.map((id) => ({ id, label: `${id} (站内网关)` })) : DEFAULT_VIDEO_MODELS
  ).map(
    (m): MediaModelDescriptor => ({
      providerId: "sub2api-media",
      providerLabel: "Sub2API 站内媒体网关",
      providerName: "Sub2API 站内媒体网关",
      modelId: m.id,
      id: m.id,
      label: m.label,
      type: "video",
      aspectRatios: ["16:9", "9:16", "1:1"],
      resolutions: ["720p", "1080p"],
      durations: [4, 5, 6, 8, 10],
      durationResolutionMap: [
        { duration: [4, 5, 6, 8, 10], resolution: ["720p", "1080p"], ratio: ["16:9", "9:16", "1:1"] },
      ],
      rules: videoRules,
      formRules: videoRules,
    })
  );

  const audioRules: Record<string, unknown>[] = [
    {
      type: "select",
      field: "voice",
      title: "音色",
      value: "alloy",
      options: ["alloy", "nova", "shimmer", "echo", "onyx", "fable"].map((v) => ({ label: v, value: v })),
    },
    {
      type: "inputNumber",
      field: "speed",
      title: "语速",
      value: 1,
      props: { min: 0.5, max: 2, step: 0.1 },
    },
  ];
  const audioModels = (
    discoveredAudios.length ? discoveredAudios.map((id) => ({ id, label: `${id} (站内网关)` })) : DEFAULT_AUDIO_MODELS
  ).map(
    (m): MediaModelDescriptor => ({
      providerId: "sub2api-media",
      providerLabel: "Sub2API 站内媒体网关",
      providerName: "Sub2API 站内媒体网关",
      modelId: m.id,
      id: m.id,
      label: m.label,
      type: "audio",
      voices: [
        { title: "Alloy", voice: "alloy" },
        { title: "Nova", voice: "nova" },
        { title: "Shimmer", voice: "shimmer" },
        { title: "Echo", voice: "echo" },
        { title: "Onyx", voice: "onyx" },
        { title: "Fable", voice: "fable" },
      ],
      rules: audioRules,
      formRules: audioRules,
    })
  );

  const customMedia = await kvGet<MediaProviderRecord[]>("toonflow.customMediaProviders", []);
  return [
    {
      id: "sub2api-media",
      name: "Sub2API 站内媒体网关",
      description: "自动复用当前登录用户的 Sub2API 密钥，浏览器直连站内图像、视频与语音网关。",
      builtin: true,
      enabled: true,
      apiUrl: `${host.origin || window.location.origin}/v1`,
      models: [...imageModels, ...videoModels, ...audioModels],
    },
    ...customMedia,
  ];
}

export async function listMediaModelsByType(mediaType?: string): Promise<MediaModelDescriptor[]> {
  const providers = await listMediaProviders();
  const all = providers.filter((p) => p.enabled).flatMap((p) => p.models);
  return mediaType ? all.filter((m) => m.type === mediaType) : all;
}

export async function saveCustomMediaProvider(provider: MediaProviderRecord): Promise<MediaProviderRecord[]> {
  const list = await kvGet<MediaProviderRecord[]>("toonflow.customMediaProviders", []);
  const next = [provider, ...list.filter((item) => item.id !== provider.id)];
  await kvSet("toonflow.customMediaProviders", next);
  return next;
}

export async function deleteCustomMediaProvider(id: string): Promise<MediaProviderRecord[]> {
  const list = await kvGet<MediaProviderRecord[]>("toonflow.customMediaProviders", []);
  const next = list.filter((item) => item.id !== id);
  await kvSet("toonflow.customMediaProviders", next);
  return next;
}

export async function fetchRemoteProviderModels(
  rawFetch: typeof fetch,
  input: { apiUrl: string; protocol: string; apiKey: string; headers?: Record<string, string> }
): Promise<{ id: string; label: string }[]> {
  const base = input.apiUrl.replace(/\/+$/, "");
  const endpoint =
    input.protocol === "anthropic-messages"
      ? `${base.endsWith("/v1") ? base : `${base}/v1`}/models?limit=200`
      : `${base}/models`;
  const headers: Record<string, string> = { Accept: "application/json", ...(input.headers ?? {}) };
  if (input.protocol === "anthropic-messages") {
    headers["x-api-key"] = input.apiKey;
    headers["anthropic-version"] = "2023-06-01";
  } else if (input.apiKey) {
    headers["Authorization"] = `Bearer ${input.apiKey}`;
  }
  const resp = await rawFetch(endpoint, { headers });
  if (!resp.ok) throw new Error(`拉取模型列表失败（HTTP ${resp.status}）`);
  const json = (await resp.json()) as { data?: { id?: string; display_name?: string; displayName?: string }[] };
  return (json.data ?? [])
    .filter((item) => typeof item.id === "string" && item.id.trim())
    .map((item) => ({
      id: item.id!.trim(),
      label: item.display_name || item.displayName || item.id!.trim(),
    }));
}

export async function resolveProviderCredentials(
  providerId: string
): Promise<{ apiUrl: string; apiKey: string; protocol: "openai-completions" | "openai-responses" | "anthropic-messages"; headers?: Record<string, string> }> {
  const providers = await listAllTextProviders();
  const found = providers.find((p) => p.id === providerId) ?? providers[0];
  if (!found) {
    throw new Error("未配置可用的 AI 模型供应商，请先在「创意广场」顶部绑定 Sub2API 密钥或在设置中添加供应商。");
  }
  if (!found.apiKey) {
    throw new Error("当前未选择 API 密钥，请在「创意广场」顶部选择一个站内 API 密钥，或在 Toonflow 设置中填写供应商密钥。");
  }
  return {
    apiUrl: found.apiUrl,
    apiKey: found.apiKey,
    protocol: found.protocol,
    headers: found.headers,
  };
}

export interface ChatCompletionMessage {
  role: "system" | "user" | "assistant" | "tool";
  content: string | Record<string, unknown>[];
  tool_call_id?: string;
  tool_calls?: { id: string; type: "function"; function: { name: string; arguments: string } }[];
}

export interface ChatCompletionTool {
  type: "function";
  function: { name: string; description: string; parameters: Record<string, unknown> };
}

export async function streamChatCompletion(
  rawFetch: typeof fetch,
  options: {
    providerId: string;
    modelId: string;
    messages: ChatCompletionMessage[];
    tools?: ChatCompletionTool[];
    signal?: AbortSignal;
    onTextDelta?: (delta: string) => void;
    onThinkingDelta?: (delta: string) => void;
  }
): Promise<{
  text: string;
  thinking: string;
  toolCalls: { id: string; name: string; arguments: Record<string, unknown> }[];
  usage: { input: number; output: number; total: number };
}> {
  const creds = await resolveProviderCredentials(options.providerId);
  const base = creds.apiUrl.replace(/\/+$/, "");

  if (creds.protocol === "anthropic-messages") {
    const url = `${base.endsWith("/v1") ? base : `${base}/v1`}/messages`;
    const systemText = options.messages
      .filter((m) => m.role === "system")
      .map((m) => (typeof m.content === "string" ? m.content : JSON.stringify(m.content)))
      .join("\n\n");
    const anthropicMessages = options.messages
      .filter((m) => m.role !== "system")
      .map((m) => {
        if (m.role === "tool") {
          return {
            role: "user",
            content: [
              {
                type: "tool_result",
                tool_use_id: m.tool_call_id || "tool",
                content: typeof m.content === "string" ? m.content : JSON.stringify(m.content),
              },
            ],
          };
        }
        if (m.role === "assistant" && m.tool_calls?.length) {
          const blocks: Record<string, unknown>[] = [];
          if (typeof m.content === "string" && m.content) blocks.push({ type: "text", text: m.content });
          for (const tc of m.tool_calls) {
            let parsedArgs: Record<string, unknown> = {};
            try {
              parsedArgs = JSON.parse(tc.function.arguments || "{}") as Record<string, unknown>;
            } catch {
              parsedArgs = {};
            }
            blocks.push({ type: "tool_use", id: tc.id, name: tc.function.name, input: parsedArgs });
          }
          return { role: "assistant", content: blocks };
        }
        return { role: m.role, content: m.content };
      });

    const resp = await rawFetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "x-api-key": creds.apiKey,
        "anthropic-version": "2023-06-01",
        ...(creds.headers ?? {}),
      },
      body: JSON.stringify({
        model: options.modelId,
        max_tokens: 4096,
        ...(systemText ? { system: systemText } : {}),
        messages: anthropicMessages,
        ...(options.tools?.length
          ? {
              tools: options.tools.map((t) => ({
                name: t.function.name,
                description: t.function.description,
                input_schema: t.function.parameters,
              })),
            }
          : {}),
      }),
      signal: options.signal,
    });
    if (!resp.ok) {
      const errText = await resp.text().catch(() => "");
      throw new Error(`Anthropic 请求失败（HTTP ${resp.status}）：${errText.slice(0, 240)}`);
    }
    const json = (await resp.json()) as {
      content?: { type: string; text?: string; thinking?: string; id?: string; name?: string; input?: Record<string, unknown> }[];
      usage?: { input_tokens?: number; output_tokens?: number };
    };
    let text = "";
    let thinking = "";
    const toolCalls: { id: string; name: string; arguments: Record<string, unknown> }[] = [];
    for (const block of json.content ?? []) {
      if (block.type === "thinking" && block.thinking) {
        thinking += block.thinking;
        options.onThinkingDelta?.(block.thinking);
      } else if (block.type === "text" && block.text) {
        text += block.text;
        options.onTextDelta?.(block.text);
      } else if (block.type === "tool_use" && block.name) {
        toolCalls.push({ id: block.id || crypto.randomUUID(), name: block.name, arguments: block.input ?? {} });
      }
    }
    const inputTokens = json.usage?.input_tokens ?? 0;
    const outputTokens = json.usage?.output_tokens ?? 0;
    return { text, thinking, toolCalls, usage: { input: inputTokens, output: outputTokens, total: inputTokens + outputTokens } };
  }

  // OpenAI-compatible streaming /v1/chat/completions
  const url = `${base}/chat/completions`;
  const resp = await rawFetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${creds.apiKey}`,
      ...(creds.headers ?? {}),
    },
    body: JSON.stringify({
      model: options.modelId,
      messages: options.messages,
      stream: true,
      ...(options.tools?.length ? { tools: options.tools, tool_choice: "auto" } : {}),
    }),
    signal: options.signal,
  });
  if (!resp.ok) {
    const errText = await resp.text().catch(() => "");
    throw new Error(`模型请求失败（HTTP ${resp.status}）：${errText.slice(0, 240)}`);
  }

  const contentType = resp.headers.get("content-type") ?? "";
  if (!resp.body || !contentType.includes("text/event-stream")) {
    const json = (await resp.json()) as {
      choices?: {
        message?: {
          content?: string;
          reasoning_content?: string;
          tool_calls?: { id?: string; function?: { name?: string; arguments?: string } }[];
        };
      }[];
      usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number };
    };
    const msg = json.choices?.[0]?.message;
    const text = msg?.content ?? "";
    const thinking = msg?.reasoning_content ?? "";
    if (thinking) options.onThinkingDelta?.(thinking);
    if (text) options.onTextDelta?.(text);
    const toolCalls = (msg?.tool_calls ?? []).map((tc) => {
      let args: Record<string, unknown> = {};
      try {
        args = JSON.parse(tc.function?.arguments || "{}") as Record<string, unknown>;
      } catch {
        args = {};
      }
      return { id: tc.id || crypto.randomUUID(), name: tc.function?.name || "", arguments: args };
    });
    return {
      text,
      thinking,
      toolCalls,
      usage: {
        input: json.usage?.prompt_tokens ?? 0,
        output: json.usage?.completion_tokens ?? 0,
        total: json.usage?.total_tokens ?? 0,
      },
    };
  }

  const reader = resp.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let text = "";
  let thinking = "";
  const tcAccumulator = new Map<number, { id: string; name: string; rawArgs: string }>();
  let usage = { input: 0, output: 0, total: 0 };

  const processSseLine = (rawLine: string) => {
    const line = rawLine.trim();
    if (!line.startsWith("data:")) return;
    const payload = line.slice(5).trim();
    if (!payload || payload === "[DONE]") return;
    try {
      const chunk = JSON.parse(payload) as {
        choices?: {
          delta?: {
            content?: string;
            reasoning_content?: string;
            reasoning?: string;
            tool_calls?: { index?: number; id?: string; function?: { name?: string; arguments?: string } }[];
          };
        }[];
        usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number };
      };
      if (chunk.usage) {
        usage = {
          input: chunk.usage.prompt_tokens ?? 0,
          output: chunk.usage.completion_tokens ?? 0,
          total: chunk.usage.total_tokens ?? 0,
        };
      }
      const delta = chunk.choices?.[0]?.delta;
      if (!delta) return;
      const thinkingDelta =
        typeof delta.reasoning_content === "string" && delta.reasoning_content
          ? delta.reasoning_content
          : typeof delta.reasoning === "string" && delta.reasoning
            ? delta.reasoning
            : "";
      if (thinkingDelta) {
        thinking += thinkingDelta;
        options.onThinkingDelta?.(thinkingDelta);
      }
      if (typeof delta.content === "string" && delta.content) {
        text += delta.content;
        options.onTextDelta?.(delta.content);
      }
      if (Array.isArray(delta.tool_calls)) {
        for (const tc of delta.tool_calls) {
          const idx = tc.index ?? 0;
          const existing = tcAccumulator.get(idx) ?? { id: tc.id || crypto.randomUUID(), name: "", rawArgs: "" };
          if (tc.id) existing.id = tc.id;
          if (tc.function?.name) existing.name += tc.function.name;
          if (tc.function?.arguments) existing.rawArgs += tc.function.arguments;
          tcAccumulator.set(idx, existing);
        }
      }
    } catch {
      // ignore malformed SSE chunk
    }
  };

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (value) buffer += value;
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      if (done && buffer.trim()) {
        lines.push(buffer);
        buffer = "";
      }
      for (const rawLine of lines) {
        processSseLine(rawLine);
      }
      if (done) break;
    }
  } finally {
    reader.releaseLock();
  }

  const toolCalls = [...tcAccumulator.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, tc]) => {
      let parsed: Record<string, unknown> = {};
      try {
        parsed = JSON.parse(tc.rawArgs || "{}") as Record<string, unknown>;
      } catch {
        parsed = {};
      }
      return { id: tc.id, name: tc.name, arguments: parsed };
    })
    .filter((tc) => Boolean(tc.name));

  return { text, thinking, toolCalls, usage };
}

export function createNodeAiGenerateStreamResponse(
  rawFetch: typeof fetch,
  body: {
    providerId: string;
    modelId: string;
    context: { systemPrompt?: string; messages: { role: string; content: unknown }[]; tools?: { name: string; description: string; parameters: Record<string, unknown> }[] };
  },
  signal?: AbortSignal
): Response {
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder();
      const send = (event: string, data: unknown) => {
        controller.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
      };
      try {
        const messages: ChatCompletionMessage[] = [];
        if (body.context.systemPrompt) {
          messages.push({ role: "system", content: body.context.systemPrompt });
        }
        for (const m of body.context.messages ?? []) {
          if (m.role === "user") {
            if (typeof m.content === "string") {
              messages.push({ role: "user", content: m.content });
            } else if (Array.isArray(m.content)) {
              const parts = m.content.map((p: { type?: string; text?: string; data?: string; mimeType?: string }) =>
                p.type === "image" && p.data
                  ? { type: "image_url", image_url: { url: `data:${p.mimeType || "image/png"};base64,${p.data}` } }
                  : { type: "text", text: p.text || "" }
              );
              messages.push({ role: "user", content: parts });
            }
          } else if (m.role === "assistant") {
            const textParts = Array.isArray(m.content)
              ? m.content
                  .filter((p: { type?: string }) => p.type === "text")
                  .map((p: { text?: string }) => p.text || "")
                  .join("")
              : typeof m.content === "string"
                ? m.content
                : "";
            messages.push({ role: "assistant", content: textParts });
          } else if (m.role === "toolResult") {
            const tr = m as unknown as { toolCallId?: string; content?: { text?: string }[] };
            messages.push({
              role: "tool",
              tool_call_id: tr.toolCallId || "tool",
              content: (tr.content ?? []).map((p) => p.text || "").join("\n"),
            });
          }
        }
        const tools: ChatCompletionTool[] | undefined = body.context.tools?.map((t) => ({
          type: "function",
          function: { name: t.name, description: t.description, parameters: t.parameters },
        }));

        const partial = {
          role: "assistant",
          content: [] as Record<string, unknown>[],
          api: "openai-completions",
          provider: body.providerId,
          model: body.modelId,
          timestamp: Date.now(),
          stopReason: "stop",
          usage: {
            input: 0,
            output: 0,
            cacheRead: 0,
            cacheWrite: 0,
            totalTokens: 0,
            cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
          },
        };
        send("start", { type: "start", partial });

        let textStarted = false;
        let thinkingStarted = false;
        const result = await streamChatCompletion(rawFetch, {
          providerId: body.providerId,
          modelId: body.modelId,
          messages,
          tools,
          signal,
          onThinkingDelta(delta) {
            if (!thinkingStarted) {
              thinkingStarted = true;
              send("thinking_start", { type: "thinking_start", contentIndex: 0, partial });
            }
            send("thinking_delta", { type: "thinking_delta", contentIndex: 0, delta, partial });
          },
          onTextDelta(delta) {
            const idx = thinkingStarted ? 1 : 0;
            if (!textStarted) {
              textStarted = true;
              send("text_start", { type: "text_start", contentIndex: idx, partial });
            }
            send("text_delta", { type: "text_delta", contentIndex: idx, delta, partial });
          },
        });

        const contentBlocks: Record<string, unknown>[] = [];
        if (result.thinking) {
          contentBlocks.push({ type: "thinking", thinking: result.thinking });
          send("thinking_end", { type: "thinking_end", contentIndex: 0, content: result.thinking, partial });
        }
        if (result.text) {
          const idx = contentBlocks.length;
          contentBlocks.push({ type: "text", text: result.text });
          send("text_end", { type: "text_end", contentIndex: idx, content: result.text, partial });
        }
        for (const tc of result.toolCalls) {
          const idx = contentBlocks.length;
          const block = { type: "toolCall", id: tc.id, name: tc.name, arguments: tc.arguments };
          contentBlocks.push(block);
          send("toolcall_start", { type: "toolcall_start", contentIndex: idx, partial });
          send("toolcall_end", { type: "toolcall_end", contentIndex: idx, toolCall: block, partial });
        }
        const finalMessage = {
          ...partial,
          content: contentBlocks,
          stopReason: result.toolCalls.length ? "toolUse" : "stop",
          usage: {
            ...partial.usage,
            input: result.usage.input,
            output: result.usage.output,
            totalTokens: result.usage.total,
          },
        };
        send("done", { type: "done", reason: finalMessage.stopReason, message: finalMessage });
      } catch (error) {
        send("error", {
          type: "error",
          reason: "error",
          error: { message: error instanceof Error ? error.message : String(error) },
        });
      } finally {
        controller.close();
      }
    },
  });

  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" },
  });
}

function base64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

async function executeMediaGeneration(
  rawFetch: typeof fetch,
  input: {
    directory: string;
    outputDirectory?: string;
    mediaType: "image" | "video" | "audio";
    providerId?: string;
    modelId?: string;
    prompt?: string;
    size?: string;
    ratio?: string;
    n?: number;
    duration?: number;
    voice?: string;
    speed?: number;
  },
  signal?: AbortSignal
): Promise<{ path: string; mimeType: string; mediaType: "image" | "video" | "audio" }[]> {
  const host = getHostContext();
  const origin = host.origin || window.location.origin;
  const outRel = (input.outputDirectory || "assets/generated").replace(/^\/+/, "");

  if (input.mediaType === "image") {
    const key = resolveSub2ApiKey("image");
    if (!key?.key) throw new Error("请先在「创意广场」顶部绑定用于生图的 Sub2API API 密钥");
    const resp = await rawFetch(`${origin}/v1/images/generations`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${key.key}` },
      body: JSON.stringify({
        model: input.modelId || "gpt-image-1",
        prompt: input.prompt || "",
        n: input.n || 1,
        size: input.size && input.size.includes("x") ? input.size : "1024x1024",
        response_format: "b64_json",
      }),
      signal,
    });
    if (!resp.ok) {
      const errText = await resp.text().catch(() => "");
      throw new Error(`图像生成失败（HTTP ${resp.status}）：${errText.slice(0, 240)}`);
    }
    const json = (await resp.json()) as { data?: { b64_json?: string; url?: string }[] };
    const saved: { path: string; mimeType: string; mediaType: "image" }[] = [];
    for (let i = 0; i < (json.data ?? []).length; i++) {
      const item = json.data![i]!;
      let bytes: Uint8Array;
      let mimeType = "image/png";
      if (item.b64_json) {
        bytes = base64ToBytes(item.b64_json);
      } else if (item.url) {
        const imgResp = await rawFetch(item.url, { signal });
        mimeType = imgResp.headers.get("content-type") || "image/png";
        bytes = new Uint8Array(await imgResp.arrayBuffer());
      } else {
        continue;
      }
      const ext = mimeType.includes("webp") ? "webp" : mimeType.includes("jpeg") ? "jpg" : "png";
      const relPath = `${outRel}/img-${Date.now()}-${i + 1}.${ext}`;
      await vfsWriteBytes(joinPath(input.directory, relPath), bytes, { mimeType });
      saved.push({ path: relPath, mimeType, mediaType: "image" });
    }
    if (!saved.length) throw new Error("未收到有效图像结果");
    return saved;
  }

  if (input.mediaType === "audio") {
    const key = resolveSub2ApiKey("audio");
    if (!key?.key) throw new Error("请先在「创意广场」顶部绑定用于音频生成的 Sub2API API 密钥");
    const resp = await rawFetch(`${origin}/v1/audio/speech`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${key.key}` },
      body: JSON.stringify({
        model: input.modelId || "gpt-4o-mini-tts",
        input: input.prompt || "",
        voice: input.voice || "alloy",
        speed: input.speed || 1,
        response_format: "mp3",
      }),
      signal,
    });
    if (!resp.ok) {
      const errText = await resp.text().catch(() => "");
      throw new Error(`音频生成失败（HTTP ${resp.status}）：${errText.slice(0, 240)}`);
    }
    const bytes = new Uint8Array(await resp.arrayBuffer());
    const mimeType = resp.headers.get("content-type") || "audio/mpeg";
    const relPath = `${outRel}/audio-${Date.now()}.mp3`;
    await vfsWriteBytes(joinPath(input.directory, relPath), bytes, { mimeType });
    return [{ path: relPath, mimeType, mediaType: "audio" }];
  }

  // Video generation (Grok / OpenAI / Seedance via sub2api gateway)
  const key = resolveSub2ApiKey("video");
  if (!key?.key) throw new Error("请先在「创意广场」顶部绑定用于视频生成的 Sub2API API 密钥");
  const model = input.modelId || "grok-imagine-video";
  const isSeedance = /seedance|doubao/i.test(model);
  const endpoint = isSeedance ? `${origin}/api/v3/contents/generations/tasks` : `${origin}/v1/videos/generations`;
  const payload = isSeedance
    ? { model, content: [{ type: "text", text: input.prompt || "" }] }
    : { model, prompt: input.prompt || "", duration: input.duration || 5, aspect_ratio: input.ratio || "16:9" };
  const submitResp = await rawFetch(endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${key.key}` },
    body: JSON.stringify(payload),
    signal,
  });
  if (!submitResp.ok) {
    const errText = await submitResp.text().catch(() => "");
    throw new Error(`视频任务提交失败（HTTP ${submitResp.status}）：${errText.slice(0, 240)}`);
  }
  const submitJson = (await submitResp.json()) as {
    id?: string;
    task_id?: string;
    url?: string;
    data?: { url?: string }[];
  };
  let videoUrl = submitJson.url || submitJson.data?.[0]?.url;
  const remoteTaskId = submitJson.id || submitJson.task_id;
  if (!videoUrl && remoteTaskId) {
    const pollBase = isSeedance
      ? `${origin}/api/v3/contents/generations/tasks/${remoteTaskId}`
      : `${origin}/v1/videos/generations/${remoteTaskId}`;
    for (let attempt = 0; attempt < 90; attempt++) {
      signal?.throwIfAborted();
      await new Promise((r) => setTimeout(r, 3000));
      const pollResp = await rawFetch(pollBase, {
        headers: { Authorization: `Bearer ${key.key}` },
        signal,
      });
      if (!pollResp.ok) continue;
      const pollJson = (await pollResp.json()) as {
        status?: string;
        url?: string;
        content?: { video_url?: string };
        data?: { url?: string }[];
        error?: { message?: string };
      };
      videoUrl = pollJson.url || pollJson.content?.video_url || pollJson.data?.[0]?.url;
      if (videoUrl) break;
      if (pollJson.status && /fail|error|cancel/i.test(pollJson.status)) {
        throw new Error(pollJson.error?.message || `视频生成失败（${pollJson.status}）`);
      }
    }
  }
  if (!videoUrl) throw new Error("视频生成超时或未返回下载地址");
  const vidResp = await rawFetch(videoUrl, { signal });
  const bytes = new Uint8Array(await vidResp.arrayBuffer());
  const mimeType = vidResp.headers.get("content-type") || "video/mp4";
  const relPath = `${outRel}/video-${Date.now()}.mp4`;
  await vfsWriteBytes(joinPath(input.directory, relPath), bytes, { mimeType });
  return [{ path: relPath, mimeType, mediaType: "video" }];
}

export function startAsyncMediaTask(
  rawFetch: typeof fetch,
  input: {
    taskId: string;
    directory: string;
    outputDirectory?: string;
    mediaType: "image" | "video" | "audio";
    providerId?: string;
    modelId?: string;
    prompt?: string;
    size?: string;
    ratio?: string;
    n?: number;
    duration?: number;
    voice?: string;
    speed?: number;
  }
): { taskId: string } {
  const controller = new AbortController();
  const record: MediaTaskRecord = {
    taskId: input.taskId,
    directory: input.directory,
    mediaType: input.mediaType,
    status: "running",
    progress: 10,
    controller,
  };
  mediaTasks.set(input.taskId, record);

  void executeMediaGeneration(rawFetch, input, controller.signal)
    .then((files) => {
      record.status = "completed";
      record.progress = 100;
      record.result = { code: 200, data: files };
    })
    .catch((err) => {
      record.status = controller.signal.aborted ? "cancelled" : "failed";
      record.result = { code: 500, message: err instanceof Error ? err.message : String(err) };
    });

  return { taskId: input.taskId };
}

export async function runSyncMediaGeneration(
  rawFetch: typeof fetch,
  input: Parameters<typeof executeMediaGeneration>[1],
  signal?: AbortSignal
) {
  return executeMediaGeneration(rawFetch, input, signal);
}

export function getMediaTaskStatuses(taskIds: string[]) {
  return taskIds.map((taskId) => {
    const task = mediaTasks.get(taskId);
    if (!task) {
      return { taskId, status: "failed", result: { code: 404, message: "任务不存在或已过期" } };
    }
    return {
      taskId,
      status: task.status,
      progress: task.progress,
      ...(task.result ? { result: task.result } : {}),
    };
  });
}

export function cancelMediaTasks(taskIds: string[]) {
  for (const id of taskIds) {
    const task = mediaTasks.get(id);
    if (task?.controller) task.controller.abort();
    if (task) task.status = "cancelled";
  }
}

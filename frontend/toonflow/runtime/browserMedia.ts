import { executeBrowserFfmpegRequest, FFMPEG_CORE_VERSION, type EmitEvent } from "./browserFfmpegEngine";
import { workspaceMediaPath, type BrowserFfmpegRequest } from "./ffmpegArgs";
import { joinPath, vfsReadBytes, vfsWriteBytes } from "./vfs";
import { translate } from "../upstream/packages/i18n/src/index";

export function getBrowserFfmpegStatus() {
  return {
    platform: "browser",
    arch: "wasm-single-thread",
    target: "browser-wasm",
    supported: true,
    directory: "browser://self-hosted-ffmpeg-wasm",
    version: `ffmpeg.wasm (@ffmpeg/core ${FFMPEG_CORE_VERSION})`,
    sources: [
      {
        id: "browser-wasm",
        label: translate("内置媒体引擎"),
        description: translate("内置媒体探测与视频抽帧处理。"),
        available: true,
      },
    ],
    config: { mode: "auto", source: "browser-wasm" },
    tools: {
      ffmpeg: {
        path: `browser://self-hosted/ffmpeg/${FFMPEG_CORE_VERSION}/ffmpeg-core.wasm`,
        version: `ffmpeg.wasm ${FFMPEG_CORE_VERSION} (single-thread)`,
        source: "system",
        error: null,
      },
      ffprobe: {
        path: `browser://self-hosted/ffmpeg/${FFMPEG_CORE_VERSION}/ffmpeg-core.wasm`,
        version: `ffprobe.wasm ${FFMPEG_CORE_VERSION} (single-thread)`,
        source: "system",
        error: null,
      },
    },
    download: { phase: "idle", received: 0 },
  };
}

export function createFfmpegEventsSse(signal?: AbortSignal): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enc = new TextEncoder();
      controller.enqueue(enc.encode(`event: status\ndata: ${JSON.stringify(getBrowserFfmpegStatus())}\n\n`));
      const timer = setInterval(() => {
        try {
          controller.enqueue(enc.encode(": keepalive\n\n"));
        } catch {
          clearInterval(timer);
        }
      }, 20000);
      signal?.addEventListener(
        "abort",
        () => {
          clearInterval(timer);
          try {
            controller.close();
          } catch {
            // ignore
          }
        },
        { once: true }
      );
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" },
  });
}

async function probeMediaInBrowser(bytes: Uint8Array, mimeType: string): Promise<{
  duration: number;
  width: number;
  height: number;
}> {
  const blob = new Blob([bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer], { type: mimeType });
  const url = URL.createObjectURL(blob);
  try {
    if (mimeType.startsWith("audio/")) {
      const audio = document.createElement("audio");
      audio.preload = "metadata";
      audio.src = url;
      await new Promise<void>((resolve, reject) => {
        audio.onloadedmetadata = () => resolve();
        audio.onerror = () => reject(new Error("无法读取音频元信息"));
      });
      return { duration: Number.isFinite(audio.duration) ? audio.duration : 0, width: 0, height: 0 };
    }
    const video = document.createElement("video");
    video.preload = "metadata";
    video.muted = true;
    video.src = url;
    await new Promise<void>((resolve, reject) => {
      video.onloadedmetadata = () => resolve();
      video.onerror = () => reject(new Error("无法读取视频元信息"));
    });
    return {
      duration: Number.isFinite(video.duration) ? video.duration : 0,
      width: video.videoWidth || 1280,
      height: video.videoHeight || 720,
    };
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function extractVideoFrameInBrowser(bytes: Uint8Array, mimeType: string, timeSec: number): Promise<Uint8Array> {
  const blob = new Blob([bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer], { type: mimeType });
  const url = URL.createObjectURL(blob);
  try {
    const video = document.createElement("video");
    video.muted = true;
    video.playsInline = true;
    video.preload = "auto";
    video.src = url;
    await new Promise<void>((resolve, reject) => {
      video.onloadeddata = () => resolve();
      video.onerror = () => reject(new Error("无法加载视频进行抽帧"));
    });
    const clamped = Math.max(0, Math.min(Math.max(0, (video.duration || 1) - 0.05), timeSec));
    if (Math.abs(video.currentTime - clamped) > 0.01) {
      await new Promise<void>((resolve) => {
        const done = () => resolve();
        video.onseeked = done;
        video.currentTime = clamped;
        setTimeout(done, 1500);
      });
    }
    const canvas = document.createElement("canvas");
    canvas.width = video.videoWidth || 1280;
    canvas.height = video.videoHeight || 720;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("无法创建浏览器 2D Canvas 上下文");
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    const outBlob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("抽帧编码失败"))), "image/webp", 0.9);
    });
    return new Uint8Array(await outBlob.arrayBuffer());
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function probeWorkspaceMedia(
  directory: string,
  relPath: string
): Promise<{ path: string; mimeType: string; sizeBytes: number; duration: number; width: number; height: number }> {
  const { bytes, mimeType } = await vfsReadBytes(joinPath(directory, relPath));
  const meta = await probeMediaInBrowser(bytes, mimeType);
  return {
    path: relPath,
    mimeType,
    sizeBytes: bytes.byteLength,
    duration: meta.duration,
    width: meta.width,
    height: meta.height,
  };
}

export async function extractWorkspaceVideoFrame(
  directory: string,
  relPath: string,
  timeSec: number,
  outputRelPath: string
): Promise<{ source: string; output: string; timeSec: number; sizeBytes: number; mimeType: string }> {
  const { bytes, mimeType } = await vfsReadBytes(joinPath(directory, relPath));
  const frame = await extractVideoFrameInBrowser(bytes, mimeType, timeSec);
  await vfsWriteBytes(joinPath(directory, outputRelPath), frame, { mimeType: "image/webp" });
  return {
    source: relPath,
    output: outputRelPath,
    timeSec,
    sizeBytes: frame.byteLength,
    mimeType: "image/webp",
  };
}

async function runCanvasCommand(body: BrowserFfmpegRequest, signal: AbortSignal, emit: EmitEvent) {
  const calls = body.calls ?? [];
  const source = body.options?.source ?? calls.find(c => c.method === "input" || c.method === "addInput")?.args[0];
  const input = workspaceMediaPath(source);
  const { bytes, mimeType } = await vfsReadBytes(joinPath(body.directory, input));
  signal.throwIfAborted();
  emit("start", [`browser-canvas ${body.operation.method}`]);
  if (body.operation.method === "save") {
    const out = workspaceMediaPath(body.operation.args[0]);
    const seek = calls.find(c => c.method === "seekInput" || c.method === "setStartTime");
    const frame = await extractVideoFrameInBrowser(bytes, mimeType, Number(seek?.args[0]) || 0);
    signal.throwIfAborted();
    await vfsWriteBytes(joinPath(body.directory, out), frame, { mimeType: "image/webp" });
  }
  signal.throwIfAborted();
  emit("progress", [{ percent: 100 }]);
  emit("end", ["", ""]);
}

const ffmpegJobs = new Map<string, { controller: AbortController; done: Promise<void> }>();

export async function handleBrowserFfmpegExecute(body: BrowserFfmpegRequest, signal?: AbortSignal): Promise<Response> {
  const { method, args } = body.operation;
  if (method === "prepare") {
    // Canvas needs no WASM; the first probe or encode loads the core and reports any loading error via SSE.
    return Response.json({ code: 200, data: { ready: true }, message: "ok" });
  }
  if (method === "cancel") {
    const job = ffmpegJobs.get(body.requestId);
    job?.controller.abort(new DOMException("FFmpeg 任务已取消", "AbortError"));
    await job?.done;
    return Response.json({ code: 200, data: { cancelled: !!job }, message: "ok" });
  }
  if (!body.requestId || ffmpegJobs.has(body.requestId)) {
    return Response.json({ code: 400, message: "FFmpeg 请求 ID 缺失或重复" }, { status: 400 });
  }
  const controller = new AbortController();
  const abort = () => controller.abort(signal?.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  let closed = false;
  const stream = new ReadableStream<Uint8Array>({
    start(streamController) {
      const enc = new TextEncoder();
      const emit: EmitEvent = (event, args) => {
        if (!closed) streamController.enqueue(enc.encode(`data: ${JSON.stringify({ event, args })}\n\n`));
      };
      const canvas = method === "screenshots" || (method === "save" && typeof args[0] === "string" && /\.(png|jpe?g|webp)$/i.test(args[0]));
      const execute = canvas ? runCanvasCommand : executeBrowserFfmpegRequest;
      const done = execute(body, controller.signal, emit).catch(error => {
        emit("error", [{ message: error instanceof Error ? error.message : String(error), name: error?.name }]);
      }).finally(() => {
        ffmpegJobs.delete(body.requestId);
        signal?.removeEventListener("abort", abort);
        if (!closed) streamController.close();
      });
      ffmpegJobs.set(body.requestId, { controller, done });
    },
    cancel() {
      closed = true;
      controller.abort();
    },
  });
  return new Response(stream, { headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" } });
}

export function getBrowserLocalModelsStatus() {
  const hasWebGpu = typeof navigator !== "undefined" && "gpu" in navigator;
  return {
    supported: false,
    runtime: hasWebGpu ? "WebGPU" : "WebGL",
    directory: "browser://unsupported",
    target: "browser-web",
    version: "",
    fileName: "",
    sources: [],
    installed: false,
    installing: false,
    items: [],
  };
}

export function createLocalModelsSse(signal?: AbortSignal): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enc = new TextEncoder();
      controller.enqueue(enc.encode(`event: status\ndata: ${JSON.stringify(getBrowserLocalModelsStatus())}\n\n`));
      const timer = setInterval(() => {
        try {
          controller.enqueue(enc.encode(": keepalive\n\n"));
        } catch {
          clearInterval(timer);
        }
      }, 20000);
      signal?.addEventListener(
        "abort",
        () => {
          clearInterval(timer);
          try {
            controller.close();
          } catch {
            // ignore
          }
        },
        { once: true }
      );
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" },
  });
}

export async function handleLocalModelMediaProcess(
  _kind: "image" | "video" | "audio",
  body: { directory: string; source: string; output: string; options?: Record<string, unknown>; cancel?: boolean }
): Promise<Response> {
  if (body.cancel) {
    return new Response(JSON.stringify({ code: 200, data: { cancelled: true } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder();
      const emit = (event: string, data: unknown) => {
        controller.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
      };
      try {
        throw new Error(translate("当前为纯浏览器本地运行模式，暂不支持桌面端 ONNX 本地模型推理。"));
      } catch (err) {
        emit("error", { message: err instanceof Error ? err.message : String(err) });
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

// ==================== Browser Plugin Server Bus ====================

const pluginServerConnections = new Map<string, { push: (msg: unknown) => void }>();

export function connectPluginServer() {
  const connectionId = crypto.randomUUID();
  const token = crypto.randomUUID();
  return { connectionId, token };
}

export function createPluginServerEventsSse(connectionId: string, signal?: AbortSignal): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enc = new TextEncoder();
      const push = (msg: unknown) => {
        try {
          controller.enqueue(enc.encode(`data: ${JSON.stringify(msg)}\n\n`));
        } catch {
          // ignore
        }
      };
      pluginServerConnections.set(connectionId, { push });
      push({ type: "ready" });
      const timer = setInterval(() => {
        try {
          controller.enqueue(enc.encode(": keepalive\n\n"));
        } catch {
          clearInterval(timer);
        }
      }, 20000);
      signal?.addEventListener(
        "abort",
        () => {
          clearInterval(timer);
          pluginServerConnections.delete(connectionId);
          try {
            controller.close();
          } catch {
            // ignore
          }
        },
        { once: true }
      );
    },
    cancel() {
      pluginServerConnections.delete(connectionId);
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" },
  });
}

export function handlePluginServerMessage(body: { connectionId: string; message?: { type?: string; requestId?: string; serviceId?: string } }) {
  const conn = pluginServerConnections.get(body.connectionId);
  const msg = body.message;
  if (conn && msg?.type === "register" && msg.requestId) {
    conn.push({ type: "registered", requestId: msg.requestId, serviceId: crypto.randomUUID() });
  }
  return { ok: true };
}

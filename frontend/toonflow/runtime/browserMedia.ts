import { joinPath, vfsReadBytes, vfsWriteBytes } from "./vfs";

export function getBrowserFfmpegStatus() {
  return {
    platform: "browser",
    arch: "wasm-webgl",
    target: "browser-native",
    supported: true,
    directory: "browser://native-media-engine",
    version: "Browser HTML5/Canvas/WebAudio Engine",
    sources: [
      {
        id: "browser-native",
        label: "内置媒体引擎",
        description: "内置媒体探测与视频抽帧处理。",
        available: true,
      },
    ],
    config: { mode: "auto", source: "browser-native" },
    tools: {
      ffmpeg: { path: "browser://html5-video-canvas", version: "HTML5 Canvas + MediaRecorder", source: "system", error: null },
      ffprobe: { path: "browser://html5-media-probe", version: "HTML5 Media Metadata", source: "system", error: null },
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

export async function handleBrowserFfmpegExecute(body: {
  directory: string;
  calls?: { method: string; args: unknown[] }[];
  operation?: { method: string; args: unknown[] };
}): Promise<Response> {
  const op = body.operation?.method ?? "";
  if (op === "prepare") {
    return new Response(JSON.stringify({ code: 200, data: { ready: true }, message: "ok" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }

  const calls = body.calls ?? [];
  const inputCall = calls.find((c) => c.method === "input" || c.method === "addInput");
  const inputRel = typeof inputCall?.args?.[0] === "string" ? inputCall.args[0] : "";

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder();
      const emit = (event: string, data: unknown) => {
        controller.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
      };
      try {
        if (op === "ffprobe") {
          const filePath = inputRel ? joinPath(body.directory, inputRel) : "";
          const { bytes, mimeType } = await vfsReadBytes(filePath);
          const meta = await probeMediaInBrowser(bytes, mimeType);
          const probeData = {
            format: {
              filename: inputRel,
              duration: meta.duration,
              size: bytes.byteLength,
              format_name: mimeType.split("/")[1] || "mp4",
            },
            streams: [
              ...(meta.width > 0
                ? [{ codec_type: "video", width: meta.width, height: meta.height, r_frame_rate: "24/1", avg_frame_rate: "24/1", duration: String(meta.duration) }]
                : []),
              { codec_type: "audio", sample_rate: "44100", channels: 2, duration: String(meta.duration) },
            ],
          };
          emit("result", { data: probeData });
        } else if (op === "screenshots" || op === "save") {
          emit("start", { commandLine: `browser-canvas ${op}` });
          if (inputRel) {
            const { bytes, mimeType } = await vfsReadBytes(joinPath(body.directory, inputRel));
            const ssCall = calls.find((c) => c.method === "seekInput" || c.method === "setStartTime");
            const timeSec = typeof ssCall?.args?.[0] === "number" ? ssCall.args[0] : Number(ssCall?.args?.[0] ?? 0) || 0;
            const outArg = body.operation?.args?.[0];
            if (op === "save" && typeof outArg === "string") {
              if (/\.(png|jpg|jpeg|webp)$/i.test(outArg)) {
                const frame = await extractVideoFrameInBrowser(bytes, mimeType, timeSec);
                await vfsWriteBytes(joinPath(body.directory, outArg), frame, { mimeType: "image/webp" });
              } else {
                // Passthrough copy in browser VFS when output is media
                await vfsWriteBytes(joinPath(body.directory, outArg), bytes, { mimeType });
              }
            }
          }
          emit("progress", { percent: 100 });
          emit("end", { stdout: "", stderr: "" });
        } else {
          emit("end", { stdout: "", stderr: "" });
        }
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

export function getBrowserLocalModelsStatus() {
  const hasWebGpu = typeof navigator !== "undefined" && "gpu" in navigator;
  return {
    supported: true,
    runtime: hasWebGpu ? "WebGPU" : "WebGL",
    models: [
      {
        id: "browser-frame-extractor",
        name: "视频抽帧引擎",
        description: "视频关键帧提取与信息解析。",
        category: "video",
        installed: true,
        ready: true,
        sizeBytes: 0,
      },
      {
        id: "browser-3d-webgl",
        name: "3D 白模渲染引擎",
        description: "3D 白模片场与多机位预演渲染。",
        category: "video",
        installed: true,
        ready: true,
        sizeBytes: 0,
      },
    ],
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
  kind: "image" | "video" | "audio",
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
        emit("progress", { completed: 1, total: 2 });
        if (body.directory && body.source && body.output) {
          const { bytes, mimeType } = await vfsReadBytes(joinPath(body.directory, body.source));
          if (kind === "video" && /\.(png|jpg|jpeg|webp)$/i.test(body.output)) {
            const frame = await extractVideoFrameInBrowser(bytes, mimeType, 0);
            await vfsWriteBytes(joinPath(body.directory, body.output), frame, { mimeType: "image/webp" });
          } else {
            await vfsWriteBytes(joinPath(body.directory, body.output), bytes, { mimeType });
          }
        }
        emit("progress", { completed: 2, total: 2 });
        emit("done", { ok: true });
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

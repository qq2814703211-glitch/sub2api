import { compileFfmpegRequest, type BrowserFfmpegRequest } from "./ffmpegArgs";
import type { WorkerReply, WorkerRequest } from "./ffmpegWorker";
import { joinPath, vfsReadBytes, vfsWriteBytes } from "./vfs";

export const FFMPEG_CORE_VERSION = "0.12.10";
export type EmitEvent = (event: string, args: unknown[]) => void;

// Reuse a single WASM heap for probe → encode → probe; release it when the studio is idle.
let worker: Worker | undefined;
let idleTimer: ReturnType<typeof setTimeout> | undefined;
let queue = Promise.resolve();

function disposeWorker() {
  worker?.terminate();
  worker = undefined;
}

async function runWorker(job: WorkerRequest, signal: AbortSignal, emit: EmitEvent) {
  signal.throwIfAborted();
  if (typeof Worker !== "function" || typeof WebAssembly !== "object") {
    throw new Error("当前浏览器不支持 Web Worker 或 WebAssembly，无法执行本地 FFmpeg");
  }
  clearTimeout(idleTimer);
  const active = worker ??= new Worker(new URL("./ffmpegWorker.ts", import.meta.url), { type: "module" });
  let abort: (() => void) | undefined;
  try {
    return await new Promise<Exclude<WorkerReply, { type: "progress" | "error" }>>((resolve, reject) => {
      abort = () => reject(signal.reason);
      signal.addEventListener("abort", abort, { once: true });
      active.onerror = error => reject(new Error(`浏览器 FFmpeg Worker 加载或执行失败：${error.message}`));
      active.onmessageerror = () => reject(new Error("无法读取浏览器 FFmpeg Worker 响应"));
      active.onmessage = ({ data }: MessageEvent<WorkerReply>) => {
        if (data.type === "progress") emit("progress", [{ percent: data.percent }]);
        else if (data.type === "error") reject(new Error(data.message));
        else resolve(data);
      };
      active.postMessage(job, [job.input.buffer]);
    });
  } catch (error) {
    // FFmpeg is synchronous inside the worker. Termination also stops a running encode or a pending fetch.
    disposeWorker();
    throw error;
  } finally {
    if (abort) signal.removeEventListener("abort", abort);
    active.onmessage = active.onerror = active.onmessageerror = null;
    if (worker) idleTimer = setTimeout(disposeWorker, 180_000);
  }
}

async function execute(request: BrowserFfmpegRequest, signal: AbortSignal, emit: EmitEvent) {
  const plan = compileFfmpegRequest(request.options, request.calls ?? [], request.operation, "/job");
  const input = plan.kind === "ffprobe" ? plan.input : plan.inputs[0];
  const { bytes } = await vfsReadBytes(joinPath(request.directory, input.relPath));
  signal.throwIfAborted();
  if (!bytes.length) throw new Error(`输入媒体文件为空：${input.relPath}`);
  if (plan.kind === "exec") emit("start", [`ffmpeg ${plan.args.join(" ")}`]);
  const base = new URL(import.meta.env.BASE_URL, window.location.href);
  const reply = await runWorker({
    coreURL: new URL(`ffmpeg/${FFMPEG_CORE_VERSION}/ffmpeg-core.js`, base).href,
    type: plan.kind, args: plan.args, inputPath: input.virtualPath, input: bytes,
    outputPaths: plan.kind === "ffprobe" ? [plan.probeOutputPath] : plan.outputs.map(file => file.virtualPath),
  }, signal, emit);
  signal.throwIfAborted();

  if (plan.kind === "ffprobe" && reply.type === "ffprobeResult") {
    const data = JSON.parse(reply.jsonText);
    if (!data?.format || !Array.isArray(data.streams) || !data.streams.length) {
      throw new Error(`FFprobe 无法读取媒体，文件可能损坏或格式不受支持：${reply.stderr || input.relPath}`);
    }
    data.format.filename = input.relPath;
    for (const key of ["duration", "size", "bit_rate"]) {
      if (Number.isFinite(Number(data.format[key]))) data.format[key] = Number(data.format[key]);
    }
    emit("result", [data]);
  } else if (plan.kind === "exec" && reply.type === "execResult") {
    const files = plan.outputs.map(ref => {
      const data = reply.outputs.find(file => file.path === ref.virtualPath)?.data;
      if (!data?.byteLength) throw new Error(`浏览器 FFmpeg 未生成有效输出：${ref.relPath}`);
      return { ref, data };
    });
    for (const { ref, data } of files) {
      await vfsWriteBytes(joinPath(request.directory, ref.relPath), new Uint8Array(data), { mimeType: ref.mimeType });
      signal.throwIfAborted();
    }
    emit("end", [reply.stdout, reply.stderr]);
  } else {
    throw new Error("浏览器 FFmpeg 返回了意外的执行结果");
  }
}

export function executeBrowserFfmpegRequest(request: BrowserFfmpegRequest, signal: AbortSignal, emit: EmitEvent) {
  // Cancel a waiting job immediately, without unlocking or terminating the job ahead of it.
  if (signal.aborted) return Promise.reject(signal.reason);
  return new Promise<void>((resolve, reject) => {
    const cancelWaiting = () => reject(signal.reason);
    signal.addEventListener("abort", cancelWaiting, { once: true });
    queue = queue.then(async () => {
      signal.removeEventListener("abort", cancelWaiting);
      try {
        signal.throwIfAborted();
        await execute(request, signal, emit);
        resolve();
      } catch (error) {
        reject(error);
      }
    });
  });
}

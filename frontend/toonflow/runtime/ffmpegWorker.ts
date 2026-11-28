interface FfmpegCore {
  FS: {
    mkdir(path: string): void;
    writeFile(path: string, bytes: Uint8Array): void;
    readFile(path: string): Uint8Array & { buffer: ArrayBuffer };
    readdir(path: string): string[];
    unlink(path: string): void;
    rmdir(path: string): void;
  };
  exec(...args: string[]): number;
  ffprobe(...args: string[]): number;
  reset(): void;
  setLogger(callback: (entry: { type: string; message: string }) => void): void;
  setProgress(callback: (entry: { progress: number }) => void): void;
}

export interface WorkerRequest {
  coreURL: string;
  type: "exec" | "ffprobe";
  args: string[];
  inputPath: string;
  input: Uint8Array;
  outputPaths: string[];
}

export type WorkerReply =
  | { type: "progress"; percent: number }
  | { type: "ffprobeResult"; jsonText: string; stderr: string }
  | { type: "execResult"; outputs: { path: string; data: ArrayBuffer }[]; stdout: string; stderr: string }
  | { type: "error"; message: string };

const worker = self as unknown as {
  onmessage: (event: MessageEvent<WorkerRequest>) => void;
  postMessage(message: WorkerReply, transfer?: Transferable[]): void;
};
let core: FfmpegCore | undefined;

async function loadCore(coreURL: string): Promise<FfmpegCore> {
  try {
    const { default: createCore } = await import(/* @vite-ignore */ coreURL);
    const response = await fetch(new URL("ffmpeg-core.wasm", coreURL));
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return await createCore({ wasmBinary: new Uint8Array(await response.arrayBuffer()) });
  } catch (error) {
    throw new Error(`浏览器 FFmpeg 核心加载失败（${coreURL}）：${error}`);
  }
}

// The engine sends only one request at a time. The private directory contains only flat, generated filenames.
worker.onmessage = async ({ data: job }) => {
  try {
    core ??= await loadCore(job.coreURL);
    const fs = core.FS;
    const dir = job.inputPath.slice(0, job.inputPath.lastIndexOf("/"));
    let diagnostic = "";
    core.setLogger(({ type, message }) => {
      if (type === "stderr" && message.trim() && message !== "Aborted()") diagnostic = message;
    });
    core.setProgress(({ progress }) => {
      if (job.type === "exec" && Number.isFinite(progress)) {
        worker.postMessage({ type: "progress", percent: Math.min(100, Math.max(0, Math.round(progress * 100))) });
      }
    });
    let reply: WorkerReply;
    let transfers: Transferable[] = [];
    fs.mkdir(dir);
    try {
      fs.writeFile(job.inputPath, job.input);
      if (job.type === "ffprobe") {
        const code = core.ffprobe(...job.args);
        // core 0.12.10 returns -1 even on success; corrupt input can still produce an empty JSON object.
        if (code > 0) throw new Error(`FFprobe 无法读取媒体，文件可能损坏：${diagnostic || code}`);
        let jsonText: string;
        try {
          jsonText = new TextDecoder().decode(fs.readFile(job.outputPaths[0]));
        } catch {
          throw new Error(`FFprobe 无法读取媒体，文件可能损坏：${diagnostic || "未生成探测结果"}`);
        }
        reply = { type: "ffprobeResult", jsonText, stderr: diagnostic };
      } else {
        const code = core.exec("-v", "error", ...job.args);
        if (code !== 0) throw new Error(`浏览器 FFmpeg 转码失败：${diagnostic || `退出码 ${code}`}`);
        const files = job.outputPaths.map(path => fs.readFile(path));
        if (files.some(bytes => !bytes.length)) throw new Error("浏览器 FFmpeg 输出文件为空");
        const outputs = files.map((bytes, i) => ({ path: job.outputPaths[i], data: bytes.buffer }));
        reply = { type: "execResult", outputs, stdout: "", stderr: diagnostic };
        transfers = outputs.map(file => file.data);
      }
    } finally {
      core.setProgress(() => {});
      core.setLogger(() => {});
      core.reset();
      for (const name of fs.readdir(dir)) {
        if (name !== "." && name !== "..") fs.unlink(`${dir}/${name}`);
      }
      fs.rmdir(dir);
    }
    worker.postMessage(reply, transfers);
  } catch (error) {
    worker.postMessage({ type: "error", message: error instanceof Error ? error.message : String(error) });
  }
};

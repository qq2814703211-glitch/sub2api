import { guessMimeType, normalizePath } from "./vfs";

interface Call {
  method: string;
  args: unknown[];
}

export interface BrowserFfmpegRequest {
  directory: string;
  requestId: string;
  options?: { source?: string };
  calls?: Call[];
  operation: Call;
}

interface FileRef {
  relPath: string;
  virtualPath: string;
  mimeType: string;
}

type Plan =
  | { kind: "ffprobe"; input: FileRef; probeOutputPath: string; args: string[] }
  | { kind: "exec"; inputs: [FileRef]; outputs: FileRef[]; args: string[] };

export function workspaceMediaPath(value: unknown): string {
  const raw = typeof value === "string" ? value.trim() : "";
  if (!raw || /^[\\/]|^[a-z][a-z\d+.-]*:/i.test(raw)
    || Array.from(raw).some(c => c.charCodeAt(0) < 32) || raw.split(/[\\/]/).some(part => part.trim() === "..")) {
    throw new Error(`无效的工作区媒体路径：${value}`);
  }
  const path = normalizePath(raw).slice(1).trimEnd();
  if (!path) throw new Error("工作区媒体路径不能为空");
  return path;
}

function fileRef(path: unknown, virtualBase: string): FileRef {
  const relPath = workspaceMediaPath(path);
  const ext = relPath.match(/\.[\da-z]{1,8}$/i)?.[0] ?? ".bin";
  return { relPath, virtualPath: virtualBase + ext, mimeType: guessMimeType(relPath) };
}

// Only the options emitted by videoProcessing.processVideo. Do not pass arbitrary CLI arguments to the worker.
const outputOptions: Record<string, RegExp> = {
  "-map": /^0:(?:\d+|a:0\??)$/,
  "-preset": /^fast$/,
  "-crf": /^18$/,
  "-pix_fmt": /^yuv420p$/,
  "-movflags": /^\+faststart$/,
};

export function compileFfmpegRequest(
  options: BrowserFfmpegRequest["options"], calls: Call[], operation: Call, workDir: string
): Plan {
  const input = fileRef(options?.source, `${workDir}/in_0`);
  if (operation.method === "ffprobe" && !calls.length && !operation.args.length) {
    const probeOutputPath = `${workDir}/probe.json`;
    return { kind: "ffprobe", input, probeOutputPath,
      args: ["-v", "error", "-show_format", "-show_streams", "-of", "json", "-o", probeOutputPath, input.virtualPath] };
  }
  if (operation.method !== "run") throw new Error(`浏览器端 FFmpeg 暂不支持操作：${operation.method}`);

  const inputArgs: string[] = [];
  const outputs: FileRef[] = [];
  const args: string[] = [];
  let output: FileRef | undefined;
  for (const { method, args: values } of calls) {
    const value = values[0];
    if (method === "seekInput" && typeof value === "number" && Number.isFinite(value) && value >= 0) {
      inputArgs.push("-ss", String(value));
    } else if (method === "output") {
      if (output) args.push(output.virtualPath);
      const next = fileRef(value, `${workDir}/out_${outputs.length}`);
      if (next.relPath === input.relPath || outputs.some(file => file.relPath === next.relPath)) {
        throw new Error("输出文件不能覆盖原视频或彼此覆盖");
      }
      outputs.push(next);
      output = next;
    } else if (!output) {
      throw new Error(`FFmpeg 输出选项前缺少 output：${method}`);
    } else if (method === "noVideo" || method === "noAudio") {
      args.push(method === "noVideo" ? "-vn" : "-an");
    } else if (method === "audioCodec" && (value === "aac" || value === "libmp3lame")) {
      args.push("-c:a", value);
    } else if (method === "videoCodec" && value === "libx264") {
      args.push("-c:v", value);
    } else if (method === "audioBitrate" && value === 192) {
      args.push("-b:a", "192k");
    } else if (method === "format" && (value === "mp3" || value === "mp4")) {
      args.push("-f", value);
    } else if (method === "duration" && typeof value === "number" && Number.isFinite(value) && value > 0) {
      args.push("-t", String(value));
    } else if (method === "videoFilters" && Array.isArray(value) && value.length && value.every(filter =>
      typeof filter === "string" && (filter === "pad=ceil(iw/2)*2:ceil(ih/2)*2" || /^setpts=\(PTS-STARTPTS\)\/[\d.e+-]+$/.test(filter)))) {
      args.push("-vf", value.join(","));
    } else if (method === "audioFilters" && typeof value === "string" && /^atempo=[\d.e+-]+(?:,atempo=[\d.e+-]+)*$/.test(value)) {
      args.push("-af", value);
    } else if (method === "outputOptions") {
      for (let i = 0; i < values.length; i += 2) {
        const flag = String(values[i]);
        const arg = values[i + 1];
        if (!Object.hasOwn(outputOptions, flag) || typeof arg !== "string" || !outputOptions[flag].test(arg)) {
          throw new Error(`浏览器端 FFmpeg 暂不支持输出选项：${flag} ${arg}`);
        }
        args.push(flag, arg);
      }
    } else {
      throw new Error(`浏览器端 FFmpeg 暂不支持调用：${method}`);
    }
  }
  if (!output) throw new Error("FFmpeg 命令缺少输出文件路径");
  return { kind: "exec", inputs: [input], outputs, args: [...inputArgs, "-i", input.virtualPath, ...args, output.virtualPath] };
}

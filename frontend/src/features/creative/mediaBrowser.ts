/**
 * Pure browser-side media computation utilities (ZERO server CPU / FFmpeg).
 *
 * Provides local File -> DataURL conversion, image dimension probing, and
 * HTML5 `<video>` + `<canvas>` frame extraction so users can perform Toonflow-
 * style frame analysis (逐帧拉片) directly on their own device.
 */
import type { ExtractedFrame } from './types'
import { createNodeId } from './graph'

export function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error ?? new Error('读取本地文件失败'))
    reader.readAsDataURL(file)
  })
}

export function probeImageDimensions(
  src: string,
): Promise<{ width: number; height: number } | null> {
  return new Promise((resolve) => {
    if (typeof Image === 'undefined') {
      resolve(null)
      return
    }
    const img = new Image()
    img.onload = () => resolve({ width: img.naturalWidth, height: img.naturalHeight })
    img.onerror = () => resolve(null)
    img.src = src
  })
}

/**
 * Downscales very large uploaded reference images in the browser canvas so
 * IndexedDB stays snappy without touching the server.
 */
export async function optimizeLocalImageDataUrl(
  file: File,
  maxEdge = 2048,
): Promise<{ dataUrl: string; width?: number; height?: number; mimeType: string }> {
  const rawDataUrl = await readFileAsDataUrl(file)
  const dims = await probeImageDimensions(rawDataUrl)
  if (!dims || (dims.width <= maxEdge && dims.height <= maxEdge)) {
    return {
      dataUrl: rawDataUrl,
      width: dims?.width,
      height: dims?.height,
      mimeType: file.type || 'image/png',
    }
  }
  const scale = maxEdge / Math.max(dims.width, dims.height)
  const width = Math.max(1, Math.round(dims.width * scale))
  const height = Math.max(1, Math.round(dims.height * scale))
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) {
    return { dataUrl: rawDataUrl, width: dims.width, height: dims.height, mimeType: file.type || 'image/png' }
  }
  const img = new Image()
  await new Promise<void>((resolve, reject) => {
    img.onload = () => resolve()
    img.onerror = () => reject(new Error('图片解码失败'))
    img.src = rawDataUrl
  })
  ctx.drawImage(img, 0, 0, width, height)
  const mimeType = file.type === 'image/png' ? 'image/png' : 'image/webp'
  return {
    dataUrl: canvas.toDataURL(mimeType, 0.9),
    width,
    height,
    mimeType,
  }
}

export interface FrameExtractionResult {
  durationSec: number
  frames: ExtractedFrame[]
}

/**
 * Extracts evenly spaced keyframes (or explicit timestamps) from a local/blob/
 * CORS-enabled video strictly inside the user's browser using `<video>` + `<canvas>`.
 */
export async function extractVideoFramesInBrowser(
  videoUrl: string,
  options: {
    count?: number
    timestamps?: number[]
    maxWidth?: number
    quality?: number
  } = {},
): Promise<FrameExtractionResult> {
  if (!videoUrl) {
    throw new Error('请先连接上游视频节点或拖入本地视频文件')
  }
  if (typeof document === 'undefined') {
    throw new Error('当前环境不支持浏览器视频抽帧')
  }

  const maxWidth = options.maxWidth ?? 960
  const quality = options.quality ?? 0.88
  const video = document.createElement('video')
  video.crossOrigin = 'anonymous'
  video.muted = true
  video.playsInline = true
  video.preload = 'auto'
  video.src = videoUrl

  await new Promise<void>((resolve, reject) => {
    const onLoaded = () => {
      cleanup()
      resolve()
    }
    const onError = () => {
      cleanup()
      reject(new Error('无法加载视频进行本机抽帧（若为跨域远程视频，请先下载到本地后拖入节点）'))
    }
    const cleanup = () => {
      video.removeEventListener('loadedmetadata', onLoaded)
      video.removeEventListener('error', onError)
    }
    video.addEventListener('loadedmetadata', onLoaded, { once: true })
    video.addEventListener('error', onError, { once: true })
    video.load()
  })

  const durationSec = Number.isFinite(video.duration) && video.duration > 0 ? video.duration : 1
  const naturalW = video.videoWidth || 1280
  const naturalH = video.videoHeight || 720
  const scale = naturalW > maxWidth ? maxWidth / naturalW : 1
  const width = Math.max(1, Math.round(naturalW * scale))
  const height = Math.max(1, Math.round(naturalH * scale))

  const count = Math.max(1, Math.min(12, Math.round(options.count ?? 4)))
  const targets =
    options.timestamps && options.timestamps.length > 0
      ? options.timestamps.map((t) => Math.max(0, Math.min(durationSec - 0.05, t)))
      : Array.from({ length: count }, (_, idx) => {
          if (count === 1) return Math.min(durationSec * 0.5, Math.max(0, durationSec - 0.05))
          const ratio = idx / (count - 1)
          return Math.max(0, Math.min(durationSec - 0.05, ratio * durationSec))
        })

  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('浏览器 Canvas 2D 上下文不可用')

  const frames: ExtractedFrame[] = []
  for (const targetTime of targets) {
    await new Promise<void>((resolve, reject) => {
      const onSeeked = () => {
        cleanup()
        resolve()
      }
      const onError = () => {
        cleanup()
        reject(new Error('视频跳转帧失败'))
      }
      const cleanup = () => {
        video.removeEventListener('seeked', onSeeked)
        video.removeEventListener('error', onError)
      }
      video.addEventListener('seeked', onSeeked, { once: true })
      video.addEventListener('error', onError, { once: true })
      video.currentTime = targetTime
    })

    ctx.drawImage(video, 0, 0, width, height)
    let dataUrl: string
    try {
      dataUrl = canvas.toDataURL('image/webp', quality)
    } catch {
      throw new Error('远程视频未开放 CORS，浏览器无法直接读取像素；请先下载视频到本地后再拖入抽帧节点')
    }
    frames.push({
      id: createNodeId('frame'),
      timeSec: Number(targetTime.toFixed(2)),
      dataUrl,
      width,
      height,
    })
  }

  video.src = ''
  return { durationSec: Number(durationSec.toFixed(2)), frames }
}

/** Downloads a JSON object as a local file strictly in the browser. */
export function downloadJsonFile(data: unknown, filename: string): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** Downloads plain text / Markdown locally in the browser. */
export function downloadTextFile(text: string, filename: string, mime = 'text/markdown;charset=utf-8'): void {
  const blob = new Blob([text], { type: mime })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

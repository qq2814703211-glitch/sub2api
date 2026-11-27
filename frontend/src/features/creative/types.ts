/**
 * Core domain types for the browser-native Creative Plaza (创意广场).
 *
 * Inspired by Toonflow's visual workflow (infinite canvas + storyboard + local
 * asset vault), every project, graph, storyboard and media extraction runs and
 * persists strictly inside the user's browser (IndexedDB + HTML5 Canvas/Video).
 * The Sub2API server only proxies standard gateway API calls billed to the
 * user's own API keys — zero server-side FFmpeg, agent runtime, or file storage.
 */

export type CreativeTab = 'canvas' | 'imagegen' | 'storyboard' | 'assets'

export type CanvasNodeType = 'text' | 'image' | 'imageGen' | 'videoGen' | 'frameExtract'

export type PortKind = 'text' | 'image' | 'video'

export type NodeRunStatus = 'idle' | 'running' | 'success' | 'error'

export type VideoProtocol = 'auto' | 'grok' | 'compatible' | 'seedance'

export type TextNodeMode = 'note' | 'ai-expand' | 'ai-storyboard'

export interface ExtractedFrame {
  id: string
  timeSec: number
  dataUrl: string
  width: number
  height: number
}

export interface TextNodeData {
  text: string
  systemPrompt: string
  model: string
  mode: TextNodeMode
}

export interface ImageNodeData {
  src: string
  name: string
  width?: number
  height?: number
  mimeType?: string
  assetId?: string
}

export interface ImageGenNodeData {
  prompt: string
  model: string
  size: string
  quality: string
  format: 'png' | 'jpeg' | 'webp'
  style: string
  count: number
  outputImages: string[]
  selectedOutputIndex: number
}

export interface VideoGenNodeData {
  prompt: string
  model: string
  protocol: VideoProtocol
  duration: number
  aspectRatio: string
  resolution: string
  taskId: string
  taskStatus: string
  outputVideoUrl: string
}

export interface FrameExtractNodeData {
  sourceVideoUrl: string
  sourceName: string
  frameCount: number
  timestampSec: number
  durationSec: number
  frames: ExtractedFrame[]
  selectedFrameIndex: number
}

export type CanvasNodeDataMap = {
  text: TextNodeData
  image: ImageNodeData
  imageGen: ImageGenNodeData
  videoGen: VideoGenNodeData
  frameExtract: FrameExtractNodeData
}

export interface CanvasNodeBase<T extends CanvasNodeType = CanvasNodeType> {
  id: string
  type: T
  title: string
  x: number
  y: number
  width: number
  status: NodeRunStatus
  error?: string
  elapsedMs?: number
  data: CanvasNodeDataMap[T]
}

export type CanvasNode =
  | CanvasNodeBase<'text'>
  | CanvasNodeBase<'image'>
  | CanvasNodeBase<'imageGen'>
  | CanvasNodeBase<'videoGen'>
  | CanvasNodeBase<'frameExtract'>

export interface CanvasEdge {
  id: string
  source: string
  sourcePort: PortKind
  target: string
  targetPort: PortKind
}

export interface CanvasViewport {
  x: number
  y: number
  zoom: number
}

export interface StoryboardShot {
  id: string
  index: number
  scene: string
  title: string
  visualPrompt: string
  cameraMotion: string
  dialogue: string
  durationSec: number
  imageDataUrl: string
  videoUrl: string
  canvasImageNodeId?: string
  canvasVideoNodeId?: string
}

export type AssetCategory = 'character' | 'scene' | 'prop' | 'generated' | 'video'

export interface CreativeAsset {
  id: string
  name: string
  category: AssetCategory
  kind: 'image' | 'video'
  mimeType: string
  dataUrl: string
  prompt?: string
  width?: number
  height?: number
  createdAt: string
}

export interface CreativeProject {
  id: string
  name: string
  description: string
  createdAt: string
  updatedAt: string
  viewport: CanvasViewport
  nodes: CanvasNode[]
  edges: CanvasEdge[]
  scriptSynopsis: string
  shots: StoryboardShot[]
}

export interface CreativeKeyBindings {
  imageKeyId: number | null
  textKeyId: number | null
  videoKeyId: number | null
}

export interface ToonflowExportBundle {
  toonflowCanvas: true
  format: 'sub2api-creative-plaza-v1'
  exportedAt: string
  project: CreativeProject
  assets: CreativeAsset[]
}

export interface QuickImageBridgePayload {
  prompt: string
  model?: string
  size?: string
  imageDataUrl?: string
  title?: string
}

export type WorkflowTemplateId = 'short-drama' | 'character-sheet' | 'blank'

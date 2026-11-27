/**
 * Pure DAG workflow graph engine for the browser-native Toonflow canvas.
 *
 * Handles port validation, cycle detection, topological execution ordering,
 * upstream value resolution, SVG Bezier edge geometry, grid snapping, automatic
 * layered layout, and starter workflow templates. Zero DOM or network imports.
 */
import type {
  CanvasEdge,
  CanvasNode,
  CanvasNodeBase,
  CanvasNodeDataMap,
  CanvasNodeType,
  CreativeProject,
  PortKind,
  StoryboardShot,
  WorkflowTemplateId,
} from './types'

export const CANVAS_GRID_SIZE = 16
export const DEFAULT_NODE_WIDTH = 300
export const DEFAULT_NODE_HEIGHT_ESTIMATE = 230

export interface NodePortSpec {
  inputs: readonly PortKind[]
  outputs: readonly PortKind[]
}

export const NODE_PORTS: Record<CanvasNodeType, NodePortSpec> = {
  text: { inputs: ['text'], outputs: ['text'] },
  image: { inputs: [], outputs: ['image'] },
  imageGen: { inputs: ['text', 'image'], outputs: ['image'] },
  videoGen: { inputs: ['text', 'image'], outputs: ['video'] },
  frameExtract: { inputs: ['video'], outputs: ['image'] },
}

export function snapToGrid(value: number, grid = CANVAS_GRID_SIZE): number {
  if (!Number.isFinite(value)) return 0
  return Math.round(value / grid) * grid
}

export function createNodeId(prefix = 'node'): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${prefix}-${crypto.randomUUID().slice(0, 8)}`
  }
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
}

export function createDefaultNodeData<T extends CanvasNodeType>(type: T): CanvasNodeDataMap[T] {
  const defaults: CanvasNodeDataMap = {
    text: {
      text: '',
      systemPrompt: '',
      model: '',
      mode: 'note',
    },
    image: {
      src: '',
      name: '',
    },
    imageGen: {
      prompt: '',
      model: 'gpt-image-1',
      size: '1024x1024',
      quality: 'auto',
      format: 'png',
      style: '',
      count: 1,
      outputImages: [],
      selectedOutputIndex: 0,
    },
    videoGen: {
      prompt: '',
      model: 'grok-imagine-video',
      protocol: 'auto',
      duration: 5,
      aspectRatio: '16:9',
      resolution: '720p',
      taskId: '',
      taskStatus: '',
      outputVideoUrl: '',
    },
    frameExtract: {
      sourceVideoUrl: '',
      sourceName: '',
      frameCount: 4,
      timestampSec: 0,
      durationSec: 0,
      frames: [],
      selectedFrameIndex: 0,
    },
  }
  return structuredClone(defaults[type])
}

export function createCanvasNode<T extends CanvasNodeType>(
  type: T,
  overrides: {
    id?: string
    title?: string
    x?: number
    y?: number
    width?: number
    data?: Partial<CanvasNodeDataMap[T]>
  } = {},
): CanvasNodeBase<T> {
  const defaultTitles: Record<CanvasNodeType, string> = {
    text: '剧本 / 提示词',
    image: '参考图素材',
    imageGen: 'AI 生图节点',
    videoGen: 'AI 视频节点',
    frameExtract: '逐帧抽帧（本机）',
  }
  const baseData = createDefaultNodeData(type)
  return {
    id: overrides.id ?? createNodeId(type),
    type,
    title: overrides.title ?? defaultTitles[type],
    x: snapToGrid(overrides.x ?? 80),
    y: snapToGrid(overrides.y ?? 80),
    width: overrides.width ?? DEFAULT_NODE_WIDTH,
    status: 'idle',
    data: { ...baseData, ...(overrides.data ?? {}) },
  }
}

/**
 * Checks whether adding an edge `source -> target` is valid:
 * - both nodes exist and are distinct
 * - ports match and are supported by the respective node types
 * - the exact edge does not already exist
 * - adding it does not create a cycle in the DAG
 */
export function canConnect(
  nodes: readonly CanvasNode[],
  edges: readonly CanvasEdge[],
  sourceId: string,
  sourcePort: PortKind,
  targetId: string,
  targetPort: PortKind,
): { ok: boolean; reason?: string } {
  if (!sourceId || !targetId || sourceId === targetId) {
    return { ok: false, reason: 'cannot_self_connect' }
  }
  if (sourcePort !== targetPort) {
    return { ok: false, reason: 'port_type_mismatch' }
  }
  const sourceNode = nodes.find((n) => n.id === sourceId)
  const targetNode = nodes.find((n) => n.id === targetId)
  if (!sourceNode || !targetNode) {
    return { ok: false, reason: 'node_not_found' }
  }
  if (!NODE_PORTS[sourceNode.type].outputs.includes(sourcePort)) {
    return { ok: false, reason: 'unsupported_source_port' }
  }
  if (!NODE_PORTS[targetNode.type].inputs.includes(targetPort)) {
    return { ok: false, reason: 'unsupported_target_port' }
  }
  const duplicate = edges.some(
    (e) =>
      e.source === sourceId &&
      e.sourcePort === sourcePort &&
      e.target === targetId &&
      e.targetPort === targetPort,
  )
  if (duplicate) {
    return { ok: false, reason: 'duplicate_edge' }
  }

  // Cycle check: if `sourceId` is already reachable from `targetId`, adding
  // `sourceId -> targetId` would form a cycle.
  const adjacency = new Map<string, string[]>()
  for (const edge of edges) {
    const list = adjacency.get(edge.source) ?? []
    list.push(edge.target)
    adjacency.set(edge.source, list)
  }
  const stack = [targetId]
  const visited = new Set<string>()
  while (stack.length > 0) {
    const current = stack.pop()!
    if (current === sourceId) {
      return { ok: false, reason: 'cycle_detected' }
    }
    if (visited.has(current)) continue
    visited.add(current)
    for (const next of adjacency.get(current) ?? []) {
      stack.push(next)
    }
  }

  return { ok: true }
}

/**
 * Returns nodes ordered topologically (upstream before downstream).
 * When `targetNodeIds` is provided, only the upstream closure of those nodes
 * (plus the target nodes themselves) is included.
 */
export function topologicalSort(
  nodes: readonly CanvasNode[],
  edges: readonly CanvasEdge[],
  targetNodeIds?: readonly string[],
): CanvasNode[] {
  const nodeMap = new Map(nodes.map((node) => [node.id, node]))
  let included: Set<string>

  if (targetNodeIds && targetNodeIds.length > 0) {
    included = new Set<string>()
    const incoming = new Map<string, string[]>()
    for (const edge of edges) {
      if (!nodeMap.has(edge.source) || !nodeMap.has(edge.target)) continue
      const list = incoming.get(edge.target) ?? []
      list.push(edge.source)
      incoming.set(edge.target, list)
    }
    const queue = [...targetNodeIds.filter((id) => nodeMap.has(id))]
    while (queue.length > 0) {
      const current = queue.pop()!
      if (included.has(current)) continue
      included.add(current)
      for (const parent of incoming.get(current) ?? []) {
        if (!included.has(parent)) queue.push(parent)
      }
    }
  } else {
    included = new Set(nodes.map((n) => n.id))
  }

  const inDegree = new Map<string, number>()
  const outgoing = new Map<string, string[]>()
  for (const id of included) {
    inDegree.set(id, 0)
    outgoing.set(id, [])
  }

  for (const edge of edges) {
    if (!included.has(edge.source) || !included.has(edge.target)) continue
    outgoing.get(edge.source)!.push(edge.target)
    inDegree.set(edge.target, (inDegree.get(edge.target) ?? 0) + 1)
  }

  // Deterministic tie-breaking: left-to-right, then top-to-bottom.
  const ready = [...included]
    .filter((id) => (inDegree.get(id) ?? 0) === 0)
    .sort((a, b) => {
      const na = nodeMap.get(a)!
      const nb = nodeMap.get(b)!
      return na.x - nb.x || na.y - nb.y
    })

  const ordered: CanvasNode[] = []
  while (ready.length > 0) {
    const id = ready.shift()!
    ordered.push(nodeMap.get(id)!)
    for (const next of outgoing.get(id) ?? []) {
      const deg = (inDegree.get(next) ?? 1) - 1
      inDegree.set(next, deg)
      if (deg === 0) {
        ready.push(next)
        ready.sort((a, b) => {
          const na = nodeMap.get(a)!
          const nb = nodeMap.get(b)!
          return na.x - nb.x || na.y - nb.y
        })
      }
    }
  }

  // Append any remaining nodes gracefully if corrupted data had a cycle.
  if (ordered.length < included.size) {
    const seen = new Set(ordered.map((n) => n.id))
    for (const id of included) {
      if (!seen.has(id)) ordered.push(nodeMap.get(id)!)
    }
  }

  return ordered
}

export interface UpstreamInputs {
  texts: string[]
  images: string[]
  videos: string[]
}

/** Extracts primary output value(s) from a single node by port kind. */
export function getNodeOutput(node: CanvasNode, port: PortKind): string[] {
  if (port === 'text' && node.type === 'text') {
    const text = node.data.text.trim()
    return text ? [text] : []
  }
  if (port === 'image') {
    if (node.type === 'image') {
      return node.data.src ? [node.data.src] : []
    }
    if (node.type === 'imageGen') {
      const selected = node.data.outputImages[node.data.selectedOutputIndex] ?? node.data.outputImages[0]
      return selected ? [selected] : []
    }
    if (node.type === 'frameExtract') {
      const frame = node.data.frames[node.data.selectedFrameIndex] ?? node.data.frames[0]
      return frame?.dataUrl ? [frame.dataUrl] : []
    }
  }
  if (port === 'video' && node.type === 'videoGen') {
    return node.data.outputVideoUrl ? [node.data.outputVideoUrl] : []
  }
  return []
}

/** Collects all connected upstream values feeding into `nodeId`. */
export function collectUpstreamInputs(
  nodeId: string,
  nodes: readonly CanvasNode[],
  edges: readonly CanvasEdge[],
): UpstreamInputs {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]))
  const texts: string[] = []
  const images: string[] = []
  const videos: string[] = []

  const incoming = edges
    .filter((edge) => edge.target === nodeId)
    .sort((a, b) => {
      const na = nodeMap.get(a.source)
      const nb = nodeMap.get(b.source)
      if (!na || !nb) return 0
      return na.y - nb.y || na.x - nb.x
    })

  for (const edge of incoming) {
    const sourceNode = nodeMap.get(edge.source)
    if (!sourceNode) continue
    const values = getNodeOutput(sourceNode, edge.sourcePort)
    if (edge.targetPort === 'text') texts.push(...values)
    else if (edge.targetPort === 'image') images.push(...values)
    else if (edge.targetPort === 'video') videos.push(...values)
  }

  return { texts, images, videos }
}

/**
 * Computes layered left-to-right auto-arrangement coordinates for all nodes,
 * grouping nodes by their longest upstream path depth.
 */
export function autoArrangeNodes(
  nodes: readonly CanvasNode[],
  edges: readonly CanvasEdge[],
  options: { startX?: number; startY?: number; columnGap?: number; rowGap?: number } = {},
): Map<string, { x: number; y: number }> {
  const startX = options.startX ?? 64
  const startY = options.startY ?? 64
  const columnGap = options.columnGap ?? 96
  const rowGap = options.rowGap ?? 56

  const ordered = topologicalSort(nodes, edges)
  const depthMap = new Map<string, number>()

  for (const node of ordered) {
    let maxParentDepth = -1
    for (const edge of edges) {
      if (edge.target === node.id) {
        const parentDepth = depthMap.get(edge.source) ?? 0
        if (parentDepth > maxParentDepth) maxParentDepth = parentDepth
      }
    }
    depthMap.set(node.id, maxParentDepth + 1)
  }

  const columns = new Map<number, CanvasNode[]>()
  for (const node of ordered) {
    const col = depthMap.get(node.id) ?? 0
    const bucket = columns.get(col) ?? []
    bucket.push(node)
    columns.set(col, bucket)
  }

  const positions = new Map<string, { x: number; y: number }>()
  const sortedCols = [...columns.keys()].sort((a, b) => a - b)
  let currentX = startX

  for (const col of sortedCols) {
    const columnNodes = columns.get(col) ?? []
    let maxColWidth = DEFAULT_NODE_WIDTH
    let currentY = startY
    for (const node of columnNodes) {
      maxColWidth = Math.max(maxColWidth, node.width || DEFAULT_NODE_WIDTH)
      positions.set(node.id, {
        x: snapToGrid(currentX),
        y: snapToGrid(currentY),
      })
      currentY += DEFAULT_NODE_HEIGHT_ESTIMATE + rowGap
    }
    currentX += maxColWidth + columnGap
  }

  return positions
}

/** Computes approximate canvas coordinates of an input/output port anchor on a node card. */
export function getPortAnchor(
  node: CanvasNode,
  side: 'input' | 'output',
  port: PortKind,
): { x: number; y: number } {
  const ports = side === 'input' ? NODE_PORTS[node.type].inputs : NODE_PORTS[node.type].outputs
  const index = Math.max(0, ports.indexOf(port))
  const width = node.width || DEFAULT_NODE_WIDTH
  const baseTop = 58
  const step = 30
  return {
    x: side === 'input' ? node.x : node.x + width,
    y: node.y + baseTop + index * step,
  }
}

export interface EdgeGeometry {
  path: string
  midX: number
  midY: number
  startX: number
  startY: number
  endX: number
  endY: number
}

/** Builds a smooth horizontal cubic Bezier path between two points. */
export function buildBezierGeometry(
  startX: number,
  startY: number,
  endX: number,
  endY: number,
): EdgeGeometry {
  const dx = Math.max(64, Math.abs(endX - startX) * 0.45)
  const c1x = startX + dx
  const c1y = startY
  const c2x = endX - dx
  const c2y = endY
  const path = `M ${startX} ${startY} C ${c1x} ${c1y}, ${c2x} ${c2y}, ${endX} ${endY}`
  // Evaluate Bezier midpoint at t = 0.5
  const midX = 0.125 * startX + 0.375 * c1x + 0.375 * c2x + 0.125 * endX
  const midY = 0.125 * startY + 0.375 * c1y + 0.375 * c2y + 0.125 * endY
  return { path, midX, midY, startX, startY, endX, endY }
}

/** Converts storyboard shots into neatly arranged connected canvas node chains. */
export function buildStoryboardCanvasGraph(
  shots: readonly StoryboardShot[],
  options: { imageModel?: string; videoModel?: string } = {},
): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  const nodes: CanvasNode[] = []
  const edges: CanvasEdge[] = []
  const imageModel = options.imageModel || 'gpt-image-1'
  const videoModel = options.videoModel || 'grok-imagine-video'

  shots.forEach((shot, rowIndex) => {
    const y = snapToGrid(64 + rowIndex * 304)
    const textNode = createCanvasNode('text', {
      title: `分镜 #${shot.index} · ${shot.title || shot.scene || '画面描述'}`,
      x: 64,
      y,
      data: {
        text: [shot.visualPrompt, shot.cameraMotion ? `运镜：${shot.cameraMotion}` : '']
          .filter(Boolean)
          .join('\n'),
        mode: 'note',
      },
    })
    const imageNode = createCanvasNode('imageGen', {
      title: `分镜 #${shot.index} 关键帧`,
      x: 464,
      y,
      data: {
        prompt: '',
        model: imageModel,
        size: '1536x1024',
        outputImages: shot.imageDataUrl ? [shot.imageDataUrl] : [],
      },
    })
    const videoNode = createCanvasNode('videoGen', {
      title: `分镜 #${shot.index} 动态视频`,
      x: 864,
      y,
      data: {
        prompt: shot.cameraMotion || '',
        model: videoModel,
        duration: Math.max(2, Math.min(15, Math.round(shot.durationSec || 5))),
        outputVideoUrl: shot.videoUrl || '',
      },
    })

    nodes.push(textNode, imageNode, videoNode)
    edges.push(
      {
        id: createNodeId('edge'),
        source: textNode.id,
        sourcePort: 'text',
        target: imageNode.id,
        targetPort: 'text',
      },
      {
        id: createNodeId('edge'),
        source: textNode.id,
        sourcePort: 'text',
        target: videoNode.id,
        targetPort: 'text',
      },
      {
        id: createNodeId('edge'),
        source: imageNode.id,
        sourcePort: 'image',
        target: videoNode.id,
        targetPort: 'image',
      },
    )
  })

  return { nodes, edges }
}

/** Builds a ready-to-run Toonflow-inspired starter project. */
export function createProjectFromTemplate(
  templateId: WorkflowTemplateId,
  customName?: string,
): CreativeProject {
  const now = new Date().toISOString()
  const id = createNodeId('proj')

  if (templateId === 'blank') {
    return {
      id,
      name: customName?.trim() || '空白创意画布',
      description: '从零搭建无限画布工作流（纯浏览器本机存储与运算）',
      createdAt: now,
      updatedAt: now,
      viewport: { x: 0, y: 0, zoom: 0.95 },
      nodes: [],
      edges: [],
      scriptSynopsis: '',
      shots: [],
    }
  }

  if (templateId === 'character-sheet') {
    const charPrompt = createCanvasNode('text', {
      id: createNodeId('text'),
      title: '角色设定提示词',
      x: 64,
      y: 64,
      data: {
        mode: 'ai-expand',
        text: '国风赛博漫剧女主角三视图：正面、侧面、背面全身设定，银白短发，青瓷色机能风长外套，干净浅灰背景，动漫厚涂电影质感。',
      },
    })
    const charSheet = createCanvasNode('imageGen', {
      id: createNodeId('imgGen'),
      title: '角色三视图生成',
      x: 448,
      y: 64,
      data: {
        model: 'gpt-image-1',
        size: '1536x1024',
        style: 'anime style, vibrant colors',
      },
    })
    const scenePrompt = createCanvasNode('text', {
      id: createNodeId('text'),
      title: '分镜场景指令',
      x: 448,
      y: 352,
      data: {
        mode: 'note',
        text: '保持角色外观一致，雨夜霓虹古巷屋檐下回眸特写，电影宽银幕构图，冷暖光影对比。',
      },
    })
    const finalScene = createCanvasNode('imageGen', {
      id: createNodeId('imgGen'),
      title: '角色入景成图',
      x: 848,
      y: 192,
      data: {
        model: 'gpt-image-1',
        size: '1536x1024',
      },
    })

    return {
      id,
      name: customName?.trim() || '角色三视图 → 场景融合',
      description: '先生成角色资产三视图，再作为参考图连线到场景生图保持角色一致性',
      createdAt: now,
      updatedAt: now,
      viewport: { x: 0, y: 0, zoom: 0.9 },
      nodes: [charPrompt, charSheet, scenePrompt, finalScene],
      edges: [
        {
          id: createNodeId('edge'),
          source: charPrompt.id,
          sourcePort: 'text',
          target: charSheet.id,
          targetPort: 'text',
        },
        {
          id: createNodeId('edge'),
          source: charSheet.id,
          sourcePort: 'image',
          target: finalScene.id,
          targetPort: 'image',
        },
        {
          id: createNodeId('edge'),
          source: scenePrompt.id,
          sourcePort: 'text',
          target: finalScene.id,
          targetPort: 'text',
        },
      ],
      scriptSynopsis: '国风赛博短剧主角定妆与雨夜古巷登场镜头。',
      shots: [
        {
          id: createNodeId('shot'),
          index: 1,
          scene: '角色定妆',
          title: '女主三视图设定',
          visualPrompt: charPrompt.data.text,
          cameraMotion: '静态三视图设定稿',
          dialogue: '',
          durationSec: 4,
          imageDataUrl: '',
          videoUrl: '',
        },
        {
          id: createNodeId('shot'),
          index: 2,
          scene: '雨夜古巷',
          title: '屋檐回眸登场',
          visualPrompt: scenePrompt.data.text,
          cameraMotion: '缓推中景至面部特写',
          dialogue: '“这场雨，比约定来得早了十分钟。”',
          durationSec: 5,
          imageDataUrl: '',
          videoUrl: '',
        },
      ],
    }
  }

  // Default: 'short-drama' — Toonflow flagship Script -> Keyframe -> Video -> Frame Extract pipeline
  const scriptNode = createCanvasNode('text', {
    id: createNodeId('text'),
    title: '分镜剧本 / AI 扩写',
    x: 32,
    y: 48,
    data: {
      mode: 'ai-expand',
      text: '漫剧开场镜头：清晨薄雾笼罩的空中浮岛车站，少年背着画板站在复古蒸汽电车前，金色逆光穿透云海，吉卜力与新海诚融合电影质感，16:9 宽银幕。',
    },
  })
  const imageNode = createCanvasNode('imageGen', {
    id: createNodeId('imgGen'),
    title: '关键帧生图（首帧）',
    x: 384,
    y: 48,
    data: {
      model: 'gpt-image-1',
      size: '1536x1024',
      style: 'anime style, vibrant colors',
    },
  })
  const videoNode = createCanvasNode('videoGen', {
    id: createNodeId('vidGen'),
    title: '图生视频（分镜动态化）',
    x: 736,
    y: 48,
    data: {
      prompt: '镜头缓慢向前推进，云海流动、电车冒出柔和白雾，少年衣角随微风轻摆',
      model: 'grok-imagine-video',
      protocol: 'auto',
      duration: 5,
      aspectRatio: '16:9',
      resolution: '720p',
    },
  })
  const frameNode = createCanvasNode('frameExtract', {
    id: createNodeId('frame'),
    title: '浏览器逐帧拉片（本机运算）',
    x: 1088,
    y: 48,
    data: {
      frameCount: 4,
    },
  })

  return {
    id,
    name: customName?.trim() || '短剧分镜工作流（Toonflow 示例）',
    description: '剧本扩写 → 关键帧生图 → 图生视频 → 本机逐帧拉片，全程浏览器本地运行与存储',
    createdAt: now,
    updatedAt: now,
    viewport: { x: 0, y: 0, zoom: 0.78 },
    nodes: [scriptNode, imageNode, videoNode, frameNode],
    edges: [
      {
        id: createNodeId('edge'),
        source: scriptNode.id,
        sourcePort: 'text',
        target: imageNode.id,
        targetPort: 'text',
      },
      {
        id: createNodeId('edge'),
        source: imageNode.id,
        sourcePort: 'image',
        target: videoNode.id,
        targetPort: 'image',
      },
      {
        id: createNodeId('edge'),
        source: scriptNode.id,
        sourcePort: 'text',
        target: videoNode.id,
        targetPort: 'text',
      },
      {
        id: createNodeId('edge'),
        source: videoNode.id,
        sourcePort: 'video',
        target: frameNode.id,
        targetPort: 'video',
      },
    ],
    scriptSynopsis: '少年登上穿越云海的空中电车，寻找传说中能把画作变成现实的浮空画室。',
    shots: [
      {
        id: createNodeId('shot'),
        index: 1,
        scene: '浮岛车站',
        title: '清晨逆光启程',
        visualPrompt: scriptNode.data.text,
        cameraMotion: '缓推全景至中景，云海缓慢流动',
        dialogue: '“下一站，云端画室。”',
        durationSec: 5,
        imageDataUrl: '',
        videoUrl: '',
      },
      {
        id: createNodeId('shot'),
        index: 2,
        scene: '电车车厢',
        title: '窗外飞鸟与速写本',
        visualPrompt: '复古木质电车车厢靠窗座位，阳光洒在翻开的速写本上，铅笔线条泛起微光，窗外是云海和白鸟，动漫电影质感。',
        cameraMotion: '俯拍特写缓慢横移向车窗外',
        dialogue: '',
        durationSec: 4,
        imageDataUrl: '',
        videoUrl: '',
      },
    ],
  }
}

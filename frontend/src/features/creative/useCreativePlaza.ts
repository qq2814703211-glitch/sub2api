/**
 * Reactive controller for the browser-native Creative Plaza (创意广场).
 *
 * Unites Toonflow-inspired multi-project workspaces, infinite DAG canvas with
 * undo/redo, client-side video frame extraction, AI storyboard director, and
 * IndexedDB local asset vault — all running strictly in the user's browser and
 * calling the Sub2API gateway directly with the user's selected API keys.
 */
import { computed, ref, watch } from 'vue'
import type { ApiKey } from '@/types'
import { listOwnKeys, resolveKeyContext } from '@/features/imagegen/rewriteCatalog'
import {
  fetchKeyModels,
  generateStoryboardWithAI,
  requestCanvasImage,
  requestCreativeText,
  requestCreativeVideo,
} from './api'
import {
  autoArrangeNodes,
  buildStoryboardCanvasGraph,
  canConnect,
  collectUpstreamInputs,
  createCanvasNode,
  createNodeId,
  createProjectFromTemplate,
  snapToGrid,
  topologicalSort,
} from './graph'
import {
  downloadJsonFile,
  downloadTextFile,
  extractVideoFramesInBrowser,
  optimizeLocalImageDataUrl,
  readFileAsDataUrl,
} from './mediaBrowser'
import {
  buildProjectExportBundle,
  clearAllLocalAssets,
  deleteLocalAsset,
  deleteLocalProject,
  estimateLocalUsageBytes,
  formatBytes,
  listLocalAssets,
  listLocalProjects,
  loadActiveProjectId,
  loadKeyBindings,
  parseProjectImportBundle,
  saveActiveProjectId,
  saveKeyBindings,
  saveLocalAsset,
  saveLocalProject,
} from './storage'
import type {
  AssetCategory,
  CanvasEdge,
  CanvasNode,
  CanvasNodeDataMap,
  CanvasNodeType,
  CanvasViewport,
  CreativeAsset,
  CreativeKeyBindings,
  CreativeProject,
  ExtractedFrame,
  PortKind,
  StoryboardShot,
  WorkflowTemplateId,
} from './types'

type ToastKind = 'success' | 'error' | 'info'

interface GraphSnapshot {
  nodes: CanvasNode[]
  edges: CanvasEdge[]
}

const MAX_UNDO_STACK = 40

export function useCreativePlaza(notify: (message: string, kind?: ToastKind) => void) {
  const initialized = ref(false)
  const projects = ref<CreativeProject[]>([])
  const activeProjectId = ref<string>('')
  const assets = ref<CreativeAsset[]>([])
  const localUsedBytes = ref(0)
  const localQuotaBytes = ref<number | null>(null)

  // Station API key selection & discovered models per modality
  const ownKeys = ref<ApiKey[]>([])
  const keyBindings = ref<CreativeKeyBindings>(loadKeyBindings())
  const textModels = ref<string[]>([])
  const imageModels = ref<string[]>([])
  const videoModels = ref<string[]>([])
  const modelsLoading = ref(false)

  // Undo / redo history per active project session
  const undoStack = ref<GraphSnapshot[]>([])
  const redoStack = ref<GraphSnapshot[]>([])

  // Execution state
  const running = ref(false)
  const runningNodeIds = ref<string[]>([])
  const generatingShots = ref(false)
  let activeAbortController: AbortController | null = null
  let persistTimer: ReturnType<typeof setTimeout> | null = null

  const activeProject = computed<CreativeProject | null>(
    () => projects.value.find((p) => p.id === activeProjectId.value) ?? projects.value[0] ?? null,
  )

  const canUndo = computed(() => undoStack.value.length > 0)
  const canRedo = computed(() => redoStack.value.length > 0)

  const localUsageSummary = computed(() => {
    const used = formatBytes(localUsedBytes.value)
    if (localQuotaBytes.value && localQuotaBytes.value > 0) {
      return `${used} / ${formatBytes(localQuotaBytes.value)}`
    }
    return used
  })

  function schedulePersistActiveProject(): void {
    const current = activeProject.value
    if (!current) return
    current.updatedAt = new Date().toISOString()
    if (persistTimer) clearTimeout(persistTimer)
    persistTimer = setTimeout(() => {
      if (activeProject.value) {
        void saveLocalProject(activeProject.value).then(() => refreshLocalUsage())
      }
    }, 220)
  }

  async function persistNow(): Promise<void> {
    if (persistTimer) {
      clearTimeout(persistTimer)
      persistTimer = null
    }
    if (activeProject.value) {
      activeProject.value.updatedAt = new Date().toISOString()
      await saveLocalProject(activeProject.value)
    }
    await refreshLocalUsage()
  }

  async function refreshLocalUsage(): Promise<void> {
    const estimate = await estimateLocalUsageBytes(projects.value, assets.value)
    localUsedBytes.value = estimate.usedBytes
    localQuotaBytes.value = estimate.quotaBytes
  }

  function pushUndoSnapshot(): void {
    const proj = activeProject.value
    if (!proj) return
    const snapshot: GraphSnapshot = {
      nodes: JSON.parse(JSON.stringify(proj.nodes)) as CanvasNode[],
      edges: JSON.parse(JSON.stringify(proj.edges)) as CanvasEdge[],
    }
    undoStack.value.push(snapshot)
    if (undoStack.value.length > MAX_UNDO_STACK) undoStack.value.shift()
    redoStack.value = []
  }

  function undo(): void {
    const proj = activeProject.value
    if (!proj || undoStack.value.length === 0) return
    redoStack.value.push({
      nodes: JSON.parse(JSON.stringify(proj.nodes)) as CanvasNode[],
      edges: JSON.parse(JSON.stringify(proj.edges)) as CanvasEdge[],
    })
    const prev = undoStack.value.pop()!
    proj.nodes = prev.nodes
    proj.edges = prev.edges
    schedulePersistActiveProject()
  }

  function redo(): void {
    const proj = activeProject.value
    if (!proj || redoStack.value.length === 0) return
    undoStack.value.push({
      nodes: JSON.parse(JSON.stringify(proj.nodes)) as CanvasNode[],
      edges: JSON.parse(JSON.stringify(proj.edges)) as CanvasEdge[],
    })
    const next = redoStack.value.pop()!
    proj.nodes = next.nodes
    proj.edges = next.edges
    schedulePersistActiveProject()
  }

  // ==================== Initialization & Keys ====================

  async function initialize(): Promise<void> {
    if (initialized.value) return
    const [loadedProjects, loadedAssets] = await Promise.all([
      listLocalProjects(),
      listLocalAssets(),
    ])

    if (loadedProjects.length > 0) {
      projects.value = loadedProjects
    } else {
      const starter = createProjectFromTemplate('short-drama')
      projects.value = [starter]
      await saveLocalProject(starter)
    }

    const savedId = loadActiveProjectId()
    const matched = projects.value.find((p) => p.id === savedId)
    activeProjectId.value = matched ? matched.id : projects.value[0].id
    saveActiveProjectId(activeProjectId.value)

    assets.value = loadedAssets
    initialized.value = true
    await refreshLocalUsage()
    await loadOwnKeys()
  }

  async function loadOwnKeys(): Promise<void> {
    const keys = await listOwnKeys()
    ownKeys.value = keys
    // Drop stale key bindings that no longer belong to the current user.
    const validIds = new Set(keys.map((k) => k.id))
    let changed = false
    const next = { ...keyBindings.value }
    if (next.imageKeyId !== null && !validIds.has(next.imageKeyId)) {
      next.imageKeyId = null
      changed = true
    }
    if (next.textKeyId !== null && !validIds.has(next.textKeyId)) {
      next.textKeyId = null
      changed = true
    }
    if (next.videoKeyId !== null && !validIds.has(next.videoKeyId)) {
      next.videoKeyId = null
      changed = true
    }
    // Seamlessly inherit previously configured ImageGen keys when Creative Plaza bindings are unset.
    try {
      if (next.imageKeyId === null) {
        const legacyImageKey = localStorage.getItem('ig_apiKey')?.trim()
        if (legacyImageKey) {
          const matched = keys.find((k) => k.key === legacyImageKey)
          if (matched) {
            next.imageKeyId = matched.id
            changed = true
          }
        }
      }
      if (next.textKeyId === null) {
        const rawRewrite = localStorage.getItem('ig_rewriteConfig')
        if (rawRewrite) {
          const parsed = JSON.parse(rawRewrite) as { keyId?: unknown }
          if (typeof parsed?.keyId === 'number' && validIds.has(parsed.keyId)) {
            next.textKeyId = parsed.keyId
            changed = true
          }
        }
      }
    } catch {
      // Ignore storage read errors in restricted environments.
    }
    if (changed) {
      keyBindings.value = next
      saveKeyBindings(next)
    }
    await reloadModelsForBindings()
  }

  function setKeyBinding(role: keyof CreativeKeyBindings, id: number | null): void {
    keyBindings.value = { ...keyBindings.value, [role]: id }
    saveKeyBindings(keyBindings.value)
    void reloadModelsForBindings()
  }

  function resolveRoleKey(role: 'image' | 'text' | 'video'): { key: string; platform: string } {
    if (role === 'image') {
      return resolveKeyContext(ownKeys.value, keyBindings.value.imageKeyId)
    }
    if (role === 'text') {
      const primary = resolveKeyContext(ownKeys.value, keyBindings.value.textKeyId)
      if (primary.key) return primary
      return resolveKeyContext(ownKeys.value, keyBindings.value.imageKeyId)
    }
    const primary = resolveKeyContext(ownKeys.value, keyBindings.value.videoKeyId)
    if (primary.key) return primary
    return resolveKeyContext(ownKeys.value, keyBindings.value.imageKeyId)
  }

  async function reloadModelsForBindings(): Promise<void> {
    const imgCtx = resolveRoleKey('image')
    const txtCtx = resolveRoleKey('text')
    const vidCtx = resolveRoleKey('video')

    const uniqueKeys = [...new Set([imgCtx.key, txtCtx.key, vidCtx.key].filter(Boolean))]
    if (!uniqueKeys.length) {
      textModels.value = []
      imageModels.value = []
      videoModels.value = []
      return
    }
    modelsLoading.value = true
    try {
      const cache = new Map<string, Awaited<ReturnType<typeof fetchKeyModels>>>()
      await Promise.all(
        uniqueKeys.map(async (k) => {
          cache.set(k, await fetchKeyModels(k))
        }),
      )
      imageModels.value = imgCtx.key ? (cache.get(imgCtx.key)?.image ?? []) : []
      textModels.value = txtCtx.key ? (cache.get(txtCtx.key)?.text ?? []) : []
      videoModels.value = vidCtx.key ? (cache.get(vidCtx.key)?.video ?? []) : []
    } finally {
      modelsLoading.value = false
    }
  }

  // ==================== Project Management ====================

  function selectProject(id: string): void {
    if (!projects.value.some((p) => p.id === id)) return
    activeProjectId.value = id
    saveActiveProjectId(id)
    undoStack.value = []
    redoStack.value = []
  }

  async function createProject(
    templateId: WorkflowTemplateId,
    name?: string,
  ): Promise<CreativeProject> {
    const project = createProjectFromTemplate(templateId, name)
    projects.value = [project, ...projects.value]
    activeProjectId.value = project.id
    saveActiveProjectId(project.id)
    undoStack.value = []
    redoStack.value = []
    await saveLocalProject(project)
    await refreshLocalUsage()
    notify(`已创建本地工程「${project.name}」`, 'success')
    return project
  }

  function renameActiveProject(newName: string): void {
    const proj = activeProject.value
    const trimmed = newName.trim()
    if (!proj || !trimmed) return
    proj.name = trimmed
    schedulePersistActiveProject()
  }

  async function duplicateActiveProject(): Promise<void> {
    const proj = activeProject.value
    if (!proj) return
    const now = new Date().toISOString()
    const clone: CreativeProject = {
      ...(JSON.parse(JSON.stringify(proj)) as CreativeProject),
      id: createNodeId('proj'),
      name: `${proj.name} 副本`,
      createdAt: now,
      updatedAt: now,
    }
    projects.value = [clone, ...projects.value]
    activeProjectId.value = clone.id
    saveActiveProjectId(clone.id)
    await saveLocalProject(clone)
    await refreshLocalUsage()
    notify(`已复制工程「${clone.name}」`, 'success')
  }

  async function removeActiveProject(): Promise<void> {
    const proj = activeProject.value
    if (!proj) return
    if (projects.value.length <= 1) {
      const fresh = createProjectFromTemplate('blank')
      await deleteLocalProject(proj.id)
      projects.value = [fresh]
      activeProjectId.value = fresh.id
      saveActiveProjectId(fresh.id)
      await saveLocalProject(fresh)
      await refreshLocalUsage()
      notify('已重置为空白画布工程', 'info')
      return
    }
    await deleteLocalProject(proj.id)
    projects.value = projects.value.filter((p) => p.id !== proj.id)
    activeProjectId.value = projects.value[0].id
    saveActiveProjectId(activeProjectId.value)
    undoStack.value = []
    redoStack.value = []
    await refreshLocalUsage()
    notify(`已删除本地工程「${proj.name}」`, 'info')
  }

  function exportActiveProject(includeAssets = false): void {
    const proj = activeProject.value
    if (!proj) return
    const bundle = buildProjectExportBundle(proj, includeAssets ? assets.value : [])
    const safeName = proj.name.replace(/[\\/:*?"<>|]+/g, '-').slice(0, 48) || 'creative-project'
    downloadJsonFile(bundle, `${safeName}.toonflow.json`)
    notify('已导出 .toonflow.json 工程文件', 'success')
  }

  async function importProjectFile(file: File): Promise<void> {
    const text = await file.text()
    const { project, assets: importedAssets } = parseProjectImportBundle(text)
    projects.value = [project, ...projects.value]
    activeProjectId.value = project.id
    saveActiveProjectId(project.id)
    await saveLocalProject(project)
    if (importedAssets.length > 0) {
      const existingIds = new Set(assets.value.map((a) => a.id))
      for (const asset of importedAssets) {
        if (existingIds.has(asset.id)) continue
        assets.value = [asset, ...assets.value]
        await saveLocalAsset(asset)
      }
    }
    undoStack.value = []
    redoStack.value = []
    await refreshLocalUsage()
    notify(`已导入工程「${project.name}」（${project.nodes.length} 个节点）`, 'success')
  }

  // ==================== Canvas Node & Edge Operations ====================

  function addNode<T extends CanvasNodeType>(
    type: T,
    options: {
      title?: string
      x?: number
      y?: number
      data?: Partial<CanvasNodeDataMap[T]>
    } = {},
  ): CanvasNode | null {
    const proj = activeProject.value
    if (!proj) return null
    pushUndoSnapshot()

    const defaultX =
      options.x ??
      (proj.nodes.length > 0
        ? Math.max(...proj.nodes.map((n) => n.x)) + 80
        : 96)
    const defaultY =
      options.y ??
      (proj.nodes.length > 0
        ? 96 + ((proj.nodes.length % 4) * 64)
        : 96)

    const enrichedData: Partial<CanvasNodeDataMap[T]> = { ...(options.data ?? {}) }
    if (type === 'text' && textModels.value[0]) {
      const textPatch = enrichedData as Partial<CanvasNodeDataMap['text']>
      if (!textPatch.model) textPatch.model = textModels.value[0]
    }
    if (type === 'imageGen' && imageModels.value[0]) {
      const imagePatch = enrichedData as Partial<CanvasNodeDataMap['imageGen']>
      if (!imagePatch.model) imagePatch.model = imageModels.value[0]
    }
    if (type === 'videoGen' && videoModels.value[0]) {
      const videoPatch = enrichedData as Partial<CanvasNodeDataMap['videoGen']>
      if (!videoPatch.model) videoPatch.model = videoModels.value[0]
    }

    const node = createCanvasNode(type, {
      title: options.title,
      x: snapToGrid(defaultX),
      y: snapToGrid(defaultY),
      data: enrichedData,
    }) as CanvasNode
    proj.nodes = [...proj.nodes, node]
    schedulePersistActiveProject()
    return node
  }

  function updateNodeTitle(nodeId: string, title: string): void {
    const proj = activeProject.value
    const node = proj?.nodes.find((n) => n.id === nodeId)
    if (!node) return
    node.title = title
    schedulePersistActiveProject()
  }

  function updateNodeData(nodeId: string, patch: Record<string, unknown>): void {
    const proj = activeProject.value
    const node = proj?.nodes.find((n) => n.id === nodeId)
    if (!node) return
    node.data = { ...(node.data as unknown as Record<string, unknown>), ...patch } as never
    schedulePersistActiveProject()
  }

  function moveNode(nodeId: string, x: number, y: number, snap = true): void {
    const proj = activeProject.value
    const node = proj?.nodes.find((n) => n.id === nodeId)
    if (!node) return
    node.x = snap ? snapToGrid(x) : Math.round(x)
    node.y = snap ? snapToGrid(y) : Math.round(y)
    schedulePersistActiveProject()
  }

  function duplicateNode(nodeId: string): CanvasNode | null {
    const proj = activeProject.value
    const source = proj?.nodes.find((n) => n.id === nodeId)
    if (!proj || !source) return null
    pushUndoSnapshot()
    const clone = JSON.parse(JSON.stringify(source)) as CanvasNode
    clone.id = createNodeId(clone.type)
    clone.title = `${source.title} 副本`
    clone.x = snapToGrid(source.x + 48)
    clone.y = snapToGrid(source.y + 48)
    clone.status = 'idle'
    clone.error = undefined
    proj.nodes = [...proj.nodes, clone]
    schedulePersistActiveProject()
    return clone
  }

  function removeNodes(nodeIds: readonly string[]): void {
    const proj = activeProject.value
    if (!proj || nodeIds.length === 0) return
    const targetSet = new Set(nodeIds)
    pushUndoSnapshot()
    proj.nodes = proj.nodes.filter((n) => !targetSet.has(n.id))
    proj.edges = proj.edges.filter((e) => !targetSet.has(e.source) && !targetSet.has(e.target))
    schedulePersistActiveProject()
  }

  function connectNodes(
    sourceId: string,
    sourcePort: PortKind,
    targetId: string,
    targetPort: PortKind,
  ): boolean {
    const proj = activeProject.value
    if (!proj) return false
    const check = canConnect(proj.nodes, proj.edges, sourceId, sourcePort, targetId, targetPort)
    if (!check.ok) {
      if (check.reason === 'cycle_detected') {
        notify('无法建立循环连线：工作流必须保持有向无环（DAG）', 'error')
      } else if (check.reason === 'port_type_mismatch') {
        notify('端口类型不匹配：仅支持同类型数据流连接（文本→文本、图片→图片、视频→视频）', 'error')
      }
      return false
    }
    pushUndoSnapshot()
    const edge: CanvasEdge = {
      id: createNodeId('edge'),
      source: sourceId,
      sourcePort,
      target: targetId,
      targetPort,
    }
    proj.edges = [...proj.edges, edge]
    schedulePersistActiveProject()
    return true
  }

  function removeEdge(edgeId: string): void {
    const proj = activeProject.value
    if (!proj) return
    pushUndoSnapshot()
    proj.edges = proj.edges.filter((e) => e.id !== edgeId)
    schedulePersistActiveProject()
  }

  function arrangeCanvas(): void {
    const proj = activeProject.value
    if (!proj || proj.nodes.length === 0) return
    pushUndoSnapshot()
    const positions = autoArrangeNodes(proj.nodes, proj.edges)
    proj.nodes = proj.nodes.map((node) => {
      const pos = positions.get(node.id)
      return pos ? { ...node, x: pos.x, y: pos.y } : node
    })
    schedulePersistActiveProject()
    notify('已自动整理画布节点布局', 'info')
  }

  function setViewport(patch: Partial<CanvasViewport>): void {
    const proj = activeProject.value
    if (!proj) return
    proj.viewport = {
      x: patch.x ?? proj.viewport.x,
      y: patch.y ?? proj.viewport.y,
      zoom: Math.max(0.25, Math.min(2.0, patch.zoom ?? proj.viewport.zoom)),
    }
    schedulePersistActiveProject()
  }

  async function handleCanvasFileDrop(files: File[], canvasX: number, canvasY: number): Promise<void> {
    let offset = 0
    for (const file of files) {
      if (file.type.startsWith('image/')) {
        const prepared = await optimizeLocalImageDataUrl(file)
        addNode('image', {
          title: file.name || '本地图片素材',
          x: canvasX + offset * 32,
          y: canvasY + offset * 32,
          data: {
            src: prepared.dataUrl,
            name: file.name,
            width: prepared.width,
            height: prepared.height,
            mimeType: prepared.mimeType,
          },
        })
        offset++
      } else if (file.type.startsWith('video/')) {
        const objectUrl = URL.createObjectURL(file)
        addNode('frameExtract', {
          title: `逐帧拉片 · ${file.name}`,
          x: canvasX + offset * 32,
          y: canvasY + offset * 32,
          data: {
            sourceVideoUrl: objectUrl,
            sourceName: file.name,
            frameCount: 4,
          },
        })
        offset++
      }
    }
    if (offset > 0) {
      notify(`已将 ${offset} 个本地素材添加到画布（纯本机读取，未上传服务器）`, 'success')
    }
  }

  // ==================== DAG Node Execution ====================

  async function executeSingleNode(node: CanvasNode, signal: AbortSignal): Promise<void> {
    const proj = activeProject.value
    if (!proj) return
    const startedAt = Date.now()
    node.status = 'running'
    node.error = undefined
    runningNodeIds.value = [...new Set([...runningNodeIds.value, node.id])]

    try {
      const upstream = collectUpstreamInputs(node.id, proj.nodes, proj.edges)

      if (node.type === 'text') {
        const combinedInput = [...upstream.texts, node.data.text.trim()].filter(Boolean).join('\n\n')
        if (node.data.mode === 'note') {
          if (!combinedInput) throw new Error('文本内容为空')
          if (upstream.texts.length > 0 && !node.data.text.trim()) {
            node.data.text = combinedInput
          }
        } else {
          const textKey = resolveRoleKey('text')
          const model = node.data.model.trim() || textModels.value[0] || ''
          const systemPrompt =
            node.data.systemPrompt.trim() ||
            (node.data.mode === 'ai-storyboard'
              ? '你是专业漫剧分镜编导。将输入的剧情或创意拆解成简洁清晰的镜头画面描述（含角色、场景、光影、运镜），方便直接送入生图模型。'
              : '你是专业的 AI 视觉提示词工程师。请把输入创意润色扩写为画面感极强、可直接生图的高质量提示词，直接输出提示词正文。')
          const result = await requestCreativeText({
            apiKey: textKey.key,
            platform: textKey.platform,
            model,
            systemPrompt,
            userPrompt: combinedInput,
            signal,
          })
          node.data.text = result.text
        }
      } else if (node.type === 'image') {
        if (!node.data.src) throw new Error('请先在图片节点选择或拖入本地图片')
      } else if (node.type === 'imageGen') {
        const imgKey = resolveRoleKey('image')
        const images = await requestCanvasImage({
          apiKey: imgKey.key,
          nodeData: node.data,
          upstreamPrompt: upstream.texts.join('\n'),
          upstreamImages: upstream.images,
          signal,
        })
        node.data.outputImages = images
        node.data.selectedOutputIndex = 0
      } else if (node.type === 'videoGen') {
        const vidKey = resolveRoleKey('video')
        const result = await requestCreativeVideo({
          apiKey: vidKey.key,
          platform: vidKey.platform,
          nodeData: node.data,
          upstreamPrompt: upstream.texts.join('\n'),
          upstreamImages: upstream.images,
          signal,
          onUpdate: ({ taskId, status }) => {
            node.data.taskId = taskId
            node.data.taskStatus = status
          },
        })
        node.data.taskId = result.taskId
        node.data.taskStatus = 'completed'
        node.data.outputVideoUrl = result.videoUrl
      } else if (node.type === 'frameExtract') {
        const videoSrc = upstream.videos[0] || node.data.sourceVideoUrl
        if (!videoSrc) throw new Error('请连接上游视频生成节点，或在节点内选择本地视频文件')
        const extracted = await extractVideoFramesInBrowser(videoSrc, {
          count: node.data.frameCount || 4,
        })
        node.data.sourceVideoUrl = videoSrc
        node.data.durationSec = extracted.durationSec
        node.data.frames = extracted.frames
        node.data.selectedFrameIndex = 0
      }

      node.status = 'success'
      node.elapsedMs = Date.now() - startedAt
      schedulePersistActiveProject()
    } catch (error) {
      node.status = 'error'
      node.elapsedMs = Date.now() - startedAt
      node.error = error instanceof Error ? error.message : '节点执行失败'
      throw error
    } finally {
      runningNodeIds.value = runningNodeIds.value.filter((id) => id !== node.id)
    }
  }

  async function runSingleNode(nodeId: string): Promise<void> {
    const proj = activeProject.value
    const node = proj?.nodes.find((n) => n.id === nodeId)
    if (!node) return
    if (running.value) {
      notify('当前有工作流正在运行，请等待完成或先停止', 'info')
      return
    }
    running.value = true
    activeAbortController = new AbortController()
    try {
      await executeSingleNode(node, activeAbortController.signal)
      notify(`节点「${node.title}」执行完成`, 'success')
    } catch (error) {
      if ((error as Error)?.name === 'AbortError') {
        notify('已取消节点执行', 'info')
      } else {
        notify(error instanceof Error ? error.message : '节点执行失败', 'error')
      }
    } finally {
      running.value = false
      activeAbortController = null
    }
  }

  async function runWorkflow(targetNodeIds?: string[]): Promise<void> {
    const proj = activeProject.value
    if (!proj || proj.nodes.length === 0) {
      notify('当前画布暂无可执行节点', 'info')
      return
    }
    if (running.value) return
    const ordered = topologicalSort(proj.nodes, proj.edges, targetNodeIds)
    running.value = true
    activeAbortController = new AbortController()
    let completed = 0
    try {
      for (const node of ordered) {
        if (activeAbortController.signal.aborted) break
        await executeSingleNode(node, activeAbortController.signal)
        completed++
      }
      notify(`工作流执行完成（共 ${completed} 个节点）`, 'success')
    } catch (error) {
      if ((error as Error)?.name === 'AbortError') {
        notify('已停止工作流运行', 'info')
      } else {
        notify(error instanceof Error ? error.message : '工作流执行中断', 'error')
      }
    } finally {
      running.value = false
      activeAbortController = null
    }
  }

  function stopExecution(): void {
    activeAbortController?.abort()
    running.value = false
    runningNodeIds.value = []
  }

  function spawnImageNodeFromFrame(sourceNodeId: string, frame: ExtractedFrame): CanvasNode | null {
    const proj = activeProject.value
    const source = proj?.nodes.find((n) => n.id === sourceNodeId)
    if (!proj || !source) return null
    const imageNode = addNode('image', {
      title: `抽取帧 ${frame.timeSec}s`,
      x: source.x + (source.width || 300) + 96,
      y: source.y,
      data: {
        src: frame.dataUrl,
        name: `frame-${frame.timeSec}s.webp`,
        width: frame.width,
        height: frame.height,
        mimeType: 'image/webp',
      },
    })
    if (imageNode) {
      notify(`已将 ${frame.timeSec}s 抽帧画面生成独立图片节点`, 'success')
    }
    return imageNode
  }

  // ==================== Storyboard & AI Director ====================

  function updateScriptSynopsis(text: string): void {
    if (!activeProject.value) return
    activeProject.value.scriptSynopsis = text
    schedulePersistActiveProject()
  }

  function addStoryboardShot(partial: Partial<StoryboardShot> = {}): StoryboardShot | null {
    const proj = activeProject.value
    if (!proj) return null
    const shot: StoryboardShot = {
      id: createNodeId('shot'),
      index: proj.shots.length + 1,
      scene: partial.scene ?? `场景 ${proj.shots.length + 1}`,
      title: partial.title ?? `分镜 ${proj.shots.length + 1}`,
      visualPrompt: partial.visualPrompt ?? '',
      cameraMotion: partial.cameraMotion ?? '缓推镜头',
      dialogue: partial.dialogue ?? '',
      durationSec: partial.durationSec ?? 5,
      imageDataUrl: partial.imageDataUrl ?? '',
      videoUrl: partial.videoUrl ?? '',
    }
    proj.shots = [...proj.shots, shot]
    schedulePersistActiveProject()
    return shot
  }

  function updateStoryboardShot(shotId: string, patch: Partial<StoryboardShot>): void {
    const proj = activeProject.value
    const shot = proj?.shots.find((s) => s.id === shotId)
    if (!shot) return
    Object.assign(shot, patch)
    schedulePersistActiveProject()
  }

  function removeStoryboardShot(shotId: string): void {
    const proj = activeProject.value
    if (!proj) return
    proj.shots = proj.shots
      .filter((s) => s.id !== shotId)
      .map((s, idx) => ({ ...s, index: idx + 1 }))
    schedulePersistActiveProject()
  }

  function moveStoryboardShot(fromIndex: number, toIndex: number): void {
    const proj = activeProject.value
    if (!proj) return
    if (toIndex < 0 || toIndex >= proj.shots.length || fromIndex === toIndex) return
    const copy = [...proj.shots]
    const [moved] = copy.splice(fromIndex, 1)
    copy.splice(toIndex, 0, moved)
    proj.shots = copy.map((s, idx) => ({ ...s, index: idx + 1 }))
    schedulePersistActiveProject()
  }

  async function generateStoryboardFromSynopsis(shotCount = 4, explicitModel = ''): Promise<void> {
    const proj = activeProject.value
    if (!proj) return
    const synopsis = proj.scriptSynopsis.trim()
    if (!synopsis) {
      notify('请先输入故事大纲或剧本灵感', 'error')
      return
    }
    const textKey = resolveRoleKey('text')
    const model = explicitModel.trim() || textModels.value[0] || ''
    generatingShots.value = true
    const controller = new AbortController()
    try {
      const generated = await generateStoryboardWithAI({
        apiKey: textKey.key,
        platform: textKey.platform,
        model,
        synopsis,
        shotCount,
        signal: controller.signal,
      })
      proj.shots = generated
      schedulePersistActiveProject()
      notify(`AI 编导已拆解出 ${generated.length} 个分镜`, 'success')
    } catch (error) {
      notify(error instanceof Error ? error.message : 'AI 分镜拆解失败', 'error')
    } finally {
      generatingShots.value = false
    }
  }

  async function generateKeyframeForShot(shotId: string, modelOverride = ''): Promise<void> {
    const proj = activeProject.value
    const shot = proj?.shots.find((s) => s.id === shotId)
    if (!shot) return
    if (!shot.visualPrompt.trim()) {
      notify('该分镜的画面提示词为空', 'error')
      return
    }
    const imgKey = resolveRoleKey('image')
    const controller = new AbortController()
    try {
      const images = await requestCanvasImage({
        apiKey: imgKey.key,
        nodeData: {
          prompt: shot.visualPrompt,
          model: modelOverride.trim() || imageModels.value[0] || 'gpt-image-1',
          size: '1536x1024',
          quality: 'auto',
          format: 'png',
          style: '',
          count: 1,
          outputImages: [],
          selectedOutputIndex: 0,
        },
        upstreamPrompt: '',
        upstreamImages: [],
        signal: controller.signal,
      })
      if (images[0]) {
        shot.imageDataUrl = images[0]
        schedulePersistActiveProject()
        notify(`分镜 #${shot.index} 关键帧已生成`, 'success')
      }
    } catch (error) {
      notify(error instanceof Error ? error.message : '分镜生图失败', 'error')
    }
  }

  function pushStoryboardToCanvas(replaceExisting = false): void {
    const proj = activeProject.value
    if (!proj || proj.shots.length === 0) {
      notify('当前没有可同步的分镜', 'info')
      return
    }
    pushUndoSnapshot()
    const graph = buildStoryboardCanvasGraph(proj.shots, {
      imageModel: imageModels.value[0] || 'gpt-image-1',
      videoModel: videoModels.value[0] || 'grok-imagine-video',
    })
    if (replaceExisting) {
      proj.nodes = graph.nodes
      proj.edges = graph.edges
    } else {
      const offsetY = proj.nodes.length > 0 ? Math.max(...proj.nodes.map((n) => n.y)) + 320 : 0
      proj.nodes = [
        ...proj.nodes,
        ...graph.nodes.map((n) => ({ ...n, y: snapToGrid(n.y + offsetY) })),
      ]
      proj.edges = [...proj.edges, ...graph.edges]
    }
    schedulePersistActiveProject()
    notify(`已将 ${proj.shots.length} 个分镜同步到无限画布（${graph.nodes.length} 个节点）`, 'success')
  }

  function exportStoryboardMarkdown(): void {
    const proj = activeProject.value
    if (!proj) return
    const lines: string[] = [
      `# ${proj.name} — 分镜脚本`,
      '',
      proj.scriptSynopsis ? `> ${proj.scriptSynopsis}` : '',
      '',
    ]
    for (const shot of proj.shots) {
      lines.push(`## 分镜 #${shot.index}｜${shot.scene} · ${shot.title}（${shot.durationSec}s）`)
      lines.push(`- **画面提示词**：${shot.visualPrompt}`)
      if (shot.cameraMotion) lines.push(`- **运镜与动作**：${shot.cameraMotion}`)
      if (shot.dialogue) lines.push(`- **台词 / 旁白**：${shot.dialogue}`)
      lines.push('')
    }
    const safeName = proj.name.replace(/[\\/:*?"<>|]+/g, '-').slice(0, 48) || 'storyboard'
    downloadTextFile(lines.join('\n'), `${safeName}-storyboard.md`)
    notify('已导出分镜脚本 Markdown', 'success')
  }

  // ==================== Local Asset Vault (IndexedDB) ====================

  async function saveMediaToAssets(payload: {
    name: string
    category: AssetCategory
    kind: 'image' | 'video'
    mimeType: string
    dataUrl: string
    prompt?: string
    width?: number
    height?: number
  }): Promise<CreativeAsset> {
    const asset: CreativeAsset = {
      id: createNodeId('asset'),
      name: payload.name.trim() || '创意素材',
      category: payload.category,
      kind: payload.kind,
      mimeType: payload.mimeType,
      dataUrl: payload.dataUrl,
      prompt: payload.prompt,
      width: payload.width,
      height: payload.height,
      createdAt: new Date().toISOString(),
    }
    assets.value = [asset, ...assets.value]
    await saveLocalAsset(asset)
    await refreshLocalUsage()
    notify(`已保存到本机素材库「${asset.name}」`, 'success')
    return asset
  }

  async function uploadAssetFiles(files: File[], category: AssetCategory): Promise<number> {
    let added = 0
    for (const file of files) {
      if (file.type.startsWith('image/')) {
        const prepared = await optimizeLocalImageDataUrl(file)
        await saveMediaToAssets({
          name: file.name,
          category,
          kind: 'image',
          mimeType: prepared.mimeType,
          dataUrl: prepared.dataUrl,
          width: prepared.width,
          height: prepared.height,
        })
        added++
      } else if (file.type.startsWith('video/')) {
        const dataUrl = await readFileAsDataUrl(file)
        await saveMediaToAssets({
          name: file.name,
          category: 'video',
          kind: 'video',
          mimeType: file.type || 'video/mp4',
          dataUrl,
        })
        added++
      }
    }
    return added
  }

  async function removeAsset(assetId: string): Promise<void> {
    assets.value = assets.value.filter((a) => a.id !== assetId)
    await deleteLocalAsset(assetId)
    await refreshLocalUsage()
  }

  async function clearAssets(): Promise<void> {
    assets.value = []
    await clearAllLocalAssets()
    await refreshLocalUsage()
    notify('已清空本机素材库', 'info')
  }

  function insertAssetToCanvas(asset: CreativeAsset): CanvasNode | null {
    if (asset.kind === 'video') {
      const node = addNode('frameExtract', {
        title: `视频抽帧 · ${asset.name}`,
        data: {
          sourceVideoUrl: asset.dataUrl,
          sourceName: asset.name,
          frameCount: 4,
        },
      })
      if (node) notify(`已将视频「${asset.name}」添加到画布`, 'success')
      return node
    }
    const node = addNode('image', {
      title: asset.name,
      data: {
        src: asset.dataUrl,
        name: asset.name,
        width: asset.width,
        height: asset.height,
        mimeType: asset.mimeType,
        assetId: asset.id,
      },
    })
    if (node) notify(`已将素材「${asset.name}」添加到画布`, 'success')
    return node
  }

  // ==================== Bridge with Quick Image Studio ====================

  function sendQuickImageToCanvas(payload: {
    src: string
    prompt?: string
    title?: string
  }): CanvasNode | null {
    const node = addNode('image', {
      title: payload.title || '生图工作台作品',
      data: {
        src: payload.src,
        name: `${payload.title || 'quick-image'}.png`,
        mimeType: 'image/png',
      },
    })
    if (payload.prompt?.trim() && node) {
      const textNode = addNode('text', {
        title: '关联提示词',
        x: Math.max(32, node.x - 360),
        y: node.y,
        data: { text: payload.prompt.trim(), mode: 'note' },
      })
      if (textNode) {
        // Keep prompt nearby so the user can wire it into downstream imageGen/videoGen nodes.
        schedulePersistActiveProject()
      }
    }
    notify('已发送到无限画布，可继续连线图生图或图生视频', 'success')
    return node
  }

  watch(activeProjectId, (id) => {
    if (id) saveActiveProjectId(id)
  })

  return {
    initialized,
    projects,
    activeProjectId,
    activeProject,
    assets,
    localUsageSummary,
    ownKeys,
    keyBindings,
    textModels,
    imageModels,
    videoModels,
    modelsLoading,
    canUndo,
    canRedo,
    running,
    runningNodeIds,
    generatingShots,
    initialize,
    loadOwnKeys,
    setKeyBinding,
    reloadModelsForBindings,
    persistNow,
    selectProject,
    createProject,
    renameActiveProject,
    duplicateActiveProject,
    removeActiveProject,
    exportActiveProject,
    importProjectFile,
    addNode,
    updateNodeTitle,
    updateNodeData,
    moveNode,
    duplicateNode,
    removeNodes,
    connectNodes,
    removeEdge,
    arrangeCanvas,
    setViewport,
    handleCanvasFileDrop,
    undo,
    redo,
    runSingleNode,
    runWorkflow,
    stopExecution,
    spawnImageNodeFromFrame,
    updateScriptSynopsis,
    addStoryboardShot,
    updateStoryboardShot,
    removeStoryboardShot,
    moveStoryboardShot,
    generateStoryboardFromSynopsis,
    generateKeyframeForShot,
    pushStoryboardToCanvas,
    exportStoryboardMarkdown,
    saveMediaToAssets,
    uploadAssetFiles,
    removeAsset,
    clearAssets,
    insertAssetToCanvas,
    sendQuickImageToCanvas,
  }
}

export type CreativePlazaController = ReturnType<typeof useCreativePlaza>

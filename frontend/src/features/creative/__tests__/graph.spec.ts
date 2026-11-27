import { describe, expect, it } from 'vitest'
import {
  CANVAS_GRID_SIZE,
  autoArrangeNodes,
  buildBezierGeometry,
  buildStoryboardCanvasGraph,
  canConnect,
  collectUpstreamInputs,
  createCanvasNode,
  createProjectFromTemplate,
  getNodeOutput,
  snapToGrid,
  topologicalSort,
} from '../graph'
import type { CanvasEdge } from '../types'

describe('creative DAG graph engine', () => {
  it('validates port types, duplicates, and rejects cycles', () => {
    const n1 = createCanvasNode('text', { id: 't1', x: 0, y: 0 })
    const n2 = createCanvasNode('text', { id: 't2', x: 320, y: 0 })
    const imgGen = createCanvasNode('imageGen', { id: 'ig1', x: 640, y: 0 })
    const nodes = [n1, n2, imgGen]
    const edges: CanvasEdge[] = [
      { id: 'e1', source: 't1', sourcePort: 'text', target: 't2', targetPort: 'text' },
    ]

    expect(canConnect(nodes, edges, 't1', 'text', 't1', 'text')).toEqual({
      ok: false,
      reason: 'cannot_self_connect',
    })
    expect(canConnect(nodes, edges, 't1', 'text', 'ig1', 'image')).toEqual({
      ok: false,
      reason: 'port_type_mismatch',
    })
    expect(canConnect(nodes, edges, 't1', 'text', 't2', 'text')).toEqual({
      ok: false,
      reason: 'duplicate_edge',
    })
    // Cycle: t2 -> t1 when t1 -> t2 already exists
    expect(canConnect(nodes, edges, 't2', 'text', 't1', 'text')).toEqual({
      ok: false,
      reason: 'cycle_detected',
    })
    expect(canConnect(nodes, edges, 't2', 'text', 'ig1', 'text')).toEqual({ ok: true })
  })

  it('orders nodes topologically and supports target-scoped upstream closure', () => {
    const script = createCanvasNode('text', { id: 'script', x: 0, y: 0 })
    const refImg = createCanvasNode('image', { id: 'ref', x: 0, y: 200 })
    const genImg = createCanvasNode('imageGen', { id: 'genImg', x: 320, y: 0 })
    const genVid = createCanvasNode('videoGen', { id: 'genVid', x: 640, y: 0 })
    const isolated = createCanvasNode('text', { id: 'isolated', x: 960, y: 300 })

    const nodes = [genVid, isolated, genImg, refImg, script]
    const edges: CanvasEdge[] = [
      { id: 'e1', source: 'script', sourcePort: 'text', target: 'genImg', targetPort: 'text' },
      { id: 'e2', source: 'ref', sourcePort: 'image', target: 'genImg', targetPort: 'image' },
      { id: 'e3', source: 'genImg', sourcePort: 'image', target: 'genVid', targetPort: 'image' },
    ]

    const fullOrder = topologicalSort(nodes, edges).map((n) => n.id)
    expect(fullOrder.indexOf('script')).toBeLessThan(fullOrder.indexOf('genImg'))
    expect(fullOrder.indexOf('ref')).toBeLessThan(fullOrder.indexOf('genImg'))
    expect(fullOrder.indexOf('genImg')).toBeLessThan(fullOrder.indexOf('genVid'))
    expect(fullOrder).toContain('isolated')

    const partialOrder = topologicalSort(nodes, edges, ['genImg']).map((n) => n.id)
    expect(partialOrder).toEqual(['script', 'ref', 'genImg'])
  })

  it('collects selected upstream outputs across text, imageGen, videoGen, and frameExtract nodes', () => {
    const t = createCanvasNode('text', { id: 't1', x: 0, y: 0, data: { text: 'cinematic rain' } })
    const ig = createCanvasNode('imageGen', {
      id: 'ig1',
      x: 0,
      y: 120,
      data: {
        outputImages: ['data:image/png;base64,AAA', 'data:image/png;base64,BBB'],
        selectedOutputIndex: 1,
      },
    })
    const fe = createCanvasNode('frameExtract', {
      id: 'fe1',
      x: 0,
      y: 240,
      data: {
        frames: [
          { id: 'f1', timeSec: 0, dataUrl: 'data:image/webp;base64,F1', width: 640, height: 360 },
          { id: 'f2', timeSec: 2, dataUrl: 'data:image/webp;base64,F2', width: 640, height: 360 },
        ],
        selectedFrameIndex: 1,
      },
    })
    const vid = createCanvasNode('videoGen', { id: 'v1', x: 400, y: 0 })

    expect(getNodeOutput(ig, 'image')).toEqual(['data:image/png;base64,BBB'])
    expect(getNodeOutput(fe, 'image')).toEqual(['data:image/webp;base64,F2'])

    const upstream = collectUpstreamInputs('v1', [t, ig, fe, vid], [
      { id: 'e1', source: 't1', sourcePort: 'text', target: 'v1', targetPort: 'text' },
      { id: 'e2', source: 'ig1', sourcePort: 'image', target: 'v1', targetPort: 'image' },
      { id: 'e3', source: 'fe1', sourcePort: 'image', target: 'v1', targetPort: 'image' },
    ])

    expect(upstream.texts).toEqual(['cinematic rain'])
    expect(upstream.images).toEqual(['data:image/png;base64,BBB', 'data:image/webp;base64,F2'])
  })

  it('auto-arranges layered DAG columns snapped to the canvas grid', () => {
    const project = createProjectFromTemplate('short-drama')
    const layout = autoArrangeNodes(project.nodes, project.edges)

    expect(layout.size).toBe(project.nodes.length)
    for (const pos of layout.values()) {
      expect(pos.x % CANVAS_GRID_SIZE).toBe(0)
      expect(pos.y % CANVAS_GRID_SIZE).toBe(0)
    }

    const xs = project.nodes.map((n) => layout.get(n.id)!.x)
    // script -> image -> video -> frameExtract forms 4 progressive columns
    expect(xs[0]).toBeLessThan(xs[1])
    expect(xs[1]).toBeLessThan(xs[2])
    expect(xs[2]).toBeLessThan(xs[3])
  })

  it('builds valid Bezier geometry, templates, and storyboard canvas graphs', () => {
    expect(snapToGrid(25)).toBe(32)
    const geom = buildBezierGeometry(100, 100, 400, 200)
    expect(geom.path).toContain('M 100 100 C')
    expect(geom.midX).toBe(250)
    expect(geom.midY).toBe(150)

    const drama = createProjectFromTemplate('short-drama')
    expect(drama.nodes).toHaveLength(4)
    expect(drama.edges).toHaveLength(4)
    expect(drama.shots.length).toBeGreaterThanOrEqual(2)

    const charSheet = createProjectFromTemplate('character-sheet')
    expect(charSheet.nodes).toHaveLength(4)
    expect(charSheet.edges).toHaveLength(3)

    const blank = createProjectFromTemplate('blank', '我的空白画布')
    expect(blank.name).toBe('我的空白画布')
    expect(blank.nodes).toHaveLength(0)

    const built = buildStoryboardCanvasGraph(drama.shots)
    expect(built.nodes).toHaveLength(drama.shots.length * 3)
    expect(built.edges).toHaveLength(drama.shots.length * 3)
  })
})

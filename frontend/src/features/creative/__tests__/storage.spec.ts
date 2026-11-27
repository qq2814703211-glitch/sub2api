import { beforeEach, describe, expect, it } from 'vitest'
import { createProjectFromTemplate } from '../graph'
import {
  buildProjectExportBundle,
  clearAllLocalAssets,
  deleteLocalAsset,
  deleteLocalProject,
  formatBytes,
  listLocalAssets,
  listLocalProjects,
  parseProjectImportBundle,
  resetMemoryStorageForTests,
  saveLocalAsset,
  saveLocalProject,
} from '../storage'

describe('creative browser-local storage & toonflow export/import', () => {
  beforeEach(() => {
    resetMemoryStorageForTests()
    if (!('indexedDB' in globalThis)) {
      Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: undefined })
    }
  })

  it('persists and removes projects and assets locally without IndexedDB', async () => {
    const proj = createProjectFromTemplate('short-drama', '本地测试工程')
    await saveLocalProject(proj)
    expect((await listLocalProjects()).map((p) => p.name)).toEqual(['本地测试工程'])

    await saveLocalAsset({
      id: 'asset-1',
      name: '主角三视图.png',
      category: 'character',
      kind: 'image',
      mimeType: 'image/png',
      dataUrl: 'data:image/png;base64,AAA',
      createdAt: new Date().toISOString(),
    })
    expect(await listLocalAssets()).toHaveLength(1)

    await deleteLocalAsset('asset-1')
    expect(await listLocalAssets()).toHaveLength(0)

    await saveLocalAsset({
      id: 'asset-2',
      name: '场景.png',
      category: 'scene',
      kind: 'image',
      mimeType: 'image/png',
      dataUrl: 'data:image/png;base64,BBB',
      createdAt: new Date().toISOString(),
    })
    await clearAllLocalAssets()
    expect(await listLocalAssets()).toHaveLength(0)

    await deleteLocalProject(proj.id)
    expect(await listLocalProjects()).toHaveLength(0)
  })

  it('exports and imports Creative Plaza bundles and bare Toonflow canvas JSON', () => {
    const project = createProjectFromTemplate('character-sheet', '角色融合工程')
    const bundle = buildProjectExportBundle(project, [
      {
        id: 'a-1',
        name: 'hero.png',
        category: 'character',
        kind: 'image',
        mimeType: 'image/png',
        dataUrl: 'data:image/png;base64,XYZ',
        createdAt: new Date().toISOString(),
      },
    ])

    const imported = parseProjectImportBundle(JSON.stringify(bundle))
    expect(imported.project.name).toBe('角色融合工程')
    expect(imported.project.id).not.toBe(project.id)
    expect(imported.project.nodes).toHaveLength(project.nodes.length)
    expect(imported.assets).toHaveLength(1)

    const rawToonflow = parseProjectImportBundle(
      JSON.stringify({
        toonflowCanvas: true,
        name: 'Toonflow 导出的画布',
        nodes: project.nodes,
        edges: project.edges,
      }),
    )
    expect(rawToonflow.project.name).toBe('Toonflow 导出的画布')
    expect(rawToonflow.project.nodes).toHaveLength(project.nodes.length)

    expect(() => parseProjectImportBundle('{invalid')).toThrow()
    expect(() => parseProjectImportBundle('{"foo":1}')).toThrow()
  })

  it('formats byte usage into readable labels', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_WORKSPACE_DIR,
  ensureSeeded,
  joinPath,
  vfsExportZip,
  vfsListAssetTree,
  vfsListWorkspaceRel,
  vfsReadText,
  vfsWriteBytes,
} from '../../../../toonflow/runtime/vfs'
import {
  connectRemoteA2aAgent,
  listAgentTeams,
  listMcpServers,
  saveMcpServer,
  testMcpServer,
} from '../../../../toonflow/runtime/mcpAndA2a'
import {
  installFromMarketOrFile,
  listAllSkills,
  listInstalledExtensions,
  listInstalledNodes,
  listMarketPlugins,
  toggleMarketCollection,
} from '../../../../toonflow/runtime/pluginMarketRuntime'
import {
  getBrowserFfmpegStatus,
  getBrowserLocalModelsStatus,
} from '../../../../toonflow/runtime/browserMedia'

describe('Toonflow browser-native local runtime', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('seeds browser VFS workspace, reads/writes files, lists assets, and exports ZIP', async () => {
    await ensureSeeded()
    const listed = await vfsListWorkspaceRel(DEFAULT_WORKSPACE_DIR)
    expect(listed.entries.some((e) => e.name === '主画布.json')).toBe(true)
    expect(listed.entries.some((e) => e.name === '剧本与分镜.md')).toBe(true)
    const canvasRead = await vfsReadText(joinPath(DEFAULT_WORKSPACE_DIR, '主画布.json'))
    expect(JSON.parse(canvasRead.text)).toMatchObject({ toonflowCanvas: true })

    const testPath = joinPath(DEFAULT_WORKSPACE_DIR, 'notes/test-shot.md')
    await vfsWriteBytes(testPath, '# 镜头测试\n雨夜霓虹')
    const read = await vfsReadText(testPath)
    expect(read.text).toContain('雨夜霓虹')

    const assets = await vfsListAssetTree()
    expect(assets.some((a) => a.name === '角色设定')).toBe(true)

    const zipBytes = await vfsExportZip(DEFAULT_WORKSPACE_DIR)
    expect(zipBytes.byteLength).toBeGreaterThan(100)
  })

  it('exposes all 9 official Toonflow nodes (including 3D trumanMap) and all 5 extensions', async () => {
    const nodes = await listInstalledNodes()
    const nodeNames = nodes.map((n) => n.name)
    expect(nodeNames).toEqual(
      expect.arrayContaining([
        'textNode',
        'imageNode',
        'imageGenerationNode',
        'videoNode',
        'videoGenerationNode',
        'audioNode',
        'audioGenerationNode',
        'frameAnalysisNode',
        'trumanMap',
      ]),
    )

    const exts = await listInstalledExtensions()
    expect(exts.map((e) => e.name)).toEqual(
      expect.arrayContaining(['canvasNode', 'image', 'md', 'media', 'txt']),
    )
  })

  it('supports Plugin Market browsing, collection toggling, and browser skill installation', async () => {
    const market = await listMarketPlugins({ page: 1, limit: 20, type: 'all' })
    expect(market.total).toBeGreaterThanOrEqual(6)
    expect(market.list.some((p) => p.identifier === 'trumanMap')).toBe(true)

    const firstId = market.list[0]!.id
    const toggled = await toggleMarketCollection(firstId)
    expect(toggled.isCollected).toBe(true)

    await installFromMarketOrFile({ type: 'skill', identifier: 'camera-director-skill' })
    const skills = await listAllSkills()
    expect(skills.some((s) => s.name === 'camera-director-skill')).toBe(true)
    expect(skills.some((s) => s.name === 'workflow')).toBe(true)
  })

  it('runs browser MCP JSON-RPC discovery and A2A Agent Card connection without server proxy', async () => {
    const saved = await saveMcpServer({
      name: 'Demo HTTP MCP',
      transport: 'streamableHttp',
      url: 'https://mcp.example.test/mcp',
      enabled: true,
    })
    expect((await listMcpServers()).some((s) => s.id === saved.id)).toBe(true)

    const mockFetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const parsed = JSON.parse(String(init?.body ?? '{}')) as { method?: string }
      if (parsed.method === 'initialize') {
        return new Response(JSON.stringify({ jsonrpc: '2.0', id: 1, result: { protocolVersion: '2025-03-26' } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json', 'mcp-session-id': 'sess-1' },
        })
      }
      if (parsed.method === 'tools/list') {
        return new Response(
          JSON.stringify({
            jsonrpc: '2.0',
            id: 2,
            result: { tools: [{ name: 'storyboard_check', description: 'Check continuity' }] },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(JSON.stringify({ ok: true }), { status: 200 })
    }) as unknown as typeof fetch

    const testResult = await testMcpServer(mockFetch, saved)
    expect(testResult.ok).toBe(true)
    expect(testResult.toolCount).toBe(1)
    expect(testResult.tools[0]?.name).toBe('storyboard_check')

    // Reject stdio in pure web mode
    await expect(testMcpServer(mockFetch, { transport: 'stdio', command: 'npx' })).rejects.toThrow(/浏览器/)

    // A2A remote agent card connect + built-in storyboardTeam
    const a2aFetch = vi.fn(async () =>
      new Response(
        JSON.stringify({
          name: 'Remote Director Agent',
          description: 'A2A director helper',
          version: '1.0.0',
          url: 'https://agent.example.test/rpc',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as unknown as typeof fetch
    const connected = await connectRemoteA2aAgent(a2aFetch, {
      name: 'remoteDirector',
      cardUrl: 'https://agent.example.test/.well-known/agent.json',
    })
    expect(connected.kind).toBe('remote')
    const teams = await listAgentTeams()
    expect(teams.some((t) => t.name === 'storyboardTeam' && t.builtin)).toBe(true)
    expect(teams.some((t) => t.name === 'remoteDirector' && t.kind === 'remote')).toBe(true)
  })

  it('reports pure browser-native media and WebGL capabilities with zero server FFmpeg', () => {
    const ffmpeg = getBrowserFfmpegStatus()
    expect(ffmpeg.platform).toBe('browser')
    expect(ffmpeg.supported).toBe(true)
    expect(ffmpeg.tools.ffmpeg.path).toMatch(/^browser:\/\//)

    const localModels = getBrowserLocalModelsStatus()
    expect(localModels.supported).toBe(true)
    expect(localModels.models.length).toBeGreaterThanOrEqual(2)
  })
})

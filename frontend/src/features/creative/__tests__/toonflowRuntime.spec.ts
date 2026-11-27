import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale, supportedLocales, translate } from '../../../../toonflow/upstream/packages/i18n/src/index'
import {
  createPluginTranslator,
  createScopedPluginTranslator,
  ensurePluginI18nLoaded,
} from '../../../../toonflow/runtime/pluginI18n'
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
  BUILTIN_TOOL_PACKAGES,
  installFromMarketOrFile,
  listAllSkills,
  listInstalledExtensions,
  listInstalledNodes,
  listInstalledTools,
  listMarketPlugins,
  setPluginEnabled,
  toggleMarketCollection,
} from '../../../../toonflow/runtime/pluginMarketRuntime'
import {
  answerAgentQuestion,
  applyAgentCommand,
  branchAgentConversation,
  buildWorkspaceAgentTools,
  createAgentConversation,
  deleteAgentMessages,
  getActiveWorkspaceToolNames,
  getAgentSession,
  runWorkspaceAgentMessage,
  submitAgentCanvasResult,
} from '../../../../toonflow/runtime/agentRuntime'
import { updateHostContext } from '../../../../toonflow/runtime/sub2apiBridge'
import {
  getBrowserFfmpegStatus,
  getBrowserLocalModelsStatus,
} from '../../../../toonflow/runtime/browserMedia'

async function readNdjsonEvents(response: Response): Promise<Record<string, unknown>[]> {
  const text = await response.text()
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line) as Record<string, unknown>)
}

describe('Toonflow browser-native local runtime', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('includes all 22 upstream Toonflow interface languages including Japanese and Portuguese', () => {
    expect(supportedLocales).toHaveLength(22)
    const names = supportedLocales.map((item) => item.name)
    expect(names).toEqual(
      expect.arrayContaining(['简体中文', '繁體中文', 'English', '日本語', 'Português (Brasil)', '한국어', 'العربية']),
    )
    setLocale('ja')
    expect(translate('插件市场')).not.toBe('插件市场')
    setLocale('pt')
    expect(translate('插件市场')).not.toBe('插件市场')
    setLocale('zh-CN')
  })

  it('loads and merges per-package 22-locale plugin dictionaries for imageNode, trumanMap, shared deps, and tools', async () => {
    try {
      setLocale('ja')
      await ensurePluginI18nLoaded('imageNode', 'ja')
      await ensurePluginI18nLoaded('trumanMap', 'ja')

      const imageTranslator = createScopedPluginTranslator('imageNode')
      expect(imageTranslator.translate('生成720°全景图')).toBe('720°のパノラマを生成')
      expect(imageTranslator.translate('生成360°环绕图')).toBe('360°サラウンド画像を生成')
      expect(imageTranslator.translate('预览720°全景图')).toBe('720° パノラマをプレビュー')
      expect(imageTranslator.translate('预览360°环绕图')).toBe('360°サラウンド画像をプレビュー')
      // Fallback from imageNode -> host catalog
      expect(imageTranslator.translate('插件市场')).toBe('プラグインマーケット')

      const trumanTranslator = createScopedPluginTranslator('trumanMap')
      expect(trumanTranslator.translate('素材')).toBe('資産')
      expect(trumanTranslator.translate('画笔')).toBe('ブラシ')
      expect(trumanTranslator.translate('演绎')).toBe('パフォーマンス')
      expect(trumanTranslator.translate('按剧本一键预演')).toBe('スクリプトからワンクリックでプリビズ')
      expect(trumanTranslator.translate('打开场景编辑器')).toBe('シーンエディタを開く')

      // Shared node components (mediaToolDialog / cascadeMenuItems) can translate node tool labels
      const sharedTranslator = createScopedPluginTranslator('nodeShared')
      expect(sharedTranslator.translate('搜索画布素材')).not.toBe('搜索画布素材')
      expect(sharedTranslator.translate('生成720°全景图')).toBe('720°のパノラマを生成')

      // Tool UI scopes (askUser & canvas)
      const askUserTranslator = createScopedPluginTranslator('askUser')
      expect(askUserTranslator.translate('提交回答')).not.toBe('提交回答')

      // Custom plugin host translator respects own catalog first, then falls back to shared/host catalogs
      const customTranslator = createPluginTranslator({
        ja: { 自定义按钮: 'カスタムボタン' },
      })
      expect(customTranslator.translate('自定义按钮')).toBe('カスタムボタン')
      expect(customTranslator.translate('生成720°全景图')).toBe('720°のパノラマを生成')
      expect(customTranslator.translate('插件市场')).toBe('プラグインマーケット')

      // Verify additional non-zh/en locales (pt & ar) also resolve node & trumanMap dictionaries
      for (const code of ['pt', 'ar', 'ko', 'ru'] as const) {
        setLocale(code)
        await ensurePluginI18nLoaded('trumanMap', code)
        expect(imageTranslator.translate('生成720°全景图')).not.toBe('生成720°全景图')
        expect(trumanTranslator.translate('按剧本一键预演')).not.toBe('按剧本一键预演')
      }
    } finally {
      setLocale('zh-CN')
    }
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

  it('maintains 1-to-1 alignment between Plugin Market enabled tools and Agent Runtime tools', async () => {
    const installed = await listInstalledTools()
    const activeToolNames = await getActiveWorkspaceToolNames(DEFAULT_WORKSPACE_DIR)
    const activeSet = new Set(activeToolNames)

    // Every browser-supported enabled package must register ALL its declared tool names
    for (const pkg of BUILTIN_TOOL_PACKAGES) {
      const uiEntry = installed.tools.find((item) => item.name === pkg.name)
      expect(uiEntry).toBeDefined()
      if (pkg.browserSupported) {
        expect(uiEntry?.enabled).toBe(true)
        expect(uiEntry?.loadError).toBe('')
        for (const fnName of pkg.agentToolNames) {
          expect(activeSet.has(fnName)).toBe(true)
        }
      } else {
        // Server/desktop-only packages must be honestly disabled with loadError
        expect(uiEntry?.enabled).toBe(false)
        expect(uiEntry?.loadError).toBeTruthy()
        await expect(setPluginEnabled('tool', pkg.name, true)).rejects.toThrow()
      }
    }

    // Toggling a browser-supported tool OFF must remove it from Agent Runtime
    await setPluginEnabled('tool', 'webFetch', false)
    expect(await getActiveWorkspaceToolNames(DEFAULT_WORKSPACE_DIR)).not.toContain('web_fetch')
    await setPluginEnabled('tool', 'webFetch', true)
    expect(await getActiveWorkspaceToolNames(DEFAULT_WORKSPACE_DIR)).toContain('web_fetch')
  })

  it('executes workspace, assetOperator, skillOperator (incl. references), askUser, web_fetch, and web_search tools', async () => {
    await ensureSeeded()
    const mockFetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('wikipedia.org')) {
        return new Response(
          JSON.stringify({
            query: {
              search: [{ title: 'Toonflow', pageid: 42, snippet: '<span>开源短剧平台</span>' }],
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response('<html><body><h1>示例标题</h1><p>静态正文</p></body></html>', {
        status: 200,
        headers: { 'Content-Type': 'text/html; charset=utf-8' },
      })
    }) as unknown as typeof fetch

    const emittedEvents: Record<string, unknown>[] = []
    const { execute } = await buildWorkspaceAgentTools(mockFetch, DEFAULT_WORKSPACE_DIR, {
      emitEvent: (ev) => {
        emittedEvents.push(ev)
        if (ev.type === 'question' && typeof ev.callId === 'string') {
          queueMicrotask(() => {
            answerAgentQuestion(ev.callId as string, { answer: '竖屏 9:16 赛博风' })
          })
        }
      },
    })

    // workspace write / edit / read / find / grep
    await execute('write', { path: 'notes/demo.md', content: '第一行\n旧台词\n' })
    await execute('edit', { path: 'notes/demo.md', oldText: '旧台词', newText: '雨夜新台词' })
    const readOut = await execute('read', { path: 'notes/demo.md' })
    expect(readOut).toContain('雨夜新台词')
    const grepOut = await execute('grep', { pattern: '雨夜新台词', path: 'notes' })
    expect(grepOut).toContain('notes/demo.md:2:')
    const findOut = await execute('find', { pattern: '*.md', path: 'notes' })
    expect(findOut).toContain('notes/demo.md')

    // assetOperator create / read / update (with SHA-256 revision) / import / delete
    const createdAsset = JSON.parse(
      await execute('assetOperator', { action: 'create', path: '角色设定/测试角色.md', content: '# 主角设定' }),
    ) as { ok: boolean; revision: string }
    expect(createdAsset.ok).toBe(true)
    expect(createdAsset.revision).toMatch(/^[a-f0-9]{64}$/)
    const updatedAsset = JSON.parse(
      await execute('assetOperator', {
        action: 'update',
        path: '角色设定/测试角色.md',
        content: '# 主角设定 V2',
        revision: createdAsset.revision,
      }),
    ) as { ok: boolean }
    expect(updatedAsset.ok).toBe(true)
    const importedAsset = JSON.parse(
      await execute('assetOperator', { action: 'import', path: '角色设定/测试角色.md' }),
    ) as { ok: boolean; workspacePath: string }
    expect(importedAsset.ok).toBe(true)
    expect(importedAsset.workspacePath).toMatch(/^assets\//)
    await execute('assetOperator', { action: 'delete', path: '角色设定/测试角色.md' })

    // skillOperator list + read bundled references/sopRouting.md
    const skillList = JSON.parse(await execute('skillOperator', { action: 'list' })) as {
      skills: { name: string; files: string[] }[]
    }
    const wfSkill = skillList.skills.find((s) => s.name === 'workflow')
    expect(wfSkill?.files.some((f) => f.includes('references/sopRouting.md'))).toBe(true)
    const refDoc = JSON.parse(
      await execute('skillOperator', { action: 'read', name: 'workflow', path: 'references/sopRouting.md' }),
    ) as { content: string }
    expect(refDoc.content.length).toBeGreaterThan(50)

    // askUser interactive flow
    const askOut = JSON.parse(
      await execute('askUser', {
        title: '确认画幅',
        question: '请选择目标短剧画幅',
        options: ['竖屏 9:16 赛博风', '横屏 16:9 电影风'],
      }, 'tc-ask-1'),
    ) as { answer: string }
    expect(askOut.answer).toBe('竖屏 9:16 赛博风')
    expect(emittedEvents.some((e) => e.type === 'question' && e.toolCallId === 'tc-ask-1')).toBe(true)

    // web_fetch & web_search
    const fetched = await execute('web_fetch', { url: 'https://example.test/doc' })
    expect(fetched).toContain('示例标题')
    expect(fetched).toContain('静态正文')
    const searched = JSON.parse(await execute('web_search', { query: 'Toonflow' })) as {
      provider: string
      sources: { title: string; url: string }[]
    }
    expect(searched.provider).toBe('wikipedia')
    expect(searched.sources[0]?.title).toBe('Toonflow')
  })

  it('streams AI Assistant replies with matching runId, canvasCall round-trip, status=success, and persistent parts', async () => {
    await ensureSeeded()
    updateHostContext({
      origin: 'http://127.0.0.1:8080',
      keys: [{ id: 1, name: 'Test Key', key: 'sk-test-agent', platform: 'openai', models: ['gpt-4o'] }],
      selectedTextKeyId: 1,
    })

    let turn = 0
    const mockFetch = vi.fn(async () => {
      turn++
      if (turn === 1) {
        // First LLM round invokes official canvas getCanvas tool
        const sse = [
          `data: ${JSON.stringify({
            choices: [
              {
                delta: {
                  tool_calls: [
                    {
                      index: 0,
                      id: 'call_canvas_1',
                      function: { name: 'getCanvas', arguments: '{"include":["canvases"]}' },
                    },
                  ],
                },
              },
            ],
          })}`,
          'data: [DONE]',
          '',
        ].join('\n\n')
        return new Response(sse, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
      }
      // Second LLM round returns real streamed Chinese reply
      const sse = [
        `data: ${JSON.stringify({ choices: [{ delta: { content: '你好，我是运行在你浏览器本机的 ' } }] })}`,
        `data: ${JSON.stringify({ choices: [{ delta: { content: 'Toonflow AI 创作助手。' } }] })}`,
        `data: ${JSON.stringify({ usage: { prompt_tokens: 42, completion_tokens: 18, total_tokens: 60 } })}`,
        'data: [DONE]',
        '',
      ].join('\n\n')
      return new Response(sse, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
    }) as unknown as typeof fetch

    const conv = await createAgentConversation(DEFAULT_WORKSPACE_DIR)
    const callerRunId = 'run-test-echo-123'
    const resp = runWorkspaceAgentMessage(mockFetch, {
      directory: DEFAULT_WORKSPACE_DIR,
      sessionFile: conv.file,
      runId: callerRunId,
      prompt: '请用一句话介绍你自己',
      providerId: 'sub2api-text',
      modelId: 'gpt-4o',
      canvas: { id: '主画布.json', tools: [] },
    })

    // Consume NDJSON incrementally so we can answer canvasCall live
    const reader = resp.body!.pipeThrough(new TextDecoderStream()).getReader()
    const events: Record<string, unknown>[] = []
    let pending = ''
    while (true) {
      const { value, done } = await reader.read()
      if (value) pending += value
      const lines = pending.split('\n')
      pending = lines.pop() ?? ''
      if (done && pending.trim()) lines.push(pending)
      for (const line of lines) {
        if (!line.trim()) continue
        const ev = JSON.parse(line) as Record<string, unknown>
        events.push(ev)
        if (ev.type === 'canvasCall' && typeof ev.callId === 'string') {
          submitAgentCanvasResult({
            callId: ev.callId,
            result: { canvasId: '主画布.json', nodeCount: 4, edgeCount: 3 },
          })
        }
      }
      if (done) break
    }

    // 1. Caller runId MUST be echoed verbatim so conversation.vue does not drop events
    expect(events.find((e) => e.type === 'session')).toMatchObject({
      type: 'session',
      file: conv.file,
      runId: callerRunId,
    })
    // 2. Canvas tool completed with upstream status "success" (not "done")
    expect(
      events.some(
        (e) =>
          e.type === 'tool' &&
          (e.tool as { name?: string; status?: string })?.name === 'getCanvas' &&
          (e.tool as { status?: string })?.status === 'success',
      ),
    ).toBe(true)
    // 3. Streamed text deltas + done emitted
    const streamedText = events
      .filter((e) => e.type === 'text' && typeof e.delta === 'string')
      .map((e) => String(e.delta))
      .join('')
    expect(streamedText).toBe('你好，我是运行在你浏览器本机的 Toonflow AI 创作助手。')
    expect(events.at(-1)?.type).toBe('done')

    // 4. Session persistence preserves parts + replyTo + entryId for reload rendering
    const saved = await getAgentSession(DEFAULT_WORKSPACE_DIR, conv.file)
    expect(saved.messages).toHaveLength(2)
    const assistantMsg = saved.messages[1]!
    expect(assistantMsg.role).toBe('assistant')
    expect(assistantMsg.content).toContain('Toonflow AI 创作助手')
    expect(assistantMsg.parts?.some((p) => p.type === 'text' && p.content.includes('Toonflow AI 创作助手'))).toBe(true)
    expect(assistantMsg.parts?.some((p) => p.type === 'tool')).toBe(true)

    // 5. Commands, branching, compaction, and deletion work cleanly
    const cmdState = await applyAgentCommand(DEFAULT_WORKSPACE_DIR, conv.file, { action: 'plan', enabled: true })
    expect(cmdState.plan.active).toBe(true)
    const branched = await branchAgentConversation(DEFAULT_WORKSPACE_DIR, conv.file, assistantMsg.entryId)
    expect(branched.messages).toHaveLength(2)
    const compactResp = runWorkspaceAgentMessage(mockFetch, {
      directory: DEFAULT_WORKSPACE_DIR,
      sessionFile: conv.file,
      runId: 'compact-run-1',
      operation: 'compact',
    })
    const compactEvents = await readNdjsonEvents(compactResp)
    expect(compactEvents.at(-1)?.type).toBe('done')
    const afterDelete = await deleteAgentMessages(DEFAULT_WORKSPACE_DIR, conv.file, {
      replyTo: assistantMsg.replyTo,
    })
    expect(afterDelete.messages).toHaveLength(1)
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

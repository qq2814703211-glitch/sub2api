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
  createMcpControlSseResponse,
  handleMcpControlCall,
  listAgentTeams,
  listControlConnections,
  listMcpServers,
  saveMcpServer,
  testMcpServer,
} from '../../../../toonflow/runtime/mcpAndA2a'
import { RETOUCH_CATEGORIES } from '../../imagegen/retouch'
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
import {
  SUB2API_BRIDGE_STORAGE_KEY,
  getHostContext,
  initSub2ApiBridge,
  subscribeHostContext,
  updateHostContext,
  updateKeyBindingsFromStudio,
} from '../../../../toonflow/runtime/sub2apiBridge'
import { listAllTextProviders, listMediaProviders } from '../../../../toonflow/runtime/aiGateway'
import { kvSet } from '../../../../toonflow/runtime/vfs'
import { sanitizeBrandText } from '../../../../toonflow/custom/i18nOverrides'
import {
  getBrowserFfmpegStatus,
  getBrowserLocalModelsStatus,
} from '../../../../toonflow/runtime/browserMedia'
import { verifySiteKey } from '../../../../toonflow/custom/quickImage/quickImageApi'

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

  it('maintains 1-to-1 alignment between Plugin Market browser tools and Agent Runtime tools without desktop CLI entries', async () => {
    const installed = await listInstalledTools()
    const activeToolNames = await getActiveWorkspaceToolNames(DEFAULT_WORKSPACE_DIR)
    const activeSet = new Set(activeToolNames)

    // Every browser-supported enabled package must register ALL its declared tool names,
    // and desktop-only CLI tools (ffmpeg, sandbox, browser, pluginDeveloper) must be hidden from browser UI.
    for (const pkg of BUILTIN_TOOL_PACKAGES) {
      const uiEntry = installed.tools.find((item) => item.name === pkg.name)
      if (pkg.browserSupported) {
        expect(uiEntry).toBeDefined()
        expect(uiEntry?.enabled).toBe(true)
        expect(uiEntry?.loadError).toBe('')
        for (const fnName of pkg.agentToolNames) {
          expect(activeSet.has(fnName)).toBe(true)
        }
      } else {
        expect(uiEntry).toBeUndefined()
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
    await expect(testMcpServer(mockFetch, { transport: 'stdio', command: 'npx' })).rejects.toThrow(/网页端|浏览器/)

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

  it('honestly marks desktop ONNX local models unsupported in browser mode while keeping browser media probe/frame support', () => {
    const ffmpeg = getBrowserFfmpegStatus()
    expect(ffmpeg.platform).toBe('browser')
    expect(ffmpeg.supported).toBe(true)
    expect(ffmpeg.tools.ffmpeg.path).toMatch(/^browser:\/\//)

    const localModels = getBrowserLocalModelsStatus()
    expect(localModels.supported).toBe(false)
    expect(localModels.items).toEqual([])
  })

  it('sanitizes Toonflow branding and serves custom Studio i18n overrides across all 22 locales', () => {
    try {
      setLocale('zh-CN')
      expect(sanitizeBrandText('登录 Toonflow 开始创作', 'zh-CN')).toBe('登录 创意广场 开始创作')
      expect(translate('快捷生图')).toBe('快捷生图')
      expect(translate('你好，我是 AI 助手')).toBe('你好，我是 AI 助手')
      expect(translate('扩展')).toBe('扩展')

      setLocale('en')
      expect(sanitizeBrandText('Welcome to Toonflow', 'en')).toBe('Welcome to Creative Plaza')
      expect(translate('快捷生图')).toBe('Quick Image')
      expect(translate('密钥与模型')).toBe('API Keys & Models')
      expect(translate('Toonflow')).toBe('Creative Plaza')
      expect(translate('你好，我是 AI 助手')).toBe('Hello, I am AI Assistant')
      expect(translate('扩展')).toBe('Extensions')
      expect(translate('每一个灵感，都值得被看见')).toBe('Every inspiration deserves to be seen')
      expect(translate('工作区 Skill')).toBe('Workspace Skill')
      expect(translate('无')).toBe('None')
      expect(translate('宽度')).toBe('Width')
      expect(translate('高度')).toBe('Height')

      setLocale('ja')
      expect(sanitizeBrandText('Toonflow の設定', 'ja')).toBe('クリエイティブ広場 の設定')
      expect(translate('快捷生图')).toBe('クイック画像生成')
      expect(translate('质量')).toBe('画質')
      expect(translate('数量')).toBe('枚数')
      expect(translate('格式')).toBe('形式')
      expect(translate('风格')).toBe('スタイル')
      expect(translate('高级参数')).toBe('詳細設定')
      expect(translate('审核')).toBe('モデレーション')
      expect(translate('压缩')).toBe('圧縮')
      expect(translate('改写模型')).toBe('リライトモデル')
      expect(translate('自动（不指定）')).toBe('自動（指定なし）')
      expect(translate('暂无匹配模板')).toBe('一致するテンプレートがありません')
      expect(translate('扩展')).toBe('拡張機能')
      expect(translate('你好，我是 AI 助手')).toBe('こんにちは、私は AI アシスタントです')

      const criticalKeys = [
        '你好，我是 AI 助手',
        '扩展',
        '快捷生图',
        '密钥与模型',
        '质量',
        '数量',
        '格式',
        '风格',
        '高级参数',
        '改写模型',
        '自动（不指定）',
        '暂无匹配模板',
        'AI 助手',
        '助手说明',
        '助手记忆',
        '每一个灵感，都值得被看见',
      ]
      for (const item of supportedLocales) {
        if (item.code === 'zh-CN' || item.code === 'zh-TW') continue
        setLocale(item.code)
        for (const key of criticalKeys) {
          const translated = translate(key)
          expect(translated, `locale=${item.code} key=${key}`).not.toBe(key)
          expect(translated, `locale=${item.code} key=${key}`).not.toMatch(/toonflow/i)
          if (item.code !== 'ja') {
            expect(translated, `locale=${item.code} key=${key}`).not.toMatch(/[\u4e00-\u9fff]/)
          }
        }
      }
    } finally {
      setLocale('zh-CN')
    }
  })

  it('strictly uses /models lists with zero hardcoded fallbacks, orders configured default models first, and verifies all 4 roles uniformly', async () => {
    // 1. Key with no discovered models must NOT synthesize hardcoded fallback models
    updateHostContext({
      origin: 'https://studio.example.com',
      keys: [
        { id: 201, name: 'Empty Key', key: 'sk-empty', platform: 'openai', groupName: 'Empty' },
      ],
      selectedTextKeyId: 201,
      selectedImageKeyId: 201,
      selectedVideoKeyId: 201,
      selectedAudioKeyId: 201,
      defaultTextModel: '',
      defaultImageModel: '',
      defaultVideoModel: '',
      defaultAudioModel: '',
      discoveredModels: { 201: [] },
      discoveredModelMeta: { 201: [] },
    })

    const emptyText = await listAllTextProviders()
    expect(emptyText[0]?.models).toEqual([])
    const emptyMedia = await listMediaProviders()
    expect(emptyMedia[0]?.models).toEqual([])

    // 2. Non-standard model IDs returned by /models are preserved without hardcoded filtering,
    //    and configured default models are ordered first for every role.
    updateHostContext({
      origin: 'https://studio.example.com',
      keys: [
        { id: 301, name: 'Text/Img Key', key: 'sk-ti', platform: 'openai', groupName: 'G1' },
        { id: 302, name: 'Vid/Aud Key', key: 'sk-va', platform: 'openai', groupName: 'G2' },
      ],
      selectedTextKeyId: 301,
      selectedImageKeyId: 301,
      selectedVideoKeyId: 302,
      selectedAudioKeyId: 302,
      defaultTextModel: 'my-custom-writer',
      defaultImageModel: 'nano-banana-pro',
      defaultVideoModel: 'custom-vid-gen',
      defaultAudioModel: 'custom-tts-v3',
      discoveredModels: {
        301: ['alpha-model', 'nano-banana-pro', 'my-custom-writer'],
        302: ['custom-tts-v3', 'custom-vid-gen', 'other-model'],
      },
      discoveredModelMeta: {
        301: [{ id: 'alpha-model' }, { id: 'nano-banana-pro' }, { id: 'my-custom-writer', supportsReasoningEffort: true }],
        302: [{ id: 'custom-tts-v3' }, { id: 'custom-vid-gen' }, { id: 'other-model' }],
      },
    })

    const textProviders = await listAllTextProviders()
    expect(textProviders[0]?.models.map((m) => m.id)).toEqual(['my-custom-writer', 'alpha-model', 'nano-banana-pro'])
    expect(textProviders[0]?.models[0]?.reasoning).toBe(true)

    const mediaProviders = await listMediaProviders()
    const allMedia = mediaProviders[0]?.models ?? []
    expect(allMedia.filter((m) => m.type === 'image').map((m) => m.modelId)[0]).toBe('nano-banana-pro')
    expect(allMedia.filter((m) => m.type === 'video').map((m) => m.modelId)[0]).toBe('custom-vid-gen')
    expect(allMedia.filter((m) => m.type === 'audio').map((m) => m.modelId)[0]).toBe('custom-tts-v3')
    expect(allMedia.filter((m) => m.type === 'video').map((m) => m.modelId)).toContain('other-model')

    // 3. Uniform /v1/models verification across image, text, video, and audio roles
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(
        JSON.stringify({
          data: [
            { id: 'kling-v2-master' },
            { id: 'nano-banana-pro' },
            { id: 'fish-speech-v2' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )
    for (const role of ['image', 'text', 'video', 'audio'] as const) {
      const verified = await verifySiteKey('sk-uniform-test', role)
      expect(verified.ok).toBe(true)
      expect(verified.modelCount).toBe(3)
      expect(verified.models).toEqual(['fish-speech-v2', 'kling-v2-master', 'nano-banana-pro'])
    }
  })

  it('enforces site-only API providers and ignores stored third-party customProviders', async () => {
    await kvSet('toonflow.settings', {
      customProviders: [
        {
          id: 'evil-third-party',
          label: 'External Provider',
          apiUrl: 'https://third-party.example.com/v1',
          protocol: 'openai-completions',
          apiKey: 'sk-external',
          models: [{ id: 'external-model', label: 'external-model' }],
        },
      ],
    })
    updateHostContext({
      origin: 'https://studio.example.com',
      keys: [
        { id: 101, name: 'Site Key', key: 'sk-site-only', platform: 'openai', groupName: 'OpenAI' },
      ],
      selectedTextKeyId: 101,
      selectedImageKeyId: 101,
      discoveredModels: { 101: ['gpt-4o', 'gpt-image-1'] },
    })

    const textProviders = await listAllTextProviders()
    expect(textProviders.length).toBeGreaterThan(0)
    expect(textProviders.every((p) => p.apiUrl.startsWith('https://studio.example.com'))).toBe(true)
    expect(textProviders.some((p) => p.id === 'evil-third-party')).toBe(false)

    const mediaProviders = await listMediaProviders()
    expect(mediaProviders.length).toBeGreaterThan(0)
    expect(mediaProviders.every((p) => !p.apiUrl || p.apiUrl.startsWith('https://studio.example.com'))).toBe(true)
  })

  it('updates host context and notifies parent window when Studio changes key bindings', () => {
    const parentPostMessage = vi.fn()
    const origParent = window.parent
    Object.defineProperty(window, 'parent', {
      configurable: true,
      value: { postMessage: parentPostMessage },
    })
    try {
      updateKeyBindingsFromStudio({
        textKeyId: 101,
        imageKeyId: 102,
        rewriteModel: 'gpt-4o-mini',
        rewriteEffort: 'low',
      })
      const ctx = getHostContext()
      expect(ctx.selectedTextKeyId).toBe(101)
      expect(ctx.selectedImageKeyId).toBe(102)
      expect(ctx.rewriteModel).toBe('gpt-4o-mini')
      expect(ctx.rewriteEffort).toBe('low')
      expect(parentPostMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          source: 'toonflow-web',
          type: 'updateKeyBindings',
          payload: expect.objectContaining({
            textKeyId: 101,
            imageKeyId: 102,
            rewriteModel: 'gpt-4o-mini',
            rewriteEffort: 'low',
          }),
        }),
        window.location.origin,
      )
    } finally {
      Object.defineProperty(window, 'parent', {
        configurable: true,
        value: origParent,
      })
    }
  })

  it('syncs host context from cross-window storage events without echoing back to localStorage', () => {
    initSub2ApiBridge()
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem')
    const seenThemes: string[] = []
    const unsubscribe = subscribeHostContext((ctx) => {
      seenThemes.push(ctx.theme)
    })
    try {
      const next = {
        ...getHostContext(),
        theme: 'light' as const,
        discoveredModels: { 101: ['gpt-4o', 'gpt-image-1'] },
      }
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: SUB2API_BRIDGE_STORAGE_KEY,
          newValue: JSON.stringify(next),
        }),
      )
      expect(getHostContext().theme).toBe('light')
      expect(seenThemes).toContain('light')
      expect(setItemSpy).not.toHaveBeenCalled()
    } finally {
      unsubscribe()
    }
  })

  it('returns MCP control connections with id and nested state shape expected by Settings -> MCP', () => {
    const controller = new AbortController()
    const connId = 'mcp-conn-test-1'
    createMcpControlSseResponse(connId, controller.signal)
    try {
      handleMcpControlCall('state', {
        connectionId: connId,
        state: {
          directory: '/workspaces/demo',
          panel: 'document',
          canvasId: 'canvas-1',
        },
      })
      const connections = listControlConnections()
      const found = connections.find((c) => c.id === connId)
      expect(found).toBeDefined()
      expect(found?.id).toBe(connId)
      expect(found?.state).toEqual({
        directory: '/workspaces/demo',
        panel: 'document',
        canvasId: 'canvas-1',
      })
    } finally {
      controller.abort()
    }
  })

  it('localizes plugin market catalog, built-in nodes/tools/skills/agents, and retouch presets cleanly in EN and JA', async () => {
    try {
      setLocale('en')
      const [nodesEn, extsEn, toolsEn, skillsEn, marketEn, teamsEn, textProvidersEn, mediaProvidersEn] = await Promise.all([
        listInstalledNodes(),
        listInstalledExtensions(),
        listInstalledTools(),
        listAllSkills(),
        listMarketPlugins({ page: 1, limit: 20 }),
        listAgentTeams(),
        listAllTextProviders(),
        listMediaProviders(),
      ])

      for (const provider of textProvidersEn) {
        expect(provider.label).not.toMatch(/[\u4e00-\u9fff]/)
      }
      for (const provider of mediaProvidersEn) {
        expect(provider.name).not.toMatch(/[\u4e00-\u9fff]/)
        for (const model of provider.models) {
          expect(model.providerLabel).not.toMatch(/[\u4e00-\u9fff]/)
          for (const rule of model.rules ?? []) {
            if (typeof rule.title === 'string') {
              expect(rule.title).not.toMatch(/[\u4e00-\u9fff]/)
            }
          }
        }
      }
      const ffmpegEn = getBrowserFfmpegStatus()
      for (const src of ffmpegEn.sources) {
        expect(src.label).not.toMatch(/[\u4e00-\u9fff]/)
        expect(src.description).not.toMatch(/[\u4e00-\u9fff]/)
      }

      for (const node of nodesEn) {
        expect(node.displayName).not.toMatch(/[\u4e00-\u9fff]/)
        expect(node.description).not.toMatch(/[\u4e00-\u9fff]/)
        expect(node.author).not.toMatch(/[\u4e00-\u9fff]/)
      }
      for (const ext of extsEn) {
        expect(ext.displayName).not.toMatch(/[\u4e00-\u9fff]/)
        expect(ext.author).not.toMatch(/[\u4e00-\u9fff]/)
      }
      for (const tool of toolsEn.tools) {
        expect(tool.displayName).not.toMatch(/[\u4e00-\u9fff]/)
        expect(tool.description).not.toMatch(/[\u4e00-\u9fff]/)
        expect(tool.loadError).not.toMatch(/[\u4e00-\u9fff]/)
      }
      for (const skill of skillsEn.filter((s) => s.builtin)) {
        expect(skill.displayName).not.toMatch(/[\u4e00-\u9fff]/)
        expect(skill.description).not.toMatch(/[\u4e00-\u9fff]/)
      }
      for (const item of marketEn.list) {
        expect(item.name).not.toMatch(/[\u4e00-\u9fff]/)
        expect(item.desc).not.toMatch(/[\u4e00-\u9fff]/)
        expect(item.supplier).not.toMatch(/[\u4e00-\u9fff]/)
        for (const tag of item.tags ?? []) {
          expect(tag.text).not.toMatch(/[\u4e00-\u9fff]/)
        }
      }
      for (const team of teamsEn.filter((t) => t.builtin)) {
        expect(team.displayName).not.toMatch(/[\u4e00-\u9fff]/)
        expect(team.description).not.toMatch(/[\u4e00-\u9fff]/)
      }

      for (const extraKey of [
        '安装',
        '更新',
        '访问未授权，请刷新页面后重试',
        '当前环境不支持云端上传插件',
        '选择站内 API 密钥即可开始创作。',
        '当前浏览器运行环境版本过旧，请更新浏览器后重试。',
        '当前移动端浏览器内核缺少所需能力，请更新系统浏览器后重试。',
        '未返回图片数据',
        '未返回改写结果',
        '本地导入',
        '仅存浏览器本地，不上传服务器',
        '默认模型',
        '请先校验密钥获取模型列表',
        '当前密钥未返回可用模型，请先校验密钥',
        '该密钥未从 /models 返回可用模型',
        '当前为纯浏览器本地运行模式，暂不支持桌面端 FFmpeg 音视频转码。',
        '当前为纯浏览器本地运行模式，暂不支持桌面端 ONNX 本地模型推理。',
      ]) {
        const translated = translate(extraKey)
        expect(translated, `en key=${extraKey}`).not.toBe(extraKey)
        expect(translated, `en key=${extraKey}`).not.toMatch(/[\u4e00-\u9fff]/)
      }

      for (const category of RETOUCH_CATEGORIES) {
        expect(translate(category.title), `en cat=${category.id}`).not.toMatch(/[\u4e00-\u9fff]/)
        for (const group of category.groups) {
          expect(translate(group.title), `en group=${group.title}`).not.toMatch(/[\u4e00-\u9fff]/)
          for (const preset of group.presets) {
            expect(translate(preset.title), `en preset=${preset.id}`).not.toMatch(/[\u4e00-\u9fff]/)
            if (preset.scenario) {
              expect(translate(preset.scenario), `en scenario=${preset.id}`).not.toMatch(/[\u4e00-\u9fff]/)
            }
          }
        }
      }

      setLocale('ja')
      expect(translate('安装')).toBe('インストール')
      expect(translate('肤色瑕疵')).toBe('肌・美肌補正')
      expect(translate('肤色矫正')).toBe('肌色補正')
      const [marketJa, nodesJa, textProvidersJa, mediaProvidersJa] = await Promise.all([
        listMarketPlugins({ page: 1, limit: 20 }),
        listInstalledNodes(),
        listAllTextProviders(),
        listMediaProviders(),
      ])
      expect(marketJa.list.find((p) => p.identifier === 'trumanMap')?.name).toBe('3D 白モデルプレビズ')
      expect(nodesJa.find((n) => n.name === 'textNode')?.displayName).toBe('テキスト')
      expect(textProvidersJa[0]?.label).toContain('サイト API')
      expect(mediaProvidersJa[0]?.name).toBe('サイト API')
      expect(getBrowserFfmpegStatus().sources[0]?.label).toBe('内蔵メディアエンジン')
    } finally {
      setLocale('zh-CN')
    }
  })
})

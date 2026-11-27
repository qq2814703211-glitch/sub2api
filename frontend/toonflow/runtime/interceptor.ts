import axios, { AxiosHeaders, type AxiosResponse, type InternalAxiosRequestConfig } from "axios";
import { getHostContext } from "./sub2apiBridge";
import {
  ASSETS_ROOT,
  DEFAULT_SCENE_DIR,
  DEFAULT_WORKSPACE_DIR,
  SCENES_ROOT,
  WORKSPACES_ROOT,
  dirnamePath,
  ensureDir,
  ensureSeeded,
  joinPath,
  kvGet,
  kvSet,
  normalizePath,
  vfsCopy,
  vfsExportZip,
  vfsListAssetTree,
  vfsListDir,
  vfsListWorkspaceRel,
  vfsMkdir,
  vfsReadBytes,
  vfsReadText,
  vfsRemove,
  vfsRename,
  vfsWriteBytes,
} from "./vfs";
import {
  cancelMediaTasks,
  createNodeAiGenerateStreamResponse,
  deleteCustomMediaProvider,
  fetchRemoteProviderModels,
  getMediaTaskStatuses,
  listAiModels,
  listAllTextProviders,
  listMediaModelsByType,
  listMediaProviders,
  runSyncMediaGeneration,
  saveCustomMediaProvider,
  startAsyncMediaTask,
  type MediaProviderRecord,
} from "./aiGateway";
import {
  connectRemoteA2aAgent,
  createMcpControlSseResponse,
  getA2aSettings,
  getMcpSkillMarkdown,
  handleMcpControlCall,
  listAgentTeams,
  listControlConnections,
  listMcpServers,
  removeMcpServer,
  saveA2aSettings,
  saveAgentTeam,
  saveMcpServer,
  setAgentTeamEnabled,
  setMcpServerEnabled,
  testMcpServer,
  uninstallAgentTeam,
  type StoredAgentTeam,
} from "./mcpAndA2a";
import {
  getExploreProjectById,
  getNodeSourceCode,
  installFromMarketOrFile,
  listAllSkills,
  listExploreProjects,
  listInstalledExtensions,
  listInstalledNodes,
  listInstalledTools,
  listMarketPlugins,
  publishLocalMarketPlugin,
  savePluginConfig,
  saveSkillContent,
  setPluginEnabled,
  toggleMarketCollection,
  uninstallSkill,
} from "./pluginMarketRuntime";
import {
  connectPluginServer,
  createFfmpegEventsSse,
  createLocalModelsSse,
  createPluginServerEventsSse,
  getBrowserFfmpegStatus,
  getBrowserLocalModelsStatus,
  handleBrowserFfmpegExecute,
  handleLocalModelMediaProcess,
  handlePluginServerMessage,
} from "./browserMedia";
import {
  answerAgentQuestion,
  controlSingleAgent,
  createAgentConversation,
  createSingleAgentSession,
  getAgentSession,
  listAgentSessions,
  renameAgentSession,
  runSingleAgentStream,
  runWorkspaceAgentMessage,
  stopAgentRun,
  submitSingleAgentToolResult,
} from "./agentRuntime";

const SETTINGS_KV_KEY = "toonflow.settings";
let settingsRevision = 1;
let installed = false;
let nativeFetch: typeof fetch;

function ok(data: unknown = null, message = "ok", extraHeaders?: Record<string, string>): Response {
  return new Response(JSON.stringify({ code: 200, data, message }), {
    status: 200,
    headers: { "Content-Type": "application/json; charset=utf-8", ...(extraHeaders ?? {}) },
  });
}

function fail(status: number, message: string, data: unknown = null): Response {
  return new Response(JSON.stringify({ code: status, data, message }), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}

function isToonflowInternalApi(url: URL): boolean {
  if (url.origin !== window.location.origin) return false;
  const p = url.pathname;
  if (!p.startsWith("/api/")) return false;
  // Pass real sub2api backend routes through untouched
  if (p.startsWith("/api/v1/") || p.startsWith("/api/v2/") || p.startsWith("/api/v3/")) return false;
  return true;
}

async function readJsonBody(body: BodyInit | null | undefined): Promise<Record<string, unknown>> {
  if (!body) return {};
  if (typeof body === "string") {
    try {
      return JSON.parse(body) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  if (body instanceof Uint8Array || body instanceof ArrayBuffer) {
    const text = new TextDecoder().decode(body instanceof Uint8Array ? body : new Uint8Array(body));
    try {
      return JSON.parse(text) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  if (body instanceof Blob) {
    const text = await body.text();
    try {
      return JSON.parse(text) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  return {};
}

async function readRawBytes(body: BodyInit | null | undefined): Promise<Uint8Array> {
  if (!body) return new Uint8Array();
  if (typeof body === "string") return new TextEncoder().encode(body);
  if (body instanceof Uint8Array) return body;
  if (body instanceof ArrayBuffer) return new Uint8Array(body);
  if (body instanceof Blob) return new Uint8Array(await body.arrayBuffer());
  if (body instanceof FormData) {
    const file = body.get("file");
    if (file instanceof Blob) return new Uint8Array(await file.arrayBuffer());
  }
  return new Uint8Array();
}

async function routeToonflowApi(
  url: URL,
  method: string,
  headers: Headers,
  rawBody: BodyInit | null | undefined,
  signal?: AbortSignal
): Promise<Response> {
  await ensureSeeded();
  const path = url.pathname;
  const q = url.searchParams;
  const m = method.toUpperCase();

  try {
    // -------------------- Session & Hello --------------------
    if (path === "/api/multiTenant/user/session") {
      const host = getHostContext();
      return ok({
        enabled: false,
        publicWeb: false,
        host: true,
        registrationEnabled: false,
        user: host.user
          ? {
              id: String(host.user.id),
              username: host.user.username || host.user.email || "creator",
              role: "admin",
            }
          : null,
      });
    }
    if (path === "/api/multiTenant/enabled") return ok({ enabled: false });
    if (path === "/api/multiTenant/status" || path === "/api/multiTenant/state") {
      return ok({ enabled: false, initialized: true });
    }
    if (path === "/api/hello" || path === "/api/hello/status") {
      return ok({ completed: true });
    }

    // -------------------- Settings & Personalization --------------------
    if (path === "/api/settings/get") {
      const host = getHostContext();
      const saved = await kvGet<Record<string, unknown>>(SETTINGS_KV_KEY, {});
      const ui = (saved.ui && typeof saved.ui === "object" ? saved.ui : {}) as Record<string, unknown>;
      const stores = (saved.stores && typeof saved.stores === "object" ? saved.stores : {}) as Record<string, unknown>;
      // Seed starter workspace & 3D scene list on first launch if empty
      if (!stores["toonflow.projectList"]) {
        stores["toonflow.projectList"] = {
          project: { directory: DEFAULT_WORKSPACE_DIR, name: "雨夜便利店·短剧分镜", lastOpenedAt: Date.now() },
          projectList: [{ directory: DEFAULT_WORKSPACE_DIR, name: "雨夜便利店·短剧分镜", lastOpenedAt: Date.now() }],
          sceneList: [{ directory: DEFAULT_SCENE_DIR, name: "雨夜街角·白模片场", lastOpenedAt: Date.now() }],
        };
      }
      const savedMcp = (saved.mcp && typeof saved.mcp === "object" ? saved.mcp : {}) as Record<string, unknown>;
      const merged = {
        ...saved,
        helloCompleted: true,
        multiTenantEnabled: false,
        mcp: {
          enabled: true,
          port: 3000,
          token: "sub2api-browser-mcp",
          ...savedMcp,
        },
        ui: {
          theme: host.theme || "dark",
          language: host.locale || "zh-CN",
          primaryColor: "#409eff",
          fontScale: 100,
          mobileScale: 75,
          radius: 8,
          startupAnimation: false,
          ...ui,
        },
        stores,
        // Pre-populate virtual creator account so Plugin Market & Account panels work seamlessly
        toonflowAccount: saved.toonflowAccount || {
          token: "sub2api-browser-session",
          info: {
            userName: host.user?.username || host.user?.email || "Sub2API 创作者",
            phone: host.user?.email || "",
          },
        },
        toonflowAccountToken: saved.toonflowAccountToken || "sub2api-browser-session",
        toonflowAccountInfo: saved.toonflowAccountInfo || {
          userName: host.user?.username || host.user?.email || "Sub2API 创作者",
          phone: host.user?.email || "",
        },
        aiModels: await listAiModels(),
      };
      return ok(merged, "ok", { "X-Toonflow-Settings-Revision": String(settingsRevision) });
    }

    if (path === "/api/settings/save") {
      const body = await readJsonBody(rawBody);
      const nextSettings = (body.settings && typeof body.settings === "object"
        ? body.settings
        : {}) as Record<string, unknown>;
      await kvSet(SETTINGS_KV_KEY, nextSettings);
      settingsRevision++;
      return ok(
        {
          customProviders: nextSettings.customProviders,
          mediaProviderConfigs: nextSettings.mediaProviderConfigs,
          toonflowProvider: nextSettings.toonflowProvider ?? null,
          nodeMenu: nextSettings.nodeMenu ?? null,
          aiModels: await listAiModels(),
          multiTenantEnabled: false,
        },
        "设置已保存",
        { "X-Toonflow-Settings-Revision": String(settingsRevision) }
      );
    }

    if (path === "/api/settings/personalization/get") {
      const doc = q.get("document") || "agents";
      const content = await kvGet<string>(`toonflow.personalization.${doc}`, "");
      return ok({ document: doc, content });
    }
    if (path === "/api/settings/personalization/save") {
      const body = await readJsonBody(rawBody);
      const doc = String(body.document || "agents");
      await kvSet(`toonflow.personalization.${doc}`, String(body.content || ""));
      return ok({ document: doc });
    }
    if (path === "/api/settings/systemPrompt") {
      return ok({ prompt: "你是 Toonflow AI 创作助手，运行在用户的浏览器本机工作区中。" });
    }

    // -------------------- Workspaces & Virtual Filesystem --------------------
    if (path === "/api/workspaces/check") {
      const dir = normalizePath(q.get("directory") || DEFAULT_WORKSPACE_DIR);
      await ensureDir(dir);
      return ok({ directory: dir });
    }
    if (path === "/api/workspaces/selectDirectory") {
      // Return native: false so Toonflow opens its built-in virtual directory picker modal!
      return ok({ native: false, directory: null });
    }
    if (path === "/api/workspaces/list") {
      const target = normalizePath(q.get("path") || WORKSPACES_ROOT);
      await ensureDir(target);
      const entries = await vfsListDir(target);
      const parentPath = target === "/" ? null : dirnamePath(target);
      return ok({
        path: target,
        absolutePath: target,
        parentPath,
        roots: [
          { name: "创作工作区 (浏览器本机)", path: WORKSPACES_ROOT },
          { name: "3D 白模片场 (浏览器本机)", path: SCENES_ROOT },
        ],
        entries: entries.filter((e) => e.type === "directory"),
      });
    }
    if (path === "/api/workspaces/clone") {
      const body = await readJsonBody(rawBody);
      const name = String(body.name || `项目-${Date.now().toString(36)}`).replace(/[\\/]/g, "-");
      const baseDir = normalizePath(String(body.directory || WORKSPACES_ROOT));
      const targetDir = joinPath(baseDir, name);
      await ensureDir(targetDir);
      const projId = String(body.url || "").replace(/^explore:\/\//, "");
      const explore = getExploreProjectById(projId);
      await vfsWriteBytes(
        joinPath(targetDir, "主画布.json"),
        JSON.stringify({ toonflowCanvas: true, ...explore.process, viewport: { x: 80, y: 60, zoom: 0.85 } }, null, 2)
      );
      return ok({ directory: targetDir });
    }

    if (path === "/api/workspaces/files/list") {
      const dir = q.get("directory") || DEFAULT_WORKSPACE_DIR;
      const sub = q.get("path") || "";
      return ok(await vfsListWorkspaceRel(dir, sub));
    }
    if (path === "/api/workspaces/files/read") {
      const dir = q.get("directory") || DEFAULT_WORKSPACE_DIR;
      const rel = q.get("path") || "";
      const fullPath = joinPath(dir, rel);
      if (q.get("snapshot") === "true") {
        const snap = await vfsReadText(fullPath);
        return ok(snap);
      }
      const { bytes, mimeType } = await vfsReadBytes(fullPath);
      const rangeHeader = headers.get("range");
      if (rangeHeader) {
        const match = /^bytes=(\d+)-(\d*)$/.exec(rangeHeader);
        if (match) {
          if (bytes.byteLength === 0) {
            return new Response(null, { status: 416, headers: { "Content-Range": "bytes */0" } });
          }
          const start = Number(match[1]);
          const end = match[2] ? Math.min(bytes.byteLength - 1, Number(match[2])) : bytes.byteLength - 1;
          const slice = bytes.subarray(start, end + 1);
          const sliceCopy = new Uint8Array(slice.byteLength);
          sliceCopy.set(slice);
          return new Response(sliceCopy, {
            status: 206,
            headers: {
              "Content-Type": mimeType,
              "Content-Range": `bytes ${start}-${end}/${bytes.byteLength}`,
            },
          });
        }
      }
      const copy = new Uint8Array(bytes.byteLength);
      copy.set(bytes);
      return new Response(copy, { status: 200, headers: { "Content-Type": mimeType } });
    }
    if (path === "/api/workspaces/files/write") {
      const dir = q.get("directory") || DEFAULT_WORKSPACE_DIR;
      const rel = q.get("path") || "";
      const exclusive = q.get("exclusive") === "true";
      const expectedRevision = q.get("expectedRevision") || undefined;
      const bytes = await readRawBytes(rawBody);
      const res = await vfsWriteBytes(joinPath(dir, rel), bytes, { exclusive, expectedRevision });
      return ok({ revision: res.revision, path: rel });
    }
    if (path === "/api/workspaces/files/mkdir") {
      const body = await readJsonBody(rawBody);
      await vfsMkdir(joinPath(String(body.directory || DEFAULT_WORKSPACE_DIR), String(body.path || "")));
      return ok();
    }
    if (path === "/api/workspaces/files/rename") {
      const body = await readJsonBody(rawBody);
      const dir = String(body.directory || DEFAULT_WORKSPACE_DIR);
      await vfsRename(joinPath(dir, String(body.path || "")), joinPath(dir, String(body.target || "")));
      return ok();
    }
    if (path === "/api/workspaces/files/copy") {
      const body = await readJsonBody(rawBody);
      const dir = String(body.directory || DEFAULT_WORKSPACE_DIR);
      await vfsCopy(joinPath(dir, String(body.path || "")), joinPath(dir, String(body.target || "")));
      return ok();
    }
    if (path === "/api/workspaces/files/remove") {
      const body = await readJsonBody(rawBody);
      const dir = String(body.directory || DEFAULT_WORKSPACE_DIR);
      await vfsRemove(joinPath(dir, String(body.path || "")), body.recursive !== false);
      return ok();
    }
    if (path === "/api/workspaces/files/export") {
      const dir = q.get("directory") || DEFAULT_WORKSPACE_DIR;
      const zip = await vfsExportZip(dir);
      const zipCopy = new Uint8Array(zip.byteLength);
      zipCopy.set(zip);
      return new Response(zipCopy, { status: 200, headers: { "Content-Type": "application/zip" } });
    }
    if (path === "/api/workspaces/files/importImage") {
      const body = await readJsonBody(rawBody);
      const dir = String(body.directory || DEFAULT_WORKSPACE_DIR);
      const imageUrl = String(body.url || "");
      const resp = await nativeFetch(imageUrl, { signal });
      if (!resp.ok) throw new Error(`拉取图片失败（HTTP ${resp.status}）`);
      const mimeType = resp.headers.get("content-type") || "image/png";
      const ext = mimeType.includes("webp") ? "webp" : mimeType.includes("jpeg") ? "jpg" : "png";
      const rel = `assets/import-${Date.now()}.${ext}`;
      await vfsWriteBytes(joinPath(dir, rel), new Uint8Array(await resp.arrayBuffer()), { mimeType });
      return ok({ path: rel, mimeType });
    }
    if (path === "/api/workspaces/files/reveal") {
      return ok({ revealed: false, message: "浏览器本机工作区支持导出 ZIP 打包下载查看。" });
    }

    // -------------------- Global Assets Library --------------------
    if (path === "/api/assets/list") {
      const sub = q.get("path") || "";
      return ok({ entries: await vfsListAssetTree(sub) });
    }
    if (path === "/api/assets/mkdir") {
      const body = await readJsonBody(rawBody);
      await vfsMkdir(joinPath(ASSETS_ROOT, String(body.path || "")), { exclusive: true });
      return ok();
    }
    if (path === "/api/assets/read") {
      const rel = q.get("path") || "";
      const { bytes, mimeType } = await vfsReadBytes(joinPath(ASSETS_ROOT, rel));
      const copy = new Uint8Array(bytes.byteLength);
      copy.set(bytes);
      return new Response(copy, { status: 200, headers: { "Content-Type": mimeType } });
    }
    if (path === "/api/assets/save") {
      const rel = q.get("path") || "";
      const bytes = await readRawBytes(rawBody);
      await vfsWriteBytes(joinPath(ASSETS_ROOT, rel), bytes);
      return ok({ path: rel });
    }
    if (path === "/api/assets/rename") {
      const body = await readJsonBody(rawBody);
      await vfsRename(joinPath(ASSETS_ROOT, String(body.path || "")), joinPath(ASSETS_ROOT, String(body.target || "")));
      return ok();
    }
    if (path === "/api/assets/remove") {
      const body = await readJsonBody(rawBody);
      await vfsRemove(joinPath(ASSETS_ROOT, String(body.path || "")), true);
      return ok();
    }
    if (path === "/api/media/preview") {
      const dir = q.get("directory") || ASSETS_ROOT;
      const rel = q.get("path") || "";
      const { bytes, mimeType } = await vfsReadBytes(joinPath(dir, rel));
      const copy = new Uint8Array(bytes.byteLength);
      copy.set(bytes);
      return new Response(copy, { status: 200, headers: { "Content-Type": mimeType } });
    }

    // -------------------- Nodes, Extensions, Tools, Skills --------------------
    if (path === "/api/nodes/get") return ok(await listInstalledNodes());
    if (path === "/api/nodes/files") {
      const name = q.get("name") || "";
      const code = await getNodeSourceCode(name);
      if (code === null) return fail(404, `节点 ${name} 由浏览器 ESM 原生加载`);
      return new Response(code, { status: 200, headers: { "Content-Type": "text/javascript; charset=utf-8" } });
    }
    if (path === "/api/nodes/setEnabled") {
      const body = await readJsonBody(rawBody);
      await setPluginEnabled("node", String(body.name || ""), Boolean(body.enabled));
      return ok();
    }
    if (path === "/api/nodes/saveConfig") {
      const body = await readJsonBody(rawBody);
      await savePluginConfig("node", String(body.name || ""), (body.config as Record<string, unknown>) ?? {});
      return ok();
    }
    if (path === "/api/ext/list") return ok(await listInstalledExtensions());
    if (path === "/api/ext/save") {
      const body = await readJsonBody(rawBody);
      await savePluginConfig("ext", String(body.name || ""), (body.config as Record<string, unknown>) ?? {});
      return ok();
    }
    if (path === "/api/ext/setEnabled") {
      const body = await readJsonBody(rawBody);
      await setPluginEnabled("ext", String(body.name || ""), Boolean(body.enabled));
      return ok();
    }
    if (path === "/api/tools/get") return ok(await listInstalledTools());
    if (path === "/api/tools/setEnabled") {
      const body = await readJsonBody(rawBody);
      await setPluginEnabled("tool", String(body.name || ""), Boolean(body.enabled));
      return ok();
    }

    if (path === "/api/agent/skills") {
      const skills = (await listAllSkills()).filter((s) => s.enabled);
      return ok(skills.map((s) => ({ name: s.name, displayName: s.displayName, description: s.description })));
    }
    if (path === "/api/skills/get") return ok(await listAllSkills());
    if (path === "/api/skills/list") {
      const name = q.get("name") || "";
      const skills = await listAllSkills();
      const found = skills.find((s) => s.name === name);
      const files = Object.keys(found?.files ?? { "SKILL.md": "" });
      return ok({ mainPath: "SKILL.md", files: files.map((f) => ({ name: f, path: f, type: "file" })) });
    }
    if (path === "/api/skills/read") {
      const name = q.get("name") || "";
      const skills = await listAllSkills();
      const found = skills.find((s) => s.name === name);
      if (!found) return fail(404, `未找到技能：${name}`);
      return ok({ name: found.name, content: found.content, path: "SKILL.md" });
    }
    if (path === "/api/skills/create" || path === "/api/skills/save") {
      const body = await readJsonBody(rawBody);
      const saved = await saveSkillContent(
        String(body.name || `skill-${Date.now()}`),
        String(body.content || ""),
        typeof body.displayName === "string" ? body.displayName : undefined,
        typeof body.description === "string" ? body.description : undefined
      );
      return ok(saved);
    }
    if (path === "/api/skills/setEnabled") {
      const body = await readJsonBody(rawBody);
      await setPluginEnabled("skill", String(body.name || ""), Boolean(body.enabled));
      return ok();
    }
    if (path === "/api/skills/uninstall" || path === "/api/skills/remove") {
      const body = await readJsonBody(rawBody);
      await uninstallSkill(String(body.name || q.get("name") || ""));
      return ok();
    }

    // -------------------- AI Models & Media Providers --------------------
    if (path === "/api/ai/models") return ok(await listAiModels());
    if (path === "/api/ai/generate") {
      const body = await readJsonBody(rawBody);
      return createNodeAiGenerateStreamResponse(nativeFetch, body as never, signal);
    }
    if (path === "/api/providers/models") {
      const body = await readJsonBody(rawBody);
      return ok(await fetchRemoteProviderModels(nativeFetch, body as never));
    }
    if (path === "/api/channel/provider/getSource") {
      return ok({ language: "sub2api-gateway", media: "sub2api-media" });
    }
    if (path === "/api/providers/account/install") {
      const providers = await listAllTextProviders();
      const base = providers[0];
      return ok({
        provider: {
          id: "Toonflow",
          label: base?.label || "Sub2API 站内网关",
          apiUrl: base?.apiUrl || `${window.location.origin}/v1`,
          apiKey: base?.apiKey || "",
          protocol: base?.protocol || "openai-completions",
          models: base?.models ?? [],
        },
      });
    }
    if (path === "/api/providers/builtin/list") {
      return ok([
        {
          id: "sub2api-gateway",
          name: "Sub2API 站内网关",
          description: "自动同步当前账号的 Sub2API 站内密钥与模型列表。",
          updateTime: Date.now(),
          version: "2.0.0",
        },
      ]);
    }
    if (path === "/api/providers/media/list") return ok(await listMediaProviders());
    if (path === "/api/providers/media/models" || path === "/api/ai/media/models") {
      return ok(await listMediaModelsByType(q.get("mediaType") || q.get("type") || undefined));
    }
    if (path === "/api/providers/media/save" || path === "/api/providers/media/add") {
      const body = await readJsonBody(rawBody);
      const rec: MediaProviderRecord = {
        id: String(body.id || `media-${Date.now()}`),
        name: String(body.name || "自定义媒体供应商"),
        description: String(body.description || "浏览器直连媒体供应商"),
        enabled: body.enabled !== false,
        apiUrl: typeof body.apiUrl === "string" ? body.apiUrl : undefined,
        apiKey: typeof body.apiKey === "string" ? body.apiKey : undefined,
        models: Array.isArray(body.models) ? (body.models as MediaProviderRecord["models"]) : [],
      };
      return ok(await saveCustomMediaProvider(rec));
    }
    if (path === "/api/providers/media/delete") {
      const body = await readJsonBody(rawBody);
      return ok(await deleteCustomMediaProvider(String(body.id || q.get("id") || "")));
    }
    if (path === "/api/providers/media/validate") return ok({ valid: true });

    if (path === "/api/ai/media/price") {
      return ok({ price: "按 Sub2API 站内密钥费率计费", tips: "直接走你的 Sub2API 站内 API 网关" });
    }
    if (path === "/api/ai/media/balance") {
      const host = getHostContext();
      return ok({ balance: host.user?.balance ?? 0, currency: "USD" });
    }
    if (path === "/api/ai/media/balanceEvents") {
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(": connected\n\n"));
        },
      });
      return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream" } });
    }
    if (path === "/api/ai/media/generate") {
      const body = await readJsonBody(rawBody);
      if (body.taskId) {
        return ok(startAsyncMediaTask(nativeFetch, body as never));
      }
      const files = await runSyncMediaGeneration(nativeFetch, body as never, signal);
      return ok(files);
    }
    if (path === "/api/ai/media/tasks/status") {
      const body = await readJsonBody(rawBody);
      return ok(getMediaTaskStatuses(Array.isArray(body.taskIds) ? (body.taskIds as string[]) : []));
    }
    if (path === "/api/ai/media/tasks/cancel") {
      const body = await readJsonBody(rawBody);
      cancelMediaTasks(Array.isArray(body.taskIds) ? (body.taskIds as string[]) : []);
      return ok();
    }

    // -------------------- AI Assistant & SingleAgent (TrumanMap) --------------------
    if (path === "/api/agent/create") {
      const body = await readJsonBody(rawBody);
      return ok(await createAgentConversation(String(body.directory || DEFAULT_WORKSPACE_DIR)));
    }
    if (path === "/api/agent/list") {
      return ok(await listAgentSessions(q.get("directory") || DEFAULT_WORKSPACE_DIR));
    }
    if (path === "/api/agent/get") {
      return ok(await getAgentSession(q.get("directory") || DEFAULT_WORKSPACE_DIR, q.get("sessionFile") || ""));
    }
    if (path === "/api/agent/rename") {
      const body = await readJsonBody(rawBody);
      return ok(await renameAgentSession(String(body.directory || DEFAULT_WORKSPACE_DIR), String(body.sessionFile || ""), String(body.name || "")));
    }
    if (path === "/api/agent/message") {
      const body = await readJsonBody(rawBody);
      return runWorkspaceAgentMessage(nativeFetch, body as never, signal);
    }
    if (path === "/api/agent/stop") {
      const body = await readJsonBody(rawBody);
      stopAgentRun(String(body.directory || DEFAULT_WORKSPACE_DIR), typeof body.sessionFile === "string" ? body.sessionFile : undefined);
      return ok();
    }
    if (path === "/api/agent/answer") {
      const body = await readJsonBody(rawBody);
      return ok(answerAgentQuestion(String(body.callId || ""), body));
    }
    if (path === "/api/agent/automations/list") return ok([]);
    if (path === "/api/agent/automations/update") return ok({ ok: true });
    if (path === "/api/agent/command" || path === "/api/agent/queue" || path === "/api/agent/canvasResult") {
      return ok({ ok: true });
    }
    if (path === "/api/agent/mentionSource") return ok([]);

    if (path === "/api/singleAgent/create") {
      const body = await readJsonBody(rawBody);
      return ok(createSingleAgentSession(body as never));
    }
    if (path === "/api/singleAgent/run") {
      const body = await readJsonBody(rawBody);
      return runSingleAgentStream(nativeFetch, body as never, signal);
    }
    if (path === "/api/singleAgent/toolResult") {
      const body = await readJsonBody(rawBody);
      return ok(submitSingleAgentToolResult(body as never));
    }
    if (path === "/api/singleAgent/control") {
      const body = await readJsonBody(rawBody);
      return ok(controlSingleAgent(body as never));
    }
    if (path === "/api/singleAgent/answer") {
      const body = await readJsonBody(rawBody);
      return ok(answerAgentQuestion(String(body.callId || ""), body));
    }

    // -------------------- MCP & A2A --------------------
    if (path === "/api/mcp/status") {
      const settings = await kvGet<Record<string, unknown>>(SETTINGS_KV_KEY, {});
      const mcp = (settings.mcp && typeof settings.mcp === "object" ? settings.mcp : {}) as Record<string, unknown>;
      return ok({
        enabled: mcp.enabled !== false,
        running: true,
        preferredPort: Number(mcp.port || 3000),
        port: Number(mcp.port || 3000),
        endpoint: `${window.location.origin}/mcp`,
        connections: listControlConnections(),
        browserNative: true,
      });
    }
    if (path === "/api/mcp/skill") {
      return new Response(getMcpSkillMarkdown(), {
        status: 200,
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
      });
    }
    if (path === "/api/mcp/control/events") {
      return createMcpControlSseResponse(q.get("connectionId") || crypto.randomUUID(), signal);
    }
    if (path.startsWith("/api/mcp/control/")) {
      const sub = path.slice("/api/mcp/control/".length);
      const body = await readJsonBody(rawBody);
      return ok(handleMcpControlCall(sub, body));
    }
    if (path === "/api/mcp/servers/list") {
      return ok({
        servers: await listMcpServers(),
        canManage: true,
        localSupported: false,
      });
    }
    if (path === "/api/mcp/servers/save") {
      const body = await readJsonBody(rawBody);
      return ok(await saveMcpServer((body.server ?? body) as never), "MCP 配置已保存");
    }
    if (path === "/api/mcp/servers/remove") {
      const body = await readJsonBody(rawBody);
      await removeMcpServer(String(body.id || q.get("id") || ""));
      return ok(null, "MCP 服务已移除");
    }
    if (path === "/api/mcp/servers/setEnabled") {
      const body = await readJsonBody(rawBody);
      return ok(await setMcpServerEnabled(String(body.id || ""), Boolean(body.enabled)));
    }
    if (path === "/api/mcp/servers/test") {
      const body = await readJsonBody(rawBody);
      return ok(await testMcpServer(nativeFetch, (body.server ?? body) as never), "MCP 连接成功");
    }

    if (path === "/api/agents/get") {
      return ok({ agents: await listAgentTeams(), canManage: true });
    }
    if (path === "/api/agents/a2a/get") return ok(await getA2aSettings());
    if (path === "/api/agents/a2a/save") {
      const body = await readJsonBody(rawBody);
      return ok(await saveA2aSettings(body as never), "A2A 设置已保存");
    }
    if (path === "/api/agents/connect") {
      const body = await readJsonBody(rawBody);
      return ok(await connectRemoteA2aAgent(nativeFetch, body as never), "远端团队已连接");
    }
    if (path === "/api/agents/read") {
      const name = q.get("name") || "";
      const list = await listAgentTeams();
      const team = list.find((t) => t.name === name);
      if (!team) return fail(404, `未找到 Agent 团队：${name}`);
      return ok(team);
    }
    if (path === "/api/agents/save") {
      const body = await readJsonBody(rawBody);
      const saved = await saveAgentTeam({
        name: String(body.name || `agent-${Date.now()}`),
        displayName: String(body.displayName || body.name || "自定义团队"),
        description: String(body.description || ""),
        version: String(body.version || "1.0.0"),
        author: String(body.author || "User"),
        kind: "local",
        enabled: true,
        files: (body.files as Record<string, string>) ?? {},
        installedAt: Date.now(),
      } satisfies StoredAgentTeam);
      return ok(saved);
    }
    if (path === "/api/agents/setEnabled") {
      const body = await readJsonBody(rawBody);
      await setAgentTeamEnabled(String(body.name || ""), Boolean(body.enabled));
      return ok();
    }
    if (path === "/api/agents/uninstall") {
      const body = await readJsonBody(rawBody);
      await uninstallAgentTeam(String(body.name || q.get("name") || ""));
      return ok();
    }

    // -------------------- Plugin Market, Explore & Channel --------------------
    if (path === "/api/channel/plugin/getPlugins") {
      const body = m === "GET" ? Object.fromEntries(q.entries()) : await readJsonBody(rawBody);
      return ok(await listMarketPlugins(body as never));
    }
    if (path === "/api/channel/plugin/getPluginExistence") {
      const body = await readJsonBody(rawBody);
      const items = Array.isArray(body.plugins) ? (body.plugins as { type: string; identifier: string }[]) : [];
      return ok(items.map((item) => ({ ...item, exists: true })));
    }
    if (path === "/api/channel/plugin/toggleCollection") {
      const body = await readJsonBody(rawBody);
      return ok(await toggleMarketCollection(Number(body.pluginId ?? body.id ?? 0)));
    }
    if (path === "/api/channel/plugin/uploadPlugin") {
      const body = await readJsonBody(rawBody);
      return ok(await publishLocalMarketPlugin(body as never));
    }
    if (path === "/api/channel/plugin/deletePlugin") return ok();
    if (path === "/api/desktop/plugins/install" || path === "/api/plugins/install") {
      const body = await readJsonBody(rawBody);
      return ok(await installFromMarketOrFile(body as never), "插件已安装到浏览器本机");
    }
    if (path === "/api/plugins/export") {
      const payload = JSON.stringify({ exportedAt: new Date().toISOString(), source: "toonflow-web" }, null, 2);
      return new Response(payload, { status: 200, headers: { "Content-Type": "application/json" } });
    }

    if (path === "/api/channel/community/getAdvertisements") {
      return ok({ banner: null, banners: [], sidebar: [], bannerMode: "carousel", bannerInterval: 5000 });
    }
    if (path === "/api/channel/community/getSponsorList") return ok([]);
    if (path === "/api/channel/community/getExploreProjects") {
      const body = m === "GET" ? Object.fromEntries(q.entries()) : await readJsonBody(rawBody);
      return ok(listExploreProjects(body as never));
    }
    if (path === "/api/channel/community/getExploreProject") {
      const body = await readJsonBody(rawBody);
      return ok(getExploreProjectById(String(body.id || body.projectId || q.get("id") || "")));
    }
    if (path === "/api/channel/community/getExploreProcess") {
      const body = await readJsonBody(rawBody);
      const proj = getExploreProjectById(String(body.id || body.projectId || q.get("id") || ""));
      return ok({ canvases: proj.canvases });
    }
    if (path === "/api/channel/community/getExploreDownload") {
      const body = await readJsonBody(rawBody);
      const p = getExploreProjectById(String(body.id || body.projectId || q.get("id") || ""));
      return ok({ downloadUrl: `explore://${p.id}`, url: `explore://${p.id}`, name: p.title });
    }
    if (path === "/api/channel/account/getPreviewData") {
      const host = getHostContext();
      return ok({
        totalPoints: host.user?.balance ?? 0,
        bonusBalance: 0,
        totalConsumption: 0,
        daily: [],
      });
    }
    if (path === "/api/channel/model/getModelData") return ok([]);

    // -------------------- Browser FFmpeg, LocalModels, PluginServer --------------------
    if (path === "/api/ffmpeg/status") return ok(getBrowserFfmpegStatus());
    if (path === "/api/ffmpeg/events") return createFfmpegEventsSse(signal);
    if (path === "/api/ffmpeg/execute") {
      const body = await readJsonBody(rawBody);
      return handleBrowserFfmpegExecute(body as never);
    }
    if (path.startsWith("/api/ffmpeg/")) return ok(getBrowserFfmpegStatus());

    if (path === "/api/localModels/status") {
      if (headers.get("accept")?.includes("text/event-stream")) {
        return createLocalModelsSse(signal);
      }
      return ok(getBrowserLocalModelsStatus());
    }
    if (path === "/api/localModels/processImage") {
      return handleLocalModelMediaProcess("image", (await readJsonBody(rawBody)) as never);
    }
    if (path === "/api/localModels/processVideo") {
      return handleLocalModelMediaProcess("video", (await readJsonBody(rawBody)) as never);
    }
    if (path === "/api/localModels/processAudio") {
      return handleLocalModelMediaProcess("audio", (await readJsonBody(rawBody)) as never);
    }

    if (path === "/api/pluginServer/connect") return ok(connectPluginServer());
    if (path === "/api/pluginServer/events") {
      return createPluginServerEventsSse(q.get("connectionId") || crypto.randomUUID(), signal);
    }
    if (path === "/api/pluginServer/message") {
      return ok(handlePluginServerMessage((await readJsonBody(rawBody)) as never));
    }

    // -------------------- Graceful Desktop / Mobile / Telemetry Stubs --------------------
    if (path.startsWith("/api/desktop/")) {
      if (path === "/api/desktop/clipboard/write") {
        const body = await readJsonBody(rawBody);
        if (typeof body.text === "string" && navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(body.text).catch(() => {});
        }
        return ok();
      }
      if (path === "/api/desktop/clipboard/read") {
        const text = (await navigator.clipboard?.readText?.().catch(() => "")) || "";
        return ok({ text, image: null, files: [] });
      }
      return ok({ ready: true, supported: false });
    }
    if (path.startsWith("/api/mobileLink/") || path.startsWith("/api/connection/")) {
      return ok({ enabled: false, connected: false, devices: [] });
    }

    // Default safe JSON response for any auxiliary telemetry/channel endpoint
    return ok(null);
  } catch (err) {
    const e = err as { status?: number; code?: string; message?: string };
    return fail(e.status || 500, e.message || "浏览器本机运行时处理失败", e.code ? { code: e.code } : null);
  }
}

async function responseToAxios(config: InternalAxiosRequestConfig, resp: Response): Promise<AxiosResponse> {
  const responseType = config.responseType || "json";
  let data: unknown;
  if (responseType === "arraybuffer") {
    data = await resp.arrayBuffer();
  } else if (responseType === "blob") {
    data = await resp.blob();
  } else if (responseType === "text") {
    data = await resp.text();
  } else {
    const text = await resp.text();
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = text;
    }
  }
  const respHeaders: Record<string, string> = {};
  resp.headers.forEach((v, k) => {
    respHeaders[k] = v;
  });
  const axiosResp: AxiosResponse = {
    data,
    status: resp.status,
    statusText: resp.statusText,
    headers: respHeaders,
    config,
  };
  if (resp.status >= 400) {
    throw new axios.AxiosError(
      (data && typeof data === "object" && "message" in data ? String((data as { message?: string }).message) : null) ||
        `Request failed with status code ${resp.status}`,
      axios.AxiosError.ERR_BAD_REQUEST,
      config,
      null,
      axiosResp
    );
  }
  return axiosResp;
}

function buildAxiosUrl(config: InternalAxiosRequestConfig): URL {
  const base = config.baseURL || "";
  const raw = config.url || "";
  const full = raw.startsWith("http://") || raw.startsWith("https://")
    ? raw
    : base.startsWith("http://") || base.startsWith("https://")
      ? `${base.replace(/\/+$/, "")}/${raw.replace(/^\/+/, "")}`
      : `${window.location.origin}${base.replace(/\/+$/, "")}${raw.startsWith("/") ? raw : `/${raw}`}`;
  const url = new URL(full);
  if (config.params && typeof config.params === "object") {
    for (const [k, v] of Object.entries(config.params as Record<string, unknown>)) {
      if (v !== undefined && v !== null) url.searchParams.set(k, String(v));
    }
  }
  return url;
}

export function installBrowserRuntimeInterceptor(): void {
  if (installed) return;
  installed = true;

  nativeFetch = window.fetch.bind(window);

  // 1. Intercept window.fetch for Toonflow internal /api/* endpoints
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const rawUrl =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
    const url = new URL(rawUrl, window.location.origin);
    if (!isToonflowInternalApi(url)) {
      return nativeFetch(input, init);
    }
    const method = init?.method || (input instanceof Request ? input.method : "GET");
    const hdrs = new Headers(input instanceof Request ? input.headers : undefined);
    if (init?.headers) {
      new Headers(init.headers).forEach((v, k) => hdrs.set(k, v));
    }
    const body = init?.body ?? (input instanceof Request && method !== "GET" && method !== "HEAD" ? await input.clone().arrayBuffer() : undefined);
    const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined);
    return routeToonflowApi(url, method, hdrs, body, signal ?? undefined);
  };

  // 2. Intercept Axios default adapter (applies to axios.* AND all axios.create() instances!)
  const defaultAdapter = axios.getAdapter(axios.defaults.adapter);
  axios.defaults.adapter = async (config: InternalAxiosRequestConfig): Promise<AxiosResponse> => {
    const url = buildAxiosUrl(config);
    if (!isToonflowInternalApi(url)) {
      return defaultAdapter(config);
    }
    const hdrs = new Headers();
    if (config.headers instanceof AxiosHeaders) {
      for (const [k, v] of Object.entries(config.headers.toJSON())) {
        if (v !== undefined && v !== null) hdrs.set(k, String(v));
      }
    } else if (config.headers && typeof config.headers === "object") {
      for (const [k, v] of Object.entries(config.headers as Record<string, unknown>)) {
        if (v !== undefined && v !== null) hdrs.set(k, String(v));
      }
    }
    let body: BodyInit | undefined;
    if (config.data !== undefined && config.data !== null) {
      if (
        typeof config.data === "string" ||
        config.data instanceof Blob ||
        config.data instanceof ArrayBuffer ||
        config.data instanceof Uint8Array ||
        config.data instanceof FormData
      ) {
        body = config.data as BodyInit;
      } else {
        body = JSON.stringify(config.data);
      }
    }
    const resp = await routeToonflowApi(url, config.method || "GET", hdrs, body, config.signal as AbortSignal | undefined);
    return responseToAxios(config, resp);
  };

  // 3. Intercept EventSource for /api/ffmpeg/events & /api/ai/media/balanceEvents
  const NativeEventSource = window.EventSource;
  if (typeof NativeEventSource === "function") {
    class BrowserEventSourceShim extends EventTarget {
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      static readonly CLOSED = 2;
      readonly CONNECTING = 0;
      readonly OPEN = 1;
      readonly CLOSED = 2;
      readyState = 1;
      url: string;
      withCredentials = false;
      onopen: ((ev: Event) => unknown) | null = null;
      onmessage: ((ev: MessageEvent) => unknown) | null = null;
      onerror: ((ev: Event) => unknown) | null = null;
      private inner?: EventSource;

      constructor(url: string | URL, init?: EventSourceInit) {
        super();
        const parsed = new URL(String(url), window.location.origin);
        this.url = parsed.href;
        if (!isToonflowInternalApi(parsed)) {
          this.inner = new NativeEventSource(url, init);
          return this.inner as unknown as BrowserEventSourceShim;
        }
        queueMicrotask(() => {
          const ev = new Event("open");
          this.onopen?.(ev);
          this.dispatchEvent(ev);
        });
      }

      close() {
        this.readyState = 2;
        this.inner?.close();
      }
    }
    (window as unknown as { EventSource: unknown }).EventSource = BrowserEventSourceShim;
  }
}

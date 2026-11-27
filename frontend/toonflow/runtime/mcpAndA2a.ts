import { kvGet, kvSet } from "./vfs";
import storyboardTeamJson from "../upstream/packages/teams/storyboardTeam/team.json";
import storyboardDirectorMd from "../upstream/packages/teams/storyboardTeam/members/director.md?raw";
import storyboardWriterMd from "../upstream/packages/teams/storyboardTeam/members/writer.md?raw";
import storyboardReviewerMd from "../upstream/packages/teams/storyboardTeam/members/reviewer.md?raw";
import storyboardContinuityMd from "../upstream/packages/teams/storyboardTeam/knowledge/continuity.md?raw";

// ==================== 1. Live Browser MCP Canvas Control Bus ====================

interface ControlConnection {
  connectionId: string;
  directory: string;
  canvasId?: string;
  connectedAt: number;
  push: (event: unknown) => void;
}

const controlConnections = new Map<string, ControlConnection>();
const pendingControlRequests = new Map<
  string,
  { resolve: (value: unknown) => void; reject: (err: Error) => void; timer: ReturnType<typeof setTimeout> }
>();

export function createMcpControlSseResponse(connectionId: string, signal?: AbortSignal): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enc = new TextEncoder();
      const send = (payload: unknown) => {
        try {
          controller.enqueue(enc.encode(`data: ${JSON.stringify(payload)}\n\n`));
        } catch {
          // stream closed
        }
      };
      const conn: ControlConnection = {
        connectionId,
        directory: "",
        connectedAt: Date.now(),
        push: send,
      };
      controlConnections.set(connectionId, conn);
      send({ type: "connected", connectionId });

      const keepalive = setInterval(() => {
        try {
          controller.enqueue(enc.encode(": keepalive\n\n"));
        } catch {
          clearInterval(keepalive);
        }
      }, 15000);

      signal?.addEventListener(
        "abort",
        () => {
          clearInterval(keepalive);
          controlConnections.delete(connectionId);
          try {
            controller.close();
          } catch {
            // ignore
          }
        },
        { once: true }
      );
    },
    cancel() {
      controlConnections.delete(connectionId);
    },
  });

  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store" },
  });
}

export function handleMcpControlCall(path: string, body: Record<string, unknown>) {
  const connectionId = String(body.connectionId ?? "");
  if (path === "state" || path === "register" || path === "heartbeat") {
    const conn = controlConnections.get(connectionId);
    if (conn) {
      const state = body.state && typeof body.state === "object" ? (body.state as Record<string, unknown>) : body;
      if (typeof state.directory === "string") conn.directory = state.directory;
      if (typeof state.canvasId === "string") conn.canvasId = state.canvasId;
    }
    return { ok: true, connectionId };
  }
  if (path === "unregister") {
    controlConnections.delete(connectionId);
    return { ok: true };
  }
  if (path === "respond" || path === "result") {
    const callId = String(body.callId ?? body.requestId ?? "");
    const pending = pendingControlRequests.get(callId);
    if (pending) {
      clearTimeout(pending.timer);
      pendingControlRequests.delete(callId);
      if (body.error) {
        pending.reject(new Error(typeof body.error === "string" ? body.error : JSON.stringify(body.error)));
      } else {
        pending.resolve(body.result ?? body.data ?? { ok: true });
      }
    }
    return { ok: true };
  }
  return { ok: true };
}

export async function dispatchCanvasControlCommand(
  directory: string,
  action: string,
  payload: Record<string, unknown>
): Promise<unknown> {
  const conn =
    [...controlConnections.values()].find((c) => c.directory === directory) ??
    [...controlConnections.values()][0];
  if (!conn) {
    throw new Error("当前没有处于活跃状态的画布连接，请先打开工作区画布。");
  }
  const callId = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pendingControlRequests.delete(callId);
      reject(new Error(`画布控制指令超时：${action}`));
    }, 15000);
    pendingControlRequests.set(callId, { resolve, reject, timer });
    conn.push({
      type: "call",
      callId,
      name: action,
      args: payload,
      directory: conn.directory || directory,
      canvasId: conn.canvasId,
    });
  });
}

export function listControlConnections() {
  return [...controlConnections.values()].map((c) => ({
    connectionId: c.connectionId,
    directory: c.directory,
    canvasId: c.canvasId ?? "",
    connectedAt: c.connectedAt,
  }));
}

// ==================== 2. Browser MCP Client (Streamable HTTP / SSE JSON-RPC) ====================

export interface StoredMcpServer {
  id: string;
  name: string;
  transport: "streamableHttp" | "sse" | "stdio";
  url?: string;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
  enabled: boolean;
  status?: "connected" | "disconnected" | "error";
  error?: string;
  toolCount?: number;
  tools?: { name: string; description?: string; inputSchema?: Record<string, unknown> }[];
}

const MCP_SERVERS_KEY = "toonflow.mcpServers.v1";

export async function listMcpServers(): Promise<StoredMcpServer[]> {
  return kvGet<StoredMcpServer[]>(MCP_SERVERS_KEY, []);
}

export async function saveMcpServer(server: Partial<StoredMcpServer>): Promise<StoredMcpServer> {
  const list = await listMcpServers();
  const id = server.id || crypto.randomUUID();
  const existing = list.find((s) => s.id === id);
  const next: StoredMcpServer = {
    id,
    name: server.name?.trim() || "未命名 MCP 服务",
    transport: server.transport === "sse" ? "sse" : server.transport === "stdio" ? "stdio" : "streamableHttp",
    url: server.url?.trim() || undefined,
    headers: server.headers,
    command: server.command,
    args: server.args,
    cwd: server.cwd,
    env: server.env,
    enabled: server.enabled ?? existing?.enabled ?? true,
    status: existing?.status ?? "disconnected",
    toolCount: existing?.toolCount ?? 0,
    tools: existing?.tools ?? [],
  };
  await kvSet(MCP_SERVERS_KEY, [next, ...list.filter((s) => s.id !== id)]);
  return next;
}

export async function removeMcpServer(id: string): Promise<void> {
  const list = await listMcpServers();
  await kvSet(
    MCP_SERVERS_KEY,
    list.filter((s) => s.id !== id)
  );
}

export async function setMcpServerEnabled(id: string, enabled: boolean): Promise<StoredMcpServer | undefined> {
  const list = await listMcpServers();
  const updated = list.map((s) => (s.id === id ? { ...s, enabled } : s));
  await kvSet(MCP_SERVERS_KEY, updated);
  return updated.find((s) => s.id === id);
}

async function mcpJsonRpcCall(
  rawFetch: typeof fetch,
  url: string,
  method: string,
  params: Record<string, unknown>,
  headers?: Record<string, string>,
  sessionId?: string
): Promise<{ result: unknown; sessionId?: string }> {
  const reqHeaders: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "application/json, text/event-stream",
    ...(headers ?? {}),
  };
  if (sessionId) reqHeaders["mcp-session-id"] = sessionId;

  const resp = await rawFetch(url, {
    method: "POST",
    headers: reqHeaders,
    body: JSON.stringify({ jsonrpc: "2.0", id: Date.now(), method, params }),
    signal: AbortSignal.timeout(15000),
  });
  if (!resp.ok) {
    const errBody = await resp.text().catch(() => "");
    throw new Error(`MCP 服务响应失败（HTTP ${resp.status}）：${errBody.slice(0, 200)}`);
  }
  const returnedSession = resp.headers.get("mcp-session-id") ?? sessionId;
  const ctype = resp.headers.get("content-type") ?? "";
  let parsed: { result?: unknown; error?: { message?: string } } | undefined;
  if (ctype.includes("text/event-stream")) {
    const raw = await resp.text();
    for (const line of raw.split("\n")) {
      if (line.startsWith("data:")) {
        try {
          const obj = JSON.parse(line.slice(5).trim()) as { result?: unknown; error?: { message?: string } };
          if (obj.result !== undefined || obj.error) parsed = obj;
        } catch {
          // continue
        }
      }
    }
  } else {
    parsed = (await resp.json()) as { result?: unknown; error?: { message?: string } };
  }
  if (parsed?.error) throw new Error(parsed.error.message || "MCP JSON-RPC 错误");
  return { result: parsed?.result, sessionId: returnedSession };
}

export async function testMcpServer(
  rawFetch: typeof fetch,
  server: Partial<StoredMcpServer>
): Promise<{ ok: boolean; toolCount: number; tools: { name: string; description?: string; inputSchema?: Record<string, unknown> }[] }> {
  if (server.transport === "stdio") {
    throw new Error("纯浏览器 Web 端不运行服务端本地 stdio 进程，请改用 Streamable HTTP 或 SSE 远程/本机 MCP 服务地址。");
  }
  if (!server.url) throw new Error("请填写有效的 MCP 服务地址（HTTP/HTTPS）");

  const init = await mcpJsonRpcCall(
    rawFetch,
    server.url,
    "initialize",
    {
      protocolVersion: "2025-03-26",
      capabilities: {},
      clientInfo: { name: "toonflow-web", version: "2.0.0" },
    },
    server.headers
  );

  // Best-effort initialized notification
  void rawFetch(server.url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(server.headers ?? {}),
      ...(init.sessionId ? { "mcp-session-id": init.sessionId } : {}),
    },
    body: JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }),
  }).catch(() => {});

  const listed = await mcpJsonRpcCall(rawFetch, server.url, "tools/list", {}, server.headers, init.sessionId);
  const tools = Array.isArray((listed.result as { tools?: unknown[] })?.tools)
    ? ((listed.result as { tools: { name: string; description?: string; inputSchema?: Record<string, unknown> }[] }).tools)
    : [];

  if (server.id) {
    const list = await listMcpServers();
    const updated = list.map((s) =>
      s.id === server.id ? { ...s, status: "connected" as const, error: undefined, toolCount: tools.length, tools } : s
    );
    await kvSet(MCP_SERVERS_KEY, updated);
  }
  return { ok: true, toolCount: tools.length, tools };
}

export async function callMcpTool(
  rawFetch: typeof fetch,
  server: StoredMcpServer,
  toolName: string,
  args: Record<string, unknown>
): Promise<unknown> {
  if (!server.url) throw new Error(`MCP 服务 ${server.name} 缺少地址`);
  const init = await mcpJsonRpcCall(
    rawFetch,
    server.url,
    "initialize",
    {
      protocolVersion: "2025-03-26",
      capabilities: {},
      clientInfo: { name: "toonflow-web", version: "2.0.0" },
    },
    server.headers
  );
  const res = await mcpJsonRpcCall(
    rawFetch,
    server.url,
    "tools/call",
    { name: toolName, arguments: args },
    server.headers,
    init.sessionId
  );
  return res.result;
}

export function getMcpSkillMarkdown(): string {
  return `# Toonflow Browser MCP Skill

Toonflow Web 端内置浏览器原生 MCP 能力：

1. **画布实时控制桥接**：工作区打开时自动接入浏览器内 \`mcpControl\` 总线，AI 助手可直接读取画布、创建节点、连接工作流与自动排版。
2. **直连远程 / 本机 HTTP MCP 服务**：在「插件市场 → MCP 服务」中配置任何支持 CORS 的 Streamable HTTP 或 SSE MCP 服务（包括用户本机 \`http://127.0.0.1:...\` 服务），所有连接均由用户浏览器直接发起，零服务器算力消耗。
`;
}

// ==================== 3. Browser A2A Protocol & Agent Teams ====================

export interface StoredA2aSettings {
  enabled: boolean;
  directory: string;
  token?: string;
  providerId?: string;
  modelId?: string;
  thinkingLevel?: string;
}

export interface StoredAgentTeam {
  name: string;
  displayName: string;
  description: string;
  version: string;
  author: string;
  github?: string;
  kind: "local" | "remote";
  enabled: boolean;
  builtin?: boolean;
  cardUrl?: string;
  token?: string;
  card?: Record<string, unknown>;
  files?: Record<string, string>;
  installedAt: number;
}

const A2A_SETTINGS_KEY = "toonflow.a2aSettings.v1";
const AGENT_TEAMS_KEY = "toonflow.agentTeams.v1";

function createBuiltinStoryboardTeam(): StoredAgentTeam {
  return {
    name: storyboardTeamJson.name,
    displayName: storyboardTeamJson.displayName,
    description: storyboardTeamJson.description,
    version: storyboardTeamJson.version,
    author: storyboardTeamJson.author,
    github: storyboardTeamJson.github,
    kind: "local",
    enabled: true,
    builtin: true,
    installedAt: 1700000000000,
    files: {
      "team.json": JSON.stringify(storyboardTeamJson, null, 2),
      "members/director.md": storyboardDirectorMd,
      "members/writer.md": storyboardWriterMd,
      "members/reviewer.md": storyboardReviewerMd,
      "knowledge/continuity.md": storyboardContinuityMd,
    },
  };
}

export async function getA2aSettings(): Promise<StoredA2aSettings & { url: string }> {
  const saved = await kvGet<StoredA2aSettings>(A2A_SETTINGS_KEY, {
    enabled: false,
    directory: "",
  });
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return {
    ...saved,
    url: `${origin}/a2a`,
  };
}

export async function saveA2aSettings(patch: Partial<StoredA2aSettings>): Promise<StoredA2aSettings & { url: string }> {
  const prev = await getA2aSettings();
  const token =
    patch.enabled && !patch.token && !prev.token
      ? Array.from(crypto.getRandomValues(new Uint8Array(24)), (b) => b.toString(16).padStart(2, "0")).join("")
      : patch.token ?? prev.token;
  const next: StoredA2aSettings = {
    enabled: patch.enabled ?? prev.enabled,
    directory: patch.directory ?? prev.directory,
    token,
    providerId: patch.providerId ?? prev.providerId,
    modelId: patch.modelId ?? prev.modelId,
    thinkingLevel: patch.thinkingLevel ?? prev.thinkingLevel,
  };
  await kvSet(A2A_SETTINGS_KEY, next);
  return { ...next, url: prev.url };
}

export async function listAgentTeams(): Promise<StoredAgentTeam[]> {
  const stored = await kvGet<StoredAgentTeam[]>(AGENT_TEAMS_KEY, []);
  const builtin = createBuiltinStoryboardTeam();
  const hasBuiltin = stored.some((t) => t.name === builtin.name);
  const all = hasBuiltin ? stored : [builtin, ...stored];
  const a2a = await getA2aSettings();
  return all.map((team) => ({
    ...team,
    ...(team.kind === "local" && team.enabled && a2a.enabled
      ? { cardUrl: `${a2a.url}/${team.name}/.well-known/agent-card.json` }
      : {}),
  }));
}

export async function saveAgentTeam(team: StoredAgentTeam): Promise<StoredAgentTeam> {
  const list = await listAgentTeams();
  const next = [team, ...list.filter((t) => t.name !== team.name)];
  await kvSet(AGENT_TEAMS_KEY, next);
  return team;
}

export async function setAgentTeamEnabled(name: string, enabled: boolean): Promise<void> {
  const list = await listAgentTeams();
  await kvSet(
    AGENT_TEAMS_KEY,
    list.map((t) => (t.name === name ? { ...t, enabled } : t))
  );
}

export async function uninstallAgentTeam(name: string): Promise<void> {
  const list = await listAgentTeams();
  await kvSet(
    AGENT_TEAMS_KEY,
    list.filter((t) => t.name !== name || t.builtin)
  );
}

export async function connectRemoteA2aAgent(
  rawFetch: typeof fetch,
  input: { name: string; cardUrl: string; token?: string }
): Promise<StoredAgentTeam> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (input.token) headers["Authorization"] = `Bearer ${input.token}`;
  const resp = await rawFetch(input.cardUrl, { headers, signal: AbortSignal.timeout(15000) });
  if (!resp.ok) throw new Error(`拉取远端 Agent Card 失败（HTTP ${resp.status}）`);
  const card = (await resp.json()) as {
    name?: string;
    description?: string;
    version?: string;
    url?: string;
    provider?: { organization?: string };
    skills?: { id?: string; name?: string; description?: string }[];
  };
  const team: StoredAgentTeam = {
    name: input.name,
    displayName: card.name || input.name,
    description: card.description || "远端 A2A 协作 Agent",
    version: card.version || "1.0.0",
    author: card.provider?.organization || "Remote A2A",
    kind: "remote",
    enabled: true,
    builtin: false,
    cardUrl: input.cardUrl,
    token: input.token,
    card: card as Record<string, unknown>,
    installedAt: Date.now(),
  };
  return saveAgentTeam(team);
}

export async function invokeRemoteA2aAgent(
  rawFetch: typeof fetch,
  team: StoredAgentTeam,
  prompt: string
): Promise<string> {
  const endpoint = (typeof team.card?.url === "string" && team.card.url) || team.cardUrl;
  if (!endpoint) throw new Error(`A2A Agent ${team.displayName} 未配置服务地址`);
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (team.token) headers["Authorization"] = `Bearer ${team.token}`;
  const rpcBody = {
    jsonrpc: "2.0",
    id: crypto.randomUUID(),
    method: "message/send",
    params: {
      message: {
        role: "user",
        parts: [{ kind: "text", text: prompt }],
      },
    },
  };
  const resp = await rawFetch(endpoint, {
    method: "POST",
    headers,
    body: JSON.stringify(rpcBody),
    signal: AbortSignal.timeout(30000),
  });
  if (!resp.ok) throw new Error(`A2A 调用失败（HTTP ${resp.status}）`);
  const json = (await resp.json()) as { result?: unknown; error?: { message?: string } };
  if (json.error) throw new Error(json.error.message || "A2A Agent 返回错误");
  return typeof json.result === "string" ? json.result : JSON.stringify(json.result, null, 2);
}

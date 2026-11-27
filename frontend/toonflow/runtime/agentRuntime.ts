import { listAiModels, streamChatCompletion, type ChatCompletionMessage, type ChatCompletionTool } from "./aiGateway";
import {
  callMcpTool,
  dispatchCanvasControlCommand,
  invokeRemoteA2aAgent,
  listAgentTeams,
  listMcpServers,
} from "./mcpAndA2a";
import { listAllSkills } from "./pluginMarketRuntime";
import {
  joinPath,
  vfsListAssetTree,
  vfsListWorkspaceRel,
  vfsReadText,
  vfsSearchText,
  vfsWriteBytes,
} from "./vfs";

// ==================== 1. Workspace AI Assistant Sessions ====================

interface StoredSessionMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  timestamp: number;
  parts?: unknown[];
}

interface StoredAgentSession {
  file: string;
  name: string;
  directory: string;
  createdAt: number;
  updatedAt: number;
  messages: StoredSessionMessage[];
  planActive: boolean;
}

const activeRuns = new Map<string, AbortController>();
const pendingQuestions = new Map<
  string,
  { resolve: (answer: unknown) => void }
>();

function sessionPath(directory: string, file: string): string {
  return joinPath(directory, ".agent/sessions", file);
}

async function readSession(directory: string, file: string): Promise<StoredAgentSession> {
  try {
    const { text } = await vfsReadText(sessionPath(directory, file));
    return JSON.parse(text) as StoredAgentSession;
  } catch {
    return {
      file,
      name: "新对话",
      directory,
      createdAt: Date.now(),
      updatedAt: Date.now(),
      messages: [],
      planActive: false,
    };
  }
}

async function writeSession(session: StoredAgentSession): Promise<void> {
  session.updatedAt = Date.now();
  await vfsWriteBytes(sessionPath(session.directory, session.file), JSON.stringify(session, null, 2), {
    mimeType: "application/json",
  });
}

export async function createAgentConversation(directory: string) {
  const id = `session-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}.jsonl`;
  const session: StoredAgentSession = {
    file: id,
    name: "新对话",
    directory,
    createdAt: Date.now(),
    updatedAt: Date.now(),
    messages: [],
    planActive: false,
  };
  await writeSession(session);
  return { file: session.file, name: session.name, updatedAt: session.updatedAt };
}

export async function listAgentSessions(directory: string) {
  try {
    const { entries } = await vfsListWorkspaceRel(directory, ".agent/sessions");
    const sessions = await Promise.all(
      entries
        .filter((e) => e.type === "file" && e.name.endsWith(".jsonl"))
        .map(async (e) => {
          const s = await readSession(directory, e.name);
          return {
            file: s.file,
            name: s.name,
            updatedAt: s.updatedAt,
            createdAt: s.createdAt,
            messageCount: s.messages.length,
          };
        })
    );
    return sessions.sort((a, b) => b.updatedAt - a.updatedAt);
  } catch {
    return [];
  }
}

export async function getAgentSession(directory: string, sessionFile: string) {
  const s = await readSession(directory, sessionFile);
  return {
    file: s.file,
    name: s.name,
    messages: s.messages,
    plan: { active: s.planActive },
    goal: null,
    running: activeRuns.has(`${directory}:${sessionFile}`),
  };
}

export async function renameAgentSession(directory: string, sessionFile: string, name: string) {
  const s = await readSession(directory, sessionFile);
  s.name = name.trim() || s.name;
  await writeSession(s);
  return { file: s.file, name: s.name };
}

export function stopAgentRun(directory: string, sessionFile?: string) {
  if (sessionFile) {
    activeRuns.get(`${directory}:${sessionFile}`)?.abort();
    activeRuns.delete(`${directory}:${sessionFile}`);
  } else {
    for (const [k, ctrl] of activeRuns) {
      if (k.startsWith(`${directory}:`)) {
        ctrl.abort();
        activeRuns.delete(k);
      }
    }
  }
}

export function answerAgentQuestion(callId: string, response: unknown) {
  const pending = pendingQuestions.get(callId);
  if (pending) {
    pendingQuestions.delete(callId);
    pending.resolve(response);
  }
  return { ok: true };
}

async function buildWorkspaceAgentTools(rawFetch: typeof fetch, directory: string) {
  const tools: ChatCompletionTool[] = [
    {
      type: "function",
      function: {
        name: "workspace_list_files",
        description: "列出当前浏览器工作区指定目录下的文件和文件夹。",
        parameters: {
          type: "object",
          properties: { path: { type: "string", description: "相对路径，留空表示根目录" } },
        },
      },
    },
    {
      type: "function",
      function: {
        name: "workspace_read_file",
        description: "读取当前工作区中的文本文件（如 .md、.txt、.canvas、.json）。",
        parameters: {
          type: "object",
          properties: { path: { type: "string", description: "文件相对路径" } },
          required: ["path"],
        },
      },
    },
    {
      type: "function",
      function: {
        name: "workspace_write_file",
        description: "在当前浏览器工作区创建或覆盖文本文件（如剧本 Markdown 或画布 JSON）。",
        parameters: {
          type: "object",
          properties: {
            path: { type: "string", description: "文件相对路径" },
            content: { type: "string", description: "完整文本内容" },
          },
          required: ["path", "content"],
        },
      },
    },
    {
      type: "function",
      function: {
        name: "workspace_search",
        description: "在当前工作区的文本文件中按关键词搜索内容。",
        parameters: {
          type: "object",
          properties: { query: { type: "string" } },
          required: ["query"],
        },
      },
    },
    {
      type: "function",
      function: {
        name: "asset_list",
        description: "列出浏览器全局素材库中的文件夹和素材文件。",
        parameters: { type: "object", properties: {} },
      },
    },
    {
      type: "function",
      function: {
        name: "skill_read",
        description: "读取已安装的 Toonflow 创作技能（如 canvas、workflow、v118workflow）。",
        parameters: {
          type: "object",
          properties: { name: { type: "string", description: "技能名称" } },
          required: ["name"],
        },
      },
    },
    {
      type: "function",
      function: {
        name: "canvas_control",
        description: "向当前打开的无限画布发送控制指令（如添加节点、查询画布、自动排版）。",
        parameters: {
          type: "object",
          properties: {
            action: { type: "string", description: "画布指令名称，例如 getCanvas / addNode / arrange" },
            payload: { type: "object", description: "指令参数对象" },
          },
          required: ["action"],
        },
      },
    },
  ];

  // Dynamically attach tools from enabled browser MCP servers
  const mcpServers = (await listMcpServers()).filter((s) => s.enabled && s.url && s.transport !== "stdio");
  const mcpMap = new Map<string, { server: (typeof mcpServers)[number]; toolName: string }>();
  for (const server of mcpServers) {
    for (const tool of server.tools ?? []) {
      const safeName = `mcp_${server.id.slice(0, 6)}_${tool.name}`.replace(/[^a-zA-Z0-9_]/g, "_").slice(0, 64);
      mcpMap.set(safeName, { server, toolName: tool.name });
      tools.push({
        type: "function",
        function: {
          name: safeName,
          description: `[MCP: ${server.name}] ${tool.description || tool.name}`,
          parameters: tool.inputSchema || { type: "object", properties: {} },
        },
      });
    }
  }

  // Dynamically attach enabled remote A2A agents
  const a2aTeams = (await listAgentTeams()).filter((t) => t.enabled && t.kind === "remote");
  const a2aMap = new Map<string, (typeof a2aTeams)[number]>();
  for (const team of a2aTeams) {
    const safeName = `a2a_${team.name}`.replace(/[^a-zA-Z0-9_]/g, "_").slice(0, 64);
    a2aMap.set(safeName, team);
    tools.push({
      type: "function",
      function: {
        name: safeName,
        description: `[A2A Agent: ${team.displayName}] ${team.description}`,
        parameters: {
          type: "object",
          properties: { prompt: { type: "string", description: "发送给远端 A2A Agent 的任务指令" } },
          required: ["prompt"],
        },
      },
    });
  }

  const execute = async (name: string, args: Record<string, unknown>): Promise<string> => {
    if (name === "workspace_list_files") {
      const res = await vfsListWorkspaceRel(directory, String(args.path ?? ""));
      return JSON.stringify(res.entries, null, 2);
    }
    if (name === "workspace_read_file") {
      const { text } = await vfsReadText(joinPath(directory, String(args.path ?? "")));
      return text.slice(0, 16000);
    }
    if (name === "workspace_write_file") {
      const rel = String(args.path ?? "");
      await vfsWriteBytes(joinPath(directory, rel), String(args.content ?? ""));
      return `已写入 ${rel}`;
    }
    if (name === "workspace_search") {
      const hits = await vfsSearchText(directory, String(args.query ?? ""));
      return JSON.stringify(hits, null, 2);
    }
    if (name === "asset_list") {
      const tree = await vfsListAssetTree();
      return JSON.stringify(tree, null, 2);
    }
    if (name === "skill_read") {
      const skills = await listAllSkills();
      const found = skills.find((s) => s.name === String(args.name ?? ""));
      return found ? found.content.slice(0, 16000) : `未找到技能：${String(args.name ?? "")}`;
    }
    if (name === "canvas_control") {
      const res = await dispatchCanvasControlCommand(
        directory,
        String(args.action ?? "getCanvas"),
        (args.payload as Record<string, unknown>) ?? {}
      );
      return JSON.stringify(res);
    }
    const mcpTarget = mcpMap.get(name);
    if (mcpTarget) {
      const res = await callMcpTool(rawFetch, mcpTarget.server, mcpTarget.toolName, args);
      return JSON.stringify(res);
    }
    const a2aTarget = a2aMap.get(name);
    if (a2aTarget) {
      return invokeRemoteA2aAgent(rawFetch, a2aTarget, String(args.prompt ?? ""));
    }
    throw new Error(`未知工具：${name}`);
  };

  return { tools, execute };
}

export function runWorkspaceAgentMessage(
  rawFetch: typeof fetch,
  body: {
    directory: string;
    sessionFile?: string;
    prompt?: string;
    content?: string;
    message?: string;
    providerId?: string;
    modelId?: string;
    selectedModel?: string;
  },
  signal?: AbortSignal
): Response {
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder();
      const emit = (event: Record<string, unknown>) => {
        try {
          controller.enqueue(enc.encode(`${JSON.stringify(event)}\n`));
        } catch {
          // stream closed
        }
      };

      const runController = new AbortController();
      signal?.addEventListener("abort", () => runController.abort(), { once: true });

      const sessionFile = body.sessionFile || (await createAgentConversation(body.directory)).file;
      const runKey = `${body.directory}:${sessionFile}`;
      activeRuns.set(runKey, runController);

      try {
        const session = await readSession(body.directory, sessionFile);
        const userText = (body.prompt ?? body.content ?? body.message ?? "").trim();
        const runId = crypto.randomUUID();
        emit({ type: "session", file: sessionFile, runId });

        const userMsgId = crypto.randomUUID();
        emit({ type: "userMessage", id: userMsgId, content: userText });

        if (!session.messages.length && userText) {
          session.name = userText.slice(0, 28);
        }
        session.messages.push({ id: userMsgId, role: "user", content: userText, timestamp: Date.now() });

        let providerId = body.providerId || "";
        let modelId = body.modelId || "";
        if (!providerId || !modelId) {
          if (body.selectedModel && body.selectedModel.includes("::")) {
            const [p, m] = body.selectedModel.split("::");
            providerId = p || "";
            modelId = m || "";
          } else {
            const models = await listAiModels();
            if (models[0]) {
              providerId = models[0].providerId;
              modelId = models[0].id;
            }
          }
        }

        const { tools, execute } = await buildWorkspaceAgentTools(rawFetch, body.directory);
        const skills = (await listAllSkills()).filter((s) => s.enabled);
        const systemPrompt = [
          "你是 Toonflow AI 创作助手，运行在用户的浏览器本机工作区中。",
          `当前工作区路径：${body.directory}`,
          `可用创作技能：${skills.map((s) => `${s.name}(${s.displayName})`).join("、")}`,
          "你可以直接调用工作区文件工具、素材库工具、画布控制工具、已启用的 MCP 服务工具与远端 A2A Agent 工具协助完成短剧剧本、分镜拆解、3D 白模预演与画布编排。",
        ].join("\n");

        const chatMessages: ChatCompletionMessage[] = [
          { role: "system", content: systemPrompt },
          ...session.messages.slice(-20).map((m) => ({
            role: m.role as "user" | "assistant",
            content: m.content,
          })),
        ];

        let finalAssistantText = "";
        const totalUsage = { input: 0, output: 0, total: 0 };

        for (let round = 0; round < 6; round++) {
          runController.signal.throwIfAborted();
          const thinkId = `thinking-${round}`;
          const textId = `text-${round}`;
          const res = await streamChatCompletion(rawFetch, {
            providerId,
            modelId,
            messages: chatMessages,
            tools,
            signal: runController.signal,
            onThinkingDelta(delta) {
              emit({ type: "thinking", blockId: thinkId, delta });
            },
            onTextDelta(delta) {
              emit({ type: "text", blockId: textId, delta });
            },
          });
          if (res.thinking) emit({ type: "thinking", blockId: thinkId, done: true });
          if (res.text) {
            emit({ type: "text", blockId: textId, done: true });
            finalAssistantText += (finalAssistantText ? "\n\n" : "") + res.text;
          }
          totalUsage.input += res.usage.input;
          totalUsage.output += res.usage.output;
          totalUsage.total += res.usage.total;

          if (!res.toolCalls.length) break;

          chatMessages.push({
            role: "assistant",
            content: res.text || "",
            tool_calls: res.toolCalls.map((tc) => ({
              id: tc.id,
              type: "function",
              function: { name: tc.name, arguments: JSON.stringify(tc.arguments) },
            })),
          });

          for (const tc of res.toolCalls) {
            const blockId = `tool-${tc.id}`;
            emit({
              type: "tool",
              blockId,
              tool: { id: tc.id, name: tc.name, args: tc.arguments, status: "running" },
            });
            try {
              const output = await execute(tc.name, tc.arguments);
              emit({
                type: "tool",
                blockId,
                tool: { id: tc.id, name: tc.name, args: tc.arguments, status: "done", result: output },
              });
              chatMessages.push({ role: "tool", tool_call_id: tc.id, content: output });
            } catch (err) {
              const errMsg = err instanceof Error ? err.message : String(err);
              emit({
                type: "tool",
                blockId,
                tool: { id: tc.id, name: tc.name, args: tc.arguments, status: "error", error: errMsg },
              });
              chatMessages.push({ role: "tool", tool_call_id: tc.id, content: `工具执行出错：${errMsg}` });
            }
          }
        }

        session.messages.push({
          id: crypto.randomUUID(),
          role: "assistant",
          content: finalAssistantText,
          timestamp: Date.now(),
        });
        await writeSession(session);

        emit({
          type: "stats",
          stats: {
            tokens: {
              input: totalUsage.input,
              output: totalUsage.output,
              cacheRead: 0,
              cacheWrite: 0,
              total: totalUsage.total,
            },
          },
        });
        emit({ type: "done" });
      } catch (err) {
        if (!runController.signal.aborted) {
          emit({ type: "error", message: err instanceof Error ? err.message : String(err) });
        }
      } finally {
        activeRuns.delete(runKey);
        controller.close();
      }
    },
  });

  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "application/x-ndjson; charset=utf-8", "Cache-Control": "no-store" },
  });
}

// ==================== 2. SingleAgent Runtime (TrumanMap 3D Holodeck AI) ====================

interface SingleAgentDeclaredTool {
  name: string;
  label?: string;
  description: string;
  parameters: Record<string, unknown>;
}

interface SingleAgentSession {
  sessionId: string;
  directory?: string;
  systemPrompt?: string;
  clientTools: SingleAgentDeclaredTool[];
  history: ChatCompletionMessage[];
  pendingToolCalls: Map<string, { resolve: (res: { result?: { content?: { type: string; text?: string }[] }; error?: string }) => void }>;
  controller?: AbortController;
}

const singleAgentSessions = new Map<string, SingleAgentSession>();

export function createSingleAgentSession(body: {
  tools?: (string | SingleAgentDeclaredTool | Record<string, unknown>)[];
  directory?: string;
  systemPrompt?: string;
}) {
  const sessionId = crypto.randomUUID();
  const clientTools: SingleAgentDeclaredTool[] = [];
  for (const item of body.tools ?? []) {
    if (item && typeof item === "object" && "name" in item && "description" in item && "parameters" in item) {
      clientTools.push(item as SingleAgentDeclaredTool);
    }
  }
  const session: SingleAgentSession = {
    sessionId,
    directory: body.directory,
    systemPrompt: body.systemPrompt,
    clientTools,
    history: body.systemPrompt ? [{ role: "system", content: body.systemPrompt }] : [],
    pendingToolCalls: new Map(),
  };
  singleAgentSessions.set(sessionId, session);
  return { sessionId, tools: clientTools.map((t) => t.name) };
}

export function submitSingleAgentToolResult(body: {
  sessionId: string;
  callId: string;
  result?: { content?: { type: string; text?: string }[] };
  error?: string;
  partial?: boolean;
}) {
  if (body.partial) return { ok: true };
  const session = singleAgentSessions.get(body.sessionId);
  const pending = session?.pendingToolCalls.get(body.callId);
  if (pending) {
    session!.pendingToolCalls.delete(body.callId);
    pending.resolve({ result: body.result, error: body.error });
  }
  return { ok: true };
}

export function controlSingleAgent(body: { sessionId: string; action: "pause" | "resume" | "stop" | "close" }) {
  const session = singleAgentSessions.get(body.sessionId);
  if (session) {
    if (body.action === "stop" || body.action === "close") {
      session.controller?.abort();
      for (const [, pending] of session.pendingToolCalls) {
        pending.resolve({ error: "会话已停止" });
      }
      session.pendingToolCalls.clear();
    }
    if (body.action === "close") singleAgentSessions.delete(body.sessionId);
  }
  return { ok: true };
}

export function runSingleAgentStream(
  rawFetch: typeof fetch,
  body: {
    sessionId: string;
    runId: string;
    prompt: string;
    providerId: string;
    modelId: string;
    images?: { type: "image"; data: string; mimeType: string }[];
  },
  signal?: AbortSignal
): Response {
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder();
      const emit = (event: Record<string, unknown>) => {
        try {
          controller.enqueue(enc.encode(`${JSON.stringify(event)}\n`));
        } catch {
          // ignore
        }
      };

      const session = singleAgentSessions.get(body.sessionId);
      if (!session) {
        emit({ type: "error", message: "SingleAgent 会话不存在或已关闭" });
        controller.close();
        return;
      }

      const runCtrl = new AbortController();
      session.controller = runCtrl;
      signal?.addEventListener("abort", () => runCtrl.abort(), { once: true });

      try {
        const userContent = body.images?.length
          ? [
              { type: "text", text: body.prompt },
              ...body.images.map((img) => ({
                type: "image_url",
                image_url: { url: `data:${img.mimeType};base64,${img.data}` },
              })),
            ]
          : body.prompt;
        session.history.push({ role: "user", content: userContent });

        const chatTools: ChatCompletionTool[] = session.clientTools.map((t) => ({
          type: "function",
          function: { name: t.name, description: t.description, parameters: t.parameters },
        }));

        for (let round = 0; round < 10; round++) {
          runCtrl.signal.throwIfAborted();
          const thinkId = `sa-think-${round}`;
          const textId = `sa-text-${round}`;
          const res = await streamChatCompletion(rawFetch, {
            providerId: body.providerId,
            modelId: body.modelId,
            messages: session.history,
            tools: chatTools,
            signal: runCtrl.signal,
            onThinkingDelta(delta) {
              emit({ type: "thinking", blockId: thinkId, delta });
            },
            onTextDelta(delta) {
              emit({ type: "text", blockId: textId, delta });
            },
          });
          if (res.thinking) emit({ type: "thinking", blockId: thinkId, done: true });
          if (res.text) emit({ type: "text", blockId: textId, done: true });

          if (!res.toolCalls.length) {
            session.history.push({ role: "assistant", content: res.text });
            break;
          }

          session.history.push({
            role: "assistant",
            content: res.text || "",
            tool_calls: res.toolCalls.map((tc) => ({
              id: tc.id,
              type: "function",
              function: { name: tc.name, arguments: JSON.stringify(tc.arguments) },
            })),
          });

          for (const tc of res.toolCalls) {
            const callId = crypto.randomUUID();
            const blockId = `sa-tool-${tc.id}`;
            emit({
              type: "tool",
              blockId,
              tool: { id: tc.id, name: tc.name, args: tc.arguments, status: "running" },
            });
            // Dispatch client-side tool execution to TrumanMap browser UI and wait for /api/singleAgent/toolResult
            const toolPromise = new Promise<{ result?: { content?: { type: string; text?: string }[] }; error?: string }>(
              (resolve) => {
                session.pendingToolCalls.set(callId, { resolve });
              }
            );
            emit({ type: "toolCall", callId, toolCallId: tc.id, name: tc.name, args: tc.arguments });

            const outcome = await toolPromise;
            const textOutput = outcome.error
              ? `错误：${outcome.error}`
              : (outcome.result?.content ?? [])
                  .filter((p) => p.type === "text" && p.text)
                  .map((p) => p.text)
                  .join("\n") || "执行完成";
            emit({
              type: "tool",
              blockId,
              tool: {
                id: tc.id,
                name: tc.name,
                args: tc.arguments,
                status: outcome.error ? "error" : "done",
                result: textOutput,
                error: outcome.error,
              },
            });
            session.history.push({ role: "tool", tool_call_id: tc.id, content: textOutput });
          }
        }
        emit({ type: "done" });
      } catch (err) {
        if (!runCtrl.signal.aborted) {
          emit({ type: "error", message: err instanceof Error ? err.message : String(err) });
        }
      } finally {
        controller.close();
      }
    },
  });

  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "application/x-ndjson; charset=utf-8", "Cache-Control": "no-store" },
  });
}

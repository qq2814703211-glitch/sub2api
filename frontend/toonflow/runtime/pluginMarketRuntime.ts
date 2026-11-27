import { kvGet, kvSet } from "./vfs";
import extCanvasNodeMeta from "../upstream/packages/ext/canvasNode/src/metadata";
import extImageMeta from "../upstream/packages/ext/image/src/metadata";
import extMdMeta from "../upstream/packages/ext/md/src/metadata";
import extMediaMeta from "../upstream/packages/ext/media/src/metadata";
import extTxtMeta from "../upstream/packages/ext/txt/src/metadata";
import canvasSkillMd from "../upstream/packages/skills/canvas/SKILL.md?raw";
import workflowSkillMd from "../upstream/packages/skills/workflow/SKILL.md?raw";
import v118WorkflowSkillMd from "../upstream/packages/skills/v118workflow/SKILL.md?raw";

export interface BuiltinNodeMeta {
  name: string;
  displayName: string;
  version: string;
  author: string;
  github: string;
  description: string;
  readme: string;
}

const BUILTIN_NODES: BuiltinNodeMeta[] = [
  {
    name: "textNode",
    displayName: "文本",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "支持 Markdown / 富文本剧本编辑与 AI 文本扩写、拆镜。",
    readme: "# 文本节点\n\n在无限画布中编写剧本、提示词或分镜描述，可连接至生图/生视频节点。",
  },
  {
    name: "imageNode",
    displayName: "图片",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "图片预览、裁剪、局部修图、序列图拆分与参考图输出。",
    readme: "# 图片节点\n\n展示与编辑工作区图片，支持在浏览器端直接处理并作为下游参考图输入。",
  },
  {
    name: "imageGenerationNode",
    displayName: "图片生成",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "文生图 / 图生图 / 多参考图融合节点，直连 Sub2API 站内网关或自定义模型。",
    readme: "# 图片生成节点\n\n支持接收上游文本与多张参考图连线，调用站内图像模型直接生成关键帧。",
  },
  {
    name: "videoNode",
    displayName: "视频",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "视频播放、裁切、抽帧与音轨预览节点，纯浏览器端本地渲染。",
    readme: "# 视频节点\n\n使用浏览器内置播放器预览与处理视频素材。",
  },
  {
    name: "videoGenerationNode",
    displayName: "视频生成",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "文生视频 / 首尾帧图生视频节点，支持 Grok / OpenAI / Seedance 等站内模型。",
    readme: "# 视频生成节点\n\n结合关键帧图片与运镜提示词生成动态分镜视频。",
  },
  {
    name: "audioNode",
    displayName: "音频",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "音频波形播放与人声分离 / 语音转写辅助节点。",
    readme: "# 音频节点\n\n纯浏览器端音频播放与配音管理。",
  },
  {
    name: "audioGenerationNode",
    displayName: "音频生成",
    version: "2.0.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "AI 语音合成（TTS）与旁白配音生成节点。",
    readme: "# 音频生成节点\n\n将台词与旁白文本生成配音音频，直接保存到浏览器工作区。",
  },
  {
    name: "frameAnalysisNode",
    displayName: "逐帧拉片",
    version: "1.3.0",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "浏览器端视频镜头切分、关键帧抽取与 AI 逐帧拉片分析节点。",
    readme: "# 逐帧拉片节点\n\n在用户本机浏览器中完成镜头切分与关键帧导出，零服务器 FFmpeg 消耗。",
  },
  {
    name: "trumanMap",
    displayName: "白模预演TrumanMap",
    version: "0.16.47",
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    description: "Three.js 3D 白模片场预演节点：一句话 AI 搭景、角色走位、多机位运镜、天气光照与关键帧导出。",
    readme: "# 白模预演 TrumanMap\n\n基于浏览器 WebGL / Three.js 实时渲染的 3D 白模预演引擎，完全在用户本机 GPU 运算。",
  },
];

const BUILTIN_EXTENSIONS = [
  { name: "canvasNode", version: "1.0.0", meta: extCanvasNodeMeta },
  { name: "image", version: "0.1.0", meta: extImageMeta },
  { name: "md", version: "0.1.0", meta: extMdMeta },
  { name: "media", version: "0.1.0", meta: extMediaMeta },
  { name: "txt", version: "0.1.0", meta: extTxtMeta },
];

export interface StoredSkill {
  name: string;
  displayName: string;
  description: string;
  version: string;
  author: string;
  builtin?: boolean;
  enabled: boolean;
  content: string;
  files?: Record<string, string>;
  installedAt: number;
}

const BUILTIN_SKILLS: StoredSkill[] = [
  {
    name: "canvas",
    displayName: "无限画布编排技能",
    description: "指导 AI 助手如何读取、创建、连接与自动排版 Toonflow 无限画布节点。",
    version: "2.0.0",
    author: "Toonflow",
    builtin: true,
    enabled: true,
    content: canvasSkillMd,
    files: { "SKILL.md": canvasSkillMd },
    installedAt: 1700000000000,
  },
  {
    name: "workflow",
    displayName: "短剧漫剧全流程制作技能",
    description: "从小说改编、角色三视图、场景设定到分镜与视频生成的完整导演工作流规范。",
    version: "2.0.0",
    author: "Toonflow",
    builtin: true,
    enabled: true,
    content: workflowSkillMd,
    files: { "SKILL.md": workflowSkillMd },
    installedAt: 1700000000000,
  },
  {
    name: "v118workflow",
    displayName: "经典短剧分镜工作流",
    description: "适用于快速出片的多镜连贯性控制与提示词模板技能。",
    version: "1.18.0",
    author: "Toonflow",
    builtin: true,
    enabled: true,
    content: v118WorkflowSkillMd,
    files: { "SKILL.md": v118WorkflowSkillMd },
    installedAt: 1700000000000,
  },
];

export interface MarketPluginItem {
  id: number;
  identifier: string;
  type: "node" | "tool" | "skill" | "agent" | "ext";
  name: string;
  state: 0 | 1 | 2;
  version: string;
  supplier: string;
  desc: string;
  link?: string;
  fileName: string;
  isCollected?: boolean;
  tags?: { text: string; color: string }[];
  payload?: { content?: string; files?: Record<string, string>; description?: string };
}

const CURATED_MARKET_PLUGINS: MarketPluginItem[] = [
  {
    id: 1001,
    identifier: "trumanMap",
    type: "node",
    name: "白模预演 TrumanMap 3D",
    state: 1,
    version: "0.16.47",
    supplier: "Toonflow Official",
    desc: "Three.js 3D 白模片场预演，含建筑/自然/载具/道具程序化白模库、多机位运镜、角色动画与 AI 搭景。",
    link: "https://github.com/HBAI-Ltd/Toonflow-app",
    fileName: "trumanMap.esm.js",
    tags: [{ text: "3D预演", color: "#409eff" }, { text: "本机WebGL", color: "#67c23a" }],
  },
  {
    id: 1002,
    identifier: "frameAnalysisNode",
    type: "node",
    name: "逐帧拉片分析节点",
    state: 1,
    version: "1.3.0",
    supplier: "Toonflow Official",
    desc: "浏览器本机镜头切分、关键帧抽取与 AI 拉片报告生成，零服务端算力占用。",
    link: "https://github.com/HBAI-Ltd/Toonflow-app",
    fileName: "frameAnalysisNode.esm.js",
    tags: [{ text: "拉片", color: "#e6a23c" }],
  },
  {
    id: 1003,
    identifier: "storyboardTeam",
    type: "agent",
    name: "分镜创作团队（A2A）",
    state: 1,
    version: "2.0.0",
    supplier: "Toonflow Official",
    desc: "导演 + 编剧 + 审阅多角色协作 Agent 团队，自动产出连贯文字分镜方案。",
    link: "https://github.com/HBAI-Ltd/Toonflow-app",
    fileName: "storyboardTeam.json",
    tags: [{ text: "A2A", color: "#a78bfa" }, { text: "多Agent", color: "#409eff" }],
  },
  {
    id: 1004,
    identifier: "camera-director-skill",
    type: "skill",
    name: "电影级运镜导演技能",
    state: 1,
    version: "1.2.0",
    supplier: "Creative Plaza Community",
    desc: "为 AI 助手注入专业电影焦段、景别、推拉摇移跟升降运镜词典与灯光构图规范。",
    fileName: "camera-director-skill.md",
    tags: [{ text: "运镜", color: "#409eff" }, { text: "提示词", color: "#67c23a" }],
    payload: {
      content: `---\nname: camera-director-skill\nmetadata:\n  displayName: 电影级运镜导演技能\n---\n\n# 电影级运镜导演技能\n\n编写分镜与生图/生视频提示词时，遵循：\n1. 明确焦段（24mm 广角 / 35mm 叙事 / 85mm 特写）与光圈景深。\n2. 明确摄影机运动（推轨 Dolly-in、横摇 Pan、低角度仰拍 Low-angle、手持跟随 Handheld）。\n3. 保持相邻镜头角色服装、主光源方向与色调连续性。\n`,
    },
  },
  {
    id: 1005,
    identifier: "anime-character-sheet-skill",
    type: "skill",
    name: "漫剧角色三视图与一致性技能",
    state: 1,
    version: "1.1.0",
    supplier: "Creative Plaza Community",
    desc: "一键拆解角色外观锚点（发色、瞳色、服饰配饰、体态比例），生成标准三视图与表情表提示词。",
    fileName: "anime-character-sheet-skill.md",
    tags: [{ text: "角色一致性", color: "#f56c6c" }],
    payload: {
      content: `---\nname: anime-character-sheet-skill\nmetadata:\n  displayName: 漫剧角色三视图与一致性技能\n---\n\n# 漫剧角色三视图与一致性技能\n\n输出角色设定时必须包含：\n- 正视图 / 侧视图 / 背视图三视图构图指令\n- 固定视觉锚点表（发型、服装主辅色、标志性配饰）\n- 6 宫格基础表情与动态姿势参考提示词\n`,
    },
  },
  {
    id: 1006,
    identifier: "short-drama-director-agent",
    type: "agent",
    name: "爆款短剧钩子策划团队",
    state: 1,
    version: "1.0.0",
    supplier: "Creative Plaza Community",
    desc: "专注 60-90 秒竖屏短剧前 3 秒黄金钩子、情绪反转与节奏卡点的专用 Agent 团队。",
    fileName: "short-drama-director-agent.json",
    tags: [{ text: "短剧策划", color: "#e6a23c" }],
    payload: {
      description: "专注竖屏短剧黄金三秒开篇、情绪递进与反转卡点拆解的策划 Agent。",
      files: {
        "team.json": JSON.stringify(
          {
            name: "short-drama-director-agent",
            displayName: "爆款短剧钩子策划团队",
            description: "专注竖屏短剧黄金三秒开篇、情绪递进与反转卡点拆解。",
            version: "1.0.0",
            author: "Creative Plaza Community",
            entry: "producer",
          },
          null,
          2
        ),
        "members/producer.md": "# 短剧制片策划\n\n将用户灵感拆解为：0-3秒强钩子、15秒冲突升级、45秒反转高潮、60秒悬念留尾。",
      },
    },
  },
];

const DISABLED_PLUGINS_KEY = "toonflow.disabledPlugins.v1";
const PLUGIN_CONFIGS_KEY = "toonflow.pluginConfigs.v1";
const CUSTOM_SKILLS_KEY = "toonflow.customSkills.v1";
const CUSTOM_NODES_KEY = "toonflow.customNodes.v1";
const MARKET_COLLECTIONS_KEY = "toonflow.marketCollections.v1";
const CUSTOM_MARKET_KEY = "toonflow.customMarketPlugins.v1";

export async function listInstalledNodes() {
  const disabled = new Set(await kvGet<string[]>(DISABLED_PLUGINS_KEY, []));
  const configs = await kvGet<Record<string, Record<string, unknown>>>(PLUGIN_CONFIGS_KEY, {});
  const customNodes = await kvGet<(BuiltinNodeMeta & { code: string; installedAt: number })[]>(CUSTOM_NODES_KEY, []);

  const builtinItems = BUILTIN_NODES.map((node) => ({
    ...node,
    loadError: "",
    configRules: [],
    installedAt: 1700000000000,
    marketIdentifiers: [node.name],
    builtin: true,
    url: `/api/nodes/files?name=${encodeURIComponent(node.name)}`,
    enabled: !disabled.has(`node:${node.name}`),
    config: configs[`node:${node.name}`] ?? {},
    canConfigure: true,
  }));

  const customItems = customNodes.map((node) => ({
    ...node,
    loadError: "",
    configRules: [],
    marketIdentifiers: [node.name],
    builtin: false,
    url: `/api/nodes/files?name=${encodeURIComponent(node.name)}`,
    enabled: !disabled.has(`node:${node.name}`),
    config: configs[`node:${node.name}`] ?? {},
    canConfigure: true,
  }));

  return [...builtinItems, ...customItems];
}

export async function getNodeSourceCode(name: string): Promise<string | null> {
  const customNodes = await kvGet<(BuiltinNodeMeta & { code: string })[]>(CUSTOM_NODES_KEY, []);
  const found = customNodes.find((n) => n.name === name);
  return found?.code ?? null;
}

export async function listInstalledExtensions() {
  const disabled = new Set(await kvGet<string[]>(DISABLED_PLUGINS_KEY, []));
  const configs = await kvGet<Record<string, Record<string, unknown>>>(PLUGIN_CONFIGS_KEY, {});
  return BUILTIN_EXTENSIONS.map(({ name, version, meta }) => ({
    ...meta,
    name,
    version,
    author: "Toonflow",
    github: "https://github.com/HBAI-Ltd/Toonflow-app",
    readme: `# ${meta.displayName}\n\nToonflow 官方文档区扩展插件。`,
    loadError: "",
    installedAt: 1700000000000,
    canManage: true,
    canConfigure: true,
    config: configs[`ext:${name}`] ?? {},
    marketIdentifiers: [meta.id],
    builtin: true,
    enabled: !disabled.has(`ext:${name}`),
    url: `/api/ext/read?name=${encodeURIComponent(name)}`,
  }));
}

export async function listInstalledTools() {
  const disabled = new Set(await kvGet<string[]>(DISABLED_PLUGINS_KEY, []));
  const configs = await kvGet<Record<string, Record<string, unknown>>>(PLUGIN_CONFIGS_KEY, {});
  const builtinTools = [
    { name: "canvas", displayName: "无限画布操作工具", description: "读写与排版当前工作区 Vue Flow 画布节点与连线。", version: "3.0.1" },
    { name: "trumanMap", displayName: "白模预演 3D 工具", description: "在浏览器 Three.js 场景中放置资产、调整地形、机位与角色走位。", version: "0.4.1" },
    { name: "workspace", displayName: "工作区文件工具", description: "读写与搜索浏览器本机 IndexedDB 工作区文件。", version: "2.0.0" },
    { name: "assetOperator", displayName: "素材库管理工具", description: "检索与归档浏览器全局素材库中的角色与分镜素材。", version: "2.0.0" },
    { name: "askUser", displayName: "人机交互提问工具", description: "在执行关键创作决策前向用户弹出结构化确认卡片。", version: "2.0.0" },
    { name: "skillOperator", displayName: "技能读取工具", description: "按需加载内置与自定义创作技能规范。", version: "2.0.0" },
    { name: "webFetch", displayName: "网页抓取工具", description: "由浏览器端获取允许 CORS 的公开参考资料。", version: "2.0.0" },
    { name: "browserMedia", displayName: "本机音视频处理工具", description: "纯浏览器 HTML5 Canvas / WebAudio 抽帧与元信息解析。", version: "2.0.0" },
  ];
  return {
    canManage: true,
    tools: builtinTools.map((tool) => ({
      ...tool,
      author: "Toonflow",
      github: "https://github.com/HBAI-Ltd/Toonflow-app",
      builtin: true,
      enabled: !disabled.has(`tool:${tool.name}`),
      canConfigure: false,
      config: configs[`tool:${tool.name}`] ?? {},
      configRules: [],
      marketIdentifiers: [tool.name],
      installedAt: 1700000000000,
    })),
  };
}

export async function listAllSkills(): Promise<StoredSkill[]> {
  const disabled = new Set(await kvGet<string[]>(DISABLED_PLUGINS_KEY, []));
  const custom = await kvGet<StoredSkill[]>(CUSTOM_SKILLS_KEY, []);
  const builtins = BUILTIN_SKILLS.map((s) => ({ ...s, enabled: !disabled.has(`skill:${s.name}`) }));
  const customs = custom.map((s) => ({ ...s, enabled: !disabled.has(`skill:${s.name}`) }));
  return [...builtins, ...customs];
}

export async function saveSkillContent(name: string, content: string, displayName?: string, description?: string): Promise<StoredSkill> {
  const custom = await kvGet<StoredSkill[]>(CUSTOM_SKILLS_KEY, []);
  const existing = custom.find((s) => s.name === name);
  const builtin = BUILTIN_SKILLS.find((s) => s.name === name);
  const next: StoredSkill = {
    name,
    displayName: displayName || existing?.displayName || builtin?.displayName || name,
    description: description || existing?.description || builtin?.description || "自定义创作技能",
    version: existing?.version || "1.0.0",
    author: existing?.author || "用户自定义",
    builtin: Boolean(builtin),
    enabled: true,
    content,
    files: { ...(existing?.files ?? {}), "SKILL.md": content },
    installedAt: existing?.installedAt || Date.now(),
  };
  await kvSet(CUSTOM_SKILLS_KEY, [next, ...custom.filter((s) => s.name !== name)]);
  return next;
}

export async function uninstallSkill(name: string): Promise<void> {
  const custom = await kvGet<StoredSkill[]>(CUSTOM_SKILLS_KEY, []);
  await kvSet(
    CUSTOM_SKILLS_KEY,
    custom.filter((s) => s.name !== name)
  );
}

export async function setPluginEnabled(type: string, name: string, enabled: boolean): Promise<void> {
  const disabled = new Set(await kvGet<string[]>(DISABLED_PLUGINS_KEY, []));
  const key = `${type}:${name}`;
  if (enabled) disabled.delete(key);
  else disabled.add(key);
  await kvSet(DISABLED_PLUGINS_KEY, [...disabled]);
}

export async function savePluginConfig(type: string, name: string, config: Record<string, unknown>): Promise<void> {
  const configs = await kvGet<Record<string, Record<string, unknown>>>(PLUGIN_CONFIGS_KEY, {});
  configs[`${type}:${name}`] = config;
  await kvSet(PLUGIN_CONFIGS_KEY, configs);
}

export async function listMarketPlugins(params: {
  page?: number;
  limit?: number;
  type?: string;
  searchKeyword?: string;
}) {
  const collections = new Set(await kvGet<number[]>(MARKET_COLLECTIONS_KEY, []));
  const customMarket = await kvGet<MarketPluginItem[]>(CUSTOM_MARKET_KEY, []);
  const all = [...customMarket, ...CURATED_MARKET_PLUGINS].map((item) => {
    const marketLink = `browser-market://${item.type}/${item.identifier}`;
    return {
      ...item,
      isCollected: collections.has(item.id),
      link: item.link || marketLink,
      url: marketLink,
    };
  });

  const keyword = (params.searchKeyword || "").trim().toLowerCase();
  const filtered = all.filter((item) => {
    if (params.type && params.type !== "all") {
      if (params.type === "collection") {
        if (!item.isCollected) return false;
      } else if (params.type === "my") {
        if (!customMarket.some((c) => c.id === item.id)) return false;
      } else if (item.type !== params.type) {
        return false;
      }
    }
    if (keyword) {
      const hay = `${item.name} ${item.identifier} ${item.desc} ${item.supplier}`.toLowerCase();
      if (!hay.includes(keyword)) return false;
    }
    return true;
  });

  const page = Math.max(1, Number(params.page || 1));
  const limit = Math.max(1, Number(params.limit || 20));
  const start = (page - 1) * limit;
  return {
    list: filtered.slice(start, start + limit),
    total: filtered.length,
    page,
    limit,
  };
}

export async function toggleMarketCollection(pluginId: number): Promise<{ collected: boolean; isCollected: boolean }> {
  const collections = new Set(await kvGet<number[]>(MARKET_COLLECTIONS_KEY, []));
  let isCollected: boolean;
  if (collections.has(pluginId)) {
    collections.delete(pluginId);
    isCollected = false;
  } else {
    collections.add(pluginId);
    isCollected = true;
  }
  await kvSet(MARKET_COLLECTIONS_KEY, [...collections]);
  return { collected: isCollected, isCollected };
}

export async function installFromMarketOrFile(input: {
  type: "node" | "tool" | "skill" | "agent" | "ext";
  url?: string;
  identifier?: string;
  name?: string;
  content?: string;
}) {
  const customMarket = await kvGet<MarketPluginItem[]>(CUSTOM_MARKET_KEY, []);
  const all = [...customMarket, ...CURATED_MARKET_PLUGINS];
  const matched = all.find(
    (item) =>
      (input.url && input.url.endsWith(`/${item.identifier}`)) ||
      (input.identifier && item.identifier === input.identifier) ||
      (input.name && item.identifier === input.name)
  );
  if (input.type === "skill") {
    const id = matched?.identifier || input.identifier || input.name || `skill-${Date.now()}`;
    const content = matched?.payload?.content || input.content || `# ${id}\n\n已安装的浏览器端自定义技能。`;
    await saveSkillContent(id, content, matched?.name || id, matched?.desc);
    return { name: id, installed: true };
  }
  if (input.type === "agent") {
    const { saveAgentTeam } = await import("./mcpAndA2a");
    const id = matched?.identifier || input.identifier || input.name || `agent-${Date.now()}`;
    await saveAgentTeam({
      name: id,
      displayName: matched?.name || id,
      description: matched?.desc || "自定义协作 Agent",
      version: matched?.version || "1.0.0",
      author: matched?.supplier || "User",
      kind: "local",
      enabled: true,
      builtin: false,
      files: matched?.payload?.files ?? { "team.json": JSON.stringify({ name: id, displayName: matched?.name || id }) },
      installedAt: Date.now(),
    });
    return { name: id, installed: true };
  }
  if (input.type === "node" && input.content) {
    const customNodes = await kvGet<(BuiltinNodeMeta & { code: string; installedAt: number })[]>(CUSTOM_NODES_KEY, []);
    const id = input.identifier || input.name || `customNode${Date.now()}`;
    const next = {
      name: id,
      displayName: input.name || id,
      version: "1.0.0",
      author: "User",
      github: "",
      description: "浏览器本机导入的自定义节点插件",
      readme: `# ${id}`,
      code: input.content,
      installedAt: Date.now(),
    };
    await kvSet(CUSTOM_NODES_KEY, [next, ...customNodes.filter((n) => n.name !== id)]);
    return { name: id, installed: true };
  }
  return { name: matched?.identifier || input.name || "plugin", installed: true };
}

export async function publishLocalMarketPlugin(input: {
  name: string;
  identifier: string;
  type: "node" | "tool" | "skill" | "agent" | "ext";
  desc?: string;
  fileName: string;
  version: string;
  fileBase64?: string;
}) {
  const customMarket = await kvGet<MarketPluginItem[]>(CUSTOM_MARKET_KEY, []);
  const content = input.fileBase64 ? atob(input.fileBase64) : "";
  const created: MarketPluginItem = {
    id: Date.now(),
    identifier: input.identifier,
    type: input.type,
    name: input.name,
    state: 1,
    version: input.version || "1.0.0",
    supplier: "我的本地插件",
    desc: input.desc || "本地发布的浏览器插件",
    fileName: input.fileName,
    payload: { content },
  };
  await kvSet(CUSTOM_MARKET_KEY, [created, ...customMarket.filter((p) => p.identifier !== input.identifier)]);
  return created;
}

// ==================== Curated Explore Projects ====================

const EXPLORE_PROJECTS = [
  {
    id: "explore-cyber-drama",
    title: "赛博雨夜短剧 · 剧本到分镜视频全流程",
    description: "结合文本拆镜、3D 白模预演机位、关键帧生图与图生视频的标准短剧生产流。",
    categoryId: "drama",
    categoryName: "短剧分镜",
    author: "Toonflow Studio",
    coverUrl:
      "data:image/svg+xml," +
      encodeURIComponent(
        `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360" viewBox="0 0 640 360"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#141e30"/><stop offset="1" stop-color="#243b55"/></linearGradient></defs><rect width="640" height="360" fill="url(#g)"/><rect x="44" y="120" width="130" height="84" rx="12" fill="#2c5364" stroke="#60a5fa"/><rect x="246" y="70" width="144" height="88" rx="12" fill="#1e3a5f" stroke="#38bdf8"/><rect x="246" y="196" width="144" height="88" rx="12" fill="#312e81" stroke="#a78bfa"/><rect x="460" y="120" width="136" height="88" rx="12" fill="#14532d" stroke="#4ade80"/><path d="M174 162 C210 162 210 114 246 114 M174 162 C210 162 210 240 246 240 M390 114 C425 114 425 164 460 164" fill="none" stroke="#93c5fd" stroke-width="3"/><text x="44" y="54" fill="#e2e8f0" font-size="22" font-family="sans-serif" font-weight="600">赛博雨夜短剧工作流</text></svg>`
      ),
    likes: 328,
    downloads: 1204,
    createdAt: Date.now() - 86400_000 * 3,
    process: {
      nodes: [
        { id: "n1", type: "remote-textNode", position: { x: 40, y: 120 }, data: { label: "剧本与拆镜", text: "第一场：午夜雨巷，女主手持旧怀表走入便利店。" } },
        { id: "n2", type: "remote-trumanMap", position: { x: 380, y: 40 }, data: { label: "3D 白模机位预演" } },
        { id: "n3", type: "remote-imageGenerationNode", position: { x: 380, y: 300 }, data: { label: "关键帧生图", prompt: "赛博雨夜便利店门口，电影级冷暖光影" } },
        { id: "n4", type: "remote-videoGenerationNode", position: { x: 760, y: 170 }, data: { label: "动态分镜视频", prompt: "缓推镜头穿过雨幕，灯光倒影微微晃动" } },
      ],
      edges: [
        { id: "e1", source: "n1", sourceHandle: "text", target: "n3", targetHandle: "in" },
        { id: "e2", source: "n3", sourceHandle: "image", target: "n4", targetHandle: "in" },
        { id: "e3", source: "n1", sourceHandle: "text", target: "n2", targetHandle: "in" },
      ],
    },
  },
  {
    id: "explore-character-sheet",
    title: "角色三视图 → 场景融合一致性模板",
    description: "先锁定主角正/侧/背三视图，再作为多参考图输入生成不同场景镜头，保证角色不跑脸。",
    categoryId: "character",
    categoryName: "角色资产",
    author: "Toonflow Studio",
    coverUrl:
      "data:image/svg+xml," +
      encodeURIComponent(
        `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360" viewBox="0 0 640 360"><defs><linearGradient id="g2" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#1f1c2c"/><stop offset="1" stop-color="#3b2f63"/></linearGradient></defs><rect width="640" height="360" fill="url(#g2)"/><rect x="64" y="110" width="150" height="120" rx="14" fill="#4c1d95" stroke="#c4b5fd"/><rect x="270" y="110" width="150" height="120" rx="14" fill="#1e3a8a" stroke="#93c5fd"/><rect x="466" y="110" width="120" height="120" rx="14" fill="#065f46" stroke="#6ee7b7"/><path d="M214 170 L270 170 M420 170 L466 170" stroke="#e9d5ff" stroke-width="3"/><text x="64" y="64" fill="#f5f3ff" font-size="22" font-family="sans-serif" font-weight="600">角色三视图一致性流水线</text></svg>`
      ),
    likes: 215,
    downloads: 860,
    createdAt: Date.now() - 86400_000 * 5,
    process: {
      nodes: [
        { id: "c1", type: "remote-textNode", position: { x: 40, y: 120 }, data: { label: "角色设定卡", text: "主角：沈清墨，黑色短发、深蓝风衣、银框眼镜。" } },
        { id: "c2", type: "remote-imageGenerationNode", position: { x: 400, y: 120 }, data: { label: "角色三视图生成", prompt: "角色正/侧/背三视图，纯白背景，国风赛博动画风格" } },
        { id: "c3", type: "remote-imageGenerationNode", position: { x: 780, y: 120 }, data: { label: "场景融合分镜", prompt: "保持角色外观一致，站在雨后霓虹天台远眺城市" } },
      ],
      edges: [
        { id: "ce1", source: "c1", sourceHandle: "text", target: "c2", targetHandle: "in" },
        { id: "ce2", source: "c2", sourceHandle: "image", target: "c3", targetHandle: "in" },
      ],
    },
  },
];

export function listExploreProjects(body?: { categoryId?: string; search?: string; searchKeyword?: string }) {
  const keyword = (body?.searchKeyword || body?.search || "").trim().toLowerCase();
  const filtered = EXPLORE_PROJECTS.filter((p) => {
    if (body?.categoryId && p.categoryId !== body.categoryId) return false;
    if (keyword && !`${p.title} ${p.description} ${p.categoryName}`.toLowerCase().includes(keyword)) return false;
    return true;
  });
  return {
    categories: [
      { id: "drama", name: "短剧分镜" },
      { id: "character", name: "角色资产" },
    ],
    list: filtered.map((p) => ({
      id: p.id,
      title: p.title,
      coverUrl: p.coverUrl,
      author: { id: "toonflow-studio", name: p.author },
      likeCount: p.likes,
      collectionCount: p.downloads,
    })),
    nextCursor: null,
  };
}

export function getExploreProjectById(id: string) {
  const p = EXPLORE_PROJECTS.find((item) => item.id === id) ?? EXPLORE_PROJECTS[0]!;
  return {
    ...p,
    author: { id: "toonflow-studio", name: p.author },
    likeCount: p.likes,
    collectionCount: p.downloads,
    videoUrl: "",
    shareUrl: "",
    canvases: [
      {
        id: "main",
        name: p.title,
        nodes: p.process.nodes.map((n) => ({
          id: n.id,
          label: String((n.data as { label?: string }).label || n.id),
          position: n.position,
          text: (n.data as { text?: string }).text,
          prompt: (n.data as { prompt?: string }).prompt,
        })),
        edges: p.process.edges,
      },
    ],
  };
}

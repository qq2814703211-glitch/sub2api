import type { NodeTypesObject } from "@vue-flow/core";
import { shallowReactive, type Component } from "vue";
import * as vueRuntime from "vue";
import * as vueFlowRuntime from "@vue-flow/core";
import * as elementPlusRuntime from "element-plus";
import "element-plus/theme-chalk/index.css";
import * as threeRuntime from "three";
import * as modelIcons from "@toonflow/model-icons";
import { runAgentLoop } from "@earendil-works/pi-agent-core";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { getLocale } from "@toonflow/i18n";
import { createPluginTranslator, ensurePluginI18nLoaded } from "./pluginI18n";

type NodeComponent = Exclude<NodeTypesObject[string], string>;

const components = shallowReactive(new Map<string, NodeComponent>());
const requests = new Map<string, Promise<NodeComponent>>();

(window as typeof window & { toonflowNodeHost?: unknown }).toonflowNodeHost = {
  vue: vueRuntime,
  vueFlow: vueFlowRuntime,
  elementPlus: elementPlusRuntime,
  three: threeRuntime,
  modelIcons,
  ai: { runAgentLoop, createAssistantMessageEventStream },
  i18n: { createTranslator: createPluginTranslator },
};

const builtinNodeLoaders: Record<string, () => Promise<{ default: NodeComponent }>> = {
  textNode: () => import("@toonflow-upstream/packages/nodes/textNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  imageNode: () => import("@toonflow-upstream/packages/nodes/imageNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  imageGenerationNode: () =>
    import("@toonflow-upstream/packages/nodes/imageGenerationNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  videoNode: () => import("@toonflow-upstream/packages/nodes/videoNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  videoGenerationNode: () =>
    import("@toonflow-upstream/packages/nodes/videoGenerationNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  audioNode: () => import("@toonflow-upstream/packages/nodes/audioNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  audioGenerationNode: () =>
    import("@toonflow-upstream/packages/nodes/audioGenerationNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  frameAnalysisNode: () =>
    import("@toonflow-upstream/packages/nodes/frameAnalysisNode/src/index.vue") as Promise<{ default: NodeComponent }>,
  trumanMap: () => import("@toonflow-upstream/packages/nodes/trumanMap/src/index.vue") as Promise<{ default: NodeComponent }>,
};

export function getNodeIcon(type?: string): Component | undefined {
  if (!type?.startsWith("remote-")) return;
  return (components.get(type.slice(7)) as { icon?: Component } | undefined)?.icon;
}

export function loadNodeComponent(name: string, url: string, force = false): Promise<NodeComponent> {
  const pending = requests.get(name);
  if (pending) return pending;

  const nodeWindow = window as typeof window & { toonflowNodes?: NodeTypesObject };
  const cached =
    components.get(name) ??
    (Object.hasOwn(nodeWindow.toonflowNodes ?? {}, name) ? nodeWindow.toonflowNodes?.[name] : undefined);
  if (!force && cached && (typeof cached === "object" || typeof cached === "function")) {
    components.set(name, cached);
    return ensurePluginI18nLoaded(name, getLocale()).then(() => cached);
  }
  components.delete(name);

  const request = (async (): Promise<NodeComponent> => {
    const builtinLoader = builtinNodeLoaders[name];
    if (builtinLoader) {
      await ensurePluginI18nLoaded(name, getLocale());
      if (name === "textNode" || name === "trumanMap") {
        await import("@/lib/tiptapHost");
      }
      const mod = await builtinLoader();
      const component = mod.default;
      if (!component) throw new Error(`内置节点模块未导出默认组件：${name}`);
      return component;
    }

    // Custom user-installed node plugin loaded from browser IndexedDB via /api/nodes/files
    const scriptUrl = force ? `${url}${url.includes("?") ? "&" : "?"}reload=${crypto.randomUUID()}` : url;
    const response = await fetch(scriptUrl);
    if (!response.ok) throw new Error(`节点脚本加载失败（HTTP ${response.status}）`);
    const code = await response.text();
    if (code.includes("toonflowTiptapHost")) await import("@/lib/tiptapHost");
    const blob = new Blob([code], { type: "text/javascript" });
    const blobUrl = URL.createObjectURL(blob);
    try {
      const mod = (await import(/* @vite-ignore */ blobUrl)) as { default?: NodeComponent };
      if (mod?.default && (typeof mod.default === "object" || typeof mod.default === "function")) {
        return mod.default;
      }
      const globalComp = nodeWindow.toonflowNodes?.[name];
      if (globalComp && (typeof globalComp === "object" || typeof globalComp === "function")) {
        return globalComp;
      }
      throw new Error(`自定义节点未导出有效组件：${name}`);
    } finally {
      URL.revokeObjectURL(blobUrl);
    }
  })()
    .then((component) => {
      components.set(name, component);
      return component;
    })
    .finally(() => {
      requests.delete(name);
    });

  requests.set(name, request);
  return request;
}

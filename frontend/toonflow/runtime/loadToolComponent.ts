import * as vueRuntime from "vue";
import * as elementPlusRuntime from "element-plus";
import axios from "axios";
import formCreate from "@form-create/element-ui";
import { createTranslator } from "@toonflow/i18n/vue";
import type { Component } from "vue";

export {
  createBrowserAsr,
  isBrowserAsrSupported,
  isBrowserAudioAsrSupported,
  transcribeBrowserAudio,
} from "@toonflow/asr";
export * from "@toonflow/plugin-server/localModelClient";
export type {
  BrowserAsrState,
  BrowserAsrResult,
  BrowserAsrError,
  BrowserAsrOptions,
  BrowserAudioAsrOptions,
} from "@toonflow/asr";

const toolWindow = window as typeof window & {
  toonflowToolHost?: {
    vue: typeof vueRuntime;
    elementPlus: typeof elementPlusRuntime;
    axios: typeof axios;
    formCreate: typeof formCreate;
    i18n: { createTranslator: typeof createTranslator };
  };
  toonflowToolViews?: Record<string, Record<string, Component>>;
};
toolWindow.toonflowToolHost = {
  vue: vueRuntime,
  elementPlus: elementPlusRuntime,
  axios,
  formCreate,
  i18n: { createTranslator },
};

const CANVAS_FOCUS_TOOLS = new Set([
  "getCanvas",
  "findCanvasNodes",
  "getCanvasNodes",
  "getCanvasEdges",
  "getNodeTools",
  "addCanvas",
  "switchCanvas",
  "renameCanvas",
  "addNode",
  "deleteNodes",
  "moveNodes",
  "renameNodes",
  "connectNodes",
  "deleteEdges",
  "selectNodes",
  "arrangeCanvas",
  "fitCanvas",
  "nodeTools",
]);

const cachedComponents = new Map<string, Component>();
const pendingLoads = new Map<string, Promise<Component | undefined>>();

export function getCachedToolComponent(toolName: string): Component | undefined {
  return cachedComponents.get(toolName);
}

export async function loadToolComponent(toolName: string): Promise<Component | undefined> {
  const existing = cachedComponents.get(toolName);
  if (existing) return existing;

  const inflight = pendingLoads.get(toolName);
  if (inflight) return inflight;

  const promise = (async (): Promise<Component | undefined> => {
    if (toolName === "askUser") {
      const mod = await import("@toonflow-upstream/packages/tools/askUser/src/questionCard.vue");
      const comp = vueRuntime.markRaw(mod.default);
      cachedComponents.set(toolName, comp);
      return comp;
    }
    if (CANVAS_FOCUS_TOOLS.has(toolName)) {
      const mod = await import("@toonflow-upstream/packages/tools/canvas/src/nodeFocusCard.vue");
      const comp = vueRuntime.markRaw(mod.default);
      cachedComponents.set(toolName, comp);
      return comp;
    }
    for (const pack of Object.values(toolWindow.toonflowToolViews ?? {})) {
      const custom = Object.hasOwn(pack, toolName) ? pack[toolName] : undefined;
      if (custom && (typeof custom === "object" || typeof custom === "function")) {
        const comp = vueRuntime.markRaw(custom);
        cachedComponents.set(toolName, comp);
        return comp;
      }
    }
    return undefined;
  })().finally(() => {
    pendingLoads.delete(toolName);
  });

  pendingLoads.set(toolName, promise);
  return promise;
}

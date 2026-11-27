import axios from "axios";
import * as vue from "vue";
import * as elementPlus from "element-plus";
import * as i18n from "@toonflow/i18n/vue";
import { saveSettings, settings } from "@/stores/settings";
import { defineExt, type ExtDefinition, type ExtResource } from "@toonflow/ext-scaffold/runtime";

export type InstalledExtension = Omit<ExtDefinition, "load"> & {
  name: string;
  enabled: boolean;
  url: string;
  version: string;
  revision?: string;
  loadError?: string;
  config?: Record<string, unknown>;
};

const host = window as typeof window & { toonflowExts?: Record<string, ExtDefinition> };
Object.assign(host, { toonflowExtHost: { vue, elementPlus, i18n } });

const builtinExtLoaders: Record<string, () => Promise<{ default: ExtDefinition }>> = {
  "ext-canvasNode": () => import("../upstream/packages/ext/canvasNode/src/index.ts"),
  "ext-image": () => import("../upstream/packages/ext/image/src/index.ts"),
  "ext-md": () => import("../upstream/packages/ext/md/src/index.ts"),
  "ext-media": () => import("../upstream/packages/ext/media/src/index.ts"),
  "ext-txt": () => import("../upstream/packages/ext/txt/src/index.ts"),
  canvasNode: () => import("../upstream/packages/ext/canvasNode/src/index.ts"),
  image: () => import("../upstream/packages/ext/image/src/index.ts"),
  md: () => import("../upstream/packages/ext/md/src/index.ts"),
  media: () => import("../upstream/packages/ext/media/src/index.ts"),
  txt: () => import("../upstream/packages/ext/txt/src/index.ts"),
};

const requests = new Map<string, Promise<ExtDefinition>>();
const configSaves = new Map<string, Promise<void>>();
let installed: Promise<InstalledExtension[]> | undefined;
let revision = 0;
let configRevision = 0;
const configChanges = new Map<string, { revision: number; config: Record<string, unknown> }>();
const definitions = vue.shallowRef<InstalledExtension[]>([]);

export const extensionAssociations = vue.computed<Record<string, string>>(() => {
  const value = settings.value.documentExtensions;
  return value && typeof value === "object" && !Array.isArray(value)
    ? Object.fromEntries(
        Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === "string")
      )
    : {};
});

export async function setExtensionAssociation(suffix: string, id?: string) {
  if (id) {
    const extensions = await listExtensions();
    if (
      !extensions.some(
        (extension) =>
          extension.id === id &&
          extension.enabled &&
          !extension.loadError &&
          (suffix === "canvasNode"
            ? extension.resourceKind === "canvasNode"
            : extension.resourceKind === "file" && extension.extensions.includes(suffix))
      )
    ) {
      throw new Error("所选扩展未启用或不支持此文件类型");
    }
  }
  await saveSettings((current) => {
    const previous = current.documentExtensions;
    const associations =
      previous && typeof previous === "object" && !Array.isArray(previous) ? { ...previous } : {};
    if (id) Object.assign(associations, { [suffix]: id });
    else Reflect.deleteProperty(associations, suffix);
    return { documentExtensions: associations };
  });
}

function invalidateExtensions() {
  installed = undefined;
  requests.clear();
  revision++;
}

export function listExtensions(refresh = false): Promise<InstalledExtension[]> {
  if (refresh) invalidateExtensions();
  if (!installed) {
    const requestedRevision = revision;
    const requestedConfigRevision = configRevision;
    installed = axios
      .get<{ data: InstalledExtension[] }>("/api/ext/list", { headers: { "x-toonflow-workspace": "1" } })
      .then(({ data }): InstalledExtension[] | Promise<InstalledExtension[]> => {
        if (requestedRevision !== revision) return listExtensions();
        definitions.value = data.data.map((extension) => {
          const change = configChanges.get(extension.id);
          return change && change.revision > requestedConfigRevision
            ? { ...extension, config: change.config }
            : extension;
        });
        for (const [id, change] of configChanges) {
          if (change.revision <= requestedConfigRevision) configChanges.delete(id);
        }
        return definitions.value;
      })
      .catch((error) => {
        if (requestedRevision !== revision) return listExtensions();
        installed = undefined;
        throw error;
      });
  }
  return installed.then(() => definitions.value);
}

function loadExtension(extension: InstalledExtension): Promise<ExtDefinition> {
  const requestedRevision = revision;
  const key = JSON.stringify([extension.id, extension.url, extension.version, extension.revision, requestedRevision]);
  let request = requests.get(key);
  if (!request) {
    request = (async () => {
      const builtinLoader = builtinExtLoaders[extension.id] ?? builtinExtLoaders[extension.name];
      if (builtinLoader) {
        if (extension.id === "ext-md" || extension.name === "md") {
          await import("@/lib/tiptapHost");
        }
        const mod = await builtinLoader();
        return defineExt(mod.default);
      }
      const response = await fetch(extension.url);
      if (!response.ok) throw new Error(`扩展脚本加载失败（HTTP ${response.status}）`);
      const code = await response.text();
      if (code.includes("toonflowTiptapHost")) await import("@/lib/tiptapHost");
      const blob = new Blob([code], { type: "text/javascript" });
      const blobUrl = URL.createObjectURL(blob);
      try {
        const mod = (await import(/* @vite-ignore */ blobUrl)) as { default?: ExtDefinition };
        if (mod?.default) return defineExt(mod.default);
        const found = host.toonflowExts?.[extension.id] ?? host.toonflowExts?.[extension.name];
        if (found) return defineExt(found);
        throw new Error(`扩展未导出有效定义：${extension.id}`);
      } finally {
        URL.revokeObjectURL(blobUrl);
      }
    })().catch((error) => {
      requests.delete(key);
      throw error;
    });
    requests.set(key, request);
  }
  return request;
}

export async function extensionCandidates(resource: ExtResource) {
  await listExtensions();
  return matchingExtensions(resource);
}

function matchingExtensions(resource: ExtResource) {
  const suffix = resource.path.split(".").at(-1)?.toLowerCase();
  return definitions.value.filter(
    (extension) =>
      extension.enabled &&
      !extension.loadError &&
      extension.resourceKind === resource.kind &&
      (resource.kind === "canvasNode" || extension.extensions.includes(suffix ?? ""))
  );
}

export function extensionIcon(resource: ExtResource) {
  const candidates = matchingExtensions(resource);
  const key = resource.kind === "canvasNode" ? "canvasNode" : resource.path.split(".").at(-1)!.toLowerCase();
  return (
    candidates.find((extension) => extension.id === extensionAssociations.value[key]) ??
    (candidates.length === 1 ? candidates[0] : undefined)
  )?.icon;
}

export function extensionConfig(id: string) {
  return definitions.value.find((extension) => extension.id === id)?.config ?? configChanges.get(id)?.config ?? {};
}

export function extensionRevision(id: string) {
  const extension = definitions.value.find((extension) => extension.id === id);
  return extension?.revision ?? extension?.version;
}

export function updateExtensionConfig(id: string, config: Record<string, unknown>) {
  configChanges.set(id, { revision: ++configRevision, config });
  definitions.value = definitions.value.map((extension) =>
    extension.id === id ? { ...extension, config } : extension
  );
}

export function saveExtensionConfig(name: string, config: Record<string, unknown>) {
  const snapshot = structuredClone(vue.toRaw(config));
  const saving = (configSaves.get(name) ?? Promise.resolve())
    .catch(() => {})
    .then(async () => {
      const { data } = await axios.put<{ data: Record<string, unknown> }>(
        "/api/ext/save",
        { name, config: snapshot },
        { headers: { "x-toonflow-workspace": "1" } }
      );
      window.dispatchEvent(new CustomEvent("toonflow:ext-config-updated", { detail: { name, config: data.data } }));
    });
  configSaves.set(name, saving);
  return saving;
}

function extensionsUpdated() {
  invalidateExtensions();
  void listExtensions().catch(() => {});
}

function configUpdated(event: Event) {
  const detail = (event as CustomEvent<{ name?: unknown; config?: unknown }>).detail;
  if (
    typeof detail?.name === "string" &&
    detail.config &&
    typeof detail.config === "object" &&
    !Array.isArray(detail.config)
  ) {
    updateExtensionConfig(detail.name, detail.config as Record<string, unknown>);
  }
}

window.addEventListener("toonflow:ext-updated", extensionsUpdated);
window.addEventListener("toonflow:ext-config-updated", configUpdated);
import.meta.hot?.dispose(() => {
  window.removeEventListener("toonflow:ext-updated", extensionsUpdated);
  window.removeEventListener("toonflow:ext-config-updated", configUpdated);
});

export async function resolveExtension(resource: ExtResource, id: string): Promise<ExtDefinition> {
  const extension = (await extensionCandidates(resource)).find((ext) => ext.id === id);
  if (!extension) throw new Error(`尚未启用支持此文件的扩展，请到插件市场安装或启用：${resource.label}`);
  return defineExt({ ...extension, load: async () => (await loadExtension(extension)).load() });
}

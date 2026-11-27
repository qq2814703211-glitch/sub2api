import { shallowRef } from "vue";
import {
  getLocale,
  msg,
  subscribeLocale,
  translate as coreTranslate,
  type Catalogs,
  type Locale,
  type MessageDescriptor,
} from "../upstream/packages/i18n/src/index";
import { locale } from "../upstream/packages/i18n/src/vue";
import {
  loadCorePluginLocaleBundle,
  loadTrumanMapLocaleCatalog,
  type NonDefaultLocale,
  type PluginI18nScope,
} from "./pluginI18nLoader";

export type { PluginI18nScope } from "./pluginI18nLoader";

interface LoadedCatalogEntry {
  dict: Record<string, string>;
  catalogs: Catalogs;
}

const ALL_PLUGIN_SCOPES = new Set<PluginI18nScope>([
  "nodeScaffold",
  "nodeShared",
  "textNode",
  "imageNode",
  "imageGenerationNode",
  "videoNode",
  "videoGenerationNode",
  "audioNode",
  "audioGenerationNode",
  "frameAnalysisNode",
  "trumanMap",
  "askUser",
  "canvas",
]);

const LIGHT_NODE_SCOPES: readonly PluginI18nScope[] = [
  "imageNode",
  "videoNode",
  "audioNode",
  "textNode",
  "imageGenerationNode",
  "videoGenerationNode",
  "audioGenerationNode",
  "frameAnalysisNode",
];

const SCOPE_LOOKUP_ORDER: Record<PluginI18nScope, readonly PluginI18nScope[]> = {
  nodeScaffold: ["nodeScaffold", "nodeShared", ...LIGHT_NODE_SCOPES, "trumanMap"],
  nodeShared: ["nodeShared", "nodeScaffold", ...LIGHT_NODE_SCOPES, "trumanMap"],
  textNode: ["textNode", "nodeShared", "nodeScaffold"],
  imageNode: ["imageNode", "nodeShared", "nodeScaffold"],
  imageGenerationNode: ["imageGenerationNode", "imageNode", "nodeShared", "nodeScaffold"],
  videoNode: ["videoNode", "imageNode", "nodeShared", "nodeScaffold"],
  videoGenerationNode: ["videoGenerationNode", "videoNode", "imageNode", "nodeShared", "nodeScaffold"],
  audioNode: ["audioNode", "videoNode", "imageNode", "nodeShared", "nodeScaffold"],
  audioGenerationNode: ["audioGenerationNode", "audioNode", "videoNode", "imageNode", "nodeShared", "nodeScaffold"],
  frameAnalysisNode: ["frameAnalysisNode", "videoNode", "imageNode", "nodeShared", "nodeScaffold"],
  trumanMap: ["trumanMap", "nodeShared", "nodeScaffold"],
  askUser: ["askUser"],
  canvas: ["canvas"],
};

const catalogVersion = shallowRef(0);
const loadedCatalogs = new Map<PluginI18nScope, Map<NonDefaultLocale, LoadedCatalogEntry>>();
const loadedCoreLocales = new Set<NonDefaultLocale>();
const pendingCoreLocales = new Map<NonDefaultLocale, Promise<void>>();
const loadedTrumanMapLocales = new Set<NonDefaultLocale>();
const pendingTrumanMapLocales = new Map<NonDefaultLocale, Promise<void>>();
let trumanMapActivated = false;

function storeCatalog(scope: PluginI18nScope, targetLocale: NonDefaultLocale, dict: Record<string, string>): void {
  let byLocale = loadedCatalogs.get(scope);
  if (!byLocale) {
    byLocale = new Map<NonDefaultLocale, LoadedCatalogEntry>();
    loadedCatalogs.set(scope, byLocale);
  }
  byLocale.set(targetLocale, {
    dict,
    catalogs: { [targetLocale]: dict },
  });
}

export function isPluginI18nScope(value: string): value is PluginI18nScope {
  return ALL_PLUGIN_SCOPES.has(value as PluginI18nScope);
}

export function ensureCorePluginI18nLoaded(targetLocale: Locale = getLocale()): Promise<void> {
  if (targetLocale === "zh-CN") return Promise.resolve();
  if (loadedCoreLocales.has(targetLocale)) return Promise.resolve();
  const inflight = pendingCoreLocales.get(targetLocale);
  if (inflight) return inflight;

  const task = loadCorePluginLocaleBundle(targetLocale)
    .then((bundle) => {
      for (const [rawScope, dict] of Object.entries(bundle)) {
        if (dict && isPluginI18nScope(rawScope)) {
          storeCatalog(rawScope, targetLocale, dict);
        }
      }
      loadedCoreLocales.add(targetLocale);
      catalogVersion.value += 1;
    })
    .catch((err) => {
      console.warn(`[toonflow-i18n] 加载插件字典失败 (${targetLocale}):`, err);
    })
    .finally(() => {
      pendingCoreLocales.delete(targetLocale);
    });

  pendingCoreLocales.set(targetLocale, task);
  return task;
}

function ensureTrumanMapI18nLoaded(targetLocale: Locale = getLocale()): Promise<void> {
  trumanMapActivated = true;
  if (targetLocale === "zh-CN") return Promise.resolve();
  if (loadedTrumanMapLocales.has(targetLocale)) return Promise.resolve();
  const inflight = pendingTrumanMapLocales.get(targetLocale);
  if (inflight) return inflight;

  const task = loadTrumanMapLocaleCatalog(targetLocale)
    .then((dict) => {
      if (dict) storeCatalog("trumanMap", targetLocale, dict);
      loadedTrumanMapLocales.add(targetLocale);
      catalogVersion.value += 1;
    })
    .catch((err) => {
      console.warn(`[toonflow-i18n] 加载 TrumanMap 字典失败 (${targetLocale}):`, err);
    })
    .finally(() => {
      pendingTrumanMapLocales.delete(targetLocale);
    });

  pendingTrumanMapLocales.set(targetLocale, task);
  return task;
}

export function ensurePluginI18nLoaded(scopeOrName: string, targetLocale: Locale = getLocale()): Promise<void> {
  if (targetLocale === "zh-CN") return Promise.resolve();
  if (scopeOrName === "trumanMap") {
    return Promise.all([
      ensureCorePluginI18nLoaded(targetLocale),
      ensureTrumanMapI18nLoaded(targetLocale),
    ]).then(() => undefined);
  }
  return ensureCorePluginI18nLoaded(targetLocale);
}

export function initPluginI18n(): void {
  const current = getLocale();
  if (current !== "zh-CN") {
    void ensureCorePluginI18nLoaded(current);
  }
}

subscribeLocale((nextLocale) => {
  if (nextLocale === "zh-CN") return;
  void ensureCorePluginI18nLoaded(nextLocale);
  if (trumanMapActivated) {
    void ensureTrumanMapI18nLoaded(nextLocale);
  }
});

function translateFromScopes(
  scopes: readonly PluginI18nScope[],
  message: string | MessageDescriptor,
  values: Record<string, unknown> | undefined,
  currentLocale: Locale,
  triggerTrumanMapLoad: boolean
): string {
  // Read reactive version so components re-render automatically when async locale chunks finish loading.
  void catalogVersion.value;

  if (currentLocale === "zh-CN") {
    return coreTranslate(message, values, currentLocale);
  }

  if (!loadedCoreLocales.has(currentLocale)) {
    void ensureCorePluginI18nLoaded(currentLocale);
  }
  if (triggerTrumanMapLoad && !loadedTrumanMapLocales.has(currentLocale)) {
    void ensureTrumanMapI18nLoaded(currentLocale);
  }

  const id = typeof message === "string" ? message : message.id;
  for (const scope of scopes) {
    const entry = loadedCatalogs.get(scope)?.get(currentLocale);
    if (entry && Object.hasOwn(entry.dict, id) && typeof entry.dict[id] === "string") {
      return coreTranslate(message, values, currentLocale, entry.catalogs);
    }
  }

  return coreTranslate(message, values, currentLocale);
}

export function createScopedPluginTranslator(scope: PluginI18nScope) {
  const scopes = SCOPE_LOOKUP_ORDER[scope];
  const isTrumanMap = scope === "trumanMap";

  const translateText = (message: string | MessageDescriptor, values?: Record<string, unknown>): string =>
    translateFromScopes(scopes, message, values, locale.value, isTrumanMap);

  const t = (strings: TemplateStringsArray, ...values: unknown[]): string => translateText(msg(strings, ...values));

  return {
    locale,
    msg,
    translate: translateText,
    t,
  };
}

export function createPluginTranslator(dictionaries: Catalogs) {
  const fallbackScopes = SCOPE_LOOKUP_ORDER.nodeShared;

  const translateText = (message: string | MessageDescriptor, values?: Record<string, unknown>): string => {
    const currentLocale = locale.value;
    void catalogVersion.value;
    const id = typeof message === "string" ? message : message.id;
    const ownCatalog = Object.hasOwn(dictionaries, currentLocale) ? dictionaries[currentLocale] : undefined;
    if (ownCatalog && Object.hasOwn(ownCatalog, id) && typeof ownCatalog[id] === "string") {
      return coreTranslate(message, values, currentLocale, dictionaries);
    }
    return translateFromScopes(fallbackScopes, message, values, currentLocale, false);
  };

  const t = (strings: TemplateStringsArray, ...values: unknown[]): string => translateText(msg(strings, ...values));

  return {
    locale,
    msg,
    translate: translateText,
    t,
  };
}

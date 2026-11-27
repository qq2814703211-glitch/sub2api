import type { Locale } from "../upstream/packages/i18n/src/index";

export type PluginI18nScope =
  | "nodeScaffold"
  | "nodeShared"
  | "textNode"
  | "imageNode"
  | "imageGenerationNode"
  | "videoNode"
  | "videoGenerationNode"
  | "audioNode"
  | "audioGenerationNode"
  | "frameAnalysisNode"
  | "trumanMap"
  | "askUser"
  | "canvas";

export type NonDefaultLocale = Exclude<Locale, "zh-CN">;
export type CorePluginLocaleBundle = Partial<Record<PluginI18nScope, Record<string, string>>>;

interface CatalogModule {
  default?: Record<string, string>;
}

const LOCALE_TO_FILENAME: Record<NonDefaultLocale, string> = {
  "zh-TW": "zhTw",
  en: "en",
  ja: "ja",
  ru: "ru",
  vi: "vi",
  th: "th",
  ko: "ko",
  hi: "hi",
  id: "id",
  ms: "ms",
  fil: "fil",
  bn: "bn",
  ur: "ur",
  ta: "ta",
  te: "te",
  mr: "mr",
  pa: "pa",
  ar: "ar",
  fa: "fa",
  tr: "tr",
  pt: "pt",
};

const FILENAME_TO_LOCALE = new Map<string, NonDefaultLocale>(
  Object.entries(LOCALE_TO_FILENAME).map(([code, fileName]) => [fileName, code as NonDefaultLocale])
);

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

const corePluginLocaleModules = import.meta.glob<CatalogModule>([
  "../upstream/packages/nodeScaffold/i18n/*.json",
  "!../upstream/packages/nodeScaffold/i18n/zhCn.json",
  "../upstream/packages/nodeShared/i18n/*.json",
  "!../upstream/packages/nodeShared/i18n/zhCn.json",
  "../upstream/packages/nodes/*/i18n/*.json",
  "!../upstream/packages/nodes/*/i18n/zhCn.json",
  "!../upstream/packages/nodes/trumanMap/i18n/*.json",
  "../upstream/packages/tools/askUser/i18n/*.json",
  "!../upstream/packages/tools/askUser/i18n/zhCn.json",
  "../upstream/packages/tools/canvas/i18n/*.json",
  "!../upstream/packages/tools/canvas/i18n/zhCn.json",
]);

const trumanMapLocaleModules = import.meta.glob<CatalogModule>([
  "../upstream/packages/nodes/trumanMap/i18n/*.json",
  "!../upstream/packages/nodes/trumanMap/i18n/zhCn.json",
]);

interface IndexedLoader {
  scope: PluginI18nScope;
  load: () => Promise<CatalogModule>;
}

const coreLoadersByLocale = new Map<NonDefaultLocale, IndexedLoader[]>();
const trumanMapLoadersByLocale = new Map<NonDefaultLocale, () => Promise<CatalogModule>>();

function parseModulePath(modulePath: string): { scope: PluginI18nScope; locale: NonDefaultLocale } | undefined {
  const normalized = modulePath.replaceAll("\\", "/");
  const match = normalized.match(
    /\/packages\/(?:(nodeScaffold|nodeShared)|nodes\/([^/]+)|tools\/(askUser|canvas))\/i18n\/([^/]+)\.json$/
  );
  if (!match) return undefined;
  const rawScope = match[1] ?? match[2] ?? match[3];
  const fileName = match[4];
  if (!rawScope || !fileName) return undefined;
  const resolvedLocale = FILENAME_TO_LOCALE.get(fileName);
  if (!resolvedLocale || !ALL_PLUGIN_SCOPES.has(rawScope as PluginI18nScope)) return undefined;
  return { scope: rawScope as PluginI18nScope, locale: resolvedLocale };
}

for (const [modulePath, loader] of Object.entries(corePluginLocaleModules)) {
  const parsed = parseModulePath(modulePath);
  if (!parsed) continue;
  const list = coreLoadersByLocale.get(parsed.locale) ?? [];
  list.push({ scope: parsed.scope, load: loader });
  coreLoadersByLocale.set(parsed.locale, list);
}

for (const [modulePath, loader] of Object.entries(trumanMapLocaleModules)) {
  const parsed = parseModulePath(modulePath);
  if (!parsed || parsed.scope !== "trumanMap") continue;
  trumanMapLoadersByLocale.set(parsed.locale, loader);
}

function extractDictionary(mod: CatalogModule): Record<string, string> | undefined {
  const candidate = mod?.default ?? (mod as unknown);
  if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) return undefined;
  return candidate as Record<string, string>;
}

export async function loadCorePluginLocaleBundle(
  targetLocale: NonDefaultLocale
): Promise<CorePluginLocaleBundle> {
  const loaders = coreLoadersByLocale.get(targetLocale) ?? [];
  const bundle: CorePluginLocaleBundle = {};
  await Promise.all(
    loaders.map(async ({ scope, load }) => {
      const dict = extractDictionary(await load());
      if (dict) bundle[scope] = dict;
    })
  );
  return bundle;
}

export async function loadTrumanMapLocaleCatalog(
  targetLocale: NonDefaultLocale
): Promise<Record<string, string> | undefined> {
  const loader = trumanMapLoadersByLocale.get(targetLocale);
  if (!loader) return undefined;
  return extractDictionary(await loader());
}

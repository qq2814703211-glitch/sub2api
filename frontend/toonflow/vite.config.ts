import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import vue from "@vitejs/plugin-vue";
import components from "unplugin-vue-components/vite";
import { ElementPlusResolver } from "unplugin-vue-components/resolvers";
import pxtorem from "postcss-pxtorem";
import { transformSource } from "./upstream/packages/i18n/src/transform.ts";

const rootDir = dirname(fileURLToPath(import.meta.url));
const upstreamDir = resolve(rootDir, "upstream");
const webSrcDir = resolve(upstreamDir, "apps/web/src");
const pkgsDir = resolve(upstreamDir, "packages");
const serverSrcDir = resolve(upstreamDir, "apps/server/src");

function loadTablerIconMap(): Map<string, string> {
  const map = new Map<string, string>();
  try {
    const entry = readFileSync(
      resolve(rootDir, "../node_modules/@tabler/icons-vue/dist/esm/tabler-icons-vue.mjs"),
      "utf8"
    );
    const lineRe = /^export\s*\{([^}]+)\}\s*from\s*['"]\.\/icons\/([^'"]+)\.mjs['"];?$/gm;
    let match: RegExpExecArray | null;
    while ((match = lineRe.exec(entry)) !== null) {
      const exportsChunk = match[1] ?? "";
      const target = match[2] ?? "";
      if (!target) continue;
      for (const spec of exportsChunk.split(",")) {
        const aliasMatch = spec.trim().match(/^default\s+as\s+([A-Za-z0-9_$]+)$/);
        if (aliasMatch?.[1]) {
          map.set(aliasMatch[1], target);
        }
      }
    }
  } catch {
    // Fallback to barrel imports if @tabler/icons-vue layout changes
  }
  return map;
}

const tablerIconMap = loadTablerIconMap();

// Rewrite named imports from "@tabler/icons-vue" to direct per-icon subpaths so
// Rollup (Vite 5) does not parse all 6,500+ barrel icon files into memory.
function tablerIconsDirectImportPlugin(): Plugin {
  return {
    name: "toonflowTablerIconsDirectImport",
    transform(code, id) {
      if (tablerIconMap.size === 0) return;
      if (id.includes("/node_modules/") || !code.includes("@tabler/icons-vue")) return;
      let changed = false;
      const rewritten = code.replace(
        /import\s*\{([^}]*)\}\s*from\s*["']@tabler\/icons-vue["']\s*;?/g,
        (fullMatch, specifiers: string) => {
          const directImports: string[] = [];
          const remaining: string[] = [];
          for (const raw of specifiers.split(",")) {
            const cleaned = raw.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "").trim();
            if (!cleaned) continue;
            if (/^type\s+/.test(cleaned)) {
              remaining.push(cleaned);
              continue;
            }
            const m = cleaned.match(/^([A-Za-z0-9_$]+)(?:\s+as\s+([A-Za-z0-9_$]+))?$/);
            const imported = m?.[1];
            const local = m?.[2] ?? imported;
            const target = imported ? tablerIconMap.get(imported) : undefined;
            if (target && local) {
              directImports.push(`import ${local} from "@tabler/icons-vue/icons/${target}";`);
            } else {
              remaining.push(cleaned);
            }
          }
          if (directImports.length === 0) return fullMatch;
          changed = true;
          if (remaining.length > 0) {
            directImports.push(`import { ${remaining.join(", ")} } from "@tabler/icons-vue";`);
          }
          return directImports.join("\n");
        }
      );
      if (!changed) return;
      return { code: rewritten, map: null };
    },
  };
}

function toonflowJsonImportAttrPlugin(): Plugin {
  return {
    name: "toonflowJsonImportAttributes",
    enforce: "pre",
    transform(source, id) {
      const normalized = id.replaceAll("\\", "/");
      if (!normalized.includes("/toonflow/upstream/packages/i18n/src/index.ts")) return;
      if (!source.includes('with { type: "json" }')) return;
      return {
        code: source.replace(/\s+with\s*\{\s*type:\s*"json"\s*\}/g, ""),
        map: null,
      };
    },
  };
}

function toonflowI18nTransformPlugin(): Plugin {
  return {
    name: "toonflowBrowserI18n",
    enforce: "pre",
    transform(source, id) {
      const normalized = id.replaceAll("\\", "/");
      if (normalized.includes("/node_modules/")) return;
      if (
        !/\/toonflow\/upstream\/(?:apps\/web\/src|packages\/(?:providerWeb|nodeShared\/src|nodeScaffold\/src|nodes\/[^/]+\/src|ext\/[^/]+\/src|tools\/[^/]+\/src|multiTenant\/(?:userWeb|adminWeb)))\//.test(
          normalized
        )
      ) {
        return;
      }
      if (normalized.includes("?")) return;
      if (!/\.(?:vue|ts|tsx|js|mjs)$/.test(normalized)) return;
      return transformSource(source, normalized);
    },
  };
}

const SCOPED_PLUGIN_I18N_PREFIX = "\0toonflow-plugin-i18n:";
const PLUGIN_I18N_LOADER_VIRTUAL_ID = "\0toonflow-plugin-i18n-loader";

const NON_DEFAULT_LOCALE_FILES: ReadonlyArray<readonly [string, string]> = [
  ["zh-TW", "zhTw"],
  ["en", "en"],
  ["ja", "ja"],
  ["ru", "ru"],
  ["vi", "vi"],
  ["th", "th"],
  ["ko", "ko"],
  ["hi", "hi"],
  ["id", "id"],
  ["ms", "ms"],
  ["fil", "fil"],
  ["bn", "bn"],
  ["ur", "ur"],
  ["ta", "ta"],
  ["te", "te"],
  ["mr", "mr"],
  ["pa", "pa"],
  ["ar", "ar"],
  ["fa", "fa"],
  ["tr", "tr"],
  ["pt", "pt"],
];

const CORE_PLUGIN_I18N_DIRS: ReadonlyArray<readonly [string, string]> = [
  ["nodeScaffold", "nodeScaffold/i18n"],
  ["nodeShared", "nodeShared/i18n"],
  ["textNode", "nodes/textNode/i18n"],
  ["imageNode", "nodes/imageNode/i18n"],
  ["imageGenerationNode", "nodes/imageGenerationNode/i18n"],
  ["videoNode", "nodes/videoNode/i18n"],
  ["videoGenerationNode", "nodes/videoGenerationNode/i18n"],
  ["audioNode", "nodes/audioNode/i18n"],
  ["audioGenerationNode", "nodes/audioGenerationNode/i18n"],
  ["frameAnalysisNode", "nodes/frameAnalysisNode/i18n"],
  ["askUser", "tools/askUser/i18n"],
  ["canvas", "tools/canvas/i18n"],
];

function buildCoreLocalePayload(fileName: string): string {
  const bundle: Record<string, Record<string, string>> = {};
  for (const [scope, relDir] of CORE_PLUGIN_I18N_DIRS) {
    const raw = readFileSync(resolve(pkgsDir, relDir, `${fileName}.json`), "utf8");
    bundle[scope] = JSON.parse(raw) as Record<string, string>;
  }
  return JSON.stringify(bundle);
}

function buildTrumanMapLocalePayload(fileName: string): string {
  const raw = readFileSync(resolve(pkgsDir, "nodes/trumanMap/i18n", `${fileName}.json`), "utf8");
  return JSON.stringify(JSON.parse(raw));
}

function shortContentHash(content: string): string {
  return createHash("sha256").update(content).digest("hex").slice(0, 8);
}

function detectPluginI18nScope(importer?: string): string | undefined {
  if (!importer) return undefined;
  const normalized = importer.replaceAll("\\", "/").split("?")[0] ?? "";
  const nodeMatch = normalized.match(/\/toonflow\/upstream\/packages\/nodes\/([^/]+)\//);
  if (nodeMatch?.[1]) return nodeMatch[1];
  if (/\/toonflow\/upstream\/packages\/nodeShared\//.test(normalized)) return "nodeShared";
  if (/\/toonflow\/upstream\/packages\/nodeScaffold\//.test(normalized)) return "nodeScaffold";
  const toolMatch = normalized.match(/\/toonflow\/upstream\/packages\/tools\/(askUser|canvas)\//);
  if (toolMatch?.[1]) return toolMatch[1];
  return undefined;
}

// Route "@toonflow/i18n/vue" per upstream plugin/shared package so each node & tool
// resolves its own 22-locale dictionary + workspace deps + host fallback, matching upstream pluginI18n.
// Emit plugin i18n JSON dictionaries as static content-hashed assets (fetched via native Response.json())
// so Rollup never parses ~320k JSON keys as JS AST during build.
function toonflowPluginI18nPlugin(): Plugin {
  const hostVueI18nPath = resolve(pkgsDir, "i18n/src/vue.ts").replaceAll("\\", "/");
  const pluginI18nRuntimePath = resolve(rootDir, "runtime/pluginI18n.ts").replaceAll("\\", "/");

  let assetManifest:
    | {
        core: Record<string, string>;
        trumanMap: Record<string, string>;
        byAssetPath: Map<string, { kind: "core" | "trumanMap"; fileName: string }>;
      }
    | undefined;

  function ensureAssetManifest() {
    if (assetManifest) return assetManifest;
    const core: Record<string, string> = {};
    const trumanMap: Record<string, string> = {};
    const byAssetPath = new Map<string, { kind: "core" | "trumanMap"; fileName: string }>();

    for (const [localeCode, fileName] of NON_DEFAULT_LOCALE_FILES) {
      const corePayload = buildCoreLocalePayload(fileName);
      const coreAsset = `assets/i18n-plugins-${fileName}-${shortContentHash(corePayload)}.json`;
      core[localeCode] = coreAsset;
      byAssetPath.set(coreAsset, { kind: "core", fileName });

      const trumanPayload = buildTrumanMapLocalePayload(fileName);
      const trumanAsset = `assets/i18n-trumanMap-${fileName}-${shortContentHash(trumanPayload)}.json`;
      trumanMap[localeCode] = trumanAsset;
      byAssetPath.set(trumanAsset, { kind: "trumanMap", fileName });
    }

    assetManifest = { core, trumanMap, byAssetPath };
    return assetManifest;
  }

  return {
    name: "toonflowPluginScopedI18n",
    enforce: "pre",
    buildStart() {
      ensureAssetManifest();
    },
    configureServer(server) {
      const manifest = ensureAssetManifest();
      server.middlewares.use((req, res, next) => {
        const cleanUrl = (req.url ?? "").split("?")[0] ?? "";
        const relPath = cleanUrl.replace(/^\/creative-studio\//, "").replace(/^\/+/, "");
        const hit = manifest.byAssetPath.get(relPath);
        if (!hit) {
          next();
          return;
        }
        const payload =
          hit.kind === "core" ? buildCoreLocalePayload(hit.fileName) : buildTrumanMapLocalePayload(hit.fileName);
        res.setHeader("Content-Type", "application/json; charset=utf-8");
        res.end(payload);
      });
    },
    resolveId(id, importer) {
      if (id === "@toonflow/i18n/vue") {
        const scope = detectPluginI18nScope(importer);
        if (scope) return `${SCOPED_PLUGIN_I18N_PREFIX}${scope}`;
        return hostVueI18nPath;
      }
      if (
        (id === "./pluginI18nLoader" || id === "./pluginI18nLoader.ts") &&
        importer?.replaceAll("\\", "/").split("?")[0] === pluginI18nRuntimePath
      ) {
        return PLUGIN_I18N_LOADER_VIRTUAL_ID;
      }
      return undefined;
    },
    load(id) {
      if (id === PLUGIN_I18N_LOADER_VIRTUAL_ID) {
        const manifest = ensureAssetManifest();
        return [
          `const CORE_ASSETS = ${JSON.stringify(manifest.core)};`,
          `const TRUMAN_MAP_ASSETS = ${JSON.stringify(manifest.trumanMap)};`,
          `const base = (import.meta.env.BASE_URL || "/creative-studio/").replace(/\\/+$/, "") + "/";`,
          `async function fetchJson(relPath) {`,
          `  if (!relPath) return undefined;`,
          `  const resp = await fetch(base + relPath);`,
          `  if (!resp.ok) throw new Error("Failed to load i18n asset: " + relPath + " (" + resp.status + ")");`,
          `  return await resp.json();`,
          `}`,
          `export async function loadCorePluginLocaleBundle(targetLocale) {`,
          `  const data = await fetchJson(CORE_ASSETS[targetLocale]);`,
          `  return data && typeof data === "object" ? data : {};`,
          `}`,
          `export async function loadTrumanMapLocaleCatalog(targetLocale) {`,
          `  const data = await fetchJson(TRUMAN_MAP_ASSETS[targetLocale]);`,
          `  return data && typeof data === "object" ? data : undefined;`,
          `}`,
        ].join("\n");
      }
      if (!id.startsWith(SCOPED_PLUGIN_I18N_PREFIX)) return;
      const scope = id.slice(SCOPED_PLUGIN_I18N_PREFIX.length);
      return [
        `export { locale, setLocale, supportedLocales, normalizeLocale, detectLocale, msg, markMessages } from ${JSON.stringify(hostVueI18nPath)};`,
        `import { createPluginTranslator, createScopedPluginTranslator } from ${JSON.stringify(pluginI18nRuntimePath)};`,
        `export { createPluginTranslator as createTranslator };`,
        `export const { translate, t } = createScopedPluginTranslator(${JSON.stringify(scope)});`,
      ].join("\n");
    },
    generateBundle() {
      const manifest = ensureAssetManifest();
      for (const [fileName, entry] of manifest.byAssetPath.entries()) {
        const source =
          entry.kind === "core" ? buildCoreLocalePayload(entry.fileName) : buildTrumanMapLocalePayload(entry.fileName);
        this.emitFile({
          type: "asset",
          fileName,
          source,
        });
      }
    },
  };
}

export default defineConfig({
  root: rootDir,
  base: "/creative-studio/",
  css: {
    preprocessorOptions: {
      scss: {
        api: "modern-compiler",
      },
    },
    postcss: {
      plugins: [
        pxtorem({
          rootValue: 16,
          propList: ["*font*", "*line-height*", "letter-spacing", "word-spacing"],
          exclude: () => false,
        }),
      ],
    },
  },
  define: {
    "import.meta.env.appVersion": JSON.stringify("2.0.0-web"),
  },
  resolve: {
    alias: [
      // Browser-native ESM node & extension loaders (zero eval, strict CSP compliant)
      {
        find: /^@\/pages\/workspace\/panels\/canvas\/loadNodeComponent(?:\.ts)?$/,
        replacement: resolve(rootDir, "runtime/loadNodeComponent.ts"),
      },
      {
        find: /^\.\/loadNodeComponent(?:\.ts)?$/,
        replacement: resolve(rootDir, "runtime/loadNodeComponent.ts"),
      },
      {
        find: /^@\/pages\/workspace\/panels\/document\/extensions(?:\.ts)?$/,
        replacement: resolve(rootDir, "runtime/extensions.ts"),
      },
      {
        find: "@toonflow/tools-scaffold/client",
        replacement: resolve(rootDir, "runtime/loadToolComponent.ts"),
      },
      {
        find: "@toonflow/tool-asset-operator/runtime",
        replacement: resolve(pkgsDir, "tools/assetOperator/src/runtime.ts"),
      },
      {
        find: /^@toonflow-upstream\/(.+)$/,
        replacement: resolve(upstreamDir, "$1"),
      },
      // Shiki web alias from upstream apps/web/vite.config.ts
      { find: /^shiki$/, replacement: resolve(webSrcDir, "lib/shiki.ts") },
      // Custom Creative Plaza modules & shared host pure utilities
      { find: /^@custom\/(.+)$/, replacement: resolve(rootDir, "custom/$1") },
      { find: /^@host\/(.+)$/, replacement: resolve(rootDir, "../src/$1") },
      // Upstream @/ alias -> apps/web/src
      { find: "@", replacement: webSrcDir },

      // @toonflow/i18n ("@toonflow/i18n/vue" is resolved per-package by toonflowPluginI18nPlugin)
      { find: "@toonflow/i18n/elementPlus", replacement: resolve(pkgsDir, "i18n/src/elementPlus.ts") },
      { find: "@toonflow/i18n/errors", replacement: resolve(pkgsDir, "i18n/src/errors.ts") },
      { find: "@toonflow/i18n/transform", replacement: resolve(pkgsDir, "i18n/src/transform.ts") },
      { find: "@toonflow/i18n/plugin", replacement: resolve(pkgsDir, "i18n/src/plugin.ts") },
      { find: "@toonflow/i18n/vite", replacement: resolve(pkgsDir, "i18n/src/vite.ts") },
      { find: /^@toonflow\/i18n$/, replacement: resolve(pkgsDir, "i18n/src/index.ts") },

      // @toonflow/assets, asr, modelIcons, providers, providerWeb, pluginServer, ffmpeg
      { find: /^@toonflow\/assets\/(.+)$/, replacement: resolve(pkgsDir, "assets/$1") },
      { find: /^@toonflow\/asr$/, replacement: resolve(pkgsDir, "asr/src/index.ts") },
      { find: /^@toonflow\/model-icons$/, replacement: resolve(pkgsDir, "modelIcons/src/index.ts") },
      { find: /^@toonflow\/providers\/types(\?.*)?$/, replacement: `${resolve(pkgsDir, "providers/types.d.ts")}$1` },
      { find: /^@toonflow\/providers$/, replacement: resolve(pkgsDir, "providers/index.ts") },
      { find: "@toonflow/provider-web/formCreate", replacement: resolve(pkgsDir, "providerWeb/formCreate.ts") },
      { find: "@toonflow/provider-web/modelEditor", replacement: resolve(pkgsDir, "providerWeb/modelEditorDialog.vue") },
      { find: "@toonflow/provider-web/providerEditor", replacement: resolve(pkgsDir, "providerWeb/providerEditor.vue") },
      { find: "@toonflow/provider-web/providerTypes", replacement: resolve(pkgsDir, "providerWeb/providerTypes.ts") },
      { find: "@toonflow/provider-web/types", replacement: resolve(pkgsDir, "providerWeb/types.ts") },
      { find: "@toonflow/plugin-server/vue", replacement: resolve(pkgsDir, "pluginServer/src/vue.ts") },
      { find: "@toonflow/plugin-server/protocol", replacement: resolve(pkgsDir, "pluginServer/src/protocol.ts") },
      { find: "@toonflow/plugin-server/localModels", replacement: resolve(pkgsDir, "pluginServer/src/localModels.ts") },
      { find: "@toonflow/plugin-server/localModelInference", replacement: resolve(pkgsDir, "pluginServer/src/localModelInference.ts") },
      { find: "@toonflow/plugin-server/localModelClient", replacement: resolve(pkgsDir, "pluginServer/src/localModelClient.ts") },
      { find: "@toonflow/plugin-server/client", replacement: resolve(pkgsDir, "pluginServer/src/client.ts") },
      { find: /^@toonflow\/plugin-server$/, replacement: resolve(pkgsDir, "pluginServer/src/index.ts") },
      { find: "@toonflow/ffmpeg/browser", replacement: resolve(pkgsDir, "ffmpeg/src/browser.ts") },
      { find: "@toonflow/ffmpeg/types", replacement: resolve(pkgsDir, "ffmpeg/src/types.ts") },

      // @toonflow/nodes-scaffold
      { find: "@toonflow/nodes-scaffold/runtime", replacement: resolve(pkgsDir, "nodeScaffold/src/runtime.ts") },
      { find: "@toonflow/nodes-scaffold/nodeSkeleton", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeSkeleton.vue") },
      { find: "@toonflow/nodes-scaffold/connection", replacement: resolve(pkgsDir, "nodeScaffold/src/connection.ts") },
      { find: "@toonflow/nodes-scaffold/values", replacement: resolve(pkgsDir, "nodeScaffold/src/values.ts") },
      { find: "@toonflow/nodes-scaffold/nodeInputs", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeInputs.ts") },
      { find: "@toonflow/nodes-scaffold/utils", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeInputs.ts") },
      { find: "@toonflow/nodes-scaffold/nodeEvent", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeEvent.ts") },
      { find: "@toonflow/nodes-scaffold/nodeDocument", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeDocument.ts") },
      { find: "@toonflow/nodes-scaffold/nodeAi", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeAi.ts") },
      { find: "@toonflow/nodes-scaffold/nodeModel", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeModel.ts") },
      { find: "@toonflow/nodes-scaffold/nodeFfmpeg", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeFfmpeg.ts") },
      { find: "@toonflow/nodes-scaffold/nodeAsr", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeAsr.ts") },
      { find: "@toonflow/nodes-scaffold/nodeServer", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeServer.ts") },
      { find: "@toonflow/nodes-scaffold/serverClient", replacement: resolve(pkgsDir, "nodeScaffold/src/serverClient.ts") },
      { find: "@toonflow/nodes-scaffold/workspaceFiles", replacement: resolve(pkgsDir, "nodeScaffold/src/workspaceFiles.ts") },
      { find: "@toonflow/nodes-scaffold/nodeTools", replacement: resolve(pkgsDir, "nodeScaffold/src/nodeTools.ts") },

      // @toonflow/node-shared
      { find: "@toonflow/node-shared/cascadeMenuItems", replacement: resolve(pkgsDir, "nodeShared/src/components/cascadeMenuItems.vue") },
      { find: "@toonflow/node-shared/mediaCommands", replacement: resolve(pkgsDir, "nodeShared/src/mediaCommands.ts") },
      { find: "@toonflow/node-shared/mediaModelJob", replacement: resolve(pkgsDir, "nodeShared/src/mediaModelJob.ts") },
      { find: "@toonflow/node-shared/mediaPreview", replacement: resolve(pkgsDir, "nodeShared/src/mediaPreview.ts") },
      { find: "@toonflow/node-shared/mediaNodePreview", replacement: resolve(pkgsDir, "nodeShared/src/components/mediaNodePreview.vue") },
      { find: "@toonflow/node-shared/mediaTypes", replacement: resolve(pkgsDir, "nodeShared/src/mediaTypes.ts") },
      { find: "@toonflow/node-shared/pcmPreview", replacement: resolve(pkgsDir, "nodeShared/src/pcmPreview.ts") },
      { find: "@toonflow/node-shared/promptInput", replacement: resolve(pkgsDir, "nodeShared/src/components/promptInput.vue") },
      { find: "@toonflow/node-shared/promptText", replacement: resolve(pkgsDir, "nodeShared/src/promptText.ts") },
      { find: "@toonflow/node-shared/referenceItem", replacement: resolve(pkgsDir, "nodeShared/src/components/referenceItem.vue") },
      { find: "@toonflow/node-shared/mediaHistory", replacement: resolve(pkgsDir, "nodeShared/src/components/mediaHistory.vue") },
      { find: "@toonflow/node-shared/mediaTools", replacement: resolve(pkgsDir, "nodeShared/src/mediaTools.ts") },
      { find: "@toonflow/node-shared/useMediaToolAttachment", replacement: resolve(pkgsDir, "nodeShared/src/useMediaToolAttachment.ts") },
      { find: "@toonflow/node-shared/mediaToolMenu", replacement: resolve(pkgsDir, "nodeShared/src/components/mediaToolMenu.vue") },
      { find: "@toonflow/node-shared/mediaToolDialog", replacement: resolve(pkgsDir, "nodeShared/src/components/mediaToolDialog.vue") },
      { find: "@toonflow/node-shared/mediaPrice", replacement: resolve(pkgsDir, "nodeShared/src/components/mediaPrice.vue") },
      { find: "@toonflow/node-shared/useMediaPrice", replacement: resolve(pkgsDir, "nodeShared/src/useMediaPrice.ts") },
      { find: "@toonflow/node-shared/modelForm", replacement: resolve(pkgsDir, "nodeShared/src/components/modelForm.vue") },
      { find: "@toonflow/node-shared/imageGenerationSettings", replacement: resolve(pkgsDir, "nodeShared/src/components/imageGenerationSettings.vue") },
      { find: "@toonflow/node-shared/modelLabel", replacement: resolve(pkgsDir, "nodeShared/src/components/modelLabel.vue") },
      { find: "@toonflow/node-shared/modelSelect", replacement: resolve(pkgsDir, "nodeShared/src/components/modelSelect.vue") },
      { find: "@toonflow/node-shared/showNodeError", replacement: resolve(pkgsDir, "nodeShared/src/showNodeError.ts") },
      { find: "@toonflow/node-shared/useNodeReferences", replacement: resolve(pkgsDir, "nodeShared/src/useNodeReferences.ts") },
      { find: "@toonflow/node-shared/useNodeGeneration", replacement: resolve(pkgsDir, "nodeShared/src/useNodeGeneration.ts") },
      { find: "@toonflow/node-shared/useModelForm", replacement: resolve(pkgsDir, "nodeShared/src/useModelForm.ts") },
      { find: "@toonflow/node-shared/useAudioTranscription", replacement: resolve(pkgsDir, "nodeShared/src/useAudioTranscription.ts") },
      { find: "@toonflow/node-shared/groupNodeModels", replacement: resolve(pkgsDir, "nodeShared/src/groupNodeModels.ts") },

      // @toonflow/ext-scaffold & @toonflow/tools-scaffold
      { find: "@toonflow/ext-scaffold/runtime", replacement: resolve(pkgsDir, "extScaffold/src/runtime.ts") },
      { find: "@toonflow/ext-scaffold/serverClient", replacement: resolve(pkgsDir, "extScaffold/src/serverClient.ts") },
      { find: "@toonflow/tools-scaffold/runtime", replacement: resolve(pkgsDir, "toolScaffold/src/runtime.ts") },

      // @toonflow/tool-*
      { find: "@toonflow/tool-canvas/runtime", replacement: resolve(pkgsDir, "tools/canvas/src/runtime.ts") },
      { find: "@toonflow/tool-browser/panel", replacement: resolve(pkgsDir, "tools/browser/src/browserPanel.vue") },
      { find: "@toonflow/tool-browser/client", replacement: resolve(pkgsDir, "tools/browser/src/client.ts") },
      { find: "@toonflow/tool-browser/protocol", replacement: resolve(pkgsDir, "tools/browser/src/protocol.ts") },

      // @toonflow/node-* cross-package exports
      { find: "@toonflow/node-video/videoScaleOptions", replacement: resolve(pkgsDir, "nodes/videoNode/src/components/videoScaleOptions.vue") },
      { find: "@toonflow/node-video/videoPlayer", replacement: resolve(pkgsDir, "nodes/videoNode/src/components/videoPlayer.vue") },
      { find: "@toonflow/node-video/videoActions", replacement: resolve(pkgsDir, "nodes/videoNode/src/components/videoActions.vue") },
      { find: "@toonflow/node-video/portraitMatting", replacement: resolve(pkgsDir, "nodes/videoNode/src/portraitMatting.ts") },
      { find: "@toonflow/node-video/videoFilmstrip", replacement: resolve(pkgsDir, "nodes/videoNode/src/components/videoFilmstrip.vue") },
      { find: "@toonflow/node-video/videoProcessing", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoProcessing.ts") },
      { find: "@toonflow/node-video/rifeFrames", replacement: resolve(pkgsDir, "nodes/videoNode/src/rifeFrames.ts") },
      { find: "@toonflow/node-video/videoSubject", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoSubject.ts") },
      { find: "@toonflow/node-video/videoTextDetection", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoTextDetection.ts") },
      { find: "@toonflow/node-video/subtitleRepair", replacement: resolve(pkgsDir, "nodes/videoNode/src/subtitleRepair.ts") },
      { find: "@toonflow/node-video/videoRecognition", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoRecognition.ts") },
      { find: "@toonflow/node-video/videoTracking", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoTracking.ts") },
      { find: "@toonflow/node-video/soundTools", replacement: resolve(pkgsDir, "nodes/videoNode/src/videoSound.ts") },
      { find: "@toonflow/node-truman-map/promptImages", replacement: resolve(pkgsDir, "nodes/trumanMap/src/promptImages.ts") },
      { find: "@toonflow/node-truman-map/assetSearchControls", replacement: resolve(pkgsDir, "nodes/trumanMap/src/assetSearchControls.vue") },
      { find: "@toonflow/node-truman-map/cameraViewfinder", replacement: resolve(pkgsDir, "nodes/trumanMap/src/cameraViewfinder.vue") },
      { find: "@toonflow/node-truman-map/timelineTransport", replacement: resolve(pkgsDir, "nodes/trumanMap/src/timelineTransport.vue") },
      { find: "@toonflow/node-text/markdownPreview", replacement: resolve(pkgsDir, "nodes/textNode/src/markdownPreview.vue") },
      { find: "@toonflow/node-image/imageParts", replacement: resolve(pkgsDir, "nodes/imageNode/src/imageParts.ts") },
      { find: "@toonflow/node-image/materialNormals", replacement: resolve(pkgsDir, "nodes/imageNode/src/materialNormals.ts") },
      { find: "@toonflow/node-image/handPose", replacement: resolve(pkgsDir, "nodes/imageNode/src/handPose.ts") },
      { find: "@toonflow/node-image/upscaleTiles", replacement: resolve(pkgsDir, "nodes/imageNode/src/upscaleTiles.ts") },
      { find: "@toonflow/node-image/imageUpscaleEditor", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageUpscaleEditor.vue") },
      { find: "@toonflow/node-image/imageTools", replacement: resolve(pkgsDir, "nodes/imageNode/src/imageTools.ts") },
      { find: "@toonflow/node-image/imageSequenceTools", replacement: resolve(pkgsDir, "nodes/imageNode/src/imageSequenceTools.ts") },
      { find: "@toonflow/node-image/imageSequenceOptions", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageSequenceOptions.vue") },
      { find: "@toonflow/node-image/imageInpaint", replacement: resolve(pkgsDir, "nodes/imageNode/src/imageInpaint.ts") },
      { find: "@toonflow/node-image/imageInpaintPrompt", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageInpaintPrompt.vue") },
      { find: "@toonflow/node-image/imagePanorama", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imagePanorama.vue") },
      { find: "@toonflow/node-image/imageLighting", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageLighting.vue") },
      { find: "@toonflow/node-image/imageMultiAngle", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageMultiAngle.vue") },
      { find: "@toonflow/node-image/imageEditor", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageEditor.vue") },
      { find: "@toonflow/node-image/imageGridSplit", replacement: resolve(pkgsDir, "nodes/imageNode/src/components/imageGridSplit.vue") },
      { find: "@toonflow/node-audio/vocalChunks", replacement: resolve(pkgsDir, "nodes/audioNode/src/vocalChunks.ts") },
      { find: "@toonflow/node-audio/audioPlayer", replacement: resolve(pkgsDir, "nodes/audioNode/src/components/audioPlayer.vue") },
      { find: "@toonflow/node-audio/audioActions", replacement: resolve(pkgsDir, "nodes/audioNode/src/components/audioActions.vue") },
      { find: "@toonflow/plugin-server/readme.md", replacement: resolve(pkgsDir, "pluginServer/readme.md") },
      { find: /^@toonflow\/ext-md$/, replacement: resolve(pkgsDir, "ext/md/src/index.ts") },
      { find: /^@toonflow\/ext-txt$/, replacement: resolve(pkgsDir, "ext/txt/src/index.ts") },
      { find: /^@toonflow\/ext-image$/, replacement: resolve(pkgsDir, "ext/image/src/index.ts") },
      { find: /^@toonflow\/ext-media$/, replacement: resolve(pkgsDir, "ext/media/src/index.ts") },
      { find: /^@toonflow\/ext-canvas-node$/, replacement: resolve(pkgsDir, "ext/canvasNode/src/index.ts") },

      // @toonflow/multi-tenant-*
      { find: "@toonflow/multi-tenant-user-web/types", replacement: resolve(pkgsDir, "multiTenant/userWeb/types.ts") },
      { find: "@toonflow/multi-tenant-user-web/account", replacement: resolve(pkgsDir, "multiTenant/userWeb/account.vue") },
      { find: /^@toonflow\/multi-tenant-user-web$/, replacement: resolve(pkgsDir, "multiTenant/userWeb/index.vue") },
      { find: "@toonflow/multi-tenant-admin-web/routes", replacement: resolve(pkgsDir, "multiTenant/adminWeb/routes.ts") },
      { find: /^@toonflow\/multi-tenant-admin-web$/, replacement: resolve(pkgsDir, "multiTenant/adminWeb/index.vue") },
      { find: "@toonflow/multi-tenant-billing/config", replacement: resolve(pkgsDir, "multiTenant/billing/config.ts") },
      { find: "@toonflow/multi-tenant-models/config", replacement: resolve(pkgsDir, "multiTenant/models/config.ts") },
      { find: "@toonflow/multi-tenant-source-mapping/config", replacement: resolve(pkgsDir, "multiTenant/sourceMapping/config.ts") },

      // @toonflow/server/* pure TS shared types/helpers
      { find: "@toonflow/server/channel", replacement: resolve(serverSrcDir, "utils/channel/types.ts") },
      { find: "@toonflow/server/agent/types", replacement: resolve(serverSrcDir, "agent/runtime/types.ts") },
      { find: "@toonflow/server/agent/mentionSources", replacement: resolve(serverSrcDir, "agent/mentionSources.ts") },
      { find: "@toonflow/server/modelReasoning", replacement: resolve(serverSrcDir, "lib/modelReasoning.ts") },
      { find: "@toonflow/server/desktop", replacement: resolve(serverSrcDir, "types/desktop.ts") },
      { find: "@toonflow/server/localModels", replacement: resolve(serverSrcDir, "types/localModels.ts") },
      { find: "@toonflow/server/documentFormats", replacement: resolve(serverSrcDir, "lib/documentFormats.ts") },
      { find: "@toonflow/server/singleAgent/types", replacement: resolve(serverSrcDir, "utils/singleAgent/types.ts") },
      { find: "@toonflow/server/nodeMenu", replacement: resolve(serverSrcDir, "lib/nodeMenu.ts") },
      { find: "@toonflow/server/pluginDevelopment", replacement: resolve(serverSrcDir, "lib/pluginDevelopment.ts") },
      { find: "@toonflow/server/platform", replacement: resolve(serverSrcDir, "lib/platform.ts") },
    ],
  },
  build: {
    outDir: resolve(rootDir, "../../backend/internal/web/dist/creative-studio"),
    emptyOutDir: true,
    chunkSizeWarningLimit: 15000,
  },
  plugins: [
    toonflowJsonImportAttrPlugin(),
    toonflowPluginI18nPlugin(),
    toonflowI18nTransformPlugin(),
    vue(),
    components({
      dirs: [],
      dts: false,
      resolvers: [
        ElementPlusResolver(),
        (name) => {
          if (name.startsWith("Icon")) {
            const target = tablerIconMap.get(name);
            if (target) return { name: "default", from: `@tabler/icons-vue/icons/${target}` };
            return { name, from: "@tabler/icons-vue" };
          }
        },
      ],
    }),
    tablerIconsDirectImportPlugin(),
  ],
});

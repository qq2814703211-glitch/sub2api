# Upstream Provenance: HBAI-Ltd/Toonflow-app

This directory integrates the real Web frontend and browser-side packages from [HBAI-Ltd/Toonflow-app](https://github.com/HBAI-Ltd/Toonflow-app) under the MIT License (`./LICENSE`).

- **Upstream repository**: `https://github.com/HBAI-Ltd/Toonflow-app`
- **Pinned commit**: `2112d7cd65374d11ce67f64790705618c261f597` (`master`)
- **Included upstream modules**:
  - `apps/web` — complete Toonflow Web SPA (Home, Projects, Explore, 3D TrumanMap pre-visualization, Vue Flow infinite canvas workspace, Dockview document workspace, AI Assistant, Plugin Market, MCP/A2A settings)
  - `packages/nodes/*` — all 9 official nodes (`textNode`, `imageNode`, `imageGenerationNode`, `videoNode`, `videoGenerationNode`, `audioNode`, `audioGenerationNode`, `frameAnalysisNode`, `trumanMap`)
  - `packages/ext/*` — all 5 official document extensions (`canvasNode`, `image`, `md`, `media`, `txt`)
  - `packages/{i18n,nodeShared,nodeScaffold,extScaffold,toolScaffold,pluginServer,providerWeb,providers,modelIcons,assets,asr,skills,teams,tools/{canvas,browser,trumanMap},multiTenant}`
  - Pure TypeScript shared types/constants exported by `@toonflow/server/*`
- **Excluded server/native modules**: `apps/server` runtime, `apps/desktop`, `apps/mobile`, `apps/updateServer`, `compat/ohos`, native FFmpeg binaries, and non-zh/en locale packs.
- **Minimal upstream bridge adjustments**:
  - `upstream/apps/web/src/pages/home/index.vue` watches `route.query.section` (`home`, `projects`, `explore`, `assets`, `pluginMarket`, `trumanMap`, resetting to `home` when cleared) so host Creative Plaza quick-navigation buttons jump directly to the requested Toonflow section, and uses `v-if` for `trumanMapPage` so leaving the 3D section cleanly closes any fullscreen `appendToBody` scene editor dialog and releases its WebGL context.
  - `upstream/apps/web/src/components/settings/panels/pluginMarket/index.vue` sets `agentMarketEnabled = true` so users can directly open the built-in Agent Teams / A2A connection & A2A settings dialogs inside Plugin Market.
- **Browser-local execution**: All Toonflow `/api/*` requests are served inside the user's browser by `./runtime/` (IndexedDB virtual workspace & asset storage, browser AI gateway bridge to sub2api `/v1/*`, browser AI Assistant & TrumanMap Holodeck single-agent runtime, browser Streamable HTTP/SSE MCP client + canvas control bus, browser A2A client, browser Plugin Market, and browser HTML5/WebAudio media tools) — consuming zero server compute or server disk.

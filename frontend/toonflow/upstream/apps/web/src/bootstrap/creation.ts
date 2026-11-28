import { createApp, watch, type App } from "vue";
import { createPinia } from "pinia";
import { createPersistedState } from "pinia-plugin-persistedstate";
import type { UserSession } from "@toonflow/multi-tenant-user-web/types";
import { pluginServerKey } from "@toonflow/plugin-server/vue";
import { createPluginServerHost } from "@/lib/pluginServer";

export async function createCreationApp(session: UserSession): Promise<App<Element>> {
  const [
    { default: appRoot }, { default: router }, { loadSettings, settingsStorage, multiTenantEnabled }, { registerLanguage },
    { registerDownloads },
  ] = await Promise.all([
    import("@/App.vue"), import("@/router"), import("@/stores/settings"), import("@/lib/i18n"),
    import("@/lib/saveFile"),
  ]);
  await loadSettings();
  const app = createApp(appRoot);
  const pluginServer = createPluginServerHost();
  app.provide(pluginServerKey, pluginServer.createServer);
  app.onUnmount(pluginServer.dispose);
  app.onUnmount(registerLanguage());
  app.use(createPinia().use(createPersistedState({ storage: settingsStorage })));
  app.use(router);
  await router.isReady();
  app.onUnmount(registerDownloads());
  if (!session.publicWeb) app.onUnmount(watch(multiTenantEnabled, enabled => { if (enabled) window.location.reload(); }));
  return app;
}

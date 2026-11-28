import { getBrandName } from "@custom/i18nOverrides";
import { installBrowserRuntimeInterceptor } from "./interceptor";
import { initPluginI18n } from "./pluginI18n";
import { getHostContext, initSub2ApiBridge, subscribeHostContext, subscribeIncomingMedia } from "./sub2apiBridge";
import { ASSETS_ROOT, ensureSeeded, joinPath, kvGet, vfsWriteBytes } from "./vfs";

// 1. Install browser-native local API runtime BEFORE studio boots
installBrowserRuntimeInterceptor();
initSub2ApiBridge();
initPluginI18n();

// 2. Sync host theme, title, model caches & incoming media
function applyHostTheme(theme: "dark" | "light") {
  const hostWindow = window as Window & {
    __sub2apiHostTheme?: "dark" | "light";
    __toonflowAppearanceReady?: boolean;
  };
  hostWindow.__sub2apiHostTheme = theme;
  window.dispatchEvent(new CustomEvent("toonflow:host-theme-changed", { detail: theme }));
  if (!hostWindow.__toonflowAppearanceReady) {
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.setAttribute("theme-mode", theme);
    document.documentElement.setAttribute("data-theme", theme);
    document.documentElement.style.colorScheme = theme;
  }
}

function applyDocumentTitle(locale: string) {
  document.title = getBrandName(locale === "en" ? "en" : "zh-CN");
}

async function hasExplicitStudioLanguage(): Promise<boolean> {
  const saved = await kvGet<Record<string, unknown>>("toonflow.settings", {});
  const ui = saved.ui && typeof saved.ui === "object" && !Array.isArray(saved.ui)
    ? (saved.ui as Record<string, unknown>)
    : undefined;
  return typeof ui?.language === "string" && ui.language.trim() !== "" && ui.language !== "system";
}

let lastHostLocale = getHostContext().locale;
let lastKeysSig = "";
applyHostTheme(getHostContext().theme);
applyDocumentTitle(lastHostLocale);

subscribeHostContext((ctx) => {
  applyHostTheme(ctx.theme);
  if (ctx.locale !== lastHostLocale) {
    lastHostLocale = ctx.locale;
    applyDocumentTitle(ctx.locale);
    void hasExplicitStudioLanguage().then((explicit) => {
      if (explicit) return;
      return import("@toonflow/i18n/vue").then(({ setLocale }) => {
        setLocale(ctx.locale === "en" ? "en" : "zh-CN");
      });
    });
  }
  const nextKeysSig = JSON.stringify([
    ctx.selectedTextKeyId,
    ctx.selectedImageKeyId,
    ctx.selectedVideoKeyId,
    ctx.selectedAudioKeyId,
    ctx.keys.map((k) => k.id),
    ctx.discoveredModels,
  ]);
  if (nextKeysSig !== lastKeysSig) {
    lastKeysSig = nextKeysSig;
    void import("@toonflow/nodes-scaffold/nodeAi")
      .then(({ invalidateNodeModels }) => {
        invalidateNodeModels("language");
        invalidateNodeModels("media");
      })
      .catch(() => {});
  }
});

subscribeIncomingMedia(async (payload) => {
  try {
    await ensureSeeded();
    const resp = await fetch(payload.src);
    const bytes = new Uint8Array(await resp.arrayBuffer());
    const mimeType = resp.headers.get("content-type") || "image/png";
    const ext = mimeType.includes("webp") ? "webp" : mimeType.includes("jpeg") ? "jpg" : "png";
    const safeTitle = (payload.title || "快捷生图").replace(/[\\/:*?"<>|]/g, "_").slice(0, 40);
    const rel = `快捷生图导入/${safeTitle}-${Date.now().toString(36)}.${ext}`;
    await vfsWriteBytes(joinPath(ASSETS_ROOT, rel), bytes, { mimeType });
  } catch (err) {
    console.warn("[creative-plaza] 导入快捷生图素材失败:", err);
  }
});

// 3. Boot Creative Plaza web frontend
void ensureSeeded().then(async () => {
  await import("@toonflow-upstream/apps/web/src/main.ts");
  const ctx = getHostContext();
  applyHostTheme(ctx.theme);
  const explicit = await hasExplicitStudioLanguage();
  if (!explicit) {
    void import("@toonflow/i18n/vue").then(({ setLocale }) => {
      setLocale(ctx.locale === "en" ? "en" : "zh-CN");
    });
  }
});

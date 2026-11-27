import { installBrowserRuntimeInterceptor } from "./interceptor";
import { getHostContext, initSub2ApiBridge, subscribeHostContext, subscribeIncomingMedia } from "./sub2apiBridge";
import { ASSETS_ROOT, ensureSeeded, joinPath, vfsWriteBytes } from "./vfs";

// 1. Install browser-native local API runtime BEFORE upstream Toonflow boots
installBrowserRuntimeInterceptor();
initSub2ApiBridge();

// 2. Sync host theme & incoming Quick Image Studio media
function applyHostTheme(theme: "dark" | "light") {
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.setAttribute("theme-mode", theme);
  document.documentElement.setAttribute("data-theme", theme);
}

applyHostTheme(getHostContext().theme);
subscribeHostContext((ctx) => {
  applyHostTheme(ctx.theme);
  void import("@toonflow/i18n/vue").then(({ setLocale }) => {
    setLocale(ctx.locale === "en" ? "en" : "zh-CN");
  });
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
    console.warn("[toonflow-browser] 导入快捷生图素材失败:", err);
  }
});

// 3. Boot real upstream HBAI-Ltd/Toonflow-app web frontend
void ensureSeeded().then(async () => {
  await import("../upstream/apps/web/src/main.ts");
  const ctx = getHostContext();
  applyHostTheme(ctx.theme);
  void import("@toonflow/i18n/vue").then(({ setLocale }) => {
    setLocale(ctx.locale === "en" ? "en" : "zh-CN");
  });
});

import axios from "axios";
import { getMediaMimeType } from "@toonflow/node-shared/mediaTypes";

type CachedAssetUrl = {
  path: string;
  url: Promise<string>;
  users: number;
};

const assetUrlCache = new Map<string, CachedAssetUrl>();

function normalizeCachePath(path: string): string {
  return path.replaceAll("\\", "/").split("/").filter((part) => part && part !== ".").join("/").toLowerCase();
}

export function invalidateGlobalAssetUrls(path?: string): void {
  if (!path) {
    assetUrlCache.clear();
    return;
  }
  const target = normalizeCachePath(path);
  for (const [key, entry] of assetUrlCache) {
    const entryPath = normalizeCachePath(entry.path);
    if (entryPath === target || entryPath.startsWith(`${target}/`)) {
      assetUrlCache.delete(key);
    }
  }
}

/**
 * 从浏览器本地 IndexedDB 素材库读取文件并生成 blob: URL，
 * 避免 <img>/<video>/<audio> 直接请求 /api/assets/read 落到服务端。
 */
export function acquireGlobalAssetUrl(path: string, mimeType?: string): { url: Promise<string>; release: () => void } {
  const normalized = path.replaceAll("\\", "/").replace(/^\/+/, "");
  const fallbackMime = mimeType || getMediaMimeType({ name: normalized, type: "" }) || "application/octet-stream";
  const key = JSON.stringify([normalized, fallbackMime]);
  let entry = assetUrlCache.get(key);
  if (!entry) {
    const url = axios
      .get<ArrayBuffer>("/api/assets/read", {
        params: { path: normalized },
        responseType: "arraybuffer",
      })
      .then(({ data, headers }) => {
        const type = String(headers["content-type"] || "") || fallbackMime;
        return URL.createObjectURL(new Blob([data], { type }));
      });
    entry = { path: normalized, url, users: 0 };
    assetUrlCache.set(key, entry);
    const current = entry;
    void url.catch(() => {
      if (assetUrlCache.get(key) === current) assetUrlCache.delete(key);
    });
  }
  const current = entry;
  current.users++;
  let released = false;
  return {
    url: current.url,
    release() {
      if (released) return;
      released = true;
      if (--current.users) return;
      if (assetUrlCache.get(key) === current) assetUrlCache.delete(key);
      void current.url.then((url) => URL.revokeObjectURL(url), () => {});
    },
  };
}

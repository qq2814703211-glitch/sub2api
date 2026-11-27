import type { Locale } from "./index";
import zhCn from "./elementPlus/zhCn.json";
import zhTw from "./elementPlus/zhTw.json";
import en from "./elementPlus/en.json";

export const elementLocales: Record<Locale, typeof en> = {
  "zh-CN": zhCn,
  "zh-TW": zhTw,
  en,
  ja: en,
  ru: en,
  vi: en,
  th: en,
  ko: en,
  hi: en,
  id: en,
  ms: en,
  fil: en,
  bn: en,
  ur: en,
  ta: en,
  te: en,
  mr: en,
  pa: en,
  ar: en,
  fa: en,
  tr: en,
  pt: en,
};

import { computed, onMounted, onUnmounted, ref } from "vue";
import { locale } from "@toonflow/i18n/vue";
import { getBrandName } from "./i18nOverrides";
import { getHostContext, subscribeHostContext } from "../runtime/sub2apiBridge";

export function resolveHostLocale(): "zh-CN" | "en" {
  return getHostContext().locale === "en" ? "en" : "zh-CN";
}

export function subscribeHostLocale(listener: (next: "zh-CN" | "en") => void): () => void {
  let last = resolveHostLocale();
  return subscribeHostContext((ctx) => {
    const next = ctx.locale === "en" ? "en" : "zh-CN";
    if (next !== last) {
      last = next;
      listener(next);
    }
  });
}

export function useStudioBrand() {
  const hostCtx = ref(getHostContext());
  let unsubscribe: (() => void) | undefined;

  onMounted(() => {
    hostCtx.value = getHostContext();
    unsubscribe = subscribeHostContext((next) => {
      hostCtx.value = next;
    });
  });

  onUnmounted(() => {
    unsubscribe?.();
  });

  const brandName = computed(() => hostCtx.value.siteName?.trim() || getBrandName(locale.value));
  const brandLogo = computed(() => hostCtx.value.siteLogo?.trim() || "");

  return { brandName, brandLogo, hostCtx };
}

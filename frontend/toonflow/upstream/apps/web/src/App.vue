<template>
  <el-config-provider :locale="elementLocale">
    <config-provider :globalConfig="tdesignLocale">
      <router-view v-slot="{ Component: currentComponent }">
        <transition name="el-fade-in" mode="out-in">
          <component :is="currentComponent" />
        </transition>
      </router-view>
      <loadingPage v-model:visible="workspaceStore.projectLoadingVisible" v-model:drawn="workspaceStore.projectLoadingDrawn" :active="workspaceStore.projectLoading" />
    </config-provider>
  </el-config-provider>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, provide, watchEffect } from "vue";
import { useZIndex } from "element-plus";
import { elementLocales } from "@toonflow/i18n/elementPlus";
import { ConfigProvider, type GlobalConfigProvider } from "tdesign-vue-next";
import tdesignZhCn from "tdesign-vue-next/es/locale/zh_CN";
import tdesignZhTw from "tdesign-vue-next/es/locale/zh_TW";
import tdesignEn from "tdesign-vue-next/es/locale/en_US";
import tdesignJa from "tdesign-vue-next/es/locale/ja_JP";
import tdesignRu from "tdesign-vue-next/es/locale/ru_RU";
import tdesignKo from "tdesign-vue-next/es/locale/ko_KR";
import tdesignAr from "tdesign-vue-next/es/locale/ar_KW";
import { locale } from "@toonflow/i18n/vue";
import { chatLocale } from "@/lib/i18n";
import { registerAppearance } from "@/lib/appearance";
import { isMobile } from "@/lib/platform";
import { uiSettings } from "@/stores/settings";
import { useWorkspaceStore } from "@/stores/workspace";
import { currentUser } from "@/stores/multiTenant";
import loadingPage from "@/components/loadingPage.vue";
import { useMcpControl } from "@/lib/mcpControl";
import "element-plus/theme-chalk/dark/css-vars.css";

if (currentUser.value?.role !== "user") useMcpControl();
const workspaceStore = useWorkspaceStore();
provide("mediaCostReminderEnabled", () => false);
provide("confirmMediaCost", async () => true);

// ACT: TDesign 缺少的语言使用英语基底，聊天控件由应用字典补齐。
const tdesignLocales = {
  "zh-CN": tdesignZhCn, "zh-TW": tdesignZhTw, en: tdesignEn, ja: tdesignJa, ru: tdesignRu, vi: tdesignEn, th: tdesignEn, ko: tdesignKo, hi: tdesignEn,
  id: tdesignEn, ms: tdesignEn, fil: tdesignEn, bn: tdesignEn, ur: tdesignEn, ta: tdesignEn, te: tdesignEn, mr: tdesignEn, pa: tdesignEn, ar: tdesignAr, fa: tdesignEn, tr: tdesignEn, pt: tdesignEn,
};
const elementLocale = computed(() => elementLocales[locale.value]);
// ACT: TDesign 自带语言包声明为 readonly，而 ConfigProvider 的只读使用接口声明为可写。
const tdesignLocale = computed(() => ({ ...tdesignLocales[locale.value], chat: chatLocale.value }) as unknown as GlobalConfigProvider);

function preventPageZoom(event: WheelEvent) {
  if (event.ctrlKey || event.metaKey) event.preventDefault();
}
function preventPageZoomShortcut(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && !event.altKey && ["+", "=", "-", "0"].includes(event.key)) event.preventDefault();
}
// 仅取消浏览器默认缩放，继续传递事件供 Vue Flow 缩放画布。
window.addEventListener("wheel", preventPageZoom, { capture: true, passive: false });
// 画布在捕获阶段先处理自己的快捷键，再在冒泡阶段取消浏览器缩放。
window.addEventListener("keydown", preventPageZoomShortcut);
onBeforeUnmount(() => {
  window.removeEventListener("wheel", preventPageZoom, true);
  window.removeEventListener("keydown", preventPageZoomShortcut);
});

const { currentZIndex } = useZIndex();
watchEffect(() => document.documentElement.style.setProperty("--markdown-tooltip-z-index", String(currentZIndex.value + 1)));

watchEffect(() => {
  if (!isMobile) return;
  const scale = uiSettings.value.mobileScale / 100;
  // ACT: 移动端视口比例由设置统一控制，避免输入聚焦自动放大后无法还原；画布仍由 Vue Flow 缩放。
  document.querySelector('meta[name="viewport"]')?.setAttribute("content", `width=device-width, initial-scale=${scale}, minimum-scale=${scale}, maximum-scale=${scale}, user-scalable=no`);
});

onBeforeUnmount(registerAppearance());
</script>

<style lang="scss">
html {
  --canvasGridBackground: var(--el-bg-color);
  background-color: var(--el-bg-color);
  color: var(--el-text-color-primary);

  &[theme-mode="light"] {
    --canvasGridBackground: var(--el-bg-color-page);
  }
}

.installFailureContent {
  .failureMessage {
    margin: 0;
    max-height: 160px;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--el-color-danger);
    line-height: 1.7;
  }

  .failureVersions {
    margin: 20px 0;
    padding: 14px;
    display: grid;
    gap: 16px;
    border-radius: var(--ui-radius);
    background: var(--el-fill-color-light);

    > div {
      dt { margin-bottom: 6px; color: var(--el-text-color-secondary); }
      dd {
        margin: 0;
        display: flex;
        flex-direction: column;
        gap: 5px;
        code { font-size: 12px; overflow-wrap: anywhere; }
      }
    }
  }

  .failureHint { margin: 0; line-height: 1.7; }
}

// ACT: RTL 只改变界面阅读方向，画布坐标、代码和技术值保留从左到右。
.vue-flow,
pre,
code,
input[type="url"],
input[type="email"],
input[type="password"],
input[type="number"],
input[inputmode="numeric"],
input[inputmode="decimal"],
.projectPath,
.locationPath {
  direction: ltr;
  unicode-bidi: isolate;
}

// 主题切换圆形扩散动效，坐标由触发点写入 --themeX/--themeY/--themeR。
::view-transition-old(root),
::view-transition-new(root) {
  animation: none;
  mix-blend-mode: normal;
}

::view-transition-new(root) {
  animation: themeReveal 0.4s ease-in forwards;
}

@keyframes themeReveal {
  from {
    clip-path: circle(0 at var(--themeX) var(--themeY));
  }
  to {
    clip-path: circle(var(--themeR) at var(--themeX) var(--themeY));
  }
}

.vue-flow {
  --vf-node-bg: var(--el-bg-color-overlay);
  --vf-node-text: var(--el-text-color-primary);
  --vf-node-color: var(--el-border-color-darker);
  --vf-connection-path: var(--el-text-color-secondary);
  --vf-handle: var(--el-text-color-secondary);

  .vue-flow__background { background-color: var(--canvasGridBackground); }

  html[theme-mode="light"] & {
    .vue-flow__node {
      .contentCard,
      .groupNode {
        border-color: color-mix(in srgb, var(--el-text-color-regular) 70%, var(--el-border-color));

        &.selected {
          border-color: color-mix(in srgb, var(--el-color-primary) 40%, var(--el-text-color-primary));
          outline: 1px solid var(--el-color-primary);
        }
      }

      .groupLabel { color: var(--el-text-color-regular); }
    }
  }

  .vue-flow__minimap {
    background-color: var(--el-bg-color);
  }
}
</style>

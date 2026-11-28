<template>
  <el-dialog v-model="visible" title="设置" width="min(1080px, calc(100vw - 32px))" alignCenter appendToBody>
    <div class="settings">
      <aside class="sidebar" aria-label="设置分类">
        <template v-for="item in settingsPanels" :key="item.id">
          <h3 v-if="item.groupLabel" class="settingsGroupLabel">{{ translate(item.groupLabel) }}</h3>
          <button
            class="settingsItem"
            type="button"
            :title="translate(item.label)"
            :aria-label="translate(item.label)"
            :aria-pressed="activePanel.id === item.id"
            @click="activePanel = item">
            <span class="panelIcon">
              <component :is="item.icon" :size="18" aria-hidden="true" />
            </span>
            <span class="itemLabel">{{ translate(item.label) }}</span>
          </button>
        </template>
      </aside>
      <section class="content" :aria-label="translate(activePanel.label)" tabindex="0">
        <h2 class="panelTitle">{{ translate(activePanel.label) }}</h2>
        <div class="panelContent">
          <keep-alive include="personalization">
            <component
              :is="activePanel.component"
              v-bind="['apiKeys', 'pluginMarket', 'personalization'].includes(activePanel.id) ? { visible } : {}" />
          </keep-alive>
        </div>
      </section>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, shallowRef, watch } from "vue";
import { markMessages, translate } from "@toonflow/i18n/vue";
import {
  IconKey,
  IconSettings,
  IconBuildingStore,
  IconPlugConnected,
  IconUserCog,
} from "@tabler/icons-vue";

const allSettingsPanels = markMessages([
  { id: "apiKeys", label: "密钥与模型", icon: IconKey, component: defineAsyncComponent(() => import("@custom/apiKeysPanel.vue")) },
  { id: "general", label: "通用设置", icon: IconSettings, component: defineAsyncComponent(() => import("./panels/general/index.vue")) },
  {
    id: "pluginMarket",
    label: "插件市场",
    icon: IconBuildingStore,
    groupLabel: "扩展",
    component: defineAsyncComponent(() => import("./panels/pluginMarket/index.vue")),
  },
  { id: "mcp", label: "MCP", icon: IconPlugConnected, groupLabel: "通讯", component: defineAsyncComponent(() => import("./panels/mcp/index.vue")) },
  {
    id: "personalization",
    label: "个性化",
    icon: IconUserCog,
    groupLabel: "其他",
    component: defineAsyncComponent(() => import("./panels/personalization/index.vue")),
  },
]);
const settingsPanels = computed(() => allSettingsPanels);
const activePanel = shallowRef(settingsPanels.value[0]!);
const visible = defineModel<boolean>({ default: false });

watch(settingsPanels, panels => {
  activePanel.value = panels.find(item => item.id === activePanel.value.id) ?? panels[0]!;
});

function openPanel(id: string) {
  const normalized = ["account", "languageModel", "mediaModel"].includes(id) ? "apiKeys" : id;
  const panel = settingsPanels.value.find(item => item.id === normalized) ?? settingsPanels.value[0];
  if (!panel) return false;
  activePanel.value = panel;
  visible.value = true;
  return true;
}

defineExpose({ openPanel });
</script>

<style lang="scss" scoped>
.settings {
  display: grid;
  grid-template-columns: fit-content(32%) minmax(0, 1fr);
  height: min(72vh, calc(100dvh - 140px));
  overflow: hidden;

  .sidebar {
    min-width: 160px;
    min-height: 0;
    overflow-y: auto;
    scrollbar-gutter: stable;
    padding: 2px;

    .settingsGroupLabel {
      margin: 14px 12px 6px;
      color: var(--el-text-color-secondary);
      font-size: 12px;
      font-weight: 400;
      line-height: 1.5;
    }

    .settingsItem {
      display: flex;
      align-items: center;
      gap: 10px;
      width: 100%;
      padding: 10px 12px;
      margin-bottom: 4px;
      border: 0;
      border-radius: var(--el-border-radius-base);
      background: transparent;
      color: var(--el-text-color-regular);
      font: inherit;
      text-align: start;
      cursor: pointer;

      .panelIcon {
        display: inline-flex;
        flex-shrink: 0;
      }

      .itemLabel {
        min-width: 0;
        overflow-wrap: break-word;
      }

      &:hover {
        background: var(--el-fill-color-light);
      }

      &[aria-pressed="true"] {
        background: var(--el-color-primary-light-9);
        color: var(--el-color-primary);
      }

      &:focus-visible {
        outline: 2px solid var(--el-color-primary);
      }
    }
  }

  .content {
    display: flex;
    flex-direction: column;
    min-height: 0;
    padding: 0 20px;
    overflow: hidden;

    .panelTitle {
      flex-shrink: 0;
      margin: 0 0 20px;
      font-size: 18px;
    }

    .panelContent {
      flex: 1;
      min-height: 0;
      overflow-x: hidden;
      overflow-y: auto;
      overscroll-behavior: contain;
      padding-left: 5px;
      padding-right: 5px;
      padding-bottom: 50px;
    }
  }

  @media (max-width: 700px) {
    grid-template-columns: 44px minmax(0, 1fr);

    .sidebar {
      min-width: 0;
      scrollbar-gutter: auto;

      .settingsGroupLabel, .settingsItem .itemLabel {
        display: none;
      }

      .settingsGroupLabel + .settingsItem {
        margin-top: 12px;
      }

      .settingsItem {
        justify-content: center;
        padding: 12px 0;
      }
    }

    .content {
      padding-inline: 16px 8px;
    }
  }
}
</style>

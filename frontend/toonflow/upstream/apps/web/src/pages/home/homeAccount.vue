<template>
  <div class="homeAccount" :class="{ collapsed }">
    <button
      class="footerAction"
      type="button"
      :aria-label="translate('密钥与模型')"
      :title="translate('密钥与模型')"
      data-test="sidebar-open-keys"
      @click="emit('openApiKeys')"
    >
      <icon-key :size="18" aria-hidden="true" />
      <span class="actionLabel">{{ translate("密钥与模型") }}</span>
    </button>
    <button
      class="footerAction"
      type="button"
      :aria-label="translate('设置')"
      :title="translate('设置')"
      data-test="sidebar-open-settings"
      @click="emit('openSettings')"
    >
      <icon-settings :size="18" aria-hidden="true" />
      <span class="actionLabel">{{ translate("设置") }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { IconKey, IconSettings } from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";

defineProps<{ collapsed: boolean }>();
const emit = defineEmits<{ openSettings: []; openApiKeys: [] }>();
</script>

<style lang="scss" scoped>
.homeAccount {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;

  .footerAction {
    display: flex;
    align-items: center;
    flex-shrink: 0;
    min-height: 42px;
    width: 100%;
    gap: 10px;
    padding: 10px 12px;
    border: 0;
    border-radius: var(--ui-radius);
    background: transparent;
    color: var(--el-text-color-regular);
    font: inherit;
    font-size: 14px;
    line-height: 1.5;
    text-align: start;
    cursor: pointer;
    transition: gap 220ms ease, padding 220ms ease;

    > svg {
      flex-shrink: 0;
    }

    .actionLabel {
      min-width: 0;
      max-width: 180px;
      overflow: hidden;
      white-space: nowrap;
      text-overflow: ellipsis;
      transition: max-width 220ms ease, opacity 140ms ease;
    }

    &:hover {
      background: var(--el-fill-color);
      color: var(--el-text-color-primary);
    }

    &:focus-visible {
      outline: 2px solid var(--el-color-primary);
      outline-offset: 2px;
    }
  }

  &.collapsed {
    .footerAction {
      gap: 0;
      padding-inline: 14px;

      .actionLabel {
        max-width: 0;
        opacity: 0;
      }
    }
  }

  @media (max-width: 900px) {
    .footerAction {
      gap: 0;
      padding-inline: 14px;

      .actionLabel {
        max-width: 0;
        opacity: 0;
      }
    }
  }
}
</style>

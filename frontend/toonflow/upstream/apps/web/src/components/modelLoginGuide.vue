<template>
  <div class="modelLoginGuide">
    <span>{{ translate("请先在「密钥与模型」中选择站内 API 密钥") }}</span>
    <el-button type="primary" size="small" @click.stop="openLogin">{{ translate("前往配置") }}</el-button>
  </div>
</template>

<script setup lang="ts">
import { translate } from "@toonflow/i18n/vue";
import { inject } from "vue";

const emit = defineEmits<{ login: [] }>();
const openSettingsPanel = inject<(id: string) => void>("openSettingsPanel");

function openLogin() {
  emit("login");
  openSettingsPanel?.("apiKeys");
}
</script>

<style lang="scss" scoped>
.modelLoginGuide {
  display: grid;
  justify-items: center;
  gap: 12px;
  padding: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.5;
  text-align: center;
  white-space: normal;
}
</style>

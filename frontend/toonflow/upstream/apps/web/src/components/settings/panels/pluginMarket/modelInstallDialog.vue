<template>
  <span v-if="false" />
</template>

<script setup lang="ts">
import { ref } from "vue";
import { ElMessage } from "element-plus";
import { translate } from "@toonflow/i18n/vue";

const visible = ref(false);

function open(_id: string): Promise<boolean> {
  ElMessage.warning(translate("当前为纯浏览器本地运行模式，暂不支持桌面端 ONNX 本地模型推理。"));
  return Promise.resolve(false);
}

defineExpose({ open, visible });
</script>

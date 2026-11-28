<template>
  <span v-if="false" />
</template>

<script setup lang="ts">
import { ElMessage } from "element-plus";
import { translate } from "@toonflow/i18n/vue";
import { useFfmpegRequired } from "@/lib/ffmpegEvents";

useFfmpegRequired(() => {
  ElMessage.warning(translate("当前为纯浏览器本地运行模式，暂不支持桌面端 FFmpeg 音视频转码。"));
});
</script>

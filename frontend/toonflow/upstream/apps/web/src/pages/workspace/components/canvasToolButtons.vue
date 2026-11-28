<template>
  <template v-for="button in buttons" :key="button">
    <el-tooltip v-if="button === 'add'" content="添加节点" :placement="placement" :showArrow="false" :hideAfter="0" :enterable="false" :triggerKeys="[]">
      <el-button class="toolButton addNodeButton" :class="{ sideAddButton: placement === 'right' }" :disabled="!ready" aria-label="添加节点" aria-haspopup="menu" @click="emit('add')">
        <icon-plus :size="18" aria-hidden="true" />
      </el-button>
    </el-tooltip>
    <el-tooltip v-else-if="button === 'pointer'" :content="toolHint" :placement="placement" :showArrow="false" :hideAfter="0" :enterable="false" :triggerKeys="[]">
      <el-button class="toolButton" text :type="selectedTool === 'move' ? 'default' : 'primary'" :aria-label="toolHint" @click="selectedTool = currentTool.next">
        <component :is="currentTool.icon" :size="18" aria-hidden="true" />
      </el-button>
    </el-tooltip>
    <el-tooltip v-else-if="button === 'search'" content="搜索节点" :placement="placement" :showArrow="false" :hideAfter="0" :enterable="false" :triggerKeys="[]">
      <el-button class="toolButton" text :disabled="!ready" aria-label="搜索节点" @click="emit('search')">
        <icon-search :size="18" aria-hidden="true" />
      </el-button>
    </el-tooltip>
    <el-tooltip v-else-if="button === 'assets' && hasCanvas && currentUser?.role !== 'user'" :content="assetsVisible ? '关闭素材库' : '打开素材库'" :placement="placement" :showArrow="false" :hideAfter="0" :enterable="false" :triggerKeys="[]">
      <el-button class="toolButton" text :type="assetsVisible ? 'primary' : 'default'" :aria-pressed="assetsVisible" aria-label="素材库" @click="assetsVisible = !assetsVisible">
        <icon-folders :size="18" aria-hidden="true" />
      </el-button>
    </el-tooltip>
  </template>
</template>

<script setup lang="ts">
import { markMessages, translate } from "@toonflow/i18n/vue";
import { computed } from "vue";
import { IconPointer, IconHandStop, IconMarquee2, IconPlus, IconSearch, IconFolders } from "@tabler/icons-vue";
import { touchScreen } from "@/lib/platform";
import { currentUser } from "@/stores/multiTenant";

export type CanvasToolButton = "add" | "pointer" | "search" | "assets" | "help";
export type CanvasPointerTool = "move" | "hand" | "select";
withDefaults(defineProps<{
  buttons: readonly CanvasToolButton[];
  placement?: "top" | "right" | "bottom";
  ready?: boolean;
  hasCanvas?: boolean;
}>(), { placement: "top" });
const emit = defineEmits<{ add: []; search: [] }>();
const selectedTool = defineModel<CanvasPointerTool>("selectedTool", { default: "move" });
const assetsVisible = defineModel<boolean>("assetsVisible", { default: false });
const canvasTools = markMessages({
  move: { label: "移动", icon: IconPointer, next: "hand" },
  hand: { label: "抓手工具", icon: IconHandStop, next: touchScreen ? "select" : "move" },
  select: { label: "框选与多选", icon: IconMarquee2, next: "move" },
} as const);
const currentTool = computed(() => canvasTools[selectedTool.value]);
const toolHint = computed(() => `${translate(currentTool.value.label)} → ${translate(canvasTools[currentTool.value.next].label)}`);
</script>

<style scoped lang="scss">
.toolButton.el-button {
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  margin: 0;
  padding: 0;

  &.addNodeButton {
    margin-right: 6px;
    color: var(--el-bg-color);
    background: var(--el-text-color-primary);
    border-color: transparent;

    &.sideAddButton { margin: 0 0 6px; }
    &:hover { opacity: 0.85; }
    &.is-disabled { opacity: 0.45; }
  }

  @media (any-pointer: coarse) {
    width: 36px;
    height: 36px;
  }
}
</style>

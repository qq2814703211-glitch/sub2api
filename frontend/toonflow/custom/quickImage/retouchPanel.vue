<template>
  <div class="retouchPanel">
    <div class="paramRow">
      <span class="rowLabel">{{ translate("强度") }}</span>
      <div class="pillGroup">
        <button
          v-for="option in RETOUCH_INTENSITIES"
          :key="option.id"
          type="button"
          class="pill"
          :class="{ active: option.id === modelValue.intensity }"
          @click="emit('update:intensity', option.id)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div class="paramRow">
      <span class="rowLabel">{{ translate("主体") }}</span>
      <div class="pillGroup">
        <button
          v-for="option in RETOUCH_SUBJECTS"
          :key="option.id"
          type="button"
          class="pill"
          :class="{ active: option.id === modelValue.subject }"
          @click="emit('update:subject', option.id)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div class="categoryTabs">
      <button
        v-for="category in RETOUCH_CATEGORIES"
        :key="category.id"
        type="button"
        class="pill"
        :class="{ active: category.id === activeCategory }"
        @click="selectCategory(category.id)"
      >
        {{ category.title }}
        <span v-if="countInCategory(category.id)" class="pillCount">{{ countInCategory(category.id) }}</span>
      </button>
    </div>

    <div class="groupTabs">
      <button
        v-for="group in activeGroups"
        :key="group.id"
        type="button"
        class="groupPill"
        :class="{ active: group.id === activeGroup }"
        @click="selectGroup(group.id)"
      >
        {{ group.title }}
        <span v-if="countInGroup(group.id)" class="pillCount">{{ countInGroup(group.id) }}</span>
      </button>
    </div>

    <div class="presetGrid">
      <button
        v-for="preset in presets"
        :key="preset.id"
        type="button"
        class="presetItem"
        :class="{ selected: isSelected(preset.id) }"
        :title="preset.prompt"
        @click="toggle(preset)"
      >
        <div class="presetHead">
          <span class="presetTitle">{{ preset.title }}</span>
          <icon-check v-if="isSelected(preset.id)" :size="14" class="checkIcon" aria-hidden="true" />
        </div>
        <span v-if="preset.scenario" class="presetScenario">{{ preset.scenario }}</span>
      </button>
    </div>

    <div v-if="selections.length" class="retouchFooter">
      <el-button text size="small" @click="emit('clear')">{{ translate("清空") }}</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { IconCheck } from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";
import {
  RETOUCH_CATEGORIES,
  RETOUCH_INTENSITIES,
  RETOUCH_SUBJECTS,
  findRetouchCategory,
  findRetouchGroup,
} from "@host/features/imagegen/retouch";
import type { RetouchParams, RetouchPreset } from "@host/features/imagegen/types";

const props = defineProps<{
  modelValue: RetouchParams;
  activeCategory: string;
  activeGroup: string;
  selectedPreset: string;
  selections: { category: string; group: string; preset: string }[];
}>();

const emit = defineEmits<{
  "update:intensity": [value: string];
  "update:subject": [value: string];
  "update:activeCategory": [value: string];
  "update:activeGroup": [value: string];
  "update:selectedPreset": [value: string];
  toggle: [preset: RetouchPreset];
  clear: [];
}>();

const activeGroups = computed(() => findRetouchCategory(props.activeCategory).groups);
const presets = computed(() =>
  findRetouchGroup(findRetouchCategory(props.activeCategory), props.activeGroup).presets
);

function selectCategory(id: string) {
  emit("update:activeCategory", id);
  const category = findRetouchCategory(id);
  emit("update:activeGroup", category.groups[0]?.id ?? "");
  emit("update:selectedPreset", category.groups[0]?.presets[0]?.id ?? "");
}

function selectGroup(id: string) {
  emit("update:activeGroup", id);
  const group = findRetouchGroup(findRetouchCategory(props.activeCategory), id);
  emit("update:selectedPreset", group.presets[0]?.id ?? "");
}

function isSelected(presetId: string): boolean {
  return props.selections.some(
    (item) =>
      item.category === props.activeCategory &&
      item.group === props.activeGroup &&
      item.preset === presetId
  );
}

function countInCategory(categoryId: string): number {
  return props.selections.filter((item) => item.category === categoryId).length;
}

function countInGroup(groupId: string): number {
  return props.selections.filter(
    (item) => item.category === props.activeCategory && item.group === groupId
  ).length;
}

function toggle(preset: RetouchPreset) {
  emit("update:selectedPreset", preset.id);
  emit("toggle", preset);
}
</script>

<style lang="scss" scoped>
.retouchPanel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 8px;

  .paramRow {
    display: flex;
    align-items: flex-start;
    gap: 8px;

    .rowLabel {
      flex-shrink: 0;
      width: 36px;
      padding-top: 4px;
      color: var(--el-text-color-secondary);
      font-size: 12px;
    }
  }

  .pillGroup,
  .categoryTabs,
  .groupTabs {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .categoryTabs {
    padding-top: 8px;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  .pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius-small);
    background: transparent;
    color: var(--el-text-color-regular);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    transition: border-color 0.15s, background-color 0.15s, color 0.15s;

    &:hover {
      border-color: var(--el-border-color);
      color: var(--el-text-color-primary);
    }

    &.active {
      border-color: var(--el-color-primary);
      background: var(--el-color-primary-light-9);
      color: var(--el-color-primary);
      font-weight: 500;
    }
  }

  .groupPill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 9px;
    border: 0;
    border-radius: var(--ui-radius-small);
    background: var(--el-fill-color-light);
    color: var(--el-text-color-secondary);
    font: inherit;
    font-size: 12px;
    cursor: pointer;

    &.active {
      background: var(--el-fill-color-darker);
      color: var(--el-text-color-primary);
      font-weight: 500;
    }
  }

  .pillCount {
    font-weight: 600;
  }

  .presetGrid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 6px;
    max-height: 220px;
    overflow-y: auto;
  }

  .presetItem {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px 10px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius);
    background: transparent;
    color: var(--el-text-color-primary);
    font: inherit;
    text-align: start;
    cursor: pointer;
    transition: border-color 0.15s, background-color 0.15s;

    &:hover {
      background: var(--el-fill-color-light);
    }

    &.selected {
      border-color: var(--el-color-primary);
      background: var(--el-color-primary-light-9);
    }

    .presetHead {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 6px;

      .presetTitle {
        overflow: hidden;
        font-size: 12px;
        font-weight: 500;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .checkIcon {
        flex-shrink: 0;
        color: var(--el-color-primary);
      }
    }

    .presetScenario {
      overflow: hidden;
      color: var(--el-text-color-secondary);
      font-size: 11px;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .retouchFooter {
    display: flex;
    justify-content: flex-end;
  }
}
</style>

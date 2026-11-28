<template>
  <teleport to="body">
    <div v-if="visible" class="loadingPage" role="status" aria-label="正在加载项目">
      <div class="logoWrap" :class="{ drawn, filling }" aria-hidden="true">
        <div class="logoOutline" @animationend="finishDrawing">
          <div ref="logoElement" class="logoPulse" v-html="logoSvg" />
        </div>
        <img class="logoFill" :src="logoUrl" alt="" @animationend="finishFilling" />
      </div>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";

const emblemRaw = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256"><path fill="#000000" d="M 128 24 C 142 86 170 114 232 128 C 170 142 142 170 128 232 C 114 170 86 142 24 128 C 86 114 114 86 128 24 Z"/><path fill="#000000" d="M 198 34 C 202 52 210 60 228 64 C 210 68 202 76 198 94 C 194 76 186 68 168 64 C 186 60 194 52 198 34 Z"/></svg>`;
const logoUrl = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(emblemRaw)}`;

const props = defineProps<{ active: boolean }>();
const visible = defineModel<boolean>("visible", { default: false });
const drawn = defineModel<boolean>("drawn", { default: false });
const logoDocument = new DOMParser().parseFromString(emblemRaw, "image/svg+xml");
// ACT: 仅拆分当前图形的绝对 M 轮廓；填充复用中性创意图形。
for (const [index, path] of [...logoDocument.querySelectorAll("path")].entries()) {
  const contours = path.getAttribute("d")!.match(/M[^M]*/g)!;
  path.replaceWith(...contours.map(contour => {
    const outline = path.cloneNode(false) as SVGPathElement;
    outline.setAttribute("d", contour);
    outline.setAttribute("pathLength", "1");
    outline.setAttribute("data-part", String(index));
    return outline;
  }));
}
const logoSvg = new XMLSerializer().serializeToString(logoDocument.documentElement);
const logoElement = ref<HTMLElement>();
const filling = ref(false);
const motionPreference = window.matchMedia("(prefers-reduced-motion: reduce)");
const reducedMotion = ref(motionPreference.matches);

function updateMotionPreference(event: MediaQueryListEvent) {
  reducedMotion.value = event.matches;
}
motionPreference.addEventListener("change", updateMotionPreference);
onBeforeUnmount(() => motionPreference.removeEventListener("change", updateMotionPreference));

watch([() => props.active, reducedMotion], ([active, reducedMotion]) => {
  if (active) {
    if (!visible.value) {
      drawn.value = false;
      visible.value = true;
    }
    filling.value = false;
  }
  if (reducedMotion) {
    drawn.value = true;
    if (!active) visible.value = false;
  } else if (!active && drawn.value && visible.value) {
    filling.value = true;
  }
}, { immediate: true });

function finishDrawing(event: AnimationEvent) {
  if (event.target !== logoElement.value?.querySelector("path:last-of-type")) return;
  drawn.value = true;
  if (!props.active) filling.value = true;
}

function finishFilling() {
  if (!props.active) visible.value = false;
}
</script>

<style lang="scss" scoped>
.loadingPage {
  position: fixed;
  inset: 0;
  z-index: 1100;
  background: var(--el-bg-color);

  // 网格单独成层，用 transform 从中心缩小入场，不逐帧重绘背景尺寸。
  &::before {
    content: "";
    position: absolute;
    inset: 0;
    background-image: radial-gradient(var(--el-border-color) 1px, transparent 1px);
    background-position: center;
    background-size: 20px 20px;
    animation: gridZoom 1.2s cubic-bezier(0.33, 1, 0.68, 1) both;

    @media (prefers-reduced-motion: reduce) { animation: none; }
  }

  .logoWrap {
    position: absolute;
    top: 50dvh;
    left: 50vw;
    width: 96px;
    aspect-ratio: 1;
    transform: translate(-50%, -50%);

    // 先沿轮廓投出黑色阴影再整体反相，暗色主题下即为亮色边缘辉光；描边与填充共用。
    .dark & { filter: drop-shadow(0 0 3px rgb(0 0 0 / 60%)) invert(1); }

    .logoOutline {
      width: 100%;
      line-height: 0;
      will-change: opacity, filter;
      // 时长不超过描边，避免收尾填充提前打断模糊过渡。
      animation: logoFocus 0.7s ease-out both;

      :deep(svg) { display: block; width: 100%; height: auto; overflow: visible; }
      :deep(path) {
        stroke: #000;
        stroke-width: 6;
        stroke-linecap: round;
        stroke-linejoin: round;
        fill: none;
        stroke-dasharray: 1;
        stroke-dashoffset: 1;
        animation: logoDraw 0.65s linear both;
      }
      :deep(path[data-part="1"]) { animation-delay: 0.08s; }
    }

    .logoFill {
      position: absolute;
      inset: 0;
      display: block;
      width: 100%;
      height: 100%;
      opacity: 0;
      will-change: opacity;
    }

    &.drawn .logoOutline :deep(path) { animation: none; stroke-dashoffset: 0; }
    &.drawn .logoPulse { animation: logoPulse 0.9s ease-in-out infinite; will-change: opacity; }
    &.filling {
      .logoOutline { animation: logoOutlineOut 0.25s ease both; }
      .logoPulse { animation-play-state: paused; }
      .logoFill { animation: logoFill 0.25s ease both; }
    }

    @media (prefers-reduced-motion: reduce) {
      .logoOutline { opacity: 0; animation: none; }
      .logoOutline :deep(path) { animation: none; }
      &.drawn .logoPulse { animation: none; }
      .logoFill { opacity: 1; }
    }
  }
}

@keyframes gridZoom {
  from { opacity: 0; transform: scale(1.2); }
  to { opacity: 1; transform: scale(1); }
}

@keyframes logoFocus {
  from { opacity: 0; filter: blur(6px); }
  to { opacity: 1; filter: blur(0); }
}

@keyframes logoDraw {
  from { stroke-dashoffset: 1; }
  to { stroke-dashoffset: 0; }
}

@keyframes logoFill {
  0% { opacity: 0; }
  70%, 100% { opacity: 1; }
}

@keyframes logoOutlineOut {
  0% { opacity: 1; }
  70%, 100% { opacity: 0; }
}

@keyframes logoPulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.45; }
}
</style>

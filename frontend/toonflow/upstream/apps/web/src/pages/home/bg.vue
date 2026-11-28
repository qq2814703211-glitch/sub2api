<template>
  <canvas ref="fogCanvas" class="pixelFog" aria-hidden="true" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

type FogPoint = { x: number; y: number; nx: number; ny: number; variation: number; pixelIndex: number };

const props = defineProps<{ scrollTop: number }>();
const fogCanvas = ref<HTMLCanvasElement>();
const events = new AbortController();
const fogPixelSize = 2;
const step = 6;
const pointer = { x: -1000, y: -1000, targetX: -1000, targetY: -1000, strength: 0, targetStrength: 0 };
let context: CanvasRenderingContext2D | null = null;
let fogLayer: HTMLCanvasElement;
let fogContext: CanvasRenderingContext2D | null = null;
let fogPixels: ImageData;
let points: FogPoint[] = [];
let resizeObserver: ResizeObserver;
let themeObserver: MutationObserver;
let reducedMotion: MediaQueryList;
let animationFrame = 0;
let elapsed = 9;
let lastFrame = 0;
let width = 0;
let height = 0;
let pixelRatio = 1;
let inkColor = "";

function resize() {
  const canvas = fogCanvas.value;
  if (!canvas || !context) return;
  width = canvas.clientWidth;
  height = canvas.clientHeight;
  if (!width || !height) return syncAnimation();
  pixelRatio = Math.min(window.devicePixelRatio || 1, 1.5);
  canvas.width = Math.round(width * pixelRatio);
  canvas.height = Math.round(height * pixelRatio);
  context.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
  context.imageSmoothingEnabled = false;
  fogLayer.width = Math.ceil(width / fogPixelSize);
  fogLayer.height = Math.ceil(height / fogPixelSize);
  fogPixels = fogContext!.createImageData(fogLayer.width, fogLayer.height);
  // ACT: 颗粒和点距都按 CSS 像素固定，点数随可视面积变化，避免窗口大小改变疏密。
  points = [];
  for (let x = step / 2; x < width; x += step) {
    for (let y = step / 2; y < height; y += step) {
      const seed = Math.sin(x * 18.23 + y * 73.17) * 43758.55;
      points.push({ x, y, nx: x / width, ny: y / height,
        variation: 0.82 + (seed - Math.floor(seed)) * 0.18,
        pixelIndex: (Math.floor(y / fogPixelSize) * fogLayer.width + Math.floor(x / fogPixelSize)) * 4 + 3 });
    }
  }
  updateScrollPoints();
  syncAnimation();
}

function updateScrollPoints() {
  if (!width || !height) return;
  for (const point of points) {
    const contentY = point.y + props.scrollTop;
    point.ny = contentY / height;
  }
}

watch(() => props.scrollTop, () => {
  updateScrollPoints();
  draw();
});

function draw() {
  if (!context || !fogContext || !fogPixels || !width || !height) return;
  context.clearRect(0, 0, width, height);
  const time = elapsed * 0.7;
  const radius = width < 600 ? 78 : 112;
  let columnX = -1;
  let waveA = 0;
  let waveB = 0;
  let waveC = 0;
  let bandWidth = 0;
  let modulation = 0;
  for (const { x, y, nx, ny, variation, pixelIndex } of points) {
    if (x !== columnX) {
      columnX = x;
      const flowX = 0.7 - nx;
      const amplitude = 0.08 + Math.abs(flowX) * 0.09;
      waveA = 0.46 + Math.sin(nx * 7.5 - time) * amplitude + Math.sin(nx * 15 + time * 0.7) * 0.018;
      waveB = 0.61 + Math.sin(nx * 6.3 - time * 0.79 + 1.7) * amplitude;
      waveC = 0.24 + Math.sin(nx * 8.1 + time * 0.63 + 3.1) * amplitude;
      bandWidth = 0.036 + Math.max(0, flowX) * 0.032;
      modulation = 0.65 + Math.sin(nx * 15 - time * 1.3) * 0.35;
    }
    const fog = ny < 1 ? Math.exp(-(((ny - waveA) / bandWidth) ** 2)) * 0.77
      + Math.exp(-(((ny - waveB) / bandWidth) ** 2)) * 0.53
      + Math.exp(-(((ny - waveC) / (bandWidth * 0.8)) ** 2)) * 0.42 : 0;
    const hover = pointer.strength > 0.001 ? Math.max(0, 1 - Math.hypot(x - pointer.x, y - pointer.y) / radius) ** 2 * pointer.strength : 0;
    const mist = Math.min(1, fog * modulation);
    const size = Math.min(fogPixelSize, (0.55 + step * (mist * 0.49 + hover * 0.47)) * variation);
    fogPixels.data[pixelIndex] = Math.min(0.95, (ny < 1 ? 0.08 : 0) + mist * 0.3 + hover * 0.2)
      * Math.min(1, (size / fogPixelSize) ** 2) * 255;
  }
  // ACT: 点阵统一写入离屏掩膜后批量绘制，避免逐点调用 Canvas fillRect。
  fogContext.putImageData(fogPixels, 0, 0);
  context.drawImage(fogLayer, 0, 0, fogLayer.width * fogPixelSize, fogLayer.height * fogPixelSize);
  context.globalCompositeOperation = "source-in";
  context.fillStyle = inkColor;
  context.fillRect(0, 0, width, height);
  context.globalCompositeOperation = "source-over";
}

function tick(timestamp: number) {
  animationFrame = 0;
  if (events.signal.aborted || document.hidden || reducedMotion.matches || !width || !height) return;
  if (!lastFrame || timestamp - lastFrame >= 1000 / 30) {
    elapsed += lastFrame ? Math.min((timestamp - lastFrame) / 1000, 0.07) : 0;
    lastFrame = timestamp;
    pointer.x += (pointer.targetX - pointer.x) * 0.22;
    pointer.y += (pointer.targetY - pointer.y) * 0.22;
    pointer.strength += (pointer.targetStrength - pointer.strength) * 0.18;
    draw();
  }
  animationFrame = requestAnimationFrame(tick);
}

function syncAnimation() {
  cancelAnimationFrame(animationFrame);
  animationFrame = 0;
  lastFrame = 0;
  draw();
  if (width && height && !document.hidden && !reducedMotion.matches && !events.signal.aborted) {
    animationFrame = requestAnimationFrame(tick);
  }
}

function updateColor() {
  const color = getComputedStyle(fogCanvas.value!).color;
  if (color === inkColor) return;
  inkColor = color;
  draw();
}

onMounted(() => {
  const canvas = fogCanvas.value!;
  context = canvas.getContext("2d");
  if (!context) return;
  fogLayer = document.createElement("canvas");
  fogContext = fogLayer.getContext("2d");
  if (!fogContext) return;
  reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const options = { passive: true, signal: events.signal };
  updateColor();
  resize();
  resizeObserver = new ResizeObserver(resize);
  resizeObserver.observe(canvas);
  themeObserver = new MutationObserver(updateColor);
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "style"] });
  canvas.parentElement!.addEventListener("pointermove", (event) => {
    if (event.pointerType === "touch" || reducedMotion.matches) return;
    const bounds = canvas.getBoundingClientRect();
    pointer.targetX = event.clientX - bounds.left;
    pointer.targetY = event.clientY - bounds.top;
    pointer.targetStrength = +(pointer.targetX >= 0 && pointer.targetX <= bounds.width && pointer.targetY >= 0 && pointer.targetY <= bounds.height);
    if (pointer.strength < 0.01) { pointer.x = pointer.targetX; pointer.y = pointer.targetY; }
  }, options);
  canvas.parentElement!.addEventListener("pointerleave", () => { pointer.targetStrength = 0; }, options);
  window.addEventListener("blur", () => { pointer.targetStrength = 0; }, options);
  document.addEventListener("visibilitychange", syncAnimation, options);
  reducedMotion.addEventListener("change", () => {
    pointer.strength = pointer.targetStrength = 0;
    syncAnimation();
  }, options);
});

onBeforeUnmount(() => {
  events.abort();
  cancelAnimationFrame(animationFrame);
  resizeObserver?.disconnect();
  themeObserver?.disconnect();
});
</script>

<style lang="scss" scoped>
.pixelFog {
  display: block;
  width: 100%;
  height: 100%;
  pointer-events: none;
  color: var(--el-text-color-primary);
  opacity: 0.6;
}
</style>

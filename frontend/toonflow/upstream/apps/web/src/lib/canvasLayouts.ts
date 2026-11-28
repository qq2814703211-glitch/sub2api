import { markMessages } from "@toonflow/i18n";

export const canvasLayouts = markMessages({
  default: {
    label: "默认", shortName: "", side: false, centerControls: false, cornerSearch: false, agent: "beside",
    primary: ["add", "pointer", "search"], controls: ["assets", "view", "zoom"],
  },
  sideSearch: {
    label: "侧栏搜索", shortName: "TN", side: true, centerControls: false, cornerSearch: false, agent: "bottomRight",
    primary: ["add", "search", "assets", "pointer"], controls: ["view", "zoom"],
  },
  centerControls: {
    label: "居中视图栏", shortName: "UD", side: true, centerControls: true, cornerSearch: true, agent: "bottomRight",
    primary: ["add", "assets"], controls: ["pointer", "zoom", "view"],
  },
  zoomFirst: {
    label: "缩放优先", shortName: "LJ", side: true, centerControls: false, cornerSearch: false, agent: "topRight",
    primary: ["add", "assets", "search", "pointer"], controls: ["zoom", "view"],
  },
  pointerFirst: {
    label: "指针优先", shortName: "PT", side: true, centerControls: false, cornerSearch: false, agent: "topRight",
    primary: ["pointer", "add", "search", "assets"], controls: ["zoom", "view"],
  },
  compactTools: {
    label: "紧凑侧栏", shortName: "SK", side: true, centerControls: false, cornerSearch: false, agent: "topRight",
    primary: ["add", "assets"], controls: ["view", "pointer", "zoom", "search"],
  },
  cornerSearch: {
    label: "右上搜索", shortName: "KL", side: true, centerControls: false, cornerSearch: true, agent: "bottomRight",
    primary: ["assets", "add"], controls: ["pointer", "view", "zoom"],
  },
} as const);

export type CanvasLayoutMode = keyof typeof canvasLayouts;

const legacyModes = new Map<string, CanvasLayoutMode>([
  ["A", "default"], ["B", "sideSearch"], ["C", "centerControls"], ["D", "pointerFirst"],
]);

export function resolveCanvasLayout(value: unknown): CanvasLayoutMode {
  if (typeof value !== "string") return "default";
  if (Object.hasOwn(canvasLayouts, value)) return value as CanvasLayoutMode;
  return legacyModes.get(value) ?? "default";
}

interface SpeechRecognitionEvent extends Event {
  readonly resultIndex: number;
  readonly results: SpeechRecognitionResultList;
}

interface SpeechRecognitionErrorEvent extends Event {
  readonly error: string;
  readonly message: string;
}

declare module "*.vue" {
  import type { Component } from "vue";
  const component: Component;
  export default component;
}

declare module "*?raw" {
  const content: string;
  export default content;
}

declare module "@toonflow-upstream/packages/ext/*" {
  import type { ExtDefinition } from "@toonflow/ext-scaffold/runtime";
  const ext: ExtDefinition;
  export default ext;
}

declare module "@toonflow-upstream/*" {
  import type { Component } from "vue";
  const defaultExport: Component & Record<string, unknown>;
  export default defaultExport;
}

declare module "@/lib/tiptapHost" {
  const host: Record<string, unknown>;
  export default host;
}

declare module "@/stores/settings" {
  import type { Ref } from "vue";
  export const settings: Ref<Record<string, unknown>>;
  export function saveSettings(
    patch?: Record<string, unknown> | ((current: Record<string, unknown>) => Record<string, unknown>),
    signal?: AbortSignal
  ): Promise<boolean>;
}

declare module "@/stores/workspace" {
  export interface Project {
    directory: string;
    name: string;
    lastOpenedAt: number;
  }
  export function useWorkspaceStore(): {
    project: Project | null;
    projectList: Project[];
    openProject(directory: string, previousDirectory?: string): Promise<void>;
  };
}

declare module "@toonflow/nodes-scaffold/nodeAi" {
  export function invalidateNodeModels(type?: "language" | "media"): void;
}

declare module "@toonflow/plugin-server" {
  export interface PluginServer {
    id?: string;
    [key: string]: unknown;
  }
}

declare module "@toonflow/plugin-server/localModels" {
  export interface LocalModelDefinition {
    id: string;
    name?: string;
    description?: string;
  }
}

declare module "@toonflow/plugin-server/client" {
  export function createServerClient(options?: Record<string, unknown>): Record<string, unknown>;
}

declare module "@toonflow/plugin-server/localModelClient" {
  export type LocalModelTensorType = string;
  export interface LocalModelTensor {
    type: LocalModelTensorType;
    dims: number[];
    data: Uint8Array;
  }
  export interface LocalModelDefinition {
    id: string;
    name?: string;
    description?: string;
  }
  export interface LocalModelItem extends LocalModelDefinition {
    installed?: boolean;
    ready?: boolean;
  }
  export interface LocalModelRunRequest {
    modelId: string;
    inputs: Record<string, LocalModelTensor>;
  }
  export interface LocalModelRunResult {
    outputs: Record<string, LocalModelTensor>;
  }
  export interface LocalModelClient {
    run(request: LocalModelRunRequest, signal?: AbortSignal): Promise<LocalModelRunResult>;
  }
  export function localModelId(name: string): string;
  export function packLocalTensor(tensor: LocalModelTensor): Uint8Array;
  export function unpackLocalTensor(bytes: Uint8Array): LocalModelTensor;
}

declare module "@toonflow/tools-scaffold/runtime" {
  export interface CanvasToolCall {
    name: string;
    args: Record<string, unknown>;
  }
  export interface CanvasInfo {
    id: string;
    tools: unknown[];
  }
  export interface CanvasContext extends CanvasInfo {
    getNodeLabel?(nodeId: string): string | undefined;
    call(request: CanvasToolCall, signal?: AbortSignal): Promise<unknown>;
  }
}

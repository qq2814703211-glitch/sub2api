<template>
  <div class="flex flex-col gap-3">
    <!-- Canvas top toolbar -->
    <div
      class="card flex flex-wrap items-center justify-between gap-2 px-3 py-2.5"
      data-test="creative-canvas-toolbar"
    >
      <!-- Add nodes -->
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="mr-1 text-[11px] font-medium text-gray-500 dark:text-dark-400">
          {{ t('creative.canvas.addNode') }}
        </span>
        <button
          v-for="item in nodePalette"
          :key="item.type"
          type="button"
          class="btn btn-secondary btn-sm text-xs"
          :data-test="`add-node-${item.type}`"
          @click="addNodeAtCenter(item.type)"
        >
          <Icon :name="item.icon" size="xs" />
          {{ t(item.labelKey) }}
        </button>
      </div>

      <!-- Canvas utilities & execution -->
      <div class="flex flex-wrap items-center gap-1.5">
        <div class="relative">
          <Icon
            name="search"
            size="xs"
            class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model="searchQuery"
            type="search"
            class="input h-8 w-36 pl-7 text-xs sm:w-44"
            :placeholder="t('creative.canvas.searchNodes')"
          />
        </div>

        <button
          type="button"
          class="btn btn-sm"
          :class="handMode ? 'btn-primary' : 'btn-secondary'"
          :title="t('creative.canvas.handMode')"
          @click="handMode = !handMode"
        >
          {{ handMode ? t('creative.canvas.panTool') : t('creative.canvas.selectTool') }}
        </button>

        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="!canUndo"
          :title="t('creative.canvas.undo')"
          @click="emit('undo')"
        >
          {{ t('creative.canvas.undo') }}
        </button>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="!canRedo"
          :title="t('creative.canvas.redo')"
          @click="emit('redo')"
        >
          {{ t('creative.canvas.redo') }}
        </button>

        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :title="t('creative.canvas.arrange')"
          @click="emit('arrange')"
        >
          <Icon name="grid" size="xs" />
          {{ t('creative.canvas.arrange') }}
        </button>

        <div class="flex items-center rounded-lg border border-gray-200 dark:border-dark-700">
          <button
            type="button"
            class="px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800"
            @click="adjustZoom(-0.1)"
          >
            -
          </button>
          <button
            type="button"
            class="px-2 py-1 text-xs font-medium text-gray-700 hover:bg-gray-100 dark:text-dark-200 dark:hover:bg-dark-800"
            :title="t('creative.canvas.fitView')"
            @click="fitView"
          >
            {{ Math.round(viewport.zoom * 100) }}%
          </button>
          <button
            type="button"
            class="px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800"
            @click="adjustZoom(0.1)"
          >
            +
          </button>
        </div>

        <button
          v-if="!running"
          type="button"
          class="btn btn-primary btn-sm"
          data-test="run-workflow-btn"
          :disabled="nodes.length === 0"
          @click="emit('runWorkflow')"
        >
          <Icon name="play" size="xs" />
          {{ t('creative.canvas.runAll') }}
        </button>
        <button
          v-else
          type="button"
          class="btn btn-danger btn-sm"
          @click="emit('stopExecution')"
        >
          <Icon name="x" size="xs" />
          {{ t('creative.canvas.stop') }}
        </button>
      </div>
    </div>

    <!-- Main infinite stage -->
    <div
      ref="stageRef"
      class="relative h-[680px] w-full overflow-hidden rounded-xl border border-gray-200 bg-gray-50 select-none dark:border-dark-700 dark:bg-dark-950"
      :class="isPanning ? 'cursor-grabbing' : handMode ? 'cursor-grab' : 'cursor-default'"
      :style="gridBackgroundStyle"
      data-test="creative-canvas-stage"
      @pointerdown="onStagePointerDown"
      @pointermove="onStagePointerMove"
      @pointerup="onStagePointerUp"
      @pointerleave="onStagePointerUp"
      @dblclick="onStageDoubleClick"
      @wheel.prevent="onStageWheel"
      @dragover.prevent="fileDropActive = true"
      @dragleave="fileDropActive = false"
      @drop.prevent="onStageDrop"
    >
      <!-- Drop overlay -->
      <div
        v-if="fileDropActive"
        class="pointer-events-none absolute inset-0 z-30 flex items-center justify-center bg-primary-500/10 ring-2 ring-inset ring-primary-500"
      >
        <div class="rounded-xl bg-white/95 px-5 py-3 text-sm font-medium text-primary-700 shadow-lg dark:bg-dark-900/95 dark:text-primary-300">
          {{ t('creative.canvas.dropMediaHint') }}
        </div>
      </div>

      <!-- Pending connection banner -->
      <div
        v-if="pendingLink"
        class="absolute left-1/2 top-3 z-20 flex -translate-x-1/2 items-center gap-2 rounded-full border border-primary-300 bg-primary-50/95 px-3 py-1 text-xs font-medium text-primary-800 shadow-sm dark:border-primary-700 dark:bg-primary-900/80 dark:text-primary-200"
      >
        <Icon name="link" size="xs" />
        <span>{{ t('creative.canvas.connectingHint', { port: portLabel(pendingLink.sourcePort) }) }}</span>
        <button type="button" class="underline" @click="pendingLink = null">
          {{ t('common.cancel') }}
        </button>
      </div>

      <!-- Transformed world layer -->
      <div
        class="absolute left-0 top-0 origin-top-left"
        :style="{ transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})` }"
      >
        <!-- SVG edges layer -->
        <svg
          class="pointer-events-none absolute left-0 top-0 overflow-visible"
          width="1"
          height="1"
        >
          <g v-for="edge in renderedEdges" :key="edge.id">
            <!-- Invisible wide hit path for easier clicking -->
            <path
              :d="edge.geometry.path"
              fill="none"
              stroke="transparent"
              stroke-width="18"
              class="pointer-events-auto cursor-pointer"
              @click.stop="selectedEdgeId = selectedEdgeId === edge.id ? null : edge.id"
            />
            <path
              :d="edge.geometry.path"
              fill="none"
              :stroke="edgeColor(edge.sourcePort)"
              :stroke-width="selectedEdgeId === edge.id ? 3.5 : 2.5"
              stroke-linecap="round"
              :stroke-dasharray="running ? '6 4' : undefined"
              class="transition-all"
            />
          </g>

          <!-- In-progress drag link preview -->
          <path
            v-if="pendingLinkPath"
            :d="pendingLinkPath"
            fill="none"
            :stroke="edgeColor(pendingLink!.sourcePort)"
            stroke-width="2.5"
            stroke-dasharray="5 5"
          />
        </svg>

        <!-- Edge disconnect pill (Toonflow style) -->
        <button
          v-if="selectedEdgeInfo"
          type="button"
          class="absolute z-20 flex h-6 w-6 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-red-600 text-white shadow-md hover:bg-red-700"
          :style="{
            left: `${selectedEdgeInfo.geometry.midX}px`,
            top: `${selectedEdgeInfo.geometry.midY}px`,
          }"
          :title="t('creative.canvas.disconnectEdge')"
          @click.stop="disconnectSelectedEdge"
        >
          <Icon name="x" size="xs" />
        </button>

        <!-- Node cards -->
        <article
          v-for="node in nodes"
          :key="node.id"
          :data-test="`canvas-node-${node.type}`"
          class="absolute flex flex-col rounded-xl border bg-white shadow-sm transition-shadow dark:bg-dark-900"
          :class="[
            selectedNodeId === node.id
              ? 'z-10 border-primary-500 ring-2 ring-primary-500/20 shadow-md dark:border-primary-400'
              : 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600',
            searchQuery && !matchesSearch(node) ? 'opacity-30' : 'opacity-100',
          ]"
          :style="{
            left: `${node.x}px`,
            top: `${node.y}px`,
            width: `${node.width || 300}px`,
          }"
          @pointerdown.stop="selectedNodeId = node.id; selectedEdgeId = null"
        >
          <!-- Input ports (left edge) -->
          <div
            v-for="(port, pIdx) in NODE_PORTS[node.type].inputs"
            :key="`in-${port}`"
            class="absolute -left-2.5 flex items-center"
            :style="{ top: `${50 + pIdx * 30}px` }"
          >
            <button
              type="button"
              class="flex h-5 w-5 items-center justify-center rounded-full border-2 bg-white shadow-sm transition-transform hover:scale-125 dark:bg-dark-900"
              :class="[
                portRingClass(port),
                pendingLink && pendingLink.sourcePort === port && pendingLink.sourceId !== node.id
                  ? 'scale-125 ring-4 ring-primary-400/40'
                  : '',
              ]"
              :title="`${t('creative.canvas.inputPort')}: ${portLabel(port)}`"
              @pointerup.stop="completeConnection(node.id, port)"
              @click.stop="completeConnection(node.id, port)"
            >
              <span class="h-2 w-2 rounded-full" :class="portDotClass(port)" />
            </button>
          </div>

          <!-- Output ports (right edge) -->
          <div
            v-for="(port, pIdx) in NODE_PORTS[node.type].outputs"
            :key="`out-${port}`"
            class="absolute -right-2.5 flex items-center"
            :style="{ top: `${50 + pIdx * 30}px` }"
          >
            <button
              type="button"
              class="flex h-5 w-5 items-center justify-center rounded-full border-2 bg-white shadow-sm transition-transform hover:scale-125 dark:bg-dark-900"
              :class="portRingClass(port)"
              :title="`${t('creative.canvas.outputPort')}: ${portLabel(port)}`"
              @pointerdown.stop="startConnection(node.id, port, $event)"
              @click.stop="toggleClickConnection(node.id, port)"
            >
              <span class="h-2 w-2 rounded-full" :class="portDotClass(port)" />
            </button>
          </div>

          <!-- Draggable Node Header -->
          <div
            class="flex cursor-move items-center justify-between gap-2 rounded-t-xl border-b border-gray-100 px-3 py-2 dark:border-dark-800"
            :class="nodeHeaderAccentClass(node.type)"
            @pointerdown.stop="startNodeDrag(node, $event)"
          >
            <div class="flex min-w-0 items-center gap-1.5">
              <Icon :name="nodeTypeIcon(node.type)" size="xs" class="shrink-0" />
              <input
                :value="node.title"
                type="text"
                class="min-w-0 flex-1 bg-transparent text-xs font-semibold text-gray-900 focus:outline-none dark:text-white"
                @pointerdown.stop
                @change="emit('updateTitle', node.id, ($event.target as HTMLInputElement).value)"
              />
            </div>
            <div class="flex shrink-0 items-center gap-1" @pointerdown.stop>
              <span
                v-if="node.status !== 'idle'"
                class="badge text-[10px]"
                :class="statusBadgeClass(node.status)"
              >
                {{ statusLabel(node.status, node.elapsedMs) }}
              </span>
              <button
                type="button"
                class="rounded p-1 text-gray-500 hover:bg-white/70 hover:text-primary-600 dark:text-dark-300 dark:hover:bg-dark-800"
                :title="t('creative.canvas.runNode')"
                :disabled="running"
                @click="emit('runNode', node.id)"
              >
                <Icon name="play" size="xs" />
              </button>
              <button
                type="button"
                class="rounded p-1 text-gray-500 hover:bg-white/70 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800"
                :title="t('creative.canvas.duplicateNode')"
                @click="emit('duplicateNode', node.id)"
              >
                <Icon name="plus" size="xs" />
              </button>
              <button
                type="button"
                class="rounded p-1 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:text-dark-300 dark:hover:bg-red-900/30"
                :title="t('creative.canvas.deleteNode')"
                @click="emit('removeNode', node.id)"
              >
                <Icon name="trash" size="xs" />
              </button>
            </div>
          </div>

          <!-- Node Body -->
          <div class="space-y-2 p-3 text-xs" @pointerdown.stop>
            <!-- Port Legend Chips -->
            <div class="flex flex-wrap items-center justify-between gap-1 text-[10px] text-gray-400 dark:text-dark-400">
              <span>
                <template v-if="NODE_PORTS[node.type].inputs.length">
                  {{ t('creative.canvas.inShort') }}:
                  {{ NODE_PORTS[node.type].inputs.map(portLabel).join(' / ') }}
                </template>
                <template v-else>{{ t('creative.canvas.sourceNode') }}</template>
              </span>
              <span>
                {{ t('creative.canvas.outShort') }}:
                {{ NODE_PORTS[node.type].outputs.map(portLabel).join(' / ') }}
              </span>
            </div>

            <!-- TEXT / SCRIPT NODE -->
            <template v-if="node.type === 'text'">
              <div class="grid grid-cols-3 gap-1">
                <button
                  v-for="mode in textModes"
                  :key="mode.value"
                  type="button"
                  class="rounded-md border px-1.5 py-1 text-[11px] transition-colors"
                  :class="
                    node.data.mode === mode.value
                      ? 'border-primary-500 bg-primary-50 font-medium text-primary-700 dark:border-primary-500 dark:bg-primary-900/30 dark:text-primary-300'
                      : 'border-gray-200 text-gray-600 dark:border-dark-700 dark:text-dark-300'
                  "
                  @click="emit('updateData', node.id, { mode: mode.value })"
                >
                  {{ t(mode.labelKey) }}
                </button>
              </div>
              <textarea
                :value="node.data.text"
                rows="4"
                class="input w-full resize-y text-xs"
                :placeholder="t('creative.nodes.textPlaceholder')"
                @input="emit('updateData', node.id, { text: ($event.target as HTMLTextAreaElement).value })"
              />
              <div v-if="node.data.mode !== 'note'" class="space-y-1">
                <input
                  :value="node.data.model"
                  list="creative-text-models"
                  class="input h-7 text-[11px]"
                  :placeholder="t('creative.nodes.textModelPlaceholder')"
                  @input="emit('updateData', node.id, { model: ($event.target as HTMLInputElement).value })"
                />
              </div>
            </template>

            <!-- IMAGE ASSET NODE -->
            <template v-else-if="node.type === 'image'">
              <div
                v-if="node.data.src"
                class="overflow-hidden rounded-lg border border-gray-200 bg-gray-100 dark:border-dark-700 dark:bg-dark-800"
              >
                <img
                  :src="node.data.src"
                  :alt="node.data.name || node.title"
                  class="max-h-44 w-full object-contain"
                />
              </div>
              <label
                class="flex cursor-pointer items-center justify-center gap-1.5 rounded-lg border border-dashed border-gray-300 px-3 py-2 text-[11px] text-gray-600 hover:border-primary-400 hover:text-primary-600 dark:border-dark-600 dark:text-dark-300"
              >
                <Icon name="upload" size="xs" />
                <span>{{ node.data.src ? t('creative.nodes.replaceImage') : t('creative.nodes.pickLocalImage') }}</span>
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  class="hidden"
                  @change="onPickNodeImage(node.id, $event)"
                />
              </label>
              <div v-if="node.data.src" class="flex flex-wrap items-center gap-1.5">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm text-[11px]"
                  @click="emit('saveMediaAsset', { name: node.title, category: 'generated', kind: 'image', mimeType: node.data.mimeType || 'image/png', dataUrl: node.data.src })"
                >
                  {{ t('creative.actions.saveAsset') }}
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm text-[11px]"
                  @click="emit('sendToQuickStudio', { prompt: '', imageDataUrl: node.data.src, title: node.title })"
                >
                  {{ t('creative.actions.editInQuickStudio') }}
                </button>
              </div>
            </template>

            <!-- IMAGE GENERATION NODE -->
            <template v-else-if="node.type === 'imageGen'">
              <p
                v-if="upstreamSummary(node.id)"
                class="rounded bg-emerald-50 px-2 py-1 text-[10px] text-emerald-700 dark:bg-emerald-900/25 dark:text-emerald-300"
              >
                {{ upstreamSummary(node.id) }}
              </p>
              <textarea
                :value="node.data.prompt"
                rows="2"
                class="input w-full resize-y text-xs"
                :placeholder="t('creative.nodes.imagePromptPlaceholder')"
                @input="emit('updateData', node.id, { prompt: ($event.target as HTMLTextAreaElement).value })"
              />
              <div class="grid grid-cols-2 gap-1.5">
                <input
                  :value="node.data.model"
                  list="creative-image-models"
                  class="input h-7 text-[11px]"
                  :placeholder="t('imagegen.modelLabel')"
                  @input="emit('updateData', node.id, { model: ($event.target as HTMLInputElement).value })"
                />
                <select
                  :value="node.data.size"
                  class="input h-7 text-[11px]"
                  @change="emit('updateData', node.id, { size: ($event.target as HTMLSelectElement).value })"
                >
                  <option value="1024x1024">1024x1024 (1:1)</option>
                  <option value="1536x1024">1536x1024 (3:2)</option>
                  <option value="1024x1536">1024x1536 (2:3)</option>
                  <option value="2048x1152">2048x1152 (16:9)</option>
                  <option value="1152x2048">1152x2048 (9:16)</option>
                </select>
              </div>
              <div
                v-if="node.data.outputImages.length"
                class="space-y-1.5"
              >
                <div class="overflow-hidden rounded-lg border border-gray-200 bg-gray-100 dark:border-dark-700 dark:bg-dark-800">
                  <img
                    :src="node.data.outputImages[node.data.selectedOutputIndex] || node.data.outputImages[0]"
                    :alt="node.title"
                    class="max-h-48 w-full object-contain"
                  />
                </div>
                <div v-if="node.data.outputImages.length > 1" class="flex gap-1 overflow-x-auto pb-0.5">
                  <button
                    v-for="(imgSrc, imgIdx) in node.data.outputImages"
                    :key="imgIdx"
                    type="button"
                    class="h-10 w-10 shrink-0 overflow-hidden rounded border transition-all"
                    :class="
                      node.data.selectedOutputIndex === imgIdx
                        ? 'border-primary-500 ring-1 ring-primary-500'
                        : 'border-gray-200 opacity-75 hover:opacity-100 dark:border-dark-700'
                    "
                    @click="emit('updateData', node.id, { selectedOutputIndex: imgIdx })"
                  >
                    <img :src="imgSrc" :alt="`${node.title} ${imgIdx + 1}`" class="h-full w-full object-cover" />
                  </button>
                </div>
                <div class="flex flex-wrap items-center gap-1">
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-[11px]"
                    @click="downloadMedia(node.data.outputImages[node.data.selectedOutputIndex] || node.data.outputImages[0], `${node.title}.${node.data.format}`)"
                  >
                    {{ t('imagegen.download') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-[11px]"
                    @click="emit('saveMediaAsset', { name: node.title, category: 'generated', kind: 'image', mimeType: `image/${node.data.format}`, dataUrl: node.data.outputImages[node.data.selectedOutputIndex] || node.data.outputImages[0], prompt: node.data.prompt })"
                  >
                    {{ t('creative.actions.saveAsset') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-[11px]"
                    @click="emit('sendToQuickStudio', { prompt: node.data.prompt, model: node.data.model, size: node.data.size, imageDataUrl: node.data.outputImages[node.data.selectedOutputIndex] || node.data.outputImages[0], title: node.title })"
                  >
                    {{ t('creative.actions.editInQuickStudio') }}
                  </button>
                </div>
              </div>
            </template>

            <!-- VIDEO GENERATION NODE -->
            <template v-else-if="node.type === 'videoGen'">
              <p
                v-if="upstreamSummary(node.id)"
                class="rounded bg-violet-50 px-2 py-1 text-[10px] text-violet-700 dark:bg-violet-900/25 dark:text-violet-300"
              >
                {{ upstreamSummary(node.id) }}
              </p>
              <textarea
                :value="node.data.prompt"
                rows="2"
                class="input w-full resize-y text-xs"
                :placeholder="t('creative.nodes.videoPromptPlaceholder')"
                @input="emit('updateData', node.id, { prompt: ($event.target as HTMLTextAreaElement).value })"
              />
              <div class="grid grid-cols-2 gap-1.5">
                <input
                  :value="node.data.model"
                  list="creative-video-models"
                  class="input h-7 text-[11px]"
                  :placeholder="t('creative.nodes.videoModelPlaceholder')"
                  @input="emit('updateData', node.id, { model: ($event.target as HTMLInputElement).value })"
                />
                <select
                  :value="node.data.protocol"
                  class="input h-7 text-[11px]"
                  @change="emit('updateData', node.id, { protocol: ($event.target as HTMLSelectElement).value })"
                >
                  <option value="auto">{{ t('creative.nodes.videoProtoAuto') }}</option>
                  <option value="grok">Grok /v1/videos</option>
                  <option value="compatible">OpenAI /v1/video</option>
                  <option value="seedance">Seedance /api/v3</option>
                </select>
              </div>
              <div class="grid grid-cols-2 gap-1.5">
                <select
                  :value="node.data.aspectRatio"
                  class="input h-7 text-[11px]"
                  @change="emit('updateData', node.id, { aspectRatio: ($event.target as HTMLSelectElement).value })"
                >
                  <option value="16:9">16:9</option>
                  <option value="9:16">9:16</option>
                  <option value="1:1">1:1</option>
                </select>
                <input
                  :value="node.data.duration"
                  type="number"
                  min="2"
                  max="15"
                  class="input h-7 text-[11px]"
                  :title="t('creative.nodes.durationSec')"
                  @input="emit('updateData', node.id, { duration: Number(($event.target as HTMLInputElement).value) || 5 })"
                />
              </div>
              <p v-if="node.data.taskStatus" class="text-[10px] text-gray-500 dark:text-dark-400">
                {{ t('creative.nodes.taskStatus') }}: {{ node.data.taskStatus }}
                <span v-if="node.data.taskId" class="font-mono opacity-75">({{ node.data.taskId.slice(0, 12) }})</span>
              </p>
              <div v-if="node.data.outputVideoUrl" class="space-y-1.5">
                <video
                  :src="node.data.outputVideoUrl"
                  controls
                  playsinline
                  class="max-h-44 w-full rounded-lg border border-gray-200 bg-black dark:border-dark-700"
                />
                <div class="flex flex-wrap items-center gap-1">
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-[11px]"
                    @click="downloadMedia(node.data.outputVideoUrl, `${node.title}.mp4`)"
                  >
                    {{ t('imagegen.download') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-[11px]"
                    @click="emit('saveMediaAsset', { name: node.title, category: 'video', kind: 'video', mimeType: 'video/mp4', dataUrl: node.data.outputVideoUrl, prompt: node.data.prompt })"
                  >
                    {{ t('creative.actions.saveAsset') }}
                  </button>
                </div>
              </div>
            </template>

            <!-- BROWSER-LOCAL FRAME EXTRACTION NODE (Zero Server FFmpeg) -->
            <template v-else-if="node.type === 'frameExtract'">
              <p class="rounded bg-amber-50 px-2 py-1 text-[10px] text-amber-800 dark:bg-amber-900/25 dark:text-amber-300">
                {{ t('creative.nodes.localFrameHint') }}
              </p>
              <div class="flex items-center gap-1.5">
                <label class="btn btn-secondary btn-sm flex-1 cursor-pointer justify-center text-[11px]">
                  <Icon name="upload" size="xs" />
                  {{ node.data.sourceName || t('creative.nodes.pickLocalVideo') }}
                  <input
                    type="file"
                    accept="video/*"
                    class="hidden"
                    @change="onPickNodeVideo(node.id, $event)"
                  />
                </label>
                <input
                  :value="node.data.frameCount"
                  type="number"
                  min="1"
                  max="12"
                  class="input h-7 w-16 text-[11px]"
                  :title="t('creative.nodes.frameCount')"
                  @input="emit('updateData', node.id, { frameCount: Number(($event.target as HTMLInputElement).value) || 4 })"
                />
              </div>
              <div v-if="node.data.frames.length" class="space-y-1.5">
                <div class="grid grid-cols-2 gap-1.5">
                  <div
                    v-for="(frame, fIdx) in node.data.frames"
                    :key="frame.id"
                    class="group relative cursor-pointer overflow-hidden rounded border transition-all"
                    :class="
                      node.data.selectedFrameIndex === fIdx
                        ? 'border-primary-500 ring-1 ring-primary-500'
                        : 'border-gray-200 dark:border-dark-700'
                    "
                    @click="emit('updateData', node.id, { selectedFrameIndex: fIdx })"
                  >
                    <img :src="frame.dataUrl" :alt="`${frame.timeSec}s`" class="h-20 w-full object-cover" />
                    <span class="absolute bottom-0.5 left-0.5 rounded bg-black/70 px-1 text-[9px] text-white">
                      {{ frame.timeSec }}s
                    </span>
                    <button
                      type="button"
                      class="absolute right-0.5 top-0.5 rounded bg-primary-600 px-1 py-0.5 text-[9px] text-white opacity-90 hover:bg-primary-700"
                      :title="t('creative.nodes.spawnFrameNode')"
                      @click.stop="emit('spawnFrameNode', node.id, frame)"
                    >
                      +
                    </button>
                  </div>
                </div>
              </div>
            </template>

            <!-- Node error alert -->
            <p
              v-if="node.error"
              class="rounded-md bg-red-50 px-2 py-1 text-[11px] text-red-700 dark:bg-red-900/30 dark:text-red-300"
            >
              {{ node.error }}
            </p>
          </div>
        </article>
      </div>

      <!-- Double-click Quick Add Node Menu (Toonflow style) -->
      <div
        v-if="quickMenu"
        class="absolute z-30 w-44 rounded-xl border border-gray-200 bg-white/95 p-1.5 shadow-lg backdrop-blur dark:border-dark-700 dark:bg-dark-900/95"
        :style="{ left: `${quickMenu.screenX}px`, top: `${quickMenu.screenY}px` }"
        data-test="canvas-quick-menu"
        @pointerdown.stop
      >
        <p class="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-400">
          {{ t('creative.canvas.addNode') }}
        </p>
        <button
          v-for="item in nodePalette"
          :key="`qm-${item.type}`"
          type="button"
          class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-gray-700 hover:bg-primary-50 hover:text-primary-700 dark:text-dark-200 dark:hover:bg-primary-900/30 dark:hover:text-primary-300"
          @click="pickQuickMenuNode(item.type)"
        >
          <Icon :name="item.icon" size="xs" />
          <span>{{ t(item.labelKey) }}</span>
        </button>
      </div>

      <!-- Empty state -->
      <div
        v-if="nodes.length === 0"
        class="pointer-events-none absolute inset-0 flex items-center justify-center p-6"
      >
        <div class="rounded-xl border border-dashed border-gray-300 bg-white/90 px-6 py-5 text-center shadow-sm dark:border-dark-700 dark:bg-dark-900/90">
          <Icon name="sparkles" size="lg" class="mx-auto text-primary-500" />
          <p class="mt-2 text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('creative.canvas.emptyTitle') }}
          </p>
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('creative.canvas.emptyHint') }}
          </p>
        </div>
      </div>

      <!-- Minimap -->
      <div
        v-if="nodes.length > 0"
        class="absolute bottom-3 right-3 z-20 hidden overflow-hidden rounded-lg border border-gray-200 bg-white/90 p-1.5 shadow-sm backdrop-blur sm:block dark:border-dark-700 dark:bg-dark-900/90"
        style="width: 156px; height: 100px"
        :title="t('creative.canvas.minimap')"
      >
        <svg viewBox="0 0 144 88" class="h-full w-full">
          <rect
            v-for="mini in minimapNodes"
            :key="mini.id"
            :x="mini.x"
            :y="mini.y"
            :width="mini.w"
            :height="mini.h"
            rx="2"
            :fill="mini.fill"
            :opacity="selectedNodeId === mini.id ? 1 : 0.7"
          />
        </svg>
      </div>

      <!-- Bottom-left keyboard / compute hint -->
      <div class="pointer-events-none absolute bottom-3 left-3 z-10 rounded-md bg-white/85 px-2.5 py-1 text-[10px] text-gray-500 backdrop-blur dark:bg-dark-900/85 dark:text-dark-400">
        {{ t('creative.canvas.footerHint') }}
      </div>
    </div>

    <!-- Datalists for discovered station gateway models -->
    <datalist id="creative-text-models">
      <option v-for="m in textModels" :key="m" :value="m" />
    </datalist>
    <datalist id="creative-image-models">
      <option v-for="m in imageModels" :key="m" :value="m" />
    </datalist>
    <datalist id="creative-video-models">
      <option v-for="m in videoModels" :key="m" :value="m" />
    </datalist>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { downloadUrl } from '@/features/imagegen/api'
import {
  NODE_PORTS,
  buildBezierGeometry,
  getPortAnchor,
} from './graph'
import { optimizeLocalImageDataUrl } from './mediaBrowser'
import type {
  AssetCategory,
  CanvasEdge,
  CanvasNode,
  CanvasNodeType,
  CanvasViewport,
  ExtractedFrame,
  NodeRunStatus,
  PortKind,
  QuickImageBridgePayload,
} from './types'

const props = defineProps<{
  nodes: CanvasNode[]
  edges: CanvasEdge[]
  viewport: CanvasViewport
  running: boolean
  canUndo: boolean
  canRedo: boolean
  textModels: string[]
  imageModels: string[]
  videoModels: string[]
}>()

const emit = defineEmits<{
  addNode: [type: CanvasNodeType, x: number, y: number]
  updateTitle: [nodeId: string, title: string]
  updateData: [nodeId: string, patch: Record<string, unknown>]
  moveNode: [nodeId: string, x: number, y: number, snap: boolean]
  duplicateNode: [nodeId: string]
  removeNode: [nodeId: string]
  connect: [sourceId: string, sourcePort: PortKind, targetId: string, targetPort: PortKind]
  removeEdge: [edgeId: string]
  arrange: []
  undo: []
  redo: []
  setViewport: [patch: Partial<CanvasViewport>]
  runNode: [nodeId: string]
  runWorkflow: []
  stopExecution: []
  dropFiles: [files: File[], x: number, y: number]
  spawnFrameNode: [sourceNodeId: string, frame: ExtractedFrame]
  saveMediaAsset: [payload: { name: string; category: AssetCategory; kind: 'image' | 'video'; mimeType: string; dataUrl: string; prompt?: string }]
  sendToQuickStudio: [payload: QuickImageBridgePayload]
}>()

const { t } = useI18n()

const stageRef = ref<HTMLElement | null>(null)
const selectedNodeId = ref<string | null>(null)
const selectedEdgeId = ref<string | null>(null)
const searchQuery = ref('')
const handMode = ref(false)
const fileDropActive = ref(false)

// Panning & node dragging state
const isPanning = ref(false)
let panOrigin = { clientX: 0, clientY: 0, vpX: 0, vpY: 0 }
let draggingNode: { id: string; startClientX: number; startClientY: number; startX: number; startY: number } | null = null

// Port connection state
const pendingLink = ref<{
  sourceId: string
  sourcePort: PortKind
  cursorX: number
  cursorY: number
} | null>(null)

// Toonflow-style double-click quick node creation menu
const quickMenu = ref<{
  screenX: number
  screenY: number
  canvasX: number
  canvasY: number
} | null>(null)

const nodePalette: Array<{ type: CanvasNodeType; labelKey: string; icon: 'edit' | 'upload' | 'sparkles' | 'play' | 'cpu' }> = [
  { type: 'text', labelKey: 'creative.nodes.typeText', icon: 'edit' },
  { type: 'image', labelKey: 'creative.nodes.typeImage', icon: 'upload' },
  { type: 'imageGen', labelKey: 'creative.nodes.typeImageGen', icon: 'sparkles' },
  { type: 'videoGen', labelKey: 'creative.nodes.typeVideoGen', icon: 'play' },
  { type: 'frameExtract', labelKey: 'creative.nodes.typeFrameExtract', icon: 'cpu' },
]

const textModes = [
  { value: 'note', labelKey: 'creative.nodes.textModeNote' },
  { value: 'ai-expand', labelKey: 'creative.nodes.textModeExpand' },
  { value: 'ai-storyboard', labelKey: 'creative.nodes.textModeStoryboard' },
] as const

const gridBackgroundStyle = computed(() => {
  const spacing = Math.max(12, Math.round(24 * props.viewport.zoom))
  return {
    backgroundImage: 'radial-gradient(circle, rgba(148, 163, 184, 0.35) 1px, transparent 1px)',
    backgroundSize: `${spacing}px ${spacing}px`,
    backgroundPosition: `${props.viewport.x}px ${props.viewport.y}px`,
  }
})

const renderedEdges = computed(() => {
  const nodeMap = new Map(props.nodes.map((n) => [n.id, n]))
  return props.edges
    .map((edge) => {
      const source = nodeMap.get(edge.source)
      const target = nodeMap.get(edge.target)
      if (!source || !target) return null
      const start = getPortAnchor(source, 'output', edge.sourcePort)
      const end = getPortAnchor(target, 'input', edge.targetPort)
      return {
        ...edge,
        geometry: buildBezierGeometry(start.x, start.y, end.x, end.y),
      }
    })
    .filter((item): item is NonNullable<typeof item> => item !== null)
})

const selectedEdgeInfo = computed(() =>
  renderedEdges.value.find((e) => e.id === selectedEdgeId.value) ?? null,
)

const pendingLinkPath = computed(() => {
  if (!pendingLink.value) return ''
  const source = props.nodes.find((n) => n.id === pendingLink.value!.sourceId)
  if (!source) return ''
  const start = getPortAnchor(source, 'output', pendingLink.value.sourcePort)
  return buildBezierGeometry(start.x, start.y, pendingLink.value.cursorX, pendingLink.value.cursorY).path
})

const minimapNodes = computed(() => {
  if (props.nodes.length === 0) return []
  const minX = Math.min(...props.nodes.map((n) => n.x)) - 40
  const minY = Math.min(...props.nodes.map((n) => n.y)) - 40
  const maxX = Math.max(...props.nodes.map((n) => n.x + (n.width || 300))) + 40
  const maxY = Math.max(...props.nodes.map((n) => n.y + 220)) + 40
  const spanX = Math.max(1, maxX - minX)
  const spanY = Math.max(1, maxY - minY)
  const scale = Math.min(136 / spanX, 80 / spanY)

  const fills: Record<CanvasNodeType, string> = {
    text: '#3b82f6',
    image: '#10b981',
    imageGen: '#0ea5e9',
    videoGen: '#8b5cf6',
    frameExtract: '#f59e0b',
  }

  return props.nodes.map((node) => ({
    id: node.id,
    x: 4 + (node.x - minX) * scale,
    y: 4 + (node.y - minY) * scale,
    w: Math.max(8, (node.width || 300) * scale),
    h: Math.max(6, 160 * scale),
    fill: fills[node.type],
  }))
})

function clientToCanvas(clientX: number, clientY: number): { x: number; y: number } {
  const rect = stageRef.value?.getBoundingClientRect()
  if (!rect) return { x: 80, y: 80 }
  return {
    x: (clientX - rect.left - props.viewport.x) / props.viewport.zoom,
    y: (clientY - rect.top - props.viewport.y) / props.viewport.zoom,
  }
}

function addNodeAtCenter(type: CanvasNodeType): void {
  const rect = stageRef.value?.getBoundingClientRect()
  if (!rect) {
    emit('addNode', type, 80, 80)
    return
  }
  const center = clientToCanvas(rect.left + rect.width * 0.4, rect.top + rect.height * 0.35)
  const jitter = (props.nodes.length % 5) * 24
  emit('addNode', type, center.x + jitter, center.y + jitter)
}

function onStagePointerDown(event: PointerEvent): void {
  selectedEdgeId.value = null
  quickMenu.value = null
  if (pendingLink.value) {
    pendingLink.value = null
    return
  }
  if (event.button === 1 || handMode.value || event.target === stageRef.value) {
    isPanning.value = true
    panOrigin = {
      clientX: event.clientX,
      clientY: event.clientY,
      vpX: props.viewport.x,
      vpY: props.viewport.y,
    }
  }
}

function onStageDoubleClick(event: MouseEvent): void {
  if (event.target !== stageRef.value) return
  const rect = stageRef.value?.getBoundingClientRect()
  if (!rect) return
  const pt = clientToCanvas(event.clientX, event.clientY)
  quickMenu.value = {
    screenX: Math.max(8, Math.min(rect.width - 188, event.clientX - rect.left)),
    screenY: Math.max(8, Math.min(rect.height - 220, event.clientY - rect.top)),
    canvasX: pt.x,
    canvasY: pt.y,
  }
}

function pickQuickMenuNode(type: CanvasNodeType): void {
  if (!quickMenu.value) return
  emit('addNode', type, quickMenu.value.canvasX, quickMenu.value.canvasY)
  quickMenu.value = null
}

function onStagePointerMove(event: PointerEvent): void {
  if (isPanning.value) {
    const dx = event.clientX - panOrigin.clientX
    const dy = event.clientY - panOrigin.clientY
    emit('setViewport', { x: Math.round(panOrigin.vpX + dx), y: Math.round(panOrigin.vpY + dy) })
    return
  }
  if (draggingNode) {
    const dx = (event.clientX - draggingNode.startClientX) / props.viewport.zoom
    const dy = (event.clientY - draggingNode.startClientY) / props.viewport.zoom
    emit('moveNode', draggingNode.id, draggingNode.startX + dx, draggingNode.startY + dy, false)
    return
  }
  if (pendingLink.value) {
    const pt = clientToCanvas(event.clientX, event.clientY)
    pendingLink.value = { ...pendingLink.value, cursorX: pt.x, cursorY: pt.y }
  }
}

function onStagePointerUp(): void {
  if (draggingNode) {
    const node = props.nodes.find((n) => n.id === draggingNode!.id)
    if (node) emit('moveNode', node.id, node.x, node.y, true)
    draggingNode = null
  }
  isPanning.value = false
}

function onStageWheel(event: WheelEvent): void {
  if (event.ctrlKey || event.metaKey) {
    const delta = event.deltaY < 0 ? 0.08 : -0.08
    adjustZoom(delta)
  } else {
    emit('setViewport', {
      x: Math.round(props.viewport.x - event.deltaX),
      y: Math.round(props.viewport.y - event.deltaY),
    })
  }
}

function startNodeDrag(node: CanvasNode, event: PointerEvent): void {
  selectedNodeId.value = node.id
  draggingNode = {
    id: node.id,
    startClientX: event.clientX,
    startClientY: event.clientY,
    startX: node.x,
    startY: node.y,
  }
}

function startConnection(sourceId: string, sourcePort: PortKind, event: PointerEvent): void {
  const pt = clientToCanvas(event.clientX, event.clientY)
  pendingLink.value = { sourceId, sourcePort, cursorX: pt.x, cursorY: pt.y }
}

function toggleClickConnection(sourceId: string, sourcePort: PortKind): void {
  if (pendingLink.value && pendingLink.value.sourceId === sourceId && pendingLink.value.sourcePort === sourcePort) {
    return
  }
  const source = props.nodes.find((n) => n.id === sourceId)
  if (!source) return
  const anchor = getPortAnchor(source, 'output', sourcePort)
  pendingLink.value = { sourceId, sourcePort, cursorX: anchor.x + 80, cursorY: anchor.y }
}

function completeConnection(targetId: string, targetPort: PortKind): void {
  if (!pendingLink.value) return
  emit('connect', pendingLink.value.sourceId, pendingLink.value.sourcePort, targetId, targetPort)
  pendingLink.value = null
}

function disconnectSelectedEdge(): void {
  if (!selectedEdgeId.value) return
  emit('removeEdge', selectedEdgeId.value)
  selectedEdgeId.value = null
}

function adjustZoom(delta: number): void {
  const nextZoom = Number(Math.max(0.25, Math.min(2.0, props.viewport.zoom + delta)).toFixed(2))
  emit('setViewport', { zoom: nextZoom })
}

function fitView(): void {
  const rect = stageRef.value?.getBoundingClientRect()
  if (!rect || props.nodes.length === 0) {
    emit('setViewport', { x: 0, y: 0, zoom: 0.9 })
    return
  }
  const minX = Math.min(...props.nodes.map((n) => n.x))
  const minY = Math.min(...props.nodes.map((n) => n.y))
  const maxX = Math.max(...props.nodes.map((n) => n.x + (n.width || 300)))
  const maxY = Math.max(...props.nodes.map((n) => n.y + 240))
  const pad = 48
  const zoom = Math.max(
    0.35,
    Math.min(1.1, (rect.width - pad * 2) / Math.max(1, maxX - minX), (rect.height - pad * 2) / Math.max(1, maxY - minY)),
  )
  emit('setViewport', {
    x: Math.round(pad - minX * zoom),
    y: Math.round(pad - minY * zoom),
    zoom: Number(zoom.toFixed(2)),
  })
}

function onStageDrop(event: DragEvent): void {
  fileDropActive.value = false
  const files = Array.from(event.dataTransfer?.files ?? [])
  if (!files.length) return
  const pt = clientToCanvas(event.clientX, event.clientY)
  emit('dropFiles', files, pt.x, pt.y)
}

async function onPickNodeImage(nodeId: string, event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const prepared = await optimizeLocalImageDataUrl(file)
  emit('updateData', nodeId, {
    src: prepared.dataUrl,
    name: file.name,
    width: prepared.width,
    height: prepared.height,
    mimeType: prepared.mimeType,
  })
}

function onPickNodeVideo(nodeId: string, event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const url = URL.createObjectURL(file)
  emit('updateData', nodeId, {
    sourceVideoUrl: url,
    sourceName: file.name,
  })
}

function upstreamSummary(nodeId: string): string {
  const incoming = props.edges.filter((e) => e.target === nodeId)
  if (!incoming.length) return ''
  const textCount = incoming.filter((e) => e.targetPort === 'text').length
  const imgCount = incoming.filter((e) => e.targetPort === 'image').length
  const parts: string[] = []
  if (textCount) parts.push(t('creative.canvas.upstreamTexts', { count: textCount }))
  if (imgCount) parts.push(t('creative.canvas.upstreamImages', { count: imgCount }))
  return parts.join(' · ')
}

function matchesSearch(node: CanvasNode): boolean {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return true
  if (node.title.toLowerCase().includes(q)) return true
  if (node.type === 'text' && node.data.text.toLowerCase().includes(q)) return true
  if (node.type === 'imageGen' && node.data.prompt.toLowerCase().includes(q)) return true
  if (node.type === 'videoGen' && node.data.prompt.toLowerCase().includes(q)) return true
  return false
}

function portLabel(port: PortKind): string {
  if (port === 'text') return t('creative.ports.text')
  if (port === 'image') return t('creative.ports.image')
  return t('creative.ports.video')
}

function edgeColor(port: PortKind): string {
  if (port === 'text') return '#3b82f6'
  if (port === 'image') return '#10b981'
  return '#8b5cf6'
}

function portRingClass(port: PortKind): string {
  if (port === 'text') return 'border-blue-500'
  if (port === 'image') return 'border-emerald-500'
  return 'border-violet-500'
}

function portDotClass(port: PortKind): string {
  if (port === 'text') return 'bg-blue-500'
  if (port === 'image') return 'bg-emerald-500'
  return 'bg-violet-500'
}

function nodeTypeIcon(type: CanvasNodeType): 'edit' | 'upload' | 'sparkles' | 'play' | 'cpu' {
  const map: Record<CanvasNodeType, 'edit' | 'upload' | 'sparkles' | 'play' | 'cpu'> = {
    text: 'edit',
    image: 'upload',
    imageGen: 'sparkles',
    videoGen: 'play',
    frameExtract: 'cpu',
  }
  return map[type]
}

function nodeHeaderAccentClass(type: CanvasNodeType): string {
  const map: Record<CanvasNodeType, string> = {
    text: 'bg-blue-50/70 dark:bg-blue-950/30',
    image: 'bg-emerald-50/70 dark:bg-emerald-950/30',
    imageGen: 'bg-sky-50/70 dark:bg-sky-950/30',
    videoGen: 'bg-violet-50/70 dark:bg-violet-950/30',
    frameExtract: 'bg-amber-50/70 dark:bg-amber-950/30',
  }
  return map[type]
}

function statusBadgeClass(status: NodeRunStatus): string {
  if (status === 'running') return 'badge-primary'
  if (status === 'success') return 'badge-success'
  if (status === 'error') return 'badge-danger'
  return 'badge-gray'
}

function statusLabel(status: NodeRunStatus, elapsedMs?: number): string {
  if (status === 'running') return t('creative.status.running')
  if (status === 'error') return t('creative.status.error')
  if (status === 'success') {
    return elapsedMs ? `${(elapsedMs / 1000).toFixed(1)}s` : t('creative.status.done')
  }
  return ''
}

function downloadMedia(url: string, filename: string): void {
  if (!url) return
  downloadUrl(url, filename)
}

function isEditableElement(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable
}

function onWindowKeyDown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    pendingLink.value = null
    quickMenu.value = null
    selectedEdgeId.value = null
    return
  }
  if (isEditableElement(event.target)) return
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'z') {
    event.preventDefault()
    if (event.shiftKey) emit('redo')
    else emit('undo')
    return
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'y') {
    event.preventDefault()
    emit('redo')
    return
  }
  if (event.key === 'Delete' || event.key === 'Backspace') {
    if (selectedEdgeId.value) {
      event.preventDefault()
      disconnectSelectedEdge()
    } else if (selectedNodeId.value) {
      event.preventDefault()
      emit('removeNode', selectedNodeId.value)
      selectedNodeId.value = null
    }
  }
}

onMounted(() => {
  window.addEventListener('keydown', onWindowKeyDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onWindowKeyDown)
})
</script>

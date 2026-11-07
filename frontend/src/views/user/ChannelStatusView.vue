<template>
  <ChannelStatusV1View v-if="isV1" />
  <ChannelStatusV2View v-else-if="forceLegacyV2 || (resolved && !v3Enabled)" />
  <ChannelStatusV3View v-else-if="resolved" />
  <!-- Deliberately text-free: this shell resolves before any view mounts, and a
       translated label here would force the whole shell to depend on i18n. -->
  <div v-else class="flex min-h-[240px] items-center justify-center" role="status" aria-busy="true">
    <span class="h-6 w-6 animate-spin rounded-full border-2 border-gray-300 border-t-transparent dark:border-dark-600" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { isChannelMonitorV1Mode, isChannelMonitorPassiveMode } from '@/utils/featureFlags'
import { getV3Status } from '@/api/channelMonitorV3'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'
import ChannelStatusV3View from './ChannelStatusV3View.vue'

const isV1 = computed(() => isChannelMonitorV1Mode())
// Keep the plain V2 view reachable as a rollback/diagnostic surface without
// adding another backend flag: ?monitor_view=v2.
const forceLegacyV2 = computed(() =>
  isChannelMonitorPassiveMode() && new URLSearchParams(window.location.search).get('monitor_view') === 'v2')

// V3 owns its switch (ADR 0008). The board's endpoints are gated by
// channel_monitor_v3_config.enabled alone, so this shell must ask V3 — not the
// V1/V2 mode setting — which view to show. Asking here keeps a V2 deployment from
// rendering a V3 board that would only answer 403.
const resolved = ref(false)
const v3Enabled = ref(false)

onMounted(async () => {
  if (isV1.value) {
    resolved.value = true
    return
  }
  try {
    const status = await getV3Status()
    v3Enabled.value = Boolean(status?.enabled)
  } catch {
    // An unreachable status endpoint must not blank the page: fall back to the V2
    // view, which has its own error handling and mode gate.
    v3Enabled.value = false
  } finally {
    resolved.value = true
  }
})
</script>

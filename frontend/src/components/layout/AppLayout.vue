<template>
  <div :class="fullBleed ? 'h-screen overflow-hidden' : 'min-h-screen'">
    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative transition-[margin] duration-200 ease-out motion-reduce:transition-none"
      :class="[
        fullBleed ? 'flex h-screen flex-col overflow-hidden' : 'min-h-screen',
        sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64'
      ]"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main :class="fullBleed ? 'flex-1 min-h-0 overflow-hidden p-0' : 'p-4 md:p-6'">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

withDefaults(
  defineProps<{
    fullBleed?: boolean
  }>(),
  {
    fullBleed: false
  }
)

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

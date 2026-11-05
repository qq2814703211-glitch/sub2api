<template>
 <section class="flex flex-col gap-4 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
  <div class="min-w-0"><h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{title}}</h1><p v-if="description" class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{description}}</p></div>
  <div class="flex flex-wrap items-center gap-2"><slot name="actions"/></div>
 </section>
</template>
<script setup lang="ts">
defineProps<{
    title: string;
    description?: string;
}>();
</script>

<template>
  <AppLayout>
    <TicketWorkspace :admin="true" />
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import TicketWorkspace from '@/components/tickets/TicketWorkspace.vue'
</script>

<template>
 <div class="relative h-64"><Line :data="data" :options="options" /></div>
</template>
<script setup lang="ts">
import { computed } from 'vue';
import { Line } from 'vue-chartjs';
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, type ChartOptions } from 'chart.js';
ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend);
const props = defineProps<{
    labels: string[];
    values: Array<number | null>;
    unit: string;
    label: string;
}>();
const data = computed(() => ({ labels: props.labels, datasets: [{ label: `${props.label} (${props.unit || '—'})`, data: props.values, borderColor: '#0284c7', backgroundColor: '#0284c7', pointRadius: 2, borderWidth: 2, spanGaps: false }] }));
const options = computed<ChartOptions<'line'>>(() => ({ responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false } }, scales: { x: { ticks: { maxTicksLimit: 8 } }, y: { title: { display: true, text: props.unit || '—' } } } }));
</script>

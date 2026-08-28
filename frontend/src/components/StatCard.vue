<template>
  <div class="card p-5">
    <div class="flex items-center justify-between gap-3">
      <div class="text-xs font-semibold uppercase tracking-wider text-text-muted">{{ label }}</div>
      <span class="stat-marker" :class="markerClass"></span>
    </div>
    <div class="mt-3 text-3xl font-bold tracking-tight" :class="valueClass">{{ value }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  label: String,
  value: [Number, String],
  color: String,
})

const valueClass = computed(() => ({
  success: 'text-success',
  danger: 'text-danger',
  accent: 'text-accent',
  warning: 'text-warning-dark',
}[props.color] || 'text-text'))

const markerClass = computed(() => ({
  success: 'bg-success',
  danger: 'bg-danger',
  accent: 'bg-accent',
  warning: 'bg-warning',
}[props.color] || 'bg-slate-300'))
</script>

<style scoped>
.stat-marker { width: 8px; height: 8px; border-radius: 50%; box-shadow: 0 0 0 4px rgba(148, 163, 184, 0.12); }
</style>

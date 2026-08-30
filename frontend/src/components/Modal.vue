<template>
  <div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:items-center" role="dialog" aria-modal="true">
    <div class="absolute inset-0 bg-slate-900/35 backdrop-blur-[2px]" @click="$emit('close')"></div>
    <div class="card relative z-10 my-auto max-h-[calc(100dvh-2rem)] w-full max-w-xl overflow-y-auto p-5 shadow-2xl fade-in sm:p-6">
      <slot />
    </div>
  </div>
</template>

<script setup>
defineEmits(['close'])
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true">
    <div class="absolute inset-0 bg-slate-900/35 backdrop-blur-[2px]" @click="$emit('close')"></div>
    <div class="card relative z-10 w-full max-w-xl p-6 shadow-2xl fade-in">
      <slot />
    </div>
  </div>
</template>

<script setup>
defineEmits(['close'])
</script>

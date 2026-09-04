<template>
  <div class="layout-toggle" role="group" aria-label="卡片布局">
    <button
      type="button"
      class="layout-toggle-button"
      :class="ui.cardLayout === 'vertical' ? 'layout-toggle-active' : ''"
      :aria-pressed="ui.cardLayout === 'vertical'"
      title="竖向卡片"
      @click="ui.setCardLayout('vertical')"
    >
      <svg aria-hidden="true" viewBox="0 0 16 16" fill="none">
        <rect x="1.5" y="1.5" width="5" height="5" rx="1" />
        <rect x="9.5" y="1.5" width="5" height="5" rx="1" />
        <rect x="1.5" y="9.5" width="5" height="5" rx="1" />
        <rect x="9.5" y="9.5" width="5" height="5" rx="1" />
      </svg>
      <span>竖向</span>
    </button>
    <button
      type="button"
      class="layout-toggle-button"
      :class="ui.cardLayout === 'horizontal' ? 'layout-toggle-active' : ''"
      :aria-pressed="ui.cardLayout === 'horizontal'"
      title="横条卡片"
      @click="ui.setCardLayout('horizontal')"
    >
      <svg aria-hidden="true" viewBox="0 0 16 16" fill="none">
        <rect x="1.5" y="2" width="13" height="3" rx="1" />
        <rect x="1.5" y="6.5" width="13" height="3" rx="1" />
        <rect x="1.5" y="11" width="13" height="3" rx="1" />
      </svg>
      <span>横条</span>
    </button>
  </div>
</template>

<script setup>
import { useUIStore } from '../stores/ui'

const ui = useUIStore()
</script>

<style scoped>
.layout-toggle { display: inline-flex; align-items: center; gap: 2px; border: 1px solid #dce4ee; border-radius: 9px; background: #f8fafc; padding: 3px; }
.layout-toggle-button { display: inline-flex; align-items: center; gap: 5px; min-height: 28px; border: 0; border-radius: 6px; background: transparent; padding: 0 8px; color: #64748b; cursor: pointer; font-size: 10px; font-weight: 700; transition: color .16s ease, background .16s ease, box-shadow .16s ease; }
.layout-toggle-button:hover { color: #1d4ed8; }
.layout-toggle-active { background: #fff; color: #1d4ed8; box-shadow: 0 1px 3px rgba(15, 23, 42, .1); }
svg { width: 13px; height: 13px; stroke: currentColor; stroke-width: 1.35; }
@media (max-width: 520px) { .layout-toggle-button { padding-inline: 6px; }.layout-toggle-button span { display: none; } }
</style>

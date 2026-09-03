<template>
  <svg class="nav-svg-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path v-for="(path, index) in iconPaths" :key="index" :d="path" />
  </svg>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({ name: { type: String, default: 'dashboard' } })
const paths = {
  dashboard: ['M4 4h6v6H4z', 'M14 4h6v6h-6z', 'M4 14h6v6H4z', 'M14 14h6v6h-6z'],
  workspace: ['M4 4h16v16H4z', 'M8 8h8v8H8z'],
  cloud: ['M7.5 18.5h9a4 4 0 0 0 .6-7.95A6 6 0 0 0 5.4 12.2 3.2 3.2 0 0 0 7.5 18.5z'],
  users: ['M16 20v-1.5a3.5 3.5 0 0 0-3.5-3.5h-5A3.5 3.5 0 0 0 4 18.5V20', 'M10 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z', 'M16 4.2a3.5 3.5 0 0 1 0 6.6', 'M18 15.2a3.5 3.5 0 0 1 2 3.3V20'],
  pool: ['M7 7a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z', 'M17 22a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z', 'M17 7a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z', 'M7 17v-2a3 3 0 0 1 3-3h4a3 3 0 0 0 3-3V7', 'M7 7v5'],
  service: ['M4 12h15', 'M13 6l6 6-6 6'],
  relay: ['M4 5h16v14H4z', 'M8 9h.01', 'M11 9h5', 'M8 13h.01', 'M11 13h5', 'M8 17h8'],
  landing: ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z', 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M12 3v3', 'M12 18v3', 'M3 12h3', 'M18 12h3'],
  dns: ['M4 7h16', 'M4 12h16', 'M4 17h16', 'M8 4v16', 'M16 4v16'],
  logs: ['M5 6h14', 'M5 12h14', 'M5 18h9'],
  settings: ['M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z', 'M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-1.8 1.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-2.5V20a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1-1.8-1.8.1-.1A1.7 1.7 0 0 0 8 15a1.7 1.7 0 0 0-1.6-1H6v-2.5h.2a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9l-.1-.1 1.8-1.8.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.6V5h2.5v.2a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1 1.8 1.8-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2V14h-.2a1.7 1.7 0 0 0-1.6 1z']
}
const iconPaths = computed(() => paths[props.name] || paths.dashboard)
</script>

<style scoped>
.nav-svg-icon{width:14px;height:14px}
</style>

<template>
  <div class="notification-center" aria-live="polite" aria-atomic="true">
    <transition-group name="notification">
      <div v-for="item in items" :key="item.id" class="notification-item" :class="`notification-${item.type}`">
        <span class="notification-mark">{{ item.type === 'error' ? '!' : '✓' }}</span>
        <p>{{ item.text }}</p>
        <button type="button" aria-label="关闭提示" @click="remove(item.id)">×</button>
      </div>
    </transition-group>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'

const items = ref([])
let nextID = 1
const timers = new Map()

function remove(id) {
  items.value = items.value.filter(item => item.id !== id)
  window.clearTimeout(timers.get(id))
  timers.delete(id)
}

function handleNotification(event) {
  const detail = event.detail || {}
  if (!detail.text) return
  const id = nextID++
  items.value = [...items.value.slice(-2), {
    id,
    text: detail.text,
    type: detail.type === 'error' ? 'error' : 'success',
  }]
  timers.set(id, window.setTimeout(() => remove(id), Number(detail.duration) || 4500))
}

onMounted(() => window.addEventListener('alicdt:notify', handleNotification))
onUnmounted(() => {
  window.removeEventListener('alicdt:notify', handleNotification)
  timers.forEach(timer => window.clearTimeout(timer))
  timers.clear()
})
</script>

<style scoped>
.notification-center { position: fixed; top: 76px; right: 18px; z-index: 80; display: grid; width: min(390px, calc(100vw - 32px)); gap: 8px; pointer-events: none; }
.notification-item { display: flex; align-items: flex-start; gap: 9px; border: 1px solid #bbf7d0; border-radius: 10px; background: rgba(240, 253, 244, .97); padding: 11px 12px; color: #166534; box-shadow: 0 12px 32px rgba(15, 23, 42, .14); backdrop-filter: blur(12px); pointer-events: auto; }
.notification-error { border-color: #fecaca; background: rgba(254, 242, 242, .97); color: #b91c1c; }
.notification-mark { display: grid; width: 18px; height: 18px; flex: 0 0 auto; place-items: center; border-radius: 50%; background: currentColor; color: #fff; font-size: 9px; font-weight: 900; }
.notification-item p { min-width: 0; flex: 1; font-size: 11px; line-height: 1.55; word-break: break-word; }
.notification-item button { margin: -3px -4px 0 0; border: 0; background: transparent; color: currentColor; cursor: pointer; font-size: 18px; line-height: 1; opacity: .65; }
.notification-enter-active, .notification-leave-active { transition: opacity .18s ease, transform .18s ease; }
.notification-enter-from, .notification-leave-to { opacity: 0; transform: translateX(12px); }
@media (max-width: 639px) { .notification-center { top: 66px; right: 16px; } }
</style>

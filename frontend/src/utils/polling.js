import { onMounted, onUnmounted } from 'vue'

export function usePolling(task, intervalMs) {
  let timer
  let active = false
  let running = false

  function clearTimer() {
    if (timer) window.clearTimeout(timer)
    timer = undefined
  }

  function schedule(delay = intervalMs) {
    clearTimer()
    if (!active) return
    timer = window.setTimeout(run, Math.max(0, delay))
  }

  async function run() {
    timer = undefined
    if (!active) return
    if (running || document.hidden || !navigator.onLine) {
      schedule()
      return
    }
    running = true
    try {
      await task()
    } catch (_) {
      // API interceptors provide user-visible errors. A failed poll should
      // not stop later retries after a transient network interruption.
    } finally {
      running = false
      schedule()
    }
  }

  function resume() {
    if (active && !document.hidden && navigator.onLine && !running) schedule(0)
  }

  function start() {
    if (active) return
    active = true
    schedule()
  }

  function stop() {
    active = false
    clearTimer()
  }

  onMounted(() => {
    start()
    document.addEventListener('visibilitychange', resume)
    window.addEventListener('online', resume)
  })
  onUnmounted(() => {
    stop()
    document.removeEventListener('visibilitychange', resume)
    window.removeEventListener('online', resume)
  })

  return { runNow: run, start, stop }
}

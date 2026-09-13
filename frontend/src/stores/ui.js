import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

const STORAGE_KEY = 'alicdt-card-layout'
const LAYOUTS = new Set(['vertical', 'horizontal'])

function readLayout() {
  if (typeof window === 'undefined') return 'vertical'
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY)
    return LAYOUTS.has(saved) ? saved : 'vertical'
  } catch (_) {
    return 'vertical'
  }
}

export const useUIStore = defineStore('ui', () => {
  const cardLayout = ref(readLayout())
  const isHorizontal = computed(() => cardLayout.value === 'horizontal')

  function setCardLayout(layout) {
    if (!LAYOUTS.has(layout)) return
    cardLayout.value = layout
    try {
      if (typeof window !== 'undefined') window.localStorage.setItem(STORAGE_KEY, layout)
    } catch (_) {
      // A blocked/readonly localStorage should not prevent the layout switch.
    }
  }

  return { cardLayout, isHorizontal, setCardLayout }
})

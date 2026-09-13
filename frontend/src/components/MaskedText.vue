<template>
  <span class="masked-text" :class="{ 'masked-text-empty': !hasValue, 'masked-text-revealed': revealed }">
    <span class="masked-text-value">{{ displayValue }}</span>
    <button
      v-if="hasSensitiveIP"
      type="button"
      class="masked-text-toggle"
      :aria-pressed="revealed"
      :aria-label="revealed ? '隐藏文本中的完整 IP' : '显示文本中的完整 IP'"
      :title="revealed ? '隐藏文本中的完整 IP' : '显示文本中的完整 IP'"
      @click.stop.prevent="revealed = !revealed"
    >
      <svg v-if="revealed" aria-hidden="true" viewBox="0 0 24 24" fill="none"><path d="M3 3l18 18M10.58 10.58a2 2 0 0 0 2.83 2.83M9.88 4.24A10.7 10.7 0 0 1 12 4c5.25 0 9.44 3.2 11 8a11.8 11.8 0 0 1-3.04 4.86M6.1 6.1A11.8 11.8 0 0 0 1 12c1.56 4.8 5.75 8 11 8a10.7 10.7 0 0 0 2.12-.21" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" /></svg>
      <svg v-else aria-hidden="true" viewBox="0 0 24 24" fill="none"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round" /><circle cx="12" cy="12" r="2.8" stroke="currentColor" stroke-width="1.7" /></svg>
    </button>
  </span>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { maskIPsInText } from '../utils/ip'

const props = defineProps({
  value: { type: String, default: '' },
  placeholder: { type: String, default: '未设置' },
})
const revealed = ref(false)
const rawValue = computed(() => String(props.value ?? ''))
const hasValue = computed(() => rawValue.value.length > 0)
const maskedValue = computed(() => maskIPsInText(rawValue.value))
const hasSensitiveIP = computed(() => hasValue.value && maskedValue.value !== rawValue.value)
const displayValue = computed(() => {
  if (!hasValue.value) return props.placeholder
  return hasSensitiveIP.value && !revealed.value ? maskedValue.value : rawValue.value
})
watch(rawValue, () => { revealed.value = false })
</script>

<style scoped>
.masked-text { display: inline-flex; min-width: 0; max-width: 100%; align-items: flex-start; gap: 6px; vertical-align: top; }
.masked-text-value { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: pre-wrap; overflow-wrap: anywhere; }
.masked-text-empty .masked-text-value { color: #94a3b8; }
.masked-text-toggle { display: inline-flex; width: 23px; height: 23px; flex: 0 0 auto; align-items: center; justify-content: center; border: 1px solid #dbe4ef; border-radius: 6px; background: #fff; color: #64748b; cursor: pointer; transition: color .15s ease, background .15s ease, border-color .15s ease; }
.masked-text-toggle:hover { border-color: #93c5fd; background: #eff6ff; color: #1d4ed8; }
.masked-text-toggle:focus-visible { outline: 3px solid rgba(37, 99, 235, .2); outline-offset: 1px; }
.masked-text-toggle svg { width: 13px; height: 13px; }
.masked-text-revealed .masked-text-value { color: #1e3a8a; }
</style>

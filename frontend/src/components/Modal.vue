<template>
  <div class="modal-layer" role="dialog" aria-modal="true" tabindex="-1" @keydown.esc="$emit('close')">
    <div class="modal-backdrop" @click="$emit('close')"></div>
    <section class="modal-dialog fade-in" :class="`modal-dialog-${props.size}`" @click.stop>
      <button v-if="props.showClose" type="button" class="modal-close" aria-label="关闭" title="关闭" @click="$emit('close')">×</button>
      <div class="modal-scroll"><slot /></div>
    </section>
  </div>
</template>

<script setup>
const props = defineProps({
  size: { type: String, default: 'wide' },
  showClose: { type: Boolean, default: true },
})
defineEmits(['close'])
</script>

<style scoped>
.modal-layer { position: fixed; inset: 0; z-index: 50; display: flex; align-items: center; justify-content: center; overflow-y: auto; padding: clamp(10px, 3vh, 30px) clamp(10px, 3vw, 30px); }
.modal-backdrop { position: absolute; inset: 0; background: rgba(15, 23, 42, .46); backdrop-filter: blur(3px); }
.modal-dialog { position: relative; z-index: 1; display: flex; width: min(100%, 920px); max-height: calc(100dvh - clamp(20px, 6vh, 60px)); flex-direction: column; overflow: hidden; border: 1px solid #dbe4ef; border-radius: 18px; background: #fff; box-shadow: 0 24px 70px rgba(15, 23, 42, .2); }
.modal-dialog-compact { width: min(100%, 540px); }
.modal-dialog-wide { width: min(100%, 920px); }
.modal-dialog-large { width: min(100%, 1080px); }
.modal-scroll { min-width: 0; min-height: 0; overflow-y: auto; overflow-wrap: anywhere; overscroll-behavior: contain; scrollbar-gutter: stable; padding: clamp(20px, 3vw, 32px); }
.modal-close { position: absolute; top: 13px; right: 15px; z-index: 2; display: inline-flex; width: 30px; height: 30px; align-items: center; justify-content: center; border: 1px solid transparent; border-radius: 8px; background: transparent; color: #94a3b8; cursor: pointer; font-size: 24px; font-weight: 300; line-height: 1; transition: color .15s ease, background .15s ease, border-color .15s ease; }
.modal-close:hover { border-color: #dbe4ef; background: #f8fafc; color: #334155; }
.modal-close:focus-visible { outline: 3px solid rgba(37, 99, 235, .2); outline-offset: 1px; }
@media (max-width: 639px) {
  .modal-layer { align-items: flex-start; padding: 8px; }
  .modal-dialog { max-height: calc(100dvh - 16px); border-radius: 14px; }
  .modal-scroll { padding: 22px 16px 18px; }
}
</style>

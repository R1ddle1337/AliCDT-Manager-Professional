<template>
  <div class="space-y-6 fade-in">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <div class="eyebrow">OVERVIEW</div>
        <h1 class="page-title">资源总览</h1>
        <p class="page-subtitle">{{ now }} · 数据每 2 分钟自动更新</p>
      </div>
      <button type="button" @click="sync" :disabled="store.loading" class="btn-primary">
        <span v-if="store.loading" class="mr-2 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/35 border-t-white"></span>
        {{ store.loading ? '同步中...' : '立即同步' }}
      </button>
    </div>

    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <StatCard label="实例总数" :value="instances.length" />
      <StatCard label="运行中" :value="runningCount" color="success" />
      <StatCard label="已停机" :value="stoppedCount" color="danger" />
      <StatCard label="自动保活" :value="keepAliveCount" color="accent" />
    </div>

    <section class="card p-5 sm:p-6">
      <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="section-title">实例状态</h2><p class="section-caption">拖动卡片可调整展示顺序</p></div>
        <span class="count-badge">{{ instances.length }} 个实例</span>
      </div>

      <div v-if="sortedInstances.length === 0" class="empty-state border border-dashed border-slate-200 rounded-xl py-12">
        <div class="empty-mark">VM</div>
        <div class="mt-4 font-semibold text-slate-800">暂无实例数据</div>
        <p class="mt-1 text-sm text-text-muted">请先添加账户，然后等待后台同步完成。</p>
      </div>

      <div v-else class="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <div v-for="inst in sortedInstances" :key="inst.instance_id" draggable="true"
          @dragstart="onDragStart($event, inst.instance_id)" @dragover.prevent="onDragOver($event, inst.instance_id)"
          @dragend="onDragEnd" @drop.prevent="onDrop($event, inst.instance_id)"
          :class="['transition-all duration-200', dragOverId === inst.instance_id && draggingId !== inst.instance_id ? 'scale-[1.01] opacity-80' : '', draggingId === inst.instance_id ? 'opacity-40 scale-[.98]' : '']">
          <InstanceCard :instance="inst" :account="accountMap[inst.account_id]"
            @start="store.controlInstance(inst.instance_id, 'start')"
            @stop="store.controlInstance(inst.instance_id, 'stop')"
            @release="confirmRelease(inst)" />
        </div>
      </div>
    </section>

    <Modal v-if="releaseTarget" @close="releaseTarget = null">
      <div class="space-y-5">
        <div><div class="eyebrow text-danger">RELEASE INSTANCE</div><h2 class="mt-1 text-lg font-bold text-slate-900">确认释放实例？</h2><p class="mt-2 font-mono text-sm text-slate-500">{{ releaseTarget.instance_id }}</p></div>
        <div class="notice notice-error">此操作不可撤销，实例及其云端资源将被永久删除。</div>
        <div class="flex gap-3"><button type="button" @click="releaseTarget = null" class="btn-ghost flex-1 border border-slate-200">取消</button><button type="button" @click="doRelease" class="btn-danger flex-1">确认释放</button></div>
      </div>
    </Modal>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useStore } from '../stores'
import StatCard from '../components/StatCard.vue'
import InstanceCard from '../components/InstanceCard.vue'
import Modal from '../components/Modal.vue'

const store = useStore()
const releaseTarget = ref(null)
const now = ref('')
const draggingId = ref(null)
const dragOverId = ref(null)
const SORT_KEY = 'instance_sort_order'
const customOrder = ref(JSON.parse(localStorage.getItem(SORT_KEY) || '[]'))

const instances = computed(() => store.instances)
const runningCount = computed(() => instances.value.filter(i => i.status === 'Running').length)
const stoppedCount = computed(() => instances.value.filter(i => i.status === 'Stopped').length)
const keepAliveCount = computed(() => store.accounts.filter(a => a.keep_alive).length)
const accountMap = computed(() => Object.fromEntries(store.accounts.map(a => [a.id, a])))
const sortedInstances = computed(() => {
  const arr = [...instances.value]
  if (!customOrder.value.length) return arr
  return arr.sort((a, b) => {
    const ia = customOrder.value.indexOf(a.instance_id); const ib = customOrder.value.indexOf(b.instance_id)
    if (ia === -1 && ib === -1) return 0
    if (ia === -1) return 1
    if (ib === -1) return -1
    return ia - ib
  })
})

function onDragStart(e, id) { draggingId.value = id; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', id) }
function onDragOver(e, id) { dragOverId.value = id }
function onDragEnd() { draggingId.value = null; dragOverId.value = null }
function onDrop(e, targetId) {
  const sourceId = draggingId.value
  if (!sourceId || sourceId === targetId) return
  const order = sortedInstances.value.map(i => i.instance_id)
  const fromIdx = order.indexOf(sourceId); const toIdx = order.indexOf(targetId)
  if (fromIdx < 0 || toIdx < 0) return
  order.splice(fromIdx, 1); order.splice(toIdx, 0, sourceId)
  customOrder.value = order
  localStorage.setItem(SORT_KEY, JSON.stringify(order))
}

function updateTime() { now.value = new Date().toLocaleString('zh-CN', { hour12: false }) }
let timer
onMounted(async () => { await store.fetchAccounts(); await store.fetchInstances(); updateTime(); timer = setInterval(updateTime, 1000) })
onUnmounted(() => clearInterval(timer))
async function sync() { await store.syncAll() }
function confirmRelease(inst) { releaseTarget.value = inst }
async function doRelease() {
  const id = releaseTarget.value?.instance_id
  if (!id) return
  await store.releaseInstance(id)
  releaseTarget.value = null
  customOrder.value = customOrder.value.filter(item => item !== id)
  localStorage.setItem(SORT_KEY, JSON.stringify(customOrder.value))
}
</script>

<style scoped>
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 6px; color: #172033; font-size: 26px; font-weight: 750; letter-spacing: -.03em; }
.page-subtitle { margin-top: 6px; color: #64748b; font-size: 13px; }
.section-title { color: #1e293b; font-size: 15px; font-weight: 700; }
.section-caption { margin-top: 3px; color: #94a3b8; font-size: 11px; }
.count-badge { border-radius: 999px; background: #f1f5f9; padding: 5px 9px; color: #64748b; font-size: 11px; font-weight: 650; }
.empty-state { text-align: center; }
.empty-mark { display: inline-flex; align-items: center; justify-content: center; width: 46px; height: 46px; margin: 0 auto; border-radius: 11px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 800; letter-spacing: .08em; }
</style>

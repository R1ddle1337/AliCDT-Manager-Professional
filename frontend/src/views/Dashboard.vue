<template>
  <div class="space-y-5 fade-in">
    <header class="dashboard-header">
      <div>
        <div class="eyebrow">CONTROL CENTER</div>
        <div class="mt-1 flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h1 class="page-title">资源总览</h1>
          <span class="live-status"><span class="status-dot status-dot-success"></span>服务正常</span>
        </div>
        <p class="page-subtitle">{{ now }} · 自动同步周期 2 分钟</p>
      </div>
      <div class="flex items-center gap-3">
        <span class="hidden text-xs text-slate-400 sm:inline">{{ lastSyncLabel }}</span>
        <button type="button" @click="sync" :disabled="store.loading" class="btn-primary">
          <span v-if="store.loading" class="mr-2 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/35 border-t-white"></span>
          {{ store.loading ? '同步中...' : '立即同步' }}
        </button>
      </div>
    </header>

    <section class="grid grid-cols-2 gap-3 xl:grid-cols-4 summary-fixed">
      <StatCard label="实例总数" :value="instances.length" />
      <StatCard label="运行中" :value="runningCount" color="success" />
      <StatCard label="已停机" :value="stoppedCount" color="danger" />
      <StatCard label="自动保活" :value="keepAliveCount" color="accent" />
    </section>

    <section class="dashboard-grid">
      <div class="card workspace-card min-w-0 p-5 sm:p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div><h2 class="section-title">实例工作区</h2><p class="section-caption">拖动卡片可调整展示顺序</p></div>
          <span class="count-badge">{{ instances.length }} 个实例</span>
        </div>

        <div v-if="sortedInstances.length === 0" class="empty-state rounded-xl border border-dashed border-slate-200 py-12">
          <div class="empty-mark">VM</div><div class="mt-4 font-semibold text-slate-800">暂无实例数据</div><p class="mt-1 text-sm text-text-muted">请先添加账户，然后等待后台同步完成。</p>
        </div>
        <div v-else class="instance-grid layout-collection layout-collection--strip">
          <div v-for="inst in sortedInstances" :key="inst.instance_id" draggable="true"
            @dragstart="onDragStart($event, inst.instance_id)" @dragover.prevent="onDragOver($event, inst.instance_id)"
            @dragend="onDragEnd" @drop.prevent="onDrop($event, inst.instance_id)"
            :class="['layout-card transition-all duration-200', dragOverId === inst.instance_id && draggingId !== inst.instance_id ? 'scale-[1.01] opacity-80' : '', draggingId === inst.instance_id ? 'opacity-40 scale-[.98]' : '']">
            <InstanceCard :instance="inst" :account="accountMap[inst.account_id]" @start="store.controlInstance(inst.instance_id, 'start')" @stop="store.controlInstance(inst.instance_id, 'stop')" @release="confirmRelease(inst)" />
          </div>
        </div>
      </div>

      <aside class="space-y-4">
        <div class="card p-5">
          <div class="flex items-start justify-between"><div><h2 class="section-title">资源利用率</h2><p class="section-caption">当前已同步实例的流量使用情况</p></div><span class="summary-code">TRAFFIC</span></div>
          <div class="mt-6 flex items-end justify-between gap-3"><div><span class="summary-value">{{ trafficPercent.toFixed(1) }}%</span><span class="summary-unit">已使用</span></div><span class="text-right text-xs text-slate-400">{{ trafficUsed.toFixed(2) }} GB<br />共 {{ trafficLimit.toFixed(0) }} GB</span></div>
          <div class="util-track mt-3"><div class="util-fill" :class="trafficPercent >= 90 ? 'util-danger' : trafficPercent >= 75 ? 'util-warning' : ''" :style="{ width: `${trafficPercent}%` }"></div></div>
          <div class="mt-2 flex justify-between text-[11px] text-slate-400"><span>0 GB</span><span>容量使用率</span><span>{{ trafficLimit.toFixed(0) }} GB</span></div>
        </div>

        <div class="card p-5">
          <div class="flex items-start justify-between"><div><h2 class="section-title">运行状态</h2><p class="section-caption">实例健康概览</p></div><span class="summary-code">STATUS</span></div>
          <div class="mt-5 space-y-3"><div class="status-line"><span class="status-dot status-dot-success"></span><span>运行中</span><strong>{{ runningCount }}</strong></div><div class="status-line"><span class="status-dot status-dot-muted"></span><span>已停机</span><strong>{{ stoppedCount }}</strong></div><div class="status-line"><span class="status-dot status-dot-warning"></span><span>状态未知</span><strong>{{ unknownCount }}</strong></div></div>
        </div>

        <div class="card p-5">
          <div class="flex items-start justify-between"><div><h2 class="section-title">账户概况</h2><p class="section-caption">已接入云账号</p></div><span class="summary-code">ACCOUNTS</span></div>
          <div class="mt-5 grid grid-cols-3 gap-2 text-center"><div class="account-stat"><strong>{{ store.accounts.length }}</strong><span>全部</span></div><div class="account-stat"><strong>{{ internationalCount }}</strong><span>国际站</span></div><div class="account-stat"><strong>{{ chinaCount }}</strong><span>中国站</span></div></div>
          <button type="button" class="btn-ghost mt-4 w-full border border-slate-200 text-xs" @click="$router.push('/accounts')">管理账户</button>
        </div>
      </aside>
    </section>

    <Modal v-if="releaseTarget" @close="releaseTarget = null"><div class="space-y-5"><div><div class="eyebrow text-danger">RELEASE INSTANCE</div><h2 class="mt-1 text-lg font-bold text-slate-900">确认释放实例？</h2><p class="mt-2 font-mono text-sm text-slate-500">{{ releaseTarget.instance_id }}</p></div><div class="notice notice-error">此操作不可撤销，实例及其云端资源将被永久删除。</div><div class="flex gap-3"><button type="button" @click="releaseTarget = null" class="btn-ghost flex-1 border border-slate-200">取消</button><button type="button" @click="doRelease" class="btn-danger flex-1">确认释放</button></div></div></Modal>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useStore } from '../stores'
import StatCard from '../components/StatCard.vue'
import InstanceCard from '../components/InstanceCard.vue'
import Modal from '../components/Modal.vue'

const store = useStore(); const releaseTarget = ref(null); const now = ref(''); const lastSyncAt = ref(''); const draggingId = ref(null); const dragOverId = ref(null)
const SORT_KEY = 'instance_sort_order'; const customOrder = ref(JSON.parse(localStorage.getItem(SORT_KEY) || '[]'))
const instances = computed(() => store.instances); const runningCount = computed(() => instances.value.filter(i => i.status === 'Running').length); const stoppedCount = computed(() => instances.value.filter(i => i.status === 'Stopped').length); const unknownCount = computed(() => instances.value.filter(i => !['Running', 'Stopped'].includes(i.status)).length); const keepAliveCount = computed(() => store.accounts.filter(a => a.keep_alive).length)
const internationalCount = computed(() => store.accounts.filter(a => a.site_type === 'international').length); const chinaCount = computed(() => store.accounts.filter(a => a.site_type === 'china').length); const accountMap = computed(() => Object.fromEntries(store.accounts.map(a => [a.id, a])))
const trafficUsed = computed(() => instances.value.reduce((sum, item) => sum + Number(item.traffic_used_gb || 0), 0)); const trafficLimit = computed(() => instances.value.reduce((sum, item) => sum + Number(accountMap.value[item.account_id]?.traffic_limit_gb || 200), 0) || 200); const trafficPercent = computed(() => Math.min(100, trafficLimit.value ? trafficUsed.value / trafficLimit.value * 100 : 0)); const lastSyncLabel = computed(() => lastSyncAt.value ? `最近同步 ${lastSyncAt.value}` : '等待同步')
const sortedInstances = computed(() => { const arr = [...instances.value]; if (!customOrder.value.length) return arr; return arr.sort((a, b) => { const ia = customOrder.value.indexOf(a.instance_id); const ib = customOrder.value.indexOf(b.instance_id); if (ia === -1 && ib === -1) return 0; if (ia === -1) return 1; if (ib === -1) return -1; return ia - ib }) })
function onDragStart(e, id) { draggingId.value = id; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', id) }; function onDragOver(e, id) { dragOverId.value = id }; function onDragEnd() { draggingId.value = null; dragOverId.value = null }
function onDrop(e, targetId) { const sourceId = draggingId.value; if (!sourceId || sourceId === targetId) return; const order = sortedInstances.value.map(i => i.instance_id); const fromIdx = order.indexOf(sourceId); const toIdx = order.indexOf(targetId); if (fromIdx < 0 || toIdx < 0) return; order.splice(fromIdx, 1); order.splice(toIdx, 0, sourceId); customOrder.value = order; localStorage.setItem(SORT_KEY, JSON.stringify(order)) }
function updateTime() { now.value = new Date().toLocaleString('zh-CN', { hour12: false }) }
let timer; onMounted(async () => { await store.fetchAccounts(); await store.fetchInstances(); updateTime(); lastSyncAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false }); timer = setInterval(updateTime, 1000) }); onUnmounted(() => clearInterval(timer))
async function sync() { await store.syncAll(); lastSyncAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false }) }; function confirmRelease(inst) { releaseTarget.value = inst }; async function doRelease() { const id = releaseTarget.value?.instance_id; if (!id) return; await store.releaseInstance(id); releaseTarget.value = null; customOrder.value = customOrder.value.filter(item => item !== id); localStorage.setItem(SORT_KEY, JSON.stringify(customOrder.value)) }
</script>

<style scoped>
.dashboard-header { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { color: #172033; font-size: 26px; font-weight: 750; letter-spacing: -.03em; }
.page-subtitle { margin-top: 5px; color: #64748b; font-size: 12px; }
.live-status { display: inline-flex; align-items: center; gap: 6px; color: #15803d; font-size: 11px; font-weight: 650; }
.section-title { color: #1e293b; font-size: 15px; font-weight: 700; }.section-caption { margin-top: 3px; color: #94a3b8; font-size: 11px; }.count-badge, .summary-code { border-radius: 999px; background: #f1f5f9; padding: 5px 9px; color: #64748b; font-size: 10px; font-weight: 700; letter-spacing: .05em; }.summary-code { background: #eff6ff; color: #2563eb; font-size: 9px; }
.dashboard-grid { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 18px; align-items: start; }.instance-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }.summary-value { color: #172033; font-size: 34px; font-weight: 750; letter-spacing: -.05em; }.summary-unit { margin-left: 6px; color: #94a3b8; font-size: 11px; }.util-track { height: 9px; overflow: hidden; border-radius: 999px; background: #e2e8f0; }.util-fill { height: 100%; border-radius: inherit; background: #2563eb; transition: width .5s ease; }.util-warning { background: #d97706; }.util-danger { background: #dc2626; }.status-line { display: flex; align-items: center; gap: 9px; color: #64748b; font-size: 12px; }.status-line strong { margin-left: auto; color: #334155; font-size: 14px; }.status-dot-warning { background: #d97706; }.account-stat { border-radius: 8px; background: #f8fafc; padding: 9px 4px; }.account-stat strong { display: block; color: #334155; font-size: 17px; }.account-stat span { color: #94a3b8; font-size: 10px; }
.empty-state { text-align: center; }.empty-mark { display: inline-flex; align-items: center; justify-content: center; width: 46px; height: 46px; margin: 0 auto; border-radius: 11px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 800; letter-spacing: .08em; }
@media (max-width: 1279px) { .dashboard-grid { grid-template-columns: minmax(0, 1fr) 270px; gap: 14px; }.instance-grid { grid-template-columns: 1fr; } }
@media (max-width: 1100px) { .dashboard-grid { grid-template-columns: 1fr; }.instance-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 639px) { .dashboard-header { align-items: flex-start; flex-direction: column; }.instance-grid { grid-template-columns: 1fr; }.dashboard-grid { gap: 12px; } }
</style>

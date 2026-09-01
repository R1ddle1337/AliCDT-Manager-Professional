<template>
  <article class="instance-card layout-card">
    <div class="drag-handle" title="拖动排序"><span v-for="i in 6" :key="i"></span></div>

    <div class="instance-card-header flex items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <div v-if="!editingName" class="group/edit flex cursor-pointer items-center gap-2" @click="startEditName">
          <h3 class="truncate text-base font-bold text-slate-800 group-hover/edit:text-accent">{{ instance.instance_name || instance.instance_id }}</h3>
          <span class="edit-hint">编辑</span>
        </div>
        <div v-else class="flex items-center gap-2">
          <input v-model="newName" :disabled="isSavingName" class="input px-2.5 py-1.5 text-sm" @keyup.enter="saveName" @keyup.escape="editingName = false" autofocus placeholder="输入实例名称" />
          <button type="button" @click="saveName" :disabled="isSavingName" class="btn-primary px-2.5 py-1.5 text-xs">{{ isSavingName ? '保存中' : '保存' }}</button>
          <button type="button" @click="editingName = false" :disabled="isSavingName" class="btn-ghost px-2 py-1.5 text-xs">取消</button>
        </div>
        <div class="instance-id mt-1 truncate text-xs text-slate-400">{{ instance.instance_id }}</div>
      </div>
      <div class="flex flex-col items-end gap-2">
        <span class="region-label" :title="regionLabel">{{ regionLabel || '未知地域' }}</span>
        <span class="status-chip" :class="statusClass"><span class="status-dot" :class="instance.status === 'Running' ? 'status-dot-success' : 'status-dot-muted'"></span>{{ statusLabel }}</span>
      </div>
    </div>

    <div class="instance-card-traffic traffic-panel mt-5">
      <div class="flex items-center justify-between gap-3 text-xs"><span class="font-semibold text-slate-500">本月流量</span><span class="font-bold tabular-nums" :class="trafficColor">{{ instance.traffic_used_gb?.toFixed(2) || '0.00' }} <span class="font-normal text-slate-400">/ {{ account?.traffic_limit_gb || 200 }} GB</span></span></div>
      <div class="mt-2 h-2 overflow-hidden rounded-full bg-slate-200"><div class="h-full rounded-full transition-all duration-700" :class="trafficBarColor" :style="{ width: Math.min(trafficPct, 100) + '%' }"></div></div>
      <div class="mt-2 flex justify-between text-[11px] text-slate-400"><span>熔断阈值 {{ account?.threshold_percent || 95 }}%</span><span :class="trafficColor">{{ trafficPct.toFixed(1) }}%</span></div>
    </div>

    <div class="instance-card-details mt-4 grid grid-cols-2 gap-2"><div class="detail-box"><span>公网 IP</span><MaskedIP :value="instance.public_ip" placeholder="—" /></div><div class="detail-box"><span>实例规格</span><strong :title="instance.instance_type">{{ instance.instance_type || '—' }}</strong></div></div>

    <div class="instance-card-tags mt-3 flex flex-wrap gap-2"><span v-if="account?.keep_alive" class="tag tag-blue">自动保活</span><span v-if="account?.auto_stop_time" class="tag tag-muted">{{ account.auto_stop_time }} 关机</span><span v-if="account?.auto_start_time" class="tag tag-muted">{{ account.auto_start_time }} 开机</span><span class="tag tag-muted">{{ account?.shutdown_mode === 'StopCharging' ? '节省停机' : '普通停机' }}</span></div>

    <div v-if="showBilling" class="instance-card-billing billing-panel mt-4"><div class="flex items-center justify-between"><span class="panel-label">账单摘要</span><span v-if="billingLoading" class="h-3.5 w-3.5 animate-spin rounded-full border-2 border-accent/30 border-t-accent"></span></div><div v-if="billingError" class="mt-2 text-xs leading-5 text-danger">{{ billingError }}</div><div v-if="!billingLoading && (billing?.balance || billing?.bill)" class="mt-3 grid grid-cols-2 gap-3"><div><span class="detail-label">账户余额</span><strong class="tabular-nums" :class="(billing?.balance?.available_amount ?? 0) < 1 ? 'text-danger' : 'text-success'">{{ billing?.balance?.symbol }}{{ billing?.balance?.available_amount ?? '—' }}</strong></div><div><span class="detail-label">本月待还</span><strong class="tabular-nums" :class="(billing?.bill?.total_outstanding ?? 0) > 0 ? 'text-warning-dark' : 'text-slate-700'">{{ billing?.bill?.symbol }}{{ billing?.bill?.total_outstanding ?? '—' }}</strong></div></div></div>

    <div class="instance-card-footer mt-4 flex items-center justify-between border-t border-slate-100 pt-4 text-[11px] text-slate-400"><span class="truncate pr-2">{{ account?.name || '未知账户' }}</span><div class="flex items-center gap-2"><span v-if="instance.last_synced">同步于 {{ formatTime(instance.last_synced) }}</span><button type="button" @click="syncThis" :disabled="isSyncing" class="btn-ghost border border-slate-200 px-2 py-1 text-[10px]">{{ isSyncing ? '同步中' : '同步' }}</button></div></div>

    <div class="instance-card-actions mt-3 flex gap-2 border-t border-slate-100 pt-3"><button v-if="instance.status !== 'Running'" type="button" @click="handleStart" :disabled="isStarting" class="action-button action-start">{{ isStarting ? '启动中...' : '启动实例' }}</button><button v-if="instance.status === 'Running'" type="button" @click="handleStop" :disabled="isStopping" class="action-button action-stop">{{ isStopping ? '停止中...' : '停止实例' }}</button><button type="button" @click="$emit('release')" class="action-button action-release">释放</button></div>
  </article>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useStore } from '../stores'
import { formatTime as formatClockTime } from '../utils/time'
import MaskedIP from './MaskedIP.vue'

const props = defineProps({ instance: Object, account: Object })
defineEmits(['release'])
const store = useStore()
const billing = ref(null); const billingLoading = ref(false); const billingError = ref('')
const editingName = ref(false); const newName = ref(''); const isSavingName = ref(false)
const isStarting = ref(false); const isStopping = ref(false); const isSyncing = ref(false)

const REGION_MAP = {
  'cn-hangzhou': '中国 · 杭州', 'cn-shanghai': '中国 · 上海', 'cn-beijing': '中国 · 北京', 'cn-shenzhen': '中国 · 深圳', 'cn-hongkong': '中国 · 香港',
  'ap-southeast-1': '新加坡', 'ap-southeast-2': '澳大利亚 · 悉尼', 'ap-southeast-3': '马来西亚 · 吉隆坡', 'ap-southeast-5': '印度尼西亚 · 雅加达', 'ap-southeast-6': '菲律宾 · 马尼拉', 'ap-southeast-7': '泰国 · 曼谷',
  'ap-northeast-1': '日本 · 东京', 'ap-northeast-2': '韩国 · 首尔', 'ap-south-1': '印度 · 孟买', 'us-west-1': '美国 · 硅谷', 'us-east-1': '美国 · 弗吉尼亚', 'eu-west-1': '英国 · 伦敦', 'eu-central-1': '德国 · 法兰克福', 'me-east-1': '阿联酋 · 迪拜',
}
const regionLabel = computed(() => REGION_MAP[props.instance?.region_id] || props.instance?.region_id || '')
const showBilling = computed(() => props.account?.site_type !== 'china')
const statusLabel = computed(() => ({ Running: '运行中', Stopped: '已停机' }[props.instance.status] || '状态未知'))
const statusClass = computed(() => props.instance.status === 'Running' ? 'status-running' : props.instance.status === 'Stopped' ? 'status-stopped' : 'status-unknown')
const trafficPct = computed(() => Number(props.instance.traffic_percent || 0))
const trafficColor = computed(() => trafficPct.value >= 90 ? 'text-danger' : trafficPct.value >= 75 ? 'text-warning-dark' : 'text-success')
const trafficBarColor = computed(() => trafficPct.value >= 90 ? 'bg-danger' : trafficPct.value >= 75 ? 'bg-warning' : 'bg-success')

function startEditName() { newName.value = props.instance.instance_name || ''; editingName.value = true }
async function saveName() { const value = newName.value.trim(); if (!value || value === props.instance.instance_name) { editingName.value = false; return }; isSavingName.value = true; try { await store.renameInstance(props.instance.instance_id, value); editingName.value = false } catch (e) { alert('名称修改失败：' + (e.response?.data?.detail || e.message)) } finally { isSavingName.value = false } }
async function handleStart() { if (isStarting.value) return; isStarting.value = true; try { await store.controlInstance(props.instance.instance_id, 'start') } catch (e) { alert('启动失败：' + (e.response?.data?.detail || e.message)) } finally { isStarting.value = false } }
async function handleStop() { if (isStopping.value) return; isStopping.value = true; try { await store.controlInstance(props.instance.instance_id, 'stop') } catch (e) { alert('停止失败：' + (e.response?.data?.detail || e.message)) } finally { isStopping.value = false } }
async function syncThis() { if (isSyncing.value) return; isSyncing.value = true; try { await store.syncSingleInstance(props.instance.instance_id) } catch (e) { alert('同步失败：' + (e.response?.data?.detail || e.message)) } finally { isSyncing.value = false } }
async function loadBilling() { if (!props.account) return; billingLoading.value = true; billingError.value = ''; try { const result = await store.getBilling(props.account.id); billing.value = result; billingError.value = result.errors?.join('；') || '' } catch (e) { billingError.value = e.response?.data?.detail || '账单获取失败，请检查账户权限' } finally { billingLoading.value = false } }
onMounted(() => { if (showBilling.value) loadBilling() })
function formatTime(t) { return formatClockTime(t) }
</script>

<style scoped>
.instance-card { display: flex; min-height: 100%; flex-direction: column; border: 1px solid #e5eaf1; border-radius: 13px; background: #fff; padding: 18px; box-shadow: 0 4px 16px rgba(15, 23, 42, .035); transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease; }
.instance-card:hover { border-color: #bfdbfe; box-shadow: 0 10px 28px rgba(30, 64, 175, .08); transform: translateY(-1px); }
.drag-handle { display: flex; justify-content: center; gap: 3px; height: 8px; margin-bottom: 10px; cursor: grab; opacity: .35; }
.drag-handle span { width: 3px; height: 3px; border-radius: 50%; background: #94a3b8; box-shadow: 0 5px #94a3b8; }
.edit-hint { opacity: 0; border-radius: 5px; background: #eff6ff; padding: 3px 5px; color: #2563eb; font-size: 10px; transition: opacity .15s ease; }
.group\/edit:hover .edit-hint { opacity: 1; }
.region-label { max-width: 135px; overflow: hidden; color: #64748b; text-overflow: ellipsis; white-space: nowrap; font-size: 10px; font-weight: 600; }
.status-chip { display: inline-flex; align-items: center; gap: 6px; border-radius: 999px; padding: 5px 8px; font-size: 10px; font-weight: 700; }
.status-running { background: #ecfdf3; color: #15803d; }.status-stopped { background: #f1f5f9; color: #64748b; }.status-unknown { background: #fffbeb; color: #b45309; }
.status-dot { width: 6px; height: 6px; border-radius: 50%; }.status-dot-success { background: #16a34a; }.status-dot-muted { background: #94a3b8; }
.instance-id { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.traffic-panel, .billing-panel { border-radius: 9px; background: #f8fafc; padding: 12px; }.detail-box { min-width: 0; border-radius: 8px; background: #f8fafc; padding: 10px; }.detail-box span, .detail-label { display: block; color: #94a3b8; font-size: 10px; }.detail-box strong, .detail-box .masked-ip { display: block; overflow: hidden; margin-top: 4px; color: #334155; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; font-weight: 650; }.detail-box .masked-ip { display: flex; }.panel-label { color: #64748b; font-size: 11px; font-weight: 700; }.billing-panel strong { display: block; margin-top: 4px; font-size: 13px; }.tag { border-radius: 999px; padding: 4px 8px; font-size: 10px; font-weight: 650; }.tag-blue { background: #eff6ff; color: #2563eb; }.tag-muted { background: #f1f5f9; color: #64748b; }
.action-button { flex: 1; border-radius: 8px; padding: 9px 10px; font-size: 11px; font-weight: 650; transition: background .15s ease, color .15s ease; }.action-start { border: 1px solid #bbf7d0; background: #f0fdf4; color: #15803d; }.action-start:hover { background: #dcfce7; }.action-stop { border: 1px solid #fed7aa; background: #fff7ed; color: #b45309; }.action-stop:hover { background: #ffedd5; }.action-release { flex: 0 0 auto; border: 1px solid #fecaca; background: #fff; color: #dc2626; padding-inline: 15px; }.action-release:hover { background: #fef2f2; }.action-button:disabled { cursor: not-allowed; opacity: .5; }
</style>

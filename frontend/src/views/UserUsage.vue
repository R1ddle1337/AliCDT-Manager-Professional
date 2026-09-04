<template>
  <div class="usage-page fade-in">
    <header class="page-header">
      <div>
        <div class="eyebrow">MY USAGE</div>
        <h1 class="page-title">我的用量</h1>
        <p class="page-subtitle">查看入口端口、计费方向和本月可用流量</p>
      </div>
      <button class="btn-ghost border border-slate-200" type="button" :disabled="loading" @click="refresh">{{ loading ? '刷新中...' : '刷新' }}</button>
    </header>

    <div v-if="error" class="notice notice-error">{{ error }}</div>
    <template v-if="user">
      <section class="usage-summary">
        <article class="card highlight-card"><span>本月已用</span><strong>{{ user.traffic_known ? formatGB(user.traffic_used_gb) : '待同步' }}</strong><small>{{ usageSourceLabel }}</small></article>
        <article class="card summary-card"><span>我的额度</span><strong>{{ formatGB(user.traffic_limit_gb) }}</strong><small>按控制台用户设置</small></article>
        <article class="card summary-card"><span>剩余额度</span><strong>{{ formatGB(user.traffic_remaining_gb) }}</strong><small>{{ formatPercent(user.traffic_percent) }} 已使用</small></article>
      </section>

      <section class="card usage-panel">
        <div class="panel-header"><div><h2>流量进度</h2><p>用量由已分配云账户的 CDT 快照汇总</p></div><span class="panel-code">CDT</span></div>
        <div class="panel-body">
          <div class="large-track"><div class="traffic-fill" :class="user.traffic_percent >= 100 ? 'traffic-danger' : ''" :style="{ width: Math.min(100, user.traffic_percent || 0) + '%' }"></div></div>
          <div class="traffic-meta"><span>{{ formatPercent(user.traffic_percent) }} 已使用</span><span>{{ formatGB(user.traffic_remaining_gb) }} 剩余</span></div>
          <div v-if="user.traffic_source === 'agent'" class="direction-grid"><div><span>上传</span><strong>{{ formatBytes(user.bytes_up) }}</strong></div><div><span>下载</span><strong>{{ formatBytes(user.bytes_down) }}</strong></div><div><span>计费口径</span><strong>{{ billingModeLabel(user.billing_mode) }}</strong></div></div>
          <div v-if="user.traffic_percent >= 100" class="notice notice-error mt-4">已达到用户流量额度，请联系管理员调整额度或账户分配。</div>
        </div>
      </section>

      <section class="card account-panel">
        <div class="panel-header"><div><h2>我的入口端口</h2><p>同一用户全部端口共享上方的一份流量额度</p></div><span class="panel-code">{{ entryPortCount }}</span></div>
        <div v-if="user.entry_groups?.length" class="account-table">
          <div v-for="group in user.entry_groups" :key="group.id" class="entry-card">
            <div class="entry-title"><div><strong>{{ group.name }}</strong><small>{{ group.relay_node_name }} · {{ group.network.toUpperCase() }} · {{ billingModeLabel(group.billing_mode) }}</small></div><span :class="group.enabled ? 'entry-on' : 'entry-off'">{{ group.enabled ? '可用' : '已停用' }}</span></div>
            <div class="entry-address">{{ group.relay_public_ip || group.listen_host }} : {{ group.start_port }}–{{ group.start_port + group.port_count - 1 }}</div>
            <div class="port-chips"><span v-for="port in group.ports" :key="port.id">{{ port.port }}</span></div>
          </div>
        </div>
        <div v-else class="empty-panel">管理员尚未为你分配入口端口。</div>
      </section>

      <section class="card account-panel">
        <div class="panel-header"><div><h2>我的云账户</h2><p>密钥和云资源操作仅对管理员开放</p></div><span class="panel-code">{{ user.accounts.length }}</span></div>
        <div v-if="user.accounts.length" class="account-table">
          <div v-for="account in user.accounts" :key="account.id" class="account-row">
            <div><strong>{{ account.name }}</strong><small>{{ account.synced_at ? '同步于 ' + formatDate(account.synced_at) : '尚无同步快照' }}</small></div>
            <span>{{ account.traffic_known ? formatGB(account.traffic_used_gb) : '待同步' }}</span>
          </div>
        </div>
        <div v-else class="empty-panel">管理员尚未为你分配云账户。</div>
      </section>
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import { formatDateTime } from '../utils/time'

const store = useRelayStore()
const user = ref(null)
const loading = ref(false)
const error = ref('')
const usageSourceLabel = computed(() => user.value?.traffic_source === 'agent' ? '由 Relay Agent 按所选方向精确计量' : (user.value?.traffic_known ? '来自账户级 CDT 快照' : '等待管理员同步全部云账户'))
const entryPortCount = computed(() => (user.value?.entry_groups || []).reduce((total, group) => total + Number(group.port_count || 0), 0))

function formatGB(value) { return `${Number(value || 0).toFixed(2)} GB` }
function formatPercent(value) { return `${Number(value || 0).toFixed(1)}%` }
function formatBytes(value) { return `${(Number(value || 0) / 1073741824).toFixed(3)} GB` }
function billingModeLabel(value) { return { download: '仅下载', upload: '仅上传', both: '双向合计' }[value] || '双向合计' }
function formatDate(value) { return formatDateTime(value) }

async function refresh() {
  loading.value = true
  error.value = ''
  try { user.value = await store.fetchMyUsage() } catch (err) { error.value = err.response?.data?.error || '用量读取失败' }
  finally { loading.value = false }
}

onMounted(refresh)
</script>

<style scoped>
.usage-page { display: grid; gap: 20px; }
.page-header { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 5px; color: #172033; font-size: 28px; font-weight: 750; letter-spacing: -.035em; }
.page-subtitle { margin-top: 5px; color: #64748b; font-size: 12px; }
.usage-summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.summary-card, .highlight-card { padding: 18px 20px; }
.summary-card span, .highlight-card span { color: #64748b; font-size: 10px; font-weight: 700; }
.summary-card strong, .highlight-card strong { display: block; margin-top: 8px; color: #172033; font-size: 25px; }
.highlight-card { border-top: 2px solid #2563eb; }
.highlight-card strong { color: #2563eb; }
.summary-card small, .highlight-card small { display: block; margin-top: 5px; color: #94a3b8; font-size: 10px; }
.usage-panel, .account-panel { overflow: hidden; }
.panel-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; border-bottom: 1px solid #eef2f7; padding: 17px 20px; }
.panel-header h2 { color: #1e293b; font-size: 14px; font-weight: 750; }
.panel-header p { margin-top: 4px; color: #94a3b8; font-size: 10px; }
.panel-code { border-radius: 6px; background: #eff6ff; padding: 5px 7px; color: #2563eb; font-size: 9px; font-weight: 800; }
.panel-body { padding: 21px 20px; }
.large-track { height: 12px; overflow: hidden; border-radius: 999px; background: #e7edf5; }
.traffic-fill { height: 100%; border-radius: inherit; background: #2563eb; transition: width .25s ease; }
.traffic-danger { background: #dc2626; }
.traffic-meta { display: flex; justify-content: space-between; margin-top: 8px; color: #94a3b8; font-size: 10px; }
.direction-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-top: 16px; }
.direction-grid div { border-radius: 9px; background: #f8fafc; padding: 10px; }
.direction-grid span, .direction-grid strong { display: block; }
.direction-grid span { color: #94a3b8; font-size: 9px; }
.direction-grid strong { margin-top: 4px; color: #475569; font-size: 11px; }
.account-table { padding: 4px 20px 10px; }
.account-row { display: flex; align-items: center; justify-content: space-between; gap: 14px; border-bottom: 1px solid #f1f5f9; padding: 13px 0; color: #475569; font-size: 11px; }
.account-row:last-child { border-bottom: 0; }
.account-row strong, .account-row small { display: block; }
.account-row strong { color: #334155; font-size: 12px; }
.account-row small { margin-top: 4px; color: #94a3b8; font-size: 9px; }
.entry-card { border-bottom: 1px solid #f1f5f9; padding: 14px 0; }
.entry-card:last-child { border-bottom: 0; }
.entry-title { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.entry-title strong, .entry-title small { display: block; }
.entry-title strong { color: #334155; font-size: 12px; }
.entry-title small { margin-top: 4px; color: #94a3b8; font-size: 9px; }
.entry-title > span { border-radius: 999px; padding: 4px 7px; font-size: 9px; font-weight: 700; }
.entry-on { background: #ecfdf3; color: #15803d; }
.entry-off { background: #f1f5f9; color: #64748b; }
.entry-address { margin-top: 10px; color: #1d4ed8; font-family: ui-monospace, monospace; font-size: 11px; font-weight: 700; }
.port-chips { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 9px; }
.port-chips span { border: 1px solid #dbeafe; border-radius: 6px; background: #f8fbff; padding: 4px 6px; color: #475569; font-family: ui-monospace, monospace; font-size: 9px; }
.empty-panel { padding: 48px 20px; color: #94a3b8; font-size: 12px; text-align: center; }
@media (max-width: 700px) { .page-header { align-items: stretch; flex-direction: column; } .usage-summary, .direction-grid { grid-template-columns: 1fr; gap: 10px; } }
</style>

<template>
  <div class="space-y-5 fade-in">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div><div class="eyebrow">RELAY CONTROL</div><h1 class="page-title">中转总览</h1><p class="page-subtitle">CDT 入口、落地健康状态与实时连接摘要</p></div>
      <button class="btn-primary" :disabled="store.loading" @click="store.fetchAll">{{ store.loading ? '刷新中...' : '刷新状态' }}</button>
    </header>

    <section class="grid grid-cols-2 gap-3 xl:grid-cols-4 summary-fixed">
      <div class="card metric-card"><span>中转节点</span><strong>{{ store.relayNodes.length }}</strong><small>{{ onlineNodes }} 在线</small></div>
      <div class="card metric-card"><span>转发服务</span><strong>{{ entries.length }}</strong><small>{{ enabledServices }} 已启用</small></div>
      <div class="card metric-card"><span>落地节点</span><strong>{{ store.landingNodes.length }}</strong><small>{{ targetCount }} 个服务目标</small></div>
      <div class="card metric-card"><span>当前连接</span><strong>{{ activeConnections }}</strong><small>累计 {{ totalConnections }}</small></div>
    </section>

    <section class="overview-grid">
      <div class="card overflow-hidden">
        <div class="panel-header"><div><h2>运行中的入口</h2><p>用户连接保持使用同一个 CDT IP 和端口</p></div><span class="panel-code">ENTRY</span></div>
        <div v-if="entries.length === 0" class="empty-panel">还没有转发服务</div>
        <div v-else class="divide-y divide-slate-100">
          <div v-for="entry in entries" :key="entry.id" class="service-row">
            <div class="min-w-0"><div class="flex items-center gap-2"><div class="truncate font-semibold text-slate-800">{{ entry.name }}</div><span v-if="entry.kind === 'pool'" class="entry-kind">入口池</span></div><div class="mt-1 truncate text-xs text-slate-400">{{ entry.address }}</div></div>
            <span class="protocol-tag">{{ entry.network.toUpperCase() }}</span><span class="mode-tag">{{ entry.kind === 'pool' ? `Relay ${entry.onlineMembers}/${entry.activeMembers}` : modeLabel(entry.mode) }}</span>
            <div class="text-right"><div class="text-xs font-semibold" :class="entry.enabled ? 'text-success' : 'text-slate-400'">{{ entry.enabled ? '运行中' : '已停用' }}</div><div class="mt-1 text-[10px] text-slate-400">{{ entry.targetCount }} 个目标</div></div>
          </div>
        </div>
      </div>

      <aside class="space-y-4">
        <div class="card p-5"><div class="panel-title"><div><h2>节点状态</h2><p>Agent 心跳</p></div><span class="panel-code">AGENT</span></div><div class="mt-5 space-y-3"><div v-for="node in store.relayNodes" :key="node.id" class="node-line"><span class="status-dot" :class="node.status === 'online' ? 'status-dot-success' : 'status-dot-muted'"></span><span class="min-w-0 flex-1 truncate">{{ node.name }}</span><strong>{{ node.status === 'online' ? '在线' : '离线' }}</strong></div><div v-if="!store.relayNodes.length" class="text-xs text-slate-400">尚未注册中转节点</div></div></div>
        <div class="card p-5"><div class="panel-title"><div><h2>最近事件</h2><p>发布与健康状态变化</p></div><span class="panel-code">EVENT</span></div><div class="mt-4 space-y-3"><div v-for="event in store.events.slice(0,6)" :key="event.id" class="event-line"><span class="event-dot" :class="event.level==='warning'?'event-warning':''"></span><div class="min-w-0"><p class="truncate">{{ event.message }}</p><small>{{ formatTime(event.created_at) }}</small></div></div><div v-if="!store.events.length" class="text-xs text-slate-400">暂无事件</div></div><div class="mt-4 flex flex-wrap gap-2 border-t border-slate-100 pt-4"><span v-for="item in protocols" :key="item" class="protocol-chip">{{ item }}</span></div></div>
      </aside>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import { useRelayStore } from '../stores/relay'
import { formatDateTime } from '../utils/time'
const store = useRelayStore(); const protocols = ['SS2022', 'VLESS', 'REALITY', 'WebSocket', 'gRPC', 'TCP', 'UDP']
const onlineNodes = computed(() => store.relayNodes.filter(node => node.status === 'online').length)
const entries = computed(() => [
  ...store.pools.map(pool => {
    const members = Array.isArray(pool.members) ? pool.members : []
    const activeMembers = members.filter(member => member.enabled).length
    const onlineMembers = members.filter(member => member.enabled && member.status === 'online').length
    return {
      id: `pool:${pool.id}`,
      kind: 'pool',
      name: pool.name,
      address: `${pool.hostname}:${pool.listen_port}`,
      network: pool.network,
      mode: pool.mode,
      enabled: pool.enabled,
      targetCount: Array.isArray(pool.targets) ? pool.targets.length : 0,
      activeMembers,
      onlineMembers,
    }
  }),
  ...store.services.map(service => ({
    id: `service:${service.id}`,
    kind: 'service',
    name: service.name,
    address: entryAddress(service),
    network: service.network,
    mode: service.mode,
    enabled: service.enabled,
    targetCount: Array.isArray(service.targets) ? service.targets.length : 0,
  })),
])
const enabledServices = computed(() => entries.value.filter(entry => entry.enabled).length)
const targetCount = computed(() => entries.value.reduce((sum, entry) => sum + entry.targetCount, 0))
const serviceStatuses = computed(() => store.relayNodes.flatMap(node => Array.isArray(node.service_status) ? node.service_status : []))
const activeConnections = computed(() => serviceStatuses.value.reduce((sum, item) => sum + Number(item.active_connections || 0), 0))
const totalConnections = computed(() => serviceStatuses.value.reduce((sum, item) => sum + Number(item.total_connections || 0), 0))
function entryAddress(service) { const node = store.relayNodes.find(item => item.id === service.relay_node_id); return `${node?.public_ip || service.listen_host}:${service.listen_port}` }
function modeLabel(mode) { return { failover: '主备', round_robin: '轮询', ip_hash: 'IP Hash', weighted: '加权' }[mode] || mode }
function formatTime(value) { return formatDateTime(value) }
onMounted(() => store.fetchAll())
</script>

<style scoped>
.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:5px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.metric-card{position:relative;overflow:hidden;border:1px solid #e5eaf1;border-radius:13px;background:#fff;padding:17px 18px;box-shadow:0 4px 16px rgba(15,23,42,.035)}.metric-card:before{position:absolute;inset:0 auto 0 0;width:3px;background:#2563eb;content:""}.metric-card span{display:block;color:#64748b;font-size:11px;font-weight:650}.metric-card strong{display:block;margin-top:8px;color:#172033;font-size:28px;line-height:1;font-weight:750}.metric-card small{display:block;margin-top:7px;color:#94a3b8;font-size:10px}.overview-grid{display:grid;grid-template-columns:minmax(0,1fr) 290px;gap:16px;align-items:start}.panel-header,.panel-title{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.panel-header{padding:18px 20px;border-bottom:1px solid #eef2f7}.panel-header h2,.panel-title h2{color:#1e293b;font-size:14px;font-weight:700}.panel-header p,.panel-title p{margin-top:3px;color:#94a3b8;font-size:10px}.panel-code{border-radius:6px;background:#eff6ff;padding:5px 7px;color:#2563eb;font-size:8px;font-weight:800;letter-spacing:.08em}.service-row{display:grid;grid-template-columns:minmax(0,1fr) auto auto 74px;align-items:center;gap:12px;padding:13px 20px}.protocol-tag,.mode-tag,.protocol-chip{border-radius:999px;padding:4px 8px;font-size:9px;font-weight:700}.protocol-tag{background:#eff6ff;color:#2563eb}.mode-tag,.protocol-chip{background:#f1f5f9;color:#64748b}.node-line{display:flex;align-items:center;gap:8px;color:#64748b;font-size:11px}.node-line strong{font-size:10px;color:#475569}.event-line{display:flex;align-items:flex-start;gap:8px;color:#64748b;font-size:10px}.event-line p{line-height:1.4}.event-line small{display:block;margin-top:2px;color:#94a3b8;font-size:8px}.event-dot{width:6px;height:6px;flex:0 0 auto;margin-top:4px;border-radius:50%;background:#16a34a}.event-warning{background:#d97706}.empty-panel{padding:48px;text-align:center;color:#94a3b8;font-size:12px}
@media(max-width:900px){.overview-grid{grid-template-columns:1fr}}@media(max-width:520px){.service-row{grid-template-columns:minmax(0,1fr) auto}.mode-tag{display:none}.service-row>div:last-child{display:none}}.entry-kind{border-radius:999px;background:#eef2ff;padding:4px 8px;color:#4f46e5;font-size:9px;font-weight:700}
</style>

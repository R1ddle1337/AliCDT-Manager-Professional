<template>
  <div class="workspace-page space-y-5 fade-in">
    <header class="workspace-header">
      <div>
        <div class="eyebrow">OPERATIONS WORKSPACE</div>
        <div class="mt-1 flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h1 class="page-title">中转管理</h1>
          <span class="live-status"><span class="status-dot status-dot-success"></span>实时状态</span>
        </div>
        <p class="page-subtitle">从一个页面查看入口、节点、落地目标和流量保护状态</p>
      </div>
      <div class="workspace-actions">
        <button class="btn-ghost border border-slate-200" :disabled="store.loading" @click="refresh">{{ store.loading ? '刷新中...' : '刷新状态' }}</button>
        <button class="btn-ghost border border-blue-200 text-blue-700" @click="go('/landing-nodes')">添加落地目标</button>
        <button class="btn-primary" @click="go('/relay-services')">创建转发入口</button>
      </div>
    </header>

    <section class="summary-grid summary-fixed">
      <article class="card workspace-stat stat-blue"><span>Relay Agent</span><strong>{{ onlineNodes }}<small>/ {{ store.relayNodes.length }}</small></strong><em>{{ upgradeNodes.length ? `${upgradeNodes.length} 台待升级` : '全部为最新版本' }}</em></article>
      <article class="card workspace-stat stat-indigo"><span>运行入口</span><strong>{{ enabledEntries }}<small>/ {{ entries.length }}</small></strong><em>{{ unhealthyEntries ? `${unhealthyEntries} 个入口需要关注` : '入口状态正常' }}</em></article>
      <article class="card workspace-stat stat-emerald"><span>落地目标</span><strong>{{ enabledLandingNodes }}<small>/ {{ store.landingNodes.length }}</small></strong><em>{{ store.landingNodes.length ? '可用于故障切换' : '请先添加落地目标' }}</em></article>
      <article class="card workspace-stat stat-amber"><span>本月已计量</span><strong :title="`${usageGB.toFixed(6)} GB`">{{ usageDisplay.value }}<small>{{ usageDisplay.unit }}</small></strong><em>{{ quotaCaption }}</em></article>
    </section>

    <section v-if="attentionItems.length" class="attention-strip card">
      <div class="attention-icon">!</div>
      <div class="min-w-0 flex-1"><strong>需要处理的事项</strong><p>{{ attentionItems.map(item => item.text).join(' · ') }}</p></div>
      <button class="btn-ghost border border-amber-200 px-3 py-1.5 text-xs text-amber-800" @click="activeTab = attentionItems[0].tab">查看详情</button>
    </section>

    <nav class="workspace-tabs" aria-label="中转管理视图">
      <button v-for="tab in tabs" :key="tab.id" type="button" :class="['workspace-tab', activeTab === tab.id ? 'workspace-tab-active' : '']" @click="activeTab = tab.id">
        <span>{{ tab.label }}</span><b>{{ tab.count }}</b>
      </button>
    </nav>

    <template v-if="activeTab === 'overview'">
      <section class="workspace-columns">
        <article class="card panel-card">
          <div class="panel-heading"><div><h2>入口健康</h2><p>入口池和独立转发统一展示，异常项优先排在前面</p></div><span class="panel-code">ENTRY</span></div>
          <div v-if="!entries.length" class="empty-panel">还没有入口，先创建一个转发入口即可开始。</div>
          <div v-else class="entry-list">
            <div v-for="entry in entries.slice(0, 8)" :key="entry.id" class="entry-item">
              <span class="status-dot" :class="entry.healthy ? 'status-dot-success' : 'status-dot-warning'"></span>
              <div class="min-w-0 flex-1"><div class="entry-title"><strong class="truncate">{{ entry.name }}</strong><span class="type-pill">{{ entry.kind === 'pool' ? '入口池' : '独立入口' }}</span></div><p class="truncate"><MaskedIP :value="entry.host" :suffix="`:${entry.port}`" placeholder="未设置入口地址" /></p></div>
              <div class="entry-meta"><strong>{{ entry.targetCount }}</strong><span>目标</span></div>
              <span class="state-tag" :class="entry.enabled && entry.healthy ? 'state-online' : ''">{{ entry.enabled ? (entry.healthy ? '运行中' : '需关注') : '已停用' }}</span>
            </div>
          </div>
          <button v-if="entries.length > 8" class="more-link" @click="activeTab = 'entries'">查看全部 {{ entries.length }} 个入口 →</button>
        </article>

        <article class="card panel-card">
          <div class="panel-heading"><div><h2>Agent 状态</h2><p>心跳、能力和远程升级状态</p></div><div class="panel-heading-actions"><button v-if="upgradeNodes.length" class="btn-ghost border border-blue-100 px-2 py-1 text-[10px] text-blue-700" :disabled="upgradeAllBusy" @click="upgradeAll">{{ upgradeAllBusy ? '提交中...' : `升级全部 ${upgradeNodes.length} 台` }}</button><span class="panel-code">AGENT</span></div></div>
          <div v-if="!store.relayNodes.length" class="empty-panel">尚未注册 Relay Agent。</div>
          <div v-else class="agent-list">
            <div v-for="node in store.relayNodes.slice(0, 6)" :key="node.id" class="agent-item">
              <span class="status-dot" :class="node.status === 'online' ? 'status-dot-success' : 'status-dot-muted'"></span>
              <div class="min-w-0 flex-1"><strong class="block truncate">{{ node.name }}</strong><small>{{ node.agent_version || '版本未知' }} · {{ formatTime(node.last_seen_at) }}<template v-if="node.update_status && node.update_status !== 'idle'"> · <span :class="node.update_status === 'failed' ? 'update-error' : 'update-progress'">{{ updateLabel(node.update_status) }}</span></template></small></div>
              <span v-if="needsUpgrade(node)" class="upgrade-pill">待升级</span><span v-else class="ready-pill">就绪</span>
              <button v-if="needsUpgrade(node)" class="btn-ghost border border-blue-100 px-2 py-1 text-[10px] text-blue-700" :disabled="upgradeBusyIDs.has(node.id) || upgradeInProgress(node)" @click="upgrade(node)">{{ upgradeInProgress(node) ? updateLabel(node.update_status) : (upgradeBusyIDs.has(node.id) ? '请求中' : '升级') }}</button>
            </div>
          </div>
          <button v-if="store.relayNodes.length > 6" class="more-link" @click="activeTab = 'nodes'">查看全部 {{ store.relayNodes.length }} 台 Agent →</button>
        </article>
      </section>

      <section class="workspace-columns workspace-columns-bottom">
        <article class="card panel-card">
          <div class="panel-heading"><div><h2>最近事件</h2><p>发布、健康和流量保护变化</p></div><span class="panel-code">EVENTS</span></div>
          <div v-if="!store.events.length" class="empty-panel">暂无事件记录。</div>
          <div v-else class="event-list"><div v-for="event in store.events.slice(0, 6)" :key="event.id" class="event-item"><span class="event-dot" :class="event.level === 'warning' ? 'event-warning' : ''"></span><div class="min-w-0 flex-1"><p class="truncate"><MaskedText :value="event.message" /></p><small>{{ formatTime(event.created_at) }}<template v-if="event.category"> · {{ event.category }}</template></small></div></div></div>
        </article>
        <article class="card panel-card quick-card">
          <div class="panel-heading"><div><h2>常用操作</h2><p>把复杂配置留到需要时再展开</p></div><span class="panel-code">QUICK</span></div>
          <div class="quick-grid">
            <button class="quick-action" @click="go('/relay-services')"><span class="quick-mark">+</span><span><strong>创建转发入口</strong><small>配置 TCP / UDP 和目标</small></span><b>→</b></button>
            <button class="quick-action" @click="go('/relay-services?quick=minecraft')"><span class="quick-mark quick-mark-mc">MC</span><span><strong>Minecraft 快速转发</strong><small>填写 IP + 端口即可创建</small></span><b>→</b></button>
            <button class="quick-action" @click="go('/relay-nodes')"><span class="quick-mark">AG</span><span><strong>添加 Relay Agent</strong><small>生成一次性安装注册码</small></span><b>→</b></button>
            <button class="quick-action" @click="go('/users')"><span class="quick-mark">US</span><span><strong>管理用户与额度</strong><small>端口组和流量流水</small></span><b>→</b></button>
          </div>
        </article>
      </section>
    </template>

    <template v-else-if="activeTab === 'entries'">
      <section class="card panel-card">
        <div class="panel-heading workspace-list-heading"><div><h2>全部入口</h2><p>入口池与独立转发共用一张清单，减少重复配置入口</p></div><div class="list-tools"><input v-model.trim="entrySearch" class="input search-input" placeholder="搜索名称、地址或协议" /><button class="btn-primary px-3 py-2 text-xs" @click="go('/relay-services')">新建入口</button></div></div>
        <div v-if="!filteredEntries.length" class="empty-panel">没有匹配的入口。</div>
        <div v-else class="entry-table"><div class="entry-table-head"><span>入口</span><span>协议 / 类型</span><span>目标</span><span>状态</span><span></span></div><div v-for="entry in filteredEntries" :key="entry.id" class="entry-table-row"><div class="min-w-0"><strong class="block truncate">{{ entry.name }}</strong><small class="truncate"><MaskedIP :value="entry.host" :suffix="`:${entry.port}`" placeholder="未设置入口地址" /></small></div><span><b class="type-pill">{{ entry.network.toUpperCase() }}</b><small class="block mt-1 text-slate-400">{{ entry.kind === 'pool' ? '入口池' : '独立入口' }}</small></span><span>{{ entry.targetCount }} 个</span><span class="state-tag" :class="entry.enabled && entry.healthy ? 'state-online' : ''">{{ entry.enabled ? (entry.healthy ? '运行中' : '需关注') : '已停用' }}</span><button class="btn-ghost px-2 py-1 text-xs" @click="go(entry.kind === 'pool' ? '/relay-pools' : '/relay-services')">管理</button></div></div>
      </section>
    </template>

    <template v-else-if="activeTab === 'nodes'">
      <section class="workspace-columns">
        <article class="card panel-card"><div class="panel-heading"><div><h2>中转节点</h2><p>远程升级不需要登录节点 root</p></div><button class="btn-primary px-3 py-2 text-xs" @click="go('/relay-nodes')">添加节点</button></div><div class="node-table"><div v-for="node in store.relayNodes" :key="node.id" class="node-table-row"><span class="status-dot" :class="node.status === 'online' ? 'status-dot-success' : 'status-dot-muted'"></span><div class="min-w-0 flex-1"><strong class="block truncate">{{ node.name }}</strong><small>{{ node.public_ip || '未上报 IP' }} · {{ node.agent_version || '版本未知' }}</small></div><span v-if="needsUpgrade(node)" class="upgrade-pill">待升级</span><span v-else class="ready-pill">版本正常</span><button v-if="needsUpgrade(node)" class="btn-ghost border border-blue-100 px-2 py-1 text-[10px] text-blue-700" :disabled="upgradeBusyIDs.has(node.id) || upgradeInProgress(node)" @click="upgrade(node)">{{ upgradeInProgress(node) ? updateLabel(node.update_status) : '远程升级' }}</button></div><div v-if="!store.relayNodes.length" class="empty-panel">尚未注册 Relay Agent。</div></div></article>
        <article class="card panel-card"><div class="panel-heading"><div><h2>落地目标</h2><p>协议参数保留不变，只替换中转入口</p></div><button class="btn-primary px-3 py-2 text-xs" @click="go('/landing-nodes')">添加目标</button></div><div class="node-table"><div v-for="node in store.landingNodes" :key="node.id" class="node-table-row"><span class="status-dot" :class="node.enabled ? 'status-dot-success' : 'status-dot-muted'"></span><div class="min-w-0 flex-1"><strong class="block truncate">{{ node.name }}</strong><small>{{ node.address }}:{{ node.port }} · {{ (node.protocol || node.network || 'TCP').toUpperCase() }}</small></div><span class="state-tag" :class="node.enabled ? 'state-online' : ''">{{ node.enabled ? '启用' : '停用' }}</span><button class="btn-ghost px-2 py-1 text-xs" @click="go('/landing-nodes')">管理</button></div><div v-if="!store.landingNodes.length" class="empty-panel">尚未添加落地目标。</div></div></article>
      </section>
    </template>

    <div v-if="message" class="notice" :class="messageType === 'error' ? 'notice-error' : 'notice-success'">{{ message }}</div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useRelayStore } from '../stores/relay'
import MaskedIP from '../components/MaskedIP.vue'
import MaskedText from '../components/MaskedText.vue'
import { usePolling } from '../utils/polling'
import { aggregateRelayUsage, formatTrafficAmount } from '../utils/traffic'

const router = useRouter()
const store = useRelayStore()
const activeTab = ref('overview')
const entrySearch = ref('')
const upgradeBusyIDs = ref(new Set())
const upgradeAllBusy = ref(false)
const message = ref('')
const messageType = ref('success')
usePolling(() => store.fetchRelayNodes(), 5000)

const entries = computed(() => [
  ...store.pools.map(pool => {
    const members = Array.isArray(pool.members) ? pool.members : []
    const online = members.filter(member => member.enabled && member.status === 'online').length
    return { id: `pool:${pool.id}`, sourceID: pool.id, kind: 'pool', name: pool.name, host: pool.hostname, port: pool.listen_port, network: pool.network, targetCount: Array.isArray(pool.targets) ? pool.targets.length : 0, enabled: pool.enabled, healthy: !pool.enabled || online > 0 || members.length === 0 }
  }),
  ...store.services.map(service => {
    const node = store.relayNodes.find(item => item.id === service.relay_node_id)
    const status = Array.isArray(node?.service_status) ? node.service_status.find(item => item.id === service.id) : null
    return { id: `service:${service.id}`, sourceID: service.id, kind: 'service', name: service.name, host: node?.public_ip || service.listen_host, port: service.listen_port, network: service.network, targetCount: Array.isArray(service.targets) ? service.targets.length : 0, enabled: service.enabled && service.user_enabled !== false, healthy: !status || !status.targets?.some(target => target.healthy === false) }
  }),
])

const onlineNodes = computed(() => store.relayNodes.filter(node => node.status === 'online').length)
const upgradeNodes = computed(() => store.relayNodes.filter(needsUpgrade))
const enabledEntries = computed(() => entries.value.filter(entry => entry.enabled).length)
const unhealthyEntries = computed(() => entries.value.filter(entry => entry.enabled && !entry.healthy).length)
const enabledLandingNodes = computed(() => store.landingNodes.filter(node => node.enabled !== false).length)
const serviceStatuses = computed(() => store.relayNodes.flatMap(node => Array.isArray(node.service_status) ? node.service_status : []))
const usageGB = computed(() => aggregateRelayUsage(store.users, store.services, serviceStatuses.value))
const usageDisplay = computed(() => formatTrafficAmount(usageGB.value))
const quotaExceededCount = computed(() => serviceStatuses.value.filter(status => status.quota_exceeded).length)
const quotaConfiguredCount = computed(() => store.services.filter(service => Number(service.traffic_limit_gb || 0) > 0).length)
const quotaCaption = computed(() => quotaExceededCount.value
  ? `${quotaExceededCount.value} 个入口已达额度`
  : quotaConfiguredCount.value
    ? `已配置 ${quotaConfiguredCount.value} 个入口额度`
    : '独立入口未设置额度')
const filteredEntries = computed(() => {
  const query = entrySearch.value.toLowerCase()
  if (!query) return entries.value
  return entries.value.filter(entry => `${entry.name} ${entry.host} ${entry.network}`.toLowerCase().includes(query))
})
const attentionItems = computed(() => {
  const items = []
  if (upgradeNodes.value.length) items.push({ text: `${upgradeNodes.value.length} 台 Agent 有可用更新`, tab: 'nodes' })
  const offline = store.relayNodes.length - onlineNodes.value
  if (offline > 0) items.push({ text: `${offline} 台中转节点离线`, tab: 'nodes' })
  if (unhealthyEntries.value) items.push({ text: `${unhealthyEntries.value} 个入口没有健康目标`, tab: 'entries' })
  if (!store.landingNodes.length) items.push({ text: '尚未配置落地目标', tab: 'nodes' })
  return items
})
const tabs = computed(() => [
  { id: 'overview', label: '工作台', count: attentionItems.value.length ? attentionItems.value.length : '✓' },
  { id: 'entries', label: '全部入口', count: entries.value.length },
  { id: 'nodes', label: '节点与目标', count: store.relayNodes.length + store.landingNodes.length },
])

function isLegacy(node) { const capabilities = node.capabilities || []; return !capabilities.includes('shared_meters_v1') || !capabilities.includes('quota_leases_v1') }
function needsUpgrade(node) { return isLegacy(node) || node.update_available }
function upgradeInProgress(node) { return ['requested', 'draining', 'updating'].includes(node.update_status) }
function updateLabel(value) { return { requested: '等待 Agent 拉取', draining: '排空中', updating: '更新中', failed: '更新失败' }[value] || value }
function go(path) { router.push(path) }
function formatTime(value) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '尚未上报' }
async function refresh() { try { await store.fetchAll(); showMessage('状态已刷新') } catch (error) { showMessage(error.response?.data?.error || '状态刷新失败', 'error') } }
async function upgrade(node) {
  if (!window.confirm(`确认远程升级 Agent“${node.name}”？现有连接不会被主动中断。`)) return
  const busy = new Set(upgradeBusyIDs.value); busy.add(node.id); upgradeBusyIDs.value = busy
  try { await store.requestAgentUpgrade(node.id); showMessage(`已向“${node.name}”下发升级请求，Agent 恢复心跳后会自动执行`) } catch (error) { showMessage(error.response?.data?.error || '远程升级请求失败', 'error') } finally { const next = new Set(upgradeBusyIDs.value); next.delete(node.id); upgradeBusyIDs.value = next }
}
async function upgradeAll() {
  if (!upgradeNodes.value.length || !window.confirm(`确认升级 ${upgradeNodes.value.length} 台 Agent？系统会按架构校验 SHA256，并自动兼容旧版节点。`)) return
  upgradeAllBusy.value = true
  try {
    const result = await store.requestAgentUpgradeAll()
    showMessage(result.requested ? `已提交 ${result.requested} 台 Agent 升级任务，页面会自动刷新状态。` : '所有 Agent 已具备最新能力。')
  } catch (error) { showMessage(error.response?.data?.error || '升级任务提交失败，请检查宿主机升级服务', 'error') } finally { upgradeAllBusy.value = false }
}
function showMessage(text, type = 'success') { message.value = text; messageType.value = type; window.setTimeout(() => { message.value = '' }, 5000) }
onMounted(async () => {
  await Promise.all([store.fetchAll(), store.fetchUsers()])
})
</script>

<style scoped>
.panel-heading-actions{display:flex;align-items:center;gap:7px}
.status-dot{width:7px;height:7px;flex:0 0 auto;border-radius:50%;background:#94a3b8}.status-dot-success{background:#16a34a;box-shadow:0 0 0 3px #dcfce7}.status-dot-muted{background:#94a3b8;box-shadow:none}.status-dot-warning{background:#d97706;box-shadow:0 0 0 3px #fef3c7}.update-progress{color:#2563eb}.update-error{color:#b91c1c}
.workspace-page{min-width:0}.workspace-header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px}.workspace-actions{display:flex;flex-wrap:wrap;gap:8px}.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:5px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.live-status{display:inline-flex;align-items:center;gap:7px;color:#15803d;font-size:11px;font-weight:650}.status-dot{width:7px;height:7px;flex:0 0 auto;border-radius:50%;background:#94a3b8}.status-dot-success{background:#16a34a;box-shadow:0 0 0 3px #dcfce7}.status-dot-muted{background:#94a3b8;box-shadow:none}.status-dot-warning{background:#d97706;box-shadow:0 0 0 3px #fef3c7}.summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.workspace-stat{position:relative;overflow:hidden;padding:17px 18px}.workspace-stat:before{position:absolute;inset:0 auto 0 0;width:3px;content:""}.workspace-stat span{display:block;color:#64748b;font-size:11px;font-weight:650}.workspace-stat strong{display:block;margin-top:8px;color:#172033;font-size:28px;line-height:1;font-weight:750}.workspace-stat strong small{margin-left:5px;color:#94a3b8;font-size:14px;font-weight:650}.workspace-stat em{display:block;margin-top:8px;color:#94a3b8;font-size:10px;font-style:normal}.stat-blue:before{background:#2563eb}.stat-indigo:before{background:#6366f1}.stat-emerald:before{background:#10b981}.stat-amber:before{background:#f59e0b}.attention-strip{display:flex;align-items:center;gap:12px;border-color:#fde68a;background:#fffbeb;padding:11px 14px}.attention-icon{display:grid;width:26px;height:26px;flex:0 0 auto;place-items:center;border-radius:8px;background:#fef3c7;color:#a16207;font-size:13px;font-weight:800}.attention-strip strong{color:#92400e;font-size:11px}.attention-strip p{margin-top:2px;overflow:hidden;color:#a16207;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.workspace-tabs{display:flex;gap:3px;border-bottom:1px solid #e5eaf1}.workspace-tab{display:inline-flex;align-items:center;gap:7px;border:0;border-bottom:2px solid transparent;background:transparent;padding:10px 13px;color:#64748b;cursor:pointer;font-size:12px;font-weight:650}.workspace-tab:hover{color:#1d4ed8}.workspace-tab-active{border-bottom-color:#2563eb;color:#1d4ed8}.workspace-tab b{min-width:18px;border-radius:999px;background:#f1f5f9;padding:2px 5px;color:#94a3b8;font-size:9px;text-align:center}.workspace-tab-active b{background:#dbeafe;color:#2563eb}.workspace-columns{display:grid;grid-template-columns:minmax(0,1.35fr) minmax(320px,.8fr);gap:16px}.workspace-columns-bottom{grid-template-columns:minmax(0,1fr) minmax(320px,.8fr)}.panel-card{min-width:0;overflow:hidden;padding:18px}.panel-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:15px}.panel-heading h2{color:#1e293b;font-size:14px;font-weight:700}.panel-heading p{margin-top:3px;color:#94a3b8;font-size:10px;line-height:1.5}.panel-code{border-radius:6px;background:#eff6ff;padding:5px 7px;color:#2563eb;font-size:8px;font-weight:800;letter-spacing:.08em}.entry-list,.agent-list,.event-list,.node-table{display:grid;gap:7px}.entry-item,.agent-item,.event-item,.node-table-row{display:flex;min-width:0;align-items:center;gap:9px;border-radius:9px;background:#f8fafc;padding:10px 11px}.entry-title{display:flex;align-items:center;gap:7px}.entry-title strong{color:#334155;font-size:11px}.entry-item p{margin-top:3px;color:#94a3b8;font-size:9px}.type-pill{display:inline-flex;width:max-content;border-radius:999px;background:#eef2ff;padding:3px 6px;color:#4f46e5;font-size:8px;font-weight:700}.entry-meta{display:grid;flex:0 0 32px;justify-items:center;color:#94a3b8;font-size:8px}.entry-meta strong{color:#475569;font-size:12px}.state-tag{border-radius:999px;background:#f1f5f9;padding:5px 8px;color:#64748b;font-size:9px;font-weight:700;white-space:nowrap}.state-online{background:#ecfdf3;color:#15803d}.agent-item{padding:9px 10px}.agent-item strong{color:#475569;font-size:11px}.agent-item small,.node-table-row small{display:block;margin-top:3px;color:#94a3b8;font-size:9px}.upgrade-pill,.ready-pill{border-radius:999px;padding:4px 7px;font-size:9px;font-weight:700;white-space:nowrap}.upgrade-pill{background:#fef3c7;color:#a16207}.ready-pill{background:#ecfdf3;color:#15803d}.event-item{align-items:flex-start;background:transparent;padding:4px 0}.event-dot{width:6px;height:6px;flex:0 0 auto;margin-top:5px;border-radius:50%;background:#16a34a}.event-warning{background:#d97706}.event-item p{color:#64748b;font-size:10px}.event-item small{display:block;margin-top:3px;color:#94a3b8;font-size:9px}.more-link{margin-top:12px;border:0;background:transparent;padding:0;color:#2563eb;cursor:pointer;font-size:10px;font-weight:700}.quick-grid{display:grid;gap:8px}.quick-action{display:flex;align-items:center;gap:9px;border:1px solid #eef2f7;border-radius:9px;background:#fff;padding:9px;text-align:left;cursor:pointer;transition:.15s ease}.quick-action:hover{border-color:#bfdbfe;background:#f8fbff}.quick-action>span:nth-child(2){display:grid;min-width:0;gap:3px;flex:1}.quick-action strong{color:#475569;font-size:10px}.quick-action small{color:#94a3b8;font-size:9px}.quick-action>b{color:#94a3b8;font-size:13px}.quick-mark{display:grid;width:28px;height:28px;flex:0 0 auto;place-items:center;border-radius:8px;background:#eff6ff;color:#2563eb;font-size:9px;font-weight:800}.quick-mark-mc{background:#ecfdf5;color:#15803d;font-size:8px}.workspace-list-heading{align-items:center}.list-tools{display:flex;align-items:center;gap:8px}.search-input{width:220px;min-height:34px;font-size:11px}.entry-table{display:grid}.entry-table-head,.entry-table-row{display:grid;grid-template-columns:minmax(160px,1.6fr) minmax(100px,1fr) 70px 88px 52px;align-items:center;gap:12px}.entry-table-head{padding:0 11px 8px;color:#94a3b8;font-size:9px;font-weight:700}.entry-table-row{border-top:1px solid #eef2f7;padding:11px;color:#64748b;font-size:10px}.entry-table-row strong{color:#334155;font-size:11px}.entry-table-row small{display:block;margin-top:3px;color:#94a3b8;font-size:9px}.node-table-row{min-height:52px}.node-table-row .state-tag{margin-left:auto}.empty-panel{padding:38px 12px;text-align:center;color:#94a3b8;font-size:11px}@media(max-width:1000px){.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.workspace-columns,.workspace-columns-bottom{grid-template-columns:1fr}}@media(max-width:639px){.workspace-header{align-items:flex-start;flex-direction:column}.workspace-actions{width:100%}.workspace-actions>*{flex:1}.attention-strip{align-items:flex-start}.attention-strip button{display:none}.workspace-tab{padding-inline:9px}.workspace-list-heading{align-items:flex-start;flex-direction:column}.list-tools{width:100%}.search-input{width:100%;flex:1}.entry-table-head{display:none}.entry-table-row{grid-template-columns:minmax(0,1fr) auto;gap:7px}.entry-table-row>span:nth-child(2),.entry-table-row>span:nth-child(3){display:none}.entry-table-row>button{grid-column:2;grid-row:1}.entry-table-row>span:nth-child(4){grid-column:2;grid-row:2}.summary-grid{gap:8px}.workspace-stat{padding:14px}.workspace-stat strong{font-size:24px}.workspace-stat em{font-size:9px}}
</style>

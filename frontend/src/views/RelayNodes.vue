<template>
  <div class="space-y-5 fade-in relay-nodes-page">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div><div class="eyebrow">CDT RELAYS</div><h1 class="page-title">中转节点</h1><p class="page-subtitle">安装在阿里云 CDT ECS 上的 Go Relay Agent · 入口 IP 默认仅显示前两段</p></div>
      <div class="flex flex-wrap items-end gap-2">
        <label class="account-picker"><span>绑定云账户</span><select v-model="selectedAccountID" class="input"><option value="">不绑定（Agent 有元数据时自动关联）</option><option v-for="account in store.cloud.accounts" :key="account.id" :value="String(account.id)">{{ account.name }} · {{ account.region_id }}</option></select></label>
        <button class="btn-ghost border border-blue-200 text-blue-700" :disabled="upgradeBusy || !legacyNodes.length" @click="requestUpgradeAll">{{ upgradeBusy ? '升级请求中...' : (legacyNodes.length ? `远程升级 ${legacyNodes.length} 台 Agent` : 'Agent 已是最新') }}</button>
        <button class="btn-primary" @click="generateToken">添加中转节点</button>
      </div>
    </header>

    <div v-if="legacyNodeCount" class="notice notice-info legacy-agent-notice"><span>发现 {{ legacyNodeCount }} 台 Relay Agent 尚未支持共享计量/多 Relay 额度租约。</span><button class="btn-ghost border border-blue-200 px-2 py-1 text-xs text-blue-700" :disabled="upgradeBusy" @click="requestUpgradeAll">远程升级全部</button></div>
    <div v-if="upgradeMessage" class="notice" :class="upgradeMessageType === 'error' ? 'notice-error' : 'notice-success'">{{ upgradeMessage }}</div>
    <div v-if="installCommand" class="card command-card p-5"><div class="flex items-start justify-between gap-4"><div><h2 class="text-sm font-bold text-slate-800">SSH 安装命令</h2><p class="mt-1 text-xs text-slate-500">在目标 CDT 服务器 root 终端执行；注册码 {{ tokenTTL }} 分钟内有效且只能使用一次。</p></div><button class="btn-ghost border border-slate-200 text-xs" @click="copyCommand">复制</button></div><pre class="command-box mt-4"><MaskedText :value="installCommand" /></pre></div>
    <div v-if="error" class="notice notice-error">{{ error }}</div>

    <section class="relay-node-grid layout-collection layout-collection--compact">
      <article v-for="node in store.relayNodes" :key="node.id" class="card relay-node-card layout-card">
        <div class="relay-node-head">
          <div class="min-w-0"><div class="flex items-center gap-2"><span class="status-dot" :class="node.status === 'online' ? 'status-dot-success' : 'status-dot-muted'"></span><h2 class="truncate font-bold text-slate-800">{{ node.name }}</h2></div><p class="mt-2 font-mono text-[11px] text-slate-400">{{ node.id }}</p></div>
          <div class="relay-node-actions">
            <span class="state-tag" :class="node.status === 'online' ? 'state-online' : ''">{{ node.status === 'online' ? '在线' : '离线' }}</span>
            <button v-if="isLegacy(node)" class="btn-ghost border border-blue-100 px-2 py-1 text-xs text-blue-700" :disabled="upgradeBusy || node.update_status === 'requested'" @click="requestUpgrade(node)">{{ node.update_status === 'requested' ? '已请求' : '立即升级' }}</button>
          </div>
        </div>
        <div class="relay-node-metrics">
          <div class="info-box info-box-ip"><span>入口 IP</span><MaskedIP :value="node.public_ip" placeholder="待设置" /></div>
          <div class="info-box"><span>系统</span><strong>{{ node.os || '—' }}/{{ node.architecture || '—' }}</strong></div>
          <div class="info-box"><span>配置版本</span><strong>{{ node.current_revision }}/{{ node.desired_revision }}</strong></div>
          <div class="info-box"><span>Agent</span><strong>{{ node.agent_version || '—' }}</strong></div>
        </div>
        <div class="relay-node-foot">最后心跳：{{ formatTime(node.last_seen_at) }}<span class="ml-3" :class="node.capabilities?.includes('quota_leases_v1') ? 'text-emerald-600' : 'text-amber-600'">{{ node.capabilities?.includes('quota_leases_v1') ? '支持共享额度租约' : 'Agent 待升级后支持多 Relay 分片' }}</span><span v-if="node.update_status && node.update_status !== 'idle'" class="ml-3" :class="node.update_status === 'failed' ? 'text-red-600' : 'text-blue-600'">Agent 更新：{{ updateLabel(node.update_status) }}<span v-if="node.update_error">（{{ node.update_error }}）</span></span></div>
      </article>
      <div v-if="!store.relayNodes.length" class="card empty-panel layout-card">尚未注册中转节点</div>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import MaskedIP from '../components/MaskedIP.vue'
import MaskedText from '../components/MaskedText.vue'
import { usePolling } from '../utils/polling'
const store = useRelayStore()
const token = ref('')
const tokenTTL = 30
const error = ref('')
const selectedAccountID = ref('')
const legacyNodeCount = computed(() => store.relayNodes.filter(node => !(node.capabilities || []).includes('shared_meters_v1') || !(node.capabilities || []).includes('quota_leases_v1')).length)
const legacyNodes = computed(() => store.relayNodes.filter(isLegacy))
const upgradeBusy = ref(false)
const upgradeMessage = ref('')
const upgradeMessageType = ref('success')
usePolling(() => store.fetchRelayNodes(), 5000)

const installCommand = computed(() => token.value
  ? `curl -fsSL https://${window.location.host}/agent/install.sh | sh -s -- --server https://${window.location.host} --token ${token.value}`
  : '')
async function generateToken() {
  error.value = ''
  try {
    const accountID = selectedAccountID.value ? Number(selectedAccountID.value) : null
    const data = await store.createEnrollmentToken(tokenTTL, accountID)
    token.value = data.token
  } catch (e) {
    error.value = e.response?.data?.error || '生成注册码失败'
  }
}

async function copyCommand() {
  await navigator.clipboard.writeText(installCommand.value)
}

function isLegacy(node) { const capabilities = node.capabilities || []; return !capabilities.includes('shared_meters_v1') || !capabilities.includes('quota_leases_v1') }
async function requestUpgrade(node) {
  if (!window.confirm(`确认远程升级 Agent“${node.name}”？Agent 会先排空新连接，升级期间现有连接不会被主动中断。`)) return
  upgradeBusy.value = true
  upgradeMessage.value = ''
  upgradeMessageType.value = 'success'
  try { await store.requestAgentUpgrade(node.id); upgradeMessage.value = `已向“${node.name}”下发远程升级请求，Agent 将自动下载校验并重启。` } catch (error) { upgradeMessageType.value = 'error'; upgradeMessage.value = error.response?.data?.error || '远程升级请求失败' } finally { upgradeBusy.value = false }
}
async function requestUpgradeAll() {
  if (!legacyNodes.value.length || !window.confirm(`确认向 ${legacyNodes.value.length} 台旧版 Agent 下发远程升级请求？`)) return
  upgradeBusy.value = true
  upgradeMessage.value = ''
  upgradeMessageType.value = 'success'
  try {
    const result = await store.requestAgentUpgradeAll()
    upgradeMessage.value = result.requested ? `已提交 ${result.requested} 台 Agent 的宿主机兼容升级任务，页面会自动跟踪状态。` : '所有 Agent 已具备最新能力。'
  } catch (error) {
    upgradeMessageType.value = 'error'
    upgradeMessage.value = error.response?.data?.error || '升级任务提交失败，请检查宿主机升级服务'
  } finally { upgradeBusy.value = false }
}

function formatTime(value) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未上报'
}

function updateLabel(value) {
  return { requested: '等待 Agent 拉取', draining: '排空中', updating: '更新中', failed: '更新失败' }[value] || value
}

onMounted(async () => {
  await Promise.all([store.fetchRelayNodes(), store.fetchCloud()])
})
</script>

<style scoped>
.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:5px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.account-picker{display:grid;gap:4px;min-width:220px}.account-picker span{color:#64748b;font-size:9px;font-weight:700}.account-picker .input{min-height:36px;font-size:10px}.command-card{border-left:3px solid #93c5fd}.command-box{overflow:auto;border-radius:9px;background:#0f172a;padding:14px;color:#dbeafe;font-size:11px;line-height:1.7;white-space:pre-wrap}.legacy-agent-notice{display:flex;align-items:center;justify-content:space-between;gap:12px}.relay-node-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.relay-node-card{display:flex;min-width:0;flex-direction:column;padding:18px}.relay-node-head{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.relay-node-actions{display:flex;align-items:center;gap:7px}.state-tag{border-radius:999px;background:#f1f5f9;padding:5px 9px;color:#64748b;font-size:10px;font-weight:700}.state-online{background:#ecfdf3;color:#15803d}.relay-node-metrics{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:18px}.info-box{min-width:0;border-radius:8px;background:#f8fafc;padding:10px}.info-box span{display:block;color:#94a3b8;font-size:9px}.info-box strong{display:block;overflow:hidden;margin-top:4px;color:#475569;text-overflow:ellipsis;white-space:nowrap;font-size:10px}.info-box-ip{background:#f5f9ff;border:1px solid #e0ecff}.info-box-ip .masked-ip { margin-top: 4px; color: #475569; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; font-weight: 650; letter-spacing: .02em; }.info-box-ip .masked-ip-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.relay-node-foot{margin-top:auto;border-top:1px solid #eef2f7;padding-top:13px;color:#94a3b8;font-size:10px;line-height:1.6}.empty-panel{padding:52px;text-align:center;color:#94a3b8;font-size:12px}
@media(max-width:900px){.relay-node-grid{grid-template-columns:minmax(0,1fr)}}
@media(max-width:520px){.relay-node-head{flex-direction:column}.relay-node-actions{width:100%;justify-content:flex-end}.relay-node-card{padding:15px}.legacy-agent-notice{align-items:flex-start;flex-direction:column}}
</style>

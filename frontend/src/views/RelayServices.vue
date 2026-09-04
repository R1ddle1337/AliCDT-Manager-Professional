<template>
  <div class="space-y-5 fade-in">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div><div class="eyebrow">L4 SERVICES</div><h1 class="page-title">转发服务</h1><p class="page-subtitle">独立入口支持 Agent 字节级计量和流量额度熔断</p></div>
      <div class="flex flex-wrap gap-2"><button class="btn-ghost border border-blue-200 text-blue-700" :disabled="!canCreate" @click="openMinecraft">Minecraft 快速转发</button><button class="btn-primary" :disabled="!canCreate" @click="openCreate">创建转发服务</button></div>
    </header>
    <div v-if="!canCreate" class="notice notice-info">创建服务前至少需要一个在线中转节点和一个落地节点。</div>
    <div v-if="message" class="notice" :class="messageType === 'error' ? 'notice-error' : 'notice-success'">{{ message }}</div>

    <div class="grid grid-cols-1 gap-4 xl:grid-cols-2 layout-collection layout-collection--row">
      <article v-for="service in store.services" :key="service.id" class="card p-5 layout-card">
        <div class="service-row-head flex items-start justify-between gap-4">
          <div class="min-w-0"><h2 class="truncate font-bold text-slate-800">{{ service.name }}</h2><p class="mt-1 truncate text-xs text-slate-400"><MaskedIP :value="entryHost(service)" :suffix="`:${service.listen_port}`" placeholder="未设置入口地址" /></p><p v-if="serviceDomains(service).length" class="mt-1 truncate font-mono text-[10px] text-blue-600">DNS · {{ serviceDomains(service)[0] }}:{{ service.listen_port }}</p></div>
          <span class="state-tag" :class="service.enabled && service.user_enabled !== false ? 'state-online' : ''">{{ service.user_enabled === false ? '用户已禁用' : (service.enabled ? '运行中' : '已停用') }}</span>
        </div>
        <div class="service-row-body mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <div class="info-box"><span>协议</span><strong>{{ service.network.toUpperCase() }}</strong></div>
          <div class="info-box"><span>调度</span><strong>{{ modeLabel(service.mode) }}</strong></div>
          <div class="info-box"><span>计费</span><strong>{{ billingModeLabel(service.billing_mode) }}</strong></div>
          <div class="info-box"><span>用户</span><strong>{{ service.user_name || '未绑定' }}</strong></div>
        </div>
        <div v-if="service.traffic_limit_gb > 0" class="meter-box">
          <div><span>Agent 精确计量</span><strong>{{ agentMeterLabel(service) }} / {{ Number(service.traffic_limit_gb).toFixed(2) }} GB</strong></div>
          <span v-if="serviceStatus(service)?.quota_exceeded" class="quota-tag">额度已用完</span>
        </div>
        <div class="service-row-wide mt-4 space-y-2">
          <div v-for="target in service.targets" :key="target.id" class="target-line"><span class="status-dot status-dot-success"></span><span class="min-w-0 flex-1 truncate">{{ target.name }}</span><MaskedIP :value="target.address" :suffix="`:${target.port}`" placeholder="未设置地址" /><span>P{{ target.priority }} / W{{ target.weight }}</span></div>
        </div>
        <div class="service-row-actions mt-4 flex justify-end gap-1 border-t border-slate-100 pt-3">
          <button v-if="service.traffic_limit_gb > 0" class="btn-ghost px-2 py-1 text-xs" @click="resetTraffic(service)">流量清零</button>
          <button class="btn-ghost px-2 py-1 text-xs" @click="openEdit(service)">编辑</button>
          <button class="btn-danger px-2 py-1 text-xs" @click="remove(service)">删除</button>
        </div>
      </article>
      <div v-if="!store.services.length" class="card empty-panel layout-card xl:col-span-2">还没有独立转发服务</div>
    </div>

    <Modal v-if="showForm" @close="showForm = false">
      <form class="space-y-5 modal-form" @submit.prevent="save">
        <div><div class="eyebrow">RELAY SERVICE</div><h2 class="mt-1 text-lg font-bold text-slate-900">{{ editTarget ? '编辑转发服务' : '创建转发服务' }}</h2></div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2"><label class="field-label">服务名称</label><input v-model.trim="form.name" class="input" placeholder="例如：用户 A 独立入口" required /></div>
          <div><label class="field-label">CDT 中转节点</label><select v-model="form.relay_node_id" class="input" :disabled="!!editTarget"><option v-for="node in store.relayNodes" :key="node.id" :value="node.id">{{ node.name }} · {{ maskedIP(node.public_ip, '未设置 IP') }}</option></select></div>
          <div><label class="field-label">调度模式</label><select v-model="form.mode" class="input"><option value="failover">主备故障切换</option><option value="ip_hash">源 IP Hash</option><option value="weighted">加权轮询</option><option value="round_robin">轮询</option></select></div>
          <div><label class="field-label">监听 IP</label><input v-model="form.listen_host" class="input" /></div>
          <div><label class="field-label">监听端口</label><input v-model.number="form.listen_port" type="number" min="1" max="65535" class="input" required /></div>
          <div><label class="field-label">转发协议</label><select v-model="form.network" class="input"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP + UDP</option></select></div>
          <label class="flex items-center gap-2 self-end pb-2 text-sm text-slate-600"><input v-model="form.enabled" type="checkbox" />启用服务</label>
          <div><label class="field-label">归属用户</label><select v-model.number="form.user_id" class="input"><option :value="0">不绑定用户</option><option v-for="user in assignableUsers" :key="user.id" :value="user.id">{{ user.display_name || user.username }}</option></select></div>
          <div><label class="field-label">计费方向</label><select v-model="form.billing_mode" class="input"><option value="download">仅下载（落地 → 用户）</option><option value="upload">仅上传（用户 → 落地）</option><option value="both">双向合计</option></select></div>
          <div v-if="!form.user_id" class="sm:col-span-2"><label class="field-label">服务流量上限（GB，0 表示不限）</label><input v-model.number="form.traffic_limit_gb" type="number" min="0" step="0.01" class="input" /></div>
          <p v-else class="sm:col-span-2 field-hint">绑定用户后使用该用户的流量额度；禁用用户会立即阻止此入口继续传输。</p>
        </div>
        <div>
          <label class="field-label">落地目标</label>
          <div class="target-picker">
            <label v-for="(node, index) in store.landingNodes" :key="node.id" class="target-option"><input v-model="selectedTargets" type="checkbox" :value="node.id" /><span class="min-w-0 flex-1"><strong>{{ node.name }}</strong><small><MaskedIP :value="node.address" :suffix="`:${node.port}`" placeholder="未设置地址" /></small></span><input v-if="selectedTargets.includes(node.id)" v-model.number="targetOptions[node.id].priority" type="number" class="mini-input" title="优先级" /><input v-if="selectedTargets.includes(node.id)" v-model.number="targetOptions[node.id].weight" type="number" min="1" class="mini-input" title="权重" /></label>
          </div>
          <p class="mt-2 text-[10px] text-slate-400">一个用户只能绑定一个独立入口，保证额度由单个 Agent 准确执行。入口池暂不参与用户精确计量。</p>
        </div>
        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="flex gap-3 border-t border-slate-100 pt-4"><button type="button" class="btn-ghost flex-1 border border-slate-200" @click="showForm = false">取消</button><button class="btn-primary flex-1" :disabled="saving">{{ saving ? '发布中...' : '保存并发布' }}</button></div>
      </form>
    </Modal>

    <Modal v-if="showMinecraft" @close="showMinecraft = false">
      <form class="space-y-5 modal-form" @submit.prevent="saveMinecraft">
        <div><div class="eyebrow">MINECRAFT FORWARD</div><h2 class="mt-1 text-lg font-bold text-slate-900">Minecraft 快速转发</h2><p class="mt-2 text-xs leading-5 text-slate-500">填写服务器 IP 和端口即可创建透明转发，并可选择 DNS Provider 自动发布专属域名。</p></div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2"><label class="field-label">游戏版本</label><select v-model="minecraftForm.edition" class="input"><option value="java">Java Edition（TCP）</option><option value="bedrock">Bedrock Edition（UDP）</option></select></div>
          <div><label class="field-label">目标服务器 IP / 域名</label><input v-model.trim="minecraftForm.target_address" class="input" placeholder="例如 10.0.0.12" required /></div>
          <div><label class="field-label">目标服务器端口</label><input v-model.number="minecraftForm.target_port" type="number" min="1" max="65535" class="input" required /></div>
          <div><label class="field-label">对外监听端口</label><input v-model.number="minecraftForm.listen_port" type="number" min="1" max="65535" class="input" required /><p class="field-hint">{{ minecraftForm.entry_mode === 'dns' ? '域名会指向所选 Relay；Java 默认 25565 时无需填写端口。' : '用户将连接“中转节点公网 IP:此端口”。' }}</p></div>
          <div><label class="field-label">中转节点</label><select v-model="minecraftForm.relay_node_id" class="input" required><option v-for="node in store.relayNodes" :key="node.id" :value="node.id">{{ node.name }} · {{ maskedIP(node.public_ip, '未设置 IP') }}</option></select></div>
          <div class="sm:col-span-2"><label class="field-label">公网连接方式</label><select v-model="minecraftForm.entry_mode" class="input"><option value="ip">中转节点 IP + 端口</option><option value="dns" :disabled="!enabledDNSProviders.length">DNS 托管域名</option></select><p v-if="!enabledDNSProviders.length" class="field-hint">尚无已启用的 DNS Provider，请先到 DNS 托管页面添加。</p></div>
          <template v-if="minecraftForm.entry_mode === 'dns'">
            <div><label class="field-label">DNS Provider</label><select v-model="minecraftForm.dns_provider_id" class="input" required @change="normalizeMinecraftDNSSettings"><option v-for="provider in enabledDNSProviders" :key="provider.id" :value="provider.id">{{ provider.name }} · {{ provider.zone }}</option></select></div>
            <div><label class="field-label">专属记录名</label><input v-model.trim="minecraftForm.dns_record_name" class="input" placeholder="例如 mc" required /><p class="field-hint">请使用未被多节点入口占用的新记录名。</p></div>
            <div><label class="field-label">DNS TTL</label><select v-model.number="minecraftForm.dns_ttl" class="input"><option v-for="option in minecraftDNSTTLOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></div>
            <div class="minecraft-dns-preview"><span>玩家连接地址</span><code>{{ minecraftConnectionPreview || '填写记录名后自动生成' }}</code></div>
          </template>
          <div class="sm:col-span-2"><label class="field-label">归属用户（可选）</label><select v-model.number="minecraftForm.user_id" class="input"><option :value="0">不绑定用户</option><option v-for="user in store.users" :key="user.id" :value="user.id">{{ user.display_name || user.username }}</option></select><p v-if="minecraftForm.user_id" class="field-hint">绑定用户后使用该用户额度，并按 Agent 精确计量。</p></div>
        </div>
        <div v-if="minecraftError" class="notice notice-error">{{ minecraftError }}</div>
        <div class="flex gap-3 border-t border-slate-100 pt-4"><button type="button" class="btn-ghost flex-1 border border-slate-200" @click="showMinecraft = false">取消</button><button class="btn-primary flex-1" :disabled="minecraftSaving">{{ minecraftSaving ? '创建中...' : '创建 Minecraft 转发' }}</button></div>
      </form>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useRelayStore } from '../stores/relay'
import Modal from '../components/Modal.vue'
import MaskedIP from '../components/MaskedIP.vue'
import { composeDNSHostname, dnsRecordsForHostname, isValidDNSRecordName, normalizeDNSRecordName, normalizeTTLForProvider, reusableRelayDNSRecord, safeRelayDNSHostnames, ttlOptions } from '../utils/dns'
import { isIPv6, maskIP } from '../utils/ip'

const store = useRelayStore()
const route = useRoute()
const showForm = ref(false)
const showMinecraft = ref(false)
const editTarget = ref(null)
const saving = ref(false)
const formError = ref('')
const message = ref('')
const messageType = ref('success')
const selectedTargets = ref([])
const targetOptions = reactive({})
const minecraftSaving = ref(false)
const minecraftError = ref('')
const minecraftForm = ref({ edition: 'java', target_address: '', target_port: 25565, listen_port: 25565, relay_node_id: '', user_id: 0, entry_mode: 'ip', dns_provider_id: '', dns_record_name: 'mc', dns_ttl: 60 })

const blank = () => ({ relay_node_id: store.relayNodes[0]?.id || '', user_id: 0, name: '', listen_host: '0.0.0.0', listen_port: 443, network: 'tcp', mode: 'failover', enabled: true, billing_mode: 'both', traffic_limit_gb: 0 })
const form = ref(blank())
const canCreate = computed(() => store.relayNodes.length > 0 && store.landingNodes.length > 0)
const assignableUsers = computed(() => store.users.filter(user => !user.relay_service || user.id === editTarget.value?.user_id))
const enabledDNSProviders = computed(() => store.dnsProviders.filter(provider => provider.enabled !== false))
const selectedMinecraftDNSProvider = computed(() => enabledDNSProviders.value.find(provider => String(provider.id) === String(minecraftForm.value.dns_provider_id)))
const minecraftDNSTTLOptions = computed(() => ttlOptions(selectedMinecraftDNSProvider.value, minecraftForm.value.dns_ttl))
const minecraftHostname = computed(() => selectedMinecraftDNSProvider.value ? composeDNSHostname(minecraftForm.value.dns_record_name, selectedMinecraftDNSProvider.value.zone) : '')
const minecraftConnectionPreview = computed(() => formatMinecraftAddress(minecraftHostname.value, minecraftForm.value.listen_port, minecraftForm.value.edition))

function ensureOptions() { store.landingNodes.forEach((node, index) => { if (!targetOptions[node.id]) targetOptions[node.id] = { priority: index * 10, weight: 1 } }) }
function openCreate() { ensureOptions(); editTarget.value = null; form.value = blank(); selectedTargets.value = []; formError.value = ''; showForm.value = true }
function openMinecraft() { const provider = enabledDNSProviders.value[0]; minecraftError.value = ''; minecraftForm.value = { edition: 'java', target_address: '', target_port: 25565, listen_port: 25565, relay_node_id: store.relayNodes.find(node => node.status === 'online')?.id || store.relayNodes[0]?.id || '', user_id: 0, entry_mode: provider ? 'dns' : 'ip', dns_provider_id: provider?.id || '', dns_record_name: 'mc', dns_ttl: normalizeTTLForProvider(60, provider) }; showMinecraft.value = true }
function normalizeMinecraftDNSSettings() { minecraftForm.value.dns_ttl = normalizeTTLForProvider(minecraftForm.value.dns_ttl, selectedMinecraftDNSProvider.value) }
function openEdit(service) { ensureOptions(); editTarget.value = service; form.value = { relay_node_id: service.relay_node_id, user_id: service.user_id || 0, name: service.name, listen_host: service.listen_host, listen_port: service.listen_port, network: service.network, mode: service.mode, enabled: service.enabled, billing_mode: service.billing_mode || 'both', traffic_limit_gb: service.traffic_limit_gb || 0 }; selectedTargets.value = service.targets.map(target => target.landing_node_id); service.targets.forEach(target => { targetOptions[target.landing_node_id] = { priority: target.priority, weight: target.weight } }); formError.value = ''; showForm.value = true }

async function save() {
  if (!selectedTargets.value.length) { formError.value = '至少选择一个落地节点'; return }
  saving.value = true
  formError.value = ''
  const user = store.users.find(item => item.id === form.value.user_id)
  const payload = { ...form.value, traffic_limit_gb: user ? user.traffic_limit_gb : Number(form.value.traffic_limit_gb || 0), dial_timeout_ms: 2500, udp_idle_timeout_seconds: 60, health: { enabled: true, interval_seconds: 4, timeout_ms: 2000, failure_threshold: 2, success_threshold: 3, recovery_cooldown_seconds: 60 }, targets: selectedTargets.value.map(id => ({ landing_node_id: id, priority: Number(targetOptions[id].priority || 0), weight: Number(targetOptions[id].weight || 1), enabled: true })) }
  try { if (editTarget.value) await store.updateService(editTarget.value.id, payload); else await store.createService(payload); showForm.value = false; showMessage('配置已发布，Agent 将自动热更新') } catch (error) { formError.value = error.response?.data?.error || '发布失败' } finally { saving.value = false }
}

async function saveMinecraft() {
  minecraftSaving.value = true
  minecraftError.value = ''
  const network = minecraftForm.value.edition === 'bedrock' ? 'udp' : 'tcp'
  const user = store.users.find(item => item.id === minecraftForm.value.user_id)
  const relayNode = store.relayNodes.find(item => item.id === minecraftForm.value.relay_node_id)
  let landing = null
  let service = null
  let dnsRecord = null
  let dnsProvider = null
  let dnsRecordName = ''
  let reuseDNSRecord = null
  try {
    if (!relayNode?.public_ip) throw new Error('所选中转节点尚未上报公网 IP')
    if (minecraftForm.value.entry_mode === 'dns') {
      dnsProvider = selectedMinecraftDNSProvider.value
      if (!dnsProvider) throw new Error('请选择可用的 DNS Provider')
      dnsRecordName = normalizeDNSRecordName(minecraftForm.value.dns_record_name, dnsProvider.zone)
      if (!isValidDNSRecordName(dnsRecordName, dnsProvider.zone)) throw new Error('DNS 记录名格式无效，请使用字母、数字、短横线或多级子域名')
      const recordType = isIPv6(relayNode.public_ip) ? 'AAAA' : 'A'
      const conflicts = dnsRecordsForHostname(store.dnsRecords, dnsProvider, dnsRecordName, recordType)
      reuseDNSRecord = reusableRelayDNSRecord(store.dnsRecords, dnsProvider, dnsRecordName, recordType, relayNode.id)
      if (conflicts.length && !reuseDNSRecord) throw new Error(`域名 ${minecraftHostname.value} 已被其他或多台 Relay 使用，请换一个专属记录名`)
    }
    landing = await store.createLandingNode({ name: `Minecraft · ${minecraftForm.value.target_address}:${minecraftForm.value.target_port}`, address: minecraftForm.value.target_address, port: Number(minecraftForm.value.target_port), network, protocol: 'minecraft', share_uri: '', enabled: true })
    service = await store.createService({ relay_node_id: minecraftForm.value.relay_node_id, user_id: minecraftForm.value.user_id || 0, name: `Minecraft ${minecraftForm.value.edition === 'bedrock' ? 'Bedrock' : 'Java'} · ${minecraftForm.value.listen_port}`, listen_host: '0.0.0.0', listen_port: Number(minecraftForm.value.listen_port), network, mode: 'failover', enabled: true, billing_mode: user?.billing_mode || 'both', traffic_limit_gb: user?.traffic_limit_gb || 0, dial_timeout_ms: 2500, udp_idle_timeout_seconds: 120, health: { enabled: network === 'tcp', interval_seconds: 4, timeout_ms: 2000, failure_threshold: 2, success_threshold: 3, recovery_cooldown_seconds: 60 }, targets: [{ landing_node_id: landing.id, priority: 0, weight: 1, enabled: true }] })
    if (dnsProvider && !reuseDNSRecord) dnsRecord = await store.createDNSRecord({ provider_id: dnsProvider.id, relay_node_id: relayNode.id, name: dnsRecordName, type: isIPv6(relayNode.public_ip) ? 'AAAA' : 'A', value: '', ttl: normalizeTTLForProvider(minecraftForm.value.dns_ttl, dnsProvider), enabled: true })
  } catch (error) {
    if (dnsRecord?.id) { try { await store.deleteDNSRecord(dnsRecord.id) } catch (_) { /* preserve the creation error */ } }
    if (service?.id) { try { await store.deleteService(service.id) } catch (_) { /* preserve the creation error */ } }
    if (landing?.id) { try { await store.deleteLandingNode(landing.id) } catch (_) { /* preserve the creation error */ } }
    minecraftError.value = error.response?.data?.error || error.message || 'Minecraft 转发创建失败'
    minecraftSaving.value = false
    return
  }
  showMinecraft.value = false
  if (dnsProvider) {
    try {
      await store.syncDNSProvider(dnsProvider.id)
      showMessage(`Minecraft 转发已创建，玩家可连接 ${minecraftConnectionPreview.value}`)
    } catch (_) {
      await Promise.allSettled([store.fetchDNSRecords()])
      showMessage(`转发已创建，但域名 ${minecraftHostname.value} 暂未同步，请到 DNS 托管页面重试`, 'error')
    }
  } else {
    showMessage('Minecraft 转发已创建，等待 Agent 发布')
  }
  minecraftSaving.value = false
}

async function resetTraffic(service) { if (!window.confirm(`确认将“${service.name}”的 Agent 计量清零？`)) return; try { await store.resetServiceTraffic(service.id); showMessage('流量已清零，Agent 正在应用新计量周期') } catch (error) { showMessage(error.response?.data?.error || '清零失败', 'error') } }
async function remove(service) { if (!window.confirm(`确认删除转发服务“${service.name}”？新连接将不再进入该端口。`)) return; try { await store.deleteService(service.id); showMessage('服务已删除') } catch (error) { showMessage(error.response?.data?.error || '删除失败', 'error') } }
function entryHost(service) { return store.relayNodes.find(node => node.id === service.relay_node_id)?.public_ip || service.listen_host }
function serviceDomains(service) { return safeRelayDNSHostnames(store.dnsRecords, store.dnsProviders, service.relay_node_id) }
function serviceStatus(service) { const node = store.relayNodes.find(item => item.id === service.relay_node_id); return Array.isArray(node?.service_status) ? node.service_status.find(item => item.id === service.id) : null }
function agentMeterLabel(service) { const status = serviceStatus(service); return status?.billing_mode ? `${(Number(status.billed_bytes || 0) / 1073741824).toFixed(3)} GB` : '等待 Agent 升级' }
function maskedIP(value, fallback = '未设置 IP') { return maskIP(value) || fallback }
function modeLabel(mode) { return { failover: '主备', ip_hash: 'IP Hash', weighted: '加权', round_robin: '轮询' }[mode] || mode }
function billingModeLabel(mode) { return { download: '仅下载', upload: '仅上传', both: '双向' }[mode] || '双向' }
function formatMinecraftAddress(host, port, edition) { if (!host) return ''; const numericPort = Number(port); if (edition === 'java' && numericPort === 25565) return host; const displayHost = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host; return `${displayHost}:${numericPort}` }
function showMessage(text, type = 'success') { message.value = text; messageType.value = type; window.setTimeout(() => { message.value = '' }, 5000) }
onMounted(async () => {
  await Promise.all([store.fetchAll(), store.fetchUsers()])
  ensureOptions()
  if (route.query.quick === 'minecraft') openMinecraft()
})
</script>

<style scoped>
.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:5px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.state-tag{border-radius:999px;background:#f1f5f9;padding:5px 9px;color:#64748b;font-size:10px;font-weight:700}.state-online{background:#ecfdf3;color:#15803d}.info-box{min-width:0;border-radius:8px;background:#f8fafc;padding:9px}.info-box span{display:block;color:#94a3b8;font-size:9px}.info-box strong{display:block;overflow:hidden;margin-top:4px;color:#475569;text-overflow:ellipsis;white-space:nowrap;font-size:10px}.meter-box{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:12px;border:1px solid #dbeafe;border-radius:9px;background:#f8fbff;padding:10px 12px}.meter-box span,.meter-box strong{display:block}.meter-box span{color:#64748b;font-size:9px}.meter-box strong{margin-top:3px;color:#1e40af;font-size:11px}.meter-box .quota-tag{border-radius:999px;background:#fee2e2;padding:4px 7px;color:#b91c1c;font-weight:700}.target-line{display:flex;align-items:center;gap:8px;border-radius:7px;background:#f8fafc;padding:7px 9px;color:#64748b;font-size:10px}.target-picker{max-height:260px;overflow:auto;border:1px solid #e5eaf1;border-radius:9px}.target-option{display:flex;align-items:center;gap:9px;padding:10px 12px;border-bottom:1px solid #f1f5f9}.target-option:last-child{border:0}.target-option strong,.target-option small{display:block}.target-option strong{color:#475569;font-size:11px}.target-option small{margin-top:2px;color:#94a3b8;font-size:9px}.mini-input{width:52px;min-width:0;border:1px solid #d8e0eb;border-radius:6px;padding:5px;color:#475569;font-size:10px}.field-hint{color:#94a3b8;font-size:10px;line-height:1.55}.minecraft-dns-preview{display:grid;align-content:center;gap:4px;border:1px solid #dbeafe;border-radius:9px;background:#f8fbff;padding:9px 11px}.minecraft-dns-preview span{color:#64748b;font-size:9px}.minecraft-dns-preview code{color:#1e3a8a;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11px;overflow-wrap:anywhere}.empty-panel{padding:52px;text-align:center;color:#94a3b8;font-size:12px}@media(max-width:480px){.target-line{flex-wrap:wrap}.target-option{flex-wrap:wrap}.target-option span{min-width:0;flex:1 1 100%}.target-option .mini-input{flex:0 0 52px}}
</style>

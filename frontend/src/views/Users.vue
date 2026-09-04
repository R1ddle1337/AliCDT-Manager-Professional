<template>
  <div class="users-page fade-in">
    <header class="page-header">
      <div>
        <div class="eyebrow">ACCESS & USAGE</div>
        <h1 class="page-title">用户管理</h1>
        <p class="page-subtitle">创建控制台用户、分配云账户并管理月流量额度</p>
      </div>
      <button class="btn-primary" type="button" @click="openCreate">创建用户</button>
    </header>

    <div v-if="message.text" class="notice" :class="message.type === 'error' ? 'notice-error' : 'notice-success'">
      {{ message.text }}
    </div>

    <section class="summary-grid" aria-label="用户摘要">
      <article class="card summary-card"><span>用户总数</span><strong>{{ store.users.length }}</strong><small>{{ enabledCount }} 个账户已启用</small></article>
      <article class="card summary-card"><span>已分配云账户</span><strong>{{ assignedAccountCount }}</strong><small>{{ unassignedAccountCount }} 个尚未分配</small></article>
      <article class="card summary-card"><span>已知用量合计</span><strong>{{ totalKnownUsage.toFixed(2) }} GB</strong><small>数据来自账户级 CDT 快照</small></article>
    </section>

    <section class="user-grid layout-collection layout-collection--row">
      <article v-for="user in store.users" :key="user.id" class="card user-card layout-card" :class="{ 'user-disabled': !user.enabled }">
        <div class="user-head">
          <div class="min-w-0">
            <h2>{{ user.display_name }}</h2>
            <p>@{{ user.username }}</p>
          </div>
          <span class="state-tag" :class="user.enabled ? 'state-enabled' : 'state-disabled'">{{ user.enabled ? '已启用' : '已禁用' }}</span>
        </div>

        <div class="usage-line">
          <div><small>{{ user.traffic_source === 'agent' ? 'Agent 精确用量' : '账户 CDT 用量' }}</small><strong>{{ user.traffic_known ? formatGB(user.traffic_used_gb) : '待同步' }}</strong></div>
          <div class="usage-limit"><small>用户额度</small><strong>{{ formatGB(user.traffic_limit_gb) }}</strong></div>
        </div>
        <div class="traffic-track"><div class="traffic-fill" :class="user.traffic_percent >= 100 ? 'traffic-danger' : ''" :style="{ width: Math.min(100, user.traffic_percent || 0) + '%' }"></div></div>
        <div class="traffic-meta"><span>{{ user.traffic_known ? formatPercent(user.traffic_percent) + ' 已使用' : '存在未知用量' }}</span><span>剩余 {{ formatGB(user.traffic_remaining_gb) }}</span></div>

        <div v-if="user.relay_service && !(user.entry_groups || []).length" class="relay-binding">
          <div><span>独立入口</span><strong>{{ user.relay_service.name }}</strong><small>{{ user.relay_service.relay_node_name }} · {{ billingModeLabel(user.relay_service.billing_mode) }}</small></div>
          <span class="state-tag" :class="user.relay_service.quota_exceeded ? 'state-disabled' : 'state-enabled'">{{ user.relay_service.quota_exceeded ? '额度耗尽' : (user.relay_service.reported ? 'Agent 已上报' : '等待 Agent') }}</span>
        </div>

        <div class="entry-groups">
          <div class="account-list-title">入口端口组 <span>{{ user.entry_groups?.length || 0 }}</span></div>
          <div v-for="group in (user.entry_groups || [])" :key="group.id" class="entry-group-row">
            <div class="min-w-0"><strong>{{ group.name }}</strong><small>{{ group.relay_node_name }} · {{ group.network.toUpperCase() }} · {{ group.start_port }}–{{ group.start_port + group.port_count - 1 }}</small><small v-if="group.traffic_lease">额度分片：{{ formatByteGB(group.traffic_lease.used_bytes) }} / {{ formatByteGB(group.traffic_lease.reserved_bytes) }} · 序列 {{ group.traffic_lease.sequence }}</small><small class="port-list">端口：{{ group.ports.map(port => port.port).join(', ') }}</small></div>
            <div class="entry-group-actions"><span class="state-tag" :class="group.enabled ? 'state-enabled' : 'state-disabled'">{{ group.enabled ? '启用' : '停用' }}</span><button class="btn-ghost" type="button" @click="resetGroupTraffic(group)">流量清零</button><button class="btn-ghost" type="button" @click="toggleGroup(group)">{{ group.enabled ? '停用' : '启用' }}</button><button class="btn-danger" type="button" @click="removeGroup(group)">删除</button></div>
          </div>
          <div v-if="!(user.entry_groups || []).length" class="account-empty">尚未分配入口端口组</div>
        </div>

        <div class="account-list">
          <div class="account-list-title">归属云账户 <span>{{ user.accounts.length }}</span></div>
          <div v-for="account in user.accounts" :key="account.id" class="account-row">
            <span>{{ account.name }}</span>
            <strong>{{ account.traffic_known ? formatGB(account.traffic_used_gb) : '待同步' }}</strong>
          </div>
          <div v-if="!user.accounts.length" class="account-empty">尚未分配云账户</div>
        </div>

        <div class="user-footer">
          <span>{{ user.last_login_at ? '最近登录 ' + formatDate(user.last_login_at) : '尚未登录' }}</span>
          <div>
            <button class="btn-ghost" type="button" @click="openGroupCreate(user)">分配端口组</button>
            <button class="btn-ghost" type="button" @click="openQuota(user)">额度流水</button>
            <button class="btn-ghost" type="button" @click="toggleUser(user)">{{ user.enabled ? '禁用' : '启用' }}</button>
            <button class="btn-ghost" type="button" @click="openEdit(user)">编辑</button>
            <button class="btn-danger" type="button" @click="removeUser(user)">删除</button>
          </div>
        </div>
      </article>
      <div v-if="!store.users.length" class="card empty-panel">还没有用户，点击右上角创建第一个用户。</div>
    </section>

    <Modal v-if="showForm" size="large" @close="showForm = false">
      <form class="space-y-5" @submit.prevent="saveUser">
        <div>
          <div class="eyebrow">CONSOLE USER</div>
          <h2 class="mt-1 text-lg font-bold text-slate-900">{{ editTarget ? '编辑用户' : '创建用户' }}</h2>
          <p class="mt-2 text-xs leading-5 text-slate-500">用户只能查看分配给自己的云账户名称和流量汇总，不能访问管理接口或云密钥。</p>
        </div>
        <div class="form-grid">
          <div><label class="field-label">用户名</label><input v-model.trim="form.username" class="input" minlength="3" maxlength="64" autocomplete="off" required /></div>
          <div><label class="field-label">显示名称</label><input v-model.trim="form.display_name" class="input" maxlength="100" required /></div>
          <div>
            <label class="field-label">{{ editTarget ? '重置密码' : '初始密码' }}</label>
            <input v-model="form.password" type="password" class="input" minlength="8" :required="!editTarget" autocomplete="new-password" :placeholder="editTarget ? '留空表示不修改' : '至少 8 位字符'" />
          </div>
          <div><label class="field-label">用户月流量额度（GB）</label><input v-model.number="form.traffic_limit_gb" type="number" min="0.01" step="0.01" class="input" required /></div>
          <div><label class="field-label">计费方向</label><select v-model="form.billing_mode" class="input"><option value="both">双向合计</option><option value="upload">仅上传（用户 → 落地）</option><option value="download">仅下载（落地 → 用户）</option></select><p class="field-hint">修改方向会开启新的计量周期并清零当前 Agent 计数。</p></div>
          <div class="field-wide setting-toggle-row">
            <div><strong>允许登录</strong><p class="field-hint">禁用或重置密码会立即撤销该用户的全部会话。</p></div>
            <button type="button" class="toggle" :class="form.enabled ? 'toggle-on' : ''" :aria-pressed="form.enabled" @click="form.enabled = !form.enabled"><span></span></button>
          </div>
        </div>

        <div>
          <label class="field-label">分配云账户</label>
          <div class="account-picker">
            <label v-for="account in assignableAccounts" :key="account.id" class="account-option">
              <input v-model="form.account_ids" type="checkbox" :value="account.id" />
              <span><strong>{{ account.name }}</strong><small>{{ account.region_id }} · {{ accountUsage(account.id) }}</small></span>
            </label>
            <div v-if="!assignableAccounts.length" class="account-empty">没有可分配的云账户</div>
          </div>
          <p class="field-hint">一个云账户只能归属一个用户。同一账户下的 ECS 共享阿里云返回的账户级 CDT 用量。</p>
        </div>

        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="form-actions">
          <button class="btn-ghost border border-slate-200" type="button" @click="showForm = false">取消</button>
          <button class="btn-primary" :disabled="saving">{{ saving ? '保存中...' : '保存用户' }}</button>
        </div>
      </form>
    </Modal>

    <Modal v-if="showGroupForm" size="large" @close="showGroupForm = false">
      <form class="space-y-5" @submit.prevent="saveGroup">
        <div><div class="eyebrow">USER ENTRY GROUP</div><h2 class="mt-1 text-lg font-bold text-slate-900">为 {{ groupUser?.display_name }} 分配入口端口组</h2><p class="mt-2 text-xs leading-5 text-slate-500">默认自动分配连续 10 个端口，同一用户的全部端口共享一份月流量额度。</p></div>
        <div class="form-grid">
          <div><label class="field-label">端口组名称</label><input v-model.trim="groupForm.name" class="input" placeholder="例如：Alice 主入口" required /></div>
          <div><label class="field-label">中转节点</label><select v-model="groupForm.relay_node_id" class="input" required><option v-for="node in availableRelayNodes" :key="node.id" :value="node.id" :disabled="groupForm.port_count > 1 && !node.capabilities?.includes('shared_meters_v1')">{{ node.name }} · {{ node.public_ip || '未上报 IP' }}{{ node.capabilities?.includes('shared_meters_v1') ? '' : ' · Agent 待升级' }}</option></select></div>
          <div><label class="field-label">起始端口（0 = 自动）</label><input v-model.number="groupForm.start_port" type="number" min="0" max="59999" class="input" /></div>
          <div><label class="field-label">端口数量（1–100）</label><input v-model.number="groupForm.port_count" type="number" min="1" max="100" class="input" required /></div>
          <div><label class="field-label">监听协议</label><select v-model="groupForm.network" class="input"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP + UDP</option></select></div>
          <div><label class="field-label">计费方向（继承用户）</label><select v-model="groupForm.billing_mode" class="input" disabled><option value="both">双向合计</option><option value="upload">仅上传</option><option value="download">仅下载</option></select></div>
        </div>
        <div><label class="field-label">落地目标（所有端口共用）</label><div class="account-picker"><label v-for="node in store.landingNodes" :key="node.id" class="account-option"><input v-model="groupTargets" type="checkbox" :value="node.id" /><span><strong>{{ node.name }}</strong><small>{{ node.address }}:{{ node.port }}</small></span></label><div v-if="!store.landingNodes.length" class="account-empty">请先创建落地节点</div></div></div>
        <div v-if="groupError" class="notice notice-error">{{ groupError }}</div>
        <div class="form-actions"><button class="btn-ghost border border-slate-200" type="button" @click="showGroupForm = false">取消</button><button class="btn-primary" :disabled="groupSaving">{{ groupSaving ? '分配中...' : '分配并发布' }}</button></div>
      </form>
    </Modal>

    <Modal v-if="showQuotaForm" size="large" @close="showQuotaForm = false">
      <div class="space-y-5">
        <div><div class="eyebrow">USAGE LEDGER</div><h2 class="mt-1 text-lg font-bold text-slate-900">{{ quotaUser?.display_name }} · 额度与用量流水</h2><p class="mt-2 text-xs leading-5 text-slate-500">正数为追加额度，负数为扣减额度；每次调整必须填写审计备注。</p></div>
        <form class="quota-form" @submit.prevent="adjustQuota"><div><label class="field-label">调整额度（GB）</label><input v-model.number="quotaForm.delta_gb" type="number" step="0.01" class="input" placeholder="例如 50 或 -20" required /></div><div><label class="field-label">审计备注</label><input v-model.trim="quotaForm.note" class="input" maxlength="200" placeholder="例如：续费追加 50 GB" required /></div><button class="btn-primary" :disabled="quotaSaving">{{ quotaSaving ? '提交中...' : '确认调整' }}</button></form>
        <div v-if="quotaError" class="notice notice-error">{{ quotaError }}</div>
        <div class="ledger-list"><div v-for="entry in usageLedger" :key="entry.id" class="ledger-row"><div><strong>{{ ledgerKind(entry.kind) }}</strong><small>{{ formatDate(entry.created_at) }} · {{ entry.note || entry.source }}</small></div><span :class="entry.delta_bytes < 0 ? 'ledger-minus' : 'ledger-plus'">{{ formatLedgerDelta(entry) }}</span></div><div v-if="!usageLedger.length" class="account-empty">暂无流水，Agent 上报流量或调整额度后会自动记录。</div></div>
      </div>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import { formatDateTime } from '../utils/time'
import Modal from '../components/Modal.vue'

const store = useRelayStore()
const showForm = ref(false)
const showGroupForm = ref(false)
const showQuotaForm = ref(false)
const editTarget = ref(null)
const saving = ref(false)
const formError = ref('')
const groupError = ref('')
const groupSaving = ref(false)
const groupUser = ref(null)
const groupTargets = ref([])
const availableRelayNodes = computed(() => store.relayNodes)
const groupForm = ref({ user_id: 0, relay_node_id: '', name: '', start_port: 0, port_count: 10, network: 'tcp', mode: 'failover', enabled: true, billing_mode: 'both' })
const quotaUser = ref(null)
const quotaForm = ref({ delta_gb: 0, note: '' })
const quotaSaving = ref(false)
const quotaError = ref('')
const usageLedger = ref([])
const message = ref({ type: 'success', text: '' })
let messageTimer

const blank = () => ({ username: '', display_name: '', password: '', enabled: true, traffic_limit_gb: 200, billing_mode: 'both', account_ids: [] })
const form = ref(blank())
const enabledCount = computed(() => store.users.filter(user => user.enabled).length)
const assignedAccountCount = computed(() => store.cloud.accounts.filter(account => account.user_id).length)
const unassignedAccountCount = computed(() => store.cloud.accounts.length - assignedAccountCount.value)
const totalKnownUsage = computed(() => store.users.reduce((sum, user) => sum + Number(user.traffic_used_gb || 0), 0))
const assignableAccounts = computed(() => store.cloud.accounts.filter(account => !account.user_id || account.user_id === editTarget.value?.id))

function formatGB(value) {
  return `${Number(value || 0).toFixed(2)} GB`
}

function formatByteGB(value) { return `${(Number(value || 0) / 1073741824).toFixed(3)} GB` }

function formatPercent(value) {
  return `${Number(value || 0).toFixed(1)}%`
}

function billingModeLabel(value) {
  return { download: '仅下载计费', upload: '仅上传计费', both: '双向计费' }[value] || '双向计费'
}

function formatDate(value) {
  return formatDateTime(value)
}

function accountUsage(accountID) {
  const snapshot = store.cloud.traffic.find(item => item.account_id === accountID)
  return snapshot ? formatGB(snapshot.used_gb) : '待同步'
}

function openCreate() {
  editTarget.value = null
  form.value = blank()
  formError.value = ''
  showForm.value = true
}

function openEdit(user) {
  editTarget.value = user
  form.value = {
    username: user.username,
    display_name: user.display_name,
    password: '',
    enabled: user.enabled,
    traffic_limit_gb: user.traffic_limit_gb,
    billing_mode: user.billing_mode || 'both',
    account_ids: user.accounts.map(account => account.id),
  }
  formError.value = ''
  showForm.value = true
}

function userPayload(user, changes = {}) {
  return {
    username: user.username,
    display_name: user.display_name,
    password: '',
    enabled: user.enabled,
    traffic_limit_gb: user.traffic_limit_gb,
    billing_mode: user.billing_mode || 'both',
    account_ids: user.accounts.map(account => account.id),
    ...changes,
  }
}

async function saveUser() {
  saving.value = true
  formError.value = ''
  try {
    if (editTarget.value) await store.updateUser(editTarget.value.id, form.value)
    else await store.createUser(form.value)
    showForm.value = false
    showMessage('用户已保存')
  } catch (error) {
    formError.value = error.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

function openGroupCreate(user) {
  groupUser.value = user
  const capableNode = availableRelayNodes.value.find(node => node.capabilities?.includes('shared_meters_v1'))
  groupForm.value = { user_id: user.id, relay_node_id: capableNode?.id || '', name: `${user.display_name} 入口`, start_port: 0, port_count: 10, network: 'tcp', mode: 'failover', enabled: true, billing_mode: user.billing_mode || 'both' }
  groupTargets.value = store.landingNodes.filter(node => node.enabled).slice(0, 1).map(node => node.id)
  groupError.value = ''
  showGroupForm.value = true
}

async function saveGroup() {
  if (!groupTargets.value.length) { groupError.value = '至少选择一个落地节点'; return }
  groupSaving.value = true
  groupError.value = ''
  try {
    await store.createEntryGroup({ ...groupForm.value, targets: groupTargets.value.map((id, index) => ({ landing_node_id: id, priority: index * 10, weight: 1, enabled: true })), dial_timeout_ms: 2500, udp_idle_timeout_seconds: 60, health: { enabled: true, interval_seconds: 4, timeout_ms: 2000, failure_threshold: 2, success_threshold: 3, recovery_cooldown_seconds: 60 } })
    showGroupForm.value = false
    showMessage('入口端口组已分配，Agent 将自动发布')
  } catch (error) { groupError.value = error.response?.data?.error || '入口组创建失败' } finally { groupSaving.value = false }
}

async function toggleGroup(group) {
  try { await store.updateEntryGroup(group.id, { name: group.name, enabled: !group.enabled }); showMessage(group.enabled ? '入口组已停用' : '入口组已启用') } catch (error) { showMessage(error.response?.data?.error || '入口组状态更新失败', 'error') }
}

async function resetGroupTraffic(group) {
  const serviceID = group.ports?.[0]?.service_id
  if (!serviceID || !window.confirm(`确认清零入口组“${group.name}”所属用户的共享月流量？其全部入口端口都会同时清零。`)) return
  try { await store.resetServiceTraffic(serviceID); showMessage('该用户全部端口的共享流量已清零') } catch (error) { showMessage(error.response?.data?.error || '流量清零失败', 'error') }
}

async function openQuota(user) {
  quotaUser.value = user
  quotaForm.value = { delta_gb: 0, note: '' }
  quotaError.value = ''
  showQuotaForm.value = true
  try { usageLedger.value = await store.fetchUserUsageLedger(user.id) } catch (error) { quotaError.value = error.response?.data?.error || '流水读取失败' }
}

async function adjustQuota() {
  if (!quotaUser.value || !Number(quotaForm.value.delta_gb)) { quotaError.value = '调整额度不能为 0'; return }
  quotaSaving.value = true
  quotaError.value = ''
  try { await store.adjustUserQuota(quotaUser.value.id, quotaForm.value); usageLedger.value = await store.fetchUserUsageLedger(quotaUser.value.id); quotaForm.value = { delta_gb: 0, note: '' }; showMessage('额度已调整并写入审计流水') } catch (error) { quotaError.value = error.response?.data?.error || '额度调整失败' } finally { quotaSaving.value = false }
}

function ledgerKind(kind) { return { usage: 'Agent 流量', quota_adjustment: '额度增减', quota_set: '额度设置', reset: '流量清零', billing_mode_change: '计费方向变更' }[kind] || kind }
function formatLedgerDelta(entry) { const value = Number(entry.delta_bytes || 0) / 1073741824; if (entry.kind === 'reset' || entry.kind === 'billing_mode_change') return '—'; return `${value > 0 ? '+' : ''}${value.toFixed(3)} GB` }

async function removeGroup(group) {
  if (!window.confirm(`确认删除入口组“${group.name}”？端口会隔离 30 分钟，防止旧客户端误连新用户。`)) return
  try { await store.deleteEntryGroup(group.id); showMessage('入口组已删除，端口进入隔离期') } catch (error) { showMessage(error.response?.data?.error || '入口组删除失败', 'error') }
}

async function toggleUser(user) {
  const enabled = !user.enabled
  if (!enabled && !window.confirm(`禁用“${user.display_name}”后，其现有登录会话会立即失效。确认继续？`)) return
  try {
    await store.updateUser(user.id, userPayload(user, { enabled }))
    showMessage(enabled ? '用户已启用' : '用户已禁用')
  } catch (error) {
    showMessage(error.response?.data?.error || '状态更新失败', 'error')
  }
}

async function removeUser(user) {
  if (!window.confirm(`确认删除用户“${user.display_name}”？云账户和流量数据不会被删除。`)) return
  try {
    await store.deleteUser(user.id)
    showMessage('用户已删除')
  } catch (error) {
    showMessage(error.response?.data?.error || '删除失败', 'error')
  }
}

function showMessage(text, type = 'success') {
  message.value = { type, text }
  window.clearTimeout(messageTimer)
  messageTimer = window.setTimeout(() => { message.value.text = '' }, 5000)
}

onMounted(() => Promise.all([store.fetchUsers(), store.fetchCloud(), store.fetchRelayNodes(), store.fetchLandingNodes()]))
</script>

<style scoped>
.users-page { display: grid; gap: 20px; }
.page-header { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 5px; color: #172033; font-size: 28px; font-weight: 750; letter-spacing: -.035em; }
.page-subtitle { margin-top: 5px; color: #64748b; font-size: 12px; }
.summary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.summary-card { padding: 17px 19px; }
.summary-card > span { color: #64748b; font-size: 10px; font-weight: 700; }
.summary-card strong { display: block; margin-top: 8px; color: #172033; font-size: 25px; }
.summary-card small { display: block; margin-top: 5px; color: #94a3b8; font-size: 10px; }
.user-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.user-card { padding: 19px; }
.user-disabled { background: #fafafa; opacity: .82; }
.user-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.user-head h2 { overflow: hidden; color: #1e293b; font-size: 15px; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.user-head p { margin-top: 4px; color: #94a3b8; font-family: ui-monospace, monospace; font-size: 10px; }
.state-tag { border-radius: 999px; padding: 5px 9px; font-size: 9px; font-weight: 750; }
.state-enabled { background: #ecfdf3; color: #15803d; }
.state-disabled { background: #f1f5f9; color: #64748b; }
.usage-line { display: flex; justify-content: space-between; gap: 20px; margin-top: 22px; }
.usage-line small, .usage-line strong { display: block; }
.usage-line small { color: #94a3b8; font-size: 9px; }
.usage-line strong { margin-top: 4px; color: #172033; font-size: 20px; }
.usage-limit { text-align: right; }
.usage-limit strong { color: #475569; font-size: 14px; }
.traffic-track { height: 7px; margin-top: 13px; overflow: hidden; border-radius: 999px; background: #e7edf5; }
.traffic-fill { height: 100%; border-radius: inherit; background: #2563eb; }
.traffic-danger { background: #dc2626; }
.traffic-meta { display: flex; justify-content: space-between; margin-top: 7px; color: #94a3b8; font-size: 9px; }
.account-list { margin-top: 16px; border: 1px solid #eef2f7; border-radius: 10px; }
.relay-binding { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 14px; border: 1px solid #dbeafe; border-radius: 10px; background: #f8fbff; padding: 10px 11px; }
.relay-binding span, .relay-binding strong, .relay-binding small { display: block; }
.relay-binding > div > span, .relay-binding small { color: #94a3b8; font-size: 9px; }
.relay-binding strong { margin-top: 3px; color: #334155; font-size: 11px; }
.relay-binding small { margin-top: 3px; }
.entry-groups { margin-top: 14px; overflow: hidden; border: 1px solid #dbeafe; border-radius: 10px; }
.entry-group-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid #eff6ff; padding: 10px 11px; }
.entry-group-row strong, .entry-group-row small { display: block; }
.entry-group-row strong { color: #334155; font-size: 11px; }
.entry-group-row small { margin-top: 3px; color: #64748b; font-size: 9px; }
.entry-group-row .port-list { max-width: 360px; overflow: hidden; color: #94a3b8; text-overflow: ellipsis; white-space: nowrap; }
.entry-group-actions { display: flex; flex: 0 0 auto; align-items: center; gap: 3px; }
.entry-group-actions button { padding: 4px 6px; font-size: 9px; }
.quota-form { display: grid; grid-template-columns: 140px minmax(0, 1fr) auto; align-items: end; gap: 10px; }
.ledger-list { max-height: 340px; overflow-y: auto; border: 1px solid #e5eaf1; border-radius: 10px; }
.ledger-row { display: flex; align-items: center; justify-content: space-between; gap: 14px; border-bottom: 1px solid #f1f5f9; padding: 11px 13px; }
.ledger-row:last-child { border-bottom: 0; }
.ledger-row strong, .ledger-row small { display: block; }
.ledger-row strong { color: #334155; font-size: 11px; }
.ledger-row small { margin-top: 3px; color: #94a3b8; font-size: 9px; }
.ledger-row > span { font-family: ui-monospace, monospace; font-size: 10px; font-weight: 700; }
.ledger-plus { color: #15803d; }
.ledger-minus { color: #dc2626; }
.account-list-title, .account-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 9px 11px; }
.account-list-title { background: #f8fafc; color: #64748b; font-size: 9px; font-weight: 700; }
.account-list-title span { color: #2563eb; }
.account-row { border-top: 1px solid #f1f5f9; color: #64748b; font-size: 10px; }
.account-row strong { color: #475569; }
.account-empty { padding: 16px; color: #94a3b8; font-size: 10px; text-align: center; }
.user-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 14px; border-top: 1px solid #f1f5f9; padding-top: 11px; color: #94a3b8; font-size: 9px; }
.user-footer > div { display: flex; gap: 3px; }
.user-footer button { padding: 5px 8px; font-size: 10px; }
.empty-panel { grid-column: 1 / -1; padding: 48px; color: #94a3b8; font-size: 12px; text-align: center; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.field-wide { grid-column: 1 / -1; }
.field-hint { margin-top: 6px; color: #94a3b8; font-size: 10px; line-height: 1.55; }
.account-picker { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); max-height: 230px; overflow-y: auto; border: 1px solid #e5eaf1; border-radius: 10px; }
.account-option { display: flex; align-items: center; gap: 10px; padding: 12px; border-bottom: 1px solid #f1f5f9; cursor: pointer; }
.account-option:nth-child(odd) { border-right: 1px solid #f1f5f9; }
.account-option input { accent-color: #2563eb; }
.account-option span, .account-option strong, .account-option small { display: block; min-width: 0; }
.account-option strong { overflow: hidden; color: #334155; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.account-option small { margin-top: 3px; color: #94a3b8; font-size: 9px; }
.form-actions { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid #f1f5f9; padding-top: 16px; }
@media (max-width: 850px) { .user-grid { grid-template-columns: 1fr; } }
@media (max-width: 640px) { .page-header { align-items: stretch; flex-direction: column; } .summary-grid, .form-grid, .account-picker, .quota-form { grid-template-columns: 1fr; } .account-option:nth-child(odd) { border-right: 0; } .user-footer, .entry-group-row { align-items: flex-start; flex-direction: column; } }
</style>

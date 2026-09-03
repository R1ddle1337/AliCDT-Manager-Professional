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

        <div v-if="user.relay_service" class="relay-binding">
          <div><span>独立入口</span><strong>{{ user.relay_service.name }}</strong><small>{{ user.relay_service.relay_node_name }} · {{ billingModeLabel(user.relay_service.billing_mode) }}</small></div>
          <span class="state-tag" :class="user.relay_service.quota_exceeded ? 'state-disabled' : 'state-enabled'">{{ user.relay_service.quota_exceeded ? '额度耗尽' : (user.relay_service.reported ? 'Agent 已上报' : '等待 Agent') }}</span>
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
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import { formatDateTime } from '../utils/time'
import Modal from '../components/Modal.vue'

const store = useRelayStore()
const showForm = ref(false)
const editTarget = ref(null)
const saving = ref(false)
const formError = ref('')
const message = ref({ type: 'success', text: '' })
let messageTimer

const blank = () => ({ username: '', display_name: '', password: '', enabled: true, traffic_limit_gb: 200, account_ids: [] })
const form = ref(blank())
const enabledCount = computed(() => store.users.filter(user => user.enabled).length)
const assignedAccountCount = computed(() => store.cloud.accounts.filter(account => account.user_id).length)
const unassignedAccountCount = computed(() => store.cloud.accounts.length - assignedAccountCount.value)
const totalKnownUsage = computed(() => store.users.reduce((sum, user) => sum + Number(user.traffic_used_gb || 0), 0))
const assignableAccounts = computed(() => store.cloud.accounts.filter(account => !account.user_id || account.user_id === editTarget.value?.id))

function formatGB(value) {
  return `${Number(value || 0).toFixed(2)} GB`
}

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

onMounted(() => Promise.all([store.fetchUsers(), store.fetchCloud()]))
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
@media (max-width: 640px) { .page-header { align-items: stretch; flex-direction: column; } .summary-grid, .form-grid, .account-picker { grid-template-columns: 1fr; } .account-option:nth-child(odd) { border-right: 0; } .user-footer { align-items: flex-start; flex-direction: column; } }
</style>

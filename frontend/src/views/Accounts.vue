<template>
  <div class="space-y-6 fade-in">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <div class="eyebrow">WORKSPACE</div>
        <h1 class="page-title">账户管理</h1>
        <p class="page-subtitle">管理访问凭据、地域和自动化策略</p>
      </div>
      <button type="button" @click="openAdd" class="btn-primary">添加账户</button>
    </div>

    <div v-if="saveMessage" class="notice notice-info">{{ saveMessage }}</div>

    <div v-if="store.accounts.length === 0" class="card empty-state">
      <div class="empty-mark">AC</div>
      <div class="mt-4 font-semibold text-slate-800">还没有账户</div>
      <p class="mt-1 text-sm text-text-muted">添加一个阿里云账户开始同步实例数据。</p>
      <button type="button" @click="openAdd" class="btn-primary mt-5">添加第一个账户</button>
    </div>

    <div v-else class="grid grid-cols-1 gap-4 xl:grid-cols-2">
      <article v-for="acc in store.accounts" :key="acc.id" class="card account-card">
        <div class="flex items-start justify-between gap-4">
          <div class="flex min-w-0 items-center gap-3">
            <div class="account-mark">{{ initials(acc.name) }}</div>
            <div class="min-w-0">
              <h2 class="truncate font-semibold text-slate-800">{{ acc.name }}</h2>
              <div class="mt-1 truncate font-mono text-xs text-slate-400">{{ acc.access_key_id }}</div>
            </div>
          </div>
          <span class="region-badge">{{ acc.region_id }}</span>
        </div>

        <div class="mt-5 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <div class="metric-box"><span>站点</span><strong>{{ acc.site_type === 'international' ? '国际站' : '中国站' }}</strong></div>
          <div class="metric-box"><span>流量上限</span><strong>{{ acc.traffic_limit_gb }} GB</strong></div>
          <div class="metric-box"><span>熔断阈值</span><strong>{{ acc.threshold_percent }}%</strong></div>
          <div class="metric-box"><span>实例</span><strong>{{ acc.instance_id || '未指定' }}</strong></div>
        </div>

        <div class="mt-4 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-4 text-xs">
          <span class="tag" :class="acc.keep_alive ? 'tag-blue' : 'tag-muted'">{{ acc.keep_alive ? '自动保活' : '保活关闭' }}</span>
          <span class="tag tag-muted">{{ acc.shutdown_mode === 'StopCharging' ? '节省停机' : '普通停机' }}</span>
          <span v-if="acc.auto_stop_time" class="tag tag-muted">{{ acc.auto_stop_time }} 关机</span>
          <span v-if="acc.auto_start_time" class="tag tag-muted">{{ acc.auto_start_time }} 开机</span>
          <span class="tag" :class="acc.agent_installed ? 'tag-blue' : 'tag-muted'">
            {{ acc.agent_installed ? `Agent 已安装 · 在线 ${acc.online_agent_count || 0}/${acc.agent_count || 0}` : 'Agent 未安装' }}
          </span>
            <span class="ml-auto flex flex-wrap justify-end gap-1">
              <button type="button" @click="openAgentInstall(acc)" class="btn-ghost border border-blue-100 px-2.5 py-1.5 text-xs text-accent">{{ acc.agent_installed ? '添加 Agent' : '安装 Agent' }}</button>
              <button type="button" @click="openEdit(acc)" class="btn-ghost px-2.5 py-1.5 text-xs">编辑</button>
              <button type="button" @click="confirmDelete(acc)" class="btn-danger px-2.5 py-1.5 text-xs">删除</button>
          </span>
        </div>
      </article>
    </div>

    <Modal v-if="showForm" @close="showForm = false">
      <form class="space-y-5" @submit.prevent="submit">
        <div class="flex items-start justify-between">
          <div>
            <div class="eyebrow">ACCOUNT</div>
            <h2 class="mt-1 text-lg font-bold text-slate-900">{{ editTarget ? '编辑账户' : '添加账户' }}</h2>
          </div>
          <button type="button" class="btn-ghost px-2 text-lg leading-none" aria-label="关闭" @click="showForm = false">×</button>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2"><label class="field-label" for="account-name">备注名 <span class="text-danger">*</span></label><input id="account-name" v-model="form.name" class="input" placeholder="例如：生产环境" /></div>
          <div><label class="field-label" for="access-key-id">CDT API Key ID <span class="text-danger">*</span></label><input id="access-key-id" v-model="form.access_key_id" class="input" autocomplete="off" placeholder="LTAI..." /></div>
          <div><label class="field-label" for="access-key-secret">CDT API Key Secret <span v-if="!editTarget" class="text-danger">*</span></label><input id="access-key-secret" v-model="form.access_key_secret" type="password" class="input" autocomplete="new-password" :placeholder="editTarget ? '留空表示不修改' : '请输入密钥'" /></div>
          <div><label class="field-label" for="region-id">地域 ID <span class="text-danger">*</span></label><input id="region-id" v-model="form.region_id" class="input" placeholder="ap-southeast-1" /></div>
          <div><label class="field-label" for="site-type">站点类型</label><select id="site-type" v-model="form.site_type" class="input"><option value="international">国际站</option><option value="china">中国站</option></select></div>
          <div class="sm:col-span-2"><label class="field-label" for="instance-id">实例 ID <span class="font-normal text-slate-400">（用于保活与定时任务，可选）</span></label><input id="instance-id" v-model="form.instance_id" class="input" placeholder="i-..." /></div>
          <div><label class="field-label" for="traffic-limit">流量上限（GB）</label><input id="traffic-limit" v-model.number="form.traffic_limit_gb" type="number" min="0" class="input" /></div>
          <div><label class="field-label" for="threshold">流量熔断阈值（%）</label><input id="threshold" v-model.number="form.threshold_percent" type="number" min="0" max="100" class="input" /></div>
          <div v-if="form.site_type !== 'china'" class="sm:col-span-2"><label class="field-label" for="outstanding">待还金额熔断阈值 <span class="font-normal text-slate-400">（0 表示不启用）</span></label><input id="outstanding" v-model.number="form.outstanding_threshold" type="number" min="0" step="0.01" class="input" /></div>
          <div v-else class="notice notice-info sm:col-span-2">国内站账单功能暂未启用，保存后不会请求 BSS 账单接口。</div>
          <div class="sm:col-span-2"><label class="field-label" for="shutdown-mode">停机模式</label><select id="shutdown-mode" v-model="form.shutdown_mode" class="input"><option value="StopCharging">节省停机（停止计费）</option><option value="KeepCharging">普通停机（继续计费）</option></select></div>
        </div>

        <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
          <label class="flex cursor-pointer items-center justify-between gap-3">
            <div><div class="text-sm font-semibold text-slate-700">自动保活</div><div class="mt-1 text-xs text-slate-500">实例被回收后自动尝试重新启动</div></div>
            <span class="toggle" :class="form.keep_alive ? 'toggle-on' : ''"><input v-model="form.keep_alive" type="checkbox" class="sr-only" /><span></span></span>
          </label>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div><label class="field-label" for="auto-stop">定时关机</label><input id="auto-stop" v-model="form.auto_stop_time" type="time" class="input" /></div>
          <div><label class="field-label" for="auto-start">定时开机</label><input id="auto-start" v-model="form.auto_start_time" type="time" class="input" /></div>
        </div>

        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="flex gap-3 border-t border-slate-100 pt-4"><button type="button" @click="showForm = false" class="btn-ghost flex-1 border border-slate-200">取消</button><button type="submit" :disabled="submitting" class="btn-primary flex-1"><span v-if="submitting" class="mr-2 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/35 border-t-white"></span>{{ submitting ? '保存中...' : '保存账户' }}</button></div>
      </form>
    </Modal>

    <Modal v-if="deleteTarget" @close="deleteTarget = null">
      <div class="space-y-5">
        <div><div class="eyebrow text-danger">REMOVE ACCOUNT</div><h2 class="mt-1 text-lg font-bold text-slate-900">确认删除账户？</h2><p class="mt-2 text-sm text-slate-500">{{ deleteTarget.name }}</p></div>
        <div class="notice notice-error">删除账户后，关联的实例记录也会被清除。该操作不可撤销。</div>
        <div class="flex gap-3"><button type="button" @click="deleteTarget = null" class="btn-ghost flex-1 border border-slate-200">取消</button><button type="button" @click="doDelete" class="btn-danger flex-1">确认删除</button></div>
      </div>
    </Modal>

    <Modal v-if="installAccount" @close="closeAgentInstall">
      <div class="space-y-5">
        <div class="flex items-start justify-between gap-4">
          <div><div class="eyebrow">RELAY AGENT</div><h2 class="mt-1 text-lg font-bold text-slate-900">为 {{ installAccount.name }} 安装 Agent</h2><p class="mt-2 text-sm leading-6 text-slate-500">在目标 CDT 机器上使用 root SSH 登录后，执行下面的一键命令。注册码 30 分钟内有效且只能使用一次。</p></div>
          <button type="button" class="btn-ghost px-2 text-lg leading-none" aria-label="关闭" @click="closeAgentInstall">×</button>
        </div>
        <div v-if="installLoading" class="notice notice-info">正在生成一次性安装命令...</div>
        <div v-if="installError" class="notice notice-error">{{ installError }}</div>
        <div v-if="installCommand" class="space-y-3">
          <div class="flex items-center justify-between gap-3"><span class="text-xs font-semibold text-slate-600">root SSH 执行命令</span><button type="button" class="btn-ghost border border-slate-200 text-xs" @click="copyAgentCommand">复制命令</button></div>
          <pre class="command-box">{{ installCommand }}</pre>
          <p class="text-xs leading-5 text-slate-400">安装完成后，Agent 会通过 HTTPS 回连控制台；面板不会获取或保存目标服务器的 SSH 密码、私钥。</p>
        </div>
      </div>
    </Modal>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useStore } from '../stores'
import { useRelayStore } from '../stores/relay'
import Modal from '../components/Modal.vue'

const store = useStore()
const relayStore = useRelayStore()
const showForm = ref(false)
const editTarget = ref(null)
const deleteTarget = ref(null)
const submitting = ref(false)
const formError = ref('')
const saveMessage = ref('')
const installAccount = ref(null)
const installCommand = ref('')
const installLoading = ref(false)
const installError = ref('')

const defaultForm = () => ({ name: '', access_key_id: '', access_key_secret: '', region_id: 'ap-southeast-1', site_type: 'international', instance_id: '', traffic_limit_gb: 200, threshold_percent: 95, outstanding_threshold: 0, shutdown_mode: 'StopCharging', keep_alive: false, auto_stop_time: null, auto_start_time: null })
const form = ref(defaultForm())

onMounted(() => store.fetchAccounts())

function initials(name) {
  return (name || 'AC').trim().slice(0, 2).toUpperCase()
}

function openAdd() {
  editTarget.value = null
  form.value = defaultForm()
  formError.value = ''
  showForm.value = true
}

function openEdit(acc) {
  editTarget.value = acc
  form.value = {
    name: acc.name || '', access_key_id: acc.access_key_id || '', access_key_secret: '',
    region_id: acc.region_id || '', site_type: acc.site_type || 'international', instance_id: acc.instance_id || '',
    traffic_limit_gb: acc.traffic_limit_gb || 200, threshold_percent: acc.threshold_percent || 95,
    outstanding_threshold: acc.outstanding_threshold || 0, shutdown_mode: acc.shutdown_mode || 'StopCharging',
    keep_alive: !!acc.keep_alive, auto_stop_time: acc.auto_stop_time || '', auto_start_time: acc.auto_start_time || '',
  }
  formError.value = ''
  showForm.value = true
}

async function submit() {
  formError.value = ''
  if (!form.value.name || !form.value.access_key_id || !form.value.region_id) { formError.value = '请填写备注名、AccessKey ID 和地域 ID'; return }
  if (!editTarget.value && !form.value.access_key_secret) { formError.value = '请填写 AccessKey Secret'; return }
  submitting.value = true
  try {
    const payload = { ...form.value }
    if (payload.site_type === 'china') payload.outstanding_threshold = 0
    if (editTarget.value) {
      await store.updateAccount(editTarget.value.id, payload)
      saveMessage.value = '账户已更新。'
    } else {
      await store.createAccount(payload)
      saveMessage.value = '账户已保存，实例和流量数据正在后台同步。'
    }
    showForm.value = false
    window.setTimeout(() => { saveMessage.value = '' }, 8000)
  } catch (e) {
    formError.value = e.response?.data?.error || e.response?.data?.detail || '保存失败，请检查网络和账户参数'
  } finally {
    submitting.value = false
  }
}

function confirmDelete(acc) { deleteTarget.value = acc }
function shellQuote(value) {
  const clean = String(value || '').replace(/[\r\n\t]+/g, ' ').trim()
  return `'${clean.replace(/'/g, `'\\''`)}'`
}
async function openAgentInstall(acc) {
  installAccount.value = acc
  installCommand.value = ''
  installError.value = ''
  installLoading.value = true
  try {
    const data = await relayStore.createEnrollmentToken(30, acc.id)
    const nodeName = acc.name ? `cdt-${acc.name}` : 'cdt-relay-agent'
    installCommand.value = `curl -fsSL https://${window.location.host}/agent/install.sh | sh -s -- --server https://${window.location.host} --token ${shellQuote(data.token)} --node-name ${shellQuote(nodeName)}`
  } catch (e) {
    installError.value = e.response?.data?.error || '生成安装命令失败，请稍后重试'
  } finally {
    installLoading.value = false
  }
}
function closeAgentInstall() { installAccount.value = null; installCommand.value = ''; installError.value = '' }
async function copyAgentCommand() { if (installCommand.value) await navigator.clipboard.writeText(installCommand.value) }
async function doDelete() {
  try { await store.deleteAccount(deleteTarget.value.id); deleteTarget.value = null } catch (e) { saveMessage.value = e.response?.data?.error || e.response?.data?.detail || '删除失败' }
}
</script>

<style scoped>
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 6px; color: #172033; font-size: 26px; font-weight: 750; letter-spacing: -.03em; }
.page-subtitle { margin-top: 6px; color: #64748b; font-size: 13px; }
.empty-state { padding: 56px 24px; text-align: center; }
.empty-mark, .account-mark { display: inline-flex; align-items: center; justify-content: center; border-radius: 11px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 800; letter-spacing: .06em; }
.empty-mark { width: 48px; height: 48px; margin: 0 auto; }
.account-card { padding: 20px; transition: border-color .16s ease, box-shadow .16s ease; }
.account-card:hover { border-color: #bfdbfe; box-shadow: 0 10px 28px rgba(30, 64, 175, .08); }
.account-mark { width: 40px; height: 40px; }
.region-badge { max-width: 150px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; border-radius: 999px; background: #f8fafc; padding: 5px 9px; color: #64748b; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; }
.metric-box { min-width: 0; border-radius: 8px; background: #f8fafc; padding: 9px 10px; }
.metric-box span { display: block; color: #94a3b8; font-size: 10px; }
.metric-box strong { display: block; overflow: hidden; margin-top: 4px; color: #334155; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; font-weight: 650; }
.tag { border-radius: 999px; padding: 4px 8px; font-size: 10px; font-weight: 650; }
.tag-blue { background: #eff6ff; color: #2563eb; }
.tag-muted { background: #f1f5f9; color: #64748b; }
.command-box { overflow: auto; border-radius: 9px; background: #0f172a; padding: 14px; color: #dbeafe; font-size: 11px; line-height: 1.7; white-space: pre-wrap; word-break: break-word; }
.toggle { position: relative; display: inline-flex; width: 40px; height: 23px; flex: 0 0 auto; border-radius: 999px; background: #cbd5e1; transition: background .15s ease; }
.toggle span { position: absolute; top: 3px; left: 3px; width: 17px; height: 17px; border-radius: 50%; background: #fff; box-shadow: 0 1px 3px rgba(15,23,42,.2); transition: transform .15s ease; }
.toggle-on { background: #2563eb; }
.toggle-on span { transform: translateX(17px); }
</style>

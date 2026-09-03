<template>
  <div class="dns-page fade-in">
    <header class="page-header">
      <div>
        <div class="eyebrow">DNS CONTROL PLANE</div>
        <h1 class="page-title">DNS 托管</h1>
        <p class="page-subtitle">集中管理域名服务商和 Relay 入口记录，配置、状态与异常各归其位。</p>
      </div>
      <div class="header-actions">
        <button
          type="button"
          class="btn-ghost border border-slate-200"
          :disabled="syncing || !store.dnsProviders.length"
          @click="syncAll"
        >
          {{ syncing ? '正在同步...' : '同步全部记录' }}
        </button>
        <button type="button" class="btn-primary" @click="openPrimaryCreate">
          {{ activeTab === 'providers' ? '添加 Provider' : '添加托管记录' }}
        </button>
      </div>
    </header>

    <div v-if="message.text" class="notice" :class="`notice-${message.type}`">
      {{ message.text }}
    </div>

    <section class="summary-grid" aria-label="DNS 健康摘要">
      <article class="card summary-card summary-card-blue">
        <div class="summary-icon">P</div>
        <div>
          <span>DNS Provider</span>
          <strong>{{ store.dnsProviders.length }}</strong>
          <small>{{ enabledProviderCount }} 个正在运行</small>
        </div>
      </article>
      <article class="card summary-card summary-card-indigo">
        <div class="summary-icon">R</div>
        <div>
          <span>托管记录</span>
          <strong>{{ store.dnsRecords.length }}</strong>
          <small>仅管理本页声明的记录</small>
        </div>
      </article>
      <article class="card summary-card" :class="failedRecordCount ? 'summary-card-red' : 'summary-card-green'">
        <div class="summary-icon">S</div>
        <div>
          <span>同步状态</span>
          <strong>{{ syncedRecordCount }} / {{ store.dnsRecords.length }}</strong>
          <small v-if="failedRecordCount" class="summary-error">{{ failedRecordCount }} 条同步失败</small>
          <small v-else>{{ pendingRecordCount }} 条等待处理</small>
        </div>
      </article>
      <article class="card summary-card summary-card-cyan">
        <div class="summary-icon">A</div>
        <div>
          <span>Relay Agent</span>
          <strong>{{ onlineRelayCount }} / {{ store.relayNodes.length }}</strong>
          <small>在线节点可自动跟随 IP</small>
        </div>
      </article>
    </section>

    <nav class="workspace-tabs" aria-label="DNS 管理视图">
      <button
        type="button"
        :class="{ active: activeTab === 'providers' }"
        @click="activeTab = 'providers'"
      >
        <span>Provider 配置</span>
        <b>{{ store.dnsProviders.length }}</b>
      </button>
      <button
        type="button"
        :class="{ active: activeTab === 'records' }"
        @click="activeTab = 'records'"
      >
        <span>托管记录</span>
        <b>{{ store.dnsRecords.length }}</b>
      </button>
    </nav>

    <section v-if="activeTab === 'providers'" class="workspace-panel">
      <div class="panel-heading">
        <div>
          <h2>域名服务商</h2>
          <p>保存前会验证凭据和 Zone 权限；密钥只写入服务端，不会在页面回显。</p>
        </div>
        <button type="button" class="btn-primary panel-create" @click="openProvider()">添加 Provider</button>
      </div>

      <div v-if="store.dnsProviders.length" class="provider-grid">
        <article v-for="provider in store.dnsProviders" :key="provider.id" class="card provider-card">
          <div class="provider-head">
            <div class="provider-identity">
              <span class="provider-mark" :class="`provider-${provider.type}`">
                {{ provider.type === 'cloudflare' ? 'CF' : 'ALI' }}
              </span>
              <div class="provider-title">
                <h3>{{ provider.name }}</h3>
                <p>{{ provider.zone }}</p>
              </div>
            </div>
            <span class="status-pill" :class="provider.enabled ? 'status-success' : 'status-muted'">
              <i></i>{{ provider.enabled ? '已启用' : '已停用' }}
            </span>
          </div>

          <dl class="provider-details">
            <div>
              <dt>服务商</dt>
              <dd>{{ provider.type === 'cloudflare' ? 'Cloudflare' : '阿里云 DNS' }}</dd>
            </div>
            <div>
              <dt>访问凭据</dt>
              <dd :class="credentialReady(provider) ? 'text-success' : 'text-danger'">
                {{ credentialLabel(provider) }}
              </dd>
            </div>
            <div>
              <dt>托管记录</dt>
              <dd>{{ recordsFor(provider.id).length }} 条</dd>
            </div>
            <div>
              <dt>最近测试</dt>
              <dd>{{ formatTime(provider.last_test_at, '尚未测试') }}</dd>
            </div>
            <div class="detail-wide">
              <dt>最近更新</dt>
              <dd>{{ formatTime(provider.updated_at, '尚未更新') }}</dd>
            </div>
          </dl>

          <div v-if="provider.last_error" class="provider-error" role="alert">
            <span>!</span>
            <div>
              <strong>Provider 异常</strong>
              <p>{{ provider.last_error }}</p>
            </div>
          </div>

          <div class="provider-actions">
            <div>
              <button
                type="button"
                class="btn-ghost action-button"
                :disabled="busyId === provider.id"
                @click="testProvider(provider)"
              >
                {{ busyId === provider.id && busyAction === 'test' ? '测试中...' : '测试连接' }}
              </button>
              <button
                type="button"
                class="btn-ghost action-button"
                :disabled="busyId === provider.id"
                @click="syncProvider(provider)"
              >
                {{ busyId === provider.id && busyAction === 'sync' ? '同步中...' : '同步记录' }}
              </button>
            </div>
            <div>
              <button type="button" class="btn-ghost action-button" @click="openProvider(provider)">编辑</button>
              <button type="button" class="btn-danger action-button" @click="removeProvider(provider)">删除</button>
            </div>
          </div>
        </article>
      </div>

      <div v-else class="card empty-state">
        <div class="empty-icon">DNS</div>
        <h3>还没有 DNS Provider</h3>
        <p>先连接阿里云 DNS 或 Cloudflare，验证成功后即可托管 Relay 入口记录。</p>
        <button type="button" class="btn-primary" @click="openProvider()">添加第一个 Provider</button>
      </div>
    </section>

    <section v-else class="workspace-panel records-panel">
      <div class="panel-heading records-heading">
        <div>
          <h2>托管记录</h2>
          <p>Agent 来源会跟随 Relay 公网 IP；系统不会修改本页之外的 DNS 记录。</p>
        </div>
        <button
          type="button"
          class="btn-primary panel-create"
          :disabled="!store.dnsProviders.length"
          @click="openRecord()"
        >
          添加记录
        </button>
      </div>

      <div class="record-toolbar">
        <label class="search-control">
          <span aria-hidden="true">⌕</span>
          <input v-model.trim="recordQuery" type="search" placeholder="搜索记录名、目标值、Provider 或 Relay" />
        </label>
        <div class="filter-group" aria-label="记录状态筛选">
          <button
            v-for="filter in statusFilters"
            :key="filter.value"
            type="button"
            :class="{ active: recordStatus === filter.value }"
            @click="recordStatus = filter.value"
          >
            {{ filter.label }}
            <span>{{ filter.count }}</span>
          </button>
        </div>
      </div>

      <div v-if="filteredRecords.length" class="record-table-wrap">
        <table class="record-table">
          <thead>
            <tr>
              <th>记录</th>
              <th>目标值 / 来源</th>
              <th>Provider</th>
              <th>TTL</th>
              <th>状态</th>
              <th><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="record in filteredRecords" :key="record.id">
              <td data-label="记录">
                <div class="record-name">
                  <span class="record-type">{{ record.type }}</span>
                  <strong>{{ record.name }}</strong>
                </div>
              </td>
              <td data-label="目标值 / 来源">
                <div class="record-target">
                  <MaskedText :value="record.value" placeholder="等待 Agent 上报 IP" />
                  <small v-if="record.relay_node_id">
                    Relay · {{ relayNodeName(record.relay_node_id) }}
                  </small>
                  <small v-else>手动记录值</small>
                </div>
              </td>
              <td data-label="Provider">
                <div class="record-provider">
                  <strong>{{ providerName(record.provider_id) }}</strong>
                  <small>{{ providerFor(record.provider_id)?.zone || 'Provider 已删除' }}</small>
                </div>
              </td>
              <td data-label="TTL" class="record-ttl">
                {{ ttlLabel(record.ttl, providerFor(record.provider_id)) }}
              </td>
              <td data-label="状态">
                <span class="status-pill" :class="statusClass(record.status)">
                  <i></i>{{ statusLabel(record.status) }}
                </span>
                <p v-if="record.last_error" class="record-error" :title="record.last_error">
                  {{ record.last_error }}
                </p>
              </td>
              <td class="record-actions">
                <button type="button" class="btn-ghost action-button" @click="openRecord(record)">编辑</button>
                <button type="button" class="btn-danger action-button" @click="removeRecord(record)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="empty-state records-empty">
        <div class="empty-icon">A</div>
        <h3>{{ store.dnsRecords.length ? '没有符合条件的记录' : '还没有托管记录' }}</h3>
        <p v-if="store.dnsRecords.length">清除搜索词或切换状态筛选后再试。</p>
        <p v-else-if="!store.dnsProviders.length">请先添加并验证一个 DNS Provider。</p>
        <p v-else>创建记录后可手动填写目标值，或让它自动跟随 Relay Agent 的公网 IP。</p>
        <button
          v-if="!store.dnsRecords.length"
          type="button"
          class="btn-primary"
          @click="store.dnsProviders.length ? openRecord() : openProvider()"
        >
          {{ store.dnsProviders.length ? '添加第一条记录' : '先添加 Provider' }}
        </button>
      </div>
    </section>

    <Modal v-if="providerForm.open" size="wide" @close="providerForm.open = false">
      <form class="modal-form" @submit.prevent="saveProvider">
        <div class="modal-heading">
          <div class="eyebrow">DNS PROVIDER</div>
          <h2>{{ providerForm.id ? '编辑 DNS Provider' : '添加 DNS Provider' }}</h2>
          <p>服务端会在保存前测试凭据和 Zone 权限，测试失败不会写入配置。</p>
        </div>

        <section class="form-section">
          <div class="form-section-heading">
            <span>1</span>
            <div><h3>基础信息</h3><p>选择服务商并指定要托管的根域名。</p></div>
          </div>
          <div class="form-grid">
            <label>
              <span class="field-label">显示名称</span>
              <input v-model.trim="providerForm.name" class="input" required placeholder="例如：主域名 DNS" />
            </label>
            <label>
              <span class="field-label">服务商</span>
              <select v-model="providerForm.type" class="input">
                <option value="aliyun">阿里云 DNS</option>
                <option value="cloudflare">Cloudflare</option>
              </select>
            </label>
            <label class="form-span-2">
              <span class="field-label">DNS Zone</span>
              <input v-model.trim="providerForm.zone" class="input" required placeholder="example.com" />
              <small class="field-hint">填写区域根域名，例如 example.com，不要填写 relay.example.com。</small>
            </label>
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-heading">
            <span>2</span>
            <div><h3>访问凭据</h3><p>建议使用仅包含当前 Zone DNS 编辑权限的凭据。</p></div>
          </div>
          <div v-if="providerForm.type === 'aliyun'" class="form-grid">
            <label>
              <span class="field-label">AccessKey ID</span>
              <input v-model.trim="providerForm.access_key_id" class="input" required autocomplete="off" />
            </label>
            <label>
              <span class="field-label">
                AccessKey Secret
                <em v-if="providerForm.id">留空保持原值</em>
              </span>
              <input
                v-model="providerForm.access_key_secret"
                type="password"
                class="input"
                :required="!providerForm.id"
                autocomplete="new-password"
              />
            </label>
          </div>
          <div v-else class="form-grid">
            <label class="form-span-2">
              <span class="field-label">
                API Token
                <em v-if="providerForm.id">留空保持原值</em>
              </span>
              <input
                v-model="providerForm.api_token"
                type="password"
                class="input"
                :required="!providerForm.id"
                autocomplete="new-password"
              />
              <small class="field-hint">Cloudflare Token 至少需要 Zone DNS Edit 与 Zone Read 权限。</small>
            </label>
          </div>
        </section>

        <section class="form-section form-section-compact">
          <div class="form-section-heading">
            <span>3</span>
            <div><h3>自动同步</h3><p>停用后保留配置，但不会再自动更新这个 Provider 的记录。</p></div>
          </div>
          <label class="toggle-row">
            <input v-model="providerForm.enabled" type="checkbox" />
            <span><strong>启用 Provider</strong><small>允许控制器自动同步托管记录</small></span>
          </label>
        </section>

        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="modal-actions">
          <button type="button" class="btn-ghost border border-slate-200" @click="providerForm.open = false">取消</button>
          <button type="submit" class="btn-primary" :disabled="saving">
            {{ saving ? '正在验证并保存...' : '验证并保存 Provider' }}
          </button>
        </div>
      </form>
    </Modal>

    <Modal v-if="recordForm.open" size="wide" @close="recordForm.open = false">
      <form class="modal-form" @submit.prevent="saveRecord">
        <div class="modal-heading">
          <div class="eyebrow">MANAGED RECORD</div>
          <h2>{{ recordForm.id ? '编辑托管记录' : '添加托管记录' }}</h2>
          <p>选择记录来源；Agent 来源会在公网 IP 变化后自动等待同步。</p>
        </div>

        <section class="form-section">
          <div class="form-section-heading">
            <span>1</span>
            <div><h3>记录信息</h3><p>指定服务商、记录类型和主机记录。</p></div>
          </div>
          <div class="form-grid">
            <label>
              <span class="field-label">DNS Provider</span>
              <select v-model="recordForm.provider_id" class="input" required>
                <option v-for="provider in store.dnsProviders" :key="provider.id" :value="provider.id">
                  {{ provider.name }} · {{ provider.zone }}
                </option>
              </select>
            </label>
            <label>
              <span class="field-label">记录类型</span>
              <select v-model="recordForm.type" class="input">
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
                <option value="TXT">TXT</option>
              </select>
            </label>
            <label class="form-span-2">
              <span class="field-label">记录名</span>
              <input v-model.trim="recordForm.name" class="input" required placeholder="relay 或 relay.example.com" />
              <small class="field-hint">可填写主机记录或完整域名，服务端会按所选 Zone 规范化。</small>
            </label>
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-heading">
            <span>2</span>
            <div><h3>记录来源</h3><p>手动设置固定值，或让记录自动跟随 Relay 公网 IP。</p></div>
          </div>
          <div class="source-switch">
            <label :class="{ active: recordForm.source === 'manual' }">
              <input v-model="recordForm.source" type="radio" value="manual" />
              <span><strong>手动填写</strong><small>适合固定 IP、CNAME 或 TXT</small></span>
            </label>
            <label :class="{ active: recordForm.source === 'agent' }">
              <input v-model="recordForm.source" type="radio" value="agent" />
              <span><strong>Relay Agent</strong><small>自动跟随节点公网 IP</small></span>
            </label>
          </div>

          <div v-if="recordForm.source === 'manual'" class="source-content">
            <label>
              <span class="field-label">记录值</span>
              <input v-model.trim="recordForm.value" class="input" required placeholder="例如：203.0.113.10" />
            </label>
          </div>
          <div v-else class="source-content">
            <template v-if="recordForm.id">
              <label>
                <span class="field-label">CDT Relay Agent</span>
                <select v-model="recordForm.relay_node_id" class="input" required>
                  <option value="">请选择已安装 Agent 的节点</option>
                  <option v-for="node in store.relayNodes" :key="node.id" :value="node.id">
                    {{ node.name }} · {{ maskDisplay(node.public_ip, '未上报 IP') }} · {{ node.status }}
                  </option>
                </select>
              </label>
              <small class="field-hint">编辑时一条记录只能绑定一台 Relay Agent。</small>
            </template>
            <template v-else>
              <span class="field-label">CDT Relay Agent（可多选）</span>
              <div class="target-picker">
                <label v-for="node in store.relayNodes" :key="node.id" class="target-option">
                  <input v-model="recordForm.relay_node_ids" type="checkbox" :value="node.id" />
                  <span class="target-status" :class="node.status === 'online' ? 'online' : ''"></span>
                  <span>
                    <strong>{{ node.name }}</strong>
                    <small><MaskedIP :value="node.public_ip" placeholder="未上报 IP" /> · {{ node.status }}</small>
                  </span>
                </label>
                <div v-if="!store.relayNodes.length" class="empty-picker">暂无已注册 Relay Agent</div>
              </div>
              <small class="field-hint">每台 Agent 会创建一条同名记录；离线时自动停用，恢复后重新同步。</small>
            </template>
          </div>
        </section>

        <section class="form-section form-section-compact">
          <div class="form-section-heading">
            <span>3</span>
            <div><h3>同步策略</h3><p>配置缓存时间，以及该记录是否进入同步队列。</p></div>
          </div>
          <div class="form-grid policy-grid">
            <label>
              <span class="field-label">TTL</span>
              <select v-model.number="recordForm.ttl" class="input">
                <option v-for="option in recordTTLOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
              <small class="field-hint">{{ recordTTLHint }}</small>
            </label>
            <label class="toggle-row">
              <input v-model="recordForm.enabled" type="checkbox" />
              <span><strong>启用托管</strong><small>保存后进入 Provider 同步队列</small></span>
            </label>
          </div>
        </section>

        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="modal-actions">
          <button type="button" class="btn-ghost border border-slate-200" @click="recordForm.open = false">取消</button>
          <button type="submit" class="btn-primary" :disabled="saving">
            {{ saving ? '保存中...' : '保存托管记录' }}
          </button>
        </div>
      </form>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRelayStore } from '../stores/relay'
import Modal from '../components/Modal.vue'
import MaskedIP from '../components/MaskedIP.vue'
import MaskedText from '../components/MaskedText.vue'
import { normalizeTTLForProvider, ttlHint, ttlLabel, ttlOptions } from '../utils/dns'
import { maskIP } from '../utils/ip'

const store = useRelayStore()
const activeTab = ref('providers')
const syncing = ref(false)
const busyId = ref('')
const busyAction = ref('')
const saving = ref(false)
const formError = ref('')
const message = ref({ type: 'success', text: '' })
const recordQuery = ref('')
const recordStatus = ref('all')

const providerForm = reactive({
  open: false,
  id: '',
  name: '',
  type: 'aliyun',
  zone: '',
  zone_id: '',
  endpoint: '',
  access_key_id: '',
  access_key_secret: '',
  api_token: '',
  enabled: true,
})

const recordForm = reactive({
  open: false,
  id: '',
  provider_id: '',
  source: 'manual',
  relay_node_id: '',
  relay_node_ids: [],
  name: '',
  type: 'A',
  value: '',
  ttl: 60,
  enabled: true,
})

const enabledProviderCount = computed(() => store.dnsProviders.filter(provider => provider.enabled !== false).length)
const syncedRecordCount = computed(() => store.dnsRecords.filter(record => record.status === 'synced').length)
const failedRecordCount = computed(() => store.dnsRecords.filter(record => record.status === 'error').length)
const pendingRecordCount = computed(() => store.dnsRecords.filter(record => ['pending', 'deleting'].includes(record.status)).length)
const onlineRelayCount = computed(() => store.relayNodes.filter(node => node.status === 'online').length)
const selectedRecordProvider = computed(() => providerFor(recordForm.provider_id))
const recordTTLOptions = computed(() => ttlOptions(selectedRecordProvider.value, recordForm.ttl))
const recordTTLHint = computed(() => ttlHint(selectedRecordProvider.value))

const statusFilters = computed(() => [
  { value: 'all', label: '全部', count: store.dnsRecords.length },
  { value: 'synced', label: '已同步', count: syncedRecordCount.value },
  { value: 'pending', label: '待处理', count: pendingRecordCount.value },
  { value: 'error', label: '失败', count: failedRecordCount.value },
  { value: 'disabled', label: '已停用', count: store.dnsRecords.filter(record => record.status === 'disabled').length },
])

const filteredRecords = computed(() => {
  const query = recordQuery.value.toLowerCase()
  return store.dnsRecords.filter(record => {
    const matchesStatus = recordStatus.value === 'all'
      || record.status === recordStatus.value
      || (recordStatus.value === 'pending' && record.status === 'deleting')
    if (!matchesStatus) return false
    if (!query) return true
    const searchable = [
      record.name,
      record.type,
      record.value,
      providerName(record.provider_id),
      record.relay_node_id ? relayNodeName(record.relay_node_id) : '',
    ].join(' ').toLowerCase()
    return searchable.includes(query)
  })
})

function recordsFor(id) {
  return store.dnsRecords.filter(item => String(item.provider_id) === String(id))
}

function providerFor(id) {
  return store.dnsProviders.find(item => String(item.id) === String(id))
}

function providerName(id) {
  return providerFor(id)?.name || String(id || '未知 Provider')
}

function relayNodeName(id) {
  return store.relayNodes.find(item => String(item.id) === String(id))?.name || String(id || '未知 Relay')
}

function maskDisplay(value, fallback = '未上报 IP') {
  return maskIP(value) || fallback
}

function credentialReady(provider) {
  return provider.type === 'cloudflare' ? provider.token_configured : provider.secret_configured
}

function credentialLabel(provider) {
  if (!credentialReady(provider)) return '未配置'
  return provider.type === 'cloudflare' ? 'Token 已配置' : 'AccessKey 已配置'
}

function formatTime(value, fallback = '—') {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : fallback
}

function statusLabel(value) {
  return {
    synced: '已同步',
    pending: '待同步',
    disabled: '已停用',
    deleting: '清理中',
    error: '同步失败',
  }[value] || value || '未知'
}

function statusClass(value) {
  return {
    synced: 'status-success',
    pending: 'status-warning',
    deleting: 'status-warning',
    error: 'status-danger',
    disabled: 'status-muted',
  }[value] || 'status-muted'
}

function showMessage(text, type = 'success') {
  message.value = { text, type }
  window.setTimeout(() => {
    message.value = { text: '', type: 'success' }
  }, 5000)
}

function openPrimaryCreate() {
  if (activeTab.value === 'providers') openProvider()
  else if (store.dnsProviders.length) openRecord()
  else {
    activeTab.value = 'providers'
    openProvider()
  }
}

function openProvider(provider) {
  formError.value = ''
  Object.assign(providerForm, provider ? {
    open: true,
    id: provider.id || '',
    name: provider.name || '',
    type: provider.type || 'aliyun',
    zone: provider.zone || '',
    zone_id: provider.zone_id || '',
    endpoint: provider.endpoint || '',
    access_key_id: provider.access_key_id || '',
    access_key_secret: '',
    api_token: '',
    enabled: provider.enabled !== false,
  } : {
    open: true,
    id: '',
    name: '',
    type: 'aliyun',
    zone: '',
    zone_id: '',
    endpoint: '',
    access_key_id: '',
    access_key_secret: '',
    api_token: '',
    enabled: true,
  })
}

async function saveProvider() {
  saving.value = true
  formError.value = ''
  const payload = {
    name: providerForm.name,
    type: providerForm.type,
    zone: providerForm.zone,
    zone_id: providerForm.zone_id,
    endpoint: providerForm.endpoint,
    access_key_id: providerForm.access_key_id,
    access_key_secret: providerForm.access_key_secret,
    api_token: providerForm.api_token,
    enabled: providerForm.enabled,
  }
  try {
    if (providerForm.id) await store.updateDNSProvider(providerForm.id, payload)
    else await store.createDNSProvider(payload)
    providerForm.open = false
    showMessage('DNS Provider 已验证并保存')
  } catch (error) {
    formError.value = error.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

async function testProvider(provider) {
  busyId.value = provider.id
  busyAction.value = 'test'
  try {
    await store.testDNSProvider(provider.id)
    showMessage(`“${provider.name}”连接测试成功`)
  } catch (error) {
    showMessage(error.response?.data?.error || 'Provider 测试失败', 'error')
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function syncProvider(provider) {
  busyId.value = provider.id
  busyAction.value = 'sync'
  try {
    const data = await store.syncDNSProvider(provider.id)
    showMessage(`同步完成：新增 ${data.result?.created || 0}，更新 ${data.result?.updated || 0}，删除 ${data.result?.deleted || 0}`)
  } catch (error) {
    showMessage(error.response?.data?.error || 'DNS 同步失败', 'error')
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function syncAll() {
  syncing.value = true
  try {
    const data = await store.syncAllDNS()
    if (data.ok === false) throw new Error(data.error || '部分 Provider 同步失败')
    showMessage('全部 DNS Provider 已同步')
  } catch (error) {
    showMessage(error.response?.data?.error || error.message || 'DNS 同步失败', 'error')
  } finally {
    syncing.value = false
  }
}

async function removeProvider(provider) {
  if (!window.confirm(`确认删除 DNS Provider“${provider.name}”？请先确认它没有托管记录。`)) return
  try {
    await store.deleteDNSProvider(provider.id)
    showMessage('DNS Provider 已删除')
  } catch (error) {
    showMessage(error.response?.data?.error || '删除失败', 'error')
  }
}

function openRecord(record) {
  formError.value = ''
  Object.assign(recordForm, record ? {
    open: true,
    id: record.id,
    provider_id: record.provider_id,
    source: record.relay_node_id ? 'agent' : 'manual',
    relay_node_id: record.relay_node_id || '',
    relay_node_ids: record.relay_node_id ? [record.relay_node_id] : [],
    name: record.name || '',
    type: record.type || 'A',
    value: record.value || '',
    ttl: normalizeTTLForProvider(record.ttl || 60, providerFor(record.provider_id)),
    enabled: record.desired_enabled !== false,
  } : {
    open: true,
    id: '',
    provider_id: store.dnsProviders[0]?.id || '',
    source: 'manual',
    relay_node_id: '',
    relay_node_ids: [],
    name: '',
    type: 'A',
    value: '',
    ttl: 60,
    enabled: true,
  })
}

watch(() => recordForm.provider_id, next => {
  recordForm.ttl = normalizeTTLForProvider(recordForm.ttl, providerFor(next))
})

async function saveRecord() {
  saving.value = true
  formError.value = ''
  const selectedRelayNodeIDs = recordForm.id
    ? [recordForm.relay_node_id].filter(Boolean)
    : [...recordForm.relay_node_ids]
  if (recordForm.source === 'agent' && !selectedRelayNodeIDs.length) {
    formError.value = '请至少选择一台 CDT Relay Agent'
    saving.value = false
    return
  }
  const payload = {
    provider_id: recordForm.provider_id,
    name: recordForm.name,
    type: recordForm.type,
    value: recordForm.source === 'agent' ? '' : recordForm.value,
    ttl: normalizeTTLForProvider(recordForm.ttl, selectedRecordProvider.value),
    enabled: recordForm.enabled,
  }
  if (recordForm.source === 'agent') {
    if (recordForm.id) payload.relay_node_id = selectedRelayNodeIDs[0]
    else payload.relay_node_ids = selectedRelayNodeIDs
  }
  try {
    if (recordForm.id) await store.updateDNSRecord(recordForm.id, payload)
    else await store.createDNSRecord(payload)
    recordForm.open = false
    showMessage(
      recordForm.id || selectedRelayNodeIDs.length < 2
        ? '托管记录已保存，等待同步'
        : `已为 ${selectedRelayNodeIDs.length} 台 Relay 创建记录，等待同步`,
    )
  } catch (error) {
    formError.value = error.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

async function removeRecord(record) {
  if (!window.confirm(`确认删除托管记录“${record.name}”？`)) return
  try {
    await store.deleteDNSRecord(record.id)
    showMessage('托管记录已删除，下一次同步会清理对应 DNS 记录')
  } catch (error) {
    showMessage(error.response?.data?.error || '删除失败', 'error')
  }
}

onMounted(async () => {
  await Promise.all([
    store.fetchDNSProviders(),
    store.fetchDNSRecords(),
    store.fetchRelayNodes(),
  ])
})
</script>

<style scoped>
.dns-page { display: grid; min-width: 0; gap: 20px; }
.page-header { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 5px; color: #172033; font-size: 27px; font-weight: 750; letter-spacing: -.03em; }
.page-subtitle { max-width: 680px; margin-top: 5px; color: #64748b; font-size: 12px; line-height: 1.6; }
.header-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 11px; }
.summary-card { display: flex; min-width: 0; align-items: center; gap: 12px; padding: 14px 15px; border-top: 2px solid #93c5fd; }
.summary-icon { display: grid; width: 34px; height: 34px; flex: 0 0 auto; place-items: center; border-radius: 9px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 850; }
.summary-card > div:last-child { display: grid; min-width: 0; grid-template-columns: auto 1fr; align-items: baseline; column-gap: 8px; }
.summary-card span { color: #64748b; font-size: 9px; font-weight: 700; }
.summary-card strong { color: #1e293b; font-size: 18px; line-height: 1; }
.summary-card small { grid-column: 1 / -1; overflow: hidden; margin-top: 5px; color: #94a3b8; font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.summary-card .summary-error { color: #dc2626; }
.summary-card-indigo { border-top-color: #a5b4fc; }
.summary-card-indigo .summary-icon { background: #eef2ff; color: #4f46e5; }
.summary-card-green { border-top-color: #86efac; }
.summary-card-green .summary-icon { background: #ecfdf3; color: #15803d; }
.summary-card-red { border-top-color: #fca5a5; }
.summary-card-red .summary-icon { background: #fef2f2; color: #dc2626; }
.summary-card-cyan { border-top-color: #67e8f9; }
.summary-card-cyan .summary-icon { background: #ecfeff; color: #0e7490; }
.workspace-tabs { display: flex; align-items: flex-end; gap: 6px; margin-bottom: -21px; padding-left: 4px; border-bottom: 1px solid #dfe7f1; }
.workspace-tabs button { display: flex; align-items: center; gap: 7px; padding: 11px 14px 12px; border-bottom: 2px solid transparent; color: #64748b; font-size: 11px; font-weight: 650; transition: color .15s ease, border-color .15s ease; }
.workspace-tabs button:hover { color: #2563eb; }
.workspace-tabs button.active { border-bottom-color: #2563eb; color: #1d4ed8; }
.workspace-tabs b { min-width: 19px; border-radius: 999px; background: #f1f5f9; padding: 2px 6px; color: #64748b; font-size: 9px; text-align: center; }
.workspace-tabs button.active b { background: #dbeafe; color: #1d4ed8; }
.workspace-panel { min-width: 0; padding-top: 20px; }
.panel-heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 18px; margin-bottom: 14px; }
.panel-heading h2 { color: #1e293b; font-size: 15px; font-weight: 750; }
.panel-heading p { margin-top: 4px; color: #94a3b8; font-size: 10px; line-height: 1.5; }
.panel-create { display: none; }
.provider-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.provider-card { display: flex; min-width: 0; flex-direction: column; padding: 18px; }
.provider-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.provider-identity { display: flex; min-width: 0; align-items: center; gap: 10px; }
.provider-mark { display: grid; width: 36px; height: 36px; flex: 0 0 auto; place-items: center; border-radius: 9px; background: #eff6ff; color: #2563eb; font-size: 9px; font-weight: 850; letter-spacing: .03em; }
.provider-cloudflare { background: #fff7ed; color: #ea580c; }
.provider-title { min-width: 0; }
.provider-title h3 { overflow: hidden; color: #1e293b; font-size: 13px; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.provider-title p { overflow: hidden; margin-top: 4px; color: #64748b; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.status-pill { display: inline-flex; flex: 0 0 auto; align-items: center; gap: 6px; border-radius: 999px; padding: 5px 8px; font-size: 9px; font-weight: 700; white-space: nowrap; }
.status-pill i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.status-success { background: #ecfdf3; color: #15803d; }
.status-warning { background: #fffbeb; color: #b45309; }
.status-danger { background: #fef2f2; color: #b91c1c; }
.status-muted { background: #f1f5f9; color: #64748b; }
.provider-details { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1px; overflow: hidden; margin-top: 17px; border: 1px solid #edf1f6; border-radius: 9px; background: #edf1f6; }
.provider-details > div { min-width: 0; background: #f8fafc; padding: 9px 10px; }
.provider-details .detail-wide { grid-column: 1 / -1; }
.provider-details dt { color: #94a3b8; font-size: 8px; }
.provider-details dd { overflow: hidden; margin-top: 4px; color: #475569; font-size: 10px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.provider-details .text-success { color: #15803d; }
.provider-details .text-danger { color: #dc2626; }
.provider-error { display: flex; align-items: flex-start; gap: 9px; margin-top: 11px; border: 1px solid #fecaca; border-radius: 9px; background: #fef2f2; padding: 9px 10px; color: #b91c1c; }
.provider-error > span { display: grid; width: 17px; height: 17px; flex: 0 0 auto; place-items: center; border-radius: 50%; background: #fee2e2; font-size: 9px; font-weight: 900; }
.provider-error strong { display: block; font-size: 9px; }
.provider-error p { margin-top: 3px; font-size: 9px; line-height: 1.5; word-break: break-word; }
.provider-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: auto; padding-top: 15px; }
.provider-actions > div { display: flex; gap: 4px; }
.action-button { padding: 6px 8px; font-size: 10px; }
.empty-state { display: grid; justify-items: center; padding: 58px 24px; text-align: center; }
.empty-icon { display: grid; width: 42px; height: 42px; place-items: center; border-radius: 12px; background: #eff6ff; color: #2563eb; font-size: 10px; font-weight: 850; }
.empty-state h3 { margin-top: 13px; color: #334155; font-size: 13px; font-weight: 750; }
.empty-state p { max-width: 480px; margin-top: 5px; color: #94a3b8; font-size: 10px; line-height: 1.6; }
.empty-state .btn-primary { margin-top: 16px; }
.records-panel { overflow: hidden; border: 1px solid #dfe7f1; border-radius: 11px; background: #fff; }
.records-heading { margin: 0; padding: 17px 18px 14px; }
.record-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid #edf1f6; border-bottom: 1px solid #edf1f6; background: #f8fafc; padding: 10px 12px; }
.search-control { display: flex; width: min(100%, 330px); min-width: 220px; align-items: center; gap: 7px; border: 1px solid #dfe7f1; border-radius: 8px; background: #fff; padding: 0 10px; color: #94a3b8; }
.search-control input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; padding: 8px 0; color: #334155; font-size: 10px; }
.filter-group { display: flex; overflow-x: auto; gap: 3px; }
.filter-group button { display: flex; align-items: center; gap: 5px; border-radius: 7px; padding: 6px 8px; color: #64748b; font-size: 9px; font-weight: 650; white-space: nowrap; }
.filter-group button:hover { background: #eef2f7; color: #334155; }
.filter-group button.active { background: #dbeafe; color: #1d4ed8; }
.filter-group span { min-width: 16px; border-radius: 999px; background: rgba(148, 163, 184, .14); padding: 1px 4px; text-align: center; }
.record-table-wrap { overflow-x: auto; }
.record-table { width: 100%; border-collapse: collapse; table-layout: fixed; text-align: left; }
.record-table th { padding: 10px 12px; border-bottom: 1px solid #edf1f6; background: #fff; color: #94a3b8; font-size: 8px; font-weight: 750; letter-spacing: .04em; text-transform: uppercase; }
.record-table th:nth-child(1) { width: 20%; }
.record-table th:nth-child(2) { width: 23%; }
.record-table th:nth-child(3) { width: 18%; }
.record-table th:nth-child(4) { width: 10%; }
.record-table th:nth-child(5) { width: 17%; }
.record-table th:nth-child(6) { width: 12%; }
.record-table td { min-width: 0; padding: 12px; border-bottom: 1px solid #f0f3f7; color: #475569; font-size: 10px; vertical-align: middle; }
.record-table tbody tr:last-child td { border-bottom: 0; }
.record-table tbody tr:hover { background: #fbfdff; }
.record-name { display: flex; min-width: 0; align-items: center; gap: 7px; }
.record-name strong { overflow: hidden; color: #334155; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.record-type { flex: 0 0 auto; border-radius: 5px; background: #eaf2ff; padding: 3px 5px; color: #2563eb; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 8px; font-weight: 800; }
.record-target, .record-provider { display: grid; min-width: 0; gap: 3px; }
.record-target :deep(.masked-text), .record-target > span { overflow: hidden; color: #475569; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.record-target small, .record-provider small { overflow: hidden; color: #94a3b8; font-size: 8px; text-overflow: ellipsis; white-space: nowrap; }
.record-provider strong { overflow: hidden; color: #475569; font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.record-ttl { color: #64748b; white-space: nowrap; }
.record-error { display: -webkit-box; overflow: hidden; margin-top: 5px; color: #dc2626; font-size: 8px; line-height: 1.4; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.record-actions { text-align: right; white-space: nowrap; }
.records-empty { border: 0; padding-block: 54px; }
.modal-form { display: grid; gap: 15px; }
.modal-heading { padding-right: 30px; }
.modal-heading h2 { margin-top: 5px; color: #172033; font-size: 19px; font-weight: 750; letter-spacing: -.02em; }
.modal-heading p { margin-top: 5px; color: #94a3b8; font-size: 10px; line-height: 1.6; }
.form-section { border: 1px solid #e5eaf1; border-radius: 11px; padding: 15px; }
.form-section-compact { background: #fbfdff; }
.form-section-heading { display: flex; align-items: flex-start; gap: 9px; margin-bottom: 14px; }
.form-section-heading > span { display: grid; width: 21px; height: 21px; flex: 0 0 auto; place-items: center; border-radius: 6px; background: #eaf2ff; color: #2563eb; font-size: 9px; font-weight: 800; }
.form-section-heading h3 { color: #334155; font-size: 11px; font-weight: 750; }
.form-section-heading p { margin-top: 2px; color: #94a3b8; font-size: 9px; line-height: 1.4; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 13px; }
.form-grid label, .source-content > label { display: block; min-width: 0; }
.form-span-2 { grid-column: 1 / -1; }
.field-label { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 6px; color: #64748b; font-size: 9px; font-weight: 700; }
.field-label em { color: #94a3b8; font-size: 8px; font-style: normal; font-weight: 500; }
.field-hint { display: block; margin-top: 5px; color: #94a3b8; font-size: 8px; line-height: 1.5; }
.toggle-row { display: flex !important; align-items: center; gap: 9px; border: 1px solid #e5eaf1; border-radius: 9px; background: #fff; padding: 10px 11px; }
.toggle-row input { flex: 0 0 auto; accent-color: #2563eb; }
.toggle-row span { display: grid; gap: 2px; }
.toggle-row strong { color: #475569; font-size: 9px; }
.toggle-row small { color: #94a3b8; font-size: 8px; }
.source-switch { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.source-switch label { display: flex; min-width: 0; align-items: center; gap: 9px; border: 1px solid #e5eaf1; border-radius: 9px; padding: 10px; cursor: pointer; }
.source-switch label.active { border-color: #93c5fd; background: #eff6ff; }
.source-switch input { accent-color: #2563eb; }
.source-switch span { display: grid; min-width: 0; gap: 2px; }
.source-switch strong { color: #475569; font-size: 9px; }
.source-switch small { color: #94a3b8; font-size: 8px; }
.source-content { margin-top: 12px; }
.target-picker { max-height: 230px; overflow: auto; border: 1px solid #e5eaf1; border-radius: 9px; }
.target-option { display: flex; align-items: center; gap: 9px; padding: 9px 11px; border-bottom: 1px solid #edf1f6; cursor: pointer; }
.target-option:hover { background: #f8fafc; }
.target-option:last-child { border-bottom: 0; }
.target-option input { accent-color: #2563eb; }
.target-option > span:last-child { display: grid; min-width: 0; gap: 2px; }
.target-option strong { color: #475569; font-size: 9px; }
.target-option small { display: flex; min-width: 0; gap: 3px; color: #94a3b8; font-size: 8px; }
.target-status { width: 6px; height: 6px; flex: 0 0 auto; border-radius: 50%; background: #cbd5e1; }
.target-status.online { background: #22c55e; box-shadow: 0 0 0 2px #dcfce7; }
.empty-picker { padding: 22px; color: #94a3b8; font-size: 9px; text-align: center; }
.policy-grid { align-items: end; }
.modal-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 2px; }
.modal-actions button { min-width: 126px; }

@media (max-width: 1050px) {
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .record-table th:nth-child(2) { width: 21%; }
  .record-table th:nth-child(3) { width: 17%; }
  .record-table th:nth-child(5) { width: 19%; }
  .record-table th:nth-child(6) { width: 13%; }
}

@media (max-width: 820px) {
  .provider-grid { grid-template-columns: minmax(0, 1fr); }
  .record-toolbar { align-items: stretch; flex-direction: column; }
  .search-control { width: 100%; }
  .filter-group { padding-bottom: 2px; }
  .record-table, .record-table tbody, .record-table tr, .record-table td { display: block; width: 100%; }
  .record-table thead { display: none; }
  .record-table tbody { display: grid; gap: 0; }
  .record-table tr { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 12px 13px; border-bottom: 1px solid #edf1f6; }
  .record-table tr:last-child { border-bottom: 0; }
  .record-table td { display: grid; align-content: start; gap: 5px; padding: 7px; border: 0; }
  .record-table td::before { content: attr(data-label); color: #94a3b8; font-size: 8px; font-weight: 700; }
  .record-table .record-actions { display: flex; align-items: end; justify-content: flex-end; grid-column: 1 / -1; padding-top: 9px; border-top: 1px dashed #edf1f6; }
  .record-table .record-actions::before { display: none; }
}

@media (max-width: 639px) {
  .dns-page { gap: 16px; }
  .page-header { align-items: flex-start; flex-direction: column; }
  .header-actions { width: 100%; }
  .header-actions button { flex: 1; }
  .summary-grid { grid-template-columns: minmax(0, 1fr); gap: 8px; }
  .summary-card { padding: 12px 13px; }
  .workspace-tabs { margin-bottom: -17px; }
  .workspace-tabs button { flex: 1; justify-content: center; }
  .workspace-panel { padding-top: 16px; }
  .panel-heading { align-items: flex-start; }
  .panel-create { display: inline-flex; flex: 0 0 auto; }
  .provider-card { padding: 15px; }
  .provider-actions { align-items: flex-end; flex-direction: column; }
  .provider-actions > div { width: 100%; justify-content: flex-end; }
  .record-table tr { grid-template-columns: minmax(0, 1fr); }
  .record-table .record-actions { grid-column: auto; }
  .form-grid, .source-switch { grid-template-columns: minmax(0, 1fr); }
  .form-span-2 { grid-column: auto; }
  .policy-grid { gap: 12px; }
  .modal-actions button { min-width: 0; flex: 1; }
}
</style>

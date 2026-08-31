<template>
  <div class="cloud-page fade-in">
    <header class="page-header">
      <div>
        <div class="eyebrow">ALIYUN CDT</div>
        <h1 class="page-title">云资源</h1>
        <p class="page-subtitle">统一管理阿里云账户、ECS 实例和账户级 CDT 月流量快照</p>
      </div>
      <div class="header-actions">
        <button class="btn-ghost border border-slate-200" type="button" @click="openCreate">添加账户</button>
        <button class="btn-primary" type="button" :disabled="syncing" @click="sync">
          {{ syncing ? '同步中...' : '立即同步' }}
        </button>
      </div>
    </header>

    <div v-if="message" class="notice" :class="messageType === 'error' ? 'notice-error' : 'notice-success'">
      {{ message }}
    </div>

    <section class="summary-grid" aria-label="云资源摘要">
      <article class="summary-card">
        <span>云账户</span>
        <strong>{{ store.cloud.accounts.length }}</strong>
        <small>{{ enabledAccountCount }} 个账户已启用</small>
      </article>
      <article class="summary-card">
        <span>ECS 实例</span>
        <strong>{{ store.cloud.instances.length }}</strong>
        <small>{{ runningInstanceCount }} 个实例运行中</small>
      </article>
      <article class="summary-card">
        <span>账户流量合计</span>
        <strong>{{ store.cloud.traffic.length ? totalTraffic.toFixed(2) + ' GB' : '待同步' }}</strong>
        <small>{{ store.cloud.traffic.length }} 个账户已有快照 · {{ activeProtectionCount }} 个保护中</small>
      </article>
    </section>

    <section class="account-grid">
      <article v-for="account in store.cloud.accounts" :key="account.id" class="card account-card" :class="account.protection_triggered ? 'account-protected' : ''">
        <div class="account-head">
          <div class="min-w-0">
            <h2>{{ account.name }}</h2>
            <p>{{ maskAccessKey(account.access_key_id) }} · {{ account.region_id }}</p>
          </div>
          <div class="account-badges">
            <span v-if="account.protection_triggered" class="protection-tag protection-tag-active">{{ account.protection_predictive ? '预测排空' : '保护已触发' }}</span>
            <span class="site-tag" :class="account.agent_installed ? 'agent-tag-installed' : 'agent-tag-missing'">
              {{ account.agent_installed ? `Agent 已安装 · ${account.online_agent_count || 0}/${account.agent_count || 0} 在线` : 'Agent 未安装' }}
            </span>
            <span class="site-tag">{{ account.site_type === 'china' ? '中国站' : '国际站' }}</span>
          </div>
        </div>

        <div class="traffic-row">
          <div>
            <span class="traffic-scope">账户 CDT 本月出向流量</span>
            <span class="traffic-value">{{ trafficFor(account.id) ? trafficFor(account.id).used_gb.toFixed(2) : '待同步' }}</span>
            <span v-if="trafficFor(account.id)" class="traffic-unit">GB</span>
          </div>
          <dl class="account-limits">
            <div><dt>月限额</dt><dd>{{ account.traffic_limit_gb }} GB</dd></div>
            <div><dt>保护阈值</dt><dd>{{ account.threshold_percent }}%</dd></div>
            <div><dt>保护策略</dt><dd>{{ protectionModeLabel(account.protection_mode) }}</dd></div>
          </dl>
        </div>

        <div class="traffic-track">
          <div
            class="traffic-fill"
            :class="trafficPercent(account) >= account.threshold_percent ? 'traffic-danger' : ''"
            :style="{ width: Math.min(100, trafficPercent(account)) + '%' }"
          ></div>
        </div>
        <div class="traffic-meta">
          <span>{{ trafficSummary(account) }}</span>
          <span>{{ instancesFor(account.id).length }} 个实例共享该账户额度</span>
        </div>

        <p class="traffic-disclaimer">阿里云接口不提供单个 ECS 的 CDT 用量；同一账户下多个实例共享此快照与保护阈值。</p>

        <div v-if="trafficFor(account.id)?.last_error" class="sync-warning">
          <strong>本次同步失败</strong>
          <span>已保留上次有效流量：{{ trafficFor(account.id).last_error }}</span>
        </div>

        <div v-if="account.protection_triggered" class="protection-notice">
          <strong>{{ protectionModeLabel(account.protection_mode) }}</strong>
          <span>{{ protectionStatusText(account) }}</span>
          <small v-if="account.protection_last_error">最近一次执行失败：{{ account.protection_last_error }}</small>
        </div>

        <div class="account-actions">
          <button class="btn-ghost" type="button" @click="openEdit(account)">编辑</button>
          <button class="btn-danger" type="button" @click="removeAccount(account)">删除</button>
        </div>
      </article>
      <div v-if="!store.cloud.accounts.length" class="card empty-panel account-empty">还没有阿里云账户</div>
    </section>

    <section class="card instance-panel">
      <div class="panel-header">
        <div>
          <h2>CDT 中转候选实例</h2>
          <p>Agent 注册后会自动关联 ECS，可作为固定入口节点</p>
        </div>
        <span class="panel-code">ECS</span>
      </div>
      <div v-if="!store.cloud.instances.length" class="empty-panel">暂无实例数据</div>
      <div v-else class="instance-table">
        <div class="instance-head" aria-hidden="true">
          <span>实例</span><span>公网入口</span><span>规格</span><span>带宽</span><span>状态</span><span>操作</span>
        </div>
        <article v-for="instance in store.cloud.instances" :key="instance.instance_id" class="instance-row">
          <div class="instance-identity">
            <strong>{{ instance.instance_name || instance.instance_id }}</strong>
            <small>{{ instance.instance_id }}</small>
          </div>
          <div class="instance-cell" data-label="公网入口">{{ instance.public_ip || '无公网 IP' }}</div>
          <div class="instance-cell" data-label="规格">{{ instance.instance_type || '—' }}</div>
          <div class="instance-cell" data-label="带宽">{{ instance.bandwidth_mbps }} Mbps</div>
          <div class="instance-cell" data-label="状态">
            <span class="state-tag" :class="instance.status === 'Running' ? 'state-online' : ''">
              {{ instance.status === 'Running' ? '运行中' : '已停机' }}
            </span>
          </div>
          <div class="instance-action">
            <button
              class="btn-ghost border border-slate-200"
              type="button"
              @click="control(instance, instance.status === 'Running' ? 'stop' : 'start')"
            >
              {{ instance.status === 'Running' ? '停止' : '启动' }}
            </button>
          </div>
        </article>
      </div>
    </section>

    <Modal v-if="showForm" @close="showForm = false">
      <form class="space-y-5" @submit.prevent="saveAccount">
        <div>
          <div class="eyebrow">ALIYUN ACCOUNT</div>
          <h2 class="mt-1 text-lg font-bold text-slate-900">{{ editTarget ? '编辑阿里云账户' : '添加阿里云账户' }}</h2>
          <p class="mt-2 text-xs leading-5 text-slate-500">密钥仅由控制端保存，不会下发给中转 Agent。</p>
        </div>
        <div class="form-grid">
          <div class="field-wide"><label class="field-label">账户名称</label><input v-model.trim="form.name" class="input" required /></div>
          <div><label class="field-label">AccessKey ID</label><input v-model.trim="form.access_key_id" class="input" required autocomplete="off" /></div>
          <div><label class="field-label">AccessKey Secret</label><input v-model="form.access_key_secret" type="password" class="input" :required="!editTarget" :placeholder="editTarget ? '留空表示不修改' : ''" autocomplete="new-password" /></div>
          <div><label class="field-label">地域 ID</label><input v-model.trim="form.region_id" class="input" placeholder="cn-hongkong" required /></div>
          <div><label class="field-label">站点</label><select v-model="form.site_type" class="input"><option value="china">中国站</option><option value="international">国际站</option></select></div>
          <div class="field-wide"><label class="field-label">绑定实例 ID <span class="font-normal text-slate-400">（保活与停机保护共用）</span></label><input v-model.trim="form.instance_id" class="input" list="cloud-instance-options" placeholder="可选，输入 i-..." /><datalist id="cloud-instance-options"><option v-for="instance in store.cloud.instances" :key="instance.instance_id" :value="instance.instance_id">{{ instance.instance_name || instance.instance_id }}</option></datalist></div>
          <div><label class="field-label">账户 CDT 流量限额（GB）</label><input v-model.number="form.traffic_limit_gb" type="number" min="1" class="input" required /><p class="field-hint">按阿里云账号统计，不是每台 ECS 的独立额度。</p></div>
          <div><label class="field-label">保护阈值（%）</label><input v-model.number="form.threshold_percent" type="number" min="1" max="100" class="input" required /></div>
          <div class="field-wide">
            <label class="field-label">流量保护策略</label>
            <select v-model="form.protection_mode" class="input">
              <option value="alert_only">仅记录告警</option>
              <option value="drain_relay">停止接受新连接</option>
              <option value="stop_ecs">停止指定 ECS</option>
            </select>
            <p class="field-hint">新账户默认自动排空。达到阈值后从入口池撤下该账户的 Relay，并停止新连接；已建立的 TCP 连接会自然结束，月初流量恢复后自动重新开放。</p>
          </div>
          <div class="field-wide setting-toggle-row">
            <div><strong>抢占实例自动保活</strong><p class="field-hint">每分钟检查实例状态，发现被回收后自动尝试重新启动。</p></div>
            <button type="button" class="toggle" :class="form.keep_alive ? 'toggle-on' : ''" :aria-pressed="form.keep_alive" @click="form.keep_alive = !form.keep_alive"><span></span></button>
          </div>
          <div><label class="field-label">定时关机</label><input v-model="form.auto_stop_time" type="time" class="input" /></div>
          <div><label class="field-label">定时开机</label><input v-model="form.auto_start_time" type="time" class="input" /></div>
          <p v-if="form.protection_mode === 'stop_ecs'" class="field-wide field-hint">流量超过阈值后，会对上面绑定的实例发送一次停机指令；失败会在下次有效同步时重试。</p>
        </div>
        <div v-if="formError" class="notice notice-error">{{ formError }}</div>
        <div class="form-actions">
          <button type="button" class="btn-ghost border border-slate-200" @click="showForm = false">取消</button>
          <button class="btn-primary" :disabled="saving">{{ saving ? '保存中...' : '保存账户' }}</button>
        </div>
      </form>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import Modal from '../components/Modal.vue'

const store = useRelayStore()
const syncing = ref(false)
const showForm = ref(false)
const editTarget = ref(null)
const saving = ref(false)
const formError = ref('')
const message = ref('')
const messageType = ref('success')
let messageTimer

const enabledAccountCount = computed(() => store.cloud.accounts.filter(account => account.enabled).length)
const runningInstanceCount = computed(() => store.cloud.instances.filter(instance => instance.status === 'Running').length)
const totalTraffic = computed(() => store.cloud.traffic.reduce((sum, snapshot) => sum + snapshot.used_gb, 0))
const activeProtectionCount = computed(() => store.cloud.accounts.filter(account => account.protection_triggered).length)
const blank = () => ({
  name: '', access_key_id: '', access_key_secret: '', region_id: 'cn-hongkong', site_type: 'china',
  instance_id: '', traffic_limit_gb: 200, threshold_percent: 95, outstanding_threshold: 0,
  shutdown_mode: 'StopCharging', keep_alive: false, auto_start_time: '', auto_stop_time: '', protection_mode: 'drain_relay', enabled: true,
})
const form = ref(blank())

function trafficFor(id) {
  return store.cloud.traffic.find(item => item.account_id === id) || null
}

function instancesFor(id) {
  return store.cloud.instances.filter(item => item.account_id === id)
}

function trafficPercent(account) {
  const traffic = trafficFor(account.id)
  return traffic && account.traffic_limit_gb ? traffic.used_gb / account.traffic_limit_gb * 100 : 0
}

function trafficSummary(account) {
  const traffic = trafficFor(account.id)
  if (!traffic) return '尚无有效快照'
  const parts = [trafficPercent(account).toFixed(1) + '%', '账号维度']
  if (traffic.rate_gb_per_minute > 0) parts.push(traffic.rate_gb_per_minute.toFixed(3) + ' GB/分钟')
  if (traffic.minutes_to_threshold > 0) parts.push('预计 ' + Math.ceil(traffic.minutes_to_threshold) + ' 分钟到阈值')
  return parts.join(' · ')
}

function maskAccessKey(value) {
  if (!value || value.length < 8) return value || '未设置 AccessKey'
  return value.slice(0, 4) + '••••' + value.slice(-4)
}

function protectionModeLabel(mode) {
  return { alert_only: '仅告警', drain_relay: '停止新连接', stop_ecs: '停止 ECS' }[mode] || '仅告警'
}

function protectionStatusText(account) {
  if (account.protection_predictive) return '根据最近流量增速，预计将在控制与 DNS 生效窗口内达到阈值，已提前撤下入口并停止新连接。'
  if (account.protection_mode === 'drain_relay') return 'Agent 已停止接受新连接，已有 TCP 连接不被强制中断。'
  if (account.protection_mode === 'stop_ecs') {
    return account.protection_action_completed ? '目标 ECS 停机指令已发送。' : '正在等待发送或重试 ECS 停机指令。'
  }
  return '已记录阈值告警，云实例和转发服务保持运行。'
}

function openCreate() {
  editTarget.value = null
  form.value = blank()
  formError.value = ''
  showForm.value = true
}

function openEdit(account) {
  editTarget.value = account
  form.value = {
    name: account.name || '', access_key_id: account.access_key_id || '', access_key_secret: '',
    region_id: account.region_id || '', site_type: account.site_type || 'international',
    instance_id: account.instance_id || '', traffic_limit_gb: account.traffic_limit_gb || 200,
    threshold_percent: account.threshold_percent || 95, outstanding_threshold: account.outstanding_threshold || 0,
    shutdown_mode: account.shutdown_mode || 'StopCharging', keep_alive: !!account.keep_alive,
    auto_start_time: account.auto_start_time || '', auto_stop_time: account.auto_stop_time || '',
    protection_mode: account.protection_mode || 'drain_relay', enabled: account.enabled !== false,
  }
  formError.value = ''
  showForm.value = true
}

async function saveAccount() {
  saving.value = true
  formError.value = ''
  try {
    if (editTarget.value) await store.updateCloudAccount(editTarget.value.id, form.value)
    else await store.createCloudAccount(form.value)
    showForm.value = false
    showMessage('账户已保存')
  } catch (error) {
    formError.value = error.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

async function removeAccount(account) {
  if (!confirm('确认删除账户“' + account.name + '”及其同步数据？')) return
  try {
    await store.deleteCloudAccount(account.id)
    showMessage('账户已删除')
  } catch (error) {
    showMessage(error.response?.data?.error || '删除失败', 'error')
  }
}

async function sync() {
  syncing.value = true
  try {
    const results = await store.syncCloud()
    const failed = results.filter(item => item.error)
    const protectedCount = results.filter(item => item.protection_triggered && item.protection_action).length
    const successMessage = protectedCount ? '同步完成，' + protectedCount + ' 个账户已进入流量保护' : '同步完成'
    showMessage(failed.length ? failed.length + ' 个账户部分同步失败，旧数据已保留' : successMessage, failed.length ? 'error' : 'success')
  } catch (error) {
    showMessage(error.response?.data?.error || '同步失败', 'error')
  } finally {
    syncing.value = false
  }
}

async function control(instance, action) {
  try {
    await store.controlCloudInstance(instance.instance_id, action)
    showMessage(action === 'start' ? '启动指令已发送' : '停止指令已发送')
  } catch (error) {
    showMessage(error.response?.data?.error || '操作失败', 'error')
  }
}

function showMessage(text, type = 'success') {
  message.value = text
  messageType.value = type
  clearTimeout(messageTimer)
  messageTimer = setTimeout(() => { message.value = '' }, 5000)
}

onMounted(() => store.fetchCloud())
</script>

<style scoped>
.cloud-page { display: grid; gap: 20px; }
.page-header { display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }
.page-title { margin-top: 5px; color: #172033; font-size: clamp(24px, 3vw, 30px); font-weight: 750; letter-spacing: -.035em; }
.page-subtitle { margin-top: 5px; color: #64748b; font-size: 12px; line-height: 1.6; }
.header-actions { display: flex; flex: 0 0 auto; gap: 8px; }
.summary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.summary-card { min-width: 0; border: 1px solid #e5eaf1; border-radius: 13px; background: #fff; padding: 17px 19px; box-shadow: 0 5px 20px rgba(15, 23, 42, .035); }
.summary-card > span { display: block; color: #64748b; font-size: 10px; font-weight: 700; }
.summary-card strong { display: block; margin-top: 8px; overflow: hidden; color: #172033; font-size: clamp(20px, 2.4vw, 26px); font-weight: 750; letter-spacing: -.035em; text-overflow: ellipsis; white-space: nowrap; }
.summary-card small { display: block; margin-top: 5px; color: #94a3b8; font-size: 10px; }
.account-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.account-card { min-width: 0; padding: 19px; }
.account-protected { border-color: #fbbf24; box-shadow: 0 6px 22px rgba(180, 83, 9, .08); }
.account-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.account-head h2 { overflow: hidden; color: #1e293b; font-size: 14px; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.account-head p { margin-top: 5px; overflow: hidden; color: #94a3b8; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.account-badges { display: flex; flex: 0 0 auto; flex-wrap: wrap; justify-content: flex-end; gap: 5px; }
.site-tag, .state-tag, .protection-tag { display: inline-flex; flex: 0 0 auto; align-items: center; border-radius: 999px; background: #f1f5f9; padding: 5px 9px; color: #64748b; font-size: 9px; font-weight: 750; white-space: nowrap; }
.protection-tag-active { background: #fef3c7; color: #a16207; }
.state-online { background: #ecfdf3; color: #15803d; }
.traffic-row { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; margin-top: 21px; }
.traffic-scope { display: block; margin-bottom: 3px; color: #64748b; font-size: 10px; font-weight: 650; }
.traffic-value { color: #172033; font-size: clamp(23px, 3vw, 29px); font-weight: 750; letter-spacing: -.045em; }
.traffic-unit { margin-left: 5px; color: #94a3b8; font-size: 11px; }
.account-limits { display: grid; gap: 3px; margin: 0; font-size: 9px; }
.account-limits div { display: flex; justify-content: flex-end; gap: 8px; }
.account-limits dt { color: #94a3b8; }
.account-limits dd { margin: 0; color: #64748b; font-weight: 650; }
.traffic-track { height: 7px; margin-top: 13px; overflow: hidden; border-radius: 999px; background: #e7edf5; }
.traffic-fill { height: 100%; border-radius: inherit; background: #2563eb; transition: width .25s ease; }
.traffic-danger { background: #dc2626; }
.traffic-meta { display: flex; justify-content: space-between; margin-top: 7px; color: #94a3b8; font-size: 9px; }
.traffic-disclaimer { margin-top: 9px; color: #94a3b8; font-size: 9px; line-height: 1.5; }
.sync-warning { display: grid; gap: 3px; margin-top: 12px; border: 1px solid #fde68a; border-radius: 9px; background: #fffbeb; padding: 9px 11px; color: #a16207; font-size: 9px; line-height: 1.55; }
.protection-notice { display: grid; gap: 3px; margin-top: 12px; border: 1px solid #fed7aa; border-radius: 9px; background: #fff7ed; padding: 10px 11px; color: #9a3412; font-size: 9px; line-height: 1.55; }
.protection-notice small { color: #c2410c; }
.account-actions { display: flex; justify-content: flex-end; gap: 4px; margin-top: 15px; border-top: 1px solid #f1f5f9; padding-top: 11px; }
.account-actions button { padding: 6px 9px; font-size: 11px; }
.account-empty { grid-column: 1 / -1; }
.instance-panel { min-width: 0; overflow: hidden; }
.panel-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 17px 19px; border-bottom: 1px solid #eef2f7; }
.panel-header h2 { color: #1e293b; font-size: 13px; font-weight: 750; }
.panel-header p { margin-top: 4px; color: #94a3b8; font-size: 10px; line-height: 1.5; }
.panel-code { border-radius: 6px; background: #eff6ff; padding: 5px 7px; color: #2563eb; font-size: 8px; font-weight: 800; }
.instance-head, .instance-row { display: grid; grid-template-columns: minmax(170px, 1.35fr) minmax(115px, 1fr) minmax(120px, 1fr) 80px 78px 70px; align-items: center; gap: 12px; padding: 11px 18px; }
.instance-head { background: #f8fafc; color: #94a3b8; font-size: 9px; font-weight: 700; }
.instance-row { border-top: 1px solid #f1f5f9; color: #64748b; font-size: 10px; }
.instance-identity { min-width: 0; }
.instance-identity strong, .instance-identity small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.instance-identity strong { color: #334155; font-size: 11px; font-weight: 700; }
.instance-identity small { margin-top: 4px; color: #94a3b8; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 8px; }
.instance-cell { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.instance-action { display: flex; justify-content: flex-end; }
.instance-action button { padding: 5px 8px; font-size: 10px; }
.empty-panel { padding: 48px 20px; text-align: center; color: #94a3b8; font-size: 12px; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.field-wide { grid-column: 1 / -1; }
.field-hint { margin-top: 6px; color: #94a3b8; font-size: 10px; line-height: 1.55; }
.form-actions { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid #f1f5f9; padding-top: 16px; }

@media (max-width: 900px) {
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .summary-card:last-child { grid-column: 1 / -1; }
  .account-grid { grid-template-columns: minmax(0, 1fr); }
  .instance-head { display: none; }
  .instance-table { display: grid; gap: 10px; padding: 11px; background: #f8fafc; }
  .instance-row { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 11px 18px; border: 1px solid #e5eaf1; border-radius: 10px; background: #fff; padding: 14px; }
  .instance-identity { grid-column: 1 / -1; padding-bottom: 9px; border-bottom: 1px solid #f1f5f9; }
  .instance-cell { display: grid; gap: 4px; overflow: visible; white-space: normal; }
  .instance-cell::before { content: attr(data-label); color: #94a3b8; font-size: 8px; font-weight: 700; }
  .instance-action { grid-column: 1 / -1; justify-content: flex-start; padding-top: 2px; }
}

@media (max-width: 639px) {
  .cloud-page { gap: 16px; }
  .page-header { align-items: stretch; flex-direction: column; gap: 14px; }
  .header-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); width: 100%; }
  .summary-grid { grid-template-columns: minmax(0, 1fr); gap: 10px; }
  .summary-card:last-child { grid-column: auto; }
  .account-card { padding: 16px; }
  .traffic-row { align-items: flex-start; flex-direction: column; gap: 9px; }
  .account-limits { width: 100%; }
  .account-limits div { justify-content: space-between; }
  .account-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .panel-header { padding: 15px; }
  .instance-table { padding: 9px; }
  .instance-row { grid-template-columns: minmax(0, 1fr); }
  .instance-identity, .instance-action { grid-column: auto; }
  .instance-action button { width: 100%; }
  .form-grid { grid-template-columns: minmax(0, 1fr); }
  .field-wide { grid-column: auto; }
  .form-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>

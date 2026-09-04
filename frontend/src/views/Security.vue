<template>
  <div class="security-page space-y-5 fade-in">
    <header class="security-header">
      <div><div class="eyebrow">SECURITY CENTER</div><h1 class="page-title">安全中心</h1><p class="page-subtitle">管理员密码、登录保护和活动会话统一管理</p></div>
      <span class="security-status"><span class="status-dot status-dot-success"></span>基础防护已启用</span>
    </header>

    <section class="security-grid">
      <article class="card security-metric"><span class="metric-icon">S</span><div><strong>强密码策略</strong><small>新密码至少 10 位，必须包含大小写字母和数字</small></div><b class="metric-ok">已启用</b></article>
      <article class="card security-metric"><span class="metric-icon">T</span><div><strong>登录失败保护</strong><small>同一来源 15 分钟内失败 8 次后暂时锁定</small></div><b class="metric-ok">已启用</b></article>
      <article class="card security-metric"><span class="metric-icon">H</span><div><strong>浏览器安全响应头</strong><small>禁止嵌套、阻止嗅探并限制跨源权限</small></div><b class="metric-ok">已启用</b></article>
    </section>

    <article class="card security-card twofa-card">
      <div class="card-heading"><div><h2>双因素认证（2FA）</h2><p>为管理员登录增加身份验证器验证码。启用或关闭后会撤销全部已有会话。</p></div><span class="panel-code">TOTP</span></div>
      <div v-if="twoFAEnabled" class="twofa-enabled"><span class="twofa-check">✓</span><div><strong>双因素认证已启用</strong><small>每次管理员登录都需要密码和 6 位验证码。</small></div><button class="btn-danger" :disabled="twoFALoading" @click="disableTwoFA">关闭 2FA</button></div>
      <template v-else-if="twoFASetup">
        <div class="twofa-setup"><div><strong>1. 在身份验证器中添加账户</strong><p>无法扫码时，可以手动输入下方密钥。</p><code>{{ twoFASetup.secret }}</code></div><div><strong>2. 输入一次性验证码确认</strong><p class="twofa-uri">{{ twoFASetup.otpauth_uri }}</p><div class="twofa-confirm"><input v-model.trim="twoFACode" class="input" inputmode="numeric" maxlength="6" placeholder="6 位验证码" /><button class="btn-primary" :disabled="twoFALoading" @click="confirmTwoFA">{{ twoFALoading ? '确认中...' : '确认启用' }}</button></div></div></div>
      </template>
      <div v-else class="twofa-disabled"><div><strong>尚未启用双因素认证</strong><small>推荐管理员使用身份验证器保护控制台。</small></div><button class="btn-primary" :disabled="twoFALoading" @click="beginTwoFA">{{ twoFALoading ? '生成中...' : '开始设置 2FA' }}</button></div>
    </article>

    <section class="security-columns">
      <article class="card security-card">
        <div class="card-heading"><div><h2>修改管理员密码</h2><p>修改后所有管理员会话都会立即失效，需要重新登录。</p></div><span class="panel-code">PASSWORD</span></div>
        <form class="password-form" @submit.prevent="changePassword">
          <div><label class="field-label">当前密码</label><input v-model="passwordForm.current_password" type="password" class="input" autocomplete="current-password" required /></div>
          <div><label class="field-label">新密码</label><input v-model="passwordForm.new_password" type="password" class="input" autocomplete="new-password" minlength="10" required /><div class="strength-track"><span :class="`strength-${passwordStrength.level}`" :style="{ width: `${passwordStrength.percent}%` }"></span></div><small class="strength-label">{{ passwordStrength.label }}</small></div>
          <div><label class="field-label">确认新密码</label><input v-model="passwordForm.confirm_password" type="password" class="input" autocomplete="new-password" required /></div>
          <div v-if="passwordError" class="notice notice-error">{{ passwordError }}</div>
          <button class="btn-primary" :disabled="passwordSaving">{{ passwordSaving ? '更新中...' : '更新密码并撤销会话' }}</button>
        </form>
        <div class="security-note"><strong>安全提示</strong><span>不要复用云账号、SSH 或数据库密码。建议使用密码管理器生成随机密码。</span></div>
      </article>

      <article class="card security-card">
        <div class="card-heading"><div><h2>活动会话</h2><p>只显示安全摘要，不会返回任何会话凭据。</p></div><span class="session-count">{{ sessions.length }} 个</span></div>
        <div v-if="sessionsLoading" class="empty-panel">读取会话中...</div>
        <div v-else-if="!sessions.length" class="empty-panel">当前没有数据库会话。使用管理员 Token 登录时不会创建会话。</div>
        <div v-else class="session-list">
          <div v-for="session in sessions" :key="session.id" class="session-row"><span class="session-device">{{ session.current ? '本' : '会' }}</span><div class="min-w-0 flex-1"><strong>{{ session.current ? '当前浏览器会话' : `管理员会话 ${session.id}` }}</strong><small>{{ session.ip_address || '未知 IP' }} · {{ uaLabel(session.user_agent) }} · 创建于 {{ formatTime(session.created_at) }} · {{ session.current ? '当前正在使用' : `将于 ${formatTime(session.expires_at)} 过期` }}</small></div><span v-if="session.current" class="current-pill">当前</span><button v-else class="btn-danger px-2 py-1 text-xs" :disabled="sessionBusy === session.id" @click="revoke(session)">{{ sessionBusy === session.id ? '撤销中' : '撤销' }}</button></div>
        </div>
        <button v-if="sessions.length > 1" class="btn-danger mt-4 w-full border border-red-100" :disabled="sessionBusy === 'all'" @click="revokeOthers">{{ sessionBusy === 'all' ? '撤销中...' : '撤销其他全部会话' }}</button>
      </article>
    </section>

    <div class="notice notice-info"><strong>关于环境管理员 Token：</strong> `CDT_ADMIN_TOKEN` 是宿主机应急凭据，不属于浏览器会话，不能在此页面撤销。生产环境请将它保存在密钥管理器中，并定期更换。</div>
    <div v-if="message" class="notice" :class="messageType === 'error' ? 'notice-error' : 'notice-success'">{{ message }}</div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useRelayStore } from '../stores/relay'

const router = useRouter()
const store = useRelayStore()
const sessions = ref([])
const sessionsLoading = ref(true)
const twoFAEnabled = ref(false)
const twoFASetup = ref(null)
const twoFACode = ref('')
const twoFALoading = ref(false)
const sessionBusy = ref('')
const passwordSaving = ref(false)
const passwordError = ref('')
const message = ref('')
const messageType = ref('success')
const passwordForm = ref({ current_password: '', new_password: '', confirm_password: '' })

const passwordStrength = computed(() => {
  const value = passwordForm.value.new_password || ''
  let score = 0
  if (value.length >= 10) score++
  if (/[a-z]/.test(value)) score++
  if (/[A-Z]/.test(value)) score++
  if (/\d/.test(value)) score++
  if (/[^a-zA-Z\d]/.test(value)) score++
  return { level: score >= 5 ? 'strong' : score >= 3 ? 'medium' : 'weak', percent: Math.min(100, score * 20), label: !value ? '请输入新密码' : score >= 5 ? '密码强度：强' : score >= 3 ? '密码强度：中' : '密码强度：弱' }
})

function formatTime(value) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未知时间' }
function uaLabel(value) { const ua = String(value || ''); if (!ua) return '未知设备'; if (/iphone|ipad/i.test(ua)) return 'iOS 设备'; if (/android/i.test(ua)) return 'Android 设备'; if (/windows/i.test(ua)) return 'Windows 浏览器'; if (/mac os/i.test(ua)) return 'macOS 浏览器'; return '浏览器' }
async function loadSessions() { sessionsLoading.value = true; try { sessions.value = await store.fetchAdminSessions() } catch (error) { showMessage(error.response?.data?.error || '会话读取失败', 'error') } finally { sessionsLoading.value = false } }
async function loadTwoFA() { try { const data = await store.fetchAdminTwoFA(); twoFAEnabled.value = Boolean(data.enabled) } catch (error) { showMessage(error.response?.data?.error || '2FA 状态读取失败', 'error') } }
async function beginTwoFA() { twoFALoading.value = true; try { twoFASetup.value = await store.beginAdminTwoFA(); twoFACode.value = '' } catch (error) { showMessage(error.response?.data?.error || '2FA 初始化失败', 'error') } finally { twoFALoading.value = false } }
async function confirmTwoFA() { if (!/^\d{6}$/.test(twoFACode.value)) { showMessage('请输入 6 位验证码', 'error'); return }; twoFALoading.value = true; try { await store.confirmAdminTwoFA(twoFACode.value); forceLogout() } catch (error) { showMessage(error.response?.data?.error || '验证码无效', 'error') } finally { twoFALoading.value = false } }
async function disableTwoFA() { const code = window.prompt('请输入当前身份验证器中的 6 位验证码以关闭 2FA'); if (!code) return; twoFALoading.value = true; try { await store.disableAdminTwoFA(code); forceLogout() } catch (error) { showMessage(error.response?.data?.error || '验证码无效', 'error') } finally { twoFALoading.value = false } }
function forceLogout() { localStorage.removeItem('token'); localStorage.removeItem('role'); localStorage.removeItem('username'); localStorage.removeItem('displayName'); router.push('/login') }
async function changePassword() {
  passwordError.value = ''
  if (passwordForm.value.new_password !== passwordForm.value.confirm_password) { passwordError.value = '两次输入的新密码不一致'; return }
  if (passwordStrength.value.level === 'weak') { passwordError.value = '新密码必须至少 10 位，并包含大小写字母和数字'; return }
  passwordSaving.value = true
  try {
    await store.changeAdminPassword({ current_password: passwordForm.value.current_password, new_password: passwordForm.value.new_password })
    localStorage.removeItem('token'); localStorage.removeItem('role'); localStorage.removeItem('username'); localStorage.removeItem('displayName')
    router.push('/login')
  } catch (error) { passwordError.value = error.response?.data?.error || '密码更新失败' } finally { passwordSaving.value = false }
}
async function revoke(session) {
  if (!window.confirm('确认撤销这个管理员会话？该浏览器将立即退出。')) return
  sessionBusy.value = session.id
  try { await store.revokeAdminSession(session.id); sessions.value = sessions.value.filter(item => item.id !== session.id); showMessage('会话已撤销') } catch (error) { showMessage(error.response?.data?.error || '会话撤销失败', 'error') } finally { sessionBusy.value = '' }
}
async function revokeOthers() {
  if (!window.confirm('确认撤销其他全部管理员会话？其他浏览器将立即退出。')) return
  sessionBusy.value = 'all'
  try { await store.revokeOtherAdminSessions(); sessions.value = sessions.value.filter(session => session.current); showMessage('其他管理员会话已全部撤销') } catch (error) { showMessage(error.response?.data?.error || '会话撤销失败', 'error') } finally { sessionBusy.value = '' }
}
function showMessage(text, type = 'success') { message.value = text; messageType.value = type; window.setTimeout(() => { message.value = '' }, 5000) }
onMounted(() => Promise.all([loadSessions(), loadTwoFA()]))
</script>

<style scoped>
.security-page{min-width:0}.security-header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px}.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:6px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.security-status{display:inline-flex;align-items:center;gap:8px;color:#15803d;font-size:11px;font-weight:650}.status-dot{width:7px;height:7px;border-radius:50%;background:#94a3b8}.status-dot-success{background:#16a34a;box-shadow:0 0 0 3px #dcfce7}.security-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.security-metric{display:flex;align-items:center;gap:10px;padding:15px}.metric-icon{display:grid;width:31px;height:31px;flex:0 0 auto;place-items:center;border-radius:8px;background:#eff6ff;color:#2563eb;font-size:11px;font-weight:800}.security-metric>div{display:grid;min-width:0;gap:3px;flex:1}.security-metric strong{color:#334155;font-size:11px}.security-metric small{color:#94a3b8;font-size:9px;line-height:1.4}.metric-ok{border-radius:999px;background:#ecfdf3;padding:4px 7px;color:#15803d;font-size:9px;white-space:nowrap}.security-columns{display:grid;grid-template-columns:minmax(0,1fr) minmax(360px,.9fr);gap:16px}.security-card{min-width:0;padding:20px}.twofa-card{border-top:2px solid #a5b4fc}.twofa-enabled,.twofa-disabled{display:flex;align-items:center;gap:12px}.twofa-enabled>div,.twofa-disabled>div{display:grid;min-width:0;gap:4px;flex:1}.twofa-enabled strong,.twofa-disabled strong{color:#334155;font-size:11px}.twofa-enabled small,.twofa-disabled small{color:#94a3b8;font-size:9px}.twofa-check{display:grid;width:30px;height:30px;place-items:center;border-radius:50%;background:#dcfce7;color:#15803d;font-weight:800}.twofa-setup{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.twofa-setup>div{display:grid;align-content:start;gap:5px;border-radius:9px;background:#f8fafc;padding:12px}.twofa-setup strong{color:#475569;font-size:10px}.twofa-setup p{color:#94a3b8;font-size:9px;line-height:1.4}.twofa-setup code{overflow:auto;border-radius:6px;background:#fff;padding:8px;color:#1e3a8a;font-size:10px;word-break:break-all}.twofa-uri{max-height:40px;overflow:auto;word-break:break-all}.twofa-confirm{display:flex;gap:7px}.twofa-confirm .input{min-width:0;font-size:11px}.card-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:18px}.card-heading h2{color:#1e293b;font-size:15px;font-weight:700}.card-heading p{margin-top:4px;color:#94a3b8;font-size:10px;line-height:1.5}.panel-code,.session-count{border-radius:999px;background:#eff6ff;padding:5px 8px;color:#2563eb;font-size:9px;font-weight:800}.session-count{background:#f1f5f9;color:#64748b}.password-form{display:grid;gap:14px;max-width:520px}.strength-track{height:4px;margin-top:8px;overflow:hidden;border-radius:999px;background:#e2e8f0}.strength-track span{display:block;height:100%;border-radius:inherit;transition:width .2s ease}.strength-weak{background:#dc2626}.strength-medium{background:#d97706}.strength-strong{background:#16a34a}.strength-label{display:block;margin-top:5px;color:#94a3b8;font-size:9px}.security-note{display:grid;gap:3px;margin-top:20px;border-radius:9px;background:#f8fafc;padding:11px;color:#64748b;font-size:10px;line-height:1.5}.security-note strong{color:#475569;font-size:10px}.session-list{display:grid;gap:7px}.session-row{display:flex;min-width:0;align-items:center;gap:9px;border-radius:9px;background:#f8fafc;padding:10px}.session-device{display:grid;width:27px;height:27px;flex:0 0 auto;place-items:center;border-radius:8px;background:#e0e7ff;color:#4f46e5;font-size:10px;font-weight:800}.session-row strong{display:block;color:#475569;font-size:10px}.session-row small{display:block;margin-top:3px;color:#94a3b8;font-size:9px}.current-pill{border-radius:999px;background:#ecfdf3;padding:4px 7px;color:#15803d;font-size:9px;font-weight:700}.empty-panel{padding:35px 8px;text-align:center;color:#94a3b8;font-size:11px}@media(max-width:900px){.security-grid,.security-columns{grid-template-columns:1fr}}@media(max-width:639px){.security-header{align-items:flex-start;flex-direction:column}.security-metric{padding:13px}.security-metric small{font-size:8px}.security-card{padding:16px}.twofa-setup{grid-template-columns:minmax(0,1fr)}.twofa-disabled,.twofa-enabled{align-items:flex-start;flex-wrap:wrap}.twofa-confirm{flex-wrap:wrap}.twofa-confirm .input{flex:1}}
</style>

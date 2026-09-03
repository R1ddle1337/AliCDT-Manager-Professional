<template>
  <div class="space-y-6 fade-in settings-page">
    <div><div class="eyebrow">PREFERENCES</div><h1 class="page-title">系统设置</h1><p class="page-subtitle">配置通知和自动化选项</p></div>

    <div class="settings-card-grid layout-collection layout-collection--strip">
    <div class="card settings-card layout-card">
      <div class="section-heading"><div><h2>Telegram 通知</h2><p>将熔断、保活和定时任务结果发送到指定会话。</p></div><span class="section-code">TG</span></div>
      <div class="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2"><div><label class="field-label" for="bot-token">Bot Token</label><input id="bot-token" v-model="form.tg_bot_token" class="input" placeholder="123456:ABC..." /></div><div><label class="field-label" for="chat-id">Chat ID</label><input id="chat-id" v-model="form.tg_chat_id" class="input" placeholder="123..." /></div></div>
      <div class="setting-row mt-5"><div><div class="text-sm font-semibold text-slate-700">每日流量汇报</div><div class="mt-1 text-xs text-slate-500">每天北京时间 00:00 推送所有实例的流量摘要。</div></div><button type="button" class="toggle" :class="form.tg_daily_report === '1' ? 'toggle-on' : ''" :aria-pressed="form.tg_daily_report === '1'" @click="form.tg_daily_report = form.tg_daily_report === '1' ? '0' : '1'"><span></span></button></div>
      <div class="mt-4 rounded-lg bg-slate-50 p-3 text-xs leading-6 text-slate-500"><div class="font-semibold text-slate-600">通知触发条件</div><div>流量熔断自动停机、抢占式实例被回收、定时开关机执行。</div><div :class="form.tg_daily_report === '1' ? 'text-accent' : 'text-slate-400'">每日流量汇报：{{ form.tg_daily_report === '1' ? '已开启' : '已关闭' }}</div></div>
      <div class="mt-5 flex flex-wrap gap-2"><button type="button" @click="save" :disabled="saving" class="btn-primary">{{ saving ? '保存中...' : '保存设置' }}</button><button type="button" @click="testTg" :disabled="testing" class="btn-ghost border border-slate-200">{{ testing ? '发送中...' : '发送测试消息' }}</button><button type="button" @click="testDailyReport" :disabled="reportTesting" class="btn-ghost border border-slate-200">{{ reportTesting ? '发送中...' : '发送测试汇报' }}</button></div>
    </div>

    <div class="card settings-card layout-card">
      <div class="section-heading"><div><h2>管理员安全</h2><p>密码轮换、活动会话和双因素认证已集中到安全中心。</p></div><span class="section-code">SEC</span></div>
      <div class="mt-5 rounded-lg bg-slate-50 p-3 text-xs leading-6 text-slate-500">为避免重复配置，系统设置只保留通知和自动化选项；所有管理员安全操作请在安全中心完成。</div>
      <button type="button" @click="$router.push('/security')" class="btn-primary mt-4">打开安全中心</button>
    </div>
    </div>

    <div v-if="msg.text" class="notice" :class="`notice-${msg.type}`">{{ msg.text }}</div>
    <div class="text-center text-xs text-slate-400">AliCDT Manager v1.2 <a v-if="versionInfo.has_update" :href="versionInfo.url" target="_blank" class="ml-2 text-accent hover:underline">发现新版本 v{{ versionInfo.latest }}</a></div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useStore } from '../stores'
import { apiErrorMessage } from '../utils/session'

const store = useStore(); const saving = ref(false); const testing = ref(false); const reportTesting = ref(false); const msg = ref({ type: 'success', text: '' })
const form = ref({ tg_bot_token: '', tg_chat_id: '', tg_daily_report: '0' }); const versionInfo = ref({ has_update: false, latest: '', url: '' })
onMounted(async () => { await store.fetchSettings(); form.value.tg_bot_token = store.settings.tg_bot_token || ''; form.value.tg_chat_id = store.settings.tg_chat_id || ''; form.value.tg_daily_report = store.settings.tg_daily_report || '0'; checkVersion() })
function showMessage(type, text, timeout = 4000) { msg.value = { type, text }; window.setTimeout(() => { msg.value = { type: 'success', text: '' } }, timeout) }
async function checkVersion() { try { versionInfo.value = await store.fetchVersionInfo() } catch (_) { /* version discovery is optional */ } }
function settingItems() { return Object.entries(form.value).map(([key, value]) => ({ key, value })) }
async function save() { saving.value = true; try { await store.saveSettings(settingItems()); showMessage('success', '设置已保存') } catch (e) { showMessage('error', '保存失败：' + apiErrorMessage(e)) } finally { saving.value = false } }
async function testTg() { testing.value = true; try { await store.saveSettings(settingItems()); await store.testTelegram(); showMessage('success', '测试消息已发送，请检查 Telegram') } catch (e) { showMessage('error', '发送失败：' + apiErrorMessage(e)) } finally { testing.value = false } }
async function testDailyReport() { reportTesting.value = true; try { await store.testDailyReport(); showMessage('success', '测试汇报已发送，请检查 Telegram') } catch (e) { showMessage('error', '发送失败：' + apiErrorMessage(e)) } finally { reportTesting.value = false } }
</script>

<style scoped>
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }.page-title { margin-top: 6px; color: #172033; font-size: 26px; font-weight: 750; letter-spacing: -.03em; }.page-subtitle { margin-top: 6px; color: #64748b; font-size: 13px; }.settings-card-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }.settings-card { padding: 22px; }.section-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }.section-heading h2 { color: #1e293b; font-size: 15px; font-weight: 700; }.section-heading p { margin-top: 4px; color: #94a3b8; font-size: 12px; }.section-code { display: inline-flex; align-items: center; justify-content: center; border-radius: 7px; background: #eff6ff; padding: 6px 8px; color: #2563eb; font-size: 9px; font-weight: 800; letter-spacing: .08em; }.setting-row { display: flex; align-items: center; justify-content: space-between; gap: 20px; border-top: 1px solid #eef2f7; padding-top: 18px; }.toggle { position: relative; display: inline-flex; width: 40px; height: 23px; flex: 0 0 auto; border: 0; border-radius: 999px; background: #cbd5e1; transition: transform .15s ease, background .15s ease; }.toggle span { position: absolute; top: 3px; left: 3px; width: 17px; height: 17px; border-radius: 50%; background: #fff; box-shadow: 0 1px 3px rgba(15,23,42,.2); transition: transform .15s ease; }.toggle-on { background: #2563eb; }.toggle-on span { transform: translateX(17px); }
@media (max-width: 900px) { .settings-card-grid { grid-template-columns: minmax(0, 1fr); } }
</style>

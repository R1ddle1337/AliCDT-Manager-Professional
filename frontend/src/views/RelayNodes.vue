<template>
  <div class="space-y-5 fade-in">
    <header class="flex flex-wrap items-end justify-between gap-4"><div><div class="eyebrow">CDT RELAYS</div><h1 class="page-title">中转节点</h1><p class="page-subtitle">安装在阿里云 CDT ECS 上的 Go Relay Agent</p></div><div class="flex flex-wrap gap-2"><button class="btn-ghost border border-slate-200" @click="showUpgrade = !showUpgrade">{{ showUpgrade ? '收起升级命令' : '升级已安装 Agent' }}</button><button class="btn-primary" @click="generateToken">添加中转节点</button></div></header>
    <div v-if="showUpgrade" class="card p-5"><div class="flex items-start justify-between gap-4"><div><h2 class="text-sm font-bold text-slate-800">已安装 Agent 一次性升级</h2><p class="mt-1 text-xs leading-5 text-slate-500">旧版 Agent 首次升级需要在对应 CDT 服务器 root 终端执行。升级会保留现有凭证和配置，之后由 Agent 自动检查更新。</p></div><button class="btn-ghost border border-slate-200 text-xs" @click="copyUpgradeCommand">复制</button></div><pre class="command-box mt-4">{{ upgradeCommand }}</pre></div>
    <div v-if="installCommand" class="card p-5"><div class="flex items-start justify-between gap-4"><div><h2 class="text-sm font-bold text-slate-800">SSH 安装命令</h2><p class="mt-1 text-xs text-slate-500">在目标 CDT 服务器 root 终端执行；注册码 {{ tokenTTL }} 分钟内有效且只能使用一次。</p></div><button class="btn-ghost border border-slate-200 text-xs" @click="copyCommand">复制</button></div><pre class="command-box mt-4">{{ installCommand }}</pre></div>
    <div v-if="error" class="notice notice-error">{{ error }}</div>
    <div class="grid grid-cols-1 gap-4 xl:grid-cols-2"><article v-for="node in store.relayNodes" :key="node.id" class="card p-5"><div class="flex items-start justify-between gap-4"><div class="min-w-0"><div class="flex items-center gap-2"><span class="status-dot" :class="node.status === 'online' ? 'status-dot-success' : 'status-dot-muted'"></span><h2 class="truncate font-bold text-slate-800">{{ node.name }}</h2></div><p class="mt-2 font-mono text-[11px] text-slate-400">{{ node.id }}</p></div><span class="state-tag" :class="node.status === 'online' ? 'state-online' : ''">{{ node.status === 'online' ? '在线' : '离线' }}</span></div><div class="mt-5 grid grid-cols-2 gap-2 sm:grid-cols-4"><div class="info-box"><span>入口 IP</span><strong>{{ node.public_ip || '待设置' }}</strong></div><div class="info-box"><span>系统</span><strong>{{ node.os }}/{{ node.architecture }}</strong></div><div class="info-box"><span>配置版本</span><strong>{{ node.current_revision }}/{{ node.desired_revision }}</strong></div><div class="info-box"><span>Agent</span><strong>{{ node.agent_version || '—' }}</strong></div></div><div class="mt-4 border-t border-slate-100 pt-3 text-[11px] text-slate-400">最后心跳：{{ formatTime(node.last_seen_at) }}<span v-if="node.update_status && node.update_status !== 'idle'" class="ml-3" :class="node.update_status === 'failed' ? 'text-red-600' : 'text-blue-600'">Agent 更新：{{ updateLabel(node.update_status) }}<span v-if="node.update_error">（{{ node.update_error }}）</span></span></div></article><div v-if="!store.relayNodes.length" class="card empty-panel xl:col-span-2">尚未注册中转节点</div></div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
const store=useRelayStore();const token=ref('');const tokenTTL=30;const error=ref('');const showUpgrade=ref(false)
const installCommand=computed(()=>token.value?`curl -fsSL https://${window.location.host}/agent/install.sh | sh -s -- --server https://${window.location.host} --token ${token.value}`: '')
const upgradeCommand=computed(()=>`curl -fsSL https://${window.location.host}/agent/upgrade.sh | sh -s -- --server https://${window.location.host}`)
async function generateToken(){error.value='';try{const data=await store.createEnrollmentToken(tokenTTL);token.value=data.token}catch(e){error.value=e.response?.data?.error||'生成注册码失败'}}
async function copyCommand(){await navigator.clipboard.writeText(installCommand.value)}
async function copyUpgradeCommand(){await navigator.clipboard.writeText(upgradeCommand.value)}
function formatTime(value){return value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'尚未上报'}
function updateLabel(value){return {draining:'排空中',updating:'更新中',failed:'更新失败'}[value]||value}
onMounted(()=>store.fetchRelayNodes())
</script>

<style scoped>
.eyebrow{color:#2563eb;font-size:10px;font-weight:800;letter-spacing:.16em}.page-title{margin-top:5px;color:#172033;font-size:27px;font-weight:750;letter-spacing:-.03em}.page-subtitle{margin-top:5px;color:#64748b;font-size:12px}.command-box{overflow:auto;border-radius:9px;background:#0f172a;padding:14px;color:#dbeafe;font-size:11px;line-height:1.7;white-space:pre-wrap}.state-tag{border-radius:999px;background:#f1f5f9;padding:5px 9px;color:#64748b;font-size:10px;font-weight:700}.state-online{background:#ecfdf3;color:#15803d}.info-box{min-width:0;border-radius:8px;background:#f8fafc;padding:9px}.info-box span{display:block;color:#94a3b8;font-size:9px}.info-box strong{display:block;overflow:hidden;margin-top:4px;color:#475569;text-overflow:ellipsis;white-space:nowrap;font-size:10px}.empty-panel{padding:52px;text-align:center;color:#94a3b8;font-size:12px}
</style>

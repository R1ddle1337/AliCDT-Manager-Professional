<template>
  <div class="space-y-6 fade-in">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div><div class="eyebrow">AUDIT LOG</div><h1 class="page-title">系统日志</h1><p class="page-subtitle">查看自动化任务和账户操作记录</p></div>
      <div class="flex flex-wrap items-center gap-2"><select v-model="category" @change="load" class="input w-36 py-2"><option value="">全部分类</option><option value="traffic">流量</option><option value="keepalive">保活</option><option value="scheduler">定时任务</option><option value="ddns">DDNS</option><option value="notify">通知</option><option value="system">系统</option></select><button type="button" @click="load" class="btn-ghost border border-slate-200">刷新</button><button type="button" @click="clearLogs" class="btn-danger">清空</button></div>
    </div>

    <div class="card overflow-hidden">
      <div v-if="store.logs.length === 0" class="empty-state py-14"><div class="empty-mark">LOG</div><p class="mt-4 text-sm text-text-muted">暂无日志记录</p></div>
      <div v-else class="divide-y divide-slate-100"><div v-for="log in store.logs" :key="log.id" class="log-row"><span class="level-marker" :class="levelClass(log.level)"></span><span class="category-tag">{{ log.category }}</span><span class="min-w-0 flex-1 break-words text-xs leading-6 text-slate-600">{{ log.message }}</span><span class="flex-shrink-0 font-mono text-[11px] text-slate-400">{{ formatTime(log.created_at) }}</span></div></div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useStore } from '../stores'
import { formatDateTime } from '../utils/time'

const store = useStore(); const category = ref('')
onMounted(load)
async function load() { await store.fetchLogs(category.value || null) }
async function clearLogs() { if (confirm('确认清空当前日志？')) await store.clearLogs(category.value || null) }
function levelClass(level) { return { info: 'level-info', warning: 'level-warning', error: 'level-error' }[level] || 'level-muted' }
function formatTime(t) { return formatDateTime(t, '时间未知') }
</script>

<style scoped>
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }.page-title { margin-top: 6px; color: #172033; font-size: 26px; font-weight: 750; letter-spacing: -.03em; }.page-subtitle { margin-top: 6px; color: #64748b; font-size: 13px; }.empty-state { text-align: center; }.empty-mark { display: inline-flex; align-items: center; justify-content: center; width: 46px; height: 46px; border-radius: 11px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 800; letter-spacing: .08em; }.log-row { display: flex; align-items: flex-start; gap: 12px; padding: 13px 17px; transition: background .15s ease; }.log-row:hover { background: #f8fafc; }.level-marker { width: 7px; height: 7px; flex: 0 0 auto; margin-top: 8px; border-radius: 50%; }.level-info { background: #2563eb; }.level-warning { background: #d97706; }.level-error { background: #dc2626; }.level-muted { background: #94a3b8; }.category-tag { flex: 0 0 auto; border-radius: 5px; background: #f1f5f9; padding: 3px 7px; color: #64748b; font-size: 10px; font-weight: 650; }
@media (max-width: 520px) { .log-row { flex-wrap: wrap; gap: 8px 10px; padding: 12px 13px; }.log-row > span:nth-last-child(1) { width: 100%; padding-left: 17px; font-size: 10px; } }
</style>

<template>
  <div class="min-h-screen bg-background text-text font-sans antialiased">
    <div v-if="isLogin">
      <router-view v-slot="{ Component }">
        <transition name="fade" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </div>

    <div v-else class="min-h-screen lg:flex">
      <aside class="app-sidebar">
        <div class="sidebar-brand">
          <div>
            <div class="brand-name">AliCDT Console</div>
            <div class="brand-caption">云资源与中转控制台</div>
          </div>
        </div>

        <nav class="sidebar-nav" aria-label="主导航">
          <section class="nav-section" aria-labelledby="primary-navigation">
            <div id="primary-navigation" class="nav-section-label">核心流程</div>
            <div class="nav-list">
              <button
                v-for="item in primaryNavItems"
                :key="item.path"
                type="button"
                class="nav-item"
                :class="isActive(item.path) ? 'nav-item-active' : ''"
                :aria-current="isActive(item.path) ? 'page' : undefined"
                @click="navigate(item.path)"
              >
                <span>{{ item.label }}</span>
                <span v-if="item.recommended" class="nav-item-badge">推荐</span>
              </button>
            </div>
          </section>

          <section class="nav-section nav-section-advanced">
            <button
              type="button"
              class="nav-group-toggle"
              :class="advancedActive ? 'nav-group-toggle-active' : ''"
              :aria-expanded="advancedOpen"
              aria-controls="advanced-navigation"
              @click="advancedOpen = !advancedOpen"
            >
              <span>高级管理</span>
              <span class="nav-group-chevron" :class="advancedOpen ? 'nav-group-chevron-open' : ''" aria-hidden="true">›</span>
            </button>
            <div v-show="advancedOpen" id="advanced-navigation" class="nav-list nav-list-advanced">
              <button
                v-for="item in advancedNavItems"
                :key="item.path"
                type="button"
                class="nav-item nav-item-secondary"
                :class="isActive(item.path) ? 'nav-item-active' : ''"
                :aria-current="isActive(item.path) ? 'page' : undefined"
                @click="navigate(item.path)"
              >
                <span>{{ item.label }}</span>
              </button>
            </div>
          </section>
        </nav>

        <div class="sidebar-footer">
          <div class="flex items-center gap-2 px-3 pb-3 text-xs text-text-muted">
            <span class="status-dot status-dot-success"></span>
            <span>服务运行正常</span>
          </div>
          <button type="button" @click="logout" class="nav-item nav-item-muted">
            <span>退出登录</span>
          </button>
        </div>
      </aside>

      <main class="app-main">
        <header class="app-topbar">
          <div class="topbar-context">
            <span class="topbar-kicker">WORKSPACE</span>
            <strong>{{ activeItem.label }}</strong>
          </div>
          <div class="topbar-actions">
            <span v-if="updateState.status !== 'idle'" class="update-status" :class="`update-status-${updateState.status}`" aria-live="polite">{{ updateStatusLabel }}</span>
            <button type="button" class="update-button" :disabled="updateBusy" @click="requestUpdate">
              <span class="update-code">UPD</span>
              <span>{{ updateButtonLabel }}</span>
            </button>
          </div>
        </header>
        <header class="mobile-header">
          <span class="font-semibold">AliCDT Console</span>
        </header>
        <div class="page-container">
          <router-view v-slot="{ Component }">
            <transition name="fade-slide" mode="out-in">
              <component :is="Component" />
            </transition>
          </router-view>
        </div>
      </main>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useRelayStore } from './stores/relay'

const route = useRoute()
const router = useRouter()
const relayStore = useRelayStore()
const isLogin = computed(() => route.path === '/login')

const primaryNavItems = [
  { path: '/', label: '运行总览' },
  { path: '/relay-pools', label: '统一入口', recommended: true },
  { path: '/relay-nodes', label: 'CDT 中转' },
  { path: '/landing-nodes', label: '全球落地' },
  { path: '/cloud-resources', label: '云资源' },
]

const advancedNavItems = [
  { path: '/dns', label: 'DNS 托管' },
  { path: '/relay-services', label: '单机转发' },
  { path: '/accounts', label: '云账户（兼容）' },
  { path: '/instances', label: '实例工作区（兼容）' },
  { path: '/logs', label: '系统日志' },
  { path: '/settings', label: '系统设置' },
]

// Keep every existing URL represented so bookmarks and older workflows remain
// discoverable, while the default view focuses on the unified entry flow.
const allNavItems = [...primaryNavItems, ...advancedNavItems]
const advancedOpen = ref(false)

function isActive(path) {
  return path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(`${path}/`)
}

const advancedActive = computed(() => advancedNavItems.some(item => isActive(item.path)))
const activeItem = computed(() => {
  return allNavItems.find(item => isActive(item.path)) || primaryNavItems[0]
})
watch(advancedActive, active => {
  if (active) advancedOpen.value = true
}, { immediate: true })
const updateState = ref({ status: 'idle', message: '暂无更新任务' })
const updateBusy = computed(() => ['pending', 'running'].includes(updateState.value.status))
const updateButtonLabel = computed(() => {
  if (updateState.value.status === 'pending') return '等待更新'
  if (updateState.value.status === 'running') return '更新中...'
  if (updateState.value.status === 'success') return '再次更新'
  return '一键更新'
})
const updateStatusLabel = computed(() => updateState.value.message || ({ pending: '等待宿主机执行', running: '正在构建并切换', success: '更新完成', error: '更新失败' }[updateState.value.status] || ''))
let updateTimer

function navigate(path) {
  router.push(path)
}

function logout() {
  localStorage.removeItem('token')
  router.push('/login')
}

async function refreshUpdateStatus() {
  if (isLogin.value || !localStorage.getItem('token')) return
  try {
    updateState.value = await relayStore.fetchUpdateStatus()
  } catch (_) {
    // The update endpoint is optional for older installations; navigation must
    // remain usable when the host watcher has not been installed yet.
  }
}

function stopUpdatePolling() {
  if (updateTimer) {
    window.clearInterval(updateTimer)
    updateTimer = undefined
  }
}

function startUpdatePolling() {
  stopUpdatePolling()
  updateTimer = window.setInterval(async () => {
    await refreshUpdateStatus()
    if (['success', 'error'].includes(updateState.value.status)) {
      stopUpdatePolling()
      if (updateState.value.status === 'success') {
        window.setTimeout(() => window.location.reload(), 1200)
      }
    }
  }, 2000)
}

async function requestUpdate() {
  if (updateBusy.value) return
  if (!window.confirm('确认从 GitHub 拉取最新代码、备份数据库并重启控制器？更新期间面板会短暂不可用。')) return
  try {
    updateState.value = await relayStore.requestUpdate()
    startUpdatePolling()
  } catch (error) {
    updateState.value = error.response?.data || { status: 'error', message: '更新请求失败，请检查宿主机更新服务' }
  }
}

onMounted(refreshUpdateStatus)
onUnmounted(stopUpdatePolling)
</script>

<style scoped>
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.fade-slide-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

.fade-slide-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

.fade-enter-active,
.fade-leave-active { transition: opacity 0.18s ease; }
.fade-enter-from,
.fade-leave-to { opacity: 0; }

.app-topbar { display: flex; height: 64px; align-items: center; justify-content: space-between; gap: 20px; border-bottom: 1px solid #e5eaf1; background: rgba(255,255,255,.96); padding: 0 30px; }
.topbar-context { display: flex; min-width: 0; align-items: baseline; gap: 10px; }
.topbar-kicker { color: #94a3b8; font-size: 9px; font-weight: 800; letter-spacing: .16em; }
.topbar-context strong { overflow: hidden; color: #334155; font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.topbar-actions { display: flex; min-width: 0; align-items: center; justify-content: flex-end; gap: 10px; }
.update-status { max-width: 260px; overflow: hidden; color: #64748b; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.update-status-success { color: #15803d; }.update-status-error { color: #b91c1c; }.update-status-running,.update-status-pending { color: #2563eb; }
.update-button { display: inline-flex; align-items: center; gap: 8px; border: 1px solid #bfdbfe; border-radius: 8px; background: #eff6ff; padding: 7px 11px; color: #1d4ed8; cursor: pointer; font-size: 11px; font-weight: 700; transition: background .15s ease, border-color .15s ease; }
.update-button:hover { border-color: #93c5fd; background: #dbeafe; }.update-button:disabled { cursor: wait; opacity: .65; }
.update-code { border-radius: 4px; background: #2563eb; padding: 3px 4px; color: #fff; font-size: 8px; font-weight: 800; letter-spacing: .04em; }
@media (max-width: 639px) { .app-topbar { height: 56px; padding: 0 16px; }.topbar-context { display: none; }.topbar-actions { width: 100%; }.update-status { flex: 1 1 auto; max-width: none; }.update-button { flex: 0 0 auto; } }
</style>

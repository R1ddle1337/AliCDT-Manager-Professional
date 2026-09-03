<template>
  <div class="app-shell min-h-screen bg-background text-text font-sans antialiased" :data-card-layout="ui.cardLayout">
    <div v-if="isLogin">
      <router-view v-slot="{ Component }">
        <transition name="fade" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </div>

    <div v-else class="min-h-screen admin-shell" :class="sidebarOpen ? 'sidebar-expanded' : 'sidebar-collapsed'">
      <aside class="app-sidebar">
        <div class="sidebar-brand">
          <div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div>
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
                <span class="nav-item-icon"><NavIcon :name="item.icon" /></span>
                <span>{{ item.label }}</span>
                <span v-if="item.recommended" class="nav-item-badge">推荐</span>
              </button>
            </div>
          </section>

          <section v-if="isAdmin" class="nav-section nav-section-advanced">
            <button
              type="button"
              class="nav-group-toggle"
              :class="advancedActive ? 'nav-group-toggle-active' : ''"
              :aria-expanded="advancedOpen"
              aria-controls="advanced-navigation"
              @click="advancedOpen = !advancedOpen"
            >
              <span>管理工具</span>
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
                <span class="nav-item-icon"><NavIcon :name="item.icon" /></span>
                <span>{{ item.label }}</span>
              </button>
            </div>
          </section>
        </nav>

        <div class="sidebar-footer">
          <div class="flex items-center gap-2 px-3 pb-3 text-xs text-text-muted">
            <span class="status-dot status-dot-success"></span>
            <span>{{ localDisplayName }}</span>
          </div>
          <button type="button" @click="logout" class="nav-item nav-item-muted">
            <span>退出登录</span>
          </button>
        </div>
      </aside>

      <main class="app-main">
        <header class="app-topbar">
          <div class="topbar-leading">
            <button type="button" class="sidebar-toggle" :aria-expanded="sidebarOpen" aria-label="展开或收起侧栏" @click="sidebarOpen = !sidebarOpen"><span aria-hidden="true">☰</span></button>
            <div class="topbar-brand"><span class="topbar-brand-mark">A</span><strong>AliCDT</strong><small>管理员控制台</small></div>
          </div>
          <div class="topbar-context">
            <span class="topbar-kicker">WORKSPACE</span>
            <strong>{{ activeItem.label }}</strong>
          </div>
          <div class="topbar-actions">
            <CardLayoutToggle v-if="isAdmin" />
            <span v-if="isAdmin && updateState.status !== 'idle'" class="update-status" :class="`update-status-${updateState.status}`" aria-live="polite">{{ updateStatusLabel }}</span>
            <button v-if="isAdmin" type="button" class="update-button" :disabled="updateBusy" @click="requestUpdate">
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
import { useUIStore } from './stores/ui'
import CardLayoutToggle from './components/CardLayoutToggle.vue'
import NavIcon from './components/NavIcon.vue'

const route = useRoute()
const router = useRouter()
const relayStore = useRelayStore()
const ui = useUIStore()
const isLogin = computed(() => route.path === '/login')

const role = ref(localStorage.getItem('role') || 'admin')
const isAdmin = computed(() => role.value !== 'user')
const localDisplayName = computed(() => localStorage.getItem('displayName') || (isAdmin.value ? '管理员' : '用户'))
const primaryNavItems = computed(() => isAdmin.value ? [
  { path: '/', label: '运行总览', icon: 'dashboard' },
  { path: '/workspace', label: '中转工作区', icon: 'workspace', recommended: true },
  { path: '/cloud-resources', label: '云资源工作区', icon: 'cloud' },
  { path: '/users', label: '用户管理', icon: 'users' },
] : [{ path: '/usage', label: '我的用量', icon: 'workspace' }])

const advancedNavItems = [
  { path: '/relay-pools', label: '入口池', icon: 'pool' },
  { path: '/relay-services', label: '独立转发', icon: 'service' },
  { path: '/relay-nodes', label: '中转节点', icon: 'relay' },
  { path: '/landing-nodes', label: '落地目标', icon: 'landing' },
  { path: '/dns', label: 'DNS 托管', icon: 'dns' },
  { path: '/logs', label: '系统日志', icon: 'logs' },
  { path: '/security', label: '安全中心', icon: 'settings' },
  { path: '/settings', label: '系统设置', icon: 'settings' },
]

// Keep every existing URL represented so bookmarks and older workflows remain
// discoverable, while the default view focuses on the unified entry flow.
const allNavItems = computed(() => [...primaryNavItems.value, ...advancedNavItems.filter(() => isAdmin.value)])
const advancedOpen = ref(false)
const sidebarOpen = ref(true)
watch(() => route.path, () => { role.value = localStorage.getItem('role') || 'admin' }, { immediate: true })

function isActive(path) {
  return path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(`${path}/`)
}

const advancedActive = computed(() => advancedNavItems.some(item => isActive(item.path)))
const activeItem = computed(() => {
  return allNavItems.value.find(item => isActive(item.path)) || primaryNavItems.value[0]
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

async function logout() {
  await relayStore.logout()
  router.push('/login')
}

async function refreshUpdateStatus() {
  if (isLogin.value || !isAdmin.value || !localStorage.getItem('token')) return
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

.admin-shell { min-height: 100vh; background: #f8fafc; }
.app-sidebar { transition: width .22s ease, transform .22s ease, opacity .18s ease; }
.sidebar-collapsed .app-sidebar { width: 0; overflow: hidden; border-right: 0; opacity: 0; pointer-events: none; }
.sidebar-collapsed .app-main { margin-left: 0; }
.app-topbar { display: flex; height: 64px; align-items: center; justify-content: space-between; gap: 20px; border-bottom: 1px solid #e5e7eb; background: rgba(255,255,255,.92); padding: 0 24px; backdrop-filter: blur(14px); }
.topbar-leading { display: flex; min-width: 0; align-items: center; gap: 11px; }
.sidebar-toggle { display: inline-grid; width: 34px; height: 34px; place-items: center; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; color: #64748b; cursor: pointer; font-size: 15px; line-height: 1; transition: .15s ease; }
.sidebar-toggle:hover { border-color: #bfdbfe; background: #eff6ff; color: #1d4ed8; }
.topbar-brand { display: none; min-width: 0; align-items: baseline; gap: 8px; }
.sidebar-collapsed .topbar-brand { display: inline-flex; }
.topbar-brand-mark { display: inline-grid; width: 23px; height: 23px; place-items: center; border-radius: 6px; background: #2563eb; color: #fff; font-size: 11px; font-weight: 800; }
.topbar-brand strong { color: #1e293b; font-size: 14px; letter-spacing: -.02em; }
.topbar-brand small { color: #94a3b8; font-size: 10px; }
.nav-item-icon { display: inline-grid; width: 21px; height: 21px; flex: 0 0 auto; place-items: center; border-radius: 6px; background: #f1f5f9; color: #94a3b8; font-size: 13px; font-weight: 700; line-height: 1; }
.nav-item-active .nav-item-icon { background: #dbeafe; color: #2563eb; }
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

<template>
  <div class="app-shell min-h-screen bg-background text-text font-sans antialiased" :data-card-layout="ui.cardLayout">
    <a v-if="!isLogin" href="#main-content" class="skip-link">跳转到主内容</a>
    <NotificationCenter />
    <div v-if="isLogin">
      <router-view v-slot="{ Component }">
        <transition name="fade" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </div>

    <div v-else class="min-h-screen admin-shell" :class="sidebarOpen ? 'sidebar-expanded' : 'sidebar-collapsed'">
      <div class="route-progress" aria-hidden="true"></div>
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
      <button v-if="sidebarOpen" type="button" class="sidebar-backdrop" aria-label="关闭导航" @click="sidebarOpen = false"></button>

      <main id="main-content" class="app-main" tabindex="-1">
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
        <div v-if="offline" class="connection-banner" role="status">
          网络连接已断开，当前页面仍可浏览；恢复连接后数据会继续更新。
        </div>
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
import NotificationCenter from './components/NotificationCenter.vue'

const route = useRoute()
const router = useRouter()
const relayStore = useRelayStore()
const ui = useUIStore()
const isLogin = computed(() => route.path === '/login')

const role = ref(localStorage.getItem('role') || 'admin')
const isAdmin = computed(() => role.value !== 'user')
const localDisplayName = computed(() => localStorage.getItem('displayName') || (isAdmin.value ? '管理员' : '用户'))
const primaryNavItems = computed(() => isAdmin.value ? [
  { path: '/workspace', label: '中转管理', icon: 'workspace' },
  { path: '/cloud-resources', label: '云资源', icon: 'cloud' },
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
const navigationStorageKey = 'alicdt_navigation_open'
const toolsStorageKey = 'alicdt_tools_open'
const mobileMedia = window.matchMedia('(max-width: 1023px)')
const isMobile = ref(mobileMedia.matches)
const advancedOpen = ref(localStorage.getItem(toolsStorageKey) === 'true')
const sidebarOpen = ref(isMobile.value ? false : localStorage.getItem(navigationStorageKey) !== 'false')
const offline = ref(!navigator.onLine)

watch(() => route.path, () => {
  role.value = localStorage.getItem('role') || 'admin'
  if (isMobile.value) sidebarOpen.value = false
}, { immediate: true })
watch(advancedOpen, value => localStorage.setItem(toolsStorageKey, String(value)))
watch(sidebarOpen, value => {
  if (!isMobile.value) localStorage.setItem(navigationStorageKey, String(value))
})

function isActive(path) {
  return route.path === path || route.path.startsWith(`${path}/`)
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
let updatePolling = false

function handleViewportChange(event) {
  const wasMobile = isMobile.value
  isMobile.value = event.matches
  if (!wasMobile && event.matches) sidebarOpen.value = false
  if (wasMobile && !event.matches) sidebarOpen.value = localStorage.getItem(navigationStorageKey) !== 'false'
}

function updateConnectionState() {
  offline.value = !navigator.onLine
}

function navigate(path) {
  router.push(path)
  if (isMobile.value) sidebarOpen.value = false
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
  updatePolling = false
  if (updateTimer) window.clearTimeout(updateTimer)
  updateTimer = undefined
}

async function pollUpdateStatus() {
  updateTimer = undefined
  if (!updatePolling) return
  if (document.hidden || !navigator.onLine) {
    updateTimer = window.setTimeout(pollUpdateStatus, 2000)
    return
  }
  await refreshUpdateStatus()
  if (['success', 'error'].includes(updateState.value.status)) {
    stopUpdatePolling()
    if (updateState.value.status === 'success') {
      window.setTimeout(() => window.location.reload(), 1200)
    }
    return
  }
  if (updatePolling) updateTimer = window.setTimeout(pollUpdateStatus, 2000)
}

function startUpdatePolling() {
  stopUpdatePolling()
  updatePolling = true
  updateTimer = window.setTimeout(pollUpdateStatus, 0)
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

onMounted(() => {
  refreshUpdateStatus()
  mobileMedia.addEventListener('change', handleViewportChange)
  window.addEventListener('online', updateConnectionState)
  window.addEventListener('offline', updateConnectionState)
})
onUnmounted(() => {
  stopUpdatePolling()
  mobileMedia.removeEventListener('change', handleViewportChange)
  window.removeEventListener('online', updateConnectionState)
  window.removeEventListener('offline', updateConnectionState)
})
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
.sidebar-backdrop { display: none; }
.connection-banner { border-bottom: 1px solid #fde68a; background: #fffbeb; padding: 8px 20px; color: #92400e; font-size: 10px; text-align: center; }
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
@media (max-width: 1023px) {
  .sidebar-backdrop { position: fixed; inset: 0; z-index: 29; display: block; border: 0; background: rgba(15, 23, 42, .34); backdrop-filter: blur(2px); }
  .sidebar-expanded .app-sidebar { width: min(82vw, 280px); opacity: 1; pointer-events: auto; transform: translateX(0); }
  .sidebar-collapsed .app-sidebar { width: min(82vw, 280px); border-right: 1px solid #e5e7eb; opacity: 1; transform: translateX(-105%); }
}
@media (max-width: 639px) { .app-topbar { height: 56px; padding: 0 16px; }.topbar-context { display: none; }.topbar-actions { width: 100%; }.update-status { flex: 1 1 auto; max-width: none; }.update-button { flex: 0 0 auto; }.update-code { display: none; } }
</style>

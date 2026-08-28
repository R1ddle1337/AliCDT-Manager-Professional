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
          <div class="brand-mark">AC</div>
          <div>
            <div class="brand-name">AliCDT Manager</div>
            <div class="brand-caption">云资源控制台</div>
          </div>
        </div>

        <nav class="flex-1 px-3 py-6" aria-label="主导航">
          <div class="nav-section-label">工作区</div>
          <div class="space-y-1">
            <button
              v-for="(item, index) in navItems"
              :key="item.path"
              type="button"
              class="nav-item"
              :class="activeIndex === index ? 'nav-item-active' : ''"
              @click="navigate(item.path)"
            >
              <span class="nav-code" :class="activeIndex === index ? 'nav-code-active' : ''">{{ item.code }}</span>
              <span>{{ item.label }}</span>
            </button>
          </div>
        </nav>

        <div class="sidebar-footer">
          <div class="flex items-center gap-2 px-3 pb-3 text-xs text-text-muted">
            <span class="status-dot status-dot-success"></span>
            <span>服务运行正常</span>
          </div>
          <button type="button" @click="logout" class="nav-item nav-item-muted">
            <span class="nav-code">OUT</span>
            <span>退出登录</span>
          </button>
        </div>
      </aside>

      <main class="app-main">
        <header class="mobile-header">
          <div class="brand-mark brand-mark-small">AC</div>
          <span class="font-semibold">AliCDT Manager</span>
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
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const isLogin = computed(() => route.path === '/login')

const navItems = [
  { path: '/', code: 'OV', label: '总览' },
  { path: '/accounts', code: 'AC', label: '账户管理' },
  { path: '/logs', code: 'LG', label: '系统日志' },
  { path: '/settings', code: 'ST', label: '系统设置' },
]

const activeIndex = computed(() => {
  const index = navItems.findIndex(item => item.path === '/' ? route.path === '/' : route.path.startsWith(item.path))
  return index === -1 ? 0 : index
})

function navigate(path) {
  router.push(path)
}

function logout() {
  localStorage.removeItem('token')
  router.push('/login')
}
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
</style>

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import axios from 'axios'
import App from './App.vue'
import './style.css'

const Login = () => import('./views/Login.vue')
const RelayOverview = () => import('./views/RelayOverview.vue')
const AdminWorkspace = () => import('./views/AdminWorkspace.vue')
const RelayNodes = () => import('./views/RelayNodes.vue')
const LandingNodes = () => import('./views/LandingNodes.vue')
const RelayServices = () => import('./views/RelayServices.vue')
const CloudResources = () => import('./views/CloudResources.vue')
const Logs = () => import('./views/Logs.vue')
const Settings = () => import('./views/Settings.vue')
const Security = () => import('./views/Security.vue')
const DNSProviders = () => import('./views/DNSProviders.vue')
const RelayPools = () => import('./views/RelayPools.vue')
const Users = () => import('./views/Users.vue')
const UserUsage = () => import('./views/UserUsage.vue')

const authMeta = (title, audience = 'admin') => ({
  auth: true,
  [audience]: true,
  title,
})

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/login', component: Login, meta: { title: '登录' } },
    { path: '/', component: RelayOverview, meta: authMeta('运行总览') },
    { path: '/workspace', component: AdminWorkspace, meta: authMeta('中转工作区') },
    { path: '/instances', redirect: '/cloud-resources' },
    { path: '/accounts', redirect: '/cloud-resources' },
    { path: '/relay-nodes', component: RelayNodes, meta: authMeta('中转节点') },
    { path: '/landing-nodes', component: LandingNodes, meta: authMeta('落地目标') },
    { path: '/relay-services', component: RelayServices, meta: authMeta('独立转发') },
    { path: '/cloud-resources', component: CloudResources, meta: authMeta('云资源') },
    { path: '/logs', component: Logs, meta: authMeta('系统日志') },
    { path: '/settings', component: Settings, meta: authMeta('系统设置') },
    { path: '/security', component: Security, meta: authMeta('安全中心') },
    { path: '/dns', component: DNSProviders, meta: authMeta('DNS 托管') },
    { path: '/relay-pools', component: RelayPools, meta: authMeta('入口池') },
    { path: '/users', component: Users, meta: authMeta('用户管理') },
    { path: '/usage', component: UserUsage, meta: authMeta('我的用量', 'user') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to, from, next) => {
  document.documentElement.classList.add('route-pending')
  const token = localStorage.getItem('token')
  if (to.meta.auth && !token) return next('/login')
  let role = localStorage.getItem('role')
  if (token && !role) {
    try {
      const { data } = await axios.get('/api/v2/auth/me', { headers: { Authorization: `Bearer ${token}` } })
      role = data.role
      localStorage.setItem('role', role)
      localStorage.setItem('username', data.username || '')
      localStorage.setItem('displayName', data.display_name || data.username || '')
    } catch (_) {
      localStorage.removeItem('token')
      localStorage.removeItem('role')
      localStorage.removeItem('username')
      localStorage.removeItem('displayName')
      return next('/login')
    }
  }
  if (to.path === '/login' && token) return next(role === 'user' ? '/usage' : '/')
  if (to.meta.admin && role !== 'admin') return next(role === 'user' ? '/usage' : '/login')
  if (to.meta.user && role !== 'user') return next(role === 'admin' ? '/' : '/login')
  next()
})

router.afterEach(to => {
  document.title = `${to.meta.title || '控制台'} · AliCDT`
  window.requestAnimationFrame(() => document.documentElement.classList.remove('route-pending'))
})

router.onError(error => {
  document.documentElement.classList.remove('route-pending')
  const message = String(error?.message || error)
  if (!/dynamically imported module|loading chunk|loading css chunk/i.test(message)) return
  const reloadKey = 'alicdt_chunk_reload'
  if (sessionStorage.getItem(reloadKey)) {
    sessionStorage.removeItem(reloadKey)
    return
  }
  sessionStorage.setItem(reloadKey, '1')
  window.location.reload()
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')

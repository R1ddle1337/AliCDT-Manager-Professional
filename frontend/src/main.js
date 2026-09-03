import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import axios from 'axios'
import App from './App.vue'
import './style.css'

import Login from './views/Login.vue'
import RelayOverview from './views/RelayOverview.vue'
import AdminWorkspace from './views/AdminWorkspace.vue'
import RelayNodes from './views/RelayNodes.vue'
import LandingNodes from './views/LandingNodes.vue'
import RelayServices from './views/RelayServices.vue'
import CloudResources from './views/CloudResources.vue'
import Logs from './views/Logs.vue'
import Settings from './views/Settings.vue'
import Security from './views/Security.vue'
import DNSProviders from './views/DNSProviders.vue'
import RelayPools from './views/RelayPools.vue'
import Users from './views/Users.vue'
import UserUsage from './views/UserUsage.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: RelayOverview, meta: { auth: true, admin: true } },
    { path: '/workspace', component: AdminWorkspace, meta: { auth: true, admin: true } },
    { path: '/instances', redirect: '/cloud-resources' },
    { path: '/accounts', redirect: '/cloud-resources' },
    { path: '/relay-nodes', component: RelayNodes, meta: { auth: true, admin: true } },
    { path: '/landing-nodes', component: LandingNodes, meta: { auth: true, admin: true } },
    { path: '/relay-services', component: RelayServices, meta: { auth: true, admin: true } },
    { path: '/cloud-resources', component: CloudResources, meta: { auth: true, admin: true } },
    { path: '/logs', component: Logs, meta: { auth: true, admin: true } },
    { path: '/settings', component: Settings, meta: { auth: true, admin: true } },
    { path: '/security', component: Security, meta: { auth: true, admin: true } },
    { path: '/dns', component: DNSProviders, meta: { auth: true, admin: true } },
    { path: '/relay-pools', component: RelayPools, meta: { auth: true, admin: true } },
    { path: '/users', component: Users, meta: { auth: true, admin: true } },
    { path: '/usage', component: UserUsage, meta: { auth: true, user: true } },
  ]
})

router.beforeEach(async (to, from, next) => {
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

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')

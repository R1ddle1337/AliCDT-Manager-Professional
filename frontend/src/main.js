import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'

import Login from './views/Login.vue'
import RelayOverview from './views/RelayOverview.vue'
import RelayNodes from './views/RelayNodes.vue'
import LandingNodes from './views/LandingNodes.vue'
import RelayServices from './views/RelayServices.vue'
import CloudResources from './views/CloudResources.vue'
import Dashboard from './views/Dashboard.vue'
import Accounts from './views/Accounts.vue'
import Logs from './views/Logs.vue'
import Settings from './views/Settings.vue'
import DNSProviders from './views/DNSProviders.vue'
import RelayPools from './views/RelayPools.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: RelayOverview, meta: { auth: true } },
    { path: '/instances', component: Dashboard, meta: { auth: true } },
    { path: '/accounts', component: Accounts, meta: { auth: true } },
    { path: '/relay-nodes', component: RelayNodes, meta: { auth: true } },
    { path: '/landing-nodes', component: LandingNodes, meta: { auth: true } },
    { path: '/relay-services', component: RelayServices, meta: { auth: true } },
    { path: '/cloud-resources', component: CloudResources, meta: { auth: true } },
    { path: '/logs', component: Logs, meta: { auth: true } },
    { path: '/settings', component: Settings, meta: { auth: true } },
    { path: '/dns', component: DNSProviders, meta: { auth: true } },
    { path: '/relay-pools', component: RelayPools, meta: { auth: true } },
  ]
})

router.beforeEach((to, from, next) => {
  const token = localStorage.getItem('token')
  if (to.meta.auth && !token) return next('/login')
  if (to.path === '/login' && token) return next('/')
  next()
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')

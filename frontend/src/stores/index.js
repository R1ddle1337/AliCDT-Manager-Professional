import { defineStore } from 'pinia'
import axios from 'axios'
import { ref } from 'vue'
import { apiErrorMessage, clearSession, saveSession } from '../utils/session'
import { notifyError } from '../utils/notifications'

const api = axios.create({ baseURL: '/api', timeout: 20000 })
api.interceptors.request.use(cfg => {
  const token = localStorage.getItem('token')
  if (token) cfg.headers.Authorization = `Bearer ${token}`
  return cfg
})
api.interceptors.response.use(r => r, err => {
  if (err.response?.status === 401) {
    clearSession()
    if (window.location.pathname !== '/login') window.location.assign('/login')
  } else if (!err.response || err.response.status >= 500) {
    notifyError(apiErrorMessage(err, '控制器暂时不可用，请稍后重试'))
  }
  return Promise.reject(err)
})

export const useStore = defineStore('main', () => {
  const instances = ref([])
  const accounts = ref([])
  const logs = ref([])
  const settings = ref({})
  const loading = ref(false)

  async function login(username, password) {
    const { data } = await api.post('/auth/login', { username, password })
    saveSession(data, username)
    return data
  }

  async function fetchInstances() {
    const { data } = await api.get('/instances')
    instances.value = data
  }

  async function fetchAccounts() {
    const { data } = await api.get('/accounts')
    accounts.value = data
  }

  async function fetchLogs(category = null) {
    const params = category ? { category } : {}
    const { data } = await api.get('/logs', { params })
    logs.value = data
  }

  async function fetchSettings() {
    const { data } = await api.get('/settings')
    settings.value = data
  }

  async function syncAll() {
    loading.value = true
    try {
      await api.post('/instances/sync')
      await fetchInstances()
    } finally {
      loading.value = false
    }
  }

  async function syncSingleInstance(instanceId) {
    await api.post(`/instances/${instanceId}/sync`)
    await fetchInstances()
  }

  async function controlInstance(instanceId, action) {
    await api.post(`/instances/${instanceId}/${action}`)
    await fetchInstances()
    const targetStatus = action === 'start' ? 'Running' : 'Stopped'
    if (instances.value.find(item => item.instance_id === instanceId)?.status === targetStatus) return
    for (let count = 0; count < 15; count++) {
      await new Promise(resolve => window.setTimeout(resolve, 2000))
      await fetchInstances()
      if (instances.value.find(item => item.instance_id === instanceId)?.status === targetStatus) return
    }
  }

  async function releaseInstance(instanceId) {
    await api.delete(`/instances/${instanceId}`)
    await fetchInstances()
  }

  async function getBilling(accountId) {
    const { data } = await api.get(`/billing/${accountId}`)
    return data
  }

  async function createAccount(payload) {
    const { data } = await api.post('/accounts', payload)
    await fetchAccounts()
    await fetchInstances()
    return data
  }

  async function updateAccount(id, payload) {
    await api.put(`/accounts/${id}`, payload)
    await fetchAccounts()
  }

  async function deleteAccount(id) {
    await api.delete(`/accounts/${id}`)
    await fetchAccounts()
    await fetchInstances()
  }

  async function saveSettings(items) {
    await api.post('/settings', items)
    await fetchSettings()
  }

  async function testTelegram() {
    const { data } = await api.post('/settings/test-tg', {})
    return data
  }

  async function testDailyReport() {
    const { data } = await api.post('/settings/test-daily-report', {})
    return data
  }

  async function fetchVersionInfo() {
    const { data } = await api.get('/version/check')
    return data
  }

  async function clearLogs(category = null) {
    await api.delete('/logs', { params: category ? { category } : {} })
    await fetchLogs()
  }

  async function renameInstance(instanceId, name) {
    await api.patch(`/instances/${instanceId}/rename`, { name })
    await fetchInstances()
  }

  return {
    instances, accounts, logs, settings, loading,
    login, fetchInstances, fetchAccounts, fetchLogs, fetchSettings,
    syncAll, syncSingleInstance, controlInstance, releaseInstance, getBilling,
    createAccount, updateAccount, deleteAccount, saveSettings, testTelegram, testDailyReport, fetchVersionInfo, clearLogs,
    renameInstance,
  }
})

import { defineStore } from 'pinia'
import axios from 'axios'
import { ref } from 'vue'
import { apiErrorMessage, clearSession } from '../utils/session'
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
  const logs = ref([])
  const settings = ref({})

  async function fetchLogs(category = null) {
    const params = category ? { category } : {}
    const { data } = await api.get('/logs', { params })
    logs.value = data
  }

  async function fetchSettings() {
    const { data } = await api.get('/settings')
    settings.value = data
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

  return {
    logs, settings,
    fetchLogs, fetchSettings, saveSettings, testTelegram, testDailyReport, fetchVersionInfo, clearLogs,
  }
})

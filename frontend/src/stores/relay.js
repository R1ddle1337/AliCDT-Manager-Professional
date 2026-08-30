import { defineStore } from 'pinia'
import axios from 'axios'
import { ref } from 'vue'

const api = axios.create({ baseURL: '/api/v2', timeout: 20000 })
api.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})
api.interceptors.response.use(response => response, error => {
  if (error.response?.status === 401 && !error.config?.url?.startsWith('/auth/')) {
    localStorage.removeItem('token')
    window.location.href = '/login'
  }
  return Promise.reject(error)
})

export const useRelayStore = defineStore('relay-platform', () => {
  const relayNodes = ref([])
  const landingNodes = ref([])
  const services = ref([])
  const events = ref([])
  const cloud = ref({ accounts: [], instances: [], traffic: [] })
  const loading = ref(false)
  const updateStatus = ref({ status: 'idle', message: '暂无更新任务' })

  async function login(username, password) {
    const { data } = await api.post('/auth/login', { username, password })
    localStorage.setItem('token', data.token)
    return data
  }

  async function fetchRelayNodes() {
    const { data } = await api.get('/relay-nodes')
    relayNodes.value = data || []
  }

  async function fetchLandingNodes() {
    const { data } = await api.get('/landing-nodes')
    landingNodes.value = data || []
  }

  async function fetchServices() {
    const { data } = await api.get('/relay-services')
    services.value = data || []
  }

  async function fetchEvents() {
    const { data } = await api.get('/events', { params: { limit: 30 } })
    events.value = data || []
  }

  async function fetchCloud() {
    const { data } = await api.get('/cloud/overview')
    cloud.value = data || { accounts: [], instances: [], traffic: [] }
  }

  async function fetchAll() {
    loading.value = true
    try {
      await Promise.all([fetchRelayNodes(), fetchLandingNodes(), fetchServices(), fetchEvents(), fetchCloud()])
    } finally {
      loading.value = false
    }
  }

  async function createEnrollmentToken(ttlMinutes = 30) {
    const { data } = await api.post('/enrollment-tokens', { ttl_minutes: ttlMinutes })
    return data
  }

  async function createLandingNode(payload) {
    const { data } = await api.post('/landing-nodes', payload)
    await fetchLandingNodes()
    return data
  }

  async function updateLandingNode(id, payload) {
    const { data } = await api.put(`/landing-nodes/${id}`, payload)
    await fetchLandingNodes()
    return data
  }

  async function deleteLandingNode(id) {
    await api.delete(`/landing-nodes/${id}`)
    await fetchLandingNodes()
  }

  async function fetchLandingRelayLinks(id) {
    const { data } = await api.get(`/landing-nodes/${id}/relay-links`)
    return data || []
  }

  async function createService(payload) {
    const { data } = await api.post('/relay-services', payload)
    await Promise.all([fetchServices(), fetchRelayNodes()])
    return data
  }

  async function updateService(id, payload) {
    const { data } = await api.put(`/relay-services/${id}`, payload)
    await Promise.all([fetchServices(), fetchRelayNodes()])
    return data
  }

  async function deleteService(id) {
    await api.delete(`/relay-services/${id}`)
    await Promise.all([fetchServices(), fetchRelayNodes()])
  }

  async function syncCloud() {
    const { data } = await api.post('/cloud/sync')
    await fetchCloud()
    return data
  }

  async function createCloudAccount(payload) {
    const { data } = await api.post('/cloud/accounts', payload)
    await fetchCloud()
    return data
  }

  async function updateCloudAccount(id, payload) {
    const { data } = await api.put(`/cloud/accounts/${id}`, payload)
    await fetchCloud()
    return data
  }

  async function deleteCloudAccount(id) {
    await api.delete(`/cloud/accounts/${id}`)
    await fetchCloud()
  }

  async function controlCloudInstance(instanceId, action) {
    const { data } = await api.post(`/cloud/instances/${instanceId}/${action}`)
    return data
  }

  async function fetchUpdateStatus() {
    const { data } = await api.get('/system/update/status')
    updateStatus.value = data || { status: 'idle', message: '暂无更新任务' }
    return updateStatus.value
  }

  async function requestUpdate() {
    const { data } = await api.post('/system/update')
    updateStatus.value = data || { status: 'pending', message: '更新请求已提交' }
    return updateStatus.value
  }

  return {
    relayNodes, landingNodes, services, events, cloud, loading, updateStatus,
    login, fetchRelayNodes, fetchLandingNodes, fetchServices, fetchEvents, fetchCloud, fetchAll,
    createEnrollmentToken, createLandingNode, updateLandingNode, deleteLandingNode, fetchLandingRelayLinks,
    createService, updateService, deleteService,
    syncCloud, createCloudAccount, updateCloudAccount, deleteCloudAccount, controlCloudInstance,
    fetchUpdateStatus, requestUpdate,
  }
})

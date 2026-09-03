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
    localStorage.removeItem('role')
    localStorage.removeItem('username')
    localStorage.removeItem('displayName')
    window.location.href = '/login'
  }
  return Promise.reject(error)
})

export const useRelayStore = defineStore('relay-platform', () => {
  const relayNodes = ref([])
  const landingNodes = ref([])
  const services = ref([])
  const pools = ref([])
  const events = ref([])
  const dnsProviders = ref([])
  const dnsRecords = ref([])
  const cloud = ref({ accounts: [], instances: [], traffic: [] })
  const users = ref([])
  const currentUser = ref(null)
  const loading = ref(false)
  const updateStatus = ref({ status: 'idle', message: '暂无更新任务' })

  async function login(username, password) {
    const { data } = await api.post('/auth/login', { username, password })
    localStorage.setItem('token', data.token)
    localStorage.setItem('role', data.role || 'admin')
    localStorage.setItem('username', data.username || username)
    localStorage.setItem('displayName', data.display_name || data.username || username)
    return data
  }

  async function logout() {
    try { await api.post('/auth/logout') } catch (_) { /* local logout still proceeds */ }
    localStorage.removeItem('token')
    localStorage.removeItem('role')
    localStorage.removeItem('username')
    localStorage.removeItem('displayName')
  }

  async function fetchUsers() {
    const { data } = await api.get('/users')
    users.value = data || []
  }

  async function createUser(payload) {
    const { data } = await api.post('/users', payload)
    await Promise.all([fetchUsers(), fetchCloud()])
    return data
  }

  async function updateUser(id, payload) {
    const { data } = await api.put(`/users/${id}`, payload)
    await Promise.all([fetchUsers(), fetchCloud()])
    return data
  }

  async function deleteUser(id) {
    await api.delete(`/users/${id}`)
    await Promise.all([fetchUsers(), fetchCloud()])
  }

  async function fetchMyUsage() {
    const { data } = await api.get('/user/overview')
    currentUser.value = data
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

  async function fetchPools() {
    const { data } = await api.get('/relay-pools')
    pools.value = data || []
  }

  async function createPool(payload) {
    const { data } = await api.post('/relay-pools', payload)
    await Promise.all([fetchPools(), fetchServices(), fetchRelayNodes(), fetchDNSRecords()])
    return data
  }

  async function updatePool(id, payload) {
    const { data } = await api.put(`/relay-pools/${id}`, payload)
    await Promise.all([fetchPools(), fetchServices(), fetchRelayNodes(), fetchDNSRecords()])
    return data
  }

  async function deletePool(id) {
    await api.delete(`/relay-pools/${id}`)
    await Promise.all([fetchPools(), fetchServices(), fetchRelayNodes(), fetchDNSRecords()])
  }

  async function fetchPoolRelayLinks(id) {
    const { data } = await api.get(`/relay-pools/${id}/relay-links`)
    return data || []
  }

  async function fetchEvents() {
    const { data } = await api.get('/events', { params: { limit: 30 } })
    events.value = data || []
  }

  async function fetchDNSProviders() {
    const { data } = await api.get('/dns/providers')
    dnsProviders.value = data || []
  }

  async function fetchDNSRecords(providerId = '') {
    const { data } = await api.get('/dns/records', { params: providerId ? { provider_id: providerId } : {} })
    dnsRecords.value = data || []
  }

  async function createDNSProvider(payload) {
    const { data } = await api.post('/dns/providers', payload)
    await fetchDNSProviders()
    return data
  }

  async function updateDNSProvider(id, payload) {
    const { data } = await api.put(`/dns/providers/${id}`, payload)
    await fetchDNSProviders()
    return data
  }

  async function deleteDNSProvider(id) {
    await api.delete(`/dns/providers/${id}`)
    await Promise.all([fetchDNSProviders(), fetchDNSRecords()])
  }

  async function testDNSProvider(id) {
    const { data } = await api.post(`/dns/providers/${id}/test`)
    await fetchDNSProviders()
    return data
  }

  async function syncDNSProvider(id) {
    const { data } = await api.post(`/dns/providers/${id}/sync`)
    await Promise.all([fetchDNSProviders(), fetchDNSRecords()])
    return data
  }

  async function syncAllDNS() {
    const { data } = await api.post('/dns/sync')
    await Promise.all([fetchDNSProviders(), fetchDNSRecords()])
    return data
  }

  async function createDNSRecord(payload) {
    const { data } = await api.post('/dns/records', payload)
    await fetchDNSRecords()
    return data
  }

  async function updateDNSRecord(id, payload) {
    const { data } = await api.put(`/dns/records/${id}`, payload)
    await fetchDNSRecords()
    return data
  }

  async function deleteDNSRecord(id) {
    await api.delete(`/dns/records/${id}`)
    await fetchDNSRecords()
  }

  async function fetchCloud() {
    const { data } = await api.get('/cloud/overview')
    cloud.value = data || { accounts: [], instances: [], traffic: [] }
  }

  async function fetchAll() {
    loading.value = true
    try {
      await Promise.all([fetchRelayNodes(), fetchLandingNodes(), fetchServices(), fetchPools(), fetchEvents(), fetchDNSProviders(), fetchDNSRecords(), fetchCloud()])
    } finally {
      loading.value = false
    }
  }

  async function createEnrollmentToken(ttlMinutes = 30, accountId = null) {
    const payload = { ttl_minutes: ttlMinutes }
    if (accountId) payload.account_id = accountId
    const { data } = await api.post('/enrollment-tokens', payload)
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
    // The delete is already committed on the controller. Refresh each view
    // independently so a transient DNS/list refresh failure does not make the
    // operator think the landing node deletion itself failed.
    await Promise.allSettled([fetchLandingNodes(), fetchServices(), fetchPools(), fetchRelayNodes(), fetchDNSRecords()])
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

  async function resetServiceTraffic(id) {
    const { data } = await api.post(`/relay-services/${id}/traffic/reset`)
    await Promise.all([fetchServices(), fetchRelayNodes(), fetchUsers()])
    return data
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
    // The controller records the requested state immediately, while ECS may
    // take a moment to finish the asynchronous transition. Refresh the local
    // projection so the cloud workspace and entry-pool views do not continue
    // showing the pre-command state until the next full page reload.
    await Promise.allSettled([fetchCloud(), fetchRelayNodes(), fetchPools(), fetchDNSRecords()])
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
    relayNodes, landingNodes, services, pools, events, dnsProviders, dnsRecords, cloud, users, currentUser, loading, updateStatus,
    login, logout, fetchUsers, createUser, updateUser, deleteUser, fetchMyUsage,
    fetchRelayNodes, fetchLandingNodes, fetchServices, fetchEvents, fetchCloud, fetchAll,
    createEnrollmentToken, createLandingNode, updateLandingNode, deleteLandingNode, fetchLandingRelayLinks,
    createService, updateService, deleteService, resetServiceTraffic,
    fetchPools, createPool, updatePool, deletePool, fetchPoolRelayLinks,
    fetchDNSProviders, fetchDNSRecords, createDNSProvider, updateDNSProvider, deleteDNSProvider,
    testDNSProvider, syncDNSProvider, syncAllDNS, createDNSRecord, updateDNSRecord, deleteDNSRecord,
    syncCloud, createCloudAccount, updateCloudAccount, deleteCloudAccount, controlCloudInstance,
    fetchUpdateStatus, requestUpdate,
  }
})

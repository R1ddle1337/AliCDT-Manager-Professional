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
  const loading = ref(false)

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

  async function fetchAll() {
    loading.value = true
    try {
      await Promise.all([fetchRelayNodes(), fetchLandingNodes(), fetchServices(), fetchEvents()])
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

  return {
    relayNodes, landingNodes, services, events, loading,
    login, fetchRelayNodes, fetchLandingNodes, fetchServices, fetchEvents, fetchAll,
    createEnrollmentToken, createLandingNode, updateLandingNode, deleteLandingNode,
    createService, updateService, deleteService,
  }
})

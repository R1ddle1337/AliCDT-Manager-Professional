<template>
  <div class="login-page min-h-screen flex items-center justify-center px-4 py-10">
    <div class="login-layout w-full max-w-4xl">
      <section class="login-intro hidden md:block">
        <div class="brand-mark mb-6">AC</div>
        <p class="text-xs font-bold uppercase tracking-[0.2em] text-accent">AliCDT Manager</p>
        <h1 class="mt-4 text-4xl font-bold leading-tight text-slate-900">集中管理云资源，<br />让运行状态清晰可见。</h1>
        <p class="mt-5 max-w-md text-sm leading-7 text-slate-500">统一查看实例、流量和账单状态，按策略执行保活与自动关停。</p>
        <div class="mt-10 flex items-center gap-3 text-xs text-slate-500">
          <span class="status-dot status-dot-success"></span>
          <span>本地控制台 · 安全连接</span>
        </div>
      </section>

      <section class="card login-card p-7 sm:p-9">
        <div class="mb-8 md:hidden">
          <div class="brand-mark mb-4">AC</div>
          <div class="text-xs font-bold uppercase tracking-[0.16em] text-accent">AliCDT Manager</div>
        </div>
        <div class="mb-7">
          <h2 class="text-2xl font-bold tracking-tight text-slate-900">{{ isInit ? '创建管理员账号' : '登录控制台' }}</h2>
          <p class="mt-2 text-sm text-slate-500">{{ isInit ? '首次使用，请设置管理员凭据' : '使用管理员凭据继续' }}</p>
        </div>

        <form class="space-y-4" @submit.prevent="submit">
          <div>
            <label class="field-label" for="username">用户名</label>
            <input id="username" v-model="form.username" class="input" autocomplete="username" placeholder="请输入用户名" />
          </div>
          <div>
            <label class="field-label" for="password">密码</label>
            <input id="password" v-model="form.password" type="password" class="input" autocomplete="current-password" :placeholder="isInit ? '至少 6 位字符' : '请输入密码'" />
          </div>
          <div v-if="isInit">
            <label class="field-label" for="confirm">确认密码</label>
            <input id="confirm" v-model="form.confirm" type="password" class="input" autocomplete="new-password" placeholder="再次输入密码" />
          </div>

          <div v-if="error" class="notice notice-error">{{ error }}</div>

          <button type="submit" :disabled="loading" class="btn-primary mt-2 w-full py-2.5">
            <span v-if="loading" class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-white/35 border-t-white"></span>
            {{ loading ? '处理中...' : (isInit ? '创建账号并登录' : '登录') }}
          </button>
        </form>
      </section>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useStore } from '../stores'
import axios from 'axios'

const router = useRouter()
const store = useStore()
const isInit = ref(false)
const loading = ref(false)
const error = ref('')
const form = ref({ username: '', password: '', confirm: '' })

onMounted(async () => {
  try {
    const { data } = await axios.get('/api/auth/initialized')
    isInit.value = !data.initialized
  } catch (e) {
    error.value = '无法连接服务，请稍后重试'
  }
})

async function submit() {
  error.value = ''
  if (!form.value.username || !form.value.password) {
    error.value = '请填写用户名和密码'
    return
  }
  if (isInit.value) {
    if (form.value.password.length < 6) {
      error.value = '密码至少需要 6 位字符'
      return
    }
    if (form.value.password !== form.value.confirm) {
      error.value = '两次输入的密码不一致'
      return
    }
  }
  loading.value = true
  try {
    if (isInit.value) {
      const { data } = await axios.post('/api/auth/init', { username: form.value.username, password: form.value.password })
      localStorage.setItem('token', data.token)
    } else {
      await store.login(form.value.username, form.value.password)
    }
    router.push('/')
  } catch (e) {
    error.value = e.response?.data?.detail || '操作失败，请稍后重试'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page { background: radial-gradient(circle at 12% 14%, #dbeafe 0, transparent 32%), #f4f7fb; }
.login-layout { display: grid; grid-template-columns: 1fr 400px; align-items: center; gap: 80px; }
.login-intro { padding: 24px 0; }
.login-card { box-shadow: 0 22px 55px rgba(15, 23, 42, 0.09); }
@media (max-width: 900px) { .login-layout { display: block; max-width: 400px; }.login-intro { display: none; } }
</style>

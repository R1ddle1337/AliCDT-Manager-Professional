const sessionKeys = ['token', 'role', 'username', 'displayName']

export function clearSession() {
  sessionKeys.forEach(key => localStorage.removeItem(key))
}

export function saveSession(data, fallbackUsername = '') {
  if (!data?.token) throw new Error('登录响应缺少会话令牌')
  localStorage.setItem('token', data.token)
  localStorage.setItem('role', data.role || 'admin')
  localStorage.setItem('username', data.username || fallbackUsername)
  localStorage.setItem('displayName', data.display_name || data.username || fallbackUsername)
}

export function apiErrorMessage(error, fallback = '请求失败，请稍后重试') {
  if (typeof navigator !== 'undefined' && !navigator.onLine) return '网络连接已断开，请恢复连接后重试'
  if (error?.code === 'ECONNABORTED') return '请求超时，请检查网络或服务状态'
  return error?.response?.data?.error
    || error?.response?.data?.detail
    || error?.response?.data?.message
    || error?.message
    || fallback
}

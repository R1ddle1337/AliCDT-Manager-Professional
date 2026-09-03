import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiErrorMessage, clearSession, saveSession } from './session'

function memoryStorage() {
  const values = new Map()
  return {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  }
}

describe('console session helpers', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage())
    vi.stubGlobal('navigator', { onLine: true })
  })

  it('stores and clears the complete identity together', () => {
    saveSession({ token: 'secret', role: 'user', username: 'alice', display_name: 'Alice' })
    expect(localStorage.getItem('role')).toBe('user')
    expect(localStorage.getItem('displayName')).toBe('Alice')
    clearSession()
    expect(localStorage.getItem('token')).toBeNull()
    expect(localStorage.getItem('displayName')).toBeNull()
  })

  it('rejects malformed login responses', () => {
    expect(() => saveSession({ role: 'admin' })).toThrow('会话令牌')
  })

  it('normalizes offline, timeout and API errors', () => {
    navigator.onLine = false
    expect(apiErrorMessage({})).toContain('网络连接已断开')
    navigator.onLine = true
    expect(apiErrorMessage({ code: 'ECONNABORTED' })).toContain('请求超时')
    expect(apiErrorMessage({ response: { data: { error: 'invalid quota' } } })).toBe('invalid quota')
    expect(apiErrorMessage({}, 'fallback')).toBe('fallback')
  })
})

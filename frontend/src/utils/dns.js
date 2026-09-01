export const DNS_TTL_AUTOMATIC = 1
export const DNS_TTL_FASTEST = 60
export const DNS_TTL_MAX = 86400

const fixedTTLOptions = [
  { value: 60, label: '最快（60 秒）' },
  { value: 120, label: '2 分钟（120 秒）' },
  { value: 300, label: '5 分钟（300 秒）' },
  { value: 600, label: '10 分钟（600 秒）' },
  { value: 1800, label: '30 分钟（1800 秒）' },
  { value: 3600, label: '1 小时（3600 秒）' },
  { value: 7200, label: '2 小时（7200 秒）' },
  { value: DNS_TTL_MAX, label: '1 天（86400 秒）' }
]

export function isCloudflareProvider(provider) {
  const type = typeof provider === 'string' ? provider : provider?.type
  return String(type || '').toLowerCase() === 'cloudflare'
}

export function ttlOptions(provider, currentValue = null) {
  const options = isCloudflareProvider(provider)
    ? [{ value: DNS_TTL_AUTOMATIC, label: '自动（由 Cloudflare 决定）' }, ...fixedTTLOptions]
    : [...fixedTTLOptions]
  const current = Number(currentValue)
  if (Number.isFinite(current) && current > 0 && !options.some(option => option.value === current)) {
    options.push({ value: current, label: `当前值（${current} 秒）` })
  }
  return options
}

export function normalizeTTLForProvider(value, provider) {
  const numeric = Number(value)
  if (isCloudflareProvider(provider) && numeric === DNS_TTL_AUTOMATIC) return DNS_TTL_AUTOMATIC
  if (!Number.isFinite(numeric) || numeric < DNS_TTL_FASTEST) return DNS_TTL_FASTEST
  return Math.min(Math.round(numeric), DNS_TTL_MAX)
}

export function ttlLabel(value, provider) {
  const numeric = Number(value)
  if (isCloudflareProvider(provider) && numeric === DNS_TTL_AUTOMATIC) return '自动'
  if (!Number.isFinite(numeric) || numeric < DNS_TTL_FASTEST) return `${DNS_TTL_FASTEST} 秒`
  return `${Math.min(Math.round(numeric), DNS_TTL_MAX)} 秒`
}

export function ttlHint(provider) {
  return isCloudflareProvider(provider)
    ? 'Cloudflare 的“自动”由服务商决定；需要更快切换不健康 Relay 时请选择“最快（60 秒）”。'
    : '已按该 DNS Provider 支持的最短 TTL 提供选项；60 秒适合需要快速切换的入口。'
}

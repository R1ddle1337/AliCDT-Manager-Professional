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

export function normalizeDNSRecordName(value, zone = '') {
  const host = String(value || '').trim().replace(/\.$/, '')
  const root = String(zone || '').trim().replace(/\.$/, '')
  if (!host) return ''
  if (host === '@' || (root && host.toLowerCase() === root.toLowerCase())) return '@'
  const suffix = `.${root}`
  return root && host.toLowerCase().endsWith(suffix.toLowerCase()) ? host.slice(0, -suffix.length) : host
}

export function composeDNSHostname(recordName, zone = '') {
  const record = normalizeDNSRecordName(recordName, zone)
  const root = String(zone || '').trim().replace(/\.$/, '')
  if (!record) return ''
  if (record === '@') return root
  return root ? `${record}.${root}` : record
}

export function isValidDNSRecordName(value, zone = '') {
  const record = normalizeDNSRecordName(value, zone)
  if (!record) return false
  if (record === '@') return true
  if (record.length > 253) return false
  return record.split('.').every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label))
}

export function dnsRecordsForHostname(records, provider, recordName, recordType) {
  if (!provider?.id) return []
  const hostname = composeDNSHostname(recordName, provider.zone).toLowerCase()
  const type = String(recordType || '').toUpperCase()
  return (Array.isArray(records) ? records : []).filter(record => String(record.provider_id) === String(provider.id)
    && String(record.type || '').toUpperCase() === type
    && composeDNSHostname(record.name, provider.zone).toLowerCase() === hostname)
}

export function reusableRelayDNSRecord(records, provider, recordName, recordType, relayNodeID) {
  const matches = dnsRecordsForHostname(records, provider, recordName, recordType)
  if (matches.length !== 1) return null
  const record = matches[0]
  if (String(record.relay_node_id) !== String(relayNodeID) || record.enabled === false || record.desired_enabled === false || ['disabled', 'deleting', 'error'].includes(record.status)) return null
  return record
}

export function safeRelayDNSHostnames(records, providers, relayNodeID) {
  const rows = Array.isArray(records) ? records : []
  const providerRows = Array.isArray(providers) ? providers : []
  const usable = rows.filter(record => record.enabled !== false && record.desired_enabled !== false && !['disabled', 'deleting', 'error'].includes(record.status) && ['A', 'AAAA'].includes(String(record.type || '').toUpperCase()))
  const result = usable.filter(record => String(record.relay_node_id) === String(relayNodeID)).map(record => {
    const provider = providerRows.find(item => String(item.id) === String(record.provider_id))
    if (!provider) return ''
    const hostname = composeDNSHostname(record.name, provider.zone)
    const peers = dnsRecordsForHostname(usable, provider, record.name, record.type)
    return peers.every(item => String(item.relay_node_id) === String(relayNodeID)) ? hostname : ''
  }).filter(Boolean)
  return [...new Set(result)]
}

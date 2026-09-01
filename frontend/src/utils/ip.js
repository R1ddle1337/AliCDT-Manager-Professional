const IPV4_PATTERN = /^(?:\d{1,3}\.){3}\d{1,3}$/
const IPV6_PATTERN = /^[0-9a-fA-F:]+(?:%[0-9a-zA-Z_.-]+)?$/

function clean(value) {
  return String(value ?? '').trim().replace(/^\[|\]$/g, '')
}

export function isIPv4(value) {
  const raw = clean(value)
  if (!IPV4_PATTERN.test(raw)) return false
  return raw.split('.').every(part => Number(part) >= 0 && Number(part) <= 255)
}

export function isIPv6(value) {
  const raw = clean(value)
  return raw.includes(':') && IPV6_PATTERN.test(raw)
}

export function isIPAddress(value) {
  return isIPv4(value) || isIPv6(value)
}

/**
 * Keep the first two address segments visible, matching the relay-node UI.
 * Host names are intentionally returned unchanged because they are not IPs.
 */
export function maskIP(value) {
  const raw = clean(value)
  if (!raw) return ''
  if (isIPv4(raw)) {
    const parts = raw.split('.')
    return `${parts[0]}.${parts[1]}.•••.•••`
  }
  if (isIPv6(raw)) {
    const parts = raw.split(':')
    const visible = parts.slice(0, 2).filter(Boolean).join(':')
    return `${visible || '•••'}:•••:•••`
  }
  return raw
}

/** Mask every IPv4 address embedded in a display string (for example a URI). */
export function maskIPsInText(value) {
  const raw = String(value ?? '')
  if (!raw) return ''
  let masked = raw.replace(/(?:\d{1,3}\.){3}\d{1,3}/g, match => (isIPv4(match) ? maskIP(match) : match))
  masked = masked.replace(/\[([0-9a-fA-F:]+(?:%[0-9a-zA-Z_.-]+)?)\]/g, (match, ip) => isIPv6(ip) ? `[${maskIP(ip)}]` : match)
  return masked
}

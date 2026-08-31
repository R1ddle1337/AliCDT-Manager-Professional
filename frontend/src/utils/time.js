// Parse timestamps produced by both the legacy Python service and the Go
// controller. Python's SQLite values are UTC strings with a space separator
// and up to six fractional-second digits; Go emits RFC3339 values that already
// carry a timezone suffix.
export function parseDate(value) {
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : value
  }

  if (typeof value === 'number') {
    const milliseconds = Math.abs(value) < 1e12 ? value * 1000 : value
    const parsed = new Date(milliseconds)
    return Number.isNaN(parsed.getTime()) ? null : parsed
  }

  if (typeof value !== 'string') return null
  let normalized = value.trim()
  if (!normalized) return null

  // Numeric timestamps are occasionally returned by older integrations.
  if (/^\d+(?:\.\d+)?$/.test(normalized)) {
    const numeric = Number(normalized)
    if (Number.isFinite(numeric)) return parseDate(numeric)
  }

  normalized = normalized.replace(' ', 'T')
  // JavaScript Date parsing is not consistent for more than milliseconds.
  normalized = normalized.replace(/(\.\d{3})\d+(?=(?:Z|[+-]\d{2}:?\d{2})?$)/i, '$1')
  if (!/(?:Z|[+-]\d{2}:?\d{2})$/i.test(normalized)) normalized += 'Z'

  const parsed = new Date(normalized)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

export function formatDateTime(value, fallback = '') {
  const parsed = parseDate(value)
  return parsed ? parsed.toLocaleString('zh-CN', { hour12: false }) : fallback
}

export function formatTime(value, fallback = '') {
  const parsed = parseDate(value)
  return parsed ? parsed.toLocaleTimeString('zh-CN', { hour12: false }) : fallback
}

const BYTES_PER_GB = 1024 ** 3

export function aggregateRelayUsage(users, services, statuses) {
  const userRows = Array.isArray(users) ? users : []
  const serviceRows = Array.isArray(services) ? services : []
  const statusRows = Array.isArray(statuses) ? statuses : []
  const userUsage = userRows.reduce((sum, user) => sum + finiteNumber(user?.traffic_used_gb), 0)
  const standaloneIDs = new Set(serviceRows.filter(service => service?.user_id == null).map(service => service.id).filter(Boolean))
  const standaloneBytes = statusRows.reduce((sum, status) => {
    if (!standaloneIDs.has(status?.id)) return sum
    return sum + finiteNumber(status?.billed_bytes)
  }, 0)
  return userUsage + standaloneBytes / BYTES_PER_GB
}

export function formatTrafficAmount(gigabytes) {
  const value = Math.max(0, finiteNumber(gigabytes))
  if (value > 0 && value < 0.01) {
    return { value: (value * 1024).toFixed(2), unit: 'MB' }
  }
  return { value: value.toFixed(2), unit: 'GB' }
}

function finiteNumber(value) {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

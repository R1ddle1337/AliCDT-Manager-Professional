function amount(value) {
  if (value === null || value === undefined || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

export function billTotal(bill) {
  if (!bill || !Array.isArray(bill.details)) return null
  let total = 0
  for (const detail of bill.details) {
    const value = amount(detail.pretax_amount)
    if (value === null) return null
    total += value
  }
  return total
}

// Outstanding debt is affected by payments. Estimate costs from the accrued
// pretax bill instead, and only for the current billing month in Beijing time.
export function estimateMonthlyCost(bill, now = new Date()) {
  const total = billTotal(bill)
  if (total === null || total <= 0 || Number.isNaN(now.getTime())) return null
  const beijing = new Date(now.getTime() + 8 * 60 * 60 * 1000)
  const year = beijing.getUTCFullYear()
  const month = beijing.getUTCMonth()
  const day = beijing.getUTCDate()
  const cycle = `${year}-${String(month + 1).padStart(2, '0')}`
  if (bill.billing_cycle !== cycle || day < 2) return null
  const daysInMonth = new Date(Date.UTC(year, month + 1, 0)).getUTCDate()
  return total / day * daysInMonth
}

export function formatMoney(value, currency = 'USD') {
  const number = amount(value)
  if (number === null) return '—'
  const options = { style: 'currency', currency, minimumFractionDigits: 2, maximumFractionDigits: 4 }
  try {
    return new Intl.NumberFormat('zh-CN', options).format(number)
  } catch (_) {
    return `${currency} ${number.toFixed(4)}`
  }
}

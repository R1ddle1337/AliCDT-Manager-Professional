import { describe, expect, it } from 'vitest'
import { billTotal, estimateMonthlyCost, formatMoney } from './billing'

describe('international account costs', () => {
  it('estimates accrued costs independently of payments and outstanding debt', () => {
    const bill = {
      billing_cycle: '2026-09',
      total_outstanding: 0,
      details: [{ pretax_amount: 8 }, { pretax_amount: 4 }],
    }
    expect(billTotal(bill)).toBe(12)
    expect(estimateMonthlyCost(bill, new Date('2026-09-12T04:00:00Z'))).toBe(30)
    expect(estimateMonthlyCost({ ...bill, total_outstanding: 200 }, new Date('2026-09-12T04:00:00Z'))).toBe(30)
  })

  it('keeps missing and invalid bills unknown while retaining known zero bills', () => {
    expect(billTotal(null)).toBeNull()
    expect(billTotal({})).toBeNull()
    expect(billTotal({ details: [{ pretax_amount: 'invalid' }] })).toBeNull()
    expect(billTotal({ details: [] })).toBe(0)
    expect(formatMoney(undefined)).toBe('—')
    expect(formatMoney(null)).toBe('—')
    expect(formatMoney(0)).toContain('0.00')
  })

  it('waits for enough data and does not project an old billing cycle into a new month', () => {
    const bill = { billing_cycle: '2026-09', details: [{ pretax_amount: 12 }] }
    expect(estimateMonthlyCost(bill, new Date('2026-09-01T05:00:00Z'))).toBeNull()
    // Already October 1 in Beijing although still September 30 in UTC.
    expect(estimateMonthlyCost(bill, new Date('2026-09-30T16:00:00Z'))).toBeNull()
    expect(estimateMonthlyCost({ ...bill, details: [] }, new Date('2026-09-12T00:00:00Z'))).toBeNull()
  })

  it('uses the real calendar month length, including leap years', () => {
    const bill = { billing_cycle: '2028-02', details: [{ pretax_amount: '10' }] }
    expect(estimateMonthlyCost(bill, new Date('2028-02-10T03:00:00Z'))).toBe(29)
  })
})

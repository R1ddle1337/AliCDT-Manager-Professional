import { describe, expect, it } from 'vitest'
import { aggregateRelayUsage, formatTrafficAmount } from './traffic'

describe('traffic helpers', () => {
  it('keeps standalone entry traffic when users also exist', () => {
    const users = [{ traffic_used_gb: 2.5 }]
    const services = [{ id: 'user-service', user_id: 7 }, { id: 'standalone-service' }]
    const statuses = [
      { id: 'user-service', billed_bytes: 9 * 1024 ** 3 },
      { id: 'standalone-service', billed_bytes: 512 * 1024 ** 2 },
      { id: 'stale-service', billed_bytes: 99 * 1024 ** 3 },
    ]
    expect(aggregateRelayUsage(users, services, statuses)).toBeCloseTo(3, 8)
  })

  it('shows small usage in MB instead of a misleading zero GB', () => {
    expect(formatTrafficAmount(3.0546 / 1024)).toEqual({ value: '3.05', unit: 'MB' })
    expect(formatTrafficAmount(1.234)).toEqual({ value: '1.23', unit: 'GB' })
    expect(formatTrafficAmount(Number.NaN)).toEqual({ value: '0.00', unit: 'GB' })
  })
})

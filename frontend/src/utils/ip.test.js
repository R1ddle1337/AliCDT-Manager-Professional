import { describe, expect, it } from 'vitest'
import { isIPv4, isIPv6, maskIP, maskIPsInText } from './ip'

describe('IP validation and masking', () => {
  it('validates IPv4 boundaries', () => {
    expect(isIPv4('203.0.113.10')).toBe(true)
    expect(isIPv4('999.0.0.1')).toBe(false)
    expect(isIPv4('1.2.3')).toBe(false)
  })

  it('validates compressed, scoped and embedded IPv6 addresses', () => {
    expect(isIPv6('2001:db8::1')).toBe(true)
    expect(isIPv6('[fe80::1%eth0]')).toBe(true)
    expect(isIPv6('::ffff:192.0.2.1')).toBe(true)
    expect(isIPv6('2001::db8::1')).toBe(false)
    expect(isIPv6('::::')).toBe(false)
    expect(isIPv6('1:2:3:4:5:6:7:8:9')).toBe(false)
  })

  it('masks addresses without changing host names or invalid lookalikes', () => {
    expect(maskIP('203.0.113.10')).toBe('203.0.•••.•••')
    expect(maskIP('relay.example.com')).toBe('relay.example.com')
    expect(maskIPsInText('tcp://203.0.113.10:25565')).toBe('tcp://203.0.•••.•••:25565')
    expect(maskIPsInText('invalid 999.0.0.1')).toBe('invalid 999.0.0.1')
  })
})

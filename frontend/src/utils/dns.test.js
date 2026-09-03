import { describe, expect, it } from 'vitest'
import { DNS_TTL_AUTOMATIC, DNS_TTL_MAX, normalizeTTLForProvider, ttlLabel, ttlOptions } from './dns'

describe('DNS TTL provider compatibility', () => {
  it('preserves Cloudflare automatic TTL but not for Aliyun', () => {
    expect(normalizeTTLForProvider(DNS_TTL_AUTOMATIC, 'cloudflare')).toBe(1)
    expect(normalizeTTLForProvider(DNS_TTL_AUTOMATIC, 'aliyun')).toBe(60)
    expect(ttlLabel(1, { type: 'cloudflare' })).toBe('自动')
  })

  it('bounds invalid and excessive fixed TTL values', () => {
    expect(normalizeTTLForProvider('invalid', 'aliyun')).toBe(60)
    expect(normalizeTTLForProvider(59, 'aliyun')).toBe(60)
    expect(normalizeTTLForProvider(DNS_TTL_MAX + 1, 'aliyun')).toBe(DNS_TTL_MAX)
  })

  it('keeps a provider-specific existing value editable', () => {
    expect(ttlOptions('aliyun', 900).some(option => option.value === 900)).toBe(true)
    expect(ttlOptions('cloudflare').at(0).value).toBe(1)
  })
})

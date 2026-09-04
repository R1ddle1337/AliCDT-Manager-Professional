import { describe, expect, it } from 'vitest'
import { composeDNSHostname, dnsRecordsForHostname, DNS_TTL_AUTOMATIC, DNS_TTL_MAX, isValidDNSRecordName, normalizeDNSRecordName, normalizeTTLForProvider, reusableRelayDNSRecord, safeRelayDNSHostnames, ttlLabel, ttlOptions } from './dns'

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

  it('normalizes and composes managed hostnames', () => {
    expect(normalizeDNSRecordName('mc.example.com.', 'example.com')).toBe('mc')
    expect(normalizeDNSRecordName('example.com', 'example.com')).toBe('@')
    expect(composeDNSHostname('mc.eu', 'example.com')).toBe('mc.eu.example.com')
    expect(composeDNSHostname('@', 'example.com')).toBe('example.com')
  })

  it('rejects record labels that are unsafe for a Minecraft hostname', () => {
    expect(isValidDNSRecordName('mc-1.eu', 'example.com')).toBe(true)
    expect(isValidDNSRecordName('-mc', 'example.com')).toBe(false)
    expect(isValidDNSRecordName('mc..eu', 'example.com')).toBe(false)
    expect(isValidDNSRecordName('mc/other', 'example.com')).toBe(false)
  })

  it('allows a dedicated Relay hostname but rejects shared multi-A aliases', () => {
    const provider = { id: 'dns-1', zone: 'example.com' }
    const records = [
      { id: 'shared-a', provider_id: 'dns-1', relay_node_id: 'relay-a', name: 'shared', type: 'A', status: 'synced', enabled: true, desired_enabled: true },
      { id: 'shared-b', provider_id: 'dns-1', relay_node_id: 'relay-b', name: 'shared.example.com', type: 'A', status: 'synced', enabled: true, desired_enabled: true },
      { id: 'mc-a', provider_id: 'dns-1', relay_node_id: 'relay-a', name: 'mc', type: 'A', status: 'synced', enabled: true, desired_enabled: true },
    ]
    expect(dnsRecordsForHostname(records, provider, 'shared', 'A')).toHaveLength(2)
    expect(reusableRelayDNSRecord(records, provider, 'shared', 'A', 'relay-a')).toBeNull()
    expect(reusableRelayDNSRecord(records, provider, 'mc', 'A', 'relay-a')?.id).toBe('mc-a')
    expect(safeRelayDNSHostnames(records, [provider], 'relay-a')).toEqual(['mc.example.com'])
  })
})

import { describe, expect, it } from 'vitest'
import { parseDate } from './time'

describe('legacy and current timestamp parsing', () => {
  it('treats historical timezone-less SQLite values as UTC', () => {
    expect(parseDate('2026-09-03 12:34:56')?.toISOString()).toBe('2026-09-03T12:34:56.000Z')
  })

  it('normalizes microseconds and numeric seconds', () => {
    expect(parseDate('2026-09-03T12:34:56.123456Z')?.toISOString()).toBe('2026-09-03T12:34:56.123Z')
    expect(parseDate(1_700_000_000)?.toISOString()).toBe('2023-11-14T22:13:20.000Z')
  })

  it('rejects empty and invalid timestamps', () => {
    expect(parseDate('')).toBeNull()
    expect(parseDate('not-a-date')).toBeNull()
    expect(parseDate(new Date('invalid'))).toBeNull()
  })
})

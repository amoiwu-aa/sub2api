import { describe, expect, it, vi } from 'vitest'

import {
  distributionDateRange,
  distributionTodayRange,
  formatLocalDate,
  newIdempotencyKey,
  resolveInviteLink
} from '../distributionRange'

describe('distributionRange', () => {
  it('formats a local calendar date', () => {
    expect(formatLocalDate(new Date(2026, 7, 19))).toBe('2026-08-19')
  })

  it('builds a 7-day inclusive range', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 7, 19, 15, 0, 0))
    expect(distributionDateRange('7d')).toEqual({
      start_date: '2026-08-13',
      end_date: '2026-08-19'
    })
    vi.useRealTimers()
  })

  it('builds a same-day range for today', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 7, 19, 15, 0, 0))
    expect(distributionTodayRange()).toEqual({
      start_date: '2026-08-19',
      end_date: '2026-08-19'
    })
    vi.useRealTimers()
  })

  it('resolves relative invite paths against the current origin', () => {
    expect(resolveInviteLink('/register?aff=ABC')).toBe(`${window.location.origin}/register?aff=ABC`)
    expect(resolveInviteLink('https://example.com/register?aff=ABC')).toBe(
      'https://example.com/register?aff=ABC'
    )
  })

  it('creates a non-empty idempotency key', () => {
    expect(newIdempotencyKey().length).toBeGreaterThan(8)
  })
})

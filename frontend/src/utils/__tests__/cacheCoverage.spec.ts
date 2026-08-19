import { describe, expect, it } from 'vitest'

import { getCacheCoverageMetrics } from '../cacheCoverage'

describe('getCacheCoverageMetrics', () => {
  it('hides coverage for mixed traffic when reported token buckets are missing', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 3,
      reported_requests: 1,
      estimated_requests: 1,
      unavailable_requests: 1,
      input_tokens: 100,
      cache_creation_tokens: 100,
      cache_read_tokens: 900,
      provider_cache_read_tokens: 300,
      forced_cache_read_tokens: 600
    })

    expect(metrics.usesReportedSubset).toBe(false)
    expect(metrics.coverageAvailable).toBe(false)
    expect(metrics.observability.partiallyObservable).toBe(true)
    expect(metrics.input).toBe(700)
    expect(metrics.providerRead).toBe(300)
  })

  it('uses the reported subset so mixed Cursor/Grok traffic can still show a percentage', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 3,
      reported_requests: 1,
      estimated_requests: 1,
      unavailable_requests: 1,
      input_tokens: 100,
      cache_creation_tokens: 100,
      cache_read_tokens: 900,
      provider_cache_read_tokens: 300,
      forced_cache_read_tokens: 600,
      reported_input_tokens: 100,
      reported_cache_creation_tokens: 0,
      reported_forced_cache_read_tokens: 300
    })

    expect(metrics.usesReportedSubset).toBe(true)
    expect(metrics.coverageAvailable).toBe(true)
    expect(metrics.observability.partiallyObservable).toBe(true)
    expect(metrics.observability.excluded).toBe(1)
    expect(metrics.observability.total).toBe(2)
    expect(metrics.input).toBe(400)
    expect(metrics.creation).toBe(0)
    expect(metrics.providerRead).toBe(300)
    expect(metrics.forcedAdjustment).toBe(300)
    expect(metrics.coverage).toBeCloseTo(42.857, 2)
  })

  it('does not treat un-backfilled 0/0/0 buckets as a 100% reported subset', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 10,
      reported_requests: 2,
      estimated_requests: 8,
      input_tokens: 800,
      cache_creation_tokens: 0,
      cache_read_tokens: 200,
      provider_cache_read_tokens: 200,
      forced_cache_read_tokens: 0,
      reported_input_tokens: 0,
      reported_cache_creation_tokens: 0,
      reported_forced_cache_read_tokens: 0
    })

    expect(metrics.usesReportedSubset).toBe(false)
    expect(metrics.coverageAvailable).toBe(false)
    expect(metrics.observability.partiallyObservable).toBe(false)
    expect(metrics.observability.excluded).toBe(8)
    expect(metrics.observability.total).toBe(2)
  })

  it('keeps full-token coverage when every request is provider-reported', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 2,
      reported_requests: 2,
      estimated_requests: 0,
      unavailable_requests: 0,
      input_tokens: 100,
      cache_creation_tokens: 100,
      cache_read_tokens: 300,
      provider_cache_read_tokens: 300,
      forced_cache_read_tokens: 0
    })

    expect(metrics.usesReportedSubset).toBe(false)
    expect(metrics.coverageAvailable).toBe(true)
    expect(metrics.coverage).toBe(60)
  })

  it('stays unobservable when no request is provider-reported', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 2,
      reported_requests: 0,
      estimated_requests: 2,
      input_tokens: 0,
      cache_read_tokens: 500,
      provider_cache_read_tokens: 0,
      forced_cache_read_tokens: 500
    })

    expect(metrics.coverageAvailable).toBe(false)
    expect(metrics.observability.unobservable).toBe(true)
    expect(metrics.observability.available).toBe(false)
    expect(metrics.observability.excluded).toBe(2)
    expect(metrics.observability.total).toBe(0)
  })

  it('does not let Cursor-only estimated traffic downgrade reported traffic observability', () => {
    const metrics = getCacheCoverageMetrics({
      requests: 305,
      reported_requests: 248,
      estimated_requests: 57,
      unavailable_requests: 0,
      reported_input_tokens: 5_560_000,
      reported_cache_creation_tokens: 0,
      reported_forced_cache_read_tokens: 0,
      provider_cache_read_tokens: 40_630_000
    })

    expect(metrics.observability.reported).toBe(248)
    expect(metrics.observability.excluded).toBe(57)
    expect(metrics.observability.total).toBe(248)
    expect(metrics.observability.ratio).toBe(100)
    expect(metrics.observability.partiallyObservable).toBe(false)
    expect(metrics.coverageAvailable).toBe(true)
  })
})

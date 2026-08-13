export interface CacheCoverageSource {
  input_tokens?: number | null
  cache_creation_tokens?: number | null
  cache_read_tokens?: number | null
  provider_cache_read_tokens?: number | null
  forced_cache_read_tokens?: number | null
  requests?: number | null
  reported_requests?: number | null
  estimated_requests?: number | null
  unavailable_requests?: number | null
}

export interface CacheObservability {
  reported: number
  estimated: number
  unavailable: number
  unknown: number
  total: number
  ratio: number
  available: boolean
  partiallyObservable: boolean
  unobservable: boolean
}

export interface CacheCoverageMetrics {
  input: number
  creation: number
  providerRead: number
  forcedAdjustment: number
  total: number
  coverage: number
  observability: CacheObservability
}

const toNonNegativeNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? Math.max(numberValue, 0) : 0
}

export const resolveProviderCacheReadTokens = (source: CacheCoverageSource): number => {
  if (source.provider_cache_read_tokens !== undefined && source.provider_cache_read_tokens !== null) {
    return toNonNegativeNumber(source.provider_cache_read_tokens)
  }

  const hasObservationContract = [
    source.reported_requests,
    source.estimated_requests,
    source.unavailable_requests
  ].some((value) => value !== undefined && value !== null)
  if (hasObservationContract) return 0

  return Math.max(
    toNonNegativeNumber(source.cache_read_tokens) -
      toNonNegativeNumber(source.forced_cache_read_tokens),
    0
  )
}

export const getCacheObservability = (source: CacheCoverageSource): CacheObservability => {
  const reported = toNonNegativeNumber(source.reported_requests)
  const estimated = toNonNegativeNumber(source.estimated_requests)
  const unavailable = toNonNegativeNumber(source.unavailable_requests)
  const classified = reported + estimated + unavailable
  const total = Math.max(classified, toNonNegativeNumber(source.requests))
  const unknown = Math.max(total - classified, 0)
  const available = total > 0 && [
    source.reported_requests,
    source.estimated_requests,
    source.unavailable_requests
  ].some((value) => value !== undefined && value !== null)

  return {
    reported,
    estimated,
    unavailable,
    unknown,
    total,
    ratio: total > 0 ? (reported / total) * 100 : 0,
    available,
    partiallyObservable: available && reported > 0 && reported < total,
    unobservable: available && reported === 0 && estimated + unavailable + unknown > 0
  }
}

export const getCacheCoverageMetrics = (source: CacheCoverageSource): CacheCoverageMetrics => {
  const forcedAdjustment = toNonNegativeNumber(source.forced_cache_read_tokens)
  const input = toNonNegativeNumber(source.input_tokens) + forcedAdjustment
  const creation = toNonNegativeNumber(source.cache_creation_tokens)
  const providerRead = resolveProviderCacheReadTokens(source)
  const total = input + creation + providerRead

  return {
    input,
    creation,
    providerRead,
    forcedAdjustment,
    total,
    coverage: total > 0 ? (providerRead / total) * 100 : 0,
    observability: getCacheObservability(source)
  }
}

export const formatCacheObservability = (observability: CacheObservability): string => {
  if (!observability.available) return ''

  const fraction = `${observability.reported}/${observability.total}`
  return observability.reported > 0
    ? `${fraction} (${observability.ratio.toFixed(1)}%)`
    : fraction
}

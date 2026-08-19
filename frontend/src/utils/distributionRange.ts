export type DistributionRangePreset = '7d' | '30d'

export function formatLocalDate(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function distributionDateRange(preset: DistributionRangePreset): {
  start_date: string
  end_date: string
} {
  const end = new Date()
  const start = new Date()
  start.setDate(end.getDate() - (preset === '30d' ? 29 : 6))
  return {
    start_date: formatLocalDate(start),
    end_date: formatLocalDate(end)
  }
}

export function distributionTodayRange(): {
  start_date: string
  end_date: string
} {
  const today = formatLocalDate(new Date())
  return {
    start_date: today,
    end_date: today
  }
}

export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

export function resolveInviteLink(pathOrUrl: string): string {
  const raw = pathOrUrl.trim()
  if (!raw) return ''
  if (/^https?:\/\//i.test(raw)) return raw
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  return `${origin}${raw.startsWith('/') ? raw : `/${raw}`}`
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

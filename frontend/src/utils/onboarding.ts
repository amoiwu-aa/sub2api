import type { User } from '@/types'

const NEW_USER_ONBOARDING_PENDING_KEY = 'new_user_onboarding_pending_v1'
const NEW_USER_ONBOARDING_DECISION_PREFIX = 'new_user_onboarding_decision_v1'
const RECENT_REGISTRATION_WINDOW_MS = 30 * 60 * 1000

export type NewUserOnboardingDecision = 'started' | 'skipped'

interface PendingOnboardingMarker {
  user_id?: number
  marked_at: number
}

function decisionKey(userID: number): string {
  return `${NEW_USER_ONBOARDING_DECISION_PREFIX}_${userID}`
}

function readPendingMarker(): PendingOnboardingMarker | null {
  if (typeof window === 'undefined') return null

  const raw = window.sessionStorage.getItem(NEW_USER_ONBOARDING_PENDING_KEY)
  if (!raw) return null

  try {
    const parsed = JSON.parse(raw) as Partial<PendingOnboardingMarker>
    if (typeof parsed.marked_at !== 'number') {
      window.sessionStorage.removeItem(NEW_USER_ONBOARDING_PENDING_KEY)
      return null
    }
    return {
      user_id: typeof parsed.user_id === 'number' ? parsed.user_id : undefined,
      marked_at: parsed.marked_at
    }
  } catch {
    window.sessionStorage.removeItem(NEW_USER_ONBOARDING_PENDING_KEY)
    return null
  }
}

export function markNewUserOnboardingPending(userID?: number): void {
  if (typeof window === 'undefined') return

  const marker: PendingOnboardingMarker = {
    user_id: userID,
    marked_at: Date.now()
  }
  window.sessionStorage.setItem(NEW_USER_ONBOARDING_PENDING_KEY, JSON.stringify(marker))
}

export function clearNewUserOnboardingPending(): void {
  if (typeof window === 'undefined') return
  window.sessionStorage.removeItem(NEW_USER_ONBOARDING_PENDING_KEY)
}

export function getNewUserOnboardingDecision(
  userID: number
): NewUserOnboardingDecision | null {
  if (typeof window === 'undefined') return null

  const decision = window.localStorage.getItem(decisionKey(userID))
  return decision === 'started' || decision === 'skipped' ? decision : null
}

export function recordNewUserOnboardingDecision(
  userID: number,
  decision: NewUserOnboardingDecision
): void {
  if (typeof window === 'undefined') return

  window.localStorage.setItem(decisionKey(userID), decision)
  clearNewUserOnboardingPending()
}

export function shouldPromptNewUserOnboarding(
  user: User | null | undefined,
  now = Date.now()
): boolean {
  if (!user || user.role !== 'user' || getNewUserOnboardingDecision(user.id)) {
    return false
  }

  const pending = readPendingMarker()
  if (pending && (pending.user_id === undefined || pending.user_id === user.id)) {
    return true
  }

  const createdAt = Date.parse(user.created_at)
  return (
    Number.isFinite(createdAt) &&
    createdAt <= now &&
    now - createdAt <= RECENT_REGISTRATION_WINDOW_MS
  )
}


import { beforeEach, describe, expect, it } from 'vitest'
import type { User } from '@/types'
import {
  markNewUserOnboardingPending,
  recordNewUserOnboardingDecision,
  shouldPromptNewUserOnboarding
} from '@/utils/onboarding'

const NOW = Date.parse('2026-08-20T01:00:00Z')

function createUser(overrides: Partial<User> = {}): User {
  return {
    id: 7,
    email: 'new-user@example.com',
    username: 'new-user',
    role: 'user',
    balance: 0,
    concurrency: 5,
    status: 'active',
    allowed_groups: null,
    balance_notify_enabled: true,
    balance_notify_threshold: null,
    balance_notify_extra_emails: [],
    created_at: '2026-08-20T00:45:00Z',
    updated_at: '2026-08-20T00:45:00Z',
    ...overrides
  }
}

describe('new user onboarding preference', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('prompts a recently registered regular user', () => {
    expect(shouldPromptNewUserOnboarding(createUser(), NOW)).toBe(true)
  })

  it('uses the explicit registration marker even when created_at is old', () => {
    const user = createUser({ created_at: '2025-01-01T00:00:00Z' })
    markNewUserOnboardingPending(user.id)

    expect(shouldPromptNewUserOnboarding(user, NOW)).toBe(true)
  })

  it('does not reuse another user registration marker', () => {
    markNewUserOnboardingPending(99)

    expect(
      shouldPromptNewUserOnboarding(
        createUser({ created_at: '2025-01-01T00:00:00Z' }),
        NOW
      )
    ).toBe(false)
  })

  it.each(['started', 'skipped'] as const)(
    'does not prompt again after the user has chosen %s',
    (decision) => {
      const user = createUser()
      markNewUserOnboardingPending(user.id)
      recordNewUserOnboardingDecision(user.id, decision)

      expect(shouldPromptNewUserOnboarding(user, NOW)).toBe(false)
    }
  )

  it('does not show the new-user prompt to administrators', () => {
    expect(
      shouldPromptNewUserOnboarding(createUser({ role: 'admin' }), NOW)
    ).toBe(false)
  })
})

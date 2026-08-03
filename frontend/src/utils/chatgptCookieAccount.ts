import type { Account } from '@/types'

export type ChatGPTCookieExpiryWarningKey =
  | 'admin.accounts.expiresWithin24Hours'
  | 'admin.accounts.expiresWithin3Days'

export function chatgptCookieExpiryWarningKey(
  account: Account,
  expiresAt: number | null,
  now = Date.now()
): ChatGPTCookieExpiryWarningKey | null {
  if (
    !expiresAt ||
    account.extra?.openai_credential_source !== 'chatgpt_cookie'
  ) {
    return null
  }

  const remainingMs = expiresAt * 1000 - now
  if (remainingMs <= 0) return null
  if (remainingMs <= 24 * 60 * 60 * 1000) {
    return 'admin.accounts.expiresWithin24Hours'
  }
  if (remainingMs <= 3 * 24 * 60 * 60 * 1000) {
    return 'admin.accounts.expiresWithin3Days'
  }
  return null
}

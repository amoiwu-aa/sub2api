import { describe, expect, it } from 'vitest'
import type { Account } from '@/types'
import { chatgptCookieExpiryWarningKey } from '@/utils/chatgptCookieAccount'

const now = Date.UTC(2026, 7, 3, 8, 0, 0)
const hoursFromNow = (hours: number) => Math.floor((now + hours * 60 * 60 * 1000) / 1000)

const cookieAccount = {
  extra: {
    openai_credential_source: 'chatgpt_cookie'
  }
} as Account

describe('chatgptCookieExpiryWarningKey', () => {
  it('returns the 24-hour and three-day warning levels', () => {
    expect(chatgptCookieExpiryWarningKey(cookieAccount, hoursFromNow(23), now))
      .toBe('admin.accounts.expiresWithin24Hours')
    expect(chatgptCookieExpiryWarningKey(cookieAccount, hoursFromNow(48), now))
      .toBe('admin.accounts.expiresWithin3Days')
  })

  it('ignores expired, distant, and non-cookie accounts', () => {
    expect(chatgptCookieExpiryWarningKey(cookieAccount, hoursFromNow(-1), now)).toBeNull()
    expect(chatgptCookieExpiryWarningKey(cookieAccount, hoursFromNow(96), now)).toBeNull()
    expect(chatgptCookieExpiryWarningKey({ extra: {} } as Account, hoursFromNow(23), now))
      .toBeNull()
  })
})

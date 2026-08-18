import { describe, expect, it } from 'vitest'

import { isAffiliateAdminAllowedAdminPath, resolvePanelHomePath } from '@/utils/adminAccess'

describe('resolvePanelHomePath', () => {
  it('sends super admins to the dashboard', () => {
    expect(resolvePanelHomePath(true)).toBe('/admin/dashboard')
    expect(resolvePanelHomePath(true, true)).toBe('/admin/dashboard')
  })

  it('sends affiliate admins to user management', () => {
    expect(resolvePanelHomePath(false, true)).toBe('/admin/users')
  })

  it('sends regular users to the user dashboard', () => {
    expect(resolvePanelHomePath(false)).toBe('/dashboard')
    expect(resolvePanelHomePath(false, false)).toBe('/dashboard')
  })
})

describe('isAffiliateAdminAllowedAdminPath', () => {
  it('allows only the users page', () => {
    expect(isAffiliateAdminAllowedAdminPath('/admin/users')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/users/')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/dashboard')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/admin/settings')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/dashboard')).toBe(false)
  })
})

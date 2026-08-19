import { describe, expect, it } from 'vitest'

import { isAffiliateAdminAllowedAdminPath, resolvePanelHomePath } from '@/utils/adminAccess'

describe('resolvePanelHomePath', () => {
  it('sends super admins to the dashboard', () => {
    expect(resolvePanelHomePath(true)).toBe('/admin/dashboard')
    expect(resolvePanelHomePath(true, true)).toBe('/admin/dashboard')
  })

  it('sends affiliate admins to the distribution dashboard', () => {
    expect(resolvePanelHomePath(false, true)).toBe('/admin/distribution/dashboard')
  })

  it('sends regular users to the user dashboard', () => {
    expect(resolvePanelHomePath(false)).toBe('/dashboard')
    expect(resolvePanelHomePath(false, false)).toBe('/dashboard')
  })
})

describe('isAffiliateAdminAllowedAdminPath', () => {
  it('allows user management and the distribution center', () => {
    expect(isAffiliateAdminAllowedAdminPath('/admin/users')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/users/')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/users/12')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/distribution')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/distribution/dashboard')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/distribution/usage')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/distribution/balance')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/distribution/invites')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/announcements')).toBe(true)
    expect(isAffiliateAdminAllowedAdminPath('/admin/announcements/12')).toBe(true)
  })

  it('blocks other admin and user paths', () => {
    expect(isAffiliateAdminAllowedAdminPath('/admin/dashboard')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/admin/settings')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/admin/affiliates')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/admin/affiliates/invites')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/admin/groups')).toBe(false)
    expect(isAffiliateAdminAllowedAdminPath('/dashboard')).toBe(false)
  })
})

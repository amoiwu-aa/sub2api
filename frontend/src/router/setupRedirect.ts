import { resolvePanelHomePath } from '@/utils/adminAccess'

export function resolveCompletedSetupRedirectPath(
  isAuthenticated: boolean,
  isAdmin: boolean,
  isAffiliateAdmin = false
): string {
  if (!isAuthenticated) {
    return '/login'
  }

  return resolvePanelHomePath(isAdmin, isAffiliateAdmin)
}

/** Home path after login / setup for the current panel role. */
export function resolvePanelHomePath(isAdmin: boolean, isAffiliateAdmin = false): string {
  if (isAdmin) {
    return '/admin/dashboard'
  }
  if (isAffiliateAdmin) {
    return '/admin/users'
  }
  return '/dashboard'
}

/** Affiliate admins may only use the user-management page. */
export function isAffiliateAdminAllowedAdminPath(path: string): boolean {
  return path === '/admin/users' || path.startsWith('/admin/users/')
}

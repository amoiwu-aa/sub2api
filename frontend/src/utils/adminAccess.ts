/** Home path after login / setup for the current panel role. */
export function resolvePanelHomePath(isAdmin: boolean, isAffiliateAdmin = false): string {
  if (isAdmin) {
    return '/admin/dashboard'
  }
  if (isAffiliateAdmin) {
    return '/admin/distribution/dashboard'
  }
  return '/dashboard'
}

/** Affiliate admins may use user management and the distribution center. */
export function isAffiliateAdminAllowedAdminPath(path: string): boolean {
  return (
    path === '/admin/users' ||
    path.startsWith('/admin/users/') ||
    path === '/admin/distribution' ||
    path.startsWith('/admin/distribution/') ||
    path === '/admin/announcements' ||
    path.startsWith('/admin/announcements/')
  )
}

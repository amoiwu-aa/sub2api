/** CC-Switch 官方发布页。直链失效或用户要便携版 / 旧版时走这里。 */
export const CC_SWITCH_RELEASES_URL = 'https://github.com/farion1231/cc-switch/releases'

const GITHUB_LATEST_API = 'https://api.github.com/repos/farion1231/cc-switch/releases/latest'

export type CcSwitchDesktopOs = 'windows' | 'macos' | 'linux' | 'other'
export type CcSwitchArch = 'x64' | 'arm64'

export interface CcSwitchDownloadLinks {
  windows: string
  windowsArm: string
  macos: string
  linux: string
  linuxArm: string
  releasesUrl: string
}

/**
 * 拉取 GitHub 失败时的兜底直链。文件名带版本号，新版本发布后仍可下到可用安装包，
 * 只是不一定是最新；成功拉到 latest 后会被覆盖。
 */
export const CC_SWITCH_FALLBACK_DOWNLOADS: CcSwitchDownloadLinks = {
  windows:
    'https://github.com/farion1231/cc-switch/releases/download/v3.20.0/CC-Switch-v3.20.0-Windows.msi',
  windowsArm:
    'https://github.com/farion1231/cc-switch/releases/download/v3.20.0/CC-Switch-v3.20.0-Windows-arm64.msi',
  macos:
    'https://github.com/farion1231/cc-switch/releases/download/v3.20.0/CC-Switch-v3.20.0-macOS.dmg',
  linux:
    'https://github.com/farion1231/cc-switch/releases/download/v3.20.0/CC-Switch-v3.20.0-Linux-x86_64.AppImage',
  linuxArm:
    'https://github.com/farion1231/cc-switch/releases/download/v3.20.0/CC-Switch-v3.20.0-Linux-arm64.AppImage',
  releasesUrl: CC_SWITCH_RELEASES_URL
}

type GithubReleaseAsset = {
  name?: string
  browser_download_url?: string
}

const ASSET_PATTERNS: Record<Exclude<keyof CcSwitchDownloadLinks, 'releasesUrl'>, RegExp> = {
  windows: /^CC-Switch-v[\d.]+-Windows\.msi$/i,
  windowsArm: /^CC-Switch-v[\d.]+-Windows-arm64\.msi$/i,
  macos: /^CC-Switch-v[\d.]+-macOS\.dmg$/i,
  linux: /^CC-Switch-v[\d.]+-Linux-x86_64\.AppImage$/i,
  linuxArm: /^CC-Switch-v[\d.]+-Linux-arm64\.AppImage$/i
}

export function detectCcSwitchDesktopOs(
  userAgent = typeof navigator === 'undefined' ? '' : navigator.userAgent
): CcSwitchDesktopOs {
  if (/Windows/i.test(userAgent)) return 'windows'
  if (/Mac OS X|Macintosh/i.test(userAgent)) return 'macos'
  if (/Linux|X11/i.test(userAgent) && !/Android/i.test(userAgent)) return 'linux'
  return 'other'
}

export function detectCcSwitchArch(
  userAgent = typeof navigator === 'undefined' ? '' : navigator.userAgent
): CcSwitchArch {
  if (/arm64|aarch64/i.test(userAgent)) return 'arm64'
  return 'x64'
}

export function pickCcSwitchDownloadLinks(
  assets: GithubReleaseAsset[],
  fallback: CcSwitchDownloadLinks = CC_SWITCH_FALLBACK_DOWNLOADS
): CcSwitchDownloadLinks {
  const links: CcSwitchDownloadLinks = { ...fallback }
  for (const [key, pattern] of Object.entries(ASSET_PATTERNS) as [
    Exclude<keyof CcSwitchDownloadLinks, 'releasesUrl'>,
    RegExp
  ][]) {
    const asset = assets.find((item) => item.name && pattern.test(item.name) && item.browser_download_url)
    if (asset?.browser_download_url) {
      links[key] = asset.browser_download_url
    }
  }
  return links
}

export function resolveCcSwitchInstallerUrl(
  os: CcSwitchDesktopOs,
  arch: CcSwitchArch,
  links: CcSwitchDownloadLinks
): string | null {
  if (os === 'windows') return arch === 'arm64' ? links.windowsArm : links.windows
  if (os === 'macos') return links.macos
  if (os === 'linux') return arch === 'arm64' ? links.linuxArm : links.linux
  return null
}

let cachedLinks: Promise<CcSwitchDownloadLinks> | null = null

export function resetCcSwitchDownloadLinksCache() {
  cachedLinks = null
}

export async function loadCcSwitchDownloadLinks(
  fetcher: typeof fetch = fetch
): Promise<CcSwitchDownloadLinks> {
  if (!cachedLinks) {
    cachedLinks = fetchLatestDownloadLinks(fetcher).catch(() => CC_SWITCH_FALLBACK_DOWNLOADS)
  }
  return cachedLinks
}

async function fetchLatestDownloadLinks(fetcher: typeof fetch): Promise<CcSwitchDownloadLinks> {
  const response = await fetcher(GITHUB_LATEST_API, {
    headers: { Accept: 'application/vnd.github+json' }
  })
  if (!response.ok) {
    throw new Error(`CC-Switch release lookup failed: ${response.status}`)
  }
  const body = (await response.json()) as { assets?: GithubReleaseAsset[] }
  return pickCcSwitchDownloadLinks(body.assets || [])
}

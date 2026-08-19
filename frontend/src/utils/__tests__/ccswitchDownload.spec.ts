import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  CC_SWITCH_FALLBACK_DOWNLOADS,
  CC_SWITCH_RELEASES_URL,
  detectCcSwitchArch,
  detectCcSwitchDesktopOs,
  loadCcSwitchDownloadLinks,
  pickCcSwitchDownloadLinks,
  resetCcSwitchDownloadLinksCache,
  resolveCcSwitchInstallerUrl
} from '@/utils/ccswitchDownload'

const latestAssets = [
  {
    name: 'CC-Switch-v3.21.0-Windows.msi',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-Windows.msi'
  },
  {
    name: 'CC-Switch-v3.21.0-Windows-arm64.msi',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-Windows-arm64.msi'
  },
  {
    name: 'CC-Switch-v3.21.0-macOS.dmg',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-macOS.dmg'
  },
  {
    name: 'CC-Switch-v3.21.0-Linux-x86_64.AppImage',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-Linux-x86_64.AppImage'
  },
  {
    name: 'CC-Switch-v3.21.0-Linux-arm64.AppImage',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-Linux-arm64.AppImage'
  },
  {
    name: 'CC-Switch-v3.21.0-Windows-Portable.zip',
    browser_download_url:
      'https://github.com/farion1231/cc-switch/releases/download/v3.21.0/CC-Switch-v3.21.0-Windows-Portable.zip'
  }
]

describe('ccswitchDownload utils', () => {
  afterEach(() => {
    resetCcSwitchDownloadLinksCache()
  })

  it('detects desktop OS from the user agent', () => {
    expect(detectCcSwitchDesktopOs('Mozilla/5.0 (Windows NT 10.0; Win64; x64)')).toBe('windows')
    expect(detectCcSwitchDesktopOs('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)')).toBe('macos')
    expect(detectCcSwitchDesktopOs('Mozilla/5.0 (X11; Linux x86_64)')).toBe('linux')
    expect(detectCcSwitchDesktopOs('Mozilla/5.0 (Linux; Android 14)')).toBe('other')
  })

  it('detects arm64 installers from the user agent', () => {
    expect(detectCcSwitchArch('Mozilla/5.0 (Windows NT 10.0; ARM64)')).toBe('arm64')
    expect(detectCcSwitchArch('Mozilla/5.0 (X11; Linux aarch64)')).toBe('arm64')
    expect(detectCcSwitchArch('Mozilla/5.0 (Windows NT 10.0; Win64; x64)')).toBe('x64')
  })

  it('picks installer assets and ignores portable archives', () => {
    const links = pickCcSwitchDownloadLinks(latestAssets)

    expect(links.windows).toContain('v3.21.0-Windows.msi')
    expect(links.windowsArm).toContain('Windows-arm64.msi')
    expect(links.macos).toContain('macOS.dmg')
    expect(links.linux).toContain('Linux-x86_64.AppImage')
    expect(links.linuxArm).toContain('Linux-arm64.AppImage')
    expect(links.releasesUrl).toBe(CC_SWITCH_RELEASES_URL)
    expect(links.windows).not.toContain('Portable')
  })

  it('keeps fallback URLs when a platform asset is missing', () => {
    const links = pickCcSwitchDownloadLinks([latestAssets[2]])

    expect(links.macos).toContain('v3.21.0-macOS.dmg')
    expect(links.windows).toBe(CC_SWITCH_FALLBACK_DOWNLOADS.windows)
  })

  it('resolves the installer that matches the current OS and arch', () => {
    const links = pickCcSwitchDownloadLinks(latestAssets)

    expect(resolveCcSwitchInstallerUrl('windows', 'x64', links)).toBe(links.windows)
    expect(resolveCcSwitchInstallerUrl('windows', 'arm64', links)).toBe(links.windowsArm)
    expect(resolveCcSwitchInstallerUrl('macos', 'arm64', links)).toBe(links.macos)
    expect(resolveCcSwitchInstallerUrl('linux', 'arm64', links)).toBe(links.linuxArm)
    expect(resolveCcSwitchInstallerUrl('other', 'x64', links)).toBeNull()
  })

  it('loads latest GitHub assets and caches the result', async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ assets: latestAssets })
    })

    const first = await loadCcSwitchDownloadLinks(fetcher as unknown as typeof fetch)
    const second = await loadCcSwitchDownloadLinks(fetcher as unknown as typeof fetch)

    expect(first.windows).toContain('v3.21.0-Windows.msi')
    expect(second).toEqual(first)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('falls back to the pinned release when GitHub is unavailable', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('network'))

    await expect(loadCcSwitchDownloadLinks(fetcher as unknown as typeof fetch)).resolves.toEqual(
      CC_SWITCH_FALLBACK_DOWNLOADS
    )
  })
})

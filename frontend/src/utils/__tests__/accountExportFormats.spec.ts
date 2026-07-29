import { describe, expect, it } from 'vitest'
import {
  buildCsvContent,
  buildExportArtifact,
  buildNativeCredential,
  formatHonorsProxyChoice,
  formatNeedsProxies
} from '@/utils/accountExportFormats'
import type { AdminDataAccount, AdminDataPayload } from '@/types'

const NOW = new Date(2026, 6, 27, 9, 5, 3)

function account(overrides: Partial<AdminDataAccount> = {}): AdminDataAccount {
  return {
    name: 'acc-1',
    platform: 'anthropic',
    type: 'oauth',
    credentials: { refresh_token: 'r', access_token: 'a' },
    concurrency: 2,
    priority: 50,
    ...overrides
  } as AdminDataAccount
}

function payload(overrides: Partial<AdminDataPayload> = {}): AdminDataPayload {
  return {
    exported_at: '2026-07-27T01:05:03Z',
    proxies: [],
    accounts: [account()],
    ...overrides
  }
}

describe('formatNeedsProxies', () => {
  it('honours the checkbox only for the backup format', () => {
    expect(formatNeedsProxies('backup', true)).toBe(true)
    expect(formatNeedsProxies('backup', false)).toBe(false)
    expect(formatHonorsProxyChoice('backup')).toBe(true)
  })

  it('always fetches proxies for CSV so the proxy column is not blank', () => {
    expect(formatNeedsProxies('csv', false)).toBe(true)
    expect(formatHonorsProxyChoice('csv')).toBe(false)
  })

  it('never fetches proxies for the credential export', () => {
    expect(formatNeedsProxies('credentials', true)).toBe(false)
    expect(formatHonorsProxyChoice('credentials')).toBe(false)
  })
})

describe('buildCsvContent', () => {
  it('emits a UTF-8 BOM and CRLF rows so Excel reads Chinese names correctly', () => {
    const csv = buildCsvContent(payload())
    expect(csv.startsWith('﻿')).toBe(true)
    expect(csv).toContain('\r\n')
  })

  it('lists credential key names but never their values', () => {
    const csv = buildCsvContent(
      payload({ accounts: [account({ credentials: { refresh_token: 'super-secret', api_key: 'sk-leak' } })] })
    )
    expect(csv).toContain('api_key refresh_token')
    expect(csv).not.toContain('super-secret')
    expect(csv).not.toContain('sk-leak')
  })

  it('resolves the proxy name from proxy_key', () => {
    const csv = buildCsvContent(
      payload({
        proxies: [
          {
            proxy_key: 'socks5|1.2.3.4|1080||',
            name: 'hk-01',
            protocol: 'socks5',
            host: '1.2.3.4',
            port: 1080,
            status: 'active'
          }
        ],
        accounts: [account({ proxy_key: 'socks5|1.2.3.4|1080||' })]
      })
    )
    expect(csv).toContain('"hk-01"')
  })

  it('neutralises spreadsheet formula injection in operator-controlled text', () => {
    const csv = buildCsvContent(payload({ accounts: [account({ name: '=cmd|/c calc' })] }))
    expect(csv).toContain(`"'=cmd|/c calc"`)
  })

  it('escapes embedded quotes per RFC 4180', () => {
    const csv = buildCsvContent(payload({ accounts: [account({ notes: 'say "hi"' })] }))
    expect(csv).toContain('"say ""hi"""')
  })

  it('renders expires_at as an ISO timestamp and blanks the never-expires case', () => {
    const withExpiry = buildCsvContent(payload({ accounts: [account({ expires_at: 1800000000 })] }))
    expect(withExpiry).toContain(new Date(1800000000 * 1000).toISOString())

    const never = buildCsvContent(payload({ accounts: [account({ expires_at: 0 })] }))
    expect(never).not.toContain('1970')
  })
})

describe('buildNativeCredential', () => {
  it('maps kiro credentials onto the on-disk kiro-auth-token.json shape', () => {
    const item = buildNativeCredential(
      account({
        platform: 'kiro',
        credentials: {
          access_token: 'at',
          refresh_token: 'rt',
          expires_at: '2026-08-01T00:00:00Z',
          auth_method: 'social',
          profile_arn: 'arn:aws:codewhisperer:us-east-1:1:profile/ABC'
        }
      })
    )
    expect(item.shape).toBe('kiro-auth-token.json')
    expect(item.credential).toEqual({
      accessToken: 'at',
      refreshToken: 'rt',
      expiresAt: '2026-08-01T00:00:00Z',
      authMethod: 'Social',
      profileArn: 'arn:aws:codewhisperer:us-east-1:1:profile/ABC'
    })
  })

  it('restores the native casing of the kiro IdC auth method', () => {
    const item = buildNativeCredential(
      account({ platform: 'kiro', credentials: { access_token: 'at', auth_method: 'idc' } })
    )
    expect((item.credential as Record<string, unknown>).authMethod).toBe('IdC')
  })

  it('drops empty kiro fields rather than emitting empty strings', () => {
    const item = buildNativeCredential(
      account({ platform: 'kiro', credentials: { access_token: 'at', client_secret: '', region: null } })
    )
    expect(item.credential).toEqual({ accessToken: 'at' })
  })

  it('exports the cursor session token as the bare cookie value it was pasted as', () => {
    const item = buildNativeCredential(
      account({ platform: 'cursor', credentials: { access_token: 'ey.session', refresh_token: 'rt' } })
    )
    expect(item.shape).toBe('cursor-cookie')
    expect(item.credential).toBe('ey.session')
  })

  it('passes other platforms through untouched instead of inventing a format', () => {
    const credentials = { refresh_token: 'r', access_token: 'a' }
    const item = buildNativeCredential(account({ platform: 'anthropic', credentials }))
    expect(item.shape).toBe('raw')
    expect(item.credential).toEqual(credentials)
  })
})

describe('buildExportArtifact', () => {
  it('keeps the existing backup filename and pretty-printed payload', () => {
    const artifact = buildExportArtifact('backup', payload(), NOW)
    expect(artifact.filename).toBe('ringstar-account-20260727090503.json')
    expect(artifact.mime).toBe('application/json')
    expect(JSON.parse(artifact.content).accounts).toHaveLength(1)
  })

  it('names the CSV with the same timestamp scheme', () => {
    expect(buildExportArtifact('csv', payload(), NOW).filename).toBe('ringstar-account-20260727090503.csv')
  })

  it('degenerates a single kiro account into a drop-in kiro-auth-token.json', () => {
    const artifact = buildExportArtifact(
      'credentials',
      payload({
        accounts: [account({ platform: 'kiro', credentials: { access_token: 'at', auth_method: 'social' } })]
      }),
      NOW
    )
    expect(artifact.filename).toBe('kiro-auth-token.json')
    expect(JSON.parse(artifact.content)).toEqual({ accessToken: 'at', authMethod: 'Social' })
  })

  it('degenerates a single cursor account into the bare token text', () => {
    const artifact = buildExportArtifact(
      'credentials',
      payload({ accounts: [account({ platform: 'cursor', credentials: { access_token: 'ey.session' } })] }),
      NOW
    )
    expect(artifact.filename).toBe('ringstar-cursor-token-20260727090503.txt')
    expect(artifact.content).toBe('ey.session')
  })

  it('wraps multiple accounts in a labelled envelope so each credential stays attributable', () => {
    const artifact = buildExportArtifact(
      'credentials',
      payload({
        accounts: [
          account({ name: 'k1', platform: 'kiro', credentials: { access_token: 'at' } }),
          account({ name: 'c1', platform: 'cursor', credentials: { access_token: 'ct' } })
        ]
      }),
      NOW
    )
    expect(artifact.filename).toBe('ringstar-credentials-20260727090503.json')
    const parsed = JSON.parse(artifact.content)
    expect(parsed.type).toBe('ringstar-credentials')
    expect(parsed.items.map((item: { name: string }) => item.name)).toEqual(['k1', 'c1'])
  })

  it('does not degenerate a lone raw-shaped account into a nameless file', () => {
    const artifact = buildExportArtifact('credentials', payload(), NOW)
    expect(artifact.filename).toBe('ringstar-credentials-20260727090503.json')
    expect(JSON.parse(artifact.content).items[0].shape).toBe('raw')
  })
})

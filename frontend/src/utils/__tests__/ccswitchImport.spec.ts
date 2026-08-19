import { describe, expect, it } from 'vitest'
import {
  CURSOR_CC_SWITCH_MODEL,
  CURSOR_CC_SWITCH_MODEL_FALLBACKS,
  GROK_CC_SWITCH_MODEL,
  OPENAI_CC_SWITCH_CODEX_MODEL,
  buildCcSwitchImportDeeplink,
  ccSwitchImportNeedsModel,
  getCcSwitchClientTypes
} from '@/utils/ccswitchImport'
import type { GroupPlatform } from '@/types'

function paramsFromDeeplink(deeplink: string): URLSearchParams {
  const query = deeplink.split('?')[1] || ''
  return new URLSearchParams(query)
}

describe('ccswitchImport utils', () => {
  it('defaults OpenAI CC Switch imports to the current Codex model', () => {
    expect(OPENAI_CC_SWITCH_CODEX_MODEL).toBe('gpt-5.5')
  })

  it('defaults Grok Build imports to the current Grok model', () => {
    expect(GROK_CC_SWITCH_MODEL).toBe('grok-4.5')
  })

  const baseInput = {
    baseUrl: 'https://api.example.com',
    providerName: 'RingStar',
    apiKey: 'sk-test',
    usageScript: 'return true'
  }

  it('adds the Codex model parameter for OpenAI imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        clientType: 'codex'
      })
    )

    expect(params.get('resource')).toBe('provider')
    expect(params.get('app')).toBe('codex')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.get('model')).toBe(OPENAI_CC_SWITCH_CODEX_MODEL)
    expect(atob(params.get('usageScript') || '')).toBe(baseInput.usageScript)
  })

  it.each([
    'https://api.example.com',
    'https://api.example.com/',
    'https://api.example.com/v1',
    'https://api.example.com/v1/'
  ])('imports Grok Build with one /v1 suffix for base URL %s', (baseUrl) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        baseUrl,
        platform: 'grok',
        clientType: 'grokbuild'
      })
    )

    expect(params.get('app')).toBe('grokbuild')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe(GROK_CC_SWITCH_MODEL)
  })

  // {{baseUrl}} 回落到 provider endpoint，而 usageScript 自己会拼 /v1/usage。
  // endpoint 带 /v1 的平台必须显式给出不带 /v1 的用量基址，否则拼成 /v1/v1/usage。
  it.each([
    { platform: 'cursor' as GroupPlatform, clientType: 'opencode' as const, app: 'opencode' },
    { platform: 'grok' as GroupPlatform, clientType: 'grokbuild' as const, app: 'grokbuild' }
  ])('pins the usage base URL below /v1 for $platform imports', ({ platform, clientType, app }) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform,
        clientType
      })
    )

    expect(params.get('app')).toBe(app)
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('usageBaseUrl')).toBe('https://api.example.com')
  })

  it.each([
    { platform: 'anthropic' as GroupPlatform, clientType: 'claude' as const },
    { platform: 'openai' as GroupPlatform, clientType: 'codex' as const },
    { platform: 'antigravity' as GroupPlatform, clientType: 'gemini' as const }
  ])(
    'leaves the usage base URL to the provider endpoint for $platform imports',
    ({ platform, clientType }) => {
      const params = paramsFromDeeplink(
        buildCcSwitchImportDeeplink({
          ...baseInput,
          platform,
          clientType
        })
      )

      expect(params.has('usageBaseUrl')).toBe(false)
    }
  )

  it.each([
    { platform: 'anthropic' as GroupPlatform, clientType: 'claude' as const, app: 'claude' },
    { platform: 'gemini' as GroupPlatform, clientType: 'gemini' as const, app: 'gemini' }
  ])('does not add a model parameter for $platform imports', ({ platform, clientType, app }) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform,
        clientType
      })
    )

    expect(params.get('app')).toBe(app)
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.has('model')).toBe(false)
  })

  it('imports Cursor into Claude Code when Claude is selected', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'cursor',
        clientType: 'claude'
      })
    )

    expect(params.get('app')).toBe('claude')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.has('usageBaseUrl')).toBe(false)
    expect(params.get('model')).toBe(CURSOR_CC_SWITCH_MODEL)
  })

  it.each([
    { clientType: 'codex' as const, app: 'codex' },
    { clientType: 'opencode' as const, app: 'opencode' }
  ])('imports Cursor into $app with the OpenAI-compatible endpoint', ({ clientType, app }) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'cursor',
        clientType
      })
    )

    expect(params.get('app')).toBe(app)
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('usageBaseUrl')).toBe('https://api.example.com')
    expect(params.get('model')).toBe(CURSOR_CC_SWITCH_MODEL)
  })

  it('carries the picked Cursor model into the OpenCode deeplink', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'cursor',
        clientType: 'opencode',
        modelOverride: 'cursor/grok-4.6-max'
      })
    )

    expect(params.get('app')).toBe('opencode')
    expect(params.get('model')).toBe('cursor/grok-4.6-max')
    // 覆盖模型不该动端点或用量基址
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('usageBaseUrl')).toBe('https://api.example.com')
  })

  it.each(['', '   ', null, undefined])(
    'falls back to the platform default when the override is %p',
    (modelOverride) => {
      const params = paramsFromDeeplink(
        buildCcSwitchImportDeeplink({
          ...baseInput,
          platform: 'cursor',
          clientType: 'claude',
          modelOverride
        })
      )

      expect(params.get('model')).toBe(CURSOR_CC_SWITCH_MODEL)
    }
  )

  it('lists only models that stay usable after the API quota runs out', () => {
    expect(CURSOR_CC_SWITCH_MODEL_FALLBACKS).toContain(CURSOR_CC_SWITCH_MODEL)
    expect(CURSOR_CC_SWITCH_MODEL_FALLBACKS).toContain('cursor/grok-4.6')
    expect(CURSOR_CC_SWITCH_MODEL_FALLBACKS).toContain('cursor/grok-4.6-max')
    expect(CURSOR_CC_SWITCH_MODEL_FALLBACKS).toContain('cursor/grok-4.5-max')
    expect(CURSOR_CC_SWITCH_MODEL_FALLBACKS.every((m) => m.startsWith('cursor/'))).toBe(true)
  })

  it('keeps Antigravity imports on the selected client endpoint without a model parameter', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        clientType: 'gemini'
      })
    )

    expect(params.get('app')).toBe('gemini')
    expect(params.get('endpoint')).toBe(`${baseInput.baseUrl}/antigravity`)
    expect(params.has('model')).toBe(false)
  })

  it('exposes a CC Switch target list for every group platform', () => {
    const platforms: GroupPlatform[] = [
      'anthropic',
      'openai',
      'gemini',
      'antigravity',
      'grok',
      'cursor',
      'kiro',
      'composite'
    ]

    for (const platform of platforms) {
      expect(getCcSwitchClientTypes(platform).length).toBeGreaterThan(0)
    }
  })

  it('adds Claude targets to OpenAI groups only when Messages dispatch is enabled', () => {
    expect(getCcSwitchClientTypes('openai')).toEqual([
      'codex',
      'opencode',
      'openclaw',
      'hermes'
    ])
    expect(getCcSwitchClientTypes('openai', true)).toEqual([
      'codex',
      'claude',
      'opencode',
      'openclaw',
      'hermes'
    ])
  })

  it.each(['opencode', 'openclaw', 'hermes'] as const)(
    'imports OpenAI-compatible additive client %s with a concrete model',
    (clientType) => {
      const params = paramsFromDeeplink(
        buildCcSwitchImportDeeplink({
          ...baseInput,
          platform: 'openai',
          clientType
        })
      )

      expect(params.get('app')).toBe(clientType)
      expect(params.get('endpoint')).toBe('https://api.example.com/v1')
      expect(params.get('model')).toBe(OPENAI_CC_SWITCH_CODEX_MODEL)
    }
  )

  it('imports an Anthropic group into OpenClaw through the chat-compatible endpoint', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'anthropic',
        clientType: 'openclaw',
        modelOverride: 'claude-sonnet-5'
      })
    )

    expect(params.get('app')).toBe('openclaw')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe('claude-sonnet-5')
  })

  it('imports Grok into the explicitly selected client instead of forcing Grok Build', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'grok',
        clientType: 'codex'
      })
    )

    expect(params.get('app')).toBe('codex')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe(GROK_CC_SWITCH_MODEL)
  })

  it('uses the Antigravity prefix only for native Claude and Gemini clients', () => {
    const claudeParams = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        clientType: 'claude'
      })
    )
    const openClawParams = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        clientType: 'openclaw'
      })
    )

    expect(claudeParams.get('endpoint')).toBe('https://api.example.com/antigravity')
    expect(openClawParams.get('endpoint')).toBe('https://api.example.com/v1')
  })

  it('requires model selection for namespaced, composite, and additive imports', () => {
    expect(ccSwitchImportNeedsModel('cursor', 'claude')).toBe(true)
    expect(ccSwitchImportNeedsModel('kiro', 'claude')).toBe(true)
    expect(ccSwitchImportNeedsModel('composite', 'gemini')).toBe(true)
    expect(ccSwitchImportNeedsModel('anthropic', 'openclaw')).toBe(true)
    expect(ccSwitchImportNeedsModel('anthropic', 'claude')).toBe(false)
  })
})

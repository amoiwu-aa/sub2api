import type { GroupPlatform } from '@/types'

export const OPENAI_CC_SWITCH_CODEX_MODEL = 'gpt-5.5'
export const ANTHROPIC_CC_SWITCH_MODEL = 'claude-sonnet-4-6'
export const GEMINI_CC_SWITCH_MODEL = 'gemini-2.5-flash'
export const GROK_CC_SWITCH_MODEL = 'grok-4.5'
export const CURSOR_CC_SWITCH_MODEL = 'cursor/default'
export const KIRO_CC_SWITCH_MODEL = 'kiro/auto'

/**
 * Cursor models use the cursor/ namespace. Keep the fallback list focused on
 * models that remain usable when the separate API quota is exhausted.
 */
export const CURSOR_CC_SWITCH_MODEL_FALLBACKS = [
  'cursor/default',
  'cursor/grok-4.6',
  'cursor/grok-4.6-max',
  'cursor/grok-4.5',
  'cursor/grok-4.5-max',
  'cursor/composer-2.5'
]

export type CcSwitchClientType =
  | 'claude'
  | 'codex'
  | 'gemini'
  | 'grokbuild'
  | 'opencode'
  | 'openclaw'
  | 'hermes'

export interface CcSwitchImportConfig {
  app: CcSwitchClientType
  endpoint: string
  model?: string
}

export interface CcSwitchImportDeeplinkInput {
  baseUrl: string
  platform?: GroupPlatform | null
  clientType: CcSwitchClientType
  providerName: string
  apiKey: string
  usageScript: string
  modelOverride?: string | null
}

const ADDITIVE_CC_SWITCH_CLIENTS: CcSwitchClientType[] = ['opencode', 'openclaw', 'hermes']

const CLIENTS_BY_PLATFORM: Record<GroupPlatform, CcSwitchClientType[]> = {
  anthropic: ['claude', 'opencode', 'openclaw', 'hermes'],
  openai: ['codex', 'opencode', 'openclaw', 'hermes'],
  gemini: ['gemini', 'opencode', 'openclaw', 'hermes'],
  antigravity: ['claude', 'gemini', 'opencode', 'openclaw', 'hermes'],
  grok: ['grokbuild', 'claude', 'codex', 'opencode', 'openclaw', 'hermes'],
  cursor: ['claude', 'codex', 'opencode', 'openclaw', 'hermes'],
  kiro: ['claude', 'opencode', 'openclaw', 'hermes'],
  composite: [
    'claude',
    'codex',
    'gemini',
    'grokbuild',
    'opencode',
    'openclaw',
    'hermes'
  ]
}

/**
 * Returns only clients backed by an endpoint RingStar exposes for the group.
 * OpenAI groups gain the Claude clients only when Messages dispatch is enabled.
 */
export function getCcSwitchClientTypes(
  platform: GroupPlatform | undefined | null,
  allowMessagesDispatch = false
): CcSwitchClientType[] {
  const normalizedPlatform = platform || 'anthropic'
  const clients = [...CLIENTS_BY_PLATFORM[normalizedPlatform]]

  if (normalizedPlatform === 'openai' && allowMessagesDispatch) {
    clients.splice(1, 0, 'claude')
  }

  return clients
}

/**
 * Namespaced and mixed-platform groups need an explicit model to avoid the
 * imported tool falling back to a model from the wrong upstream family.
 */
export function ccSwitchImportNeedsModel(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType
): boolean {
  const normalizedPlatform = platform || 'anthropic'
  return (
    normalizedPlatform === 'cursor' ||
    normalizedPlatform === 'kiro' ||
    normalizedPlatform === 'composite' ||
    ADDITIVE_CC_SWITCH_CLIENTS.includes(clientType)
  )
}

function normalizeBaseUrl(baseUrl: string): string {
  return baseUrl.replace(/\/+$/, '').replace(/\/v1$/, '')
}

function withV1Endpoint(baseUrl: string): string {
  return `${normalizeBaseUrl(baseUrl)}/v1`
}

function defaultModelFor(
  platform: GroupPlatform,
  clientType: CcSwitchClientType
): string | undefined {
  switch (platform) {
    case 'openai':
      return clientType === 'claude' ? undefined : OPENAI_CC_SWITCH_CODEX_MODEL
    case 'gemini':
      return clientType === 'gemini' ? undefined : GEMINI_CC_SWITCH_MODEL
    case 'antigravity':
      if (clientType === 'claude' || clientType === 'gemini') {
        return undefined
      }
      return ANTHROPIC_CC_SWITCH_MODEL
    case 'grok':
      return GROK_CC_SWITCH_MODEL
    case 'cursor':
      return CURSOR_CC_SWITCH_MODEL
    case 'kiro':
      return KIRO_CC_SWITCH_MODEL
    case 'composite':
      return undefined
    default:
      return clientType === 'claude' ? undefined : ANTHROPIC_CC_SWITCH_MODEL
  }
}

export function resolveCcSwitchImportConfig(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType,
  baseUrl: string
): CcSwitchImportConfig {
  const normalizedPlatform = platform || 'anthropic'
  const rootUrl = normalizeBaseUrl(baseUrl)
  const model = defaultModelFor(normalizedPlatform, clientType)

  if (clientType === 'claude') {
    return {
      app: clientType,
      endpoint: normalizedPlatform === 'antigravity' ? `${rootUrl}/antigravity` : rootUrl,
      model
    }
  }

  if (clientType === 'gemini') {
    return {
      app: clientType,
      endpoint: normalizedPlatform === 'antigravity' ? `${rootUrl}/antigravity` : rootUrl,
      model
    }
  }

  if (clientType === 'codex') {
    return {
      app: clientType,
      endpoint: normalizedPlatform === 'openai' ? rootUrl : withV1Endpoint(rootUrl),
      model
    }
  }

  return {
    app: clientType,
    endpoint: withV1Endpoint(rootUrl),
    model
  }
}

export function buildCcSwitchImportDeeplink(input: CcSwitchImportDeeplinkInput): string {
  const config = resolveCcSwitchImportConfig(input.platform, input.clientType, input.baseUrl)
  const entries: [string, string][] = [
    ['resource', 'provider'],
    ['app', config.app],
    ['name', input.providerName],
    ['homepage', normalizeBaseUrl(input.baseUrl)],
    ['endpoint', config.endpoint],
    ['apiKey', input.apiKey],
    ['configFormat', 'json'],
    ['usageEnabled', 'true'],
    ['usageScript', btoa(input.usageScript)],
    ['usageAutoInterval', '30']
  ]

  const model = (input.modelOverride || '').trim() || config.model
  if (model) {
    entries.splice(2, 0, ['model', model])
  }

  // The usage script appends /v1/usage itself. Keep its base URL at the host
  // root when the imported provider endpoint already ends in /v1.
  const usageBaseUrl = normalizeBaseUrl(config.endpoint)
  if (usageBaseUrl !== config.endpoint) {
    entries.push(['usageBaseUrl', usageBaseUrl])
  }

  return `ccswitch://v1/import?${new URLSearchParams(entries).toString()}`
}

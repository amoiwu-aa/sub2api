import type { GroupPlatform } from '@/types'

export const OPENAI_CC_SWITCH_CODEX_MODEL = 'gpt-5.5'
export const GROK_CC_SWITCH_MODEL = 'grok-4.5'
/** Cursor 的模型 id 带 cursor/ 命名空间，cursor/default 即上游的 Auto 档 */
export const CURSOR_CC_SWITCH_MODEL = 'cursor/default'
/** Kiro 的模型 id 带 kiro/ 命名空间；导入 Claude Code 时须锁定到具体模型 */
export const KIRO_CC_SWITCH_MODEL = 'kiro/claude-sonnet-4.6'

export type CcSwitchClientType = 'claude' | 'gemini'

export interface CcSwitchImportConfig {
  app: string
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
}

function withV1Endpoint(baseUrl: string): string {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '')
  return normalizedBaseUrl.endsWith('/v1') ? normalizedBaseUrl : `${normalizedBaseUrl}/v1`
}

function withoutV1Suffix(endpoint: string): string {
  return endpoint.replace(/\/v1$/, '')
}

export function resolveCcSwitchImportConfig(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType,
  baseUrl: string
): CcSwitchImportConfig {
  switch (platform || 'anthropic') {
    case 'antigravity':
      return {
        app: clientType === 'gemini' ? 'gemini' : 'claude',
        endpoint: `${baseUrl}/antigravity`
      }
    case 'openai':
      return {
        app: 'codex',
        endpoint: baseUrl,
        model: OPENAI_CC_SWITCH_CODEX_MODEL
      }
    case 'gemini':
      return {
        app: 'gemini',
        endpoint: baseUrl
      }
    case 'grok':
      return {
        app: 'grokbuild',
        endpoint: withV1Endpoint(baseUrl),
        model: GROK_CC_SWITCH_MODEL
      }
    case 'cursor':
      // Cursor 只提供 OpenAI chat/completions：/v1/messages 与 /v1/responses 都由
      // 网关显式 404（routes/gateway.go writeUnsupportedForPlatform），所以既不能导成
      // claude，也不能导成走 Responses API 的 codex。OpenCode 的 OpenAI 兼容 provider
      // 打的正是 chat/completions，是唯一对得上的客户端。
      return {
        app: 'opencode',
        endpoint: withV1Endpoint(baseUrl),
        model: CURSOR_CC_SWITCH_MODEL
      }
    case 'kiro':
      // Kiro 同时提供 Anthropic /v1/messages 与 OpenAI chat/completions，
      // 因此可以复用 claude 客户端，但模型必须锁到 kiro/ 命名空间下的具体 id。
      return {
        app: 'claude',
        endpoint: baseUrl,
        model: KIRO_CC_SWITCH_MODEL
      }
    default:
      return {
        app: 'claude',
        endpoint: baseUrl
      }
  }
}

export function buildCcSwitchImportDeeplink(input: CcSwitchImportDeeplinkInput): string {
  const config = resolveCcSwitchImportConfig(input.platform, input.clientType, input.baseUrl)
  const entries: [string, string][] = [
    ['resource', 'provider'],
    ['app', config.app],
    ['name', input.providerName],
    ['homepage', input.baseUrl],
    ['endpoint', config.endpoint],
    ['apiKey', input.apiKey],
    ['configFormat', 'json'],
    ['usageEnabled', 'true'],
    ['usageScript', btoa(input.usageScript)],
    ['usageAutoInterval', '30']
  ]

  if (config.model) {
    entries.splice(2, 0, ['model', config.model])
  }

  // usageScript 里写的是 `{{baseUrl}}/v1/usage`，而 CC-Switch 的 {{baseUrl}} 在没有
  // usageBaseUrl 时回落到 provider 的 endpoint。cursor / grok 的 endpoint 本身就带
  // /v1，回落会拼出 /v1/v1/usage —— 网关对它是 404，表现为「查询失败」。
  // 显式给用量查询一个不带 /v1 的基址，脚本与端点就不会再各拼一次。
  const usageBaseUrl = withoutV1Suffix(config.endpoint)
  if (usageBaseUrl !== config.endpoint) {
    entries.push(['usageBaseUrl', usageBaseUrl])
  }

  return `ccswitch://v1/import?${new URLSearchParams(entries).toString()}`
}

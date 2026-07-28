// 批量导入代理时的文本解析。
// 除了 URL 形式，代理商更常见的售卖格式是 host:port:user:pass（分隔符也可能是空白、逗号、竖线），
// 这里统一识别，缺协议前缀时回落到调用方给定的默认协议。
import type { ProxyProtocol } from '@/types'

export interface ParsedProxy {
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
}

export interface ProxyParseFailure {
  line: number
  text: string
}

export interface ProxyBatchParseResult {
  total: number
  valid: number
  invalid: number
  duplicate: number
  proxies: ParsedProxy[]
  failures: ProxyParseFailure[]
}

const PROTOCOLS: readonly string[] = ['http', 'https', 'socks5', 'socks5h']

// host / port / user / pass 之间允许的分隔符
const SEPARATOR = '[\\s:,|]'

function normalizeProtocol(raw: string): ProxyProtocol | null {
  const lower = raw.toLowerCase()
  return PROTOCOLS.includes(lower) ? (lower as ProxyProtocol) : null
}

function parsePort(raw: string): number | null {
  if (!/^\d{1,5}$/.test(raw)) return null
  const port = Number(raw)
  return port >= 1 && port <= 65535 ? port : null
}

function isValidHost(host: string): boolean {
  return host.length > 0 && host.length <= 253 && /^[A-Za-z0-9._-]+$/.test(host)
}

// 凭据串按第一个分隔符切成用户名 + 密码，剩余部分整体作为密码，
// 这样密码里带冒号也不会被截断。
function splitCredentials(raw: string): { username: string; password: string } {
  const match = raw.match(new RegExp(`^([^\\s:,|]+)(?:${SEPARATOR}+([\\s\\S]*))?$`))
  if (!match) return { username: '', password: '' }
  return { username: match[1], password: (match[2] ?? '').trim() }
}

function build(
  protocol: ProxyProtocol,
  host: string,
  rawPort: string,
  username: string,
  password: string,
): ParsedProxy | null {
  const port = parsePort(rawPort)
  if (port === null || !isValidHost(host)) return null
  return { protocol, host, port, username, password }
}

/**
 * 解析单行代理，识别失败返回 null。支持：
 * - protocol://user:pass@host:port
 * - protocol://host:port
 * - user:pass@host:port
 * - host:port:user:pass
 * - host:port
 */
export function parseProxyLine(
  line: string,
  defaultProtocol: ProxyProtocol = 'http',
): ParsedProxy | null {
  let rest = line.trim().replace(/[,;]+$/, '')
  if (!rest) return null

  let protocol = defaultProtocol
  const scheme = rest.match(/^([A-Za-z][A-Za-z0-9+.-]*):\/\/([\s\S]*)$/)
  if (scheme) {
    const named = normalizeProtocol(scheme[1])
    if (!named) return null
    protocol = named
    rest = scheme[2].trim()
  }
  rest = rest.replace(/\/+$/, '')
  if (!rest) return null

  // 密码里可能含 @，凭据与地址以最后一个 @ 分界
  const at = rest.lastIndexOf('@')
  if (at !== -1) {
    const { username, password } = splitCredentials(rest.slice(0, at))
    const endpoint = rest.slice(at + 1).match(/^([^\s:,|]+):(\d{1,5})$/)
    if (!username || !endpoint) return null
    return build(protocol, endpoint[1], endpoint[2], username, password)
  }

  const flat = rest.match(new RegExp(`^([^\\s:,|]+)${SEPARATOR}+(\\d{1,5})(?:${SEPARATOR}+([\\s\\S]+))?$`))
  if (!flat) return null
  const { username, password } = flat[3] ? splitCredentials(flat[3].trim()) : { username: '', password: '' }
  return build(protocol, flat[1], flat[2], username, password)
}

// 与后端 buildProxyKey 一致：同一地址在不同协议下算两条独立代理。
function dedupeKey(proxy: ParsedProxy): string {
  return `${proxy.protocol}://${proxy.host}:${proxy.port}:${proxy.username}:${proxy.password}`
}

export function parseProxyList(
  input: string,
  defaultProtocol: ProxyProtocol = 'http',
): ProxyBatchParseResult {
  const lines = input.split('\n')
  const seen = new Set<string>()
  const proxies: ParsedProxy[] = []
  const failures: ProxyParseFailure[] = []
  let total = 0
  let duplicate = 0

  lines.forEach((text, index) => {
    if (!text.trim()) return
    total++

    const parsed = parseProxyLine(text, defaultProtocol)
    if (!parsed) {
      failures.push({ line: index + 1, text: text.trim() })
      return
    }

    const key = dedupeKey(parsed)
    if (seen.has(key)) {
      duplicate++
      return
    }
    seen.add(key)
    proxies.push(parsed)
  })

  return {
    total,
    valid: proxies.length,
    invalid: failures.length,
    duplicate,
    proxies,
    failures,
  }
}

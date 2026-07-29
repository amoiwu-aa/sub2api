import type { AdminDataAccount, AdminDataPayload } from '@/types'

/**
 * 账号导出的多格式落地。
 *
 * 后端 /admin/accounts/export 只有一种结构（sub2api-data，凭据原文），
 * 这里在前端把它转成运营方真正要用的几种形态。刻意不新增后端端点：
 * 数据服务端已经交出来了，再开一条出口就要再配一次 step-up 鉴权与审计，
 * 而这几种格式之间的差别纯粹是排版。
 */

export type AccountExportFormat = 'backup' | 'csv' | 'credentials'

export const ACCOUNT_EXPORT_FORMATS: readonly AccountExportFormat[] = [
  'backup',
  'csv',
  'credentials'
] as const

export interface ExportArtifact {
  filename: string
  mime: string
  content: string
}

/**
 * 各格式对 proxies 的需求不同，所以"导出代理"这个勾选只对备份格式有意义：
 * csv 需要代理名做一列（否则那列恒为空），credentials 根本不看代理。
 */
export function formatNeedsProxies(format: AccountExportFormat, userChoice: boolean): boolean {
  switch (format) {
    case 'csv':
      return true
    case 'credentials':
      return false
    default:
      return userChoice
  }
}

/** 勾选框只在它真正生效的格式下展示，避免"勾了没用"的假开关。 */
export function formatHonorsProxyChoice(format: AccountExportFormat): boolean {
  return format === 'backup'
}

export function exportTimestamp(now: Date): string {
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return [
    now.getFullYear(),
    pad2(now.getMonth() + 1),
    pad2(now.getDate()),
    pad2(now.getHours()),
    pad2(now.getMinutes()),
    pad2(now.getSeconds())
  ].join('')
}

// ── CSV ────────────────────────────────────────────────────────────────

// 表头用后端 JSON 的同名 snake_case，而不是本地化文案：这份文件既会被人打开、
// 也会被脚本读，列名跟备份 JSON 对得上比翻译得好听更有用。
const CSV_HEADERS = [
  'name',
  'platform',
  'type',
  'proxy',
  'concurrency',
  'priority',
  'rate_multiplier',
  'expires_at',
  'auto_pause_on_expired',
  'credential_keys',
  'notes'
] as const

const CSV_FORMULA_PREFIX = /^[=+\-@\t\r]/

function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? '' : String(value)
  // Excel / Sheets 会把 = + - @ 开头的单元格当公式执行。账号名与备注是用户可控的，
  // 而这份文件几乎一定被双击打开——前置单引号把它按回纯文本。
  const safe = CSV_FORMULA_PREFIX.test(text) ? `'${text}` : text
  return `"${safe.replace(/"/g, '""')}"`
}

function isoFromUnixSeconds(seconds: number | null | undefined): string {
  if (!seconds) return ''
  const date = new Date(seconds * 1000)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

/**
 * 只出键名、不出键值：这份清单的用途是盘点与交接（谁配了 refresh_token、
 * 哪些号还没绑代理），一旦带上密文就跟备份文件没区别，却又不能再导入回去。
 */
function credentialKeySummary(account: AdminDataAccount): string {
  return Object.keys(account.credentials || {})
    .sort()
    .join(' ')
}

export function buildCsvContent(payload: AdminDataPayload): string {
  const proxyNameByKey = new Map<string, string>()
  for (const proxy of payload.proxies || []) {
    proxyNameByKey.set(proxy.proxy_key, proxy.name)
  }

  const rows = [CSV_HEADERS.map(csvCell).join(',')]
  for (const account of payload.accounts || []) {
    const proxyName = account.proxy_key ? proxyNameByKey.get(account.proxy_key) || account.proxy_key : ''
    rows.push(
      [
        account.name,
        account.platform,
        account.type,
        proxyName,
        account.concurrency,
        account.priority,
        account.rate_multiplier ?? '',
        isoFromUnixSeconds(account.expires_at),
        account.auto_pause_on_expired ? 'true' : 'false',
        credentialKeySummary(account),
        account.notes ?? ''
      ]
        .map(csvCell)
        .join(',')
    )
  }

  // BOM：没有它 Excel 会按本地代码页解析，中文账号名直接变乱码。
  // CRLF：RFC 4180 的行分隔符，也是 Excel 唯一不会把多行备注读错行的写法。
  return `\uFEFF${rows.join('\r\n')}\r\n`
}

// ── 原生凭证 ────────────────────────────────────────────────────────────

export type NativeCredentialShape = 'kiro-auth-token.json' | 'cursor-cookie' | 'raw'

export interface NativeCredentialItem {
  name: string
  platform: string
  /** 该凭据在本机上的原生形态；也是"能粘回添加账号里的哪一栏"的说明。 */
  shape: NativeCredentialShape
  credential: unknown
}

// 库里存 snake_case，本机 kiro-auth-token.json 用 camelCase。
const KIRO_FIELD_MAP: readonly [string, string][] = [
  ['access_token', 'accessToken'],
  ['refresh_token', 'refreshToken'],
  ['expires_at', 'expiresAt'],
  ['auth_method', 'authMethod'],
  ['profile_arn', 'profileArn'],
  ['client_id', 'clientId'],
  ['client_secret', 'clientSecret'],
  ['region', 'region'],
  ['provider', 'provider']
] as const

/**
 * 库里存的是小写（GetKiroAuthMethod 读时 ToLower），本机文件写的是 Social / IdC。
 * 回写成原生大小写，这样导出的文件能直接喂给 Kiro 客户端；
 * 再导入回本系统也不受影响，因为后端读的时候还会再 ToLower 一次。
 */
function kiroAuthMethod(raw: unknown): string {
  const text = String(raw ?? '').trim()
  switch (text.toLowerCase()) {
    case '':
      return ''
    case 'social':
      return 'Social'
    case 'idc':
      return 'IdC'
    default:
      return text
  }
}

export function buildNativeCredential(account: AdminDataAccount): NativeCredentialItem {
  const credentials = account.credentials || {}

  if (account.platform === 'kiro') {
    const native: Record<string, unknown> = {}
    for (const [source, target] of KIRO_FIELD_MAP) {
      const value = credentials[source]
      if (value === undefined || value === null || value === '') continue
      native[target] = target === 'authMethod' ? kiroAuthMethod(value) : value
    }
    return { name: account.name, platform: account.platform, shape: 'kiro-auth-token.json', credential: native }
  }

  if (account.platform === 'cursor') {
    // Cursor 存的 access_token 就是 WorkosCursorSessionToken 的值，
    // 「添加账号 → 粘贴 Cookie」收的也正是这一串，所以原样导出即可往返。
    return {
      name: account.name,
      platform: account.platform,
      shape: 'cursor-cookie',
      credential: String(credentials.access_token ?? '')
    }
  }

  // 其余平台没有公认的"本机凭证文件"形态，原样给出库里的 credentials——
  // 猜一个格式出来只会导出一份谁都不认的文件。
  return { name: account.name, platform: account.platform, shape: 'raw', credential: credentials }
}

// ── 组装 ────────────────────────────────────────────────────────────────

function backupArtifact(payload: AdminDataPayload, stamp: string): ExportArtifact {
  return {
    filename: `ringstar-account-${stamp}.json`,
    mime: 'application/json',
    content: JSON.stringify(payload, null, 2)
  }
}

function csvArtifact(payload: AdminDataPayload, stamp: string): ExportArtifact {
  return {
    filename: `ringstar-account-${stamp}.csv`,
    mime: 'text/csv;charset=utf-8',
    content: buildCsvContent(payload)
  }
}

function credentialsArtifact(payload: AdminDataPayload, stamp: string): ExportArtifact {
  const items = (payload.accounts || []).map(buildNativeCredential)

  // 单个账号时退化成"就是那个文件本身"，而不是包一层信封：
  // 导出一个 kiro 号得到的就是可以直接放回 ~/.aws/sso/cache/ 的 kiro-auth-token.json，
  // 导出一个 cursor 号得到的就是那一串 cookie。多账号才需要信封来分辨谁是谁。
  const single = items.length === 1 ? items[0] : null
  if (single?.shape === 'kiro-auth-token.json') {
    return {
      filename: 'kiro-auth-token.json',
      mime: 'application/json',
      content: JSON.stringify(single.credential, null, 2)
    }
  }
  if (single?.shape === 'cursor-cookie') {
    return {
      filename: `ringstar-cursor-token-${stamp}.txt`,
      mime: 'text/plain;charset=utf-8',
      content: String(single.credential)
    }
  }

  return {
    filename: `ringstar-credentials-${stamp}.json`,
    mime: 'application/json',
    content: JSON.stringify(
      {
        type: 'ringstar-credentials',
        version: 1,
        exported_at: payload.exported_at,
        items
      },
      null,
      2
    )
  }
}

export function buildExportArtifact(
  format: AccountExportFormat,
  payload: AdminDataPayload,
  now: Date
): ExportArtifact {
  const stamp = exportTimestamp(now)
  switch (format) {
    case 'csv':
      return csvArtifact(payload, stamp)
    case 'credentials':
      return credentialsArtifact(payload, stamp)
    default:
      return backupArtifact(payload, stamp)
  }
}

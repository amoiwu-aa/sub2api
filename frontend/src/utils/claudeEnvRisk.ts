// Claude 中文环境风险评分。
// Claude Code 连接非官方端点时会读取系统时区等区域信号，这里用浏览器能读到的同类指纹
// 做加权打分，帮用户判断当前环境是否容易被识别成中国大陆用户。
// 纯函数，不触碰 window，探针采集在 composables/useClaudeEnvCheck.ts。
//
// 信号集与权重参考 MIT 开源项目 LinXiaoTao/FuckClaude
// (https://github.com/LinXiaoTao/FuckClaude)。

export type EnvSignalId =
  | 'timezone'
  | 'language'
  | 'fonts'
  | 'vendorFonts'
  | 'cnBrowser'
  | 'cnDevice'
  | 'locale'
  | 'utcOffset'
  | 'emoji'

export type EnvRiskLevel = 'low' | 'medium' | 'high'

/** 国旗 emoji 渲染探测结果，见 scoreEmoji 对判定逻辑的说明 */
export interface EmojiProbe {
  /** canvas 不可用或两面国旗都渲染成字母时为 false，该项不参与计分 */
  conclusive: boolean
  /** 参照国旗（🇯🇵）正常渲染，但 🇹🇼 被降级成字母 */
  taiwanFlagSuppressed: boolean
}

export interface EnvProbe {
  timeZone: string
  languages: string[]
  locale: string
  /** Date.prototype.getTimezoneOffset() 的原始返回值，UTC+8 为 -480 */
  utcOffsetMinutes: number
  /** canvas 探测到的已安装中文系统字体 */
  chineseFonts: string[]
  /** canvas 探测到的国产厂商/办公软件字体 */
  vendorFonts: string[]
  /** 命中的国内浏览器或 WebView 名称，未命中为空串 */
  cnBrowser: string
  /** 命中的国产设备品牌，未命中为空串 */
  cnDevice: string
  emoji: EmojiProbe
  /** 由 UA 推断的系统厂商，仅用于展示，不参与计分 */
  platform: string
}

export interface EnvSignal {
  id: EnvSignalId
  weight: number
  /** 命中程度 0–1 */
  ratio: number
  /** 实际得分，weight × ratio 取整 */
  score: number
  hit: boolean
  /** 展示给用户的实测值 */
  detail: string
}

export interface EnvRiskResult {
  score: number
  level: EnvRiskLevel
  signals: EnvSignal[]
}

export const ENV_SIGNAL_WEIGHTS: Record<EnvSignalId, number> = {
  timezone: 26,
  language: 20,
  fonts: 16,
  vendorFonts: 10,
  cnBrowser: 8,
  cnDevice: 6,
  locale: 6,
  utcOffset: 4,
  emoji: 4,
}

// 单项命中阈值：低于这个比例不计入「命中的信号」列表
const HIT_THRESHOLD = 0.25

const MAINLAND_TIMEZONES = [
  'Asia/Shanghai',
  'Asia/Urumqi',
  'Asia/Chongqing',
  'Asia/Harbin',
  'Asia/Kashgar',
  'Asia/Macau',
  'PRC',
]

// 港澳台同属大中华区，是弱信号
const GREATER_CHINA_TIMEZONES = ['Asia/Hong_Kong', 'Asia/Taipei']

// 中文系统字体：Windows / macOS 简中版预装，装了几种说明是中文系统
export const CHINESE_FONT_CANDIDATES = [
  'Microsoft YaHei',
  'Microsoft YaHei UI',
  'SimSun',
  'NSimSun',
  'SimHei',
  'KaiTi',
  'FangSong',
  'DengXian',
  'PingFang SC',
  'Hiragino Sans GB',
  'Source Han Sans CN',
  'Noto Sans CJK SC',
  'STHeiti',
  'LiSu',
  'YouYuan',
]

// 国产厂商与办公软件字体：只可能随国内手机 ROM、WPS、钉钉等一起装上，
// 海外系统不会自带，所以命中任意一款都是强信号，不像系统字体那样要看数量。
export const VENDOR_FONT_CANDIDATES = [
  'HarmonyOS Sans SC',
  'MiSans',
  'MI Lanting',
  'OPPO Sans',
  'vivo Sans',
  'Alibaba PuHuiTi',
  'DingTalk JinBuTi',
  'HONOR Sans CN',
  'FZLanTingHei-R-GBK',
  'FZXiaoBiaoSong-B05',
  'STXihei',
  'Douyin Sans',
  'JingDongLangZhengTi',
  'SmileySans-Oblique',
]

function scoreTimezone(timeZone: string): number {
  if (MAINLAND_TIMEZONES.includes(timeZone)) return 1
  if (GREATER_CHINA_TIMEZONES.includes(timeZone)) return 0.5
  return 0
}

function scoreLanguages(languages: string[]): number {
  if (languages.length === 0) return 0
  const normalized = languages.map((lang) => lang.toLowerCase())
  const [primary] = normalized
  if (primary === 'zh-cn' || primary === 'zh-hans' || primary === 'zh-hans-cn') return 1
  if (primary.startsWith('zh')) return 0.75
  if (normalized.some((lang) => lang.startsWith('zh'))) return 0.5
  return 0
}

function scoreFonts(fonts: string[]): number {
  // 装了三种以上国内字体基本可以确定是中文系统
  return Math.min(1, fonts.length / 3)
}

// 厂商字体不看数量：海外机器上一款都不该有，命中一款就直接满分
function scoreVendorFonts(fonts: string[]): number {
  return fonts.length > 0 ? 1 : 0
}

function scoreCnBrowser(browser: string): number {
  return browser ? 1 : 0
}

function scoreCnDevice(device: string): number {
  return device ? 1 : 0
}

// 国内合规版系统会把 🇹🇼 降级成 "TW" 字母，而 🇯🇵 仍是正常彩色国旗。
// 两面都渲染成字母（Windows 的一贯行为）说明探测不出结论，此时不计分，
// 否则会把所有 Windows 用户误伤成中国大陆环境。
function scoreEmoji(emoji: EmojiProbe): number {
  if (!emoji.conclusive) return 0
  return emoji.taiwanFlagSuppressed ? 1 : 0
}

// detail 一栏展示的是各信号的实测原值，这里用 emoji 本身表达结论，免去翻译
export function describeEmojiProbe(emoji: EmojiProbe): string {
  if (!emoji.conclusive) return ''
  return emoji.taiwanFlagSuppressed ? '🇯🇵 ✓ / 🇹🇼 ✗' : '🇯🇵 ✓ / 🇹🇼 ✓'
}

function scoreLocale(locale: string): number {
  const normalized = locale.toLowerCase()
  if (normalized === 'zh-cn' || normalized.startsWith('zh-hans')) return 1
  if (normalized.startsWith('zh')) return 0.6
  return 0
}

// UTC+8 覆盖新马港台，单看偏移不足以定位大陆，所以不给满分
function scoreUtcOffset(offsetMinutes: number): number {
  return offsetMinutes === -480 ? 0.75 : 0
}

export function envRiskLevel(score: number): EnvRiskLevel {
  if (score <= 30) return 'low'
  if (score <= 60) return 'medium'
  return 'high'
}

export function scoreEnvProbe(probe: EnvProbe): EnvRiskResult {
  const raw: Array<{ id: EnvSignalId; ratio: number; detail: string }> = [
    { id: 'timezone', ratio: scoreTimezone(probe.timeZone), detail: probe.timeZone },
    { id: 'language', ratio: scoreLanguages(probe.languages), detail: probe.languages.join(', ') },
    { id: 'fonts', ratio: scoreFonts(probe.chineseFonts), detail: probe.chineseFonts.join(', ') },
    {
      id: 'vendorFonts',
      ratio: scoreVendorFonts(probe.vendorFonts),
      detail: probe.vendorFonts.join(', '),
    },
    { id: 'cnBrowser', ratio: scoreCnBrowser(probe.cnBrowser), detail: probe.cnBrowser },
    { id: 'cnDevice', ratio: scoreCnDevice(probe.cnDevice), detail: probe.cnDevice },
    { id: 'locale', ratio: scoreLocale(probe.locale), detail: probe.locale },
    {
      id: 'utcOffset',
      ratio: scoreUtcOffset(probe.utcOffsetMinutes),
      detail: formatUtcOffset(probe.utcOffsetMinutes),
    },
    { id: 'emoji', ratio: scoreEmoji(probe.emoji), detail: describeEmojiProbe(probe.emoji) },
  ]

  const signals = raw.map<EnvSignal>((item) => {
    const weight = ENV_SIGNAL_WEIGHTS[item.id]
    return {
      id: item.id,
      weight,
      ratio: item.ratio,
      score: Math.round(weight * item.ratio),
      hit: item.ratio >= HIT_THRESHOLD,
      detail: item.detail,
    }
  })

  const score = Math.min(
    100,
    signals.reduce((sum, signal) => sum + signal.score, 0),
  )

  return { score, level: envRiskLevel(score), signals }
}

export function formatUtcOffset(offsetMinutes: number): string {
  const totalMinutes = -offsetMinutes
  const sign = totalMinutes >= 0 ? '+' : '-'
  const abs = Math.abs(totalMinutes)
  const hours = Math.floor(abs / 60)
  const minutes = abs % 60
  return minutes === 0 ? `UTC${sign}${hours}` : `UTC${sign}${hours}:${String(minutes).padStart(2, '0')}`
}

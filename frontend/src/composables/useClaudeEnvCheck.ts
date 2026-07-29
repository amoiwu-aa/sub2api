// Claude 访问环境检测的探针层：Claude 出口 IP、WebRTC 泄露、DNS 泄露、中文环境指纹。
// 全部在浏览器侧完成，不经过本站后端，避免把用户的真实网络信息落到服务器上。
import { ref, computed } from 'vue'
import {
  CHINESE_FONT_CANDIDATES,
  VENDOR_FONT_CANDIDATES,
  scoreEnvProbe,
  type EmojiProbe,
  type EnvProbe,
  type EnvRiskResult,
} from '@/utils/claudeEnvRisk'
import {
  extractMdnsHosts,
  parseCandidateAddresses,
  type CandidateAddress,
} from '@/utils/webrtcCandidates'

export type ProbeState = 'idle' | 'running' | 'done' | 'failed'

export interface CloudflareTrace {
  host: string
  ip: string
  /** Cloudflare 边缘机房三字码 */
  colo: string
  /** Cloudflare 判定的出口国家 */
  loc: string
  warp: boolean
}

export interface WebRtcResult {
  /** ICE candidate 里公网可路由的地址 */
  addresses: CandidateAddress[]
  /** Chrome 用来遮蔽本机地址的 mDNS 假名 */
  mdnsHosts: string[]
}

export interface DnsResolver {
  ip: string
  isp: string
  country: string
  countryCode: string
  isChina: boolean
}

const CLAUDE_TRACE_HOSTS = ['claude.com', 'claude.ai']
const PROBE_TIMEOUT_MS = 12000
// 每次都用随机子域名，绕开各级 DNS 缓存，才能看到真正发起查询的递归解析器
const DNS_PROBE_ROUNDS = 4
const WEBRTC_TIMEOUT_MS = 6000
// 国内 STUN 放在前面：墙内直连时 Google / Cloudflare 的 STUN 往往不可达，
// 只挂它们会收不到 srflx candidate，把真实 IP 暴露判成「安全」。
const STUN_SERVERS = [
  'stun:stun.chat.bilibili.com:3478',
  'stun:stun.hitv.com:3478',
  'stun:stun.miwifi.com:3478',
  'stun:stun.cloudflare.com:3478',
  'stun:stun.l.google.com:19302',
]

function randomLabel(): string {
  const bytes = new Uint8Array(5)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

async function fetchWithTimeout(url: string, timeoutMs = PROBE_TIMEOUT_MS): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, { signal: controller.signal, cache: 'no-store' })
  } finally {
    clearTimeout(timer)
  }
}

// Cloudflare 的 /cdn-cgi/trace 返回 key=value 的纯文本，且允许跨域读取
function parseTrace(text: string): Record<string, string> {
  const map: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const idx = line.indexOf('=')
    if (idx > 0) map[line.slice(0, idx)] = line.slice(idx + 1)
  }
  return map
}

export async function probeClaudeTrace(host: string): Promise<CloudflareTrace> {
  const res = await fetchWithTimeout(`https://${host}/cdn-cgi/trace`)
  if (!res.ok) throw new Error(`trace ${host}: ${res.status}`)
  const map = parseTrace(await res.text())
  return {
    host,
    ip: map.ip ?? '',
    colo: map.colo ?? '',
    loc: map.loc ?? '',
    warp: map.warp === 'on',
  }
}

export function probeWebRtc(timeoutMs = WEBRTC_TIMEOUT_MS): Promise<WebRtcResult> {
  return new Promise((resolve) => {
    if (typeof RTCPeerConnection === 'undefined') {
      resolve({ addresses: [], mdnsHosts: [] })
      return
    }

    const lines: string[] = []
    const pc = new RTCPeerConnection({ iceServers: STUN_SERVERS.map((urls) => ({ urls })) })
    let settled = false

    const finish = () => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      pc.onicecandidate = null
      try {
        pc.close()
      } catch {
        // 已经关闭时忽略
      }
      resolve({
        addresses: parseCandidateAddresses(lines).filter((addr) => addr.exposed),
        mdnsHosts: extractMdnsHosts(lines),
      })
    }
    // 部分 STUN 不可达时 gathering 不会自然结束，靠超时兜底
    const timer = setTimeout(finish, timeoutMs)

    pc.onicecandidate = (event) => {
      if (!event.candidate) {
        finish()
        return
      }
      lines.push(event.candidate.candidate ?? '')
    }

    try {
      pc.createDataChannel('probe')
    } catch {
      // 某些环境禁用了 data channel，仍可继续收集 candidate
    }
    pc.createOffer()
      .then((offer) => pc.setLocalDescription(offer))
      .catch(finish)
  })
}

interface SurfsharkEntry {
  ISP?: string
  Country?: string
  CountryCode?: string
  IP?: string
}

// surfsharkdns 会回吐所有向它发起过查询的递归解析器，并带好归属地
export async function probeDnsLeak(rounds = DNS_PROBE_ROUNDS): Promise<DnsResolver[]> {
  const responses = await Promise.allSettled(
    Array.from({ length: rounds }, () =>
      fetchWithTimeout(`https://${randomLabel()}.ipv4.surfsharkdns.com/`).then(
        (res) => res.json() as Promise<Record<string, SurfsharkEntry>>,
      ),
    ),
  )

  const byIp = new Map<string, DnsResolver>()
  for (const result of responses) {
    if (result.status !== 'fulfilled' || !result.value) continue
    for (const [ip, entry] of Object.entries(result.value)) {
      const countryCode = (entry.CountryCode ?? '').toUpperCase()
      byIp.set(ip, {
        ip,
        isp: entry.ISP ?? '',
        country: entry.Country ?? '',
        countryCode,
        isChina: countryCode === 'CN',
      })
    }
  }

  if (byIp.size === 0) throw new Error('dns leak probe failed')
  return [...byIp.values()]
}

// 用 canvas 测量文本宽度：换成目标字体后宽度变化，说明该字体已安装
function detectInstalledFonts(candidates: string[]): string[] {
  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) return []

  const sample = '中文字体探测ABCabc123'
  const baselines = ['monospace', 'sans-serif', 'serif']
  const widthOf = (family: string): number => {
    ctx.font = `72px ${family}`
    return ctx.measureText(sample).width
  }

  const baseWidths = baselines.map(widthOf)
  return candidates.filter((font) =>
    baselines.some((base, i) => widthOf(`"${font}", ${base}`) !== baseWidths[i]),
  )
}

const CN_BROWSER_PATTERNS: Array<[RegExp, string]> = [
  [/MicroMessenger/i, 'WeChat'],
  [/QQBrowser|MQQBrowser/i, 'QQ Browser'],
  [/Quark/i, 'Quark'],
  [/UCBrowser|UCWEB/i, 'UC Browser'],
  [/baidubrowser|BIDUBrowser|baiduboxapp/i, 'Baidu'],
  [/QihooBrowser|360SE|360EE/i, '360'],
  [/SogouMobileBrowser|MetaSr/i, 'Sogou'],
  [/aweme|BytedanceWebview/i, 'Douyin'],
  [/MiuiBrowser/i, 'MIUI Browser'],
  [/HuaweiBrowser|HarmonyBrowser/i, 'Huawei Browser'],
  [/VivoBrowser/i, 'vivo Browser'],
  [/HeyTapBrowser|OppoBrowser/i, 'OPPO Browser'],
  [/LBBROWSER/i, 'Liebao'],
  [/Maxthon/i, 'Maxthon'],
  [/2345Explorer/i, '2345'],
  [/AlipayClient/i, 'Alipay'],
  [/DingTalk/i, 'DingTalk'],
]

const CN_DEVICE_PATTERNS: Array<[RegExp, string]> = [
  [/HarmonyOS|OpenHarmony/i, 'HarmonyOS'],
  [/HUAWEI|\bHONOR\b/i, 'Huawei / HONOR'],
  [/Xiaomi|Redmi|POCO/i, 'Xiaomi'],
  [/\bOPPO\b|CPH\d{4}/i, 'OPPO'],
  [/\bvivo\b|\bV\d{4}A\b/i, 'vivo'],
  [/OnePlus/i, 'OnePlus'],
  [/Meizu/i, 'Meizu'],
  [/realme|RMX\d{4}/i, 'realme'],
  [/\bZTE\b|Nubia/i, 'ZTE'],
]

function matchFirst(patterns: Array<[RegExp, string]>, text: string): string {
  if (!text) return ''
  for (const [re, label] of patterns) {
    if (re.test(text)) return label
  }
  return ''
}

function detectCnBrowser(): string {
  const brands = navigator.userAgentData?.brands?.map((b) => b.brand).join(' ') ?? ''
  return matchFirst(CN_BROWSER_PATTERNS, `${navigator.userAgent} ${brands}`)
}

// UA-CH 把机型收进了高熵字段，只有 Chromium 系给，且必须异步取；
// 拿不到就退回 UA 字符串，Safari / Firefox 走的都是这条路。
async function detectCnDevice(): Promise<string> {
  let model = ''
  try {
    const high = await navigator.userAgentData?.getHighEntropyValues?.(['model'])
    model = high?.model ?? ''
  } catch {
    // 权限被拒或不支持，忽略
  }
  return matchFirst(CN_DEVICE_PATTERNS, `${navigator.userAgent} ${model}`)
}

// 国内合规版系统（部分国行 iOS/macOS 与国产 ROM）会把 🇹🇼 降级成 "TW" 字母，
// 而 🇯🇵 仍渲染成彩色国旗。判据是「有没有彩色像素」：用黑色 fillStyle 画字，
// 走字母回退时整字都是黑的，走彩色 emoji 字体才会出现 R/G/B 不相等的像素。
// 参照旗本身就画不出彩色（Windows 一律显示字母）时判为无结论，避免误伤。
function probeEmojiFlags(): EmojiProbe {
  const inconclusive: EmojiProbe = { conclusive: false, taiwanFlagSuppressed: false }

  const canvas = document.createElement('canvas')
  canvas.width = 64
  canvas.height = 64
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  if (!ctx) return inconclusive

  const hasColorPixels = (emoji: string): boolean => {
    ctx.clearRect(0, 0, canvas.width, canvas.height)
    ctx.fillStyle = '#000000'
    ctx.font = '48px sans-serif'
    ctx.textBaseline = 'top'
    ctx.fillText(emoji, 0, 0)
    let data: Uint8ClampedArray
    try {
      data = ctx.getImageData(0, 0, canvas.width, canvas.height).data
    } catch {
      // canvas 被指纹防护扩展污染时读不了像素
      return false
    }
    for (let i = 0; i < data.length; i += 4) {
      if (data[i + 3] < 128) continue
      const [r, g, b] = [data[i], data[i + 1], data[i + 2]]
      if (Math.max(r, g, b) - Math.min(r, g, b) > 40) return true
    }
    return false
  }

  if (!hasColorPixels('🇯🇵')) return inconclusive
  return { conclusive: true, taiwanFlagSuppressed: !hasColorPixels('🇹🇼') }
}

function platformHint(): string {
  const ua = navigator.userAgent
  if (/Windows/i.test(ua)) return 'Microsoft (Windows)'
  if (/Macintosh|Mac OS X/i.test(ua)) return 'Apple (macOS)'
  if (/Android/i.test(ua)) return 'Android'
  if (/iPhone|iPad/i.test(ua)) return 'Apple (iOS)'
  if (/Linux/i.test(ua)) return 'Linux'
  return 'Unknown'
}

export async function collectEnvProbe(): Promise<EnvProbe> {
  const resolved = Intl.DateTimeFormat().resolvedOptions()
  return {
    timeZone: resolved.timeZone ?? '',
    languages: [...(navigator.languages ?? [navigator.language])].filter(Boolean),
    locale: resolved.locale ?? navigator.language ?? '',
    utcOffsetMinutes: new Date().getTimezoneOffset(),
    chineseFonts: detectInstalledFonts(CHINESE_FONT_CANDIDATES),
    vendorFonts: detectInstalledFonts(VENDOR_FONT_CANDIDATES),
    cnBrowser: detectCnBrowser(),
    cnDevice: await detectCnDevice(),
    emoji: probeEmojiFlags(),
    platform: platformHint(),
  }
}

export function useClaudeEnvCheck() {
  const traceState = ref<ProbeState>('idle')
  const webRtcState = ref<ProbeState>('idle')
  const dnsState = ref<ProbeState>('idle')

  const traces = ref<CloudflareTrace[]>([])
  const webRtc = ref<WebRtcResult | null>(null)
  const resolvers = ref<DnsResolver[]>([])
  const probe = ref<EnvProbe | null>(null)
  const risk = ref<EnvRiskResult | null>(null)
  const lastScannedAt = ref<Date | null>(null)

  const running = computed(
    () =>
      traceState.value === 'running' ||
      webRtcState.value === 'running' ||
      dnsState.value === 'running',
  )

  const leakedResolvers = computed(() => resolvers.value.filter((r) => r.isChina))

  // 只有跟 Claude 出口 IP 不同的地址才算泄露：相同说明本来就是直连，没有隧道可漏
  const leakedAddresses = computed(() => {
    const exitIps = new Set(traces.value.map((t) => t.ip).filter(Boolean))
    return (webRtc.value?.addresses ?? []).filter((addr) => !exitIps.has(addr.ip))
  })

  const webRtcLeaked = computed(() => leakedAddresses.value.length > 0)

  async function runTraces() {
    traceState.value = 'running'
    const settled = await Promise.allSettled(CLAUDE_TRACE_HOSTS.map(probeClaudeTrace))
    traces.value = settled
      .filter((r): r is PromiseFulfilledResult<CloudflareTrace> => r.status === 'fulfilled')
      .map((r) => r.value)
    traceState.value = traces.value.length > 0 ? 'done' : 'failed'
  }

  async function runWebRtc() {
    webRtcState.value = 'running'
    try {
      webRtc.value = await probeWebRtc()
      webRtcState.value = 'done'
    } catch {
      webRtcState.value = 'failed'
    }
  }

  async function runDns() {
    dnsState.value = 'running'
    try {
      resolvers.value = await probeDnsLeak()
      dnsState.value = 'done'
    } catch {
      resolvers.value = []
      dnsState.value = 'failed'
    }
  }

  async function runFingerprint() {
    const collected = await collectEnvProbe()
    probe.value = collected
    risk.value = scoreEnvProbe(collected)
  }

  async function scan() {
    if (running.value) return
    await Promise.all([runFingerprint(), runTraces(), runWebRtc(), runDns()])
    lastScannedAt.value = new Date()
  }

  return {
    traceState,
    webRtcState,
    dnsState,
    traces,
    webRtc,
    webRtcLeaked,
    leakedAddresses,
    resolvers,
    leakedResolvers,
    probe,
    risk,
    running,
    lastScannedAt,
    scan,
  }
}

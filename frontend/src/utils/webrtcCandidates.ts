// ICE candidate 解析与地址分类。
// 直接扫描 candidate 原始字符串而不是只读 candidate.address：不同浏览器填充 address 的
// 行为不一致，而原始串里一定带着地址，host 类型上的公网 IP 也能一并捞到。

export type Ipv4Category = 'loopback' | 'private' | 'link-local' | 'carrier-grade-NAT' | 'public'
export type Ipv6Category = 'loopback' | 'link-local' | 'unique-local' | 'ipv4-mapped' | 'global'

export interface CandidateAddress {
  ip: string
  family: 'IPv4' | 'IPv6'
  category: Ipv4Category | Ipv6Category
  /** 公网可路由，也就是真正意义上的暴露 */
  exposed: boolean
}

const IPV4_PATTERN = /(?:(?:25[0-5]|2[0-4]\d|1?\d{1,2})\.){3}(?:25[0-5]|2[0-4]\d|1?\d{1,2})/g
const IPV6_PATTERN =
  /(?:^|[\s:,("])((?:[0-9a-fA-F]{1,4}:){1,7}[0-9a-fA-F]{1,4}|::(?:[0-9a-fA-F]{1,4}:){0,6}[0-9a-fA-F]{1,4}|::1)(?=$|[\s,;)"])/g
const MDNS_PATTERN = /\b[a-z0-9-]{6,}\.local\b/gi

function toUint32(a: number, b: number, c: number, d: number): number {
  return ((a << 24) >>> 0) + (b << 16) + (c << 8) + d
}

export function classifyIpv4(ip: string): Ipv4Category {
  const parts = ip.split('.').map((p) => parseInt(p, 10))
  const value = toUint32(parts[0], parts[1], parts[2], parts[3])
  const inRange = (lo: number, hi: number): boolean => value >= lo && value <= hi

  if (ip === '127.0.0.1') return 'loopback'
  if (
    inRange(toUint32(10, 0, 0, 0), toUint32(10, 255, 255, 255)) ||
    inRange(toUint32(172, 16, 0, 0), toUint32(172, 31, 255, 255)) ||
    inRange(toUint32(192, 168, 0, 0), toUint32(192, 168, 255, 255))
  ) {
    return 'private'
  }
  if (inRange(toUint32(169, 254, 0, 0), toUint32(169, 254, 255, 255))) return 'link-local'
  // 运营商级 NAT 段，用户看到的不是自己的地址
  if (inRange(toUint32(100, 64, 0, 0), toUint32(100, 127, 255, 255))) return 'carrier-grade-NAT'
  return 'public'
}

export function classifyIpv6(ip: string): Ipv6Category {
  const lower = ip.toLowerCase()
  if (lower === '::1') return 'loopback'
  if (lower.startsWith('fe80:')) return 'link-local'
  if (/^f[cd]/.test(lower)) return 'unique-local'
  if (lower.startsWith('::ffff:')) return 'ipv4-mapped'
  return 'global'
}

/** 从一批 candidate 原始字符串里提取去重后的地址列表 */
export function parseCandidateAddresses(candidates: string[]): CandidateAddress[] {
  const seen = new Set<string>()
  const found: CandidateAddress[] = []

  for (const line of candidates) {
    if (!line) continue
    // 0.0.0.0 是 srflx 的占位 raddr，不是真实地址
    for (const ip of line.match(IPV4_PATTERN) ?? []) {
      if (ip === '0.0.0.0' || seen.has(ip)) continue
      seen.add(ip)
      const category = classifyIpv4(ip)
      found.push({ ip, family: 'IPv4', category, exposed: category === 'public' })
    }
    for (const match of line.matchAll(IPV6_PATTERN)) {
      const ip = match[1]
      if (!ip || seen.has(ip)) continue
      seen.add(ip)
      const category = classifyIpv6(ip)
      found.push({ ip, family: 'IPv6', category, exposed: category === 'global' })
    }
  }

  return found
}

/** Chrome 默认用 mDNS 假名遮蔽本机地址，出现假名说明遮蔽生效，不算泄露 */
export function extractMdnsHosts(candidates: string[]): string[] {
  const hosts = new Set<string>()
  for (const line of candidates) {
    for (const host of line?.match(MDNS_PATTERN) ?? []) hosts.add(host)
  }
  return [...hosts]
}

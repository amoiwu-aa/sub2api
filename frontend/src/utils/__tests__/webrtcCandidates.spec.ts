import { describe, expect, it } from 'vitest'
import {
  classifyIpv4,
  classifyIpv6,
  extractMdnsHosts,
  parseCandidateAddresses,
} from '../webrtcCandidates'

// 取自真实浏览器的 candidate 输出
const HOST_MDNS =
  'candidate:1090698559 1 udp 2113937151 57ecbfc8-746b-4a75-8d0a-a7d4cc621290.local 65340 typ host generation 0 ufrag rJi1 network-cost 999'
const SRFLX_PUBLIC =
  'candidate:49308653 1 udp 1677729535 58.60.154.221 15630 typ srflx raddr 0.0.0.0 rport 0 generation 0 ufrag rJi1 network-cost 999'
const HOST_PRIVATE =
  'candidate:2 1 udp 2113937151 192.168.1.20 54321 typ host generation 0 ufrag rJi1 network-cost 999'

describe('classifyIpv4', () => {
  it('区分回环、私有、链路本地、运营商 NAT 与公网', () => {
    expect(classifyIpv4('127.0.0.1')).toBe('loopback')
    expect(classifyIpv4('10.0.0.5')).toBe('private')
    expect(classifyIpv4('172.16.0.1')).toBe('private')
    expect(classifyIpv4('172.32.0.1')).toBe('public')
    expect(classifyIpv4('192.168.1.1')).toBe('private')
    expect(classifyIpv4('169.254.10.1')).toBe('link-local')
    expect(classifyIpv4('100.64.0.1')).toBe('carrier-grade-NAT')
    expect(classifyIpv4('58.60.154.221')).toBe('public')
  })
})

describe('classifyIpv6', () => {
  it('区分回环、链路本地、唯一本地、v4 映射与全局', () => {
    expect(classifyIpv6('::1')).toBe('loopback')
    expect(classifyIpv6('fe80::1')).toBe('link-local')
    expect(classifyIpv6('fd00::1')).toBe('unique-local')
    expect(classifyIpv6('::ffff:192.168.1.1')).toBe('ipv4-mapped')
    expect(classifyIpv6('2400:cb00:964:1024::a29e:b84b')).toBe('global')
  })
})

describe('parseCandidateAddresses', () => {
  it('从 srflx candidate 里提取公网地址并标记为暴露', () => {
    const found = parseCandidateAddresses([SRFLX_PUBLIC])
    expect(found).toEqual([
      { ip: '58.60.154.221', family: 'IPv4', category: 'public', exposed: true },
    ])
  })

  it('忽略 srflx 的 0.0.0.0 占位 raddr', () => {
    expect(parseCandidateAddresses([SRFLX_PUBLIC]).some((a) => a.ip === '0.0.0.0')).toBe(false)
  })

  it('mDNS 假名不产生任何地址', () => {
    expect(parseCandidateAddresses([HOST_MDNS])).toEqual([])
  })

  it('私网地址会被提取但不算暴露', () => {
    const found = parseCandidateAddresses([HOST_PRIVATE])
    expect(found).toHaveLength(1)
    expect(found[0]).toMatchObject({ ip: '192.168.1.20', category: 'private', exposed: false })
  })

  it('跨多条 candidate 去重', () => {
    const found = parseCandidateAddresses([SRFLX_PUBLIC, SRFLX_PUBLIC, HOST_MDNS])
    expect(found).toHaveLength(1)
  })
})

describe('extractMdnsHosts', () => {
  it('收集 .local 假名', () => {
    expect(extractMdnsHosts([HOST_MDNS])).toEqual([
      '57ecbfc8-746b-4a75-8d0a-a7d4cc621290.local',
    ])
  })

  it('没有假名时返回空数组', () => {
    expect(extractMdnsHosts([SRFLX_PUBLIC])).toEqual([])
  })
})

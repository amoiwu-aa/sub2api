import { describe, expect, it } from 'vitest'
import { parseProxyLine, parseProxyList } from '../proxyParse'

describe('parseProxyLine', () => {
  it('识别 protocol://user:pass@host:port', () => {
    expect(parseProxyLine('socks5://alice:s3cret@192.168.1.1:1080')).toEqual({
      protocol: 'socks5',
      host: '192.168.1.1',
      port: 1080,
      username: 'alice',
      password: 's3cret',
    })
  })

  it('识别 protocol://host:port，凭据为空', () => {
    expect(parseProxyLine('https://proxy.example.com:443')).toEqual({
      protocol: 'https',
      host: 'proxy.example.com',
      port: 443,
      username: '',
      password: '',
    })
  })

  it('识别代理商常见的 host:port:user:pass，协议回落默认值', () => {
    expect(parseProxyLine('145.79.90.121:7537:FSjQr9kmu79M:uMEPegxFqxYk')).toEqual({
      protocol: 'http',
      host: '145.79.90.121',
      port: 7537,
      username: 'FSjQr9kmu79M',
      password: 'uMEPegxFqxYk',
    })
  })

  it('无协议前缀时使用调用方给定的默认协议', () => {
    expect(parseProxyLine('1.2.3.4:1080:u:p', 'socks5')?.protocol).toBe('socks5')
  })

  it('协议前缀优先于默认协议', () => {
    expect(parseProxyLine('http://1.2.3.4:8080:u:p', 'socks5')?.protocol).toBe('http')
  })

  it('识别裸 host:port', () => {
    expect(parseProxyLine('10.0.0.1:3128')).toEqual({
      protocol: 'http',
      host: '10.0.0.1',
      port: 3128,
      username: '',
      password: '',
    })
  })

  it('识别无协议的 user:pass@host:port', () => {
    expect(parseProxyLine('alice:s3cret@10.0.0.1:3128')).toEqual({
      protocol: 'http',
      host: '10.0.0.1',
      port: 3128,
      username: 'alice',
      password: 's3cret',
    })
  })

  it('分隔符可以是空白、逗号或竖线', () => {
    const expected = {
      protocol: 'http',
      host: '1.2.3.4',
      port: 8080,
      username: 'u',
      password: 'p',
    }
    expect(parseProxyLine('1.2.3.4 8080 u p')).toEqual(expected)
    expect(parseProxyLine('1.2.3.4,8080,u,p')).toEqual(expected)
    expect(parseProxyLine('1.2.3.4|8080|u|p')).toEqual(expected)
  })

  it('密码含冒号时不被截断', () => {
    expect(parseProxyLine('1.2.3.4:8080:user:pa:ss')?.password).toBe('pa:ss')
  })

  it('密码含 @ 时按最后一个 @ 分界', () => {
    expect(parseProxyLine('user:p@ss@1.2.3.4:8080')).toEqual({
      protocol: 'http',
      host: '1.2.3.4',
      port: 8080,
      username: 'user',
      password: 'p@ss',
    })
  })

  it('容忍首尾空白、行尾逗号与末尾斜杠', () => {
    expect(parseProxyLine('  http://1.2.3.4:8080/ ,')).toEqual({
      protocol: 'http',
      host: '1.2.3.4',
      port: 8080,
      username: '',
      password: '',
    })
  })

  it('缺少端口、端口越界或协议不支持时返回 null', () => {
    expect(parseProxyLine('1.2.3.4')).toBeNull()
    expect(parseProxyLine('1.2.3.4:0')).toBeNull()
    expect(parseProxyLine('1.2.3.4:70000')).toBeNull()
    expect(parseProxyLine('ftp://1.2.3.4:21')).toBeNull()
    expect(parseProxyLine('')).toBeNull()
  })
})

describe('parseProxyList', () => {
  it('统计有效、重复与无效行，并按协议区分重复', () => {
    const result = parseProxyList(
      [
        '1.2.3.4:8080:u:p',
        '',
        'http://u:p@1.2.3.4:8080',
        'socks5://u:p@1.2.3.4:8080',
        '这不是代理',
      ].join('\n'),
    )

    expect(result.total).toBe(4)
    expect(result.valid).toBe(2)
    expect(result.duplicate).toBe(1)
    expect(result.invalid).toBe(1)
  })

  it('无效行带上原始行号与内容，便于定位', () => {
    const result = parseProxyList(['1.2.3.4:8080', '', 'bad line'].join('\n'))
    expect(result.failures).toEqual([{ line: 3, text: 'bad line' }])
  })
})

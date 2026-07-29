import { describe, expect, it } from 'vitest'
import {
  ENV_SIGNAL_WEIGHTS,
  describeEmojiProbe,
  envRiskLevel,
  formatUtcOffset,
  scoreEnvProbe,
  type EnvProbe,
  type EnvSignalId,
} from '../claudeEnvRisk'

// 典型的国内环境：小米手机 + 微信内置浏览器 + 合规版系统
const CN_PROBE: EnvProbe = {
  timeZone: 'Asia/Shanghai',
  languages: ['zh-CN', 'en-US', 'zh-TW', 'zh', 'en'],
  locale: 'zh-CN',
  utcOffsetMinutes: -480,
  chineseFonts: ['Microsoft YaHei', 'Microsoft YaHei UI', 'SimSun', 'NSimSun'],
  vendorFonts: ['MiSans'],
  cnBrowser: 'WeChat',
  cnDevice: 'Xiaomi',
  emoji: { conclusive: true, taiwanFlagSuppressed: true },
  platform: 'Android',
}

// 干净的海外环境
const CLEAN_PROBE: EnvProbe = {
  timeZone: 'America/Los_Angeles',
  languages: ['en-US', 'en'],
  locale: 'en-US',
  utcOffsetMinutes: 480,
  chineseFonts: [],
  vendorFonts: [],
  cnBrowser: '',
  cnDevice: '',
  emoji: { conclusive: true, taiwanFlagSuppressed: false },
  platform: 'Apple (macOS)',
}

const scoreOf = (probe: EnvProbe, id: EnvSignalId): number =>
  scoreEnvProbe(probe).signals.find((s) => s.id === id)!.score

describe('scoreEnvProbe', () => {
  it('权重合计为 100', () => {
    const total = Object.values(ENV_SIGNAL_WEIGHTS).reduce((a, b) => a + b, 0)
    expect(total).toBe(100)
  })

  it('典型国内环境判为高风险，九项全部命中', () => {
    const result = scoreEnvProbe(CN_PROBE)
    expect(result.score).toBe(99)
    expect(result.level).toBe('high')
    expect(result.signals.filter((s) => s.hit)).toHaveLength(9)
  })

  it('干净的海外环境得 0 分，无命中信号', () => {
    const result = scoreEnvProbe(CLEAN_PROBE)
    expect(result.score).toBe(0)
    expect(result.level).toBe('low')
    expect(result.signals.some((s) => s.hit)).toBe(false)
  })

  it('时区：大陆满分，港台减半，海外为 0', () => {
    expect(scoreOf({ ...CLEAN_PROBE, timeZone: 'Asia/Shanghai' }, 'timezone')).toBe(26)
    expect(scoreOf({ ...CLEAN_PROBE, timeZone: 'Asia/Taipei' }, 'timezone')).toBe(13)
    expect(scoreOf({ ...CLEAN_PROBE, timeZone: 'Asia/Singapore' }, 'timezone')).toBe(0)
  })

  it('语言：首选简中满分，首选其他中文次之，仅列表含中文再次之', () => {
    expect(scoreOf({ ...CLEAN_PROBE, languages: ['zh-CN', 'en'] }, 'language')).toBe(20)
    expect(scoreOf({ ...CLEAN_PROBE, languages: ['zh-TW', 'en'] }, 'language')).toBe(15)
    expect(scoreOf({ ...CLEAN_PROBE, languages: ['en-US', 'zh-CN'] }, 'language')).toBe(10)
    expect(scoreOf({ ...CLEAN_PROBE, languages: ['en-US', 'ja'] }, 'language')).toBe(0)
  })

  it('系统字体：装满三种即满分，不足按比例', () => {
    expect(scoreOf({ ...CLEAN_PROBE, chineseFonts: ['SimSun'] }, 'fonts')).toBe(5)
    expect(scoreOf({ ...CLEAN_PROBE, chineseFonts: ['SimSun', 'SimHei'] }, 'fonts')).toBe(11)
    expect(scoreOf({ ...CLEAN_PROBE, chineseFonts: ['SimSun', 'SimHei', 'KaiTi'] }, 'fonts')).toBe(16)
    expect(
      scoreOf({ ...CLEAN_PROBE, chineseFonts: ['SimSun', 'SimHei', 'KaiTi', 'LiSu'] }, 'fonts'),
    ).toBe(16)
  })

  it('厂商字体不看数量：海外系统一款都不该有，命中一款即满分', () => {
    expect(scoreOf({ ...CLEAN_PROBE, vendorFonts: ['MiSans'] }, 'vendorFonts')).toBe(10)
    expect(scoreOf({ ...CLEAN_PROBE, vendorFonts: ['MiSans', 'OPPO Sans'] }, 'vendorFonts')).toBe(10)
    expect(scoreOf({ ...CLEAN_PROBE, vendorFonts: [] }, 'vendorFonts')).toBe(0)
  })

  it('国内浏览器与国产设备是二元信号', () => {
    expect(scoreOf({ ...CLEAN_PROBE, cnBrowser: 'WeChat' }, 'cnBrowser')).toBe(8)
    expect(scoreOf({ ...CLEAN_PROBE, cnBrowser: '' }, 'cnBrowser')).toBe(0)
    expect(scoreOf({ ...CLEAN_PROBE, cnDevice: 'HarmonyOS' }, 'cnDevice')).toBe(6)
    expect(scoreOf({ ...CLEAN_PROBE, cnDevice: '' }, 'cnDevice')).toBe(0)
  })

  it('UTC+8 是弱信号，只给权重的四分之三', () => {
    expect(scoreOf({ ...CLEAN_PROBE, utcOffsetMinutes: -480 }, 'utcOffset')).toBe(3)
    expect(scoreOf({ ...CLEAN_PROBE, utcOffsetMinutes: -540 }, 'utcOffset')).toBe(0)
  })

  it('Emoji：仅在 🇹🇼 被降级时计分，无结论时不计分', () => {
    const emojiScore = (emoji: EnvProbe['emoji']) => scoreOf({ ...CLEAN_PROBE, emoji }, 'emoji')
    expect(emojiScore({ conclusive: true, taiwanFlagSuppressed: true })).toBe(4)
    expect(emojiScore({ conclusive: true, taiwanFlagSuppressed: false })).toBe(0)
    // Windows 两面国旗都渲染成字母，探不出结论，绝不能据此判定为国内环境
    expect(emojiScore({ conclusive: false, taiwanFlagSuppressed: false })).toBe(0)
    expect(emojiScore({ conclusive: false, taiwanFlagSuppressed: true })).toBe(0)
  })

  it('describeEmojiProbe 用 emoji 本身表达结论，无结论时留空', () => {
    expect(describeEmojiProbe({ conclusive: true, taiwanFlagSuppressed: true })).toBe('🇯🇵 ✓ / 🇹🇼 ✗')
    expect(describeEmojiProbe({ conclusive: true, taiwanFlagSuppressed: false })).toBe(
      '🇯🇵 ✓ / 🇹🇼 ✓',
    )
    expect(describeEmojiProbe({ conclusive: false, taiwanFlagSuppressed: false })).toBe('')
  })

  it('单项命中比例低于 0.25 不计入命中', () => {
    const result = scoreEnvProbe({ ...CLEAN_PROBE, chineseFonts: [] })
    expect(result.signals.find((s) => s.id === 'fonts')!.hit).toBe(false)
    // 一种字体 = 1/3，超过阈值
    const oneFont = scoreEnvProbe({ ...CLEAN_PROBE, chineseFonts: ['SimSun'] })
    expect(oneFont.signals.find((s) => s.id === 'fonts')!.hit).toBe(true)
  })

  it('总分不会超过 100', () => {
    expect(scoreEnvProbe(CN_PROBE).score).toBeLessThanOrEqual(100)
  })
})

describe('envRiskLevel', () => {
  it('按 0-30 / 31-60 / 61-100 分档', () => {
    expect(envRiskLevel(0)).toBe('low')
    expect(envRiskLevel(30)).toBe('low')
    expect(envRiskLevel(31)).toBe('medium')
    expect(envRiskLevel(60)).toBe('medium')
    expect(envRiskLevel(61)).toBe('high')
    expect(envRiskLevel(100)).toBe('high')
  })
})

describe('formatUtcOffset', () => {
  it('把 getTimezoneOffset 的取反语义转成可读文本', () => {
    expect(formatUtcOffset(-480)).toBe('UTC+8')
    expect(formatUtcOffset(0)).toBe('UTC+0')
    expect(formatUtcOffset(300)).toBe('UTC-5')
    expect(formatUtcOffset(-330)).toBe('UTC+5:30')
  })
})

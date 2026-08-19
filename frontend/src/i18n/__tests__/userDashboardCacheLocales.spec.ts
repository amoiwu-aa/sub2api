import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('user dashboard cache locale keys', () => {
  it('provides readable cache statistics labels in both locales', () => {
    expect(zh.dashboard.todayCacheHitRate).toBe('今日缓存命中率')
    expect(zh.dashboard.totalCacheHitRate).toBe('累计缓存命中率')
    expect(zh.dashboard.cacheReadShort).toBe('缓存读取')

    expect(en.dashboard.todayCacheHitRate).toBe('Cache Hit Rate (Today)')
    expect(en.dashboard.totalCacheHitRate).toBe('Cache Hit Rate (Total)')
    expect(en.dashboard.cacheReadShort).toBe('Cache Read')
  })
})

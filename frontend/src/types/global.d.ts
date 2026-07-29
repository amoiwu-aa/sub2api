import type { PublicSettings } from '@/types'

declare global {
  interface Window {
    __APP_CONFIG__?: PublicSettings
  }

  interface NavigatorUABrandVersion {
    brand: string
    version: string
  }

  interface NavigatorUAData {
    brands?: NavigatorUABrandVersion[]
    // utils/device.ts 依赖这个字段做移动端判定，必须一并声明，
    // 否则真实 navigator 无法赋给它那边的 NavigatorLike。
    mobile?: boolean
    getHighEntropyValues?(hints: string[]): Promise<{ model?: string }>
  }

  // User-Agent Client Hints 尚未进入 TS 标准库，这里只声明环境检测用到的部分。
  interface Navigator {
    readonly userAgentData?: NavigatorUAData
  }
}

export {}

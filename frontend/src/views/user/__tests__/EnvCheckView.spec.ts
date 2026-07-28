import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref, computed } from 'vue'

import EnvCheckView from '../EnvCheckView.vue'
import { scoreEnvProbe } from '@/utils/claudeEnvRisk'

const { scan } = vi.hoisted(() => ({ scan: vi.fn() }))

// 探针依赖 fetch / RTCPeerConnection，这里只验证视图把结果渲染成了什么
vi.mock('@/composables/useClaudeEnvCheck', () => ({
  useClaudeEnvCheck: () => ({
    traceState: ref('done'),
    webRtcState: ref('done'),
    dnsState: ref('done'),
    traces: ref([
      { host: 'claude.com', ip: '167.148.29.182', colo: 'IAD', loc: 'US', warp: false },
      { host: 'claude.ai', ip: '167.148.29.182', colo: 'IAD', loc: 'US', warp: false },
    ]),
    webRtc: ref({
      addresses: [{ ip: '58.60.154.221', family: 'IPv4', category: 'public', exposed: true }],
      mdnsHosts: ['abc-def.local'],
    }),
    webRtcLeaked: computed(() => true),
    leakedAddresses: computed(() => [
      { ip: '58.60.154.221', family: 'IPv4', category: 'public', exposed: true },
    ]),
    resolvers: ref([
      { ip: '113.96.17.244', isp: 'China Telecom', country: 'China', countryCode: 'CN', isChina: true },
      { ip: '172.253.6.21', isp: 'Google', country: 'United States', countryCode: 'US', isChina: false },
    ]),
    leakedResolvers: computed(() => [
      { ip: '113.96.17.244', isp: 'China Telecom', country: 'China', countryCode: 'CN', isChina: true },
    ]),
    probe: ref(null),
    risk: ref(
      scoreEnvProbe({
        timeZone: 'Asia/Shanghai',
        languages: ['zh-CN', 'en'],
        locale: 'zh-CN',
        utcOffsetMinutes: -480,
        chineseFonts: ['Microsoft YaHei', 'SimSun', 'SimHei'],
        vendorFonts: ['MiSans'],
        cnBrowser: 'WeChat',
        cnDevice: 'Xiaomi',
        emoji: { conclusive: true, taiwanFlagSuppressed: true },
        platform: 'Microsoft (Windows)',
      }),
    ),
    running: computed(() => false),
    lastScannedAt: ref(null),
    scan,
  }),
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

async function mountView() {
  const wrapper = mount(EnvCheckView)
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  scan.mockClear()
})

describe('EnvCheckView', () => {
  it('挂载后自动发起一次扫描', async () => {
    await mountView()
    expect(scan).toHaveBeenCalledTimes(1)
  })

  it('渲染两个 Claude 出口 IP 及边缘节点', async () => {
    const text = (await mountView()).text()
    expect(text).toContain('claude.com')
    expect(text).toContain('claude.ai')
    expect(text).toContain('167.148.29.182')
  })

  it('WebRTC 泄露时展示泄露地址而不是安全提示', async () => {
    const text = (await mountView()).text()
    expect(text).toContain('envCheck.webrtc.leaked')
    expect(text).toContain('58.60.154.221')
    expect(text).not.toContain('envCheck.webrtc.safe')
  })

  it('境内解析器排在列表最前并标红', async () => {
    const items = (await mountView()).findAll('li')
    const resolverRows = items.filter((li) => li.text().includes('.'))
    expect(resolverRows[0].text()).toContain('113.96.17.244')
    expect(resolverRows[0].find('.badge-danger').exists()).toBe(true)
  })

  it('展示风险总分与九项信号', async () => {
    const wrapper = await mountView()
    expect(wrapper.text()).toContain('99')
    expect(wrapper.text()).toContain('envCheck.risk.level.high')
    const ids = [
      'timezone',
      'language',
      'fonts',
      'vendorFonts',
      'cnBrowser',
      'cnDevice',
      'locale',
      'utcOffset',
      'emoji',
    ]
    for (const id of ids) {
      expect(wrapper.text()).toContain(`envCheck.risk.signal.${id}`)
    }
  })
})

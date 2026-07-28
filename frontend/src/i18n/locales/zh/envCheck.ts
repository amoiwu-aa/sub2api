export default {
  envCheck: {
    title: 'Claude 访问环境检测',
    description:
      '检测 Claude 出口 IP、WebRTC 与 DNS 泄露风险，并评估浏览器暴露的中文环境特征。全部检测在你的浏览器本地完成，结果不会上传。',
    rescan: '重新扫描',
    scanning: '检测中...',
    lastScanned: '上次检测 {time}',

    exit: {
      title: 'Claude 连通性及网络出口',
      hint: '实时读取 claude.com 与 claude.ai 的边缘节点信息，确认当前代理是否真的生效。',
      colo: '边缘节点 {colo}',
      warpOn: '检测到 WARP',
      failed: '无法访问 Claude 的边缘节点，可能是当前网络无法直连，或被浏览器扩展拦截。',
    },

    webrtc: {
      title: 'WebRTC 隐私泄露隐患',
      hint: 'WebRTC 会绕过代理直接暴露本机公网地址。这里比对它拿到的地址与 Claude 出口 IP 是否一致。',
      safe: '未发现隧道外的地址暴露',
      safeMdns: '未发现泄露，浏览器已用 mDNS 假名遮蔽本机地址',
      leaked: '存在泄露风险，以下地址在代理之外被暴露',
      failed: 'WebRTC 探测失败，可能被浏览器策略或扩展禁用。',
    },

    dns: {
      title: 'DNS 区域泄露隐患',
      hint: '用随机子域名绕开缓存，查看实际为你解析域名的递归服务器分布在哪些国家。',
      safe: '{count} 个解析器全部在境外',
      leaked: '发现 {count} 个中国境内解析器',
      cnResolver: '中国 DNS 泄漏',
      failed: 'DNS 泄露探测失败，可能是探测服务不可达。',
    },

    risk: {
      title: '中文环境特征评分',
      outOf: '满分 100',
      weight: '权重 {weight}',
      level: {
        low: '低风险',
        medium: '中风险',
        high: '高风险',
      },
      verdict: {
        low: '当前环境几乎看不出中文特征，可以放心使用。',
        medium: '存在部分中文特征，建议按下方建议做一轮清理。',
        high: '当前环境高度接近典型的中国大陆用户，建议尽快调整。',
      },
      signal: {
        timezone: '系统时区',
        language: '浏览器语言',
        fonts: '已安装中文字体',
        vendorFonts: '国产厂商字体',
        cnBrowser: '国内浏览器 / WebView',
        cnDevice: '国产品牌设备',
        locale: 'Intl 区域设置',
        utcOffset: '时区偏移',
        emoji: 'Emoji 国旗渲染',
      },
      signalHint: {
        timezone:
          'Intl.DateTimeFormat 读到的就是 Claude Code 读取的同一个系统时区，与 Asia/Shanghai 等中国时区比对。',
        language: '检查 navigator.languages，首选简体中文得分最高。',
        fonts: '用 canvas 宽度差异探测微软雅黑、苹方、宋体等中文字体是否已安装。',
        vendorFonts:
          '探测 MiSans、鸿蒙黑体、OPPO Sans、钉钉进步体等字体。海外系统不会自带，命中一款即算满分。',
        cnBrowser: '比对 User-Agent 与 UA-CH 品牌，识别微信、QQ、夸克、UC、百度、抖音等内置浏览器。',
        cnDevice: '读取 UA-CH 高熵机型字段与 User-Agent，识别鸿蒙、华为、小米、OPPO、vivo 等设备。',
        locale: '浏览器用于日期和数字格式化的区域设置。',
        utcOffset: 'getTimezoneOffset() 是否为 UTC+8。UTC+8 也覆盖新马港台，属于弱信号。',
        emoji:
          '用 canvas 像素比对 🇯🇵 与 🇹🇼：国内合规版系统会把 🇹🇼 降级成字母。两面都画不出彩色（Windows 的一贯行为）时判为无结论，不计分。',
      },
    },

    advice: {
      title: '降低暴露的建议',
      timezone:
        '把系统时区改成 Asia/Singapore 等同为 UTC+8 且在服务区内的地区，从源头切断时区锚定。',
      tunnel:
        '用 TUN 模式或软路由替代系统代理，让 Claude Code 这类 CLI 工具只看到物理网卡直接出海。',
      dns: '在代理端开启远程 DNS 解析（socks5h），避免解析请求走回国内递归服务器。',
      webrtc: '在浏览器里关闭 WebRTC，或安装扩展强制它只使用代理链路。',
      disclaimer:
        '本页面基于公开的逆向分析结论实现，评分仅供参考，不代表 Anthropic 的真实风控判定。',
      credit:
        '检测信号与权重参考 MIT 开源项目 LinXiaoTao/FuckClaude（https://github.com/LinXiaoTao/FuckClaude）。',
    },
  },
}

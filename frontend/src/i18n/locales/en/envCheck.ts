export default {
  envCheck: {
    title: 'Claude Environment Check',
    description:
      'Checks your Claude exit IP, WebRTC and DNS leaks, and scores the Chinese-locale fingerprints your browser exposes. Everything runs locally in your browser and nothing is uploaded.',
    rescan: 'Rescan',
    scanning: 'Scanning...',
    lastScanned: 'Last scan {time}',

    exit: {
      title: 'Claude Connectivity & Network Exit',
      hint: 'Reads the edge information for claude.com and claude.ai in real time so you can confirm your proxy is actually in effect.',
      colo: 'Edge {colo}',
      warpOn: 'WARP detected',
      failed:
        "Could not reach Claude's edge. Your network may not have direct access, or a browser extension is blocking it.",
    },

    webrtc: {
      title: 'WebRTC Privacy Leak',
      hint: 'WebRTC can bypass your proxy and expose your real public address. This compares what it sees against your Claude exit IP.',
      safe: 'No addresses exposed outside the tunnel',
      safeMdns: 'No leak — the browser is masking local addresses with mDNS hostnames',
      leaked: 'Leak detected — these addresses are exposed outside the proxy',
      failed: 'WebRTC probe failed, possibly disabled by browser policy or an extension.',
    },

    dns: {
      title: 'DNS Region Leak',
      hint: 'Uses random subdomains to bypass caching and reveal which countries your recursive resolvers sit in.',
      safe: 'All {count} resolvers are outside China',
      leaked: '{count} resolvers found inside China',
      cnResolver: 'CN DNS leak',
      failed: 'DNS leak probe failed, the probe service may be unreachable.',
    },

    risk: {
      title: 'Chinese Locale Fingerprint Score',
      outOf: 'out of 100',
      weight: 'weight {weight}',
      level: {
        low: 'Low risk',
        medium: 'Medium risk',
        high: 'High risk',
      },
      verdict: {
        low: 'Your environment shows almost no Chinese-locale traits. You are good to go.',
        medium: 'Some Chinese-locale traits are present. Consider working through the tips below.',
        high: 'Your environment closely matches a typical mainland China user. Adjust it soon.',
      },
      signal: {
        timezone: 'System time zone',
        language: 'Browser languages',
        fonts: 'Installed Chinese fonts',
        vendorFonts: 'Chinese vendor fonts',
        cnBrowser: 'Chinese browser / WebView',
        cnDevice: 'Chinese-brand device',
        locale: 'Intl locale',
        utcOffset: 'UTC offset',
        emoji: 'Flag emoji rendering',
      },
      signalHint: {
        timezone:
          'Intl.DateTimeFormat reads the same system time zone Claude Code reads, compared against Asia/Shanghai and other Chinese zones.',
        language: 'Checks navigator.languages; Simplified Chinese as the primary language scores highest.',
        fonts:
          'Probes canvas width differences to detect Microsoft YaHei, PingFang, SimSun and other Chinese fonts.',
        vendorFonts:
          'Probes for MiSans, HarmonyOS Sans, OPPO Sans, DingTalk JinBuTi and similar faces. Overseas systems never ship them, so a single hit scores full weight.',
        cnBrowser:
          'Matches the User-Agent and UA-CH brands against WeChat, QQ, Quark, UC, Baidu, Douyin and other in-app browsers.',
        cnDevice:
          'Reads the UA-CH high-entropy model field and the User-Agent to spot HarmonyOS, Huawei, Xiaomi, OPPO and vivo devices.',
        locale: 'The locale your browser uses for date and number formatting.',
        utcOffset:
          'Whether getTimezoneOffset() reports UTC+8. That offset also covers Singapore, Malaysia, Hong Kong and Taiwan, so it is a weak signal.',
        emoji:
          'Compares canvas pixels for 🇯🇵 and 🇹🇼: China-compliant system builds downgrade 🇹🇼 to plain letters. When neither renders in colour (standard Windows behaviour) the probe is inconclusive and scores nothing.',
      },
    },

    advice: {
      title: 'How to reduce exposure',
      timezone:
        'Switch your system time zone to Asia/Singapore or another UTC+8 region inside the service area, cutting the time-zone anchor at the source.',
      tunnel:
        'Use TUN mode or a router-level proxy instead of a system proxy, so CLI tools like Claude Code only see the physical interface going out directly.',
      dns: 'Enable remote DNS resolution on the proxy side (socks5h) so lookups never fall back to Chinese resolvers.',
      webrtc: 'Disable WebRTC in your browser, or use an extension that forces it through the proxy.',
      disclaimer:
        "Based on publicly available reverse-engineering findings. The score is indicative only and does not represent Anthropic's actual risk decision.",
      credit:
        'Detection signals and weights adapted from the MIT-licensed project LinXiaoTao/FuckClaude (https://github.com/LinXiaoTao/FuckClaude).',
    },
  },
}

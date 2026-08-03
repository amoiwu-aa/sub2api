# cookie2codex

把自己浏览器里已登录的 ChatGPT Cookie 换成 RingStar 可直接导入的
`accessToken` JSON，补上「Cookie-Editor 导出 → Codex 账号导入」这段缺口。

当前先覆盖最有用、且项目里尚未已有实现的一条链：

```text
Cookie-Editor JSON / Header String / Netscape
  → GET https://chatgpt.com/api/auth/session
  → codex-session.json
  → RingStar「Codex OAuth auth.json / AT 导入」
```

Claude 暂不重复实现：RingStar 后台已经能直接接收 claude.ai 的 `sessionKey`
并自动完成 OAuth。Cursor 也已经能导入 `WorkosCursorSessionToken`。

> `session cookie → accessToken` 是可行的，因为官方 session 端点会校验浏览器登录态
> 并签发/返回令牌；反方向不能靠本地格式转换完成。ChatGPT 的浏览器 Cookie 是服务端
> session，不能从 RingStar 里的 OAuth access token 安全、可靠地“算出来”。

## 后台页面（推荐）

管理员可以直接在 RingStar 页面完成转换和账号创建：

```text
账号管理 → 新建账号 → OpenAI → 下一步
  → 授权方式选择「ChatGPT Cookie 导入」
  → 粘贴 Cookie-Editor JSON / Header String / Netscape 内容
  → 转换 Cookie 并创建账号
```

页面会沿用当前表单中的代理、分组、并发和模型设置。原始浏览器 Cookie
仅用于本次官方会话接口请求，不会保存到账号凭证，也不会写入审计日志。

## 命令行用法

1. 在浏览器登录 `https://chatgpt.com`。
2. 打开 Cookie-Editor，导出当前站点的全部 Cookie，推荐选 **JSON**。
3. 在 `backend` 目录运行：

```powershell
go run ./cmd/cookie2codex -i C:\path\to\chatgpt-cookies.json
```

默认产出当前目录下的 `codex-session.json`。然后在 RingStar 后台进入：

```text
新建 OpenAI 账号
  → Codex OAuth auth.json / AT 导入
  → 粘贴 codex-session.json 全文
  → 导入并创建账号
```

也支持 Cookie-Editor 的 **Header String** 和 **Netscape** 导出：

```powershell
go run ./cmd/cookie2codex -i .\cookies.txt -o .\account-01.json
```

从剪贴板/管道读取并把纯 JSON 写到标准输出：

```powershell
Get-Clipboard | go run ./cmd/cookie2codex -i - -o -
```

运行状态写到 stderr，因此 `-o -` 的 stdout 可以继续安全地交给脚本处理。

## 参数

| 参数 | 说明 |
| --- | --- |
| `-i` | 必填。Cookie-Editor 导出文件；`-` 表示 stdin |
| `-o` | 产物路径；`-` 表示 stdout，默认 `codex-session.json` |
| `-proxy` | 请求 session 端点所用代理，支持 HTTP、HTTPS、SOCKS5 |
| `-user-agent` | 请求 UA；遇到 Cloudflare 校验时应与导出 Cookie 的浏览器完全一致 |
| `-endpoint` | 覆盖 session 端点；为防 Cookie 外泄，只接受 ChatGPT/OpenAI 官方 HTTPS 域名 |

例如浏览器登录时走了代理：

```powershell
go run ./cmd/cookie2codex `
  -i .\chatgpt-cookies.json `
  -proxy socks5://127.0.0.1:1080 `
  -user-agent "Mozilla/5.0 ..."
```

`cf_clearance` 等风控 Cookie 往往与出口 IP、User-Agent 绑定。若收到 HTTP 403，
应优先确认工具使用了和浏览器登录时相同的代理与完整 UA。

## 产物

工具只保留 RingStar 导入需要的字段，不会把浏览器 session cookie 写进产物：

```json
{
  "user": {
    "id": "user-...",
    "email": "name@example.com"
  },
  "account": {
    "id": "account-...",
    "planType": "plus"
  },
  "accessToken": "eyJ...",
  "expiresAt": "2026-08-03T08:00:00Z",
  "expires": "2026-08-10T00:00:00Z",
  "authProvider": "auth0"
}
```

- `expiresAt` 从 access token 的 JWT `exp` 得到，RingStar 会按它设置安全过期策略。
- `expires` 是浏览器 session 的过期时间，两者不是同一个生命周期。
- ChatGPT 浏览器 session 通常不返回 Codex OAuth `refresh_token`，所以令牌到期后需重新运行本工具。
- 工具不会把 Cookie 发给自定义第三方地址，也不会跟随 session 请求的 HTTP 重定向。

Cookie 与产物都等同于账号凭证。只处理你有权使用的账号，不要提交到 Git，
用完后应删除 Cookie 导出文件。

## 测试

```powershell
go test ./internal/pkg/chatgptcookie ./cmd/cookie2codex
```

单测覆盖三种 Cookie-Editor 格式、分片 session cookie、域名/路径/过期过滤、
官方域名限制、重定向阻断、响应脱敏和 JWT 过期校验。

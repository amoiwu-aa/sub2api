# kirologin

复刻 Kiro IDE 的网页登录（portal）流程，跑完直接产出 `kiro-auth-token.json`，
可以粘进 RingStar 后台「新建 Kiro 账号」，不用再去本机 `~/.aws/sso/cache` 里翻文件。

协议来自 Kiro **1.0.212** 的 `kiro.kiro-agent` 扩展，对应两个模块：

- `packages/kiro-shared/dist/portal-auth-provider-*.js` —— PKCE、本地回调服务、code 交换
- `packages/kiro-shared/dist/sso-oidc-client-*.js` —— `AuthServiceClient` 的端点与请求体

## 登录原理

Kiro 1.0.x 已经把 Google / GitHub / BuilderID / 企业 IdC 全部收口到统一的
**auth portal**（`app.kiro.dev`）。IDE 侧不再自己拼各家 IdP 的 OAuth，
只做「起本地回调 → 把用户送进 portal → 收 code → 换 token」。

```
IDE                          浏览器 / app.kiro.dev              prod.us-east-1.auth.desktop.kiro.dev
 │
 │ 1. 监听 127.0.0.1:<port>
 │    port 依次取 [3128, 4649, 6588, 8008, 9091,
 │                 49153, 50153, 51153, 52153, 53153]
 │    回调路径：/oauth/callback 或 /signin/callback
 │
 │ 2. state = uuidv4()
 │    code_verifier  = base64url(random 32B)
 │    code_challenge = base64url(sha256(code_verifier))
 │
 │ 3. 打开浏览器 ─────────────────────────►
 │    GET {portal}/signin
 │        ?state=<state>
 │        &code_challenge=<challenge>
 │        &code_challenge_method=S256
 │        &redirect_uri=http://localhost:<port>     ← 只有 origin，没有路径
 │        &redirect_from=KiroIDE
 │
 │                                用户在 portal 上选 Google / GitHub 并登录
 │
 │ 4. ◄───────────────────────────────── 302 回本地
 │    GET http://localhost:<port>/oauth/callback
 │        ?login_option=google&code=<code>&state=<state>
 │    校验 state，取 code
 │
 │ 5. POST {auth}/oauth/token ──────────────────────────────────────────────►
 │    Content-Type: application/json
 │    User-Agent: KiroIDE-<version>-<machineId>
 │    {
 │      "code":          "<code>",
 │      "code_verifier": "<verifier>",
 │      "redirect_uri":  "http://localhost:<port>/oauth/callback?login_option=google"
 │    }                                    ↑ 这里必须带路径和 login_option
 │
 │ ◄──────────────────────────────────────────────────────────────────────────
 │    { accessToken, refreshToken, profileArn, expiresIn }
 │
 │ 6. 落盘 ~/.aws/sso/cache/kiro-auth-token.json
```

### 几个容易踩的点

1. **`redirect_uri` 在两处形状不同。** 进 portal 时是裸 origin
   （`http://localhost:3128`，且末尾斜杠被 `replace(/\/$/, "")` 去掉）；
   换 token 时必须是 `http://localhost:3128/oauth/callback?login_option=google`。
   两边不一致会被判 `redirect_uri` 不匹配。

2. **端口不能随便选。** portal 侧只放行上面那 10 个端口的 localhost 回调，
   IDE 是从头往后试第一个能监听的。

3. **`code_challenge` 是对 verifier 字符串做 sha256**，
   不是对随机字节做——`createHash("sha256").update(codeVerifier)`，
   verifier 此时已经是 base64url 字符串了。

4. **`profileArn` 由 `/oauth/token` 直接返回**，社交登录不需要额外查 profile。
   （IdC 那条路才需要再调 `ListAvailableProfiles`。）

5. **刷新链和登录链不是一个端点。**
   刷新是 `POST {auth}/refreshToken`，请求体 `{"refreshToken": "..."}`，
   也就是 `internal/pkg/kiro/auth.go` 里的 `SocialRefreshURL`，本工具产出的凭证可直接被它续期。

6. **刷新响应里的 `profileArn` 不可信。** `SocialAuthProvider.refreshToken` 拿到响应后
   立刻 `token2.profileArn = profileArn`，用本地存的值盖掉响应值。
   也就是说 `profileArn` 只有登录时那一次是权威的，后续续期都得自己留着。

7. **两套 User-Agent 不要混用。**
   - auth 服务（`/oauth/token`、`/refreshToken`）：`KiroIDE-{version}-{machineId}`，**连字符**
   - Q / CodeWhisperer API：`KiroIDE {version} {machineId}`，**空格**（`getCustomUserAgent()`）

   `machineId` 两边都是 `node-machine-id` 的 `machineIdSync()`，
   即平台机器标识做 sha256 后取十六进制（Windows 上是 `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`）。

8. **换 token 只在传输层失败时重试。** 扩展的 axios-retry 配了 `retries: 3`，
   但 `retryCondition` 排除了 5xx，而 POST 不是幂等方法，
   所以实际只有「压根没收到响应」才会重发——授权码是一次性的，重发已送达的请求只会作废它。
   本工具照此实现。

### 官方的续期节奏

| 常量 | 值 | 含义 |
| --- | --- | --- |
| `REFRESH_LOOP_INTERVAL_SECONDS` | 60 | 每分钟检查一次是否临近过期 |
| `REFRESH_BEFORE_EXPIRY_SECONDS` | 600 | 距过期 **10 分钟** 内就提前续期 |
| `AUTH_TOKEN_INVALIDATION_OFFSET_SECONDS` | 180 | 距过期 3 分钟内即视为已失效 |

（RingStar 侧的 `kiro.RefreshBuffer` 目前是 5 分钟，比官方的 10 分钟激进一些。）

### auth 服务的完整端点表

| 用途 | 方法 | 路径 | 请求体 |
| --- | --- | --- | --- |
| 换 token | POST | `/oauth/token` | `{code, code_verifier, redirect_uri, invitation_code?}` |
| 刷新 token | POST | `/refreshToken` | `{refreshToken}` |
| 登出（吊销 refresh token） | POST | `/logout` | `{refreshToken}` |
| 注销账号 | DELETE | `/account` | 头部 `Authorization: Bearer <accessToken>` |

端点基址默认 `https://prod.us-east-1.auth.desktop.kiro.dev`，
可被 VS Code 配置项 `kiroAuthConfig.endpoint` 覆盖；
portal 基址默认 `https://app.kiro.dev`，可被环境变量 `KIRO_AUTH_PORTAL_URL`
或配置项 `kiroAuthConfig.portalUrl` 覆盖。

### 产物格式

官方写出的文件就是这 6 个字段（本机实测一致）：

```json
{
  "accessToken":  "...",
  "refreshToken": "...",
  "profileArn":   "arn:aws:codewhisperer:us-east-1:699475941385:profile/XXXXXXXX",
  "expiresAt":    "2026-07-26T15:25:25.645Z",
  "authMethod":   "social",
  "provider":     "Google"
}
```

`internal/pkg/kiro.ParseAuthToken` 直接吃这个格式（camelCase / snake_case 都认）。

## 用法

```bash
go run ./cmd/kirologin                      # 登录并写出 ./kiro-auth-token.json
go run ./cmd/kirologin -verify -ringstar    # 推荐：先验凭证可用，再带上 UA 字段落盘
go run ./cmd/kirologin -o -                 # 只打印到标准输出
go run ./cmd/kirologin -proxy socks5://127.0.0.1:1080
go run ./cmd/kirologin -no-browser          # headless：只打印登录地址
go run ./cmd/kirologin -install             # 顺便覆盖本机 Kiro 的登录态
```

`-verify` 会在落盘前把凭证走一遍 RingStar 真正会走的路径：
`kiro.ParseAuthToken`（后台导得进去）→ `Validate`（字段完整）→
真打一次上游 `ListAvailableModels`（上游认这张凭证）。
选 `ListAvailableModels` 是因为它只读且不计费；发一次对话虽然更彻底，
但会平白扣掉账号的 credit。养号时建议默认带上——凭证是死是活当场就知道，
而不是等导进 RingStar、真有流量打过去才发现。

| 参数 | 说明 |
| --- | --- |
| `-o` | 输出路径，`-` 表示打印到标准输出，默认 `kiro-auth-token.json` |
| `-install` | 同时写入 `~/.aws/sso/cache/kiro-auth-token.json`（**会覆盖本机 Kiro 登录态**） |
| `-ringstar` | 多写 `machineId` / `kiroVersion`，让网关复刻本机的 `KiroIDE {version} {machineId}` UA |
| `-proxy` | 换 token 走代理，`http://` 或 `socks5://` |
| `-no-browser` | 不自动拉浏览器，只打印地址（远程机器上用） |
| `-portal` / `-endpoint` | 覆盖 portal / auth 服务地址 |
| `-kiro-version` | 覆盖 UA 里的版本号，默认从本机安装的 Kiro 探测 |
| `-invitation-code` | 邀请码，仅当 portal 提示需要时填 |
| `-verify` | 落盘前真打一次上游 `ListAvailableModels` 确认凭证可用（只读，不计费） |
| `-refresh` | 续期已有凭证而不重新登录，取值为凭证文件路径（**就地写回**） |

### 续期

access token 只有 1 小时，每次都拉浏览器点一遍太费事。凭证里有 refresh token，
直接续期即可：

```bash
go run ./cmd/kirologin -refresh ./kiro-auth-token.json          # 续期
go run ./cmd/kirologin -refresh ./kiro-auth-token.json -verify  # 续期并验证可用
```

两条链都支持，按凭证里的 `authMethod` 自动分派：

| | 端点 | 请求体 |
| --- | --- | --- |
| `social` | `POST {auth}/refreshToken` | `{refreshToken}` |
| `idc` | `POST https://oidc.{region}.amazonaws.com/token` | `{clientId, clientSecret, grantType: "refresh_token", refreshToken}` |

IdC 那条**必须有 `clientId`/`clientSecret`**，所以 `-refresh` 只能用在本工具产出的
凭证上——官方 `kiro-auth-token.json` 不含这两项（它们在本机 SSO 缓存的另一个文件里）。

结果**就地写回原文件**，`-o` 在续期模式下被忽略：刷新可能轮换 refresh token，
写去别处会留下一份随时作废的旧凭证。实测目前两条链都不轮换，但协议允许，不能指望。

刷新失败通常意味着 refresh token 本身失效了（实测有效期 7~30 天），那就得重新登录。

远程/无桌面环境下用 `-no-browser`：把打印出来的地址拷到本地浏览器打开，
但要注意回调是打到**运行本工具那台机器**的 `localhost:<port>`，
所以需要把该端口通过 SSH 隧道转发到本地，否则浏览器回调不回来。

## IdC（BuilderID / 企业 IdC）

登录页选 BuilderID 或企业 IdC 时，portal 不发 token，只回 `issuer_url` +
`idc_region`，本工具会自动接着跑 IdC 链，不需要额外操作。

**它不是设备码流程。** bundle 里能搜到 `StartDeviceAuthorization`，但那是 AWS SDK
自带的命令，Kiro 没用。实际走的是 `authorization_code` + PKCE，和 portal 那条链同构：

```
1. POST https://oidc.{region}.amazonaws.com/client/register
     { clientName: "Kiro IDE", clientType: "public",
       scopes: [completions, analysis, conversations, transformations, taskassist],
       grantTypes: [authorization_code, refresh_token],
       redirectUris: ["http://127.0.0.1/oauth/callback"], issuerUrl }
   -> { clientId, clientSecret }

2. 浏览器打开 https://oidc.{region}.amazonaws.com/authorize
     ?response_type=code&client_id=..&redirect_uri=http://127.0.0.1:{port}/oauth/callback
     &scopes=completions,analysis,...      <- 逗号分隔，不是空格
     &state=..&code_challenge=..&code_challenge_method=S256

3. 回调拿 code

4. POST https://oidc.{region}.amazonaws.com/token
     { clientId, clientSecret, grantType: "authorization_code",
       code, codeVerifier, redirectUri }
   -> { accessToken, refreshToken, expiresIn }     <- 注意没有 profileArn

5. POST https://q.{region}.amazonaws.com/ListAvailableProfiles
   -> profiles[0].arn 作为 profileArn
```

与社交链的三处差异：

1. **`scopes` 逗号分隔**，不是 OAuth 常见的空格分隔。
2. **不返回 `profileArn`**，必须再查一次 `ListAvailableProfiles`；
   缺了它每个 Q API 请求都发不出去，所以这一步失败就是硬失败。
3. **`clientId` / `clientSecret` 必须随凭证落库**。服务器上没有本机的
   `~/.aws/sso/cache`，少了这两项 access token 一过期就永久失效。
   本工具会把它们写进产出的 JSON。

⚠️ **IdC 链尚未经过真实账号验证** —— 手头只有 social 账号。协议逐字段对照了
扩展源码，单测覆盖了 URL 构造与路由，但没有端到端跑通过。首次使用时请留意报错。

`external_idp`（企业自建 IdP）仍未支持：它要求与客户自己的 IdP 再做一次 OAuth，
且续期路径也不同。遇到会明确报错。

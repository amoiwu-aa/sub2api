# Ringstar Prompt Cache 中转、观测与面板接入指南

> 适用范围：AutoClaw 通过 Ringstar 转发 Anthropic、OpenAI 兼容接口或 Cursor 账号请求。
>
> 审计基线：2026-08-12，分支 `feat/ringstar-cache-bridge`，HEAD `c0d825485`。
>
> 本文解决两个不同问题：
>
> 1. 如何让 Ringstar 不破坏上游 Prompt Cache，并尽量提高真实缓存复用；
> 2. 如何把上游返回的缓存用量可靠地写入数据库、API 和管理面板。

## 1. 先说当前服务器“没有命中显示”的最可能原因

当前工作区并不是一个可以直接 `git pull` 到服务器的完整版本，至少有以下问题。

### 1.1 缓存桥接代码还没有进入 Git 提交

当前分支上大量缓存文件仍是 `M` 或 `??` 状态，其中包括：

- `backend/migrations/221_cache_usage_observation.sql`
- `backend/migrations/222_cache_usage_session_index_notx.sql`
- `backend/internal/service/cache_usage_observation.go`
- `backend/internal/handler/gateway_session_usage.go`
- `backend/internal/service/prompt_cache_capability.go`
- `frontend/src/utils/cacheCoverage.ts`
- 多个协议转换、数据库聚合和前端图表文件

`git clone`、`git pull`、CI 从远端构建或官方 Docker 镜像都不会包含未提交文件。服务器即使显示了新版图表，也可能仍在运行旧后端。

部署前必须先确认：

```bash
git status --short
git rev-parse --abbrev-ref HEAD
git rev-parse HEAD
```

只有代码已经进入目标提交，服务器才能按提交复现。

### 1.2 默认生产 Compose 拉的是官方镜像

以下文件默认使用 `weishaw/sub2api:latest`：

- `deploy/docker-compose.yml`
- `deploy/docker-compose.local.yml`
- `deploy/docker-compose.standalone.yml`

修改本地源码后继续执行：

```bash
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d
```

只会再次运行官方镜像，不会包含你的改动。

仓库中只有 `deploy/docker-compose.dev.yml` 明确从本地 `Dockerfile` 构建。

### 1.3 已安装实例重启时不会自动执行新迁移

当前 `backend/cmd/server/main.go` 只有在 `setup.NeedsSetup()` 为 true 时才进入安装流程。`repository.ApplyMigrations` 由安装流程调用，已有 `/app/data/.installed` 的实例正常重启时不会重新运行迁移。

因此老服务器升级时，必须在切换新二进制之前手工应用并验证新增 SQL。否则新统计和写入 SQL 会引用不存在的：

- `usage_logs.cache_usage_source`
- `usage_logs.forced_cache_read_tokens`

这不只会让 Dashboard API 500。当前扣费先于 best-effort usage log 写入；如果先启动新二进制，可能出现“请求和扣费成功，但 usage log 因缺列永久丢失”。数据库检查应作为部署硬门，失败就停止切换。

### 1.4 当前工作区已接通管理面板后端取数链路

前端已经声明并读取：

- `provider_cache_read_tokens`
- `forced_cache_read_tokens`
- `reported_requests`
- `estimated_requests`
- `unavailable_requests`

当前工作区的 `DashboardStats`、管理端 `GetStats`、用户/API Key Dashboard、趋势接口、
模型接口和小时/日预聚合均已下发这些字段，并额外提供 `cache_hit_requests`。

生产环境仍必须先应用 221/222 迁移并回填历史预聚合；否则新后端会引用不存在的聚合列，
或旧聚合桶继续返回全 0 观测计数。

### 1.5 OpenAI 原生网关已补齐观测来源落库

`backend/internal/service/openai_gateway_usage.go` 当前会写：

- `CacheCreationTokens`
- `CacheReadTokens`

构造 `UsageLog` 时现已写入：

- `CacheUsageSource`
- `ForcedCacheReadTokens`

OpenAI usage 明确出现任一缓存字段（包括 `cached_tokens: 0`）时标记为 `reported`；
有 usage 但没有缓存字段时标记为 `unavailable`。新成功请求不再以 NULL 表示缺失观测，
NULL 只保留给迁移前的历史行。

### 1.6 Cursor 路径目前无法得到真实缓存用量

Ringstar 的 Cursor 上游目前不返回可靠 token usage。现有实现使用本地字符数估算输入和输出，并把请求标记为 `estimated`。

与 usage 观测直接相关的生产门是：

- `backend/internal/pkg/cursor/private_usage_decoder.go`

它当前即使被打开并满足样本数，也仍固定返回 `no_validated_mapping`，因为尚无已验证字段映射。

`backend/internal/pkg/cursor/checkpoint_gate.go` 是会话状态续传门，不负责解析 usage，也不会让面板获得缓存 token。

所以纯 Cursor 流量的正确面板状态应是“缓存不可观测”，而不是“真实命中 0%”。不能为了让面板有数字，把估算值伪装成上游命中。

## 2. 先分清三个独立链路

缓存能否工作、缓存能否被观测、面板能否显示，是三个不同问题。

### 2.1 缓存行为链路

客户端请求中的稳定前缀、`cache_control`、`prompt_cache_key` 或显式 breakpoint，经过 Ringstar 协议转换后到达上游。上游决定是否创建和读取缓存。

### 2.2 缓存观测链路

上游响应中的 `cached_tokens`、`cache_read_input_tokens`、`cache_creation_input_tokens` 等字段，经过解析、转换、合并后写入 `usage_logs`。

### 2.3 面板展示链路

`usage_logs` 经过实时 SQL 或预聚合表进入 API，再由前端计算覆盖率并显示。

必须逐层验证。面板显示 0 不能直接证明上游没有缓存；上游有缓存也不代表 Ringstar 一定拿到了 usage 字段。

## 3. 指标口径

### 3.1 原始字段

- `input_tokens`：未走缓存读取或创建计费桶的普通输入。
- `cache_creation_tokens`：本次写入 Prompt Cache 的输入。
- `cache_read_tokens`：当前兼容计费口径，可能包含上游真实读取和本地强制账务调整。
- `forced_cache_read_tokens`：由 `ForceCacheBilling` 从普通输入搬入缓存读取桶的部分，只是账务调整。
- `provider_cache_read_tokens`：在当前统计范围内，由上游明确上报的真实缓存读取，应计算为：

```text
SUM(max(cache_read_tokens - max(forced_cache_read_tokens, 0), 0))
FILTER (WHERE cache_usage_source = 'reported')
```

历史 NULL、`estimated` 和 `unavailable` 行即使带有旧数值，也不能进入可信 provider 口径。

### 3.2 缓存读取覆盖率

`ForceCacheBilling` 会把原普通输入从 `input_tokens` 搬到 `cache_read_tokens`，所以计算总 Prompt 输入时必须把 forced 加回普通输入。

只有当前范围内全部请求都是 `reported` 时，现有 v2 字段才能精确计算全量 token 口径的“缓存读取覆盖率”：

```text
provider_cache_read_tokens
--------------------------------------------------------------------------------------- × 100%
input_tokens + forced_cache_read_tokens + cache_creation_tokens + provider_cache_read_tokens
```

分母不包含输出 token。`forced_cache_read_tokens` 在图表中应归回“普通输入”，另以“账务调整”文字提示，但不能成为缓存读取切片。

例如原始普通输入 900、真实缓存读取 100，ForceCacheBilling 后落库可能是：

```text
input=0, cache_read=1000, forced=900, provider_read=100
```

正确覆盖率是 `100 / (0 + 900 + 0 + 100) = 10%`，不是 100%。

### 3.3 观测来源

迁移后的每个新请求必须属于以下一种状态：

- `reported`：上游响应明确包含缓存字段，字段值可以是 0。
- `estimated`：缓存或 token 用量由本地估算，当前主要是 Cursor。
- `unavailable`：成功请求没有提供可靠缓存字段，包括“有 usage 但无缓存字段”和“响应完全没有 usage 对象”。
- NULL：历史记录，或尚未接入观测契约的旧代码路径。NULL 是未知状态，不是 reported。

关键规则：

```text
明确返回 cached_tokens: 0  => reported
完全没有 cached_tokens 字段 => unavailable
```

绝不能用 `cache_read_tokens > 0` 判断是否“可观测”。

请求观测比例的分母必须是该范围的总请求数：

```text
unknown_requests = max(
  requests - reported_requests - estimated_requests - unavailable_requests,
  0
)

reported_ratio = reported_requests / requests
```

不能用 `reported / (reported + estimated + unavailable)`，否则“99 条 NULL 历史行 + 1 条 reported”会被错误显示为 100% 可观测。

现有 SessionUsage v2 的安全显示规则：

- `reported_requests == requests`：可显示全量覆盖率；
- `0 < reported_requests < requests`：显示“部分可观测 reported/requests”，隐藏全量覆盖率；
- `reported_requests == 0 && requests > 0`：显示“缓存不可观测”；
- `unknown_requests > 0`：明确显示历史/未接入请求，不把它们当真实命中。

如果希望在混合来源中仍显示 reported 子集覆盖率，需要扩展下一版 API，额外返回：

- `reported_input_tokens`，其中应把 reported 行的 forced 加回普通输入；
- `reported_cache_creation_tokens`；
- `reported_provider_cache_read_tokens`。

没有这三个 reported-only token 桶时，不要对混合流量计算一个看似精确的百分比。

### 3.4 “覆盖率”不是“请求命中率”

当前字段在“范围内全部请求均为 reported”时足以计算 token 覆盖率，但不能精确计算“有多少请求发生过命中”。

如确实需要请求命中率，应另外聚合：

```text
cache_hit_requests = COUNT(*)
  WHERE cache_usage_source='reported'
    AND max(cache_read_tokens - max(forced_cache_read_tokens, 0), 0) > 0
request_hit_rate = cache_hit_requests / reported_requests
```

不要把 token 覆盖率标成请求命中率。

SessionUsage 接口把这个口径直接下发为：

- `cache_hit_requests`：仅统计 `reported` 且真实 provider cache read 大于 0 的请求；
- `cache_hit_rate_percent`：`cache_hit_requests / reported_requests × 100`；
- `cache_observation_status`：`no_data`、`fully_reported`、`partially_reported` 或 `unobservable`；
- `unknown_requests`：范围总请求减去 reported、estimated、unavailable 后的历史/未接入请求。

`cache_hit_rate_percent` 是 nullable。没有任何 `reported` 请求时必须返回 JSON
`null`，不能返回 `0`：`0` 表示“上游明确可观测且没有一次命中”，`null` 才表示
“无法判断”。因此纯 Cursor 会话当前应返回 `unobservable + null`。

## 4. 推荐的数据流

```text
AutoClaw
  ├─ 稳定 Prompt 前缀
  ├─ cache_control / prompt_cache_key / breakpoint
  └─ X-Session-Id
          │
          ▼
Ringstar 请求转换与能力门
          │
          ▼
Anthropic / OpenAI / Cursor 上游
          │
          ▼
usage 字段解析
  ├─ token 数值
  └─ 字段是否存在
          │
          ▼
ClaudeUsage / OpenAIUsage
  ├─ CacheUsageSource
  └─ ForcedCacheReadInputTokens
          │
          ▼
usage_logs
          │
          ├─ SessionUsage v2
          ├─ 实时统计
          └─ Dashboard 小时/日预聚合
                    │
                    ▼
缓存读取覆盖率 + 可观测状态 + 账务调整
```

## 5. 后端改造步骤

## 5.1 定义统一观测契约

文件：`backend/internal/service/cache_usage_observation.go`

```go
package service

type CacheUsageSource string

const (
	CacheUsageSourceReported    CacheUsageSource = "reported"
	CacheUsageSourceEstimated   CacheUsageSource = "estimated"
	CacheUsageSourceUnavailable CacheUsageSource = "unavailable"
)

func (source CacheUsageSource) IsValid() bool {
	switch source {
	case CacheUsageSourceReported,
		CacheUsageSourceEstimated,
		CacheUsageSourceUnavailable:
		return true
	default:
		return false
	}
}
```

在 `ClaudeUsage` 中增加：

```go
CacheUsageSource             CacheUsageSource `json:"-"`
ForcedCacheReadInputTokens   int              `json:"-"`
```

在 `OpenAIUsage` 中也应增加同样的内部来源字段，避免 OpenAI 原生网关断链：

```go
CacheUsageSource             CacheUsageSource `json:"-"`
ForcedCacheReadInputTokens   int              `json:"-"`
```

这些字段用于内部落库，不应被伪装成上游响应字段返回给客户端。

## 5.2 分离真实缓存与 ForceCacheBilling

`ForceCacheBilling` 不能被统计成真实上游命中。

```go
func ApplyForceCacheBilling(usage *ClaudeUsage) {
	if usage == nil || usage.InputTokens <= 0 {
		return
	}
	adjusted := usage.InputTokens
	usage.CacheReadInputTokens += adjusted
	usage.ForcedCacheReadInputTokens += adjusted
	usage.InputTokens = 0
}
```

保持以下不变式：

- `cache_read_tokens` 继续包含 forced 部分，兼容原计费逻辑；
- `forced_cache_read_tokens` 单独记录；
- 单行原始 provider 候选值在查询时通过相减获得，但可信面板聚合还必须限制 `CacheUsageSourceReported`；
- forced 数量要加回“普通输入”分母，不能从 Prompt 总输入中消失；
- `ForceCacheBilling` 不改变 `CacheUsageSource`。

## 5.3 Anthropic usage 解析

需要覆盖流式与非流式、透传与转换路径：

- `backend/internal/service/gateway_upstream_response.go`
- `backend/internal/service/gateway_anthropic_passthrough.go`
- `backend/internal/service/gateway_forward_as_responses.go`

识别至少以下字段：

- `cache_creation_input_tokens`
- `cache_read_input_tokens`
- `cached_tokens`
- `cache_write_input_tokens`
- `cache_write_tokens`
- `cache_creation`
- `input_tokens_details.cached_tokens`
- `prompt_tokens_details.cached_tokens`
- `input_tokens_details.cache_creation_tokens`
- `prompt_tokens_details.cache_creation_tokens`
- `input_tokens_details.cache_creation_5m_tokens`
- `input_tokens_details.cache_creation_1h_tokens`

检测器和解码器必须支持同一组别名。否则会出现“来源是 reported，但 token 被解析成 0”。

流式合并应遵守：

- `message_start` 中明确的 0 可以覆盖初始值；
- `message_delta` 只合并有效增量，不能用结束帧的 0 抹掉开始帧的非零值；
- 一旦来源成为 `reported`，后续普通事件不能降级为 `unavailable`。

兼容上游也可能完全不返回 usage 节点。除了解析器赋值，还必须在通用 `GatewayService.recordUsageCore` 最终构造日志前兜底：

```go
if !result.Usage.CacheUsageSource.IsValid() {
	result.Usage.CacheUsageSource = CacheUsageSourceUnavailable
}
```

该兜底只填非法/空来源，不会覆盖已经明确的 `reported` 或 `estimated`。这样迁移后的 Anthropic 兼容新请求也不会继续写 NULL。

## 5.4 OpenAI usage 解析必须保留字段存在性

当前 `openAIUsageFromGJSON` 能读取数值，但 `OpenAIUsage` 没有保存“缓存字段是否存在”。

建议增加检测函数：

```go
func openAIUsageHasCacheFields(value gjson.Result) bool {
	paths := []string{
		"input_tokens_details.cached_tokens",
		"prompt_tokens_details.cached_tokens",
		"input_tokens_details.cache_write_tokens",
		"prompt_tokens_details.cache_write_tokens",
		"input_tokens_details.cache_creation_tokens",
		"prompt_tokens_details.cache_creation_tokens",
		"cache_read_input_tokens",
		"cache_read_tokens",
		"cached_tokens",
		"prompt_cache_hit_tokens",
		"prompt_cache_miss_tokens",
		"cache_creation_input_tokens",
		"cache_write_input_tokens",
		"cache_creation_tokens",
		"cache_write_tokens",
	}
	for _, path := range paths {
		if value.Get(path).Exists() {
			return true
		}
	}
	return false
}
```

解析 usage 时：

```go
source := CacheUsageSourceUnavailable
if openAIUsageHasCacheFields(value) {
	source = CacheUsageSourceReported
}
```

然后写入 `OpenAIUsage.CacheUsageSource`。

注意：

- 必须使用 `Exists()`，不能使用数值是否大于 0；
- `input_tokens` 是总输入，写库前仍需减去 cache read 和 cache creation，形成互斥计费桶；
- 所有 `addOpenAIUsage`、SSE 合并、Responses/Chat/Messages 转换都必须传播来源；
- 合并优先级为 `reported > estimated > unavailable > 空值`。

在 `backend/internal/service/openai_gateway_usage.go` 构造 `UsageLog` 时补上：

```go
CacheUsageSource:       optionalCacheUsageSource(result.Usage.CacheUsageSource),
ForcedCacheReadTokens: result.Usage.ForcedCacheReadInputTokens,
```

OpenAI 某些成功响应可能完全没有 usage 对象，解析器不会调用 `openAIUsageFromGJSON`。因此最终记录层还要兜底：

```go
source := result.Usage.CacheUsageSource
if !source.IsValid() {
	source = CacheUsageSourceUnavailable
}
usageLog.CacheUsageSource = optionalCacheUsageSource(source)
```

如果某路径已经做了本地 token/cache 估算，应在进入记录层前明确标记 `estimated`，不能被这个兜底覆盖。迁移后的新成功请求不应再写空 source；NULL 只保留给历史行。

## 5.5 协议转换必须保留 explicit zero

文件：`backend/internal/pkg/apicompat/types.go`

对 Responses、Chat Completions 和 Anthropic usage，不能只依赖 `omitempty`。需要保留私有的 `*Present` 标记，并提供自定义 `MarshalJSON` / `UnmarshalJSON`。

核心逻辑：

```text
hasField = fieldPresent || value != 0
```

这样：

```json
{"cached_tokens": 0}
```

经过 Ringstar 转换后仍然存在，而不是被 `omitempty` 删除。

新增别名字段时必须同时修改：

1. Unmarshal 的 presence 检测；
2. 数值解码；
3. Marshal；
4. 协议桥接状态机；
5. 流式和非流式测试。

## 5.6 数据库迁移

当前工作区已将缓存迁移拆成两个连续编号，避免与既有 `200_channel_monitor_v2_rollup_permissions.sql`
发生编号冲突：

```text
221_cache_usage_observation.sql
222_cache_usage_session_index_notx.sql
```

`221_cache_usage_observation.sql` 添加原始表字段、非负约束以及预聚合列，保持事务执行：

```sql
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_usage_source VARCHAR(16);

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens INTEGER;

ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS provider_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_hit_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unavailable_requests BIGINT NOT NULL DEFAULT 0;

ALTER TABLE usage_dashboard_daily
    ADD COLUMN IF NOT EXISTS provider_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_hit_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS forced_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reported_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unavailable_requests BIGINT NOT NULL DEFAULT 0;
```

来源枚举和 `forced_cache_read_tokens` 非负 CHECK 约束均使用 `NOT VALID`：它们会立即
拒绝后续非法写入，但不会因为历史脏数据阻塞升级；读取与聚合层仍将负 forced 值钳制为 0。

不要在这个普通事务迁移里对热点 `usage_logs` 执行 `CREATE INDEX`，否则建索引期间可能阻塞 INSERT。

`222_cache_usage_session_index_notx.sql` 只放并发索引：

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_session_usage_v2
    ON usage_logs (user_id, api_key_id, session_id, created_at)
    WHERE session_id IS NOT NULL;
```

仓库 migration runner 要求含 `CONCURRENTLY` 的文件使用 `_notx.sql` 后缀，且文件中只能放并发索引语句。

并发建索引中断后可能留下同名 invalid index，而 `IF NOT EXISTS` 会错误跳过它。
当前 `backend/internal/repository/migrations_runner.go` 已为 222 迁移接入
`dropInvalidIndexIfPresent` 预处理；手工重试前也必须先检查 `pg_index.indisvalid`，
只删除 invalid 索引。

`usage_logs.cache_usage_source` 保持可空，用于区分历史行。不要给历史记录默认填 `reported`。
当前仓储写入边界会把 nil 或非法来源归一化为 `unavailable`，因此迁移后新写入不会再制造
NULL；读取端仍把数据库中既有 NULL 视为 unknown。

同步修改：

- `backend/ent/schema/usage_log.go`
- `backend/internal/service/usage_log.go`
- `backend/internal/repository/usage_log_repo_insert.go`
- `backend/internal/repository/usage_log_repo_query.go`

生成 Ent 代码：

```bash
cd backend
go generate ./ent
```

## 5.7 usage_logs 聚合

`backend/internal/repository/usage_log_repo_stats.go` 中所有用户、API Key、账号、模型、全局和过滤聚合都应包含：

```sql
COALESCE(
  SUM(GREATEST(
    cache_read_tokens - GREATEST(COALESCE(forced_cache_read_tokens, 0), 0),
    0
  ))
    FILTER (WHERE cache_usage_source = 'reported'),
  0
) AS total_provider_cache_read_tokens,
COALESCE(
  SUM(GREATEST(COALESCE(forced_cache_read_tokens, 0), 0)),
  0
) AS total_forced_cache_read_tokens,
COUNT(*) FILTER (WHERE cache_usage_source = 'reported') AS reported_requests,
COUNT(*) FILTER (WHERE cache_usage_source = 'estimated') AS estimated_requests,
COUNT(*) FILTER (WHERE cache_usage_source = 'unavailable') AS unavailable_requests
```

所有重复 SQL 必须同步修改。`GREATEST(..., 0)` 不能省，避免脏数据产生负数。provider read 必须限制 `reported`，避免把历史 NULL 行或估算流量伪装成可信命中。

应用层另外计算：

```text
unknown_requests =
  max(total_requests - reported_requests - estimated_requests - unavailable_requests, 0)
```

## 5.8 SessionUsage v2

路由：

```text
GET /v1/sub2api/usage
```

查询参数：

- `session_id`：必填；
- `min_requests`：期望至少已落库的请求数；
- `wait_ms`：等待结算时间，最大 3000ms。

响应应包含：

```json
{
  "object": "sub2api.session_usage",
  "schema_version": 2,
  "session_id": "autoclaw:example-session",
  "settled": true,
  "settled_at": "2026-08-12T00:00:00Z",
  "usage": {
    "requests": 2,
    "input_tokens": 400,
    "output_tokens": 100,
    "cache_creation_tokens": 1000,
    "cache_read_tokens": 900,
    "provider_cache_read_tokens": 900,
    "forced_cache_read_tokens": 0,
    "cache_hit_requests": 1,
    "cache_hit_rate_percent": 50,
    "cache_observation_status": "fully_reported",
    "reported_requests": 2,
    "estimated_requests": 0,
    "unavailable_requests": 0,
    "unknown_requests": 0,
    "total_tokens": 2400,
    "cost": 0,
    "actual_cost": 0,
    "average_duration_ms": 1200
  }
}
```

Cursor 当前没有经过验证的私有 usage 字段映射。带 `X-Session-Id` 的纯 Cursor
会话可以用同一个接口探测，但结果应明确表示“不可观测”，而不是伪造 0%：

```json
{
  "object": "sub2api.session_usage",
  "schema_version": 2,
  "session_id": "autoclaw:cursor-example",
  "settled": true,
  "usage": {
    "requests": 2,
    "provider_cache_read_tokens": 0,
    "cache_hit_requests": 0,
    "cache_hit_rate_percent": null,
    "cache_observation_status": "unobservable",
    "reported_requests": 0,
    "estimated_requests": 2,
    "unavailable_requests": 0,
    "unknown_requests": 0
  }
}
```

这个结果只说明 Ringstar 无法观测 Cursor 的 provider cache，不说明 Cursor 内部
没有缓存。未来只有私有 decoder 通过样本门并得到可靠字段映射后，Cursor 才能从
`estimated` 升级为 `reported`。

安全要求：

- 查询必须同时限制当前 API Key 的 `user_id`、`api_key_id` 和 `session_id`；
- 响应添加 `Cache-Control: no-store`；
- `session_id` 只能接受有效 UTF-8、无控制字符、长度不超过 255；
- `session_id` 只做用量关联，不得代替 `prompt_cache_key`。

## 5.9 接通管理首页 Dashboard

当前工作区已经按以下链路接通；部署时仍需执行迁移和预聚合回填。

### A. 扩展 Go 类型

在 `backend/internal/pkg/usagestats/usage_log_types.go` 的 `DashboardStats` 中增加累计和今日两套字段：

```go
TotalProviderCacheReadTokens int64 `json:"total_provider_cache_read_tokens"`
TotalCacheHitRequests         int64 `json:"total_cache_hit_requests"`
TotalForcedCacheReadTokens   int64 `json:"total_forced_cache_read_tokens"`
TotalReportedRequests        int64 `json:"total_reported_requests"`
TotalEstimatedRequests       int64 `json:"total_estimated_requests"`
TotalUnavailableRequests     int64 `json:"total_unavailable_requests"`

TodayProviderCacheReadTokens int64 `json:"today_provider_cache_read_tokens"`
TodayCacheHitRequests         int64 `json:"today_cache_hit_requests"`
TodayForcedCacheReadTokens   int64 `json:"today_forced_cache_read_tokens"`
TodayReportedRequests        int64 `json:"today_reported_requests"`
TodayEstimatedRequests       int64 `json:"today_estimated_requests"`
TodayUnavailableRequests     int64 `json:"today_unavailable_requests"`
```

`TrendDataPoint` 和 `ModelStat` 也增加不带 total/today 前缀的六个字段（含
`cache_hit_requests`）。

### B. 扩展实时查询

在 `backend/internal/repository/usage_log_repo_dashboard.go` 的：

- `fillDashboardUsageStatsFromUsageLogs`
- 用户 Dashboard 查询
- API Key Dashboard 查询

增加 provider、forced 和三态请求计数。

### C. 扩展预聚合写入

在 `backend/internal/repository/dashboard_aggregation_repo.go`：

- 小时聚合直接从 `usage_logs` 计算六个字段；
- 日聚合从小时表求和；
- `INSERT`、`SELECT`、`ON CONFLICT DO UPDATE` 三处字段顺序必须一致。

小时聚合的核心表达式：

```sql
COALESCE(
  SUM(GREATEST(
    cache_read_tokens - GREATEST(COALESCE(forced_cache_read_tokens, 0), 0),
    0
  ))
    FILTER (WHERE cache_usage_source = 'reported'),
  0
)
COUNT(*) FILTER (
  WHERE cache_usage_source = 'reported'
    AND GREATEST(
      cache_read_tokens - GREATEST(COALESCE(forced_cache_read_tokens, 0), 0),
      0
    ) > 0
)
COALESCE(SUM(GREATEST(COALESCE(forced_cache_read_tokens, 0), 0)), 0)
COUNT(*) FILTER (WHERE cache_usage_source = 'reported')
COUNT(*) FILTER (WHERE cache_usage_source = 'estimated')
COUNT(*) FILTER (WHERE cache_usage_source = 'unavailable')
```

所有 filtered `SUM` 都必须包 `COALESCE(..., 0)`；没有 reported 行时 PostgreSQL 的 `SUM` 是 NULL，直接写入 NOT NULL 预聚合列会让整个任务失败。

### D. 扩展预聚合读取

在 `fillDashboardUsageStatsAggregated` 中，从：

- `usage_dashboard_daily`
- `usage_dashboard_hourly`

读取新字段并 Scan 到 `DashboardStats`。

### E. 扩展管理 API

在 `backend/internal/handler/admin/dashboard_handler.go` 的 `GetStats` 响应中增加：

```go
"total_provider_cache_read_tokens": stats.TotalProviderCacheReadTokens,
"total_cache_hit_requests":         stats.TotalCacheHitRequests,
"total_forced_cache_read_tokens":   stats.TotalForcedCacheReadTokens,
"total_reported_requests":          stats.TotalReportedRequests,
"total_estimated_requests":         stats.TotalEstimatedRequests,
"total_unavailable_requests":       stats.TotalUnavailableRequests,

"today_provider_cache_read_tokens": stats.TodayProviderCacheReadTokens,
"today_cache_hit_requests":         stats.TodayCacheHitRequests,
"today_forced_cache_read_tokens":   stats.TodayForcedCacheReadTokens,
"today_reported_requests":          stats.TodayReportedRequests,
"today_estimated_requests":         stats.TodayEstimatedRequests,
"today_unavailable_requests":       stats.TodayUnavailableRequests,
```

### F. 扩展趋势和模型接口

在 `backend/internal/repository/usage_log_repo_trend.go` 中修改：

- 所有 trend SELECT；
- 所有 model SELECT；
- `scanTrendRows`；
- model 的 `rows.Scan`；
- 按 requested/upstream/mapping model 的分支。

否则首页总览能显示，但选择模型或用户后字段又会消失。

### G. 修正回填时区边界并回填历史预聚合

新列默认值是 0。迁移后只重算新时间桶，旧的小时/日记录仍会是 0。

当前 `DashboardAggregationService.backfillRange` 先 `start.UTC()`，再按 UTC 零点切成 24 小时窗口；而 `dashboardAggregationRepository.AggregateRange` 按应用时区（默认 `Asia/Shanghai`）切日。这会让首尾本地日重复或混入范围外小时。

回填只接受应用时区的完整本地日，并按日历日而不是固定 24 小时计算：

```go
func startOfDayInLocation(value time.Time, loc *time.Location) time.Time {
	local := value.In(loc)
	year, month, day := local.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

func validateBackfillRange(start, end time.Time, maxDays int) error {
	loc := timezone.Location()
	startLocal := start.In(loc)
	endLocal := end.In(loc)
	if !startLocal.Equal(startOfDayInLocation(startLocal, loc)) ||
		!endLocal.Equal(startOfDayInLocation(endLocal, loc)) ||
		!endLocal.After(startLocal) {
		return errors.New("回填范围必须是应用时区的完整本地日")
	}
	if endLocal.After(time.Now().In(loc)) {
		return errors.New("回填结束时间不能在未来")
	}

	days := 0
	for cursor := startLocal; cursor.Before(endLocal); cursor = cursor.AddDate(0, 0, 1) {
		days++
		if maxDays > 0 && days > maxDays {
			return ErrDashboardBackfillTooLarge
		}
	}
	return nil
}
```

在 `TriggerBackfill` 中用该函数替换 `end.Sub(start) > maxDays*24h`。然后让 `backfillRange` 使用同一时区迭代：

```go
loc := timezone.Location()
startLocal := start.In(loc)
endLocal := end.In(loc)
cursor := startOfDayInLocation(startLocal, loc)

for cursor.Before(endLocal) {
	windowEnd := cursor.AddDate(0, 0, 1)
	if windowEnd.After(endLocal) {
		windowEnd = endLocal
	}
	if err := s.aggregateRange(ctx, cursor, windowEnd); err != nil {
		return err
	}
	cursor = windowEnd
}
```

手工历史回填不得调用：

```go
s.repo.UpdateAggregationWatermark(ctx, end.UTC())
s.maybeCleanupRetention(ctx, end.UTC())
```

watermark 只由正常定时聚合推进，保留清理也只能以服务器当前时间驱动。否则历史回填会让 watermark 倒退，未来参数甚至可能提前删除 `usage_logs` 和计费去重数据。

`dashboardAggregationRepository.AggregateRange` 和 `RecomputeRange` 里的 `dayEnd.Add(24 * time.Hour)` 也要改为 `dayEnd.AddDate(0, 0, 1)`。

管理端日期解析也必须使用日历日：

- `dashboard_handler.go` 的 `endTime = t.Add(24 * time.Hour)` 改为 `t.AddDate(0, 0, 1)`；
- 所有仅用于返回末日日期的 `endTime.Add(-24 * time.Hour)` 改为 `endTime.AddDate(0, 0, -1)`。

修改后至少补两个测试：

- `Asia/Shanghai` 的起止本地日期不会多触及前一天或后一天；
- `America/New_York` 跨春季跳时和秋季回拨的范围都按完整日历日处理，不漏一小时也不多一小时。

配置中临时开启：

```yaml
dashboard_aggregation:
  backfill_enabled: true
  backfill_max_days: 31
```

该配置不热加载。修改为 true 后必须重启 Ringstar，再调用回填接口；回填结束后改回 false 也需要重启才生效。

然后调用现有管理接口：

```text
POST /api/v1/admin/dashboard/aggregation/backfill
```

请求体：

```json
{
  "start": "2026-07-12T00:00:00+08:00",
  "end": "2026-08-12T00:00:00+08:00"
}
```

以上 `+08:00` 对应默认 `Asia/Shanghai`。先从 Ringstar 启动日志中的 `Timezone initialized: ...` 确认实际应用时区。如果使用其它时区，必须按该时区构造起止本地午夜；有夏令时的范围两端 offset 可能不同。

调用时让 HTTP 错误直接失败：

```bash
set -euo pipefail
export BACKFILL_TRIGGERED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"start":"2026-07-12T00:00:00+08:00","end":"2026-08-12T00:00:00+08:00"}' \
  "$RINGSTAR_URL/api/v1/admin/dashboard/aggregation/backfill" \
  | jq -e '(.data.status // .status) == "accepted"'
```

该接口只表示“已接受”，回填在后台异步执行。必须等待日志出现：

```text
[DashboardAggregation] 回填聚合完成
```

并查询小时/日表确认新字段已更新，之后再清理 Dashboard Redis 缓存。回填失败会写日志，HTTP 接口本身不会等待失败结果。

回填完成后可以重新关闭 `backfill_enabled`。原始 `usage_logs` 默认只保留 90 天，不能回填已被清理的数据。

## 6. 前端改造

## 6.1 统一计算函数

文件：`frontend/src/utils/cacheCoverage.ts`

`CacheCoverageSource` 还应接收当前范围的 `requests`。关键规则：

```ts
const requests = Math.max(Number(source.requests || 0), 0)
const reported = Math.max(Number(source.reported_requests || 0), 0)
const estimated = Math.max(Number(source.estimated_requests || 0), 0)
const unavailable = Math.max(Number(source.unavailable_requests || 0), 0)
const unknown = Math.max(requests - reported - estimated - unavailable, 0)

const hasV2Contract =
  source.requests !== undefined &&
  source.requests !== null &&
  source.provider_cache_read_tokens !== undefined &&
  source.provider_cache_read_tokens !== null &&
  source.forced_cache_read_tokens !== undefined &&
  source.forced_cache_read_tokens !== null &&
  source.reported_requests !== undefined &&
  source.reported_requests !== null &&
  source.estimated_requests !== undefined &&
  source.estimated_requests !== null &&
  source.unavailable_requests !== undefined &&
  source.unavailable_requests !== null

const providerRead = hasV2Contract
  ? Math.max(Number(source.provider_cache_read_tokens || 0), 0)
  : 0

const forced = Math.max(Number(source.forced_cache_read_tokens || 0), 0)
const ordinaryInput = Math.max(Number(source.input_tokens || 0), 0) + forced

const total =
  ordinaryInput +
  Number(source.cache_creation_tokens || 0) +
  providerRead

const fullyReported =
  hasV2Contract &&
  requests > 0 &&
  reported === requests &&
  estimated === 0 &&
  unavailable === 0 &&
  unknown === 0

const coverageAvailable = fullyReported && total > 0
const coverage = coverageAvailable ? (providerRead / total) * 100 : null
```

不能在 v2 字段缺失时回退到 `cache_read_tokens - forced_cache_read_tokens` 并标成真实上游读取。旧后端和历史数据没有来源证明，这种 fallback 会制造假命中。

观测比例使用总请求数：

```ts
const reportedRatio = requests > 0 ? (reported / requests) * 100 : 0
const partiallyObservable = reported > 0 && reported < requests
const unobservable = requests > 0 && reported === 0
```

如果实现了上一节的 reported-only token 桶，混合流量可以改为显示“已上报子集覆盖率”，并清楚标注样本范围。

现有三个重复计算点也必须同步修改，不能只改共享 helper：

- `CacheHitRateChart.combineModelMetrics`
  - 累加每项的 `requests`、forced 和三态计数；
  - forced 加回 ordinary input；
  - 不再手工无条件计算 `providerRead / total`；
  - 把聚合后的 source 再交给 `getCacheCoverageMetrics`。
- `TokenUsageTrend.observabilitySummary`
  - 分母使用所有趋势点的 `requests`；
  - unknown 使用 `requests - 三态之和`；
  - 不使用三态之和作为总请求数。
- `DashboardView.formatCacheCoverage`
  - `coverage === null` 时输出不可观测/部分可观测文案；
  - 只有 number 才调用 `toFixed`。

对应类型也要改为：

```ts
export interface CacheCoverageMetrics {
  // ...
  coverage: number | null
  coverageAvailable: boolean
}
```

## 6.2 面板显示规则

文件：

- `frontend/src/components/charts/CacheHitRateChart.vue`
- `frontend/src/components/charts/TokenUsageTrend.vue`
- `frontend/src/views/admin/DashboardView.vue`

推荐显示：

- 上游缓存读取；
- 缓存创建；
- 普通输入；
- 缓存读取覆盖率；
- 可观测请求数；
- 账务调整 token。

状态规则：

- 当前范围全部是 `reported`：显示全量覆盖率；
- 同时有 reported 和其它来源：显示“部分可观测 reported/requests”，隐藏全量覆盖率；
- 只有 `estimated` / `unavailable`：显示“缓存不可观测”；
- 存在 NULL 历史行：显示 unknown 数量，不把其旧 cache read 当真实命中；
- 没有 v2 契约字段：显示“暂无观测数据”，不要显示红色 0% 告警；
- `forced_cache_read_tokens > 0`：计入普通输入分母，同时单独显示账务调整，不进入真实缓存读取切片。

## 6.3 类型与 API 字段

同步修改：

- `frontend/src/types/index.ts`
- `frontend/src/api/usage.ts`
- 管理端 Dashboard API 类型
- 中英文 i18n

调用 `getCacheCoverageMetrics` 时必须同时传入范围总请求数：

- 今日总览传 `today_requests`；
- 累计总览传 `total_requests`；
- 趋势点和模型项传各自的 `requests`；
- SessionUsage 传 `usage.requests`。

新增字段在灰度阶段可以先声明为可选；后端全部上线后再考虑设为必填。

## 7. 提高真实缓存复用，而不是只让面板有数字

## 7.1 稳定 Prompt 前缀

把内容按以下顺序组织：

1. 稳定 system prompt；
2. 稳定工具定义和 schema；
3. 稳定项目规则；
4. 历史对话；
5. 本轮动态内容。

不要把以下内容放到稳定前缀开头：

- 当前时间；
- 随机 ID；
- 每轮变化的工作目录或状态；
- 无序 map 序列化结果；
- 每轮重新排列的工具；
- 动态检索结果。

这些内容应放到缓存断点之后。

## 7.2 保持模型、账号和路由稳定

同一会话频繁切换模型、平台或上游账号，通常不能复用同一个 provider cache。

`X-Session-Id` 有助于用量关联，某些调度策略也可能使用会话信号，但它本身不会创建 Prompt Cache。

`prompt_cache_key`、稳定前缀和 provider 的缓存规则仍然是独立条件。

## 7.3 Anthropic

保留客户端的：

- block-level `cache_control`
- 5m / 1h TTL
- system、message、tool 上的缓存标记

不要超过 Anthropic 支持的 cache control block 数。不要为了追求命中删除模型需要看到的上下文；Prompt Cache 不会减少模型可见上下文，只改变上游读取和计费方式。

## 7.4 OpenAI

OpenAI 当前官方规则参见 [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)。

GPT-5.6+ 的显式缓存应使用：

- 请求级 `prompt_cache_key`；
- 请求级 `prompt_cache_options.mode`；
- 请求级 `prompt_cache_options.ttl`，当前只支持 `30m`；
- 放在官方支持的 prompt content block 上的 `prompt_cache_breakpoint: {"mode":"explicit"}`。

Ringstar 不能把 Anthropic 的 `cache_control`、任意 message-level breakpoint 或 tool-level breakpoint原样发给 OpenAI。应先把 Anthropic 缓存意图规范化到一个受支持的 content block，再删除 OpenAI 不认识的 `cache_control` / `breakpoint` 字段，否则上游可能返回 400。

当前 `prompt_cache_capability.go` 对 GPT-5.6+ 直接保留原字段的策略需要修正为“校验位置并规范化”，不是无条件透传。

GPT-5.6 之前的模型：

- 保留 `prompt_cache_key`；
- 不发送 GPT-5.6 显式 breakpoint；
- 标准 OpenAI API 仍可能使用旧的 `prompt_cache_retention`；
- ChatGPT 内部 Codex 等端点会拒绝 `prompt_cache_retention`，应按 endpoint 能力剥离，不能对所有标准 OpenAI 请求一刀切删除。

所有能力判断必须基于“最终发给上游的模型和 endpoint”，不能只看客户端请求模型。

## 7.5 Cursor

Cursor 私有协议可能在服务端内部使用缓存，但 Ringstar 当前没有经过真实样本验证的 usage 字段映射。

在满足以下条件前不要打开私有 decoder：

- 至少 20 个脱敏真实样本；
- 流式和非流式字段映射一致；
- explicit zero 能与 missing 区分；
- 与上游账单或官方 usage 独立对账；
- 未知帧安全回退为 unavailable；
- 灰度期间不影响响应内容和工具调用质量。

## 8. 服务器部署顺序

升级顺序必须是：

```text
备份数据库
  → 应用新增 SQL
  → 验证所有列和索引
  → 构建新前端和后端
  → 启动新容器
  → 回填预聚合
  → 确认回填完成
  → 清理旧 Dashboard Redis 缓存
  → 产生新请求
  → 验证 API 和面板
```

不要先启动引用新列的二进制，再补数据库列。

## 8.1 备份

```bash
set -euo pipefail
cd deploy
BACKUP="../ringstar-before-cache-bridge-$(date -u +%Y%m%dT%H%M%SZ).dump"

docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD";
    exec pg_dump -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "$BACKUP"

test -s "$BACKUP"
docker compose -f docker-compose.local.yml exec -T postgres \
  pg_restore -l < "$BACKUP" >/dev/null

echo "backup verified: $BACKUP"
```

Compose 的 `.env` 不一定会导出成宿主 Bash 环境变量，所以数据库命令在容器内读取 `POSTGRES_USER`、`POSTGRES_DB`、`POSTGRES_PASSWORD`。只有 `test -s` 和 `pg_restore -l` 都成功后才继续。

## 8.2 对已有实例手工执行迁移

假设迁移已拆分为两个文件：

```bash
set -euo pipefail
cd deploy

docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
  < ../backend/migrations/221_cache_usage_observation.sql

# 如果上次并发建索引中断，先检查是否存在 invalid index。
docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT c.relname, i.indisvalid
FROM pg_class c
JOIN pg_index i ON i.indexrelid = c.oid
WHERE c.relname = 'idx_usage_logs_session_usage_v2';
SQL
```

只有查询明确显示 `indisvalid = false` 时，才在执行 222 前运行：

```bash
docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
DROP INDEX CONCURRENTLY IF EXISTS idx_usage_logs_session_usage_v2;
SQL
```

确认不存在 invalid 同名索引后，执行并发索引迁移：

```bash
docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
  < ../backend/migrations/222_cache_usage_session_index_notx.sql
```

两个 SQL 都使用 `IF NOT EXISTS`；索引文件必须在事务外执行。完成后立即执行本文 9.2 的 schema 检查，任何字段或索引不符合都不能启动新二进制。

不要手工伪造 `schema_migrations` checksum。长期方案应让升级流程显式执行 migration runner。

## 8.3 从本地源码构建生产镜像

可以增加一个只覆盖构建方式的 Compose 文件：

```yaml
# deploy/docker-compose.cache-build.yml
services:
  sub2api:
    image: ringstar-cache-bridge:${RINGSTAR_TAG:-local}
    build:
      context: ..
      dockerfile: Dockerfile
      args:
        COMMIT: ${RINGSTAR_COMMIT:-local}
```

构建和启动：

```bash
set -euo pipefail
cd deploy
test -z "$(git -C .. status --porcelain)" || {
  echo "working tree is dirty; commit the deployment source first" >&2
  exit 1
}
export RINGSTAR_COMMIT="$(git -C .. rev-parse HEAD)"
export RINGSTAR_TAG="${RINGSTAR_COMMIT}"

docker compose \
  -f docker-compose.local.yml \
  -f docker-compose.cache-build.yml \
  build --no-cache sub2api

docker compose \
  -f docker-compose.local.yml \
  -f docker-compose.cache-build.yml \
  up -d sub2api
```

也可以使用仓库已有开发构建：

```bash
set -euo pipefail
cd deploy
docker compose -f docker-compose.dev.yml up --build
```

但开发 Compose 使用 debug 配置和独立容器名，不应直接替代已经规划好的生产配置。

## 8.4 确认前端已嵌入

根 `Makefile` 的正确顺序是：

```bash
set -euo pipefail
pnpm --dir frontend install
make build
```

`make build` 会先构建前端到 `backend/internal/web/dist`，再用 `-tags embed` 编译后端。

Dockerfile 也会执行同样流程。

检查：

```bash
set -o pipefail
curl --fail-with-body --silent --show-error http://127.0.0.1:8080/ \
  | grep -oE 'assets/[A-Za-z0-9_.-]+\.js'
```

如果响应是：

```text
Frontend not embedded. Build with -tags embed to include frontend.
```

说明后端编译时漏了 `-tags embed`。

还要检查 `/app/data/public`。该目录会优先覆盖嵌入前端，旧文件可能遮住新包：

```bash
docker compose -f docker-compose.local.yml exec sub2api \
  ls -la /app/data/public
```

## 8.5 回填并确认预聚合

按 5.9 的方式触发回填后，接口只会返回 accepted。等待后台日志：

```bash
: "${BACKFILL_TRIGGERED_AT:?请先按 5.9 记录触发时间}"
docker compose -f docker-compose.local.yml logs \
  --since="$BACKFILL_TRIGGERED_AT" sub2api \
  | grep '回填聚合完成'
```

接口实际返回 HTTP 200 + `status=accepted`，不代表后台已完成。然后按本次范围和触发时间核对聚合表：

```bash
export BACKFILL_START_DATE="2026-07-12"
export BACKFILL_END_DATE="2026-08-12"

docker compose -f docker-compose.local.yml exec -T \
  -e PSQL_TRIGGERED_AT="$BACKFILL_TRIGGERED_AT" \
  -e PSQL_START_DATE="$BACKFILL_START_DATE" \
  -e PSQL_END_DATE="$BACKFILL_END_DATE" \
  postgres sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -v triggered_at="$PSQL_TRIGGERED_AT" -v start_date="$PSQL_START_DATE" -v end_date="$PSQL_END_DATE" -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT
  COUNT(*) > 0 AS has_target_buckets,
  BOOL_AND(computed_at >= :'triggered_at'::timestamptz)
    AS all_recomputed_after_trigger,
  MAX(computed_at) AS latest_computed_at,
  SUM(reported_requests) AS reported,
  SUM(estimated_requests) AS estimated,
  SUM(unavailable_requests) AS unavailable,
  SUM(provider_cache_read_tokens) AS provider_cache_read,
  SUM(forced_cache_read_tokens) AS forced_cache_read
FROM usage_dashboard_daily
WHERE bucket_date >= :'start_date'::date
  AND bucket_date < :'end_date'::date;
SQL
```

只有 `has_target_buckets=true` 且 `all_recomputed_after_trigger=true` 才算本次回填完成。还应把该范围聚合值与 `usage_logs` 的同口径 SQL 对比。若日志出现“回填失败”或数值明显不符，先修复，不要继续清缓存并宣布上线成功。

## 8.6 清理旧 Dashboard 缓存

Redis key 是：

```text
<dashboard_cache.key_prefix>dashboard:stats:v1
```

默认前缀是 `sub2api:`，但配置可以自定义或设为空；Redis DB 也不一定是 0。先从正在运行的 Ringstar 容器读取实际 DB，再要求扫描结果恰好只有一个：

```bash
set -euo pipefail

ACTUAL_REDIS_DB="$(
  docker compose -f docker-compose.local.yml exec -T sub2api \
    sh -lc 'printf "%s" "${REDIS_DB:-0}"'
)"
[[ "$ACTUAL_REDIS_DB" =~ ^[0-9]+$ ]]

MATCHED_KEYS="$(
  docker compose -f docker-compose.local.yml exec -T \
    -e TARGET_REDIS_DB="$ACTUAL_REDIS_DB" \
    redis sh -euc \
    'redis-cli -n "$TARGET_REDIS_DB" --scan --pattern "*dashboard:stats:v1"'
)"

mapfile -t DASHBOARD_KEYS <<< "$MATCHED_KEYS"
if [[ "${#DASHBOARD_KEYS[@]}" -ne 1 || -z "${DASHBOARD_KEYS[0]}" ]]; then
  printf 'expected exactly one dashboard cache key in Redis DB %s, got:\n%s\n' \
    "$ACTUAL_REDIS_DB" "$MATCHED_KEYS" >&2
  exit 1
fi

docker compose -f docker-compose.local.yml exec -T \
  -e TARGET_REDIS_DB="$ACTUAL_REDIS_DB" \
  -e TARGET_KEY="${DASHBOARD_KEYS[0]}" \
  redis sh -euc \
  'test "$(redis-cli -n "$TARGET_REDIS_DB" DEL "$TARGET_KEY")" = "1"'
```

Redis 容器会使用部署时设置的 `REDISCLI_AUTH`。如果使用 ACL username 或外部 Redis，请把命令替换为对应认证参数。共享 Redis 或扫描到多个 key 时，应从实际 `dashboard_cache.key_prefix` 组成精确 key，不能批量删除。无法确认 key 时，等待配置的 Dashboard TTL 自然过期更安全，默认是 30 秒。

最后浏览器执行硬刷新。

## 9. 分层验证

## 9.1 验证版本和路由

```bash
docker compose -f docker-compose.local.yml exec sub2api \
  /app/sub2api --version

curl -fsS http://127.0.0.1:8080/health
```

版本号相同不代表代码相同，应同时检查 commit。

## 9.2 验证数据库列和索引

```bash
docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_name = 'usage_logs'
  AND column_name IN ('cache_usage_source', 'forced_cache_read_tokens')
ORDER BY column_name;

SELECT indexname
FROM pg_indexes
WHERE tablename = 'usage_logs'
  AND indexname = 'idx_usage_logs_session_usage_v2';

SELECT c.relname AS index_name, i.indisvalid
FROM pg_class c
JOIN pg_index i ON i.indexrelid = c.oid
WHERE c.relname = 'idx_usage_logs_session_usage_v2';

SELECT conname, convalidated
FROM pg_constraint
WHERE conrelid = 'usage_logs'::regclass
  AND conname IN (
    'usage_logs_cache_usage_source_valid',
    'usage_logs_forced_cache_read_tokens_nonnegative'
  )
ORDER BY conname;

SELECT table_name, column_name
FROM information_schema.columns
WHERE table_name IN ('usage_dashboard_hourly', 'usage_dashboard_daily')
  AND column_name IN (
    'provider_cache_read_tokens',
    'cache_hit_requests',
    'forced_cache_read_tokens',
    'reported_requests',
    'estimated_requests',
    'unavailable_requests'
  )
ORDER BY table_name, column_name;
SQL
```

通过标准：

- `usage_logs` 返回 2 个新列；
- 两个 CHECK 约束都存在（允许 `convalidated = false`，但会约束后续新写入）；
- 两张预聚合表合计返回 12 行新列；
- session index 存在且 `indisvalid = true`。

## 9.3 产生一个可复现的 Anthropic 两轮请求

使用同一个模型、同一个缓存前缀和同一个 session：

```bash
set -euo pipefail
export RINGSTAR_URL="http://127.0.0.1:8080"
export RINGSTAR_KEY="<你的 API Key>"
export MODEL="<支持 Prompt Cache 的模型>"
export SID="autoclaw:cache-smoke-$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM"
export STABLE_PREFIX="$(
  printf 'Ringstar prompt cache smoke stable prefix. %.0s' {1..500}
)"
```

第一轮：

```bash
jq -n \
  --arg model "$MODEL" \
  --arg prefix "$STABLE_PREFIX" \
  --arg user "第一轮测试" \
  '{
    model: $model,
    max_tokens: 64,
    system: [{
      type: "text",
      text: $prefix,
      cache_control: {type: "ephemeral"}
    }],
    messages: [{role: "user", content: $user}]
  }' \
  | curl --fail-with-body --silent --show-error "$RINGSTAR_URL/v1/messages" \
      -H "Authorization: Bearer $RINGSTAR_KEY" \
      -H "Content-Type: application/json" \
      -H "X-Session-Id: $SID" \
      --data-binary @- \
  | jq -e '.usage'
```

第二轮保持 system 和工具定义完全相同，只改末尾用户消息：

```bash
jq -n \
  --arg model "$MODEL" \
  --arg prefix "$STABLE_PREFIX" \
  --arg user "第二轮测试" \
  '{
    model: $model,
    max_tokens: 64,
    system: [{
      type: "text",
      text: $prefix,
      cache_control: {type: "ephemeral"}
    }],
    messages: [{role: "user", content: $user}]
  }' \
  | curl --fail-with-body --silent --show-error "$RINGSTAR_URL/v1/messages" \
      -H "Authorization: Bearer $RINGSTAR_KEY" \
      -H "Content-Type: application/json" \
      -H "X-Session-Id: $SID" \
      --data-binary @- \
  | jq -e '.usage'
```

通常第一轮应看到 cache creation，第二轮应看到 cache read。不同 provider 有最小缓存前缀长度和 TTL 要求；前缀太短时 0 是正常结果。

## 9.4 验证 SessionUsage v2

```bash
set -euo pipefail
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $RINGSTAR_KEY" \
  "$RINGSTAR_URL/v1/sub2api/usage?session_id=$SID&min_requests=2&wait_ms=3000" \
  | jq -e '
      .usage as $u |
      .schema_version == 2 and
      .settled == true and
      ([
        $u.requests,
        $u.cache_read_tokens,
        $u.provider_cache_read_tokens,
        $u.forced_cache_read_tokens,
        $u.cache_hit_requests,
        $u.reported_requests,
        $u.estimated_requests,
        $u.unavailable_requests,
        $u.unknown_requests
      ] | all(.[]; type == "number")) and
      ($u.cache_observation_status == "fully_reported") and
      ($u.cache_hit_rate_percent | type == "number") and
      ($u.requests == 2) and
      (($u.reported_requests + $u.estimated_requests + $u.unavailable_requests) == $u.requests) and
      ($u.reported_requests == $u.requests) and
      ($u.provider_cache_read_tokens > 0) and
      ($u.forced_cache_read_tokens <= $u.cache_read_tokens)
    '
```

必须确认：

- `schema_version` 是 2；
- `settled` 是 true；
- 唯一 session 下 `requests` 恰好为 2；
- 三态请求数之和恰好等于 `requests`，没有 NULL/unknown 新请求；
- 两个请求均为 `reported`；
- 第二轮产生真实 `provider_cache_read_tokens > 0`；
- `provider_cache_read_tokens` 不包含 forced。

返回 404 或没有 `schema_version: 2`，说明服务器仍在运行旧后端。

## 9.5 验证 usage_logs

```bash
docker compose -f docker-compose.local.yml exec -T postgres \
  sh -lc 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT
  cache_usage_source,
  COUNT(*) AS requests
FROM usage_logs
WHERE created_at >= NOW() - INTERVAL '1 day'
GROUP BY cache_usage_source
ORDER BY requests DESC;

SELECT
  COUNT(*) AS total_requests,
  COALESCE(SUM(input_tokens), 0) AS input_tokens,
  COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
  COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
  COALESCE(
    SUM(GREATEST(
      cache_read_tokens - GREATEST(COALESCE(forced_cache_read_tokens, 0), 0),
      0
    ))
      FILTER (WHERE cache_usage_source = 'reported'),
    0
  ) AS provider_cache_read_tokens,
  COUNT(*) FILTER (
    WHERE cache_usage_source = 'reported'
      AND GREATEST(
        cache_read_tokens - GREATEST(COALESCE(forced_cache_read_tokens, 0), 0),
        0
      ) > 0
  ) AS cache_hit_requests,
  COALESCE(SUM(GREATEST(COALESCE(forced_cache_read_tokens, 0), 0)), 0)
    AS forced_cache_read_tokens,
  COUNT(*) FILTER (WHERE cache_usage_source = 'reported')
    AS reported_requests,
  COUNT(*) FILTER (WHERE cache_usage_source = 'estimated')
    AS estimated_requests,
  COUNT(*) FILTER (WHERE cache_usage_source = 'unavailable')
    AS unavailable_requests,
  COUNT(*) -
    COUNT(*) FILTER (
      WHERE cache_usage_source IN ('reported', 'estimated', 'unavailable')
    ) AS unknown_requests
FROM usage_logs
WHERE created_at >= NOW() - INTERVAL '1 day';
SQL
```

## 9.6 验证管理 API

用管理员 token 请求：

```bash
set -o pipefail
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$RINGSTAR_URL/api/v1/admin/dashboard/stats" \
  | jq -e '
      (.data // .) as $d |
      ($d.total_provider_cache_read_tokens != null) and
      ($d.total_forced_cache_read_tokens != null) and
      ($d.total_reported_requests != null) and
      ($d.total_estimated_requests != null) and
      ($d.total_unavailable_requests != null) and
      ($d.today_provider_cache_read_tokens != null) and
      ($d.today_forced_cache_read_tokens != null) and
      ($d.today_reported_requests != null) and
      ($d.today_estimated_requests != null) and
      ($d.today_unavailable_requests != null)
    '
```

确认累计和今日都包含五个新字段。

产生冒烟请求后，再断言 trend 和 models 的每一行都包含新字段：

```bash
set -euo pipefail
: "${APP_TZ:?请先按启动日志中的 Timezone initialized 值设置 APP_TZ}"
export TODAY="$(TZ="$APP_TZ" date +%F)"
export TIMEZONE_QUERY="$(
  jq -rn --arg value "$APP_TZ" '$value | @uri'
)"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$RINGSTAR_URL/api/v1/admin/dashboard/trend?start_date=$TODAY&end_date=$TODAY&granularity=hour&timezone=$TIMEZONE_QUERY" \
  | jq -e '
      (.data.trend // .trend) as $rows |
      ($rows | length) > 0 and
      all($rows[]; (
        .provider_cache_read_tokens != null and
        .forced_cache_read_tokens != null and
        .reported_requests != null and
        .estimated_requests != null and
        .unavailable_requests != null
      ))
    '

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$RINGSTAR_URL/api/v1/admin/dashboard/models?start_date=$TODAY&end_date=$TODAY&timezone=$TIMEZONE_QUERY" \
  | jq -e '
      (.data.models // .models) as $rows |
      ($rows | length) > 0 and
      all($rows[]; (
        .provider_cache_read_tokens != null and
        .forced_cache_read_tokens != null and
        .reported_requests != null and
        .estimated_requests != null and
        .unavailable_requests != null
      ))
    '
```

否则按模型或用户筛选时仍会退回旧口径。

## 9.7 Provider 预期

Anthropic 原生或兼容上游：

- 有缓存字段时是 `reported`；
- 第一轮常见 creation，第二轮常见 read；
- 上游明确报 0 仍是 `reported`。

OpenAI / Responses / Chat Completions：

- 常见字段是 `input_tokens_details.cached_tokens` 或 `prompt_tokens_details.cached_tokens`；
- 通常只有 cache read，没有 cache creation；
- 必须完成 OpenAI `CacheUsageSource` 传播和落库改造。

Cursor：

- 当前是 `estimated`；
- cache read / creation 为 0；
- SessionUsage 返回 `cache_observation_status = "unobservable"`；
- SessionUsage 返回 `cache_hit_rate_percent = null`，而不是误导性的 `0`；
- 面板应显示“缓存不可观测”；
- 不能据此断言 Cursor 内部没有缓存。

历史行：

- `cache_usage_source` 为 NULL；
- 不计入三态请求数；
- 不计入可信 `provider_cache_read_tokens`；
- 只有迁移后的新请求才能验证新契约。

## 10. 常见故障判读

### 面板卡片完全不存在

- 前端仍是旧包；
- 后端没有嵌入新 dist；
- `/app/data/public` 旧文件覆盖了内嵌资源；
- 浏览器缓存未刷新。

### 卡片存在但所有数字为 0

- 管理 API 没下发新字段；
- 选定时间范围内没有新请求；
- 请求没有成功写入 usage log；
- 预聚合新列没有回填；
- 只有 Cursor 流量。

### `cache_read_tokens > 0`，但 `reported_requests = 0`

- OpenAI 原生路径没有写 `CacheUsageSource`；
- 数值被解析了，但字段存在性在协议转换中丢失；
- 全部 cache read 来自 `ForceCacheBilling`。

### `reported_requests > 0`，但一直读取 0

- 上游明确上报了 0；
- 稳定前缀太短；
- 第一轮只有 cache creation，还没有第二轮复用；
- 每轮前缀、工具顺序、模型或账号都变化；
- TTL 已过期；
- Ringstar 在转换中剥离了不被目标模型支持的显式字段。

### 少量新请求后显示“100% 可观测”

- 检查分母是否错误使用了 `reported + estimated + unavailable`；
- 正确分母是范围内全部 `requests`；
- NULL 历史行必须计入 unknown；
- 混合来源没有 reported-only token 桶时，应隐藏覆盖率而不是显示一个全局百分比。

### Dashboard API 500

先查日志和数据库：

```bash
docker compose -f docker-compose.local.yml logs --tail=200 sub2api
```

若出现：

```text
column "cache_usage_source" does not exist
```

说明迁移没有执行。

### SessionUsage 返回 0 请求

- 请求没有携带 `X-Session-Id`；
- 查询的 session 与请求头不完全一致；
- session 含控制字符或超过 255，被丢弃；
- 查询使用了不同 API Key；
- 用量仍在异步落库，可使用 `min_requests` 和 `wait_ms`。

### 面板数字更新慢

- Dashboard Redis 缓存默认 30 秒；
- 预聚合作业默认 60 秒；
- 历史时间桶需要 backfill；
- 浏览器仍在使用旧前端资源。

## 11. 测试清单

后端：

```bash
cd backend
go test ./internal/pkg/apicompat -run Cache
go test ./internal/service -run 'Cache|SessionUsage|PromptCache'
go test ./internal/repository -run 'Cache|SessionID|Stats'
go test ./internal/handler -run SessionUsage
go test ./...
```

前端：

```bash
pnpm --dir frontend run typecheck
pnpm --dir frontend exec vitest run \
  src/components/charts/__tests__/CacheHitRateChart.spec.ts \
  src/components/charts/__tests__/TokenUsageTrend.spec.ts \
  src/views/admin/__tests__/DashboardView.spec.ts
```

构建：

```bash
make build
```

至少补充以下失败用例再写实现：

- explicit `cached_tokens: 0` 被判为 `reported`；
- 缺少 cache 字段被判为 `unavailable`；
- Anthropic 兼容成功响应完全没有 usage 节点时最终写入 `unavailable`；
- 流式后续 delta 不会把 `reported` 降级；
- OpenAI 原生路径写入 `CacheUsageSource`；
- OpenAI 成功响应完全没有 usage 对象时最终写入 `unavailable`，不写 NULL；
- forced read 不进入 provider read；
- forced read 被加回普通输入分母，900 forced + 100 provider 的覆盖率是 10%；
- NULL 历史行不进入可信 provider read，且会增加 unknown；
- mixed source 在没有 reported-only token 桶时隐藏覆盖率；
- `combineModelMetrics`、趋势汇总和 Dashboard 格式化都正确处理 requests、forced 和 null coverage；
- OpenAI 只在合法 content block 上发送 breakpoint，不透传 Anthropic `cache_control`；
- session index 使用 `_notx.sql` 和 `CREATE INDEX CONCURRENTLY`；
- 没有 reported 行的预聚合仍写入 0，不因 filtered SUM 为 NULL 失败；
- Dashboard 总览、趋势、模型三条 API 都下发新字段；
- 手工回填不更新定时聚合 watermark，也不触发 retention cleanup；
- 回填拒绝未来时间，并按本地日历日限制最大天数；
- 非 UTC 和 DST 应用时区的回填只更新目标本地日期范围，不漏小时或多触及相邻日期；
- trend/models 的含末日日期范围在 DST 切换日仍使用本地日历边界；
- Cursor 显示不可观测而不是 0% 告警；
- SessionUsage 不能查询其他 API Key 的 session。

## 12. 上线验收标准

上线前逐项确认：

- 缓存相关文件已进入 Git 提交；
- 构建使用本地源码，不是官方 `latest`；
- 新 migration 已应用；
- `usage_logs` 两个新列存在；
- Dashboard 小时/日表五个新列存在；
- session index 存在且 `indisvalid = true`；
- `/v1/sub2api/usage` 返回 schema v2；
- OpenAI 和 Anthropic 新请求都有合法 source；
- Cursor 新请求标记为 estimated；
- Dashboard API 下发累计、今日、趋势、模型字段；
- backfill 配置变更后已重启，并以应用时区边界请求且接口返回 accepted；
- 本次触发时间之后出现回填完成日志，目标范围的 computed_at 已更新；
- 按实际 Redis DB 和 `dashboard_cache.key_prefix` 清理旧 Dashboard 缓存，或等待实际 TTL；
- 最近时间桶已重算，历史范围已回填并与 raw usage 对账；
- 两轮真实请求能在 DB、SessionUsage 和面板三处对账；
- forced read 没有被显示成真实 provider 命中；
- forced read 已归回普通输入分母；
- NULL 历史行显示为 unknown，混合来源不显示伪精确覆盖率；
- 未观察到时显示“缓存不可观测”，不是误导性的 0%。

## 13. 回滚

本方案的数据库变更是添加列和索引。出现问题时：

1. 切回旧镜像；
2. 保留新增列，不要在事故处理中 DROP；
3. 清理 Dashboard Redis 缓存；
4. 排查新写入路径；
5. 修复后重新部署和回填。

旧二进制会忽略新增列，因此通常不需要回滚数据库。

## 14. 最短修复路径

如果只想先让服务器可靠显示，按以下顺序处理：

1. 把当前未提交缓存改动整理成独立提交；
2. 复核现有 221/222 缓存迁移未在目标库以其他文件名执行过；
3. 运行测试确认 OpenAI `CacheUsageSource` 传播和落库；
4. 验证 DashboardStats、预聚合、趋势和模型接口均按 reported-only provider 口径聚合；
5. 在服务器先应用 SQL；
6. 从当前提交构建自定义镜像；
7. 回填并核对最近数据，确认完成后再清 Redis Dashboard 缓存；
8. 用 Anthropic/OpenAI 两轮请求验证；
9. Cursor 流量只显示不可观测，暂不启用私有 decoder。

完成这九步后，面板显示的才是“真实上游缓存读取覆盖率”，而不是旧字段缺失、估算值或账务调整造成的假命中。

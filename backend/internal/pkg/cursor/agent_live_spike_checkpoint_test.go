//go:build livespike

package cursor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// checkpoint 续传通路的现场取证。
//
// 背景：网关现在每一轮都用空的 conversation_state 重开会话，整段历史靠
// Conversation.Render() 拍平成一条用户消息重发。官方 IDE 不是这么做的——它把
// 上一轮的 checkpoint（AgentServerMessage field 3）原样带回 RunRequest field 1，
// 于是历史留在上游、每轮只付增量的钱。在 agentic 循环里这是 O(N) 与 O(N^2) 的
// 差别，也是「同一个账号用官方 IDE 能多撑一倍」的最可能解释。
//
// 协议层的两端其实都通了（AgentTurnResult.ConversationState 收得下、
// RunRequestInput.ConversationState 发得出），缺的只是服务层没接。但在接之前
// 有五件事必须用真实上游确认，凭推断动手会把整套计费改错：
//
//	S0 凭证与计量口径预检（只读，唯一一个 web 态 token 也能跑的）
//	S1 带 checkpoint 的续传轮，上游到底认不认（这条不成立后面全免谈）
//	S2 以工具调用收尾的那一轮，上游发不发 checkpoint
//	S3 续传到底省不省钱、省多少（唯一的地面真相是 usage-summary 的美分差）
//	S4 checkpoint 失效/损坏时上游的反应，决定回退策略怎么写
//	S5 checkpoint 换个账号用会怎样，决定 failover 时必须作废到什么程度
//
// 这些用例以「打印结论」为主、断言为辅：目的是取证，不是守住已知行为。
//
//	GOOS=linux GOARCH=amd64 go test -tags=livespike -c -o cursor-spike ./internal/pkg/cursor
//	CURSOR_ACCESS_TOKEN=<jwt> ./cursor-spike -test.run TestLiveSpikeCheckpoint -test.v -test.timeout 40m
//
// 环境变量：
//
//	CURSOR_ACCESS_TOKEN    必填，session 类型的 access_token（S0 例外，web 态也收）
//	CURSOR_ACCESS_TOKEN_2  选填，第二个账号的 token，只有 S5 用
//	CURSOR_USER_ID         选填，S0/S3 读额度要的 user_id（cookie 是 "<user_id>::<jwt>"）
//	SPIKE_COST_TURNS       选填，S3 每种模式跑几轮，默认 4
//	SPIKE_USAGE_SETTLE_S   选填，S3 读额度前等几秒让上游结算，默认 25

// checkpointSecret 是个不可能被猜到的暗号。续传成不成立，就看第二轮能不能复述它。
const checkpointSecret = "ringstar-4712-zx9"

// ---------------------------------------------------------------------------
// 共用驱动
// ---------------------------------------------------------------------------

// spikeTurn 是一轮 Run 请求的全部输入。
type spikeTurn struct {
	Prompt         string
	ConversationID string
	State          []byte
	Tools          []McpTool
	// Token 为空时用 CURSOR_ACCESS_TOKEN；S5 靠它换账号。
	Token string
	// Observe 是这一轮的观察窗口，到点关流。
	Observe time.Duration
	// StopOn 返回 true 时提前收尾；为 nil 时只在 turn_ended 停。
	StopOn func(ServerMessage) bool
}

// spikeModel 决定这批取证打哪个模型。
//
// 默认 Auto：它不吃「包含的 API 用量」那一档，取证时最便宜。想验别的模型
// （比如 checkpoint 是不是只有具名模型才发）就设 SPIKE_MODEL=cursor/grok-4.5，
// 走 ResolveModel 拿到与生产路径完全一致的模型名与参数。
func spikeModel() ModelSelection {
	raw := strings.TrimSpace(os.Getenv("SPIKE_MODEL"))
	if raw == "" {
		return ModelSelection{ModelID: AutoModelID, Params: []ModelParam{}}
	}
	return ResolveModel(raw)
}

// runSpikeTurn 开一条流、跑完一轮、把会话交回调用方检查。
func runSpikeTurn(t *testing.T, ctx context.Context, turn spikeTurn) *spikeSession {
	t.Helper()

	opts := spikeOptions(t)
	if strings.TrimSpace(turn.Token) != "" {
		opts.AccessToken = turn.Token
		opts.Telemetry = DeriveTelemetryIDs(turn.Token)
	}

	selection := spikeModel()
	body, err := EncodeRunRequest(RunRequestInput{
		Text:              turn.Prompt,
		ConversationID:    turn.ConversationID,
		ConversationState: turn.State,
		RequestContext:    EncodeRequestContext(DefaultRequestContextEnv("spike")),
		ModelID:           selection.ModelID,
		ModelParams:       selection.Params,
		MaxMode:           selection.MaxMode,
		Tools:             turn.Tools,
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}

	stream, err := openAgentStream(ctx, opts, turn.ConversationID)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	session := &spikeSession{t: t, stream: stream, start: time.Now()}
	if err := stream.send(body); err != nil {
		t.Fatalf("send run request: %v", err)
	}
	stream.startHeartbeat(ctx)

	observe := turn.Observe
	if observe <= 0 {
		observe = 120 * time.Second
	}
	session.pump(observe, false, func(message ServerMessage, _ []byte) bool {
		if turn.StopOn != nil && turn.StopOn(message) {
			return true
		}
		return message.Kind == KindTurnEnded
	})
	return session
}

// lastCheckpoint 取这一轮收到的最后一个 checkpoint。
// 没有 checkpoint 时返回 nil——S2 要区分的正是这种情况。
func (s *spikeSession) lastCheckpoint() []byte {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if s.frames[i].Kind != KindCheckpoint {
			continue
		}
		message, err := ParseServerMessage(s.frames[i].Raw)
		if err != nil {
			continue
		}
		return message.ConversationState
	}
	return nil
}

// frameInventory 按类型汇总这一轮收到的所有帧。
//
// 「没拿到 checkpoint」有两种截然不同的原因：上游压根没发，或者发了但我们没认出来
// （field 3 换了号、或者被归进了 KindOther）。只看 checkpoint 帧区分不了这两种，
// 所以取证时要把整轮的帧谱摊开。
func (s *spikeSession) frameInventory() string {
	order := make([]ServerMessageKind, 0, 8)
	count := map[ServerMessageKind]int{}
	for _, frame := range s.frames {
		if _, seen := count[frame.Kind]; !seen {
			order = append(order, frame.Kind)
		}
		count[frame.Kind]++
	}
	parts := make([]string, 0, len(order))
	for _, kind := range order {
		parts = append(parts, fmt.Sprintf("%s×%d", kind, count[kind]))
	}
	return strings.Join(parts, " ")
}

// dumpUnclassifiedFrames 展开所有没被归类的帧，找漏认的 checkpoint 就靠它。
func (s *spikeSession) dumpUnclassifiedFrames(t *testing.T) {
	t.Helper()
	shown := 0
	for _, frame := range s.frames {
		switch frame.Kind {
		case KindOther, KindUnknown, KindKV:
		default:
			continue
		}
		if shown >= spikeEnvInt("SPIKE_DUMP_FRAMES", 12) {
			t.Logf("... 未分类帧还有更多，已截断（调大 SPIKE_DUMP_FRAMES 可看全）")
			return
		}
		shown++
		t.Logf("[%6.1fs] %s 原始结构:\n%s", frame.At.Seconds(), frame.Kind,
			dumpProto(frame.Raw, "        ", 0))
	}
	if shown == 0 {
		t.Logf("没有未分类的帧——上游确实没发我们认不出的东西")
	}
}

// checkpointFrames 返回每个 checkpoint 帧的到达时刻与字节数，用来看它是什么时候发的。
func (s *spikeSession) checkpointFrames() []string {
	out := make([]string, 0, 4)
	for _, frame := range s.frames {
		if frame.Kind != KindCheckpoint {
			continue
		}
		size := 0
		if message, err := ParseServerMessage(frame.Raw); err == nil {
			size = len(message.ConversationState)
		}
		out = append(out, fmt.Sprintf("%.1fs/%dB", frame.At.Seconds(), size))
	}
	return out
}

func newSpikeConversationID(tag string) string {
	return "spike-" + tag + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func spikeEnvInt(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// S0：凭证与计量口径预检
// ---------------------------------------------------------------------------

// TestLiveSpikeCheckpointPrecheck 在烧额度之前先确认两件事：凭证能不能用，
// 以及这个账号的用量数字是不是还会动。
//
// 与其余用例不同，它只读 dashboard、不开 Agent 流，所以 web 态 token 也能跑
// （dashboard 认的本来就是浏览器 cookie）。S1–S5 需要 type=session 的 token，
// 拿 web token 打 Agent 只会得到 ERROR_NOT_LOGGED_IN。
//
// 之所以要单独查「还会不会动」：订阅内额度耗尽后 plan.used 会顶死在 limit 上，
// 此时 S3 的差值只剩按量那一半。这不影响结论方向，但会让绝对值偏小，事先知道
// 比事后困惑好。
func TestLiveSpikeCheckpointPrecheck(t *testing.T) {
	t.Logf("=== S0：凭证与计量口径预检（只读，不烧额度）===")

	userID := strings.TrimSpace(os.Getenv("CURSOR_USER_ID"))
	token := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN"))
	if userID == "" || token == "" {
		t.Skip("需要 CURSOR_USER_ID 与 CURSOR_ACCESS_TOKEN")
	}

	switch {
	case IsSessionToken(token):
		t.Logf("token 类型=session，S1–S5 可以跑")
	case IsWebToken(token):
		t.Logf("token 类型=web，只能跑本用例；S1–S5 需要 session 态 token")
	default:
		t.Logf("token 类型未知，DecodeJWTClaims 没解出 type")
	}
	if expiry := TokenExpiry(token); !expiry.IsZero() {
		t.Logf("token 过期时间=%s（距今 %.1f 天）",
			expiry.Format(time.RFC3339), time.Until(expiry).Hours()/24)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	opts := &Options{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	summary, err := FetchUsageSummary(ctx, opts, SessionCookie(userID, token))
	if err != nil {
		t.Fatalf("读额度失败，凭证多半不可用: %v", err)
	}

	scope := summary.Scope()
	t.Logf("计费周期 %s ~ %s，套餐=%s", summary.BillingCycleStart, summary.BillingCycleEnd, summary.MembershipType)
	if plan := scope.Plan; plan != nil {
		t.Logf("订阅内 used=%.0f limit=%.0f breakdown{included=%.0f bonus=%.0f total=%.0f} 美分",
			plan.UsedCents, plan.LimitCents,
			plan.Breakdown.IncludedCents, plan.Breakdown.BonusCents, plan.Breakdown.TotalCents)
		if plan.RemainingCents != nil && *plan.RemainingCents <= 0 {
			t.Logf("*** 订阅内额度已耗尽，plan.used 不再变化；S3 的差值将主要来自 breakdown.total 与按量 ***")
		}
	}
	if onDemand := scope.OnDemand; onDemand != nil {
		t.Logf("按量 enabled=%v used=%.0f 美分", onDemand.Enabled, onDemand.UsedCents)
		if !onDemand.Enabled {
			t.Logf("*** 按量未开启：订阅内额度耗尽后请求会被拒，S3 跑不出花费 ***")
		}
	}
	t.Logf("---- 结论 ----")
	t.Logf("S3 记账基线 usedCents=%.0f 美分", usedCents(summary))
}

// ---------------------------------------------------------------------------
// S1：带 checkpoint 的续传轮，上游认不认
// ---------------------------------------------------------------------------

// TestLiveSpikeCheckpointContinuation 是整个方案的地基。
//
// 第一轮塞一个暗号进去并拿到 checkpoint，第二轮只发「刚才的暗号是什么」——
// prompt 里不含暗号本身。模型答得出来，就说明上下文确实留在上游、不必重放。
//
// 同时跑一个不带 checkpoint 的对照轮：没有它，「模型答对了」也可能只是巧合或
// 上游按 conversation_id 自己续了会话，两种解释的后续设计完全不同。
func TestLiveSpikeCheckpointContinuation(t *testing.T) {
	selection := spikeModel()
	t.Logf("=== S1：conversation_state 续传是否成立（模型=%s max=%v）===",
		selection.ModelID, selection.MaxMode != nil && *selection.MaxMode)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	conversationID := newSpikeConversationID("cont")

	first := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         "请记住这个暗号：" + checkpointSecret + "。只回复 ok，不要解释。",
		ConversationID: conversationID,
		Observe:        90 * time.Second,
	})
	state := first.lastCheckpoint()
	t.Logf("第一轮：turn_ended=%v 正文=%q", first.turnEnded(), first.text())
	t.Logf("第一轮帧谱: %s", first.frameInventory())
	t.Logf("第一轮 checkpoint 帧: %v", first.checkpointFrames())
	if len(state) == 0 {
		t.Logf("*** 第一轮没拿到 checkpoint（field 3 一帧没来）***")
		t.Logf("下面展开所有未分类帧，确认是「上游没发」还是「我们没认出来」：")
		first.dumpUnclassifiedFrames(t)
	} else {
		t.Logf("checkpoint 大小=%dB", len(state))
	}

	const recall = "我刚才让你记住的暗号是什么？原样复述那个字符串，不要解释。"

	// 三个变体，缺一不可：
	//
	//	A 带 checkpoint + 同 conversation_id —— 原方案假设的续传路径
	//	B 不带 checkpoint + 同 conversation_id —— 上游是否自己按 id 续会话
	//	C 不带 checkpoint + 新 conversation_id —— 阴性对照，确认暗号不是猜出来的
	//
	// 没有 C 就无法排除「模型碰巧蒙对」；没有 B 就无法区分「记忆来自 checkpoint」
	// 与「记忆来自 conversation_id」，而这两者的落地方案天差地别。
	withState := ""
	if len(state) > 0 {
		a := runSpikeTurn(t, ctx, spikeTurn{
			Prompt:         recall,
			ConversationID: conversationID,
			State:          state,
			Observe:        90 * time.Second,
		})
		withState = a.text()
		t.Logf("A 带 checkpoint + 同 id: %q", withState)
	} else {
		t.Logf("A 带 checkpoint + 同 id: 跳过（第一轮没有 checkpoint 可用）")
	}

	b := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         recall,
		ConversationID: conversationID,
		Observe:        90 * time.Second,
	})
	sameID := b.text()
	t.Logf("B 无 checkpoint + 同 id: %q", sameID)

	c := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         recall,
		ConversationID: newSpikeConversationID("cont-fresh"),
		Observe:        90 * time.Second,
	})
	freshID := c.text()
	t.Logf("C 无 checkpoint + 新 id: %q", freshID)

	hitA := len(state) > 0 && strings.Contains(withState, checkpointSecret)
	hitB := strings.Contains(sameID, checkpointSecret)
	hitC := strings.Contains(freshID, checkpointSecret)

	t.Logf("---- 结论 ----")
	t.Logf("A(checkpoint)=%v B(同 id)=%v C(新 id)=%v", hitA, hitB, hitC)
	switch {
	case hitC:
		t.Errorf("阴性对照也答对了：暗号能被猜到或泄漏到了别处，这组实验无效，换暗号重做")
	case hitB:
		t.Logf("*** 上游按 conversation_id 自己维护会话：只要沿用同一个 id 就有上下文 ***")
		t.Logf("*** 那么省钱的做法不是回传 checkpoint，而是不要每轮重放历史 ***")
		t.Logf("*** 同时说明稳定 conversation_id 那个改动会让上下文翻倍，必须复核 ***")
	case hitA:
		t.Logf("*** 续传成立且只靠 checkpoint：原方案可行 ***")
	default:
		t.Logf("*** 三种方式都没有上下文：这条链路是彻底无状态的 ***")
		t.Logf("*** 全量重放不是浪费而是必需，省钱要另找路子（见 kv 帧里的内容寻址图）***")
	}
}

// ---------------------------------------------------------------------------
// S2：以工具调用收尾的那一轮，发不发 checkpoint
// ---------------------------------------------------------------------------

// TestLiveSpikeCheckpointOnToolCall 是最关键的未知。
//
// 网关收到 mcp 工具调用后是主动关流、不回 exec 回执的（见 agent_client.go 的
// KindToolCall 分支）。而真实 IDE 会先在流上回执、让结果进入服务端会话状态，
// 再开新流续跑。所以问题是：我们这种「不回执就关流」的收尾，上游有没有在关流前
// 发出可用的 checkpoint？
//
// 发了，工具循环就能吃到续传的红利——那才是烧额度的大头。
// 没发，续传就只能覆盖纯多轮对话，工具循环得另想办法（改成回执后再关流）。
func TestLiveSpikeCheckpointOnToolCall(t *testing.T) {
	t.Logf("=== S2：工具调用收尾的一轮是否带 checkpoint ===")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	tools := []McpTool{spikeBashTool()}
	conversationID := newSpikeConversationID("tool")
	prompt := ToolPolicyPreamble(tools) + "\n\n" + spikeProvokeShell

	var (
		toolCallAt  time.Duration
		gotToolCall bool
	)
	start := time.Now()

	// 收到工具调用后不立刻停，再观察一段时间：checkpoint 可能跟在调用后面。
	// 生产代码的 toolCallGrace 是 3 秒，这里放宽到 12 秒，好看清「到底有没有」
	// 与「有但来得比 grace 晚」这两种不同的结论。
	const observeAfterToolCall = 12 * time.Second
	session := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         prompt,
		ConversationID: conversationID,
		Tools:          tools,
		Observe:        150 * time.Second,
		StopOn: func(message ServerMessage) bool {
			if message.Kind == KindToolCall && !gotToolCall {
				gotToolCall = true
				toolCallAt = time.Since(start)
				t.Logf("[%6.1fs] 收到工具调用 name=%q，继续观察 %.0fs 看有没有 checkpoint",
					toolCallAt.Seconds(), message.ToolCall.Name, observeAfterToolCall.Seconds())
			}
			if gotToolCall && time.Since(start) > toolCallAt+observeAfterToolCall {
				return true
			}
			return false
		},
	})

	frames := session.checkpointFrames()
	state := session.lastCheckpoint()

	t.Logf("---- 结论 ----")
	t.Logf("收到工具调用=%v 于 %.1fs", gotToolCall, toolCallAt.Seconds())
	t.Logf("checkpoint 帧（时刻/字节）: %v", frames)

	if !gotToolCall {
		t.Skipf("这一轮模型没走 MCP 工具（正文=%q），换个 prompt 重试", session.text())
	}
	if len(state) == 0 {
		t.Logf("*** 工具轮没有 checkpoint ***")
		t.Logf("*** 结论：工具循环无法直接续传，需要改成「回执后再关流」拿新 checkpoint ***")
		return
	}

	t.Logf("*** 工具轮拿到了 checkpoint（%dB）***", len(state))

	// 拿到了就顺手验一下它能不能用：带着它把工具结果作为新一轮的输入送回去，
	// 看模型是不是基于结果继续，而不是把工具再调一遍。
	followUp := "工具已经执行完毕，输出是：\n" + spikeMarkerOutput +
		"\n请直接根据这个输出回答我，不要重复调用工具。"
	second := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         followUp,
		ConversationID: conversationID,
		State:          state,
		Tools:          tools,
		Observe:        120 * time.Second,
	})
	t.Logf("续传轮正文=%q turn_ended=%v 又调工具=%v",
		second.text(), second.turnEnded(), len(second.checkpointFrames()) > 0)
	if strings.Contains(second.text(), "ringstar-spike-marker") {
		t.Logf("*** 工具循环续传可用：模型采纳了结果 ***")
	} else {
		t.Logf("*** 续传轮没有采纳工具结果，重放格式可能还要调 ***")
	}
}

// ---------------------------------------------------------------------------
// S3：到底省不省钱
// ---------------------------------------------------------------------------

// usedCents 把订阅内与按量两部分已消耗额度加起来。
// Cursor 上游在 wire 上不返回任何用量，dashboard 的美分是唯一的地面真相。
//
// 订阅内那部分取 breakdown.total 而不是 used：used 会顶死在 limit 上不再变化
// （实测某 pro 账号 used=2000 limit=2000 remaining=0，而 breakdown.total=10968），
// 额度耗尽后拿它做差值会恒等于 0，把整个 A/B 量成「两边一样贵」。
// breakdown.total 才是与 get-aggregated-usage-events 的 totalCostCents 对得上的数。
func usedCents(summary *UsageSummary) float64 {
	if summary == nil {
		return 0
	}
	scope := summary.Scope()
	total := 0.0
	if scope.Plan != nil {
		if scope.Plan.Breakdown.TotalCents > 0 {
			total += scope.Plan.Breakdown.TotalCents
		} else {
			total += scope.Plan.UsedCents
		}
	}
	if scope.OnDemand != nil {
		total += scope.OnDemand.UsedCents
	}
	return total
}

func spikeUsageCookie(t *testing.T) string {
	t.Helper()
	userID := strings.TrimSpace(os.Getenv("CURSOR_USER_ID"))
	if userID == "" {
		t.Skip("CURSOR_USER_ID is not set（读额度要 cookie \"<user_id>::<jwt>\"）")
	}
	return SessionCookie(userID, spikeToken(t))
}

func readUsedCents(t *testing.T, ctx context.Context, cookie string) float64 {
	t.Helper()
	opts := &Options{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	summary, err := FetchUsageSummary(ctx, opts, cookie)
	if err != nil {
		t.Fatalf("fetch usage summary: %v", err)
	}
	return usedCents(summary)
}

// spikeCostTurns 是一段脚本化的多轮对话，两种模式跑同一份内容才有可比性。
func spikeCostTurns(n int) []string {
	base := []string{
		"用一句话解释什么是二分查找。",
		"给出它的时间复杂度，只回一行。",
		"它对未排序的数组能用吗？只回能或不能，加一句理由。",
		"用 Go 写一个最简版本，不要注释。",
		"这个实现在数组长度为 0 时会怎样？一句话。",
		"把边界条件补上，只给改动的那几行。",
	}
	if n > len(base) {
		n = len(base)
	}
	return base[:n]
}

// TestLiveSpikeCheckpointCost 量两种模式的真实花费。
//
// 这是整件事的验收口径：不是「理论上少发了多少 token」，而是同一段对话跑完，
// 账上少扣了多少美分。两种模式各用一个全新的 conversation_id，互不污染。
func TestLiveSpikeCheckpointCost(t *testing.T) {
	t.Logf("=== S3：重放 vs 续传，比 usage-summary 的美分差 ===")
	cookie := spikeUsageCookie(t)

	turns := spikeCostTurns(spikeEnvInt("SPIKE_COST_TURNS", 4))
	settle := time.Duration(spikeEnvInt("SPIKE_USAGE_SETTLE_S", 25)) * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// --- 模式 A：现有的全量重放 ---
	before := readUsedCents(t, ctx, cookie)
	t.Logf("[重放] 起始 used=%.4f 美分", before)

	conversation := &Conversation{}
	replayID := newSpikeConversationID("cost-replay")
	for i, prompt := range turns {
		conversation.Turns = append(conversation.Turns, Turn{Role: RoleUser, Text: prompt})
		rendered := conversation.Render()
		session := runSpikeTurn(t, ctx, spikeTurn{
			Prompt:         rendered,
			ConversationID: replayID,
			Observe:        120 * time.Second,
		})
		answer := session.text()
		conversation.Turns = append(conversation.Turns, Turn{Role: RoleAssistant, Text: answer})
		t.Logf("[重放] 第 %d 轮 发出=%dB 回复=%dB", i+1, len(rendered), len(answer))
	}
	time.Sleep(settle)
	afterReplay := readUsedCents(t, ctx, cookie)
	replayCost := afterReplay - before
	t.Logf("[重放] 合计 %.4f 美分", replayCost)

	// --- 模式 B：checkpoint 续传，每轮只发新问题 ---
	beforeCont := readUsedCents(t, ctx, cookie)
	t.Logf("[续传] 起始 used=%.4f 美分", beforeCont)

	continueID := newSpikeConversationID("cost-cont")
	var state []byte
	for i, prompt := range turns {
		session := runSpikeTurn(t, ctx, spikeTurn{
			Prompt:         prompt,
			ConversationID: continueID,
			State:          state,
			Observe:        120 * time.Second,
		})
		next := session.lastCheckpoint()
		if len(next) == 0 {
			t.Logf("[续传] 第 %d 轮没拿到 checkpoint，后续轮次会退化成冷启动", i+1)
		} else {
			state = next
		}
		t.Logf("[续传] 第 %d 轮 发出=%dB 回复=%dB checkpoint=%dB",
			i+1, len(prompt), len(session.text()), len(next))
	}
	time.Sleep(settle)
	afterCont := readUsedCents(t, ctx, cookie)
	continueCost := afterCont - beforeCont
	t.Logf("[续传] 合计 %.4f 美分", continueCost)

	t.Logf("---- 结论 ----")
	t.Logf("重放 %.4f 美分 / 续传 %.4f 美分（%d 轮）", replayCost, continueCost, len(turns))
	switch {
	case continueCost <= 0 || replayCost <= 0:
		t.Logf("*** 有一边没测到花费，可能是结算延迟，加大 SPIKE_USAGE_SETTLE_S 重跑 ***")
	case continueCost < replayCost:
		t.Logf("*** 续传省了 %.1f%%（倍率 %.2fx）***",
			(1-continueCost/replayCost)*100, replayCost/continueCost)
	default:
		t.Logf("*** 续传没有更便宜，方案的收益假设不成立，先别改生产路径 ***")
	}
}

// ---------------------------------------------------------------------------
// S4：checkpoint 失效时上游怎么反应
// ---------------------------------------------------------------------------

// TestLiveSpikeCheckpointInvalid 探损坏/错配的 checkpoint。
//
// 决定回退策略怎么写：如果上游报一个能识别的错误，网关可以捕获后原地退回全量
// 重放；如果它是静默吞掉或者干脆挂死，那就只能靠本地 TTL 保守过期，宁可少用
// checkpoint 也不能让用户等满看门狗。
func TestLiveSpikeCheckpointInvalid(t *testing.T) {
	t.Logf("=== S4：损坏 / 错配的 checkpoint ===")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	seedID := newSpikeConversationID("inv-seed")
	seed := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         "请记住这个暗号：" + checkpointSecret + "。只回复 ok。",
		ConversationID: seedID,
		Observe:        90 * time.Second,
	})
	state := seed.lastCheckpoint()
	if len(state) == 0 {
		t.Skip("种子轮没拿到 checkpoint")
	}
	t.Logf("种子 checkpoint=%dB", len(state))

	truncated := append([]byte(nil), state[:len(state)/2]...)
	garbage := make([]byte, 64)
	for i := range garbage {
		garbage[i] = byte(i * 7)
	}

	cases := []struct {
		name  string
		state []byte
		// convID 为空时用一个全新的 id，模拟 checkpoint 与会话对不上。
		convID string
	}{
		{name: "截断一半", state: truncated, convID: seedID},
		{name: "纯垃圾字节", state: garbage, convID: seedID},
		{name: "对不上的 conversation_id", state: state, convID: newSpikeConversationID("inv-other")},
	}

	for _, tc := range cases {
		t.Logf("---- 变体：%s ----", tc.name)
		func() {
			defer func() {
				// 上游可能直接把流打死，别让一个变体带走整个用例。
				if r := recover(); r != nil {
					t.Logf("panic: %v", r)
				}
			}()
			session := runSpikeTurn(t, ctx, spikeTurn{
				Prompt:         "我刚才让你记住的暗号是什么？原样复述。",
				ConversationID: tc.convID,
				State:          tc.state,
				Observe:        60 * time.Second,
			})
			t.Logf("turn_ended=%v 正文=%q", session.turnEnded(), session.text())
			t.Logf("记住了暗号=%v", strings.Contains(session.text(), checkpointSecret))
			for _, frame := range session.frames {
				if frame.Kind == "end_stream" {
					t.Logf("收尾: %s", frame.Detail)
				}
			}
		}()
	}

	t.Logf("---- 结论 ----")
	t.Logf("看上面三个变体各自的收尾形态：")
	t.Logf("  报明确错误 -> 网关可以捕获后原地退回全量重放")
	t.Logf("  静默忽略   -> 相当于冷启动，只亏钱不出错，可接受")
	t.Logf("  挂死       -> 必须靠本地 TTL 保守过期，不能指望上游告诉我们")
}

// ---------------------------------------------------------------------------
// S5：checkpoint 跨账号
// ---------------------------------------------------------------------------

// TestLiveSpikeCheckpointCrossAccount 验证 checkpoint 是不是绑在账号上。
//
// 网关是多账号池，failover 随时可能把下一轮切到别的号上。如果 checkpoint 能跨号
// 用，那 failover 只影响计费归属；如果不能（预期如此），那 binding 里就必须存
// accountID，且一旦换号必须无条件作废、退回全量重放。
func TestLiveSpikeCheckpointCrossAccount(t *testing.T) {
	t.Logf("=== S5：checkpoint 换个账号还能不能用 ===")
	second := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN_2"))
	if second == "" {
		t.Skip("CURSOR_ACCESS_TOKEN_2 is not set")
	}
	if !IsSessionToken(second) {
		t.Fatalf("CURSOR_ACCESS_TOKEN_2 不是 session 类型")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	conversationID := newSpikeConversationID("cross")
	first := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         "请记住这个暗号：" + checkpointSecret + "。只回复 ok。",
		ConversationID: conversationID,
		Observe:        90 * time.Second,
	})
	state := first.lastCheckpoint()
	if len(state) == 0 {
		t.Skip("A 号没拿到 checkpoint")
	}

	crossed := runSpikeTurn(t, ctx, spikeTurn{
		Prompt:         "我刚才让你记住的暗号是什么？原样复述。",
		ConversationID: conversationID,
		State:          state,
		Token:          second,
		Observe:        90 * time.Second,
	})

	t.Logf("---- 结论 ----")
	t.Logf("B 号带 A 号的 checkpoint：turn_ended=%v 正文=%q", crossed.turnEnded(), crossed.text())
	if strings.Contains(crossed.text(), checkpointSecret) {
		t.Logf("*** checkpoint 可跨账号复用，failover 时不必作废（但计费归属要想清楚）***")
	} else {
		t.Logf("*** checkpoint 与账号绑定：binding 必须存 accountID，换号一律退回全量重放 ***")
	}
}

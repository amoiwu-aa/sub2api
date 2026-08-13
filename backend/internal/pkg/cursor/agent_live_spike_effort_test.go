//go:build livespike

package cursor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// S6：effort 档位的现场取证。
//
// 背景：Grok 4.6 官方文档已公开 low / medium / high / xhigh 四档，RingStar
// 也已把它们映射到 RequestedModel.params。这个用例保留为私有 Agent wire 的
// 现场回归，确认 Cursor 上游没有更改参数编码或套餐限制。
//
// 反代参考实现仍只出现过 "high"，因此正式发布只允许官方列出的档位；最后一轮
// 继续发送一个伪值，用来观察上游究竟拒绝还是静默忽略。
//
// 本用例对每个档位实打一轮同样的题，观察三件事：
//
//	1. 这一轮能不能正常收尾（turn_ended，且没有 end_stream ERROR）
//	2. 推理量是否随档位下降（thinking 字符数 + wire 上游自报的 token 计数）
//	3. 答案是否还正确——降档不能把可用性一起降没
//
// 最后打一个上游肯定不认识的值，看它是拒绝还是忽略。这决定校验要写多严：
// 拒绝的话白名单可以宽松（错了会立刻暴露），忽略的话必须严格（错了会静默失效）。
//
// 跑法（需要 session 态 token，会真花钱，但每轮都是小请求）：
//
//	GOOS=linux GOARCH=amd64 go test -tags=livespike -c -o cursor-spike ./internal/pkg/cursor
//	CURSOR_ACCESS_TOKEN=<session-jwt> ./cursor-spike -test.run TestLiveSpikeModelEffort -test.v -test.timeout 20m
//
// 环境变量：
//
//	SPIKE_EFFORTS       选填，逗号分隔的档位列表，默认 xhigh,high,medium,low
//	SPIKE_EFFORT_MODEL  选填，打哪个模型，默认 cursor/grok-4.6（Auto 不吃 params，验不了）
//	SPIKE_EFFORT_BOGUS  选填，设为 0 可跳过「乱填一个值」那一轮

// effortProbePrompt 要满足两个矛盾的要求：便宜（输出短）、又真的需要推理
// （否则各档位都是零思考，量不出差异）。鸡兔同笼正好——答案是一个数字，
// 但要解一个二元一次方程。
const effortProbePrompt = "笼子里有鸡和兔共 35 只，脚共 94 只。鸡有几只？只回答数字，不要解释。"

// effortProbeAnswer 是正确答案，用来确认降档没有把正确率降没。
const effortProbeAnswer = "23"

// effortObservation 是一轮跑完之后的观测结果。
type effortObservation struct {
	Label         string
	TurnEnded     bool
	StreamError   string
	Text          string
	ThinkingChars int
	Elapsed       time.Duration
	Counters      string
}

// effortRound 是一次探测：打哪个模型、带什么 params。
type effortRound struct {
	Label   string
	ModelID string
	Params  []ModelParam
}

func TestLiveSpikeModelEffort(t *testing.T) {
	spikeToken(t)

	modelID := spikeEnvString("SPIKE_EFFORT_MODEL", "cursor/grok-4.6")
	selection := ResolveModel(modelID)
	if selection.ModelID == AutoModelID {
		t.Fatalf("SPIKE_EFFORT_MODEL=%q 解析成了 Auto；Auto 不带 params，量不出 effort 的作用", modelID)
	}

	// 第一轮永远是 Auto 基线，而且必须跑在最前面。
	//
	// 具名模型报 resource_exhausted 时有两种完全不同的原因：这个账号已经彻底
	// 不能用了，还是只有「包含的 API 用量」那一档耗尽、Auto 仍有余量。少了这一轮
	// 就分不开，会把「账号没额度」误读成「上游不认这个 effort 值」。
	// Auto 不吃 params，所以它只验通路与额度，不参与推理量对比。
	rounds := []effortRound{{
		Label:   "auto-baseline(空 params)",
		ModelID: AutoModelID,
		Params:  []ModelParam{},
	}}

	efforts := splitEfforts(spikeEnvString("SPIKE_EFFORTS", "xhigh,high,medium,low"))
	if spikeEnvString("SPIKE_EFFORT_BOGUS", "1") != "0" {
		// 放在最后：万一它把账号打出问题，前面几轮的数据已经拿到了。
		efforts = append(efforts, "ringstar-not-a-real-effort")
	}
	for _, effort := range efforts {
		rounds = append(rounds, effortRound{
			Label:   selection.ModelID + " effort=" + effort,
			ModelID: selection.ModelID,
			// fast 保持 true，与 DefaultModelParams 一致：只想动 effort 一个变量。
			Params: []ModelParam{{ID: "effort", Value: effort}, {ID: "fast", Value: "true"}},
		})
	}

	t.Logf("=== S6：effort 档位取证（具名模型=%s，共 %d 轮）===", selection.ModelID, len(rounds))
	t.Logf("题目: %s（正确答案 %s）", effortProbePrompt, effortProbeAnswer)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	observations := make([]effortObservation, 0, len(rounds))
	for _, round := range rounds {
		observations = append(observations, probeEffort(t, ctx, round))
	}

	t.Logf("")
	t.Logf("---- 结论 ----")
	t.Logf("%-34s %-7s %-9s %-8s %s", "轮次", "收尾", "thinking", "耗时", "回答 / 错误")
	for _, obs := range observations {
		outcome := obs.Text
		if obs.StreamError != "" {
			outcome = "ERROR: " + obs.StreamError
		}
		t.Logf("%-34s %-7v %-9d %-7.1fs %s",
			obs.Label, obs.TurnEnded, obs.ThinkingChars, obs.Elapsed.Seconds(), truncate(outcome, 60))
		if obs.Counters != "" {
			t.Logf("%-34s   上游自报计数: %s", "", obs.Counters)
		}
	}

	summarizeEffortProbe(t, observations)
}

// probeEffort 打一轮，其余参数与生产路径保持一致。
func probeEffort(t *testing.T, ctx context.Context, round effortRound) effortObservation {
	t.Helper()
	t.Logf("")
	t.Logf("=== %s ===", round.Label)

	conversationID := newSpikeConversationID("effort-" + sanitizeTag(round.Label))
	body, err := EncodeRunRequest(RunRequestInput{
		Text:           effortProbePrompt,
		ConversationID: conversationID,
		RequestContext: EncodeRequestContext(DefaultRequestContextEnv("spike")),
		ModelID:        round.ModelID,
		ModelParams:    round.Params,
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}

	stream, err := openAgentStream(ctx, spikeOptions(t), conversationID)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	session := &spikeSession{t: t, stream: stream, start: time.Now()}
	if err := stream.send(body); err != nil {
		t.Fatalf("send run request: %v", err)
	}
	stream.startHeartbeat(ctx)

	session.pump(3*time.Minute, false, func(message ServerMessage, _ []byte) bool {
		return message.Kind == KindTurnEnded
	})

	obs := effortObservation{
		Label:         round.Label,
		TurnEnded:     session.turnEnded(),
		Text:          strings.TrimSpace(session.text()),
		ThinkingChars: session.thinkingChars(),
		Elapsed:       time.Since(session.start),
		StreamError:   session.streamError(),
		Counters:      session.wireCounters(),
	}
	t.Logf("收尾=%v 帧谱: %s", obs.TurnEnded, session.frameInventory())
	if obs.StreamError != "" {
		if raw := session.endStreamPayload(); raw != "" {
			t.Logf("end_stream 原始载荷: %s", raw)
		}
	}
	return obs
}

// summarizeEffortProbe 把观测翻译成「代码该怎么写」的结论。
func summarizeEffortProbe(t *testing.T, observations []effortObservation) {
	t.Helper()
	t.Logf("")

	var autoBaseline, highBaseline *effortObservation
	accepted := make([]string, 0, len(observations))
	rejected := make([]string, 0, len(observations))
	for i := range observations {
		obs := &observations[i]
		if obs.StreamError != "" || !obs.TurnEnded {
			rejected = append(rejected, obs.Label)
		} else {
			accepted = append(accepted, obs.Label)
		}
		switch {
		case strings.HasPrefix(obs.Label, "auto-baseline"):
			autoBaseline = obs
		case strings.HasSuffix(obs.Label, "effort=high"):
			highBaseline = obs
		}
	}

	t.Logf("成功收尾: %v", accepted)
	t.Logf("失败: %v", rejected)

	// 先判「账号还能不能用」，再判「effort 认不认」。顺序反了会把额度问题误读成协议问题。
	if autoBaseline != nil && (autoBaseline.StreamError != "" || !autoBaseline.TurnEnded) {
		t.Logf("*** Auto 基线都跑不通，这个账号当前整体不可用；effort 的结论无从谈起 ***")
		t.Logf("    错误: %s", autoBaseline.StreamError)
		return
	}
	if highBaseline != nil && (highBaseline.StreamError != "" || !highBaseline.TurnEnded) {
		t.Logf("*** Auto 能跑但具名模型不能：这是「包含的 API 用量」那一档耗尽，不是 effort 值的问题 ***")
		t.Logf("    具名模型错误: %s", highBaseline.StreamError)
		t.Logf("    换一个具名模型额度未耗尽的账号再跑，才能得到 effort 的结论。")
		return
	}
	if highBaseline == nil || highBaseline.ThinkingChars == 0 {
		t.Logf("*** 没有可用的 high 基线（或 high 本轮没产生 thinking），推理量对比无意义 ***")
		return
	}

	for i := range observations {
		obs := &observations[i]
		if obs == highBaseline || obs == autoBaseline || obs.StreamError != "" {
			continue
		}
		delta := float64(obs.ThinkingChars-highBaseline.ThinkingChars) / float64(highBaseline.ThinkingChars) * 100
		t.Logf("%-34s 相对 high 的推理量变化 %+.0f%%（%d → %d 字符）",
			obs.Label, delta, highBaseline.ThinkingChars, obs.ThinkingChars)
	}

	t.Logf("")
	t.Logf("判读：只有当低档位既被接受、推理量又确实下降时，把 effort 做成可配才有意义。")
	t.Logf("     若低档位被接受但推理量不变，说明上游忽略了它——那开关是假的，不要做。")
	t.Logf("     若乱填的值也被接受，说明上游不校验，网关这边必须自己守白名单。")
}

// ---------------------------------------------------------------------------
// 观测辅助
// ---------------------------------------------------------------------------

// thinkingChars 返回这一轮累计的推理文本长度。
//
// 用字符数而不是 token 数：上游只在收尾帧里给一个总数，而 thinking_delta 是
// 逐块到的，字符数任何时候都能算，跨档位对比也够用。
func (s *spikeSession) thinkingChars() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len([]rune(s.thinkBuf.String()))
}

// streamError 返回 end_stream 上带的错误，没有则为空。
// effort 值不被接受时，上游多半就是在这里报错而不是安静收尾。
func (s *spikeSession) streamError() string {
	for _, frame := range s.frames {
		if frame.Kind == "end_stream" && strings.Contains(frame.Detail, "ERROR") {
			return frame.Detail
		}
	}
	return ""
}

// endStreamPayload 返回结束帧的原始 JSON。
//
// ConnectError 只解 code 与 message，上游把 message 写成 "Error" 时那两个字段
// 什么也说明不了。真要定位「到底是哪一档额度耗尽」，只能看完整载荷。
func (s *spikeSession) endStreamPayload() string {
	for _, frame := range s.frames {
		if frame.Kind == "end_stream" && len(frame.Raw) > 0 {
			return string(frame.Raw)
		}
	}
	return ""
}

// wireCounters 捞出上游自报的 token 计数。
//
// 上一轮 checkpoint 取证时发现：other 帧根上的 #5 varint 与 thinking 节点内部
// 记的 token 数完全一致，#17 是一对 {#1,#2} 计数。仓库现在用 EstimateTokens
// 按字符估算，这些才是上游的真值——顺手记下来，effort 的效果用它衡量比字符数准。
func (s *spikeSession) wireCounters() string {
	parts := make([]string, 0, 2)
	for _, frame := range s.frames {
		if len(frame.Raw) == 0 {
			continue
		}
		fields, err := ReadFields(frame.Raw)
		if err != nil {
			continue
		}
		for _, field := range fields {
			switch {
			case field.Number == 5 && field.WireType == wireVarint:
				parts = append(parts, fmt.Sprintf("#5=%d", field.Varint))
			case field.Number == 17 && field.WireType == wireBytes:
				if inner, err := ReadFields(field.Bytes); err == nil {
					pair := make([]string, 0, len(inner))
					for _, f := range inner {
						if f.WireType == wireVarint {
							pair = append(pair, fmt.Sprintf("#%d=%d", f.Number, f.Varint))
						}
					}
					if len(pair) > 0 {
						parts = append(parts, "#17{"+strings.Join(pair, " ")+"}")
					}
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

func spikeEnvString(key, fallback string) string {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		return raw
	}
	return fallback
}

func splitEfforts(raw string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// sanitizeTag 把 effort 值压成能安全塞进 conversation_id 的形状。
func sanitizeTag(raw string) string {
	var sb strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r + 32)
		default:
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

func truncate(s string, limit int) string {
	runes := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…(+" + strconv.Itoa(len(runes)-limit) + ")"
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// kiroUpstreamTimeout 覆盖整个流式会话。上游是长连接，超时要给足。
	kiroUpstreamTimeout = 15 * time.Minute
	// kiroResponseHeaderTimeout 只限制首字节前的等待。
	kiroResponseHeaderTimeout = 90 * time.Second

	// kiroContextOverflowRatio 是超限拦截的触发比例。
	//
	// 本地估算是「字符数 / 4」的粗糙口径，中文、代码、工具定义都会让它偏离
	// 真实 token 数，偏离方向还不固定。所以只在明显超出时才拦——留 15% 余量，
	// 宁可放过几个临界请求让上游去判，也不要把本来能跑的请求误杀。
	kiroContextOverflowRatio = 1.15
)

// KiroGatewayService 把 Anthropic Messages API 桥接到 Amazon Q。
//
// 它只负责「单次账号尝试」：选号、并发、计费、failover、用量落库都由
// gateway_handler 的公共链路完成，与 Antigravity 的 Forward 契约一致。
type KiroGatewayService struct {
	tokenProvider    *KiroTokenProvider
	rateLimitService *RateLimitService
	catalog          kiroCatalogCache
}

func NewKiroGatewayService(tokenProvider *KiroTokenProvider, rateLimitService *RateLimitService) *KiroGatewayService {
	return &KiroGatewayService{tokenProvider: tokenProvider, rateLimitService: rateLimitService}
}

// reportUpstreamError 把上游故障喂给账号健康度体系。
//
// 少了这一步，kiro 账号永远不会被标记限流/不健康/自动禁用：调度器会一直把请求
// 发给一个已经 429 的账号，管理员配置的错误码规则对它也完全不生效。
// 其他平台走 gateway_upstream_response.go 的 handleFailoverSideEffects，
// 自有上游桥不经过那条链路，只能自己上报。
func (s *KiroGatewayService) reportUpstreamError(ctx context.Context, account *Account, status int, body []byte, model string) {
	if s.rateLimitService == nil || account == nil {
		return
	}
	s.rateLimitService.HandleUpstreamError(ctx, account, status, nil, body, model)
}

// Forward 执行一次 kiro 上游调用并把响应写给客户端。
//
// 鉴权失败时会作废缓存 token 后重试一次——上游可能在 TTL 到期前就作废了 token
// （别处重新登录 / IdC 会话被吊销），provider 只看过期时间是发现不了的。
func (s *KiroGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	writerSizeBefore := 0
	if c != nil {
		writerSizeBefore = c.Writer.Size()
	}
	result, err := s.forwardOnce(ctx, c, account, body)
	if shouldRetryNativeBridgeAuth(ctx, c, writerSizeBefore, err, func(retryCtx context.Context) error {
		return s.tokenProvider.InvalidateToken(retryCtx, account)
	}, "kiro_gateway", accountIDOrZero(account)) {
		return s.forwardOnce(markNativeBridgeAuthRetried(ctx), c, account, body)
	}
	return result, err
}

func (s *KiroGatewayService) forwardOnce(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()

	var req kiro.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}

	publicModel := req.Model
	upstreamModel := kiro.UpstreamModelID(publicModel)

	client, err := s.buildClient(ctx, account)
	if err != nil {
		return nil, err
	}

	conversationState, err := kiro.BuildConversationState(&req, uuid.NewString(), upstreamModel)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	// 前置校验：明知会失败的请求就别发了，省一次调用和一轮 failover。
	// 拿不到目录时什么都不做——这只是优化，不能因为目录缺失就拦请求。
	if msg := s.preflightReject(ctx, account, client, conversationState, upstreamModel); msg != "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", msg)
	}

	upstreamCtx, cancel := context.WithTimeout(ctx, kiroUpstreamTimeout)
	defer cancel()

	if req.Stream {
		return s.forwardStreaming(upstreamCtx, c, account, client, conversationState, publicModel, upstreamModel, startTime)
	}
	return s.forwardBuffered(upstreamCtx, c, account, client, conversationState, publicModel, upstreamModel, startTime)
}

// preflightReject 在发请求前判断这次调用是不是注定失败。
// 返回非空字符串表示应当直接拒绝，内容是给客户端看的原因。
//
// 目前只拦一种确定失败的组合：纯文本模型收到了图片。实测 minimax-m2.5 与
// glm-5 的 supportedInputTypes 只有 TEXT，带图片发过去必然报错。
//
// 这里刻意保守：目录拿不到、模型不在目录里、或者判断不出来，一律放行让
// 上游去判。误拦一个本来能用的请求，比多发一个失败请求糟糕得多。
func (s *KiroGatewayService) preflightReject(
	ctx context.Context, account *Account, client *kiro.Client,
	state *kiro.ConversationState, upstreamModel string,
) string {
	hasImages := state.HasImages()
	estimatedTokens := int64(kiro.EstimateConversationTokens(state))

	// 闭包在后台 goroutine 里执行，触发它的请求那时可能早已结束。
	// client 为 nil 时只是拉不了新目录，已缓存的仍然可用——所以是在闭包里
	// 返回错误，而不是在函数开头短路：后者会让「有缓存但没 client」的场景
	// 白白放弃已有的目录。
	catalog := s.catalog.Get(account, func(fetchCtx context.Context) ([]kiro.AvailableModel, error) {
		if client == nil {
			return nil, errors.New("kiro client is nil")
		}
		models, _, err := client.ListAvailableModels(fetchCtx)
		return models, err
	})
	if catalog == nil {
		return ""
	}
	if _, known := catalog.Lookup(upstreamModel); !known {
		return ""
	}

	if hasImages && !catalog.SupportsImages(upstreamModel) {
		msg := "Model " + upstreamModel + " does not accept image input"
		if alt := catalog.CheapestSupporting(true, 0); alt != "" {
			msg += "; try " + kiro.PublicModelID(alt)
		}
		slog.Info("kiro.preflight_rejected_image_on_text_only_model",
			"account_id", accountIDOrZero(account), "model", upstreamModel)
		return msg
	}

	if limit := catalog.MaxInputTokens(upstreamModel); limit > 0 &&
		estimatedTokens > int64(float64(limit)*kiroContextOverflowRatio) {
		msg := fmt.Sprintf("Request is too large for %s: about %d input tokens vs a %d limit",
			upstreamModel, estimatedTokens, limit)
		if alt := catalog.CheapestSupporting(hasImages, estimatedTokens); alt != "" {
			msg += "; try " + kiro.PublicModelID(alt)
		}
		slog.Info("kiro.preflight_rejected_context_overflow",
			"account_id", accountIDOrZero(account), "model", upstreamModel,
			"estimated_tokens", estimatedTokens, "limit", limit)
		return msg
	}

	_ = ctx
	return ""
}

// CatalogModels 返回该账号实时目录里的模型，目录未就绪时返回 nil。
//
// 可用模型随订阅档位变化（实测免费号 9 个、企业号 19 个），静态目录只是两档的
// 并集，拿它当 /v1/models 会把用户不能用的模型也摆出去——点名后上游回
// INVALID_MODEL_ID，而错误信息里看不出是模型名的问题。
//
// 不阻塞：首次调用必然返回 nil 并触发一次后台拉取，调用方要能退回静态目录。
// 这是 kiroCatalogCache 的既定契约——拉一次约 2 秒，不能放在请求路径上。
func (s *KiroGatewayService) CatalogModels(_ context.Context, account *Account) []kiro.Model {
	if s == nil || account == nil {
		return nil
	}
	catalog := s.catalog.Get(account, func(fetchCtx context.Context) ([]kiro.AvailableModel, error) {
		// 在闭包里建 client：只有真要刷新时才去取 token，命中缓存的请求不必付这个代价。
		client, err := s.buildClient(fetchCtx, account)
		if err != nil {
			return nil, err
		}
		models, _, err := client.ListAvailableModels(fetchCtx)
		return models, err
	})
	return kiro.ModelsFromCatalog(catalog)
}

func (s *KiroGatewayService) buildClient(ctx context.Context, account *Account) (*kiro.Client, error) {
	if s.tokenProvider == nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"type":"error","error":{"type":"api_error","message":"Kiro token provider is not configured"}}`),
		}
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"type":"error","error":{"type":"authentication_error","message":"Failed to get upstream access token"}}`),
			Stage:        GatewayFailureStageAccountAuth,
		}
	}

	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			// 与 token provider 同一条红线：宁可失败也不从服务器出口 IP 直连。
			return nil, &UpstreamFailoverError{
				StatusCode:   http.StatusBadGateway,
				ResponseBody: []byte(`{"type":"error","error":{"type":"api_error","message":"Configured proxy is unavailable"}}`),
				Stage:        GatewayFailureStageAccountAuth,
			}
		}
		proxyURL = account.Proxy.URL()
	}

	httpClient, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               kiroUpstreamTimeout,
		ResponseHeaderTimeout: kiroResponseHeaderTimeout,
	})
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"type":"error","error":{"type":"api_error","message":"Invalid proxy configuration"}}`),
			Stage:        GatewayFailureStageAccountAuth,
		}
	}

	creds := kiro.CredentialsFromMap(account.Credentials)
	creds.AccessToken = accessToken
	client, err := kiro.NewClient(httpClient, creds)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"type":"error","error":{"type":"api_error","message":"Kiro account credentials are incomplete"}}`),
			Stage:        GatewayFailureStageAccountAuth,
		}
	}
	return client, nil
}

func (s *KiroGatewayService) forwardStreaming(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	client *kiro.Client,
	state *kiro.ConversationState,
	publicModel, upstreamModel string,
	startTime time.Time,
) (*ForwardResult, error) {
	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	var (
		headersWritten bool
		firstTokenMs   *int
		writeErr       error
	)
	writeSSE := func(event kiro.SSEEvent) error {
		if !headersWritten {
			// 只有确认上游开始产出后才写响应头：在此之前失败仍可 failover。
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			c.Status(http.StatusOK)
			headersWritten = true
		}
		payload, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Event, payload); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	translator := kiro.NewResponseTranslator(messageID, publicModel, func(event kiro.SSEEvent) error {
		if firstTokenMs == nil && event.Event == "content_block_delta" {
			elapsed := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &elapsed
		}
		if err := writeSSE(event); err != nil {
			writeErr = err
			return err
		}
		return nil
	})

	// 这里刻意不预先调用 translator.Start()。
	//
	// message_start 一旦写出去，响应头就发给客户端了，此后任何上游故障都不能再换账号
	// 重来。而「连不上上游 / 401 / 上游 5xx」恰恰几乎都发生在第一个事件到达之前——
	// 那正是最该 failover 的时刻。translator 现在会在收到首个事件时自行触发 Start，
	// 所以在此之前失败仍然落在 headersWritten=false 的可 failover 窗口里。
	_, err := client.GenerateAssistantResponse(ctx, &kiro.GenerateAssistantResponseRequest{
		ConversationState: *state,
	}, translator.Handle)
	if err != nil {
		if writeErr != nil {
			// 客户端断开：不是上游故障，如实标记后照常结算已产生的用量。
			return s.buildResult(translator, state, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
		}
		return nil, s.upstreamError(ctx, c, account, publicModel, err, headersWritten)
	}

	if err := translator.Finish(); err != nil {
		return s.buildResult(translator, state, publicModel, upstreamModel, true, firstTokenMs, startTime, true), nil
	}
	return s.buildResult(translator, state, publicModel, upstreamModel, true, firstTokenMs, startTime, false), nil
}

func (s *KiroGatewayService) forwardBuffered(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	client *kiro.Client,
	state *kiro.ConversationState,
	publicModel, upstreamModel string,
	startTime time.Time,
) (*ForwardResult, error) {
	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	translator := kiro.NewResponseTranslator(messageID, publicModel, nil)

	_, err := client.GenerateAssistantResponse(ctx, &kiro.GenerateAssistantResponseRequest{
		ConversationState: *state,
	}, translator.Handle)
	if err != nil {
		return nil, s.upstreamError(ctx, c, account, publicModel, err, false)
	}
	if err := translator.Finish(); err != nil {
		return nil, s.upstreamError(ctx, c, account, publicModel, err, false)
	}

	c.JSON(http.StatusOK, translator.Response())
	return s.buildResult(translator, state, publicModel, upstreamModel, false, nil, startTime, false), nil
}

func (s *KiroGatewayService) buildResult(
	translator *kiro.ResponseTranslator,
	state *kiro.ConversationState,
	publicModel, upstreamModel string,
	stream bool,
	firstTokenMs *int,
	startTime time.Time,
	clientDisconnect bool,
) *ForwardResult {
	usage := translator.Usage()
	inputTokens := int(usage.InputTokens)
	outputTokens := int(usage.OutputTokens)

	// meteringEvent 里的 credit 是上游对本次请求的权威扣费口径（与 GetUsageLimits
	// 的 currentUsage 同源）。它和 token 不同量纲，不参与计费，但必须落库：
	// 免费档一个周期只有 50 credit 且用尽即断供，而我们的成本列走 Anthropic 价目表，
	// 实测与真实消耗相差 20 倍，光看成本回答不了「还能用多久」。
	metering := translator.MeteringUsage()

	// 上游的 tokenUsage 挂在 metadataEvent 上，而这个事件不保证出现（工具轮、
	// 被中断的轮、部分账号类型都可能整轮没有）。缺了它直接记 0 会让本次请求不计费，
	// 平台配额的 USD 限额也就永远触发不了——所以这里退到本地估算。
	// 上游给了数就以上游为准，估算只在缺失时兜底。
	if !translator.HasUpstreamUsage() {
		inputTokens = kiro.EstimateConversationTokens(state)
		outputTokens = translator.EstimatedOutputTokens()

		// 走到估算说明本次没有权威 token 数，计费完全靠猜。留一条日志，
		// 好回头拿 credit 核对估算跑偏了多少。
		if metering > 0 {
			logger.L().Debug("kiro.usage_estimated_with_upstream_metering",
				zap.String("model", publicModel),
				zap.String("upstream_model", upstreamModel),
				zap.Int("estimated_input_tokens", inputTokens),
				zap.Int("estimated_output_tokens", outputTokens),
				zap.Float64("upstream_metering_credits", metering),
			)
		}
	}

	result := &ForwardResult{
		Usage: ClaudeUsage{
			InputTokens:              inputTokens,
			OutputTokens:             outputTokens,
			CacheReadInputTokens:     int(usage.CacheReadInputTokens),
			CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
		},
		Model:            publicModel,
		Stream:           stream,
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ClientDisconnect: clientDisconnect,
		UpstreamCredits:  metering,
	}
	if upstreamModel != publicModel {
		result.UpstreamModel = upstreamModel
	}
	return result
}

// upstreamError 把上游故障翻译成 failover 错误。
// 响应头已写出时不能再 failover——客户端已经收到了流的开头。
func (s *KiroGatewayService) upstreamError(
	ctx context.Context, c *gin.Context, account *Account, model string, err error, headersWritten bool,
) error {
	status := http.StatusBadGateway
	message := "Kiro upstream request failed"

	var apiErr *kiro.APIError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		if apiErr.Unauthorized() {
			message = "Kiro credentials were rejected by the upstream"
		}
	}

	responseBody := kiroErrorBody(status, message)
	// 无论能不能 failover，账号健康度都要更新：429/5xx/401 决定它下一次还能不能被选中。
	s.reportUpstreamError(ctx, account, status, responseBody, model)
	if headersWritten {
		// 流已经开了，只能在流里补一个错误事件收尾。
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", responseBody)
		c.Writer.Flush()
		return errors.New(message)
	}
	return &UpstreamFailoverError{
		StatusCode:   status,
		ResponseBody: responseBody,
	}
}

func (s *KiroGatewayService) writeError(c *gin.Context, status int, errType, message string) error {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errType, "message": message}})
	return errors.New(message)
}

func kiroErrorBody(status int, message string) []byte {
	errType := "api_error"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		errType = "authentication_error"
	case status == http.StatusTooManyRequests:
		errType = "rate_limit_error"
	case status >= 400 && status < 500:
		errType = "invalid_request_error"
	}
	body, err := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errType, "message": message},
	})
	if err != nil {
		return []byte(`{"type":"error","error":{"type":"api_error","message":"Kiro upstream request failed"}}`)
	}
	return body
}

// NewTestClient 为后台「测试连接」构造一个上游客户端。
//
// 刻意复用 buildClient 这条生产路径：token provider（含刷新链）、
// 「配置了代理但代理缺失即硬失败」这条红线、超时设置全都一致。
// 另起一套测试专用逻辑的话，测过了也不能说明生产路径能走通。
func (s *KiroGatewayService) NewTestClient(ctx context.Context, account *Account) (*kiro.Client, error) {
	return s.buildClient(ctx, account)
}

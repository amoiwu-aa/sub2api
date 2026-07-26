package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

const (
	generateAssistantResponsePath = "/generateAssistantResponse"
	listAvailableModelsPath       = "/ListAvailableModels"

	// AgentModeVibe 是 Kiro IDE 默认的 agent 模式。
	// 它在 schema 里绑定到 x-amzn-kiro-agent-mode 请求头，不是 body 字段。
	AgentModeVibe = "vibe"

	headerAgentMode      = "x-amzn-kiro-agent-mode"
	headerOptOut         = "x-amzn-codewhisperer-optout"
	headerTokenType      = "TokenType"
	headerRedirectIntern = "redirect-for-internal"
	headerConversationID = "x-amzn-codewhisperer-conversation-id"

	// tokenTypeExternalIDP 只在 external_idp（企业自建 IdP）账号上发送。
	//
	// 早先这里按 IdC 发，等于向上游谎报 token 来源：实测在一个 social 账号上
	// 加这个头，/getUsageLimits 直接从 200 变 403（x-amzn-errortype:
	// InternalFailure），而换成未知取值则被忽略——说明上游专门识别这个值，
	// 会按外部 IdP 的路径去校验 token。BuilderId / Enterprise 的 token 同样
	// 不是外部 IdP 签发的，发了只会把账号打死。
	tokenTypeExternalIDP = "EXTERNAL_IDP"

	maxErrorBody      = 1 << 20
	maxListModelsBody = 4 << 20
	maxListModelsPage = 20
)

// APIError 是 Q 上游返回的非 2xx 响应。
type APIError struct {
	Status    int
	Operation string
	Body      string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 1024 {
		body = body[:1024]
	}
	return fmt.Sprintf("kiro %s failed (HTTP %d): %s", e.Operation, e.Status, body)
}

// Unauthorized 报告是否为凭证问题；调用方据此决定刷新后重试一次。
func (e *APIError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// Client 是 Amazon Q 的最小客户端。
//
// 上游用 bearer token 而不是 SigV4，所以不需要 AWS 签名器；流式响应是标准的
// application/vnd.amazon.eventstream，用 aws-sdk-go-v2 的解码器解帧即可。
type Client struct {
	httpClient  HTTPClient
	accessToken string
	endpoint    string
	profileARN  string
	userAgent   string
	authMethod  string
	internal    bool
}

// NewClient 用给定凭证构造客户端。httpClient 必须由调用方注入（账号代理）。
func NewClient(httpClient HTTPClient, creds *Credentials) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("kiro http client is nil")
	}
	if creds == nil {
		return nil, errors.New("kiro credentials are nil")
	}
	if strings.TrimSpace(creds.AccessToken) == "" {
		return nil, ErrAccessTokenMissing
	}
	if strings.TrimSpace(creds.ProfileARN) == "" {
		return nil, ErrProfileARNMissing
	}
	endpoint, err := QEndpoint(creds.QRegion())
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient:  httpClient,
		accessToken: creds.AccessToken,
		endpoint:    endpoint,
		profileARN:  creds.ProfileARN,
		userAgent:   creds.UserAgent(),
		authMethod:  creds.AuthMethod,
		internal:    creds.IsInternalProvider(),
	}, nil
}

// ProfileARN 暴露给调用方用于构造请求。
func (c *Client) ProfileARN() string { return c.profileARN }

func (c *Client) applyCommonHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set(headerOptOut, "true")
	if c.authMethod == AuthMethodExternalIDP {
		req.Header.Set(headerTokenType, tokenTypeExternalIDP)
	}
	if c.internal {
		req.Header.Set(headerRedirectIntern, "true")
	}
}

// StreamEvent 是解码后的单个上游事件。Kind 取 Event* 常量之一；
// 未建模的事件类型会被跳过而不是报错，避免上游加字段时整条流失败。
type StreamEvent struct {
	Kind string

	AssistantResponse *AssistantResponseEvent
	ReasoningContent  *ReasoningContentEvent
	ToolUse           *ToolUseEvent
	Metering          *MeteringEvent
	Metadata          *MetadataEvent
	ContextUsage      *ContextUsageEvent
	InvalidState      *InvalidStateEvent
}

// GenerateAssistantResponse 发起一次对话并把事件逐个交给 handle。
//
// handle 返回错误会中止读取并向上传播；调用方据此实现客户端断开的提前退出。
// 返回的 conversationID 来自响应头，便于日志关联。
func (c *Client) GenerateAssistantResponse(
	ctx context.Context,
	request *GenerateAssistantResponseRequest,
	handle func(StreamEvent) error,
) (conversationID string, err error) {
	if request == nil {
		return "", errors.New("kiro request is nil")
	}
	if request.ProfileARN == "" {
		request.ProfileARN = c.profileARN
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode kiro request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+generateAssistantResponsePath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build kiro request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	req.Header.Set(headerAgentMode, AgentModeVibe)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("kiro request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	conversationID = resp.Header.Get(headerConversationID)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return conversationID, &APIError{Status: resp.StatusCode, Operation: "GenerateAssistantResponse", Body: string(raw)}
	}

	if err := decodeEventStream(resp.Body, handle); err != nil {
		return conversationID, err
	}
	return conversationID, nil
}

// decodeEventStream 解 application/vnd.amazon.eventstream 帧并分发事件。
func decodeEventStream(body io.Reader, handle func(StreamEvent) error) error {
	decoder := eventstream.NewDecoder()
	// 解码器需要一份可复用的缓冲区；给 0 长度切片让它按需增长。
	var buf []byte

	for {
		message, err := decoder.Decode(body, buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode kiro event stream: %w", err)
		}
		buf = message.Payload[:0]

		event, ok, err := parseEventMessage(message)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if handle != nil {
			if err := handle(event); err != nil {
				return err
			}
		}
	}
}

func parseEventMessage(message eventstream.Message) (StreamEvent, bool, error) {
	var messageType, eventType, exceptionType string
	for _, header := range message.Headers {
		switch header.Name {
		case ":message-type":
			messageType = header.Value.String()
		case ":event-type":
			eventType = header.Value.String()
		case ":exception-type":
			exceptionType = header.Value.String()
		}
	}

	switch messageType {
	case "exception":
		return StreamEvent{}, false, &APIError{
			Status:    http.StatusBadGateway,
			Operation: "GenerateAssistantResponse/" + exceptionType,
			Body:      string(message.Payload),
		}
	case "error":
		return StreamEvent{}, false, &APIError{
			Status:    http.StatusBadGateway,
			Operation: "GenerateAssistantResponse",
			Body:      string(message.Payload),
		}
	}

	event := StreamEvent{Kind: eventType}
	// 未建模的事件类型直接跳过：上游新增事件不应该让整条流失败。
	switch eventType {
	case EventAssistantResponse:
		event.AssistantResponse = &AssistantResponseEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.AssistantResponse)
	case EventReasoningContent:
		event.ReasoningContent = &ReasoningContentEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.ReasoningContent)
	case EventToolUse:
		event.ToolUse = &ToolUseEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.ToolUse)
	case EventMetering:
		event.Metering = &MeteringEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.Metering)
	case EventMetadata:
		event.Metadata = &MetadataEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.Metadata)
	case EventContextUsage:
		event.ContextUsage = &ContextUsageEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.ContextUsage)
	case EventInvalidState:
		event.InvalidState = &InvalidStateEvent{}
		return event, true, unmarshalEvent(message.Payload, eventType, event.InvalidState)
	default:
		return StreamEvent{}, false, nil
	}
}

func unmarshalEvent(payload []byte, eventType string, target any) error {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode kiro %s payload: %w", eventType, err)
	}
	return nil
}

// AvailableModel 是 ListAvailableModels 返回的单个模型。
type AvailableModel struct {
	ModelID     string `json:"modelId"`
	ModelName   string `json:"modelName,omitempty"`
	Description string `json:"description,omitempty"`
}

type listAvailableModelsResponse struct {
	Models       []AvailableModel `json:"models"`
	DefaultModel *AvailableModel  `json:"defaultModel,omitempty"`
	NextToken    string           `json:"nextToken,omitempty"`
}

// ListAvailableModels 拉取账号可用的模型目录（自动翻页）。
func (c *Client) ListAvailableModels(ctx context.Context) ([]AvailableModel, *AvailableModel, error) {
	var (
		all          []AvailableModel
		defaultModel *AvailableModel
		nextToken    string
	)

	for page := 0; page < maxListModelsPage; page++ {
		query := url.Values{}
		query.Set("origin", OriginAIEditor)
		if c.profileARN != "" {
			query.Set("profileArn", c.profileARN)
		}
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+listAvailableModelsPath+"?"+query.Encode(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("build kiro list models request: %w", err)
		}
		c.applyCommonHeaders(req)
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("kiro list models request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxListModelsBody))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, &APIError{Status: resp.StatusCode, Operation: "ListAvailableModels", Body: string(raw)}
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("read kiro list models response: %w", readErr)
		}

		var parsed listAvailableModelsResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, nil, fmt.Errorf("decode kiro list models response: %w", err)
		}
		all = append(all, parsed.Models...)
		if defaultModel == nil && parsed.DefaultModel != nil {
			defaultModel = parsed.DefaultModel
		}
		nextToken = strings.TrimSpace(parsed.NextToken)
		if nextToken == "" {
			return all, defaultModel, nil
		}
	}
	// 翻页上限是防御性的：上游若返回环形 nextToken，不能让这里无限循环。
	return all, defaultModel, nil
}

package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/stretchr/testify/require"
)

func testCredentials() *Credentials {
	return &Credentials{
		AccessToken: "access-token",
		AuthMethod:  AuthMethodSocial,
		ProfileARN:  "arn:aws:codewhisperer:eu-west-1:123456789012:profile/ABC",
		MachineID:   "machine-1",
		KiroVersion: "0.11.107",
	}
}

// encodeEventStream 用 AWS 官方编码器造帧，确保测试里的字节流与上游同构。
func encodeEventStream(t *testing.T, frames ...eventstream.Message) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, frame := range frames {
		require.NoError(t, encoder.Encode(&buf, frame))
	}
	return buf.Bytes()
}

func eventFrame(t *testing.T, eventType string, payload any) eventstream.Message {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue(eventType)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: raw,
	}
}

func streamResponse(t *testing.T, frames ...eventstream.Message) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(encodeEventStream(t, frames...))),
		Header: http.Header{
			"Content-Type": []string{"application/vnd.amazon.eventstream"},
			http.CanonicalHeaderKey(headerConversationID): []string{"conv-123"},
		},
	}
}

func TestNewClientDerivesEndpointFromProfileARN(t *testing.T) {
	client, err := NewClient(&stubHTTPClient{}, testCredentials())
	require.NoError(t, err)
	require.Equal(t, "https://q.eu-west-1.amazonaws.com", client.endpoint)
}

func TestNewClientRequiresInjectedHTTPClient(t *testing.T) {
	// 包内不得自建 client：账号代理必须由调用方注入。
	_, err := NewClient(nil, testCredentials())
	require.ErrorContains(t, err, "http client is nil")
}

func TestNewClientRequiresProfileARN(t *testing.T) {
	creds := testCredentials()
	creds.ProfileARN = ""
	_, err := NewClient(&stubHTTPClient{}, creds)
	require.ErrorIs(t, err, ErrProfileARNMissing)
}

func TestGenerateAssistantResponseSendsModeledRequest(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t)}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{
		ConversationState: ConversationState{
			ConversationID:  "conv-1",
			ChatTriggerType: ChatTriggerTypeManual,
			CurrentMessage: ChatMessage{UserInputMessage: &UserInputMessage{
				Content: "hello",
				Origin:  OriginAIEditor,
				ModelID: "claude-sonnet-4.6",
			}},
		},
	}, nil)
	require.NoError(t, err)

	req := client.requests[0]
	require.Equal(t, "https://q.eu-west-1.amazonaws.com/generateAssistantResponse", req.URL.String())
	require.Equal(t, "Bearer access-token", req.Header.Get("Authorization"))
	require.Equal(t, "KiroIDE 0.11.107 machine-1", req.Header.Get("User-Agent"))
	require.Equal(t, "true", req.Header.Get(headerOptOut))
	require.Equal(t, AgentModeVibe, req.Header.Get(headerAgentMode))
	// Social 账号不该带 TokenType 头。
	require.Empty(t, req.Header.Get(headerTokenType))
	require.Empty(t, req.Header.Get(headerRedirectIntern))

	var sent GenerateAssistantResponseRequest
	require.NoError(t, json.Unmarshal([]byte(client.bodies[0]), &sent))
	// profileArn 未显式给出时由 client 补上。
	require.Equal(t, "arn:aws:codewhisperer:eu-west-1:123456789012:profile/ABC", sent.ProfileARN)
	// modelId 必须落在 userInputMessage 上，而不是 userInputMessageContext。
	require.Equal(t, "claude-sonnet-4.6", sent.ConversationState.CurrentMessage.UserInputMessage.ModelID)
	require.NotContains(t, client.bodies[0], `"userInputMessageContext":{"modelId"`)
}

// TestGenerateAssistantResponseIdCAndInternalHeaders 覆盖 IdC + Internal 账号。
//
// IdC 账号绝不能发 TokenType：实测在真账号上加这个头，上游会从 200 变 403
// （x-amzn-errortype: InternalFailure），而换成未知取值则被忽略——说明上游
// 专门识别 EXTERNAL_IDP，会按外部 IdP 的路径去校验 token。
// BuilderId / Enterprise 的 token 不是外部 IdP 签发的，发了就是把账号打死。
func TestGenerateAssistantResponseIdCAndInternalHeaders(t *testing.T) {
	creds := testCredentials()
	creds.AuthMethod = AuthMethodIdC
	creds.Provider = ProviderInternal

	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t)}}
	qClient, err := NewClient(client, creds)
	require.NoError(t, err)

	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, nil)
	require.NoError(t, err)

	require.Empty(t, client.requests[0].Header.Get(headerTokenType))
	require.Equal(t, "true", client.requests[0].Header.Get(headerRedirectIntern))
}

// TestGenerateAssistantResponseExternalIdpSendsTokenType 确认只有 external_idp
// 这一种 authMethod 才发 TokenType。
func TestGenerateAssistantResponseExternalIdpSendsTokenType(t *testing.T) {
	creds := testCredentials()
	creds.AuthMethod = AuthMethodExternalIDP

	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t)}}
	qClient, err := NewClient(client, creds)
	require.NoError(t, err)

	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, nil)
	require.NoError(t, err)

	require.Equal(t, tokenTypeExternalIDP, client.requests[0].Header.Get(headerTokenType))
}

// TestSocialAccountNeverSendsTokenType 守住 social 这条最常用的路径。
func TestSocialAccountNeverSendsTokenType(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t)}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, nil)
	require.NoError(t, err)

	require.Empty(t, client.requests[0].Header.Get(headerTokenType))
}

func TestGenerateAssistantResponseDecodesEventStream(t *testing.T) {
	frames := []eventstream.Message{
		eventFrame(t, EventReasoningContent, ReasoningContentEvent{Text: "thinking..."}),
		eventFrame(t, EventReasoningContent, ReasoningContentEvent{Signature: "sig-1"}),
		eventFrame(t, EventAssistantResponse, AssistantResponseEvent{Content: "Hello", ModelID: "claude-sonnet-4.6"}),
		eventFrame(t, EventAssistantResponse, AssistantResponseEvent{Content: " world"}),
		eventFrame(t, EventToolUse, ToolUseEvent{ToolUseID: "tu-1", Name: "get_weather", Input: `{"city":`}),
		eventFrame(t, EventToolUse, ToolUseEvent{ToolUseID: "tu-1", Input: `"Paris"}`, Stop: true}),
		eventFrame(t, EventMetadata, MetadataEvent{TokenUsage: &TokenUsage{UncachedInputTokens: 12, OutputTokens: 34, TotalTokens: 46}}),
		eventFrame(t, EventMetering, MeteringEvent{Usage: 1.5, Unit: "request", UnitPlural: "requests"}),
		// 未建模的事件类型必须被安静跳过，而不是让整条流失败。
		eventFrame(t, "someFutureEvent", map[string]string{"foo": "bar"}),
	}

	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t, frames...)}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	var (
		kinds     []string
		text      strings.Builder
		thinking  strings.Builder
		toolInput strings.Builder
		usage     *TokenUsage
		metering  *MeteringEvent
	)
	conversationID, err := qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, func(event StreamEvent) error {
		kinds = append(kinds, event.Kind)
		switch {
		case event.AssistantResponse != nil:
			text.WriteString(event.AssistantResponse.Content)
		case event.ReasoningContent != nil:
			thinking.WriteString(event.ReasoningContent.Text)
		case event.ToolUse != nil:
			toolInput.WriteString(event.ToolUse.Input)
		case event.Metadata != nil:
			usage = event.Metadata.TokenUsage
		case event.Metering != nil:
			metering = event.Metering
		}
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, "conv-123", conversationID)
	require.Equal(t, "Hello world", text.String())
	require.Equal(t, "thinking...", thinking.String())
	require.JSONEq(t, `{"city":"Paris"}`, toolInput.String())
	require.Equal(t, int64(46), usage.TotalTokens)
	require.Equal(t, 1.5, metering.Usage)
	require.Equal(t, []string{
		EventReasoningContent, EventReasoningContent,
		EventAssistantResponse, EventAssistantResponse,
		EventToolUse, EventToolUse,
		EventMetadata, EventMetering,
	}, kinds)
}

func TestGenerateAssistantResponsePropagatesHandlerError(t *testing.T) {
	frames := []eventstream.Message{
		eventFrame(t, EventAssistantResponse, AssistantResponseEvent{Content: "a"}),
		eventFrame(t, EventAssistantResponse, AssistantResponseEvent{Content: "b"}),
	}
	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t, frames...)}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	seen := 0
	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, func(StreamEvent) error {
		seen++
		return io.ErrClosedPipe
	})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	// 客户端断开后必须立刻停读，不能把整条流跑完。
	require.Equal(t, 1, seen)
}

func TestGenerateAssistantResponseSurfacesExceptionFrame(t *testing.T) {
	frame := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("exception")},
			{Name: ":exception-type", Value: eventstream.StringValue("ThrottlingException")},
		},
		Payload: []byte(`{"message":"slow down"}`),
	}
	client := &stubHTTPClient{responses: []*http.Response{streamResponse(t, frame)}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Contains(t, apiErr.Operation, "ThrottlingException")
	require.Contains(t, apiErr.Body, "slow down")
}

func TestGenerateAssistantResponseClassifiesHTTPStatus(t *testing.T) {
	for status, unauthorized := range map[int]bool{
		http.StatusUnauthorized:        true,
		http.StatusForbidden:           true,
		http.StatusTooManyRequests:     false,
		http.StatusInternalServerError: false,
	} {
		client := &stubHTTPClient{responses: []*http.Response{{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(`{"message":"nope"}`)),
			Header:     http.Header{},
		}}}
		qClient, err := NewClient(client, testCredentials())
		require.NoError(t, err)

		_, err = qClient.GenerateAssistantResponse(context.Background(), &GenerateAssistantResponseRequest{}, nil)

		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, status, apiErr.Status)
		require.Equal(t, unauthorized, apiErr.Unauthorized())
	}
}

func TestListAvailableModelsFollowsPagination(t *testing.T) {
	client := &stubHTTPClient{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"models":[{"modelId":"claude-sonnet-4.6"}],"defaultModel":{"modelId":"auto"},"nextToken":"page-2"}`),
		jsonResponse(http.StatusOK, `{"models":[{"modelId":"claude-opus-4.6"}]}`),
	}}
	qClient, err := NewClient(client, testCredentials())
	require.NoError(t, err)

	models, defaultModel, err := qClient.ListAvailableModels(context.Background())
	require.NoError(t, err)

	require.Len(t, models, 2)
	require.Equal(t, "claude-sonnet-4.6", models[0].ModelID)
	require.Equal(t, "claude-opus-4.6", models[1].ModelID)
	require.Equal(t, "auto", defaultModel.ModelID)

	require.Equal(t, "AI_EDITOR", client.requests[0].URL.Query().Get("origin"))
	require.Equal(t, testCredentials().ProfileARN, client.requests[0].URL.Query().Get("profileArn"))
	require.Empty(t, client.requests[0].URL.Query().Get("nextToken"))
	require.Equal(t, "page-2", client.requests[1].URL.Query().Get("nextToken"))
}

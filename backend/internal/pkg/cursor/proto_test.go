package cursor

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 期望值由 testdata/proto_reference.js 生成，那份脚本是从反代
// proto.js / cursor-agent-env.js / agent-client.js 原样摘出来的。
// 上游没有公开 .proto，这些向量就是我们唯一的正确性依据。

type protoVectors struct {
	RequestContextEnv          string `json:"requestContextEnv"`
	RequestContext             string `json:"requestContext"`
	RequestedModelPlain        string `json:"requestedModelPlain"`
	RequestedModelWithParams   string `json:"requestedModelWithParams"`
	RequestedModelMaxModeFalse string `json:"requestedModelMaxModeFalse"`
	SelectedSubagentModels     string `json:"selectedSubagentModels"`
	Heartbeat                  string `json:"heartbeat"`
	EnvelopeEmpty              string `json:"envelopeEmpty"`
	EnvelopeEndStream          string `json:"envelopeEndStream"`
	ExecClientMessage          string `json:"execClientMessage"`
	ExecClientMessageZeroID    string `json:"execClientMessageZeroID"`
	RunRequest                 string `json:"runRequest"`
	RunRequestWithState        string `json:"runRequestWithState"`
	TiptapSpecialChars         string `json:"tiptapSpecialChars"`
}

func loadProtoVectors(t *testing.T) protoVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/proto_vectors.json")
	require.NoError(t, err)
	var vectors protoVectors
	require.NoError(t, json.Unmarshal(raw, &vectors))
	return vectors
}

func requireHexEqual(t *testing.T, expected string, actual []byte, name string) {
	t.Helper()
	require.Equal(t, expected, hex.EncodeToString(actual), "%s does not match the reverse-proxy encoder", name)
}

func TestEncodeVarintFieldEdgeCases(t *testing.T) {
	// 多字节 varint 与大字段号是最容易写错的两处。
	require.Equal(t, "0800", hex.EncodeToString(EncodeVarintField(1, 0)))
	require.Equal(t, "08ac02", hex.EncodeToString(EncodeVarintField(1, 300)))
	require.Equal(t, "9003ac02", hex.EncodeToString(EncodeVarintField(50, 300)))
	require.Equal(t, "08ffffffffffffffffff01", hex.EncodeToString(EncodeVarintField(1, ^uint64(0))))
}

func TestEncodeBoolFieldAlwaysEmitsFalse(t *testing.T) {
	// 上游对某些字段区分「缺省」与「显式 false」，不能把 false 优化掉。
	require.Equal(t, "1000", hex.EncodeToString(EncodeBoolField(2, false)))
	require.Equal(t, "1001", hex.EncodeToString(EncodeBoolField(2, true)))
}

func TestEncodeRequestedModelMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)

	requireHexEqual(t, vectors.RequestedModelPlain,
		EncodeRequestedModel(RequestedModel{ModelID: "default"}), "RequestedModel(default)")

	requireHexEqual(t, vectors.RequestedModelWithParams,
		EncodeRequestedModel(RequestedModel{ModelID: "grok-4.5", Params: DefaultModelParams()}),
		"RequestedModel(grok-4.5)")

	maxMode := false
	requireHexEqual(t, vectors.RequestedModelMaxModeFalse,
		EncodeRequestedModel(RequestedModel{ModelID: "gpt-5.6-sol", MaxMode: &maxMode}),
		"RequestedModel(maxMode=false)")
}

func TestEncodeSelectedSubagentModelsMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)
	requireHexEqual(t, vectors.SelectedSubagentModels,
		encodeSelectedSubagentModels(officialSelectedSubagentModels()), "selected_subagent_models")
}

func TestEncodeRequestContextMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)
	env := DefaultRequestContextEnv("")

	requireHexEqual(t, vectors.RequestContextEnv, EncodeRequestContextEnv(env), "RequestContext.env")
	requireHexEqual(t, vectors.RequestContext, EncodeRequestContext(env), "RequestContext")
}

func TestEncodeRunRequestMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)
	context := EncodeRequestContext(DefaultRequestContextEnv(""))

	encoded, err := EncodeRunRequest(RunRequestInput{
		Text:           "hello agent",
		MessageID:      "11111111-2222-3333-4444-555555555555",
		ConversationID: "conv-fixed-1",
		RequestContext: context,
		ModelID:        "claude-opus-4-8",
		ModelParams:    DefaultModelParams(),
	})
	require.NoError(t, err)
	requireHexEqual(t, vectors.RunRequest, encoded, "RunRequest")

	withState, err := EncodeRunRequest(RunRequestInput{
		Text:              "second turn",
		MessageID:         "66666666-7777-8888-9999-000000000000",
		ConversationID:    "conv-fixed-2",
		RequestContext:    context,
		ModelID:           "default",
		ModelParams:       []ModelParam{},
		ConversationState: []byte{0xde, 0xad, 0xbe, 0xef},
	})
	require.NoError(t, err)
	requireHexEqual(t, vectors.RunRequestWithState, withState, "RunRequest(with conversation state)")
}

// Go 的 json.Marshal 默认把 < > & 转义成 \u003c 之类，JSON.stringify 不会。
// 用户消息里出现这些字符（写代码时几乎必然）会让两边的字节分叉。
func TestEncodeRunRequestDoesNotHTMLEscapeUserText(t *testing.T) {
	vectors := loadProtoVectors(t)

	encoded, err := EncodeRunRequest(RunRequestInput{
		Text:           `if (a < b && c > d) { return "<tag>"; }`,
		MessageID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ConversationID: "conv-fixed-3",
		RequestContext: EncodeRequestContext(DefaultRequestContextEnv("")),
		ModelID:        "default",
		ModelParams:    []ModelParam{},
	})
	require.NoError(t, err)
	requireHexEqual(t, vectors.TiptapSpecialChars, encoded, "RunRequest(text with < > &)")

	decoded, err := hex.DecodeString(vectors.TiptapSpecialChars)
	require.NoError(t, err)
	require.NotContains(t, string(decoded), `\u003c`)
}

// TipTap 文档的键顺序必须和官方客户端一致；用 map 序列化会被 Go 按字母排序。
func TestTiptapDocPreservesKeyOrder(t *testing.T) {
	doc, err := tiptapDoc("hi")
	require.NoError(t, err)
	require.Equal(t,
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`,
		doc,
	)
}

func TestEncodeHeartbeatMatchesReference(t *testing.T) {
	requireHexEqual(t, loadProtoVectors(t).Heartbeat, EncodeHeartbeat(), "heartbeat")
}

func TestEncodeExecClientMessageMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)

	requireHexEqual(t, vectors.ExecClientMessage,
		EncodeExecClientMessage(7, "e-1", 2, []byte{1, 2, 3}), "exec client message")

	// id 为 0 时也必须写出来——上游靠它关联请求与回执。
	requireHexEqual(t, vectors.ExecClientMessageZeroID,
		EncodeExecClientMessage(0, "", 8, nil), "exec client message with id=0")
}

func TestEncodeEnvelopeMatchesReference(t *testing.T) {
	vectors := loadProtoVectors(t)
	requireHexEqual(t, vectors.EnvelopeEmpty, EncodeEnvelope(nil, 0), "empty envelope")
	requireHexEqual(t, vectors.EnvelopeEndStream,
		EncodeEnvelope([]byte(`{"error":{"code":"x"}}`), connectFlagEndStream), "end-stream envelope")
}

func TestEnvelopeReaderRoundTrip(t *testing.T) {
	stream := concat(
		EncodeEnvelope([]byte("first"), 0),
		EncodeEnvelope([]byte("second"), 0),
		EncodeEnvelope([]byte(`{"error":{"code":"boom","message":"nope"}}`), connectFlagEndStream),
	)
	reader := NewEnvelopeReader(strings.NewReader(string(stream)))

	first, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "first", string(first.Payload))
	require.False(t, first.EndStream())

	second, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "second", string(second.Payload))

	last, err := reader.Next()
	require.NoError(t, err)
	require.True(t, last.EndStream())

	connectErr := ParseEndStreamError(last.Payload)
	require.NotNil(t, connectErr)
	require.Equal(t, "boom", connectErr.Code)

	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestEnvelopeReaderRejectsTruncatedFrames(t *testing.T) {
	// 头部说有 100 字节，实际只给了 3 —— 必须报错而不是当作空帧。
	truncated := append(EncodeEnvelope(make([]byte, 100), 0)[:5], 1, 2, 3)
	_, err := NewEnvelopeReader(strings.NewReader(string(truncated))).Next()
	require.ErrorContains(t, err, "truncated connect envelope payload")

	_, err = NewEnvelopeReader(strings.NewReader("abc")).Next()
	require.ErrorContains(t, err, "truncated connect envelope header")
}

func TestParseEndStreamErrorIgnoresCleanTrailers(t *testing.T) {
	for _, payload := range []string{"", "{}", `{"message":"OK"}`, "not json"} {
		require.Nil(t, ParseEndStreamError([]byte(payload)), "payload=%q", payload)
	}
	require.NotNil(t, ParseEndStreamError([]byte(`{"code":"unavailable"}`)))
}

// 两个载荷都是从真实上游抓下来的（2026-07-30，两个不同账号）。
//
// 关键点：顶层 code 与 message 在两种情况下完全相同（resource_exhausted / "Error"），
// 但一个只要去付账单、另一个等 24 小时就自己好了。只看顶层字段必然误判，
// 所以 details 必须解出来。
func TestParseEndStreamErrorSurfacesUpstreamDetails(t *testing.T) {
	unpaidInvoice := `{"error":{"code":"resource_exhausted","message":"Error","details":[{"type":"aiserver.v1.ErrorDetails","debug":{"error":"ERROR_RATE_LIMITED","details":{"title":"You have an unpaid invoice","detail":"Visit [cursor.com/dashboard](https://cursor.com/dashboard) and pay your invoice in Stripe to resume requests.","isRetryable":false,"showRequestId":false},"isExpected":true},"value":"CDIS"}]}}`
	tooManyComputers := `{"error":{"code":"resource_exhausted","message":"Error","details":[{"type":"aiserver.v1.ErrorDetails","debug":{"error":"ERROR_CUSTOM_MESSAGE","details":{"title":"Too many computers.","detail":"Too many computers used within the last 24 hours for the same Cursor account."},"isExpected":true},"value":"CB0S"}]}}`

	invoiceErr := ParseEndStreamError([]byte(unpaidInvoice))
	require.NotNil(t, invoiceErr)
	require.Equal(t, "resource_exhausted", invoiceErr.Code)
	require.Equal(t, "ERROR_RATE_LIMITED", invoiceErr.UpstreamCode())
	require.Contains(t, invoiceErr.Description(), "You have an unpaid invoice")
	require.Contains(t, invoiceErr.Error(), "ERROR_RATE_LIMITED")
	// 顶层那个没有信息量的 "Error" 不该盖住真正的原因。
	require.NotContains(t, invoiceErr.Error(), ": Error")
	retryable, stated := invoiceErr.Retryable()
	require.True(t, stated, "上游明说了不可重试，这个表态不能丢")
	require.False(t, retryable)

	computersErr := ParseEndStreamError([]byte(tooManyComputers))
	require.NotNil(t, computersErr)
	require.Equal(t, "ERROR_CUSTOM_MESSAGE", computersErr.UpstreamCode())
	require.Contains(t, computersErr.Description(), "Too many computers")
	// 这一条上游没表态，不能把「没说」当成「不可重试」。
	_, stated = computersErr.Retryable()
	require.False(t, stated)
}

// 没有 details 时必须退回原来的行为，否则老的错误会变成一句空话。
func TestConnectErrorFallsBackToTopLevelMessage(t *testing.T) {
	err := &ConnectError{Code: "unavailable", Message: "upstream is down"}
	require.Equal(t, "cursor agent error unavailable: upstream is down", err.Error())
	require.Empty(t, err.UpstreamCode())
	require.Empty(t, err.Description())

	bare := &ConnectError{Message: "boom"}
	require.Equal(t, "cursor agent error: boom", bare.Error())
}

func TestReadFieldsRejectsMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"truncated varint":      {0x08},
		"length overflow":       {0x0a, 0x7f, 0x01},
		"unsupported wire type": {0x0b},
		"zero field number":     {0x00},
	}
	for name, data := range cases {
		_, err := ReadFields(data)
		require.ErrorIs(t, err, ErrMalformedProto, "case=%s", name)
	}
}

func TestReadFieldsRoundTripsEncoders(t *testing.T) {
	encoded := concat(
		EncodeStringField(1, "text"),
		EncodeVarintField(2, 300),
		EncodeBoolField(3, false),
		EncodeBytesField(4, []byte{0xff, 0x00}),
	)
	fields, err := ReadFields(encoded)
	require.NoError(t, err)
	require.Len(t, fields, 4)

	require.Equal(t, "text", FieldString(fields, 1))
	require.Equal(t, uint64(300), fields[1].Varint)
	require.Equal(t, uint64(0), fields[2].Varint)
	raw, ok := FieldBytes(fields, 4)
	require.True(t, ok)
	require.Equal(t, []byte{0xff, 0x00}, raw)
}

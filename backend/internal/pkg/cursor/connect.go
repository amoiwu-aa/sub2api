package cursor

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ConnectRPC 信封：[flags:1][length:4 BE][payload]。
// 注意这不是 gRPC 的分帧——两者头部长度一样但语义不同，别用 gRPC 的库来读。

const (
	// connectFlagEndStream 是 flags 的 bit1，标记流结束帧。
	connectFlagEndStream = 0x02
	connectHeaderLen     = 5
	// maxEnvelopePayload 防御性上限：上游若给出畸形长度，不能让我们直接分配几个 G。
	maxEnvelopePayload = 64 << 20
)

// ErrEnvelopeTooLarge 表示信封声明的长度超出了允许范围。
var ErrEnvelopeTooLarge = errors.New("connect envelope payload is too large")

// EncodeEnvelope 把 payload 包成一个 Connect 信封。
func EncodeEnvelope(payload []byte, flags byte) []byte {
	out := make([]byte, connectHeaderLen+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[connectHeaderLen:], payload)
	return out
}

// Envelope 是解析出来的一帧。
type Envelope struct {
	Flags   byte
	Payload []byte
}

// EndStream 报告该帧是否为流结束帧。
func (e Envelope) EndStream() bool { return e.Flags&connectFlagEndStream != 0 }

// EnvelopeReader 从流里逐帧读取 Connect 信封。
type EnvelopeReader struct {
	reader io.Reader
	header [connectHeaderLen]byte
}

func NewEnvelopeReader(reader io.Reader) *EnvelopeReader {
	return &EnvelopeReader{reader: reader}
}

// Next 读取下一帧。流正常结束时返回 io.EOF。
func (r *EnvelopeReader) Next() (Envelope, error) {
	if _, err := io.ReadFull(r.reader, r.header[:]); err != nil {
		// 半个头部说明连接被中途掐断，与干净的 EOF 是两回事。
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Envelope{}, fmt.Errorf("truncated connect envelope header: %w", err)
		}
		return Envelope{}, err
	}

	length := binary.BigEndian.Uint32(r.header[1:5])
	if length > maxEnvelopePayload {
		return Envelope{}, fmt.Errorf("%w: %d bytes", ErrEnvelopeTooLarge, length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r.reader, payload); err != nil {
		return Envelope{}, fmt.Errorf("truncated connect envelope payload: %w", err)
	}
	return Envelope{Flags: r.header[0], Payload: payload}, nil
}

// ConnectError 是流结束帧里携带的上游错误。
type ConnectError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Details 是上游真正说明白问题的地方。
	//
	// 顶层 message 实测恒为字面量 "Error"，code 也只有 resource_exhausted 这种
	// 粗粒度分类。同一个 resource_exhausted 背后可能是欠费、24 小时内设备过多、
	// 或者额度真的用完——只看 code 与 message 三者完全无法区分，运维会照着
	// 「额度耗尽」这个错误结论去处理一个其实只要付账单的账号。
	Details []ConnectErrorDetail `json:"details,omitempty"`
}

// ConnectErrorDetail 是 details 数组里的一项。
type ConnectErrorDetail struct {
	Type  string                  `json:"type,omitempty"`
	Debug ConnectErrorDetailDebug `json:"debug,omitempty"`
}

// ConnectErrorDetailDebug 承载上游的具名错误码与面向用户的文案。
type ConnectErrorDetailDebug struct {
	// Error 是具名错误码，例如 ERROR_RATE_LIMITED、ERROR_CUSTOM_MESSAGE。
	Error   string                   `json:"error,omitempty"`
	Details ConnectErrorDetailFields `json:"details,omitempty"`
}

// ConnectErrorDetailFields 是上游给终端用户看的文案。
type ConnectErrorDetailFields struct {
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
	// IsRetryable 用指针区分「上游明说不可重试」与「上游没提」。
	// 欠费这类错误上游会显式写 false，重试只是白白刷失败计数。
	IsRetryable *bool `json:"isRetryable,omitempty"`
}

// primaryDetail 返回第一条带内容的 detail。
// 实测 details 只有一项，但协议允许多项，取第一条有内容的即可。
func (e *ConnectError) primaryDetail() *ConnectErrorDetail {
	if e == nil {
		return nil
	}
	for i := range e.Details {
		detail := &e.Details[i]
		if detail.Debug.Error != "" || detail.Debug.Details.Title != "" || detail.Debug.Details.Detail != "" {
			return detail
		}
	}
	return nil
}

// UpstreamCode 返回上游的具名错误码，没有时为空。
func (e *ConnectError) UpstreamCode() string {
	if detail := e.primaryDetail(); detail != nil {
		return detail.Debug.Error
	}
	return ""
}

// Description 把上游的标题与正文拼成一句可读的说明，没有时返回空。
func (e *ConnectError) Description() string {
	detail := e.primaryDetail()
	if detail == nil {
		return ""
	}
	title := strings.TrimSpace(detail.Debug.Details.Title)
	body := strings.TrimSpace(detail.Debug.Details.Detail)
	switch {
	case title != "" && body != "":
		return title + ": " + body
	case title != "":
		return title
	default:
		return body
	}
}

// Retryable 返回上游对可重试性的判断，以及它有没有表态。
func (e *ConnectError) Retryable() (retryable, stated bool) {
	detail := e.primaryDetail()
	if detail == nil || detail.Debug.Details.IsRetryable == nil {
		return false, false
	}
	return *detail.Debug.Details.IsRetryable, true
}

func (e *ConnectError) Error() string {
	base := "cursor agent error"
	if e.Code != "" {
		base += " " + e.Code
	}
	// 有 details 就用它：顶层 message 是 "Error"，留着只会盖住真正的原因。
	if description := e.Description(); description != "" {
		if upstream := e.UpstreamCode(); upstream != "" {
			return base + " (" + upstream + "): " + description
		}
		return base + ": " + description
	}
	return base + ": " + e.Message
}

// ParseEndStreamError 从结束帧里取出错误。没有错误时返回 nil。
//
// 结束帧可能是空的、可能是 {"error":{...}}、也可能直接是 {"code":...}；
// 三种都见过，所以都认。
func ParseEndStreamError(payload []byte) *ConnectError {
	if len(payload) == 0 {
		return nil
	}
	var wrapper struct {
		Error *ConnectError `json:"error"`
	}
	if err := json.Unmarshal(payload, &wrapper); err == nil && wrapper.Error != nil {
		if wrapper.Error.Code != "" || wrapper.Error.Message != "" || len(wrapper.Error.Details) > 0 {
			return wrapper.Error
		}
		return nil
	}

	var bare ConnectError
	if err := json.Unmarshal(payload, &bare); err != nil {
		return nil
	}
	// 正常收尾的 trailer 也是 JSON，但不带 code、message 是 OK。
	if bare.Code == "" && (bare.Message == "" || bare.Message == "OK") {
		return nil
	}
	return &bare
}

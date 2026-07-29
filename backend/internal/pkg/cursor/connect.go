package cursor

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

func (e *ConnectError) Error() string {
	if e.Code != "" {
		return "cursor agent error " + e.Code + ": " + e.Message
	}
	return "cursor agent error: " + e.Message
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
		if wrapper.Error.Code != "" || wrapper.Error.Message != "" {
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

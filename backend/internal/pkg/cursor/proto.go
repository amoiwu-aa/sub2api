package cursor

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 手写的 protobuf wire format 编解码。
//
// 上游没有公开的 .proto，字段号来自对 Cursor 客户端的逆向（见反代 proto.js /
// cursor-agent-env.js）。这里刻意不引 protobuf 运行时：我们只需要按字段号拼
// 字节，引入生成器反而要维护一份猜出来的 schema。

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// appendVarint 追加一个 base-128 varint。
func appendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func appendTag(dst []byte, fieldNum int, wireType byte) []byte {
	return appendVarint(dst, uint64(fieldNum)<<3|uint64(wireType))
}

// EncodeVarintField 编码一个 varint 字段。
func EncodeVarintField(fieldNum int, value uint64) []byte {
	return appendVarint(appendTag(nil, fieldNum, wireVarint), value)
}

// EncodeBoolField 编码一个 bool 字段。
// 注意 false 也会被显式写出——上游对某些字段区分「缺省」与「显式 false」。
func EncodeBoolField(fieldNum int, value bool) []byte {
	if value {
		return EncodeVarintField(fieldNum, 1)
	}
	return EncodeVarintField(fieldNum, 0)
}

// EncodeBytesField 编码一个 length-delimited 字段。
func EncodeBytesField(fieldNum int, value []byte) []byte {
	out := appendTag(nil, fieldNum, wireBytes)
	out = appendVarint(out, uint64(len(value)))
	return append(out, value...)
}

// EncodeStringField 编码一个字符串字段。
func EncodeStringField(fieldNum int, value string) []byte {
	return EncodeBytesField(fieldNum, []byte(value))
}

// concat 把多个字段片段拼成一条消息。
func concat(parts ...[]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// ErrMalformedProto 表示字节流不是合法的 protobuf。
var ErrMalformedProto = errors.New("malformed protobuf")

// Field 是解析出来的一个字段。
type Field struct {
	Number   int
	WireType byte
	// Varint 在 WireType == 0 时有效。
	Varint uint64
	// Bytes 在 WireType == 2 时有效，指向原切片，不拷贝。
	Bytes []byte
}

// String 把 length-delimited 字段当作 UTF-8 字符串读出。
func (f Field) String() string {
	if f.WireType != wireBytes {
		return ""
	}
	return string(f.Bytes)
}

// ReadFields 解析一条消息的顶层字段。未知字段照常返回，不报错。
func ReadFields(data []byte) ([]Field, error) {
	var fields []Field
	pos := 0
	for pos < len(data) {
		tag, next, err := readVarint(data, pos)
		if err != nil {
			return nil, err
		}
		pos = next

		fieldNum := int(tag >> 3)
		wireType := byte(tag & 0x7)
		if fieldNum <= 0 {
			return nil, fmt.Errorf("%w: field number %d", ErrMalformedProto, fieldNum)
		}

		switch wireType {
		case wireVarint:
			value, next, err := readVarint(data, pos)
			if err != nil {
				return nil, err
			}
			pos = next
			fields = append(fields, Field{Number: fieldNum, WireType: wireType, Varint: value})
		case wireBytes:
			length, next, err := readVarint(data, pos)
			if err != nil {
				return nil, err
			}
			end := next + int(length)
			if length > uint64(len(data)) || end > len(data) || end < next {
				return nil, fmt.Errorf("%w: length-delimited field %d overflows", ErrMalformedProto, fieldNum)
			}
			fields = append(fields, Field{Number: fieldNum, WireType: wireType, Bytes: data[next:end]})
			pos = end
		case wireFixed64:
			if pos+8 > len(data) {
				return nil, fmt.Errorf("%w: fixed64 field %d overflows", ErrMalformedProto, fieldNum)
			}
			fields = append(fields, Field{
				Number: fieldNum, WireType: wireType,
				Varint: binary.LittleEndian.Uint64(data[pos : pos+8]),
			})
			pos += 8
		case wireFixed32:
			if pos+4 > len(data) {
				return nil, fmt.Errorf("%w: fixed32 field %d overflows", ErrMalformedProto, fieldNum)
			}
			fields = append(fields, Field{
				Number: fieldNum, WireType: wireType,
				Varint: uint64(binary.LittleEndian.Uint32(data[pos : pos+4])),
			})
			pos += 4
		default:
			// 3/4 是已废弃的 group 编码，上游不会用；遇到就说明解错位了。
			return nil, fmt.Errorf("%w: unsupported wire type %d on field %d", ErrMalformedProto, wireType, fieldNum)
		}
	}
	return fields, nil
}

// FieldBytes 返回第一个匹配字段的 length-delimited 内容。
func FieldBytes(fields []Field, fieldNum int) ([]byte, bool) {
	for _, field := range fields {
		if field.Number == fieldNum && field.WireType == wireBytes {
			return field.Bytes, true
		}
	}
	return nil, false
}

// FieldString 返回第一个匹配字段的字符串内容。
func FieldString(fields []Field, fieldNum int) string {
	if raw, ok := FieldBytes(fields, fieldNum); ok {
		return string(raw)
	}
	return ""
}

func readVarint(data []byte, pos int) (uint64, int, error) {
	var value uint64
	var shift uint
	for pos < len(data) {
		b := data[pos]
		pos++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, pos, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, pos, fmt.Errorf("%w: varint too long", ErrMalformedProto)
		}
	}
	return 0, pos, fmt.Errorf("%w: truncated varint", ErrMalformedProto)
}

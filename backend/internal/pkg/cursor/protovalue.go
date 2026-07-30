package cursor

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// google.protobuf.Value 的手写编解码。
//
// Cursor 的 McpToolDefinition.input_schema 与 McpArgs.args 都用 Value 承载
// 任意 JSON，但它们在 wire 上是序列化后的 Value 字节，不是 JSON 文本。工具
// 声明和工具入参都要过这一层，所以它是整个 tool-calling 桥的地基。
//
// Value 的 oneof 标签：
//
//	1 null_value    varint
//	2 number_value  double（fixed64）
//	3 string_value  bytes
//	4 bool_value    varint
//	5 struct_value  Struct { fields = 1 (repeated map entry) }
//	6 list_value    ListValue { values = 1 (repeated Value) }
//
// Struct 的 map entry 是 { 1: key, 2: Value }。

const (
	valueFieldNull   = 1
	valueFieldNumber = 2
	valueFieldString = 3
	valueFieldBool   = 4
	valueFieldStruct = 5
	valueFieldList   = 6

	// maxProtobufValueDepth 给递归封顶。入参来自客户端，深度不设限
	// 会让一个畸形的 JSON schema 把整个进程的栈打爆。
	maxProtobufValueDepth = 32
)

// EncodeDoubleField 编码一个 fixed64 字段，承载 IEEE-754 双精度。
func EncodeDoubleField(fieldNum int, value float64) []byte {
	out := appendTag(nil, fieldNum, wireFixed64)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(value))
	return append(out, buf[:]...)
}

// EncodeProtobufValue 把一个 JSON 值编码成 google.protobuf.Value 的消息体。
//
// 接受 encoding/json 反序列化出来的原生类型（nil / bool / float64 /
// json.Number / string / []any / map[string]any）。认不出来的类型按 null 处理，
// 而不是报错：一个字段编不出来不该让整次请求失败。
func EncodeProtobufValue(value any) []byte {
	return encodeProtobufValue(value, 0)
}

func encodeProtobufValue(value any, depth int) []byte {
	if depth > maxProtobufValueDepth {
		return EncodeVarintField(valueFieldNull, 0)
	}
	switch typed := value.(type) {
	case nil:
		return EncodeVarintField(valueFieldNull, 0)
	case bool:
		return EncodeBoolField(valueFieldBool, typed)
	case string:
		return EncodeStringField(valueFieldString, typed)
	case float64:
		return EncodeDoubleField(valueFieldNumber, typed)
	case float32:
		return EncodeDoubleField(valueFieldNumber, float64(typed))
	case int:
		return EncodeDoubleField(valueFieldNumber, float64(typed))
	case int64:
		return EncodeDoubleField(valueFieldNumber, float64(typed))
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return EncodeStringField(valueFieldString, typed.String())
		}
		return EncodeDoubleField(valueFieldNumber, number)
	case json.RawMessage:
		return encodeRawJSONValue(typed, depth)
	case []any:
		list := make([]byte, 0, len(typed)*8)
		for _, item := range typed {
			list = append(list, EncodeBytesField(1, encodeProtobufValue(item, depth+1))...)
		}
		return EncodeBytesField(valueFieldList, list)
	case map[string]any:
		return EncodeBytesField(valueFieldStruct, encodeProtobufStructFields(typed, depth))
	default:
		return EncodeVarintField(valueFieldNull, 0)
	}
}

// encodeProtobufStructFields 编码 Struct.fields。
//
// 按键名排序而不是依赖 map 的随机序：同一份工具声明每次编出的字节必须一致，
// 否则请求指纹会在每次请求间抖动。
func encodeProtobufStructFields(fields map[string]any, depth int) []byte {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]byte, 0, len(keys)*16)
	for _, key := range keys {
		entry := concat(
			EncodeStringField(1, key),
			EncodeBytesField(2, encodeProtobufValue(fields[key], depth+1)),
		)
		out = append(out, EncodeBytesField(1, entry)...)
	}
	return out
}

func encodeRawJSONValue(raw json.RawMessage, depth int) []byte {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return EncodeVarintField(valueFieldNull, 0)
	}
	return encodeProtobufValue(decoded, depth+1)
}

// EncodeProtobufValueFromJSON 把一段 JSON 文本编码成 Value 消息体。
// 解析失败时返回 null，调用方拿到的仍是一个结构上合法的 Value。
func EncodeProtobufValueFromJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return EncodeVarintField(valueFieldNull, 0)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return EncodeVarintField(valueFieldNull, 0)
	}
	return EncodeProtobufValue(decoded)
}

// DecodeProtobufValue 把 Value 消息体还原成原生 Go 值。
//
// 用于读取模型传回来的工具入参。字节里出现未知字段时按 null 处理，
// 因为上游随时可能给 Value 加新 case。
func DecodeProtobufValue(data []byte) (any, error) {
	return decodeProtobufValue(data, 0)
}

func decodeProtobufValue(data []byte, depth int) (any, error) {
	if depth > maxProtobufValueDepth {
		return nil, fmt.Errorf("protobuf value nested deeper than %d", maxProtobufValueDepth)
	}
	fields, err := ReadFields(data)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		switch field.Number {
		case valueFieldNull:
			return nil, nil
		case valueFieldNumber:
			if field.WireType == wireFixed64 {
				return math.Float64frombits(field.Varint), nil
			}
		case valueFieldString:
			if field.WireType == wireBytes {
				return string(field.Bytes), nil
			}
		case valueFieldBool:
			if field.WireType == wireVarint {
				return field.Varint != 0, nil
			}
		case valueFieldStruct:
			if field.WireType == wireBytes {
				return decodeProtobufStruct(field.Bytes, depth)
			}
		case valueFieldList:
			if field.WireType == wireBytes {
				return decodeProtobufList(field.Bytes, depth)
			}
		}
	}
	return nil, nil
}

func decodeProtobufStruct(data []byte, depth int) (map[string]any, error) {
	entries, err := ReadFields(data)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any)
	for _, entry := range entries {
		if entry.Number != 1 || entry.WireType != wireBytes {
			continue
		}
		key, value, err := decodeProtobufMapEntry(entry.Bytes, depth)
		if err != nil {
			return nil, err
		}
		if key != "" {
			out[key] = value
		}
	}
	return out, nil
}

func decodeProtobufMapEntry(data []byte, depth int) (string, any, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return "", nil, err
	}
	key := ""
	var value any
	for _, field := range fields {
		switch {
		case field.Number == 1 && field.WireType == wireBytes:
			key = string(field.Bytes)
		case field.Number == 2 && field.WireType == wireBytes:
			value, err = decodeProtobufValue(field.Bytes, depth+1)
			if err != nil {
				return "", nil, err
			}
		}
	}
	return key, value, nil
}

func decodeProtobufList(data []byte, depth int) ([]any, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(fields))
	for _, field := range fields {
		if field.Number != 1 || field.WireType != wireBytes {
			continue
		}
		value, err := decodeProtobufValue(field.Bytes, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

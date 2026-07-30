package cursor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireValueRoundTrip(t *testing.T, raw string) any {
	t.Helper()
	var decoded any
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	restored, err := DecodeProtobufValue(EncodeProtobufValue(decoded))
	require.NoError(t, err)
	return restored
}

func TestProtobufValueRoundTripsScalars(t *testing.T) {
	require.Nil(t, requireValueRoundTrip(t, `null`))
	require.Equal(t, true, requireValueRoundTrip(t, `true`))
	require.Equal(t, false, requireValueRoundTrip(t, `false`))
	require.Equal(t, "hello", requireValueRoundTrip(t, `"hello"`))
	require.InDelta(t, 3.5, requireValueRoundTrip(t, `3.5`), 0.0001)
	require.InDelta(t, -12.0, requireValueRoundTrip(t, `-12`), 0.0001)
}

func TestProtobufValueRoundTripsNestedStructure(t *testing.T) {
	// 工具的 input_schema 就是这种形状，编解码任何一环出错都会让声明失效。
	restored := requireValueRoundTrip(t, `{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "shell command"},
			"env": {"type": "array", "items": {"type": "string"}},
			"retries": {"type": "number", "default": 3},
			"detach": {"type": "boolean"}
		},
		"required": ["command"],
		"additionalProperties": false
	}`)

	root, ok := restored.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", root["type"])
	require.Equal(t, false, root["additionalProperties"])
	require.Equal(t, []any{"command"}, root["required"])

	properties, ok := root["properties"].(map[string]any)
	require.True(t, ok)
	command, ok := properties["command"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "shell command", command["description"])

	env, ok := properties["env"].(map[string]any)
	require.True(t, ok)
	items, ok := env["items"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", items["type"])
}

func TestProtobufValueStructIsKeyOrdered(t *testing.T) {
	// map 迭代序不稳定，不排序的话同一份 schema 每次编出的字节都不同。
	raw := `{"z":1,"a":2,"m":{"q":3,"b":4}}`
	var decoded any
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))

	first := EncodeProtobufValue(decoded)
	for i := 0; i < 20; i++ {
		require.Equal(t, first, EncodeProtobufValue(decoded))
	}
}

func TestEncodeProtobufValueFromJSONFallsBackToNull(t *testing.T) {
	// 解析不了也要给出一个结构上合法的 Value，否则整条工具声明会编坏。
	for _, raw := range []string{"", "not json", "{"} {
		decoded, err := DecodeProtobufValue(EncodeProtobufValueFromJSON([]byte(raw)))
		require.NoError(t, err)
		require.Nil(t, decoded)
	}
}

func TestEncodeProtobufValueHandlesJSONNumber(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"n": 42}`))
	decoder.UseNumber()
	var decoded map[string]any
	require.NoError(t, decoder.Decode(&decoded))

	restored, err := DecodeProtobufValue(EncodeProtobufValue(decoded))
	require.NoError(t, err)
	root, ok := restored.(map[string]any)
	require.True(t, ok)
	require.InDelta(t, 42.0, root["n"], 0.0001)
}

func TestProtobufValueDepthIsBounded(t *testing.T) {
	// 入参来自客户端，深度不设限会让一个畸形 schema 把栈打爆。
	nested := any("leaf")
	for i := 0; i < maxProtobufValueDepth+40; i++ {
		nested = map[string]any{"n": nested}
	}
	encoded := EncodeProtobufValue(nested)
	require.NotEmpty(t, encoded)

	// 解码同样封顶：要么在深度上限处报错，要么正常返回，但绝不能崩。
	_, err := DecodeProtobufValue(encoded)
	if err != nil {
		require.Contains(t, err.Error(), "nested deeper")
	}
}

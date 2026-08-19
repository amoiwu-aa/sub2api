package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIClientToolsAndCursorNativeBridgeKeepTheirOwnSemantics(t *testing.T) {
	responsesBody := []byte(`{
		"model":"gpt-5.4",
		"input":"update the file",
		"tools":[
			{"type":"custom","name":"Read"},
			{"type":"custom","name":"Write"}
		]
	}`)

	adapted, mapping, err := adaptOpenAIResponsesClientTools(responsesBody)
	require.NoError(t, err)
	require.True(t, mapping.CustomTools["Read"])
	require.True(t, mapping.CustomTools["Write"])
	require.Equal(t, "function", gjson.GetBytes(adapted, "tools.0.type").String())
	require.Equal(t, "Read", gjson.GetBytes(adapted, "tools.0.name").String())
	require.Equal(t, "function", gjson.GetBytes(adapted, "tools.1.type").String())
	require.Equal(t, "Write", gjson.GetBytes(adapted, "tools.1.name").String())

	cursorTools := []cursor.McpTool{
		{
			Name: "Read",
			InputSchema: json.RawMessage(
				`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			),
		},
		{
			Name: "Write",
			InputSchema: json.RawMessage(
				`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
			),
		},
	}
	bridge, remainingMCPTools, err := resolveCursorNativeToolBridge(
		[]byte(`{"model":"cursor/grok-4.6"}`),
		cursorTools,
		CursorNativeToolBridgeModeInferAll,
	)
	require.NoError(t, err)
	require.Equal(t, "Read", bridge.ClientName("read"))
	require.Equal(t, "Write", bridge.ClientName("write"))
	require.Empty(t, remainingMCPTools)
}

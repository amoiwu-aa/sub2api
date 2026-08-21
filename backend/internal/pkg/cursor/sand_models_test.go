package cursor

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchSandUsableModelsUsesAgentServiceCatalog(t *testing.T) {
	modelBody := concat(
		EncodeStringField(1, "claude-opus-4-8"),
		EncodeStringField(3, "claude-opus-4-8"),
		EncodeStringField(4, "Claude Opus 4.8"),
		EncodeStringField(5, "Opus 4.8"),
		EncodeStringField(6, "opus"),
		EncodeStringField(6, "claude-opus"),
		EncodeBoolField(7, true),
	)
	responseBody := EncodeBytesField(1, modelBody)
	client := &stubHTTPClient{responses: []*http.Response{protoResponse(http.StatusOK, responseBody)}}

	models, err := FetchSandUsableModels(
		t.Context(),
		&Options{HTTPClient: client},
		"test-token",
		"machine-id",
		"0.20.0",
		"prod",
		[]string{"custom-model"},
	)
	require.NoError(t, err)
	require.Equal(t, []SandModelDetails{{
		ModelID:          "claude-opus-4-8",
		DisplayModelID:   "claude-opus-4-8",
		DisplayName:      "Claude Opus 4.8",
		DisplayNameShort: "Opus 4.8",
		Aliases:          []string{"opus", "claude-opus"},
		MaxMode:          true,
	}}, models)

	require.Len(t, client.requests, 1)
	req := client.requests[0]
	require.Equal(t, "/agent.v1.AgentService/GetUsableModels", req.URL.Path)
	require.Equal(t, "false", req.Header.Get("X-Ghost-Mode"))
	require.Equal(t, "sand", req.Header.Get("X-Cursor-Client-Type"))

	fields, err := ReadFields([]byte(client.bodies[0]))
	require.NoError(t, err)
	require.Equal(t, "custom-model", FieldString(fields, 1))
}

func TestDecodeSandUsableModelsSkipsEmptyModelIDs(t *testing.T) {
	body := concat(
		EncodeBytesField(1, EncodeStringField(4, "missing id")),
		EncodeBytesField(1, EncodeStringField(1, "default")),
	)
	models, err := decodeSandUsableModels(body)
	require.NoError(t, err)
	require.Equal(t, []SandModelDetails{{ModelID: "default"}}, models)
}

func TestSandPublicModelIDsFiltersUnknownModelsAndExpandsVerifiedVariants(t *testing.T) {
	models := []SandModelDetails{
		{ModelID: "default"},
		{ModelID: "cursor-claude-opus-4-8-high", DisplayModelID: "claude-opus-4-8"},
		{ModelID: "gpt-5.6-sol-fast"},
		{ModelID: "cursor-grok-4.6-high-fast", DisplayModelID: "grok-4.6"},
		{ModelID: "cursor-unknown-model"},
	}

	require.Equal(t, []string{
		"cursor/default",
		"cursor/claude-opus-4-8",
		"cursor/gpt-5.6-sol",
		"cursor/grok-4.6",
		"cursor/grok-4.6-max",
	}, SandPublicModelIDs(models))
}

package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func TestCursorAgentProfileDefaultsToIDEAndParsesSandAliases(t *testing.T) {
	account := cursorAccount(map[string]any{})
	require.Equal(t, cursor.AgentProfileIDE, account.CursorAgentProfile())

	account.Credentials[CursorAgentProfileCredentialKey] = "grok-bot"
	require.Equal(t, cursor.AgentProfileSand, account.CursorAgentProfile())
}

func TestCursorTokenProviderAcceptsOpaqueSandToken(t *testing.T) {
	provider := NewCursorTokenProvider(nil, nil)
	account := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
		"access_token":                  "sand-runtime-access-token",
	})

	token, err := provider.GetAccessToken(t.Context(), account)
	require.NoError(t, err)
	require.Equal(t, "sand-runtime-access-token", token)
}

func TestCursorTokenProviderRejectsElectronStorageWrapperForSand(t *testing.T) {
	provider := NewCursorTokenProvider(nil, nil)
	account := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
		"access_token":                  "scoped:v1:abc:def",
	})

	_, err := provider.GetAccessToken(t.Context(), account)
	require.ErrorIs(t, err, errCursorTokenInvalid)
}

func TestCursorTokenRefresherKeepsOpaqueSandTokenWithoutExpiry(t *testing.T) {
	refresher := NewCursorTokenRefresher(nil)
	account := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
		"access_token":                  "sand-runtime-access-token",
		"refresh_token":                 "sand-runtime-refresh-token",
	})

	require.False(t, refresher.NeedsRefresh(account, 30*time.Minute))
}

func TestCursorOAuthServiceBuildsSandCredentials(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	defer svc.Stop()

	info, err := svc.ImportSandCredentials(
		t.Context(),
		"sand-access",
		"sand-refresh",
		"machine-123",
		"",
		"prod",
		nil,
	)
	require.NoError(t, err)
	credentials := svc.BuildAccountCredentials(info)
	require.Equal(t, "sand", credentials[CursorAgentProfileCredentialKey])
	require.Equal(t, "machine-123", credentials[CursorMachineIDCredentialKey])
	require.Equal(t, cursor.SandClientVersion, credentials[CursorClientVersionCredentialKey])
	require.Equal(t, "prod", credentials[CursorSandNamespaceCredentialKey])
}

func TestCursorOAuthServiceRejectsEncryptedSandCredential(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	defer svc.Stop()

	_, err := svc.ImportSandCredentials(
		t.Context(),
		"scoped:v1:abc:def",
		"",
		"",
		"",
		"",
		nil,
	)
	require.ErrorContains(t, err, "CURSOR_SAND_TOKEN_ENCRYPTED")
}

func TestSandAccountModelSupportIsRestrictedToVerifiedCatalog(t *testing.T) {
	account := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
	})

	require.True(t, account.IsModelSupported("cursor/claude-opus-4-8"))
	require.True(t, account.IsModelSupported("cursor/grok-4.6"))
	require.False(t, account.IsModelSupported("cursor/not-a-sand-model"))

	account.Credentials["model_mapping"] = map[string]any{
		"my-claude": "cursor/claude-opus-4-8",
		"unsafe":    "cursor/not-a-sand-model",
	}
	require.True(t, account.IsModelSupported("my-claude"))
	require.False(t, account.IsModelSupported("unsafe"))
	require.False(t, account.IsModelSupported("cursor/claude-opus-4-8"))
}

func TestResolveCursorAccountModelSelectionMapsAndRechecksSandModel(t *testing.T) {
	account := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
		"model_mapping": map[string]any{
			"my-claude": "cursor/claude-opus-4-8",
			"unsafe":    "cursor/not-a-sand-model",
		},
	})

	selection, err := resolveCursorAccountModelSelection(
		account,
		[]byte(`{"model":"my-claude"}`),
		"my-claude",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8", selection.ModelID)

	_, err = resolveCursorAccountModelSelection(
		account,
		[]byte(`{"model":"unsafe"}`),
		"unsafe",
		nil,
	)
	require.ErrorContains(t, err, "not available through Grok Bot weekly usage")

	_, err = resolveCursorAccountModelSelection(
		account,
		[]byte(`{"model":"cursor/grok-4.6"}`),
		"cursor/grok-4.6",
		nil,
	)
	require.ErrorContains(t, err, "not enabled for this Grok Bot account")
}

func TestSandAccountIgnoresCursorForceUseOverride(t *testing.T) {
	ide := cursorAccount(map[string]any{})
	ide.Extra = map[string]any{CursorForceUseExtraKey: true}
	require.True(t, ide.IsCursorForceUseEnabled())

	sand := cursorAccount(map[string]any{
		CursorAgentProfileCredentialKey: "sand",
	})
	sand.Extra = map[string]any{CursorForceUseExtraKey: true}
	require.False(t, sand.IsCursorForceUseEnabled())
}

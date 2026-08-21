package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// Cursor transport settings live in credentials so they travel with an
// account and remain compatible with the existing account API.
const (
	CursorAgentProfileCredentialKey  = "cursor_agent_profile"
	CursorMachineIDCredentialKey     = "cursor_machine_id"
	CursorClientVersionCredentialKey = "cursor_client_version"
	CursorSandNamespaceCredentialKey = "cursor_sand_namespace"
)

// CursorAgentProfile returns the transport profile for a Cursor account.
// Existing accounts default to the historical IDE profile.
func (a *Account) CursorAgentProfile() cursor.AgentProfile {
	if a == nil || !a.IsCursor() {
		return cursor.AgentProfileIDE
	}
	value := strings.TrimSpace(a.GetCredential(CursorAgentProfileCredentialKey))
	if value == "" {
		value = strings.TrimSpace(a.GetCredential("agent_profile"))
	}
	return cursor.ParseAgentProfile(value)
}

func (a *Account) CursorMachineID() string {
	if a == nil || !a.IsCursor() {
		return ""
	}
	return strings.TrimSpace(a.GetCredential(CursorMachineIDCredentialKey))
}

func (a *Account) CursorClientVersion() string {
	if a == nil || !a.IsCursor() {
		return ""
	}
	return strings.TrimSpace(a.GetCredential(CursorClientVersionCredentialKey))
}

func (a *Account) CursorSandNamespace() string {
	if a == nil || !a.IsCursor() {
		return ""
	}
	return strings.TrimSpace(a.GetCredential(CursorSandNamespaceCredentialKey))
}

// cursorTokenUsableForAccount keeps the strict session-token requirement for
// Cursor IDE while allowing the Sand client contract to carry a non-JWT token.
// The scoped/plaintext prefixes are Electron's on-disk safeStorage wrappers,
// not bearer tokens; accepting them would produce an opaque upstream 401.
func cursorTokenUsableForAccount(account *Account, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if account != nil && account.CursorAgentProfile() == cursor.AgentProfileSand {
		return !strings.HasPrefix(token, "scoped:v1:") &&
			!strings.HasPrefix(token, "plaintext:v1:")
	}
	return cursor.IsSessionToken(token)
}

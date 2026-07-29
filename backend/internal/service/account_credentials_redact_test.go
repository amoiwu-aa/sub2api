//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergePreservingSensitiveCreds_PreservesSensitiveWhenIncomingMissing(t *testing.T) {
	existing := map[string]any{
		"refresh_token": "rt-old",
		"access_token":  "at-old",
		"api_key":       "sk-old",
		"base_url":      "https://old.example.com",
	}
	incoming := map[string]any{
		"base_url":      "https://new.example.com",
		"model_mapping": map[string]any{"foo": "bar"},
	}

	out := MergePreservingSensitiveCreds(existing, incoming)

	require.Equal(t, "rt-old", out["refresh_token"], "incoming 没传 refresh_token，应保留 existing")
	require.Equal(t, "at-old", out["access_token"])
	require.Equal(t, "sk-old", out["api_key"])
	require.Equal(t, "https://new.example.com", out["base_url"], "非敏感键由 incoming 决定")
	require.Equal(t, map[string]any{"foo": "bar"}, out["model_mapping"])
}

func TestMergePreservingSensitiveCreds_OverwritesWhenIncomingProvidesSensitive(t *testing.T) {
	existing := map[string]any{
		"refresh_token": "rt-old",
		"api_key":       "sk-old",
	}
	incoming := map[string]any{
		"refresh_token": "rt-new",
		// 显式没传 api_key —— 应保留
	}
	out := MergePreservingSensitiveCreds(existing, incoming)
	require.Equal(t, "rt-new", out["refresh_token"], "incoming 显式传入应覆盖")
	require.Equal(t, "sk-old", out["api_key"], "incoming 没传应保留")
}

func TestMergePreservingSensitiveCreds_DoesNotMutateInputs(t *testing.T) {
	existing := map[string]any{"refresh_token": "rt"}
	incoming := map[string]any{"base_url": "x"}

	_ = MergePreservingSensitiveCreds(existing, incoming)

	require.Equal(t, "rt", existing["refresh_token"])
	require.NotContains(t, existing, "base_url")
	require.Equal(t, "x", incoming["base_url"])
	require.NotContains(t, incoming, "refresh_token")
}

func TestMergePreservingSensitiveCreds_NilInputs(t *testing.T) {
	out := MergePreservingSensitiveCreds(nil, map[string]any{"base_url": "x"})
	require.Equal(t, "x", out["base_url"])
	require.NotContains(t, out, "refresh_token")

	out2 := MergePreservingSensitiveCreds(map[string]any{"refresh_token": "rt"}, nil)
	require.Equal(t, "rt", out2["refresh_token"])
}

func TestMergePreservingSensitiveCreds_NonSensitiveDeletionAllowed(t *testing.T) {
	existing := map[string]any{
		"refresh_token": "rt",
		"base_url":      "https://old",
		"project_id":    "p1",
	}
	incoming := map[string]any{
		"base_url": "https://new",
		// 不带 project_id —— 等同删除（非敏感键由 incoming 决定）
	}
	out := MergePreservingSensitiveCreds(existing, incoming)
	require.Equal(t, "rt", out["refresh_token"], "敏感键保留")
	require.Equal(t, "https://new", out["base_url"])
	require.NotContains(t, out, "project_id", "非敏感键 incoming 不传 = 删除")
}

func TestIsSensitiveCredentialKey(t *testing.T) {
	require.True(t, IsSensitiveCredentialKey("refresh_token"))
	require.True(t, IsSensitiveCredentialKey("api_key"))
	require.True(t, IsSensitiveCredentialKey("private_key"))
	// kiro 的 IdC 账号把 AWS IdC 的 client_secret 落进 credentials，用于刷新链换 token；
	// 它等同于长期凭证，不能随账号详情返回给前端，也不能进审计日志。
	require.True(t, IsSensitiveCredentialKey("client_secret"))
	// client_id 只是个标识，不脱敏——脱掉反而看不出账号绑的是哪个 IdC 应用。
	require.False(t, IsSensitiveCredentialKey("client_id"))
	require.False(t, IsSensitiveCredentialKey("base_url"))
	require.False(t, IsSensitiveCredentialKey(""))
	require.False(t, IsSensitiveCredentialKey("model_mapping"))
}

// kiro IdC 账号的完整凭证形状过一遍脱敏，确认 secret 不外泄、非敏感字段不被抹掉。
func TestRedactKiroIdCCredentials(t *testing.T) {
	creds := map[string]any{
		"access_token":  "at-secret",
		"refresh_token": "rt-secret",
		"client_id":     "idc-client-id",
		"client_secret": "idc-client-secret",
		"auth_method":   "IdC",
		"profile_arn":   "arn:aws:codewhisperer:us-east-1:123:profile/ABC",
		"region":        "us-east-1",
	}
	for key, value := range creds {
		if IsSensitiveCredentialKey(key) {
			continue
		}
		require.NotContains(t, []string{"access_token", "refresh_token", "client_secret"}, key,
			"credential %q=%v must be classified sensitive", key, value)
	}
	require.True(t, IsSensitiveCredentialKey("client_secret"))
	require.False(t, IsSensitiveCredentialKey("profile_arn"))
	require.False(t, IsSensitiveCredentialKey("auth_method"))
}

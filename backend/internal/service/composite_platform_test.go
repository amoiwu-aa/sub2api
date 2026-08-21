package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestDetectModelPlatform(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		platform string
		ok       bool
	}{
		{name: "claude", model: "claude-sonnet-4-5", platform: PlatformAnthropic, ok: true},
		{name: "anthropic prefix", model: "anthropic/claude-opus-4-5", platform: PlatformAnthropic, ok: true},
		{name: "gpt", model: "gpt-5.1", platform: PlatformOpenAI, ok: true},
		{name: "o series", model: "o3-mini", platform: PlatformOpenAI, ok: true},
		{name: "embedding", model: "text-embedding-3-large", platform: PlatformOpenAI, ok: true},
		{name: "gemini", model: "gemini-3-pro", platform: PlatformGemini, ok: true},
		{name: "gemini models prefix", model: "models/gemini-2.5-flash", platform: PlatformGemini, ok: true},
		{name: "learnlm", model: "learnlm-2.0-flash-experimental", platform: PlatformGemini, ok: true},
		{name: "grok", model: "grok-4", platform: PlatformGrok, ok: true},
		{name: "xai prefix", model: "xai/grok-4", platform: PlatformGrok, ok: true},
		{name: "kiro prefix", model: "kiro/claude-sonnet-4.6", platform: PlatformKiro, ok: true},
		{name: "cursor prefix", model: "cursor/claude-opus-4-8", platform: PlatformCursor, ok: true},
		{name: "cursor prefix over gpt name", model: "cursor/gpt-5.6-sol", platform: PlatformCursor, ok: true},
		// 没有前缀就无法与官方 Claude 区分，必须继续判成 anthropic。
		{name: "kiro model without prefix stays anthropic", model: "claude-sonnet-4.6", platform: PlatformAnthropic, ok: true},
		{name: "unknown", model: "llama-4-maverick", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, ok := DetectModelPlatform(tt.model)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.platform, platform)
		})
	}
}

func TestQuotaPlatformCompositeUsesResolvedOrForceOnly(t *testing.T) {
	apiKey := &APIKey{Group: &Group{Platform: PlatformComposite}}

	require.Equal(t, "", QuotaPlatform(context.Background(), apiKey))
	require.Equal(t, PlatformGemini, QuotaPlatform(WithResolvedTargetPlatform(context.Background(), PlatformGemini), apiKey))
	require.Equal(t, PlatformAntigravity, QuotaPlatform(context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAntigravity), apiKey))

	ctx := WithResolvedTargetPlatform(context.Background(), PlatformAnthropic)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformAntigravity)
	require.Equal(t, PlatformAntigravity, QuotaPlatform(ctx, apiKey))
}

func TestCompositeGroupSchedulerHasAllCanonicalPlatformBuckets(t *testing.T) {
	seen := make(map[string]struct{})
	for _, bucket := range schedulerCanonicalBuckets(99) {
		seen[bucket.Platform] = struct{}{}
	}
	platforms := make([]string, 0, len(seen))
	for platform := range seen {
		platforms = append(platforms, platform)
	}
	require.ElementsMatch(t,
		[]string{
			PlatformAnthropic,
			PlatformGemini,
			PlatformOpenAI,
			PlatformAntigravity,
			PlatformGrok,
			PlatformCursor,
			PlatformKiro,
			PlatformKimi,
			PlatformZhipu,
			PlatformDeepseek,
		},
		platforms,
	)
}

// 调度快照的平台数组是硬编码长度，这里锁死它与 isConcreteRequestPlatform 同源，
// 避免新增平台时只改了一处导致该平台的账号永远进不了调度桶。
func TestSchedulerSnapshotPlatformsCoverConcretePlatforms(t *testing.T) {
	for _, platform := range schedulerSnapshotPlatforms() {
		require.True(t, isConcreteRequestPlatform(platform), "platform %s must be a concrete request platform", platform)
	}
	require.Len(t, schedulerSnapshotPlatforms(), 10)
}

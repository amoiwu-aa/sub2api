package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropicfp"
	"github.com/stretchr/testify/require"
)

// TestGatewayClientDatelineNormalization_Scope covers the account/switch matrix
// for the shouldNormalizeClientDateline gate: Anthropic OAuth/SetupToken pass
// only when the switch is on; API-Key and non-Anthropic platforms are excluded
// unconditionally.
func TestGatewayClientDatelineNormalization_Scope(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
	}
	ctx := context.Background()

	// Default (missing key): fallback in parseSettings/cache loader is true.
	require.True(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
	require.True(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))
	require.False(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}))
	require.False(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))

	// Switch off: no account qualifies.
	repo.data[SettingKeyEnableClientDatelineNormalization] = "false"
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	require.False(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
	require.False(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))

	// Switch back on: OAuth qualifies again.
	repo.data[SettingKeyEnableClientDatelineNormalization] = "true"
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	require.True(t, svc.shouldNormalizeClientDateline(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
}

// TestGatewayClientDatelineNormalization_HelperNoRewrite exercises the code
// path used by Forward: the helper must return ok=false when the switch is
// off, when the account is API-Key, when the account is nil, and when the
// body carries no fingerprinted dateline. It must return ok=true and a
// rewritten body when both the switch is on and the account is Anthropic
// OAuth/SetupToken and a rewrite actually happened.
func TestGatewayClientDatelineNormalization_HelperNoRewrite(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyEnableClientDatelineNormalization: "true",
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
	}
	ctx := context.Background()

	dirty := []byte(`{"messages":[{"role":"user","content":"<system-reminder>\nToday’s date is 2026/07/01.\n</system-reminder>"}]}`)
	clean := []byte(`{"messages":[{"role":"user","content":"just hello"}]}`)

	// API-Key account: never rewrites, even with dirty payload.
	next, ok := svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, dirty)
	require.False(t, ok)
	require.Nil(t, next)

	// Nil account: safe no-op.
	next, ok = svc.normalizeClientDatelineIfEnabled(ctx, nil, dirty)
	require.False(t, ok)
	require.Nil(t, next)

	// OAuth account + clean body: no changes, ok=false.
	next, ok = svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, clean)
	require.False(t, ok)
	require.Nil(t, next)

	// OAuth account + dirty body: rewritten, ok=true.
	next, ok = svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, dirty)
	require.True(t, ok)
	require.NotNil(t, next)
	require.Contains(t, string(next), "Today's date is 2026-07-01.")
	require.NotContains(t, string(next), "2026/07/01")
	require.NotContains(t, string(next), "Today’s date is")

	// SetupToken account + dirty body: rewritten, ok=true.
	next, ok = svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}, dirty)
	require.True(t, ok)
	require.Contains(t, string(next), "Today's date is 2026-07-01.")

	// Switch off: even OAuth account is not rewritten.
	repo.data[SettingKeyEnableClientDatelineNormalization] = "false"
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	next, ok = svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, dirty)
	require.False(t, ok)
	require.Nil(t, next)
}

// TestGatewayClientDatelineNormalization_LeavesUserProseUntouched double-checks
// that the pure normalizer never touches content outside <system-reminder>
// blocks. This is an integration guard between the switch-gated helper and
// the pkg/anthropicfp scope contract, tripped by anyone who broadens scope.
func TestGatewayClientDatelineNormalization_LeavesUserProseUntouched(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyEnableClientDatelineNormalization: "true",
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
	}
	ctx := context.Background()

	// User prose that happens to include a fingerprint-looking sentence
	// (outside <system-reminder>) must be preserved byte-for-byte.
	body := []byte(`{"messages":[{"role":"user","content":"I wrote: Today’s date is 2026/07/01. What do you think?"}]}`)
	next, ok := svc.normalizeClientDatelineIfEnabled(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, body)
	require.False(t, ok, "must not rewrite user prose outside <system-reminder>")
	require.Nil(t, next)

	// Direct pure-fn check for redundancy.
	out, hits, changed := anthropicfp.NormalizeDateline(body)
	require.False(t, changed)
	require.Empty(t, hits)
	require.Equal(t, body, out)
}

// TestDatelineHitLogThrottle 覆盖命中日志的节流判据：只在首次和跨越 10 的
// 整数次幂时放行，避免客户端每个请求都打标时把日志淹掉。
func TestDatelineHitLogThrottle(t *testing.T) {
	var logged []int64
	for n := int64(1); n <= 1000; n++ {
		if isFirstOrPowerOfTen(n) {
			logged = append(logged, n)
		}
	}
	require.Equal(t, []int64{1, 10, 100, 1000}, logged)
	require.False(t, isFirstOrPowerOfTen(0))
	require.False(t, isFirstOrPowerOfTen(-1))
}

// TestRecordDatelineHits_CountsPerVariant 确认计数按「账号 + 撇号变体 + 分隔符」
// 分桶：同一账号的不同指纹组合各自独立累计，互不干扰。
func TestRecordDatelineHits_CountsPerVariant(t *testing.T) {
	datelineHitCounts.Range(func(k, _ any) bool { datelineHitCounts.Delete(k); return true })
	ctx := context.Background()
	account := &Account{ID: 42}

	for i := 0; i < 3; i++ {
		recordDatelineHits(ctx, account, []anthropicfp.DatelineHit{{ApostropheVariant: "u2019", DateSeparator: "/"}})
	}
	recordDatelineHits(ctx, account, []anthropicfp.DatelineHit{{ApostropheVariant: "ascii", DateSeparator: "/"}})

	countOf := func(key string) int64 {
		v, ok := datelineHitCounts.Load(key)
		require.True(t, ok, "missing counter for %s", key)
		return v.(*atomic.Int64).Load()
	}
	require.Equal(t, int64(3), countOf("42|u2019|/"))
	require.Equal(t, int64(1), countOf("42|ascii|/"))

	// nil 账号与空命中都不应建桶
	recordDatelineHits(ctx, nil, []anthropicfp.DatelineHit{{ApostropheVariant: "u2019", DateSeparator: "-"}})
	recordDatelineHits(ctx, account, nil)
	_, ok := datelineHitCounts.Load("42|u2019|-")
	require.False(t, ok)
}

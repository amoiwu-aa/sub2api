package main

// IdC（IAM Identity Center：Enterprise / BuilderId / Internal）登录的命令行编排。
//
// 协议本身在 internal/pkg/kiro/idc.go，服务端后台走的是同一套。
// 这里只负责命令行特有的部分：起本地回调服务、拉浏览器、把结果打印出来。
//
// 它复用了 portal 那条链的本地回调服务器，因为两者同构——都是
// authorization_code + PKCE，只是授权页和 token 端点不同。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// loginIdC 跑完整条 IdC 链，产出可直接导入 RingStar 的凭证。
//
// issuerURL 与 region 来自 portal 回调（login_option=builderid/awsidc/internal），
// 也可以由用户直接指定。
// portalLoginOption 是 portal 那一步回调里的 login_option
// （builderid / awsidc / internal）。它决定凭证里的 provider，
// 必须从 portal 回调透传进来——IdC 自己那次回调是 AWS OIDC 发的，
// 不带 login_option，在那儿取只会永远拿到空值。
func loginIdC(opts options, client *http.Client, open func(string) error, issuerURL, region, portalLoginOption string) (authTokenFile, error) {
	var zero authTokenFile

	if strings.TrimSpace(issuerURL) == "" {
		return zero, errors.New("IdC 登录需要 issuer_url")
	}
	oidcBase, err := kiro.IdCOIDCBase(region)
	if err != nil {
		return zero, err
	}

	// 1. 注册客户端。clientId / clientSecret 必须随凭证落库：
	// 服务器上没有本机的 ~/.aws/sso/cache，少了这两项 token 过期即永久失效。
	reg, err := withTimeout(exchangeTimeout, func(ctx context.Context) (*kiro.IdCClientRegistration, error) {
		return kiro.RegisterIdCClient(ctx, client, oidcBase, issuerURL)
	})
	if err != nil {
		return zero, fmt.Errorf("注册 IdC 客户端: %w", err)
	}
	fmt.Printf("已注册 IdC 客户端: %s\n", reg.ClientID)

	state, err := randomUUID()
	if err != nil {
		return zero, fmt.Errorf("生成 state: %w", err)
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return zero, fmt.Errorf("生成 code_verifier: %w", err)
	}
	challenge := pkceChallenge(verifier)

	srv, err := startCallbackServer(state)
	if err != nil {
		return zero, err
	}
	defer srv.close()

	// 2/3. 把用户送进 AWS 的授权页，等回调。
	redirectURI := kiro.IdCCallbackRedirectURI(srv.port)
	authorizeURL := kiro.IdCAuthorizeURL(oidcBase, reg.ClientID, redirectURI, state, challenge)

	fmt.Printf("本地回调已就绪: %s\n", redirectURI)
	fmt.Printf("登录地址:\n  %s\n\n", authorizeURL)

	if opts.noBrowser {
		fmt.Println("已按 -no-browser 跳过自动打开，请手动在浏览器里打开上面的地址。")
	} else if err := open(authorizeURL); err != nil {
		fmt.Printf("自动打开浏览器失败（%v），请手动打开上面的地址。\n", err)
	}
	fmt.Printf("等待回调，超时 %s...\n", authFlowTimeout)

	cb, err := srv.wait(authFlowTimeout)
	if err != nil {
		return zero, err
	}
	if cb.Code == "" {
		return zero, errors.New("IdC 回调里没有 code")
	}

	// 4. 换 token。redirectURI 必须与 authorize 时用的完全一致。
	tok, err := withTimeout(exchangeTimeout, func(ctx context.Context) (*kiro.IdCTokenResponse, error) {
		return kiro.CreateIdCToken(ctx, client, oidcBase, reg, cb.Code, verifier, redirectURI)
	})
	if err != nil {
		return zero, fmt.Errorf("IdC 换 token: %w", err)
	}

	file := authTokenFile{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt(time.Now()).UTC().Format("2006-01-02T15:04:05.000Z"),
		AuthMethod:   kiro.AuthMethodIdC,
		Provider:     kiro.IdCProviderFromLoginOption(portalLoginOption),
		Region:       region,
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
	}
	if opts.ringstar {
		file.MachineID = machineID()
		file.KiroVersion = opts.kiroVersion
	}

	// 5. IdC 不像 social 那样直接给 profileArn，得再查一次。
	// 缺了它每个 Q API 请求都发不出去，所以这里失败就是硬失败。
	profileARN, profiles, err := withTimeout2(verifyTimeout, func(ctx context.Context) (string, []kiro.Profile, error) {
		return kiro.ResolveIdCProfileARN(ctx, client, &kiro.Credentials{
			AccessToken: file.AccessToken,
			AuthMethod:  kiro.AuthMethodIdC,
			Region:      file.Region,
			MachineID:   file.MachineID,
			KiroVersion: file.KiroVersion,
		})
	})
	if err != nil {
		return zero, err
	}
	if len(profiles) > 1 {
		fmt.Printf("该账号有 %d 个 profile，取第一个：\n", len(profiles))
		for _, p := range profiles {
			fmt.Printf("  - %s %s\n", p.ARN, p.ProfileName)
		}
	}
	file.ProfileARN = profileARN
	fmt.Printf("已解析 profileArn: %s\n", profileARN)

	return file, nil
}

// withTimeout 给一次共享包调用套上命令行这边的超时。
func withTimeout[T any](timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx)
}

func withTimeout2[A, B any](timeout time.Duration, fn func(context.Context) (A, B, error)) (A, B, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx)
}

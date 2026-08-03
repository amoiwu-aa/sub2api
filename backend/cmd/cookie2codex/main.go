// Command cookie2codex converts a ChatGPT browser session into the access-token
// JSON accepted by RingStar's "Codex OAuth auth.json / AT" account importer.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/chatgptcookie"
)

const (
	defaultOutputPath = "codex-session.json"
	requestTimeout    = 30 * time.Second
)

type options struct {
	inputPath string
	output    string
	endpoint  string
	proxy     string
	userAgent string
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "转换失败: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now func() time.Time) error {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}

	raw, err := readInput(opts.inputPath, stdin)
	if err != nil {
		return err
	}
	client, err := buildHTTPClient(opts.proxy)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	result, err := chatgptcookie.Convert(ctx, client, raw, chatgptcookie.Options{
		Endpoint:  opts.endpoint,
		UserAgent: opts.userAgent,
		Now:       now().UTC(),
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "已读取 %d 个 Cookie（%s），已通过 %s 验证登录态。\n",
		result.CookieCount, result.InputFormat, result.EndpointHost)

	data, err := json.MarshalIndent(result.Credential, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Codex Session: %w", err)
	}
	data = append(data, '\n')
	if err := writeOutput(opts.output, data, stdout); err != nil {
		return err
	}

	if opts.output == "-" {
		fmt.Fprintf(stderr, "转换成功；accessToken 过期时间：%s\n", result.TokenExpiresAt.Format(time.RFC3339))
	} else {
		absPath, _ := filepath.Abs(opts.output)
		fmt.Fprintf(stderr, "转换成功：%s\n", absPath)
		fmt.Fprintf(stderr, "accessToken 过期时间：%s\n", result.TokenExpiresAt.Format(time.RFC3339))
	}
	fmt.Fprintln(stderr, "可将产物粘贴到 RingStar「新建 OpenAI 账号 → Codex OAuth auth.json / AT 导入」。")
	fmt.Fprintln(stderr, "注意：浏览器会话通常不提供 refresh_token，令牌到期后需重新转换。")
	return nil
}

func parseFlags(args []string, output io.Writer) (options, error) {
	var opts options
	if output == nil {
		output = io.Discard
	}
	fs := flag.NewFlagSet("cookie2codex", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintln(output, "用法：go run ./cmd/cookie2codex -i <Cookie-Editor 导出文件> [参数]")
		fmt.Fprintln(output)
		fs.PrintDefaults()
	}
	fs.StringVar(&opts.inputPath, "i", "", "Cookie-Editor 导出文件；- 表示从标准输入读取")
	fs.StringVar(&opts.output, "o", defaultOutputPath, "输出文件；- 表示写到标准输出")
	fs.StringVar(&opts.endpoint, "endpoint", "", "覆盖 ChatGPT session 端点（仅允许官方 HTTPS 域名）")
	fs.StringVar(&opts.proxy, "proxy", "", "HTTP/HTTPS/SOCKS5 代理，例如 socks5://127.0.0.1:1080")
	fs.StringVar(&opts.userAgent, "user-agent", chatgptcookie.DefaultUserAgent, "请求 User-Agent；遇到 Cloudflare 时应与导出 Cookie 的浏览器一致")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("不接受位置参数，请使用 -i 指定输入文件")
	}
	if strings.TrimSpace(opts.inputPath) == "" {
		return opts, errors.New("必须使用 -i 指定 Cookie 文件，或用 -i - 从标准输入读取")
	}
	if strings.TrimSpace(opts.output) == "" {
		return opts, errors.New("-o 不能为空")
	}
	if strings.TrimSpace(opts.userAgent) == "" {
		return opts, errors.New("-user-agent 不能为空")
	}
	return opts, nil
}

func readInput(inputPath string, stdin io.Reader) ([]byte, error) {
	var reader io.Reader
	var closer io.Closer
	if inputPath == "-" {
		reader = stdin
	} else {
		file, err := os.Open(inputPath)
		if err != nil {
			return nil, fmt.Errorf("读取 Cookie 文件 %s: %w", inputPath, err)
		}
		reader = file
		closer = file
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	raw, err := io.ReadAll(io.LimitReader(reader, chatgptcookie.MaxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 Cookie 输入: %w", err)
	}
	if len(raw) > chatgptcookie.MaxInputBytes {
		return nil, fmt.Errorf("Cookie 输入超过 %d MiB 限制", chatgptcookie.MaxInputBytes>>20)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("Cookie 输入为空")
	}
	return raw, nil
}

func buildHTTPClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	if strings.TrimSpace(proxy) != "" {
		proxyURL, err := url.Parse(strings.TrimSpace(proxy))
		if err != nil {
			return nil, fmt.Errorf("代理地址非法: %w", err)
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("不支持的代理协议 %q", proxyURL.Scheme)
		}
		if proxyURL.Hostname() == "" {
			return nil, errors.New("代理地址缺少主机")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
	}, nil
}

func writeOutput(outputPath string, data []byte, stdout io.Writer) error {
	if outputPath == "-" {
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("写入标准输出: %w", err)
		}
		return nil
	}

	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建输出文件 %s: %w", outputPath, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("设置输出文件权限 %s: %w", outputPath, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入输出文件 %s: %w", outputPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭输出文件 %s: %w", outputPath, err)
	}
	return nil
}

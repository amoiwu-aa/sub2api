// Package chatgptcookie converts a ChatGPT browser session exported by
// Cookie-Editor into the access-token JSON accepted by RingStar's Codex
// account importer.
package chatgptcookie

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSessionEndpoint = "https://chatgpt.com/api/auth/session"
	LegacySessionEndpoint  = "https://chat.openai.com/api/auth/session"
	DefaultUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"

	MaxInputBytes     = 8 << 20
	MaxUserAgentBytes = 4096

	maxResponseBytes    = 2 << 20
	defaultTokenSkew    = 2 * time.Minute
	defaultRequestLimit = 30 * time.Second
)

// ErrorKind lets HTTP callers map conversion failures without inspecting text.
type ErrorKind string

const (
	ErrorInvalidInput     ErrorKind = "invalid_input"
	ErrorSessionRejected  ErrorKind = "session_rejected"
	ErrorRateLimited      ErrorKind = "rate_limited"
	ErrorUpstream         ErrorKind = "upstream_error"
	ErrorUpstreamProtocol ErrorKind = "upstream_protocol"
	ErrorInternal         ErrorKind = "internal_error"
)

// ConversionError classifies a safe, credential-free error message.
type ConversionError struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *ConversionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ConversionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// KindOf returns the classification of err.
func KindOf(err error) ErrorKind {
	var conversionErr *ConversionError
	if errors.As(err, &conversionErr) {
		return conversionErr.Kind
	}
	return ErrorInternal
}

// Options controls one browser-session conversion.
type Options struct {
	Endpoint  string
	UserAgent string
	Now       time.Time
}

// Credential is intentionally limited to fields understood by the existing
// Codex session importer. Browser cookies are never copied into it.
type Credential struct {
	User         map[string]any `json:"user,omitempty"`
	Account      map[string]any `json:"account,omitempty"`
	AccessToken  string         `json:"accessToken"`
	ExpiresAt    string         `json:"expiresAt"`
	Expires      string         `json:"expires,omitempty"`
	AuthProvider string         `json:"authProvider,omitempty"`
}

// Result includes non-secret diagnostics useful to CLI and HTTP callers.
type Result struct {
	Credential     Credential
	TokenExpiresAt time.Time
	InputFormat    string
	CookieCount    int
	EndpointHost   string
}

var sessionCookieFamilies = []string{
	"__Secure-next-auth.session-token",
	"next-auth.session-token",
	"__Secure-authjs.session-token",
	"authjs.session-token",
}

type browserCookie struct {
	Domain    string
	HostOnly  bool
	Path      string
	Secure    bool
	ExpiresAt int64
	Name      string
	Value     string
}

type cookieJSON struct {
	Domain         string   `json:"domain"`
	HostOnly       bool     `json:"hostOnly"`
	Path           string   `json:"path"`
	Secure         bool     `json:"secure"`
	ExpirationDate *float64 `json:"expirationDate"`
	Expiration     *float64 `json:"expiration"`
	Name           string   `json:"name"`
	Value          string   `json:"value"`
}

// Convert parses, validates, exchanges and sanitizes one Cookie-Editor export.
// The provided client is shallow-cloned so redirects can be disabled without
// mutating a shared client pool.
func Convert(ctx context.Context, client *http.Client, raw []byte, opts Options) (*Result, error) {
	if client == nil {
		return nil, conversionError(ErrorInternal, "HTTP client is unavailable", nil)
	}
	if len(raw) > MaxInputBytes {
		return nil, conversionError(
			ErrorInvalidInput,
			fmt.Sprintf("Cookie input exceeds the %d MiB limit", MaxInputBytes>>20),
			nil,
		)
	}

	cookies, inputFormat, err := parseCookieInput(raw)
	if err != nil {
		return nil, conversionError(ErrorInvalidInput, err.Error(), err)
	}

	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		endpoint = inferSessionEndpoint(cookies)
	}
	endpointURL, err := validateSessionEndpoint(endpoint)
	if err != nil {
		return nil, conversionError(ErrorInvalidInput, err.Error(), err)
	}

	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	requestCookies, err := cookiesForURL(cookies, endpointURL, now)
	if err != nil {
		return nil, conversionError(ErrorInvalidInput, err.Error(), err)
	}
	cookieHeader, err := buildCookieHeader(requestCookies)
	if err != nil {
		return nil, conversionError(ErrorInvalidInput, err.Error(), err)
	}

	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	if len(userAgent) > MaxUserAgentBytes {
		return nil, conversionError(ErrorInvalidInput, "User-Agent exceeds the size limit", nil)
	}
	if strings.ContainsAny(userAgent, "\x00\r\n") {
		return nil, conversionError(ErrorInvalidInput, "User-Agent contains invalid characters", nil)
	}

	safeClient := *client
	// A pooled browser-impersonation client may carry a cookie jar. Never mix
	// session cookies between imports or persist Set-Cookie responses in it.
	safeClient.Jar = nil
	safeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if safeClient.Timeout <= 0 || safeClient.Timeout > defaultRequestLimit {
		safeClient.Timeout = defaultRequestLimit
	}

	credential, expiry, err := exchangeSession(
		ctx,
		&safeClient,
		endpointURL,
		cookieHeader,
		userAgent,
		now,
	)
	if err != nil {
		return nil, err
	}
	return &Result{
		Credential:     *credential,
		TokenExpiresAt: expiry,
		InputFormat:    inputFormat,
		CookieCount:    len(requestCookies),
		EndpointHost:   endpointURL.Hostname(),
	}, nil
}

func conversionError(kind ErrorKind, message string, cause error) error {
	return &ConversionError{Kind: kind, Message: message, Cause: cause}
}

func parseCookieInput(raw []byte) ([]browserCookie, string, error) {
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}))
	if len(raw) == 0 {
		return nil, "", errors.New("Cookie input is empty")
	}

	switch raw[0] {
	case '[', '{':
		cookies, err := parseJSONCookies(raw)
		if err != nil {
			return nil, "", fmt.Errorf("parse Cookie-Editor JSON: %w", err)
		}
		return cookies, "Cookie-Editor JSON", nil
	}

	text := string(raw)
	if looksLikeNetscape(text) {
		cookies, err := parseNetscapeCookies(text)
		if err != nil {
			return nil, "", fmt.Errorf("parse Netscape cookies: %w", err)
		}
		return cookies, "Netscape", nil
	}

	cookies, err := parseHeaderCookies(text)
	if err != nil {
		return nil, "", fmt.Errorf("parse Cookie header: %w", err)
	}
	return cookies, "Header String", nil
}

func parseJSONCookies(raw []byte) ([]browserCookie, error) {
	var inputs []cookieJSON
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &inputs); err != nil {
			return nil, err
		}
	} else {
		var wrapper struct {
			Cookies []cookieJSON `json:"cookies"`
		}
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return nil, err
		}
		if len(wrapper.Cookies) > 0 {
			inputs = wrapper.Cookies
		} else {
			var single cookieJSON
			if err := json.Unmarshal(raw, &single); err != nil {
				return nil, err
			}
			if strings.TrimSpace(single.Name) != "" {
				inputs = []cookieJSON{single}
			}
		}
	}
	if len(inputs) == 0 {
		return nil, errors.New("no cookies found")
	}

	cookies := make([]browserCookie, 0, len(inputs))
	for i, input := range inputs {
		expiresAt := int64(0)
		switch {
		case input.ExpirationDate != nil:
			expiresAt = int64(*input.ExpirationDate)
		case input.Expiration != nil:
			expiresAt = int64(*input.Expiration)
		}
		cookie := browserCookie{
			Domain:    strings.TrimSpace(input.Domain),
			HostOnly:  input.HostOnly,
			Path:      strings.TrimSpace(input.Path),
			Secure:    input.Secure,
			ExpiresAt: expiresAt,
			Name:      strings.TrimSpace(input.Name),
			Value:     input.Value,
		}
		if err := validateBrowserCookie(cookie); err != nil {
			return nil, fmt.Errorf("cookie %d: %w", i+1, err)
		}
		cookies = append(cookies, cookie)
	}
	return cookies, nil
}

func looksLikeNetscape(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "# Netscape HTTP Cookie File") ||
			strings.HasPrefix(line, "#HttpOnly_") ||
			strings.Count(line, "\t") == 6
	}
	return false
}

func parseNetscapeCookies(text string) ([]browserCookie, error) {
	var cookies []browserCookie
	for lineNumber, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("line %d must have 7 columns, got %d", lineNumber+1, len(fields))
		}
		expiresAt, err := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d has an invalid expiry", lineNumber+1)
		}
		includeSubdomains := strings.EqualFold(strings.TrimSpace(fields[1]), "TRUE")
		cookie := browserCookie{
			Domain:    strings.TrimSpace(fields[0]),
			HostOnly:  !includeSubdomains,
			Path:      strings.TrimSpace(fields[2]),
			Secure:    strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE"),
			ExpiresAt: expiresAt,
			Name:      strings.TrimSpace(fields[5]),
			Value:     fields[6],
		}
		if err := validateBrowserCookie(cookie); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		cookies = append(cookies, cookie)
	}
	if len(cookies) == 0 {
		return nil, errors.New("no cookies found")
	}
	return cookies, nil
}

func parseHeaderCookies(text string) ([]browserCookie, error) {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, "\r\n") {
		return nil, errors.New("Cookie header cannot contain newlines")
	}
	if len(text) >= len("Cookie:") && strings.EqualFold(text[:len("Cookie:")], "Cookie:") {
		text = strings.TrimSpace(text[len("Cookie:"):])
	}

	var cookies []browserCookie
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, errors.New("a Cookie header entry is missing '='")
		}
		cookie := browserCookie{
			Path:  "/",
			Name:  strings.TrimSpace(name),
			Value: value,
		}
		if err := validateBrowserCookie(cookie); err != nil {
			return nil, err
		}
		cookies = append(cookies, cookie)
	}
	if len(cookies) == 0 {
		return nil, errors.New("no cookies found")
	}
	return cookies, nil
}

func validateBrowserCookie(cookie browserCookie) error {
	if cookie.Name == "" {
		return errors.New("cookie name is empty")
	}
	for _, r := range cookie.Name {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={} \t", r) {
			return errors.New("cookie name is invalid")
		}
	}
	if strings.ContainsAny(cookie.Value, "\x00\r\n;") {
		return errors.New("cookie value contains invalid characters")
	}
	return nil
}

func inferSessionEndpoint(cookies []browserCookie) string {
	sawOpenAI := false
	for _, cookie := range cookies {
		domain := normalizeDomain(cookie.Domain)
		if domain == "chatgpt.com" || strings.HasSuffix(domain, ".chatgpt.com") {
			return DefaultSessionEndpoint
		}
		if domain == "openai.com" || strings.HasSuffix(domain, ".openai.com") {
			sawOpenAI = true
		}
	}
	if sawOpenAI {
		return LegacySessionEndpoint
	}
	return DefaultSessionEndpoint
}

func validateSessionEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid session endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return nil, errors.New("session endpoint must use HTTPS")
	}
	if u.User != nil {
		return nil, errors.New("session endpoint cannot contain user information")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("session endpoint cannot contain a query or fragment")
	}
	if port := u.Port(); port != "" && port != "443" {
		return nil, fmt.Errorf("session endpoint only permits HTTPS port 443, got %s", port)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !officialSessionHost(host) {
		return nil, fmt.Errorf("refusing to send browser cookies to non-official host %q", host)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/auth/session"
	}
	return u, nil
}

func officialSessionHost(host string) bool {
	return host == "chatgpt.com" ||
		strings.HasSuffix(host, ".chatgpt.com") ||
		host == "openai.com" ||
		strings.HasSuffix(host, ".openai.com")
}

func cookiesForURL(cookies []browserCookie, target *url.URL, now time.Time) ([]browserCookie, error) {
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	requestPath := target.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}

	filtered := make([]browserCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.ExpiresAt > 0 && !time.Unix(cookie.ExpiresAt, 0).After(now) {
			continue
		}
		if cookie.Secure && target.Scheme != "https" {
			continue
		}
		if !cookieDomainMatches(cookie, host) {
			continue
		}
		if !cookiePathMatches(cookie.Path, requestPath) {
			continue
		}
		filtered = append(filtered, cookie)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no valid cookies apply to %s", host)
	}

	hasSessionCookie := false
	for _, cookie := range filtered {
		if isSessionCookie(cookie.Name) {
			hasSessionCookie = true
			break
		}
	}
	if !hasSessionCookie {
		return nil, errors.New("no ChatGPT session cookie found; export cookies again from a signed-in chatgpt.com page")
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		leftPath := normalizedCookiePath(filtered[i].Path)
		rightPath := normalizedCookiePath(filtered[j].Path)
		if len(leftPath) != len(rightPath) {
			return len(leftPath) > len(rightPath)
		}
		if filtered[i].Name != filtered[j].Name {
			leftFamily, leftChunk, leftIsSession := parseSessionCookieName(filtered[i].Name)
			rightFamily, rightChunk, rightIsSession := parseSessionCookieName(filtered[j].Name)
			if leftIsSession && rightIsSession && leftFamily == rightFamily && leftChunk != rightChunk {
				return leftChunk < rightChunk
			}
			return filtered[i].Name < filtered[j].Name
		}
		return filtered[i].Domain < filtered[j].Domain
	})
	return filtered, nil
}

func cookieDomainMatches(cookie browserCookie, host string) bool {
	domain := normalizeDomain(cookie.Domain)
	if domain == "" {
		return true
	}
	if cookie.HostOnly {
		return host == domain
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(domain), "."), "."))
}

func normalizedCookiePath(cookiePath string) string {
	cookiePath = strings.TrimSpace(cookiePath)
	if cookiePath == "" || cookiePath[0] != '/' {
		return "/"
	}
	return cookiePath
}

func cookiePathMatches(cookiePath, requestPath string) bool {
	cookiePath = normalizedCookiePath(cookiePath)
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") ||
		(len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func isSessionCookie(name string) bool {
	_, _, ok := parseSessionCookieName(name)
	return ok
}

func parseSessionCookieName(name string) (family string, chunk int64, ok bool) {
	for _, family := range sessionCookieFamilies {
		if name == family {
			return family, -1, true
		}
		suffix := strings.TrimPrefix(name, family+".")
		if suffix != name && suffix != "" {
			if parsed, err := strconv.ParseUint(suffix, 10, 31); err == nil {
				return family, int64(parsed), true
			}
		}
	}
	return "", 0, false
}

func buildCookieHeader(cookies []browserCookie) (string, error) {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if err := validateBrowserCookie(cookie); err != nil {
			return "", err
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	if len(parts) == 0 {
		return "", errors.New("no cookies are available to send")
	}
	return strings.Join(parts, "; "), nil
}

func exchangeSession(
	ctx context.Context,
	client *http.Client,
	endpoint *url.URL,
	cookieHeader string,
	userAgent string,
	now time.Time,
) (*Credential, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, time.Time{}, conversionError(ErrorInternal, "failed to build ChatGPT session request", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", endpoint.Scheme+"://"+endpoint.Host+"/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, conversionError(
			ErrorUpstream,
			"failed to reach the ChatGPT session endpoint",
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, time.Time{}, conversionError(
			ErrorUpstream,
			"failed to read the ChatGPT session response",
			err,
		)
	}
	if len(raw) > maxResponseBytes {
		return nil, time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"ChatGPT session response is too large",
			nil,
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, time.Time{}, sessionHTTPError(resp.StatusCode)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"ChatGPT session endpoint did not return valid JSON",
			err,
		)
	}

	accessToken := firstString(payload, "accessToken", "access_token")
	if accessToken == "" {
		return nil, time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"ChatGPT session response did not contain an accessToken; the session may be expired or the upstream protocol changed",
			nil,
		)
	}
	expiry, err := validateAccessToken(accessToken, now)
	if err != nil {
		return nil, time.Time{}, err
	}

	output := &Credential{
		User:         selectStringFields(payload["user"], "id", "name", "email", "image", "picture"),
		Account:      selectStringFields(payload["account"], "id", "account_id", "planType", "plan_type", "structure", "residencyRegion", "computeResidency"),
		AccessToken:  accessToken,
		ExpiresAt:    expiry.UTC().Format(time.RFC3339),
		Expires:      firstString(payload, "expires"),
		AuthProvider: firstString(payload, "authProvider", "auth_provider"),
	}
	return output, expiry, nil
}

func sessionHTTPError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return conversionError(
			ErrorSessionRejected,
			"ChatGPT rejected the session cookie (HTTP 401); sign in again and export fresh cookies",
			nil,
		)
	case http.StatusForbidden:
		return conversionError(
			ErrorSessionRejected,
			"ChatGPT rejected the request (HTTP 403); use the same proxy/IP and User-Agent as the browser that exported the cookies",
			nil,
		)
	case http.StatusTooManyRequests:
		return conversionError(
			ErrorRateLimited,
			"ChatGPT rate-limited the session request (HTTP 429); try again later",
			nil,
		)
	default:
		if status >= 300 && status < 400 {
			return conversionError(
				ErrorSessionRejected,
				fmt.Sprintf("ChatGPT redirected the session request (HTTP %d); the cookies are expired or scoped to another domain", status),
				nil,
			)
		}
		return conversionError(
			ErrorUpstream,
			fmt.Sprintf("ChatGPT session endpoint returned HTTP %d", status),
			nil,
		)
	}
}

func validateAccessToken(token string, now time.Time) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"ChatGPT returned a non-JWT accessToken, so its expiry cannot be verified safely",
			nil,
		)
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"failed to decode the ChatGPT accessToken JWT payload",
			err,
		)
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}, conversionError(
			ErrorUpstreamProtocol,
			"ChatGPT accessToken does not contain a valid exp",
			err,
		)
	}
	expiry := time.Unix(claims.ExpiresAt, 0).UTC()
	if !expiry.After(now.Add(defaultTokenSkew)) {
		return time.Time{}, conversionError(
			ErrorSessionRejected,
			fmt.Sprintf("ChatGPT accessToken is expired or about to expire at %s", expiry.Format(time.RFC3339)),
			nil,
		)
	}
	return expiry, nil
}

func decodeJWTSegment(segment string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(segment)
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func selectStringFields(value any, keys ...string) map[string]any {
	source, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	selected := make(map[string]any)
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			selected[key] = value
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

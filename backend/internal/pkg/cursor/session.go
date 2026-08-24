package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TokenTypeWeb     = "web"
	TokenTypeSession = "session"
)

const (
	WebsiteBaseURL                   = "https://www.cursor.com"
	EndpointLoginDeepCallbackControl = "/api/auth/loginDeepCallbackControl"
	SessionCookieName                = "WorkosCursorSessionToken"
	cookieSeparatorEncoded           = "%3A%3A"
)

const (
	defaultBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/132.0.6834.210 Safari/537.36"
	defaultClientUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Cursor/" + DefaultClientVersion + " Chrome/132.0.6834.210 Electron/34.3.4 Safari/537.36"
	defaultPollAttempts = 30
	defaultPollInterval = time.Second
	maxAuthBody         = 1 << 20
)

var (
	ErrWebSessionUnauthorized = errors.New("cursor: web session token is unauthorized")
	ErrDeepLoginPending       = errors.New("cursor: deep-link login has not completed")
	ErrWebSessionNotUpgraded  = errors.New("cursor: exchange returned a web token, not a client token")
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type TokenClaims struct {
	Subject   string
	UserID    string
	Type      string
	Scope     string
	Audience  string
	Issuer    string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

func (claims TokenClaims) IsWeb() bool { return claims.Type == TokenTypeWeb }

func ParseTokenClaims(raw string) (TokenClaims, bool) {
	token, uid := ParseToken(raw)
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return TokenClaims{}, false
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return TokenClaims{}, false
	}
	var body struct {
		Sub   string `json:"sub"`
		Type  string `json:"type"`
		Scope string `json:"scope"`
		Aud   string `json:"aud"`
		Iss   string `json:"iss"`
		Iat   int64  `json:"iat"`
		Exp   int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return TokenClaims{}, false
	}
	claims := TokenClaims{
		Subject: strings.TrimSpace(body.Sub), UserID: uid,
		Type: strings.ToLower(strings.TrimSpace(body.Type)), Scope: strings.TrimSpace(body.Scope),
		Audience: strings.TrimSpace(body.Aud), Issuer: strings.TrimSpace(body.Iss),
	}
	if body.Iat > 0 {
		claims.IssuedAt = time.Unix(body.Iat, 0)
	}
	if body.Exp > 0 {
		claims.ExpiresAt = time.Unix(body.Exp, 0)
	}
	return claims, true
}

func TokenType(raw string) string {
	claims, ok := ParseTokenClaims(raw)
	if !ok {
		return ""
	}
	return claims.Type
}

func IsWebSessionToken(raw string) bool { return TokenType(raw) == TokenTypeWeb }

func NormalizeSessionCookie(raw string) string {
	token, uid := ParseToken(raw)
	if token == "" {
		return ""
	}
	if uid == "" {
		return token
	}
	return uid + cookieSeparatorEncoded + token
}

func decodeCookieSeparator(raw string) string {
	width := len(cookieSeparatorEncoded)
	for i := 0; i+width <= len(raw); i++ {
		if strings.EqualFold(raw[i:i+width], cookieSeparatorEncoded) {
			return raw[:i] + "::" + raw[i+width:]
		}
	}
	return raw
}

type DeepLogin struct {
	UUID      string
	Verifier  string
	Challenge string
	LoginURL  string
}

func BuildDeepLoginURL() (*DeepLogin, error) {
	verifier, challenge, id, err := NewDeepLinkChallenge()
	if err != nil {
		return nil, err
	}
	return &DeepLogin{UUID: id, Verifier: verifier, Challenge: challenge, LoginURL: BuildLoginDeepControlURL(challenge, id)}, nil
}

type ExchangeOptions struct {
	HTTPClient     HTTPDoer
	WebsiteBaseURL string
	APIBaseURL     string
	PollAttempts   int
	PollInterval   time.Duration
	Sleep          func(context.Context, time.Duration) error
}

func (options ExchangeOptions) client() HTTPDoer {
	if options.HTTPClient != nil {
		return options.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (options ExchangeOptions) websiteURL(path string) string {
	base := strings.TrimSpace(options.WebsiteBaseURL)
	if base == "" {
		base = WebsiteBaseURL
	}
	return strings.TrimRight(base, "/") + path
}

func (options ExchangeOptions) apiURL(path string) string {
	base := strings.TrimSpace(options.APIBaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	return strings.TrimRight(base, "/") + path
}

func (options ExchangeOptions) attempts() int {
	if options.PollAttempts > 0 {
		return options.PollAttempts
	}
	return defaultPollAttempts
}

func (options ExchangeOptions) interval() time.Duration {
	if options.PollInterval > 0 {
		return options.PollInterval
	}
	return defaultPollInterval
}

func (options ExchangeOptions) sleep(ctx context.Context, duration time.Duration) error {
	if options.Sleep != nil {
		return options.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ApproveDeepLogin(ctx context.Context, options ExchangeOptions, workosSessionToken, id, challenge string) error {
	cookie := NormalizeSessionCookie(workosSessionToken)
	if cookie == "" {
		return ErrWebSessionUnauthorized
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(challenge) == "" {
		return errors.New("cursor: deep-link approval needs a uuid and a challenge")
	}
	payload, err := json.Marshal(map[string]string{"uuid": id, "challenge": challenge})
	if err != nil {
		return errors.New("cursor: encode approval body")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, options.websiteURL(EndpointLoginDeepCallbackControl), bytes.NewReader(payload))
	if err != nil {
		return safeRequestError(ctx, "cursor: build approval request")
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", defaultBrowserUserAgent)
	req.Header.Set("origin", strings.TrimSuffix(options.websiteURL(""), "/"))
	req.Header.Set("referer", strings.TrimSuffix(options.websiteURL(""), "/")+"/")
	req.Header.Set("cookie", SessionCookieName+"="+cookie)

	resp, err := options.client().Do(req)
	if err != nil {
		drainAndClose(resp)
		return safeRequestError(ctx, "cursor: approve deep-link login: request failed")
	}
	if resp == nil {
		return errors.New("cursor: approve deep-link login: empty response")
	}
	defer drainAndClose(resp)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrWebSessionUnauthorized
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("cursor: approve deep-link login: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func PollDeepLoginOnce(ctx context.Context, options ExchangeOptions, id, verifier string) (*TokenResponse, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(verifier) == "" {
		return nil, errors.New("cursor: deep-link poll needs a uuid and a verifier")
	}
	query := url.Values{}
	query.Set("uuid", id)
	query.Set("verifier", verifier)
	target := options.apiURL(EndpointAuthPoll) + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, safeRequestError(ctx, "cursor: build poll request")
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("user-agent", defaultClientUserAgent)

	resp, err := options.client().Do(req)
	if err != nil {
		drainAndClose(resp)
		return nil, safeRequestError(ctx, "cursor: poll deep-link login: request failed")
	}
	if resp == nil {
		return nil, errors.New("cursor: poll deep-link login: empty response")
	}
	defer drainAndClose(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cursor: poll deep-link login: unexpected status %d", resp.StatusCode)
	}
	body, err := readAuthBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cursor: read poll response: %w", err)
	}
	var token TokenResponse
	if json.Unmarshal(body, &token) != nil {
		return nil, errors.New("cursor: decode poll response")
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, nil
	}
	return &token, nil
}

func PollDeepLogin(ctx context.Context, options ExchangeOptions, id, verifier string) (*TokenResponse, error) {
	attempts := options.attempts()
	interval := options.interval()
	var hadError bool
	for i := 0; i < attempts; i++ {
		token, err := PollDeepLoginOnce(ctx, options, id, verifier)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			hadError = true
		}
		if token != nil {
			return token, nil
		}
		if i < attempts-1 {
			if err := options.sleep(ctx, interval); err != nil {
				return nil, err
			}
		}
	}
	if hadError {
		return nil, fmt.Errorf("%w: last poll failed", ErrDeepLoginPending)
	}
	return nil, ErrDeepLoginPending
}

func ExchangeWebSessionWithOptions(ctx context.Context, workosSessionToken string, options ExchangeOptions) (*TokenResponse, error) {
	if NormalizeSessionCookie(workosSessionToken) == "" {
		return nil, ErrWebSessionUnauthorized
	}
	login, err := BuildDeepLoginURL()
	if err != nil {
		return nil, err
	}
	if err := ApproveDeepLogin(ctx, options, workosSessionToken, login.UUID, login.Challenge); err != nil {
		return nil, err
	}
	token, err := PollDeepLogin(ctx, options, login.UUID, login.Verifier)
	if err != nil {
		return nil, err
	}
	if IsWebSessionToken(token.AccessToken) {
		return nil, ErrWebSessionNotUpgraded
	}
	return token, nil
}

func ExchangeWebSession(ctx context.Context, client HTTPDoer, workosSessionToken string) (string, string, error) {
	token, err := ExchangeWebSessionWithOptions(ctx, workosSessionToken, ExchangeOptions{HTTPClient: client})
	if err != nil {
		return "", "", err
	}
	return token.AccessToken, token.RefreshToken, nil
}

func readAuthBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, errors.New("response body is missing")
	}
	data, err := io.ReadAll(io.LimitReader(body, maxAuthBody+1))
	if err != nil {
		return nil, errors.New("response read failed")
	}
	if len(data) > maxAuthBody {
		return nil, errors.New("response body too large")
	}
	return data, nil
}

func safeRequestError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New(message)
}

func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxAuthBody+1))
	_ = resp.Body.Close()
}

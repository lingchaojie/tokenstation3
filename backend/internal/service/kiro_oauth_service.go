package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	KiroSocialRedirectURI = "kiro://kiro.kiroAgent/authenticate-success"

	kiroOAuthUserAgent             = "cli-proxy-api/1.0.0"
	kiroBuilderIDMethod            = "builder-id"
	kiroBuilderIDProvider          = "AWS"
	kiroBuilderIDStartURL          = "https://view.awsapps.com/start"
	kiroDeviceCodeGrantType        = "urn:ietf:params:oauth:grant-type:device_code"
	kiroOAuthDefaultDeviceInterval = 5
)

type KiroOAuthService struct {
	proxyRepo    ProxyRepository
	authEndpoint string
	oidcEndpoint string

	mu       sync.Mutex
	sessions map[string]*kiroOAuthSession
}

func NewKiroOAuthService(proxyRepo ProxyRepository) *KiroOAuthService {
	return &KiroOAuthService{
		proxyRepo:    proxyRepo,
		authEndpoint: kiroAuthEndpoint,
		oidcEndpoint: kiroSSOOIDCEndpoint,
		sessions:     make(map[string]*kiroOAuthSession),
	}
}

type KiroOAuthStartInput struct {
	Method  string
	ProxyID *int64
}

type KiroOAuthStartResult struct {
	Mode                    string `json:"mode"`
	Method                  string `json:"method"`
	AuthURL                 string `json:"auth_url,omitempty"`
	SessionID               string `json:"session_id"`
	State                   string `json:"state,omitempty"`
	VerificationURI         string `json:"verification_uri,omitempty"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	UserCode                string `json:"user_code,omitempty"`
	ExpiresIn               int    `json:"expires_in,omitempty"`
	Interval                int    `json:"interval,omitempty"`
}

type KiroOAuthExchangeCodeInput struct {
	SessionID string
	State     string
	Code      string
	ProxyID   *int64
}

type KiroOAuthPollInput struct {
	SessionID string
	ProxyID   *int64
}

type KiroOAuthPollResult struct {
	Status    string              `json:"status"`
	TokenInfo *KiroOAuthTokenInfo `json:"token_info,omitempty"`
	Interval  int                 `json:"interval,omitempty"`
}

type KiroOAuthTokenInfo struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ProfileARN   string `json:"profile_arn,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	AuthMethod   string `json:"auth_method,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	Email        string `json:"email,omitempty"`
}

type kiroOAuthSession struct {
	Method       string
	State        string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	DeviceCode   string
	ProxyURL     string
	ExpiresAt    time.Time
	Interval     int
	Provider     string
}

type kiroRegisterClientResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type kiroDeviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type kiroSocialTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

type kiroBuilderTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
}

type kiroOAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *KiroOAuthService) GenerateAuthURL(ctx context.Context, input KiroOAuthStartInput) (*KiroOAuthStartResult, error) {
	method, provider, err := normalizeKiroOAuthMethod(input.Method)
	if err != nil {
		return nil, err
	}
	proxyURL, err := s.resolveProxyURL(ctx, input.ProxyID)
	if err != nil {
		return nil, err
	}
	if method == kiroBuilderIDMethod {
		return s.startBuilderIDDeviceFlow(ctx, proxyURL)
	}
	return s.startSocialFlow(method, provider, proxyURL)
}

func (s *KiroOAuthService) ExchangeCode(ctx context.Context, input *KiroOAuthExchangeCodeInput) (*KiroOAuthTokenInfo, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	session, ok := s.getSession(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session 不存在或已过期")
	}
	if session.Method == kiroBuilderIDMethod {
		return nil, fmt.Errorf("builder-id session must be completed by device-code polling")
	}
	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		return nil, fmt.Errorf("state 无效")
	}
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		resolved, err := s.resolveProxyURL(ctx, input.ProxyID)
		if err != nil {
			return nil, err
		}
		proxyURL = resolved
	}
	payload := map[string]string{
		"code":          strings.TrimSpace(input.Code),
		"code_verifier": session.CodeVerifier,
		"redirect_uri":  KiroSocialRedirectURI,
	}
	if payload["code"] == "" {
		return nil, fmt.Errorf("authorization code is required")
	}
	var tokenResp kiroSocialTokenResponse
	targetURL := strings.TrimRight(s.authEndpoint, "/") + "/oauth/token"
	if err := postKiroOAuthJSON(ctx, targetURL, payload, proxyURL, kiroOAuthUserAgent, &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" || strings.TrimSpace(tokenResp.RefreshToken) == "" {
		return nil, fmt.Errorf("kiro OAuth response missing tokens")
	}
	if tokenResp.ExpiresIn <= 0 {
		tokenResp.ExpiresIn = 3600
	}
	s.deleteSession(input.SessionID)
	return &KiroOAuthTokenInfo{
		AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		ProfileARN:   strings.TrimSpace(tokenResp.ProfileARN),
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "social",
		Provider:     session.Provider,
		Email:        extractEmailFromJWT(tokenResp.AccessToken),
	}, nil
}

func (s *KiroOAuthService) PollDeviceCode(ctx context.Context, input KiroOAuthPollInput) (*KiroOAuthPollResult, error) {
	session, ok := s.getSession(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session 不存在或已过期")
	}
	if session.Method != kiroBuilderIDMethod {
		return nil, fmt.Errorf("session is not a builder-id device-code flow")
	}
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		resolved, err := s.resolveProxyURL(ctx, input.ProxyID)
		if err != nil {
			return nil, err
		}
		proxyURL = resolved
	}
	payload := map[string]string{
		"clientId":     session.ClientID,
		"clientSecret": session.ClientSecret,
		"deviceCode":   session.DeviceCode,
		"grantType":    kiroDeviceCodeGrantType,
	}
	var tokenResp kiroBuilderTokenResponse
	targetURL := strings.TrimRight(s.oidcEndpoint, "/") + "/token"
	status, err := postKiroOAuthJSONAllowPending(ctx, targetURL, payload, proxyURL, "KiroIDE", &tokenResp)
	if err != nil {
		return nil, err
	}
	if status == "pending" {
		return &KiroOAuthPollResult{Status: "pending", Interval: session.Interval}, nil
	}
	if status == "slow_down" {
		s.bumpSessionInterval(input.SessionID, 5)
		session, _ = s.getSession(input.SessionID)
		return &KiroOAuthPollResult{Status: "pending", Interval: session.Interval}, nil
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" || strings.TrimSpace(tokenResp.RefreshToken) == "" {
		return nil, fmt.Errorf("kiro device token response missing tokens")
	}
	if tokenResp.ExpiresIn <= 0 {
		tokenResp.ExpiresIn = 3600
	}
	s.deleteSession(input.SessionID)
	return &KiroOAuthPollResult{
		Status: "complete",
		TokenInfo: &KiroOAuthTokenInfo{
			AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
			RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
			ExpiresIn:    tokenResp.ExpiresIn,
			ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339),
			AuthMethod:   kiroBuilderIDMethod,
			Provider:     kiroBuilderIDProvider,
			ClientID:     session.ClientID,
			ClientSecret: session.ClientSecret,
			Email:        extractEmailFromJWT(tokenResp.AccessToken),
		},
	}, nil
}

func (s *KiroOAuthService) startSocialFlow(method, provider, proxyURL string) (*KiroOAuthStartResult, error) {
	state, err := generateKiroOAuthRandom(32)
	if err != nil {
		return nil, fmt.Errorf("生成 state 失败: %w", err)
	}
	codeVerifier, err := generateKiroOAuthRandom(64)
	if err != nil {
		return nil, fmt.Errorf("生成 code_verifier 失败: %w", err)
	}
	sessionID, err := generateKiroOAuthRandom(32)
	if err != nil {
		return nil, fmt.Errorf("生成 session_id 失败: %w", err)
	}
	authURL, err := url.Parse(strings.TrimRight(s.authEndpoint, "/") + "/login")
	if err != nil {
		return nil, err
	}
	q := authURL.Query()
	q.Set("idp", provider)
	q.Set("redirect_uri", KiroSocialRedirectURI)
	q.Set("code_challenge", generateKiroOAuthCodeChallenge(codeVerifier))
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("prompt", "select_account")
	authURL.RawQuery = q.Encode()

	s.setSession(sessionID, &kiroOAuthSession{
		Method:       method,
		Provider:     provider,
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	})
	return &KiroOAuthStartResult{
		Mode:      "auth_url",
		Method:    method,
		AuthURL:   authURL.String(),
		SessionID: sessionID,
		State:     state,
	}, nil
}

func (s *KiroOAuthService) startBuilderIDDeviceFlow(ctx context.Context, proxyURL string) (*KiroOAuthStartResult, error) {
	clientResp, err := s.registerBuilderIDClient(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientResp.ClientID) == "" || strings.TrimSpace(clientResp.ClientSecret) == "" {
		return nil, fmt.Errorf("kiro client registration response missing client credentials")
	}
	deviceResp, err := s.requestBuilderIDDeviceAuthorization(ctx, proxyURL, clientResp)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(deviceResp.DeviceCode) == "" || strings.TrimSpace(deviceResp.UserCode) == "" {
		return nil, fmt.Errorf("kiro device authorization response missing device code")
	}
	if deviceResp.Interval <= 0 {
		deviceResp.Interval = kiroOAuthDefaultDeviceInterval
	}
	if deviceResp.ExpiresIn <= 0 {
		deviceResp.ExpiresIn = 600
	}
	sessionID, err := generateKiroOAuthRandom(32)
	if err != nil {
		return nil, fmt.Errorf("生成 session_id 失败: %w", err)
	}
	s.setSession(sessionID, &kiroOAuthSession{
		Method:       kiroBuilderIDMethod,
		Provider:     kiroBuilderIDProvider,
		ClientID:     strings.TrimSpace(clientResp.ClientID),
		ClientSecret: strings.TrimSpace(clientResp.ClientSecret),
		DeviceCode:   strings.TrimSpace(deviceResp.DeviceCode),
		ProxyURL:     proxyURL,
		ExpiresAt:    time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second),
		Interval:     deviceResp.Interval,
	})
	return &KiroOAuthStartResult{
		Mode:                    "device_code",
		Method:                  kiroBuilderIDMethod,
		SessionID:               sessionID,
		VerificationURI:         strings.TrimSpace(deviceResp.VerificationURI),
		VerificationURIComplete: strings.TrimSpace(deviceResp.VerificationURIComplete),
		UserCode:                strings.TrimSpace(deviceResp.UserCode),
		ExpiresIn:               deviceResp.ExpiresIn,
		Interval:                deviceResp.Interval,
	}, nil
}

func (s *KiroOAuthService) registerBuilderIDClient(ctx context.Context, proxyURL string) (*kiroRegisterClientResponse, error) {
	payload := map[string]any{
		"clientName": "Kiro IDE",
		"clientType": "public",
		"scopes": []string{
			"codewhisperer:completions",
			"codewhisperer:analysis",
			"codewhisperer:conversations",
			"codewhisperer:transformations",
		},
		"grantTypes": []string{
			kiroDeviceCodeGrantType,
			"refresh_token",
		},
	}
	var out kiroRegisterClientResponse
	targetURL := strings.TrimRight(s.oidcEndpoint, "/") + "/client/register"
	if err := postKiroOAuthJSON(ctx, targetURL, payload, proxyURL, "KiroIDE", &out); err != nil {
		return nil, fmt.Errorf("register Kiro client failed: %w", err)
	}
	return &out, nil
}

func (s *KiroOAuthService) requestBuilderIDDeviceAuthorization(ctx context.Context, proxyURL string, client *kiroRegisterClientResponse) (*kiroDeviceAuthorizationResponse, error) {
	payload := map[string]string{
		"clientId":     client.ClientID,
		"clientSecret": client.ClientSecret,
		"startUrl":     kiroBuilderIDStartURL,
	}
	var out kiroDeviceAuthorizationResponse
	targetURL := strings.TrimRight(s.oidcEndpoint, "/") + "/device_authorization"
	if err := postKiroOAuthJSON(ctx, targetURL, payload, proxyURL, "KiroIDE", &out); err != nil {
		return nil, fmt.Errorf("request Kiro device authorization failed: %w", err)
	}
	return &out, nil
}

func (s *KiroOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", fmt.Errorf("proxy repository is not configured")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		return "", fmt.Errorf("proxy not found: %w", err)
	}
	if proxy == nil {
		return "", fmt.Errorf("proxy not found")
	}
	return proxy.URL(), nil
}

func (s *KiroOAuthService) setSession(sessionID string, session *kiroOAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredSessionsLocked(time.Now())
	s.sessions[sessionID] = session
}

func (s *KiroOAuthService) getSession(sessionID string) (*kiroOAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cleanupExpiredSessionsLocked(now)
	session, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok || session == nil {
		return nil, false
	}
	if !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now) {
		delete(s.sessions, sessionID)
		return nil, false
	}
	cp := *session
	return &cp, true
}

func (s *KiroOAuthService) deleteSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, strings.TrimSpace(sessionID))
}

func (s *KiroOAuthService) bumpSessionInterval(sessionID string, delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[strings.TrimSpace(sessionID)]; ok && session != nil {
		session.Interval += delta
	}
}

func (s *KiroOAuthService) cleanupExpiredSessionsLocked(now time.Time) {
	for id, session := range s.sessions {
		if session == nil || (!session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now)) {
			delete(s.sessions, id)
		}
	}
}

func normalizeKiroOAuthMethod(raw string) (method string, provider string, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "aws", "builder", "builder-id", "aws-builder-id":
		return kiroBuilderIDMethod, kiroBuilderIDProvider, nil
	case "google":
		return "google", "Google", nil
	case "github":
		return "github", "Github", nil
	default:
		return "", "", fmt.Errorf("unsupported Kiro OAuth method: %s", raw)
	}
}

func generateKiroOAuthRandom(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateKiroOAuthCodeChallenge(codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func postKiroOAuthJSON(ctx context.Context, targetURL string, payload any, proxyURL string, userAgent string, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kiro OAuth request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return err
	}
	return nil
}

func postKiroOAuthJSONAllowPending(ctx context.Context, targetURL string, payload any, proxyURL string, userAgent string, out any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusBadRequest {
		var errResp kiroOAuthErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			switch errResp.Error {
			case "authorization_pending":
				return "pending", nil
			case "slow_down":
				return "slow_down", nil
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("kiro device token request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return "", err
		}
	}
	return "complete", nil
}

func extractEmailFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{"email", "username"} {
		if v, ok := claims[key].(string); ok && strings.Contains(v, "@") {
			return v
		}
	}
	return ""
}

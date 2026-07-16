package kiro

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

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/google/uuid"
)

const (
	socialAuthPortalURL = "https://app.kiro.dev"
	socialAuthEndpoint  = "https://prod.us-east-1.auth.desktop.kiro.dev"
	defaultIDCRegion    = "us-east-1"
	BuilderIDStartURL   = "https://view.awsapps.com/start"
	sessionTTL          = 10 * time.Minute
	sessionCleanupEvery = 32
	sessionCleanupMin   = 32
)

var (
	socialAuthEndpointURL            = socialAuthEndpoint
	oidcEndpointOverride             = ""
	externalIDPTokenEndpointOverride = ""
)

type SocialProvider string

const (
	SocialProviderGoogle SocialProvider = "Google"
	SocialProviderGitHub SocialProvider = "Github"
)

const (
	ProviderGoogle      = "Google"
	ProviderGithub      = "Github"
	ProviderBuilderId   = "BuilderId"
	ProviderEnterprise  = "Enterprise"
	ProviderExternalIdp = "ExternalIdp"
)

func IsValidKiroProvider(provider string) bool {
	switch strings.TrimSpace(provider) {
	case ProviderGoogle, ProviderGithub, ProviderBuilderId, ProviderEnterprise, ProviderExternalIdp:
		return true
	default:
		return false
	}
}

func resolveIDCProvider(startURL string) string {
	switch strings.TrimSpace(startURL) {
	case "", BuilderIDStartURL:
		return ProviderBuilderId
	default:
		return ProviderEnterprise
	}
}

func normalizeKiroExpiresAt(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("expiresAt is empty")
	}

	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.Local().Format(time.RFC3339), nil
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return parsed.Local().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("invalid expiresAt format: %q", raw)
}

type AuthSession struct {
	State        string
	CodeVerifier string
	ProxyURL     string
	CreatedAt    time.Time
	AuthType     string
	Provider     string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	Region       string
	StartURL     string
	IssuerURL    string
	Scopes       []string
	LoginHint    string
	Audience     string
}

type SessionStore struct {
	mu       sync.RWMutex
	data     map[string]*AuthSession
	setCount uint64
}

func NewSessionStore() *SessionStore {
	return &SessionStore{data: make(map[string]*AuthSession)}
}

func (s *SessionStore) Get(id string) (*AuthSession, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if ok && sessionExpired(session, now) {
		delete(s.data, id)
		return nil, false
	}
	return session, ok
}

func (s *SessionStore) Set(id string, session *AuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCount++
	if len(s.data) >= sessionCleanupMin && s.setCount%sessionCleanupEvery == 0 {
		s.pruneExpiredLocked(time.Now())
	}
	s.data[id] = session
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
}

func (s *SessionStore) pruneExpiredLocked(now time.Time) {
	for id, session := range s.data {
		if sessionExpired(session, now) {
			delete(s.data, id)
		}
	}
}

func sessionExpired(session *AuthSession, now time.Time) bool {
	if session == nil {
		return true
	}
	if session.CreatedAt.IsZero() {
		return true
	}
	return now.After(session.CreatedAt.Add(sessionTTL))
}

type TokenData struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	AuthMethod   string `json:"authMethod,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	ClientIDHash string `json:"clientIdHash,omitempty"`
	Email        string `json:"email,omitempty"`
	StartURL     string `json:"startUrl,omitempty"`
	Region       string `json:"region,omitempty"`
	IssuerURL    string `json:"issuerUrl,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
}

type ExternalIDPCallback struct {
	LoginHint string
	IssuerURL string
	ClientID  string
	State     string
	Scopes    []string
	Audience  string
}

type ExternalIDPAuthURLInput struct {
	IssuerURL           string
	ClientID            string
	Scopes              []string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	LoginHint           string
}

type socialTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

type registerClientResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type createTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

type externalIDPTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

type userInfoResponse struct {
	Email string `json:"email"`
}

type deviceRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type RefreshTokenInvalidError struct {
	StatusCode int
	Body       string
}

func (e *RefreshTokenInvalidError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return "kiro refresh token invalid (invalid_grant)"
	}
	return fmt.Sprintf("kiro refresh token invalid (invalid_grant, status %d): %s", e.StatusCode, body)
}

func GenerateSessionID() string {
	return uuid.NewString()
}

func GenerateState() (string, error) {
	return randomURLSafe(16)
}

func GenerateCodeVerifier() (string, error) {
	return randomURLSafe(32)
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func BuildSocialSignInURL(redirectURI, codeChallenge, state string) string {
	params := url.Values{}
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("redirect_uri", redirectURI)
	params.Set("redirect_from", "KiroIDE")
	return fmt.Sprintf("%s/signin?%s", socialAuthPortalURL, params.Encode())
}

func BuildSocialTokenRedirectURI(baseRedirectURI, callbackPath, loginOption string) string {
	redirectURI := strings.TrimRight(strings.TrimSpace(baseRedirectURI), "/")
	if redirectURI == "" {
		return ""
	}
	path := strings.TrimSpace(callbackPath)
	if path == "" {
		path = "/oauth/callback"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	fullRedirectURI := redirectURI + path
	if option := strings.TrimSpace(loginOption); option != "" {
		return fullRedirectURI + "?login_option=" + url.QueryEscape(option)
	}
	return fullRedirectURI
}

func ParseExternalIDPCallbackURL(rawURL string) (*ExternalIDPCallback, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse external idp callback url failed: %w", err)
	}
	query := parsed.Query()
	if !strings.EqualFold(strings.TrimSpace(query.Get("login_option")), "external_idp") {
		return nil, fmt.Errorf("callback is not external_idp")
	}
	callback := &ExternalIDPCallback{
		LoginHint: strings.TrimSpace(query.Get("login_hint")),
		IssuerURL: strings.TrimRight(strings.TrimSpace(query.Get("issuer_url")), "/"),
		ClientID:  strings.TrimSpace(query.Get("client_id")),
		State:     strings.TrimSpace(query.Get("state")),
		Scopes:    splitOAuthScopes(query.Get("scopes")),
		Audience:  strings.TrimSpace(query.Get("audience")),
	}
	if callback.IssuerURL == "" {
		return nil, fmt.Errorf("external idp issuer_url is required")
	}
	if callback.ClientID == "" {
		return nil, fmt.Errorf("external idp client_id is required")
	}
	if callback.State == "" {
		return nil, fmt.Errorf("external idp state is required")
	}
	if len(callback.Scopes) == 0 {
		return nil, fmt.Errorf("external idp scopes are required")
	}
	return callback, nil
}

func BuildExternalIDPAuthURL(input ExternalIDPAuthURLInput) (string, error) {
	endpoint, err := externalIDPAuthorizeEndpoint(input.IssuerURL)
	if err != nil {
		return "", err
	}
	clientID := strings.TrimSpace(input.ClientID)
	if clientID == "" {
		return "", fmt.Errorf("external idp client_id is required")
	}
	redirectURI := strings.TrimSpace(input.RedirectURI)
	if redirectURI == "" {
		return "", fmt.Errorf("external idp redirect_uri is required")
	}
	state := strings.TrimSpace(input.State)
	if state == "" {
		return "", fmt.Errorf("external idp state is required")
	}
	scopes := normalizeOAuthScopes(input.Scopes)
	if len(scopes) == 0 {
		return "", fmt.Errorf("external idp scopes are required")
	}

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("response_mode", "query")
	if isMicrosoftExternalIDPEndpoint(endpoint) {
		params.Set("prompt", "login")
	}
	params.Set("scope", strings.Join(scopes, " "))
	params.Set("state", state)
	if loginHint := strings.TrimSpace(input.LoginHint); loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	if challenge := strings.TrimSpace(input.CodeChallenge); challenge != "" {
		params.Set("code_challenge", challenge)
		method := strings.TrimSpace(input.CodeChallengeMethod)
		if method == "" {
			method = "S256"
		}
		params.Set("code_challenge_method", method)
	}
	return endpoint + "?" + params.Encode(), nil
}

func isMicrosoftExternalIDPEndpoint(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "login.microsoftonline.com")
}

func ExchangeExternalIDPAuthCode(ctx context.Context, proxyURL, issuerURL, clientID string, scopes []string, code, codeVerifier, redirectURI, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPTokenEndpoint(issuerURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(clientID))
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", strings.TrimSpace(redirectURI))
	form.Set("code_verifier", strings.TrimSpace(codeVerifier))
	if joinedScopes := strings.Join(normalizeOAuthScopes(scopes), " "); joinedScopes != "" {
		form.Set("scope", joinedScopes)
	}
	var resp externalIDPTokenResponse
	if err := doForm(ctx, proxyURL, tokenURL, form, &resp); err != nil {
		return nil, err
	}
	return externalIDPTokenData(resp, issuerURL, clientID, scopes, loginHint, ""), nil
}

func RefreshExternalIDPToken(ctx context.Context, proxyURL, issuerURL, clientID string, scopes []string, refreshToken, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPTokenEndpoint(issuerURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(clientID))
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	if joinedScopes := strings.Join(normalizeOAuthScopes(scopes), " "); joinedScopes != "" {
		form.Set("scope", joinedScopes)
	}
	var resp externalIDPTokenResponse
	if err := doForm(ctx, proxyURL, tokenURL, form, &resp); err != nil {
		return nil, err
	}
	return externalIDPTokenData(resp, issuerURL, clientID, scopes, loginHint, refreshToken), nil
}

func CreateSocialToken(ctx context.Context, proxyURL, code, codeVerifier, redirectURI string) (*TokenData, error) {
	payload := map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  redirectURI,
	}
	var resp socialTokenResponse
	if err := doJSON(ctx, proxyURL, http.MethodPost, socialAuthEndpointURL+"/oauth/token", payload, &resp, BuildLoginHeaders(shortSHA(codeVerifier), BuildMachineID("", "", "codeVerifier:"+codeVerifier))); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "social",
		Region:       defaultIDCRegion,
	}, nil
}

func RefreshSocialToken(ctx context.Context, proxyURL, refreshToken, provider string) (*TokenData, error) {
	payload := map[string]string{
		"refreshToken": refreshToken,
	}
	var resp socialTokenResponse
	accountKey := BuildAccountKey("", "", refreshToken, "", 0)
	if err := doJSON(ctx, proxyURL, http.MethodPost, socialAuthEndpointURL+"/refreshToken", payload, &resp, BuildLoginHeaders(accountKey, BuildMachineID(refreshToken, "", accountKey))); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "social",
		Provider:     provider,
		Region:       defaultIDCRegion,
	}, nil
}

func RegisterIDCClient(ctx context.Context, proxyURL, redirectURI, issuerURL, region string) (*registerClientResponse, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]any{
		"clientName":   "Kiro IDE",
		"clientType":   "public",
		"scopes":       []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations", "codewhisperer:transformations", "codewhisperer:taskassist"},
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{redirectURI},
		"issuerUrl":    issuerURL,
	}
	var resp registerClientResponse
	headers := oidcHeaders("", BuildMachineID("", "", "register-idc-client"))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/client/register", payload, &resp, headers); err != nil {
		return nil, err
	}
	return &resp, nil
}

func BuildIDCAuthURL(clientID, redirectURI, state, codeChallenge, region string) string {
	if region == "" {
		region = defaultIDCRegion
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scopes", strings.Join([]string{
		"codewhisperer:completions",
		"codewhisperer:analysis",
		"codewhisperer:conversations",
		"codewhisperer:transformations",
		"codewhisperer:taskassist",
	}, " "))
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	return fmt.Sprintf("%s/authorize?%s", getOIDCEndpoint(region), params.Encode())
}

func ExchangeIDCAuthCode(ctx context.Context, proxyURL, clientID, clientSecret, code, codeVerifier, redirectURI, region, startURL string) (*TokenData, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"code":         code,
		"codeVerifier": codeVerifier,
		"redirectUri":  redirectURI,
		"grantType":    "authorization_code",
	}
	var resp createTokenResponse
	accountKey := BuildAccountKey(clientID, "", "", "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID("", "", "clientID:"+clientID))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/token", payload, &resp, headers); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	token := &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "idc",
		Provider:     resolveIDCProvider(startURL),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		StartURL:     startURL,
		Region:       region,
	}
	token.Email = FetchOIDCUserEmail(ctx, proxyURL, token.AccessToken, region)
	return token, nil
}

func RefreshIDCToken(ctx context.Context, proxyURL, clientID, clientSecret, refreshToken, region, startURL, provider string) (*TokenData, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	}
	var resp createTokenResponse
	accountKey := BuildAccountKey(clientID, "", refreshToken, "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID(refreshToken, "", accountKey))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/token", payload, &resp, headers); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	token := &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "idc",
		Provider:     strings.TrimSpace(provider),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		StartURL:     startURL,
		Region:       region,
	}
	if token.Provider == "" {
		token.Provider = resolveIDCProvider(startURL)
	}
	token.Email = FetchOIDCUserEmail(ctx, proxyURL, token.AccessToken, region)
	return token, nil
}

func FetchOIDCUserEmail(ctx context.Context, proxyURL, accessToken, region string) string {
	if strings.TrimSpace(accessToken) == "" {
		return ""
	}
	var resp userInfoResponse
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if err := doJSON(ctx, proxyURL, http.MethodGet, getOIDCEndpoint(region)+"/userinfo", nil, &resp, headers); err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Email)
}

func ParseImportedToken(tokenJSON string, deviceRegistrationJSON string) (*TokenData, error) {
	var token TokenData
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return nil, fmt.Errorf("failed to parse kiro token: %w", err)
	}
	token.AuthMethod = strings.ToLower(strings.TrimSpace(token.AuthMethod))
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("access token is empty")
	}
	if token.ClientIDHash != "" && (token.ClientID == "" || token.ClientSecret == "") && strings.TrimSpace(deviceRegistrationJSON) != "" {
		var reg deviceRegistration
		if err := json.Unmarshal([]byte(deviceRegistrationJSON), &reg); err != nil {
			return nil, fmt.Errorf("failed to parse device registration: %w", err)
		}
		if reg.ClientID != "" {
			token.ClientID = reg.ClientID
		}
		if reg.ClientSecret != "" {
			token.ClientSecret = reg.ClientSecret
		}
	}
	token.Provider = strings.TrimSpace(token.Provider)
	if !IsValidKiroProvider(token.Provider) {
		return nil, fmt.Errorf("unsupported or missing kiro provider: %q (must be one of Google/Github/BuilderId/Enterprise/ExternalIdp)", token.Provider)
	}
	if token.AuthMethod == "" {
		if token.Provider == ProviderExternalIdp {
			token.AuthMethod = "external_idp"
		} else if strings.TrimSpace(token.ClientID) != "" && strings.TrimSpace(token.ClientSecret) != "" {
			token.AuthMethod = "idc"
		}
	}
	if token.Provider == ProviderExternalIdp && token.AuthMethod != "external_idp" {
		return nil, fmt.Errorf("kiro provider %s requires authMethod external_idp", ProviderExternalIdp)
	}
	if token.AuthMethod == "external_idp" && token.Provider != ProviderExternalIdp {
		return nil, fmt.Errorf("kiro authMethod external_idp requires provider %s", ProviderExternalIdp)
	}
	if token.AuthMethod == "idc" {
		if strings.TrimSpace(token.Region) == "" {
			token.Region = defaultIDCRegion
		}
	} else if token.AuthMethod == "external_idp" {
		token.RefreshToken = strings.TrimSpace(token.RefreshToken)
		token.ClientID = strings.TrimSpace(token.ClientID)
		token.IssuerURL = strings.TrimSpace(token.IssuerURL)
		token.Scopes = strings.TrimSpace(token.Scopes)
		if token.RefreshToken == "" || token.ClientID == "" || token.IssuerURL == "" || token.Scopes == "" {
			return nil, fmt.Errorf("kiro external_idp import requires refreshToken, clientId, issuerUrl, and scopes")
		}
		if strings.TrimSpace(token.Region) == "" {
			token.Region = defaultIDCRegion
		}
	}
	if strings.TrimSpace(token.ExpiresAt) != "" {
		normalized, err := normalizeKiroExpiresAt(token.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse kiro token expiresAt: %w", err)
		}
		token.ExpiresAt = normalized
	}
	return &token, nil
}

func getOIDCEndpoint(region string) string {
	if strings.TrimSpace(oidcEndpointOverride) != "" {
		return strings.TrimRight(strings.TrimSpace(oidcEndpointOverride), "/")
	}
	if region == "" {
		region = defaultIDCRegion
	}
	return fmt.Sprintf("https://oidc.%s.amazonaws.com", region)
}

func externalIDPAuthorizeEndpoint(issuerURL string) (string, error) {
	base, err := normalizeExternalIDPIssuerBase(issuerURL)
	if err != nil {
		return "", err
	}
	return base + "/oauth2/v2.0/authorize", nil
}

func externalIDPTokenEndpoint(issuerURL string) (string, error) {
	if strings.TrimSpace(externalIDPTokenEndpointOverride) != "" {
		return strings.TrimRight(strings.TrimSpace(externalIDPTokenEndpointOverride), "/"), nil
	}
	base, err := normalizeExternalIDPIssuerBase(issuerURL)
	if err != nil {
		return "", err
	}
	return base + "/oauth2/v2.0/token", nil
}

func normalizeExternalIDPIssuerBase(issuerURL string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if raw == "" {
		return "", fmt.Errorf("external idp issuer_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse external idp issuer_url failed: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("external idp issuer_url must be an https url")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v2.0")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func splitOAuthScopes(raw string) []string {
	return normalizeOAuthScopes(strings.Fields(strings.TrimSpace(raw)))
}

func normalizeOAuthScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func externalIDPTokenData(resp externalIDPTokenResponse, issuerURL, clientID string, scopes []string, loginHint, fallbackRefreshToken string) *TokenData {
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	refreshToken := resp.RefreshToken
	if refreshToken == "" {
		refreshToken = fallbackRefreshToken
	}
	scopeString := strings.Join(normalizeOAuthScopes(scopes), " ")
	if strings.TrimSpace(scopeString) == "" {
		scopeString = resp.Scope
	}
	return &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "external_idp",
		Provider:     ProviderExternalIdp,
		ClientID:     strings.TrimSpace(clientID),
		Email:        strings.TrimSpace(loginHint),
		Region:       defaultIDCRegion,
		IssuerURL:    strings.TrimRight(strings.TrimSpace(issuerURL), "/"),
		Scopes:       scopeString,
	}
}

func oidcHeaders(accountKey, machineID string) map[string]string {
	headers := BuildOIDCHeaders(accountKey, machineID)
	if headers["amz-sdk-invocation-id"] == "" {
		headers["amz-sdk-invocation-id"] = uuid.NewString()
	}
	if headers["amz-sdk-request"] == "" {
		headers["amz-sdk-request"] = "attempt=1; max=4"
	}
	return headers
}

func doJSON(ctx context.Context, proxyURL, method, rawURL string, payload any, out any, extraHeaders map[string]string) error {
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(respBody))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(bodyText), "invalid_grant") {
			return &RefreshTokenInvalidError{StatusCode: resp.StatusCode, Body: bodyText}
		}
		return fmt.Errorf("upstream request failed (status %d): %s", resp.StatusCode, bodyText)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func doForm(ctx context.Context, proxyURL, rawURL string, form url.Values, out any) error {
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(respBody))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(bodyText), "invalid_grant") {
			return &RefreshTokenInvalidError{StatusCode: resp.StatusCode, Body: bodyText}
		}
		return fmt.Errorf("upstream request failed (status %d): %s", resp.StatusCode, bodyText)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func newHTTPClient(rawProxyURL string) (*http.Client, error) {
	_, parsed, err := proxyurl.Parse(rawProxyURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if parsed != nil {
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}, nil
}

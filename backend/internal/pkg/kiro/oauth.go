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
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/google/uuid"
)

const (
	socialAuthPortalURL              = "https://app.kiro.dev"
	socialAuthEndpoint               = "https://prod.us-east-1.auth.desktop.kiro.dev"
	defaultIDCRegion                 = "us-east-1"
	BuilderIDStartURL                = "https://view.awsapps.com/start"
	sessionTTL                       = 10 * time.Minute
	sessionCleanupEvery              = 32
	sessionCleanupMin                = 32
	externalIdpMaxDiscoveryRedirects = 3
	externalIdpDiscoveryBodyLimit    = 1 << 20
	externalIdpTokenBodyLimit        = 256 << 10
)

var allowedExternalIdpHostSuffixes = []string{
	".microsoftonline.com",
	".microsoftonline.us",
	".microsoftonline.cn",
}

var (
	socialAuthEndpointURL            = socialAuthEndpoint
	oidcEndpointOverride             = ""
	externalIDPTokenEndpointOverride = ""
	newExternalIdpHTTPClient         = newHTTPClient
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
	State         string
	CodeVerifier  string
	ProxyURL      string
	CreatedAt     time.Time
	AuthType      string
	Provider      string
	RedirectURI   string
	ClientID      string
	ClientSecret  string
	Region        string
	StartURL      string
	TokenEndpoint string
	IssuerURL     string
	Scopes        []string
	LoginHint     string
	Audience      string
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
	AccessToken   string `json:"accessToken"`
	RefreshToken  string `json:"refreshToken"`
	ProfileArn    string `json:"profileArn,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	AuthMethod    string `json:"authMethod,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	ClientIDHash  string `json:"clientIdHash,omitempty"`
	Email         string `json:"email,omitempty"`
	StartURL      string `json:"startUrl,omitempty"`
	Region        string `json:"region,omitempty"`
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	IssuerURL     string `json:"issuerUrl,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
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
	AuthorizationEndpoint string
	IssuerURL             string
	ClientID              string
	Scopes                []string
	RedirectURI           string
	State                 string
	CodeChallenge         string
	CodeChallengeMethod   string
	LoginHint             string
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

type externalIdpDiscoveryResponse struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
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
	endpoint := strings.TrimSpace(input.AuthorizationEndpoint)
	if endpoint == "" {
		var err error
		endpoint, err = externalIDPAuthorizeEndpoint(input.IssuerURL)
		if err != nil {
			return "", err
		}
	} else {
		if err := validateExternalIdpAuthorizationEndpoint(endpoint); err != nil {
			return "", err
		}
		if strings.TrimSpace(input.IssuerURL) != "" {
			if err := validateExternalIdpEndpointMatchesIssuer(input.IssuerURL, endpoint, "authorization_endpoint"); err != nil {
				return "", err
			}
		}
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

func DiscoverExternalIdp(ctx context.Context, proxyURL, issuerURL string) (*externalIdpDiscoveryResponse, error) {
	issuer := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if issuer == "" {
		return nil, fmt.Errorf("kiro external_idp issuer_url is required")
	}
	if err := validateExternalIdpIssuerURL(issuer); err != nil {
		return nil, err
	}
	discoveryURL := issuer + "/.well-known/openid-configuration"
	if err := validateExternalIdpDiscoveryURL(discoveryURL); err != nil {
		return nil, err
	}

	client, err := newExternalIdpHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > externalIdpMaxDiscoveryRedirects {
			return fmt.Errorf("external IdP discovery redirect limit exceeded")
		}
		if err := validateExternalIdpDiscoveryURL(req.URL.String()); err != nil {
			return err
		}
		return validateExternalIdpEndpointMatchesIssuer(issuer, req.URL.String(), "discovery redirect")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readExternalIdpResponseBody(resp.Body, externalIdpDiscoveryBodyLimit, "discovery")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("external IdP discovery failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var discovery externalIdpDiscoveryResponse
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, fmt.Errorf("parse external IdP discovery document failed: %w", err)
	}
	if err := validateExternalIdpDiscoveryForIssuer(issuer, &discovery); err != nil {
		return nil, err
	}
	return &discovery, nil
}

func validateExternalIdpDiscovery(discovery *externalIdpDiscoveryResponse) error {
	if discovery == nil {
		return fmt.Errorf("external IdP discovery document is empty")
	}
	discovery.AuthorizationEndpoint = strings.TrimSpace(discovery.AuthorizationEndpoint)
	discovery.TokenEndpoint = strings.TrimSpace(discovery.TokenEndpoint)
	if discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" {
		return fmt.Errorf("external IdP discovery document missing authorization_endpoint or token_endpoint")
	}
	if err := validateExternalIdpAuthorizationEndpoint(discovery.AuthorizationEndpoint); err != nil {
		return fmt.Errorf("unsafe external IdP authorization_endpoint: %w", err)
	}
	if err := validateExternalIdpTokenEndpoint(discovery.TokenEndpoint); err != nil {
		return fmt.Errorf("unsafe external IdP token_endpoint: %w", err)
	}
	return nil
}

func validateExternalIdpDiscoveryForIssuer(issuerURL string, discovery *externalIdpDiscoveryResponse) error {
	if err := validateExternalIdpIssuerURL(issuerURL); err != nil {
		return err
	}
	if err := validateExternalIdpDiscovery(discovery); err != nil {
		return err
	}
	if err := validateExternalIdpEndpointMatchesIssuer(issuerURL, discovery.AuthorizationEndpoint, "authorization_endpoint"); err != nil {
		return err
	}
	return validateExternalIdpEndpointMatchesIssuer(issuerURL, discovery.TokenEndpoint, "token_endpoint")
}

func validateExternalIdpEndpoint(rawURL string) error {
	_, err := parseExternalIdpURL(rawURL)
	return err
}

func validateExternalIdpIssuerURL(rawURL string) error {
	parsed, err := parseExternalIdpURL(rawURL)
	if err != nil {
		return err
	}
	return validateExternalIdpPath(parsed, "issuer", []string{"tenant", "v2.0"})
}

func validateExternalIdpDiscoveryURL(rawURL string) error {
	parsed, err := parseExternalIdpURL(rawURL)
	if err != nil {
		return err
	}
	return validateExternalIdpPath(parsed, "discovery", []string{"tenant", "v2.0", ".well-known", "openid-configuration"})
}

func validateExternalIdpAuthorizationEndpoint(rawURL string) error {
	parsed, err := parseExternalIdpURL(rawURL)
	if err != nil {
		return err
	}
	return validateExternalIdpPath(parsed, "authorization_endpoint", []string{"tenant", "oauth2", "v2.0", "authorize"})
}

func validateExternalIdpTokenEndpoint(rawURL string) error {
	parsed, err := parseExternalIdpURL(rawURL)
	if err != nil {
		return err
	}
	return validateExternalIdpPath(parsed, "token_endpoint", []string{"tenant", "oauth2", "v2.0", "token"})
}

func parseExternalIdpURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid external IdP URL %q: %w", rawURL, err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return nil, fmt.Errorf("external IdP URL must use https: %q", rawURL)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("external IdP URL must not contain userinfo: %q", rawURL)
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return nil, fmt.Errorf("external IdP URL must use the default HTTPS port: %q", rawURL)
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("external IdP URL must not contain a fragment: %q", rawURL)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, fmt.Errorf("external IdP URL must not contain a query: %q", rawURL)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("external IdP URL has no host: %q", rawURL)
	}
	if net.ParseIP(host) != nil {
		return nil, fmt.Errorf("external IdP URL host must not be an IP literal: %q", rawURL)
	}
	for _, suffix := range allowedExternalIdpHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("external IdP host %q is not allow-listed", host)
}

func validateExternalIdpPath(parsed *url.URL, role string, pattern []string) error {
	if parsed.RawPath != "" {
		return fmt.Errorf("external IdP %s URL must not use an encoded path", role)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != len(pattern) {
		return fmt.Errorf("external IdP %s URL has unsupported path %q", role, parsed.Path)
	}
	for index, expected := range pattern {
		segment := segments[index]
		if expected == "tenant" {
			if segment == "" || segment == "." || segment == ".." || strings.TrimSpace(segment) != segment {
				return fmt.Errorf("external IdP %s URL has invalid tenant path", role)
			}
			continue
		}
		if segment != expected {
			return fmt.Errorf("external IdP %s URL has unsupported path %q", role, parsed.Path)
		}
	}
	return nil
}

func validateExternalIdpEndpointMatchesIssuer(issuerURL, endpointURL, role string) error {
	issuer, err := parseExternalIdpURL(issuerURL)
	if err != nil {
		return err
	}
	endpoint, err := parseExternalIdpURL(endpointURL)
	if err != nil {
		return err
	}
	issuerSegments := strings.Split(strings.Trim(issuer.Path, "/"), "/")
	endpointSegments := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
	if len(issuerSegments) < 1 || len(endpointSegments) < 1 ||
		!strings.EqualFold(issuer.Hostname(), endpoint.Hostname()) ||
		!strings.EqualFold(issuerSegments[0], endpointSegments[0]) {
		return fmt.Errorf("external IdP %s must match issuer host and tenant", role)
	}
	return nil
}

func isMicrosoftExternalIDPEndpoint(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	for _, suffix := range allowedExternalIdpHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func ExchangeExternalIDPAuthCode(ctx context.Context, proxyURL, issuerURL, clientID string, scopes []string, code, codeVerifier, redirectURI, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPTokenEndpoint(issuerURL)
	if err != nil {
		return nil, err
	}
	return ExchangeExternalIDPAuthCodeAtEndpoint(ctx, proxyURL, tokenURL, issuerURL, clientID, scopes, code, codeVerifier, redirectURI, loginHint)
}

func ExchangeExternalIDPAuthCodeAtEndpoint(ctx context.Context, proxyURL, tokenEndpoint, issuerURL, clientID string, scopes []string, code, codeVerifier, redirectURI, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPRequestTokenEndpoint(tokenEndpoint, issuerURL)
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
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("external IdP token exchange returned empty access_token")
	}
	return externalIDPTokenData(resp, tokenEndpoint, issuerURL, clientID, scopes, loginHint, ""), nil
}

func RefreshExternalIDPToken(ctx context.Context, proxyURL, issuerURL, clientID string, scopes []string, refreshToken, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPTokenEndpoint(issuerURL)
	if err != nil {
		return nil, err
	}
	return RefreshExternalIDPTokenAtEndpoint(ctx, proxyURL, tokenURL, issuerURL, clientID, scopes, refreshToken, loginHint)
}

func RefreshExternalIDPTokenAtEndpoint(ctx context.Context, proxyURL, tokenEndpoint, issuerURL, clientID string, scopes []string, refreshToken, loginHint string) (*TokenData, error) {
	tokenURL, err := externalIDPRequestTokenEndpoint(tokenEndpoint, issuerURL)
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
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("external IdP refresh returned empty access_token")
	}
	return externalIDPTokenData(resp, tokenEndpoint, issuerURL, clientID, scopes, loginHint, refreshToken), nil
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
		token.TokenEndpoint = strings.TrimSpace(token.TokenEndpoint)
		token.IssuerURL = strings.TrimSpace(token.IssuerURL)
		token.Scopes = strings.TrimSpace(token.Scopes)
		if token.RefreshToken == "" || token.ClientID == "" || token.TokenEndpoint == "" || token.IssuerURL == "" || token.Scopes == "" {
			return nil, fmt.Errorf("kiro external_idp import requires refreshToken, clientId, tokenEndpoint, issuerUrl, and scopes")
		}
		if err := validateExternalIdpTokenEndpoint(token.TokenEndpoint); err != nil {
			return nil, fmt.Errorf("unsafe kiro external_idp tokenEndpoint: %w", err)
		}
		if err := validateExternalIdpIssuerURL(token.IssuerURL); err != nil {
			return nil, fmt.Errorf("unsafe kiro external_idp issuerUrl: %w", err)
		}
		if err := validateExternalIdpEndpointMatchesIssuer(token.IssuerURL, token.TokenEndpoint, "tokenEndpoint"); err != nil {
			return nil, err
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
	base, err := normalizeExternalIDPIssuerBase(issuerURL)
	if err != nil {
		return "", err
	}
	return base + "/oauth2/v2.0/token", nil
}

func externalIDPRequestTokenEndpoint(tokenEndpoint, issuerURL string) (string, error) {
	endpoint := strings.TrimSpace(tokenEndpoint)
	if err := validateExternalIdpTokenEndpoint(endpoint); err != nil {
		return "", err
	}
	if err := validateExternalIdpIssuerURL(issuerURL); err != nil {
		return "", err
	}
	if err := validateExternalIdpEndpointMatchesIssuer(issuerURL, endpoint, "token_endpoint"); err != nil {
		return "", err
	}
	if strings.TrimSpace(externalIDPTokenEndpointOverride) != "" {
		return strings.TrimRight(strings.TrimSpace(externalIDPTokenEndpointOverride), "/"), nil
	}
	return endpoint, nil
}

func normalizeExternalIDPIssuerBase(issuerURL string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if raw == "" {
		return "", fmt.Errorf("external idp issuer_url is required")
	}
	if err := validateExternalIdpIssuerURL(raw); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(raw)
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

func externalIDPTokenData(resp externalIDPTokenResponse, tokenEndpoint, issuerURL, clientID string, scopes []string, loginHint, fallbackRefreshToken string) *TokenData {
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
		AccessToken:   resp.AccessToken,
		RefreshToken:  refreshToken,
		ExpiresAt:     time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:    "external_idp",
		Provider:      ProviderExternalIdp,
		ClientID:      strings.TrimSpace(clientID),
		Email:         strings.TrimSpace(loginHint),
		Region:        defaultIDCRegion,
		TokenEndpoint: strings.TrimSpace(tokenEndpoint),
		IssuerURL:     strings.TrimRight(strings.TrimSpace(issuerURL), "/"),
		Scopes:        scopeString,
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
	client, err := newExternalIdpHTTPClient(proxyURL)
	if err != nil {
		return err
	}
	// Token requests contain authorization codes or refresh tokens. Never replay
	// them to a redirect target, even when the target also looks allowlisted.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
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

	respBody, err := readExternalIdpResponseBody(resp.Body, externalIdpTokenBodyLimit, "token")
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

func readExternalIdpResponseBody(body io.Reader, limit int, role string) ([]byte, error) {
	limited := io.LimitReader(body, int64(limit)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("external IdP %s response body exceeds %d bytes", role, limit)
	}
	return data, nil
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

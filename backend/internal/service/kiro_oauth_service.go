package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const (
	// Kiro desktop social auth uses localhost loopback callbacks from a fixed
	// allowlist. Use one of the bundled ports from the official client.
	kiroSocialRedirectURI = "http://localhost:49153"
	// The official Microsoft-backed External IdP application allowlists this
	// callback independently from the app.kiro.dev social portal callback.
	kiroExternalIdpRedirectURI = "http://localhost:3128/oauth/callback"
	// AWS IAM Identity Center native/public clients require an explicit loopback IP redirect URI.
	kiroIDCRedirectURI = "http://127.0.0.1:9876/oauth/callback"
)

var kiroDiscoverExternalIdp = func(ctx context.Context, proxyURL, issuerURL string) (string, string, error) {
	discovery, err := kiropkg.DiscoverExternalIdp(ctx, proxyURL, issuerURL)
	if err != nil {
		return "", "", err
	}
	return discovery.AuthorizationEndpoint, discovery.TokenEndpoint, nil
}

var kiroRefreshExternalIdpAtEndpoint = kiropkg.RefreshExternalIDPTokenAtEndpoint
var kiroRefreshExternalIdpLegacy = kiropkg.RefreshExternalIDPToken

type KiroOAuthService struct {
	sessionStore *kiropkg.SessionStore
	proxyRepo    ProxyRepository
}

func NewKiroOAuthService(proxyRepo ProxyRepository) *KiroOAuthService {
	return &KiroOAuthService{
		sessionStore: kiropkg.NewSessionStore(),
		proxyRepo:    proxyRepo,
	}
}

func (s *KiroOAuthService) Stop() {}

type KiroAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

type KiroIDCAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	ClientID  string `json:"client_id"`
	Region    string `json:"region"`
	StartURL  string `json:"start_url"`
}

type KiroExternalIDPAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	ClientID  string `json:"client_id"`
	IssuerURL string `json:"issuer_url"`
	Scopes    string `json:"scopes"`
	Email     string `json:"email,omitempty"`
}

type KiroTokenInfo struct {
	AccessToken   string `json:"access_token,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	ProfileArn    string `json:"profile_arn,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	ClientIDHash  string `json:"client_id_hash,omitempty"`
	Email         string `json:"email,omitempty"`
	StartURL      string `json:"start_url,omitempty"`
	Region        string `json:"region,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	IssuerURL     string `json:"issuer_url,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
}

type KiroGenerateAuthURLInput struct {
	ProxyID  *int64
	Provider string
}

type KiroExchangeCodeInput struct {
	SessionID    string
	State        string
	Code         string
	CallbackPath string
	LoginOption  string
	ProxyID      *int64
}

type KiroStartExternalIDPAuthInput struct {
	SessionID   string
	CallbackURL string
	ProxyID     *int64
}

type KiroGenerateIDCAuthURLInput struct {
	ProxyID  *int64
	StartURL string
	Region   string
}

type KiroRefreshTokenInput struct {
	RefreshToken  string
	AuthMethod    string
	Provider      string
	ClientID      string
	ClientSecret  string
	StartURL      string
	Region        string
	ProfileArn    string
	TokenEndpoint string
	IssuerURL     string
	Scopes        string
	Email         string
	ProxyID       *int64
}

type KiroImportTokenInput struct {
	TokenJSON              string
	DeviceRegistrationJSON string
}

func (s *KiroOAuthService) GenerateAuthURL(ctx context.Context, input *KiroGenerateAuthURLInput) (*KiroAuthURLResult, error) {
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		provider = string(kiropkg.SocialProviderGoogle)
	}
	if provider != string(kiropkg.SocialProviderGoogle) && provider != string(kiropkg.SocialProviderGitHub) {
		return nil, fmt.Errorf("unsupported kiro social provider: %s", provider)
	}
	state, err := kiropkg.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state failed: %w", err)
	}
	codeVerifier, err := kiropkg.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate code verifier failed: %w", err)
	}
	sessionID := kiropkg.GenerateSessionID()
	proxyURL, _ := s.resolveProxyURL(ctx, input.ProxyID)
	s.sessionStore.Set(sessionID, &kiropkg.AuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
		AuthType:     "social",
		Provider:     provider,
		RedirectURI:  kiroSocialRedirectURI,
	})
	return &KiroAuthURLResult{
		AuthURL:   kiropkg.BuildSocialSignInURL(kiroSocialRedirectURI, kiropkg.GenerateCodeChallenge(codeVerifier), state),
		SessionID: sessionID,
		State:     state,
	}, nil
}

func (s *KiroOAuthService) ExchangeCode(ctx context.Context, input *KiroExchangeCodeInput) (*KiroTokenInfo, error) {
	session, ok := s.sessionStore.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}
	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		return nil, fmt.Errorf("state invalid")
	}
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		proxyURL, _ = s.resolveProxyURL(ctx, input.ProxyID)
	}

	switch session.AuthType {
	case "social":
		token, err := kiropkg.CreateSocialToken(
			ctx,
			proxyURL,
			input.Code,
			session.CodeVerifier,
			buildKiroSocialExchangeRedirectURI(session.RedirectURI, session.Provider, input.CallbackPath, input.LoginOption),
		)
		if err != nil {
			return nil, err
		}
		token.Provider = session.Provider
		s.sessionStore.Delete(input.SessionID)
		return toKiroTokenInfo(token), nil
	case "external_idp":
		token, err := kiropkg.ExchangeExternalIDPAuthCodeAtEndpoint(
			ctx,
			proxyURL,
			session.TokenEndpoint,
			session.IssuerURL,
			session.ClientID,
			session.Scopes,
			input.Code,
			session.CodeVerifier,
			session.RedirectURI,
			session.LoginHint,
		)
		if err != nil {
			return nil, err
		}
		s.sessionStore.Delete(input.SessionID)
		return toKiroTokenInfo(token), nil
	case "idc":
		token, err := kiropkg.ExchangeIDCAuthCode(ctx, proxyURL, session.ClientID, session.ClientSecret, input.Code, session.CodeVerifier, session.RedirectURI, session.Region, session.StartURL)
		if err != nil {
			return nil, err
		}
		s.sessionStore.Delete(input.SessionID)
		return toKiroTokenInfo(token), nil
	default:
		return nil, fmt.Errorf("unsupported auth session type: %s", session.AuthType)
	}
}

func buildKiroSocialExchangeRedirectURI(baseRedirectURI, provider, callbackPath, loginOption string) string {
	option := strings.ToLower(strings.TrimSpace(loginOption))
	if option == "" {
		switch provider {
		case string(kiropkg.SocialProviderGitHub):
			option = "github"
		case string(kiropkg.SocialProviderGoogle):
			option = "google"
		}
	}
	return kiropkg.BuildSocialTokenRedirectURI(baseRedirectURI, callbackPath, option)
}

func (s *KiroOAuthService) StartExternalIDPAuth(ctx context.Context, input *KiroStartExternalIDPAuthInput) (*KiroExternalIDPAuthURLResult, error) {
	session, ok := s.sessionStore.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}
	callback, err := kiropkg.ParseExternalIDPCallbackURL(input.CallbackURL)
	if err != nil {
		return nil, err
	}
	if callback.State != session.State {
		return nil, fmt.Errorf("state invalid")
	}
	if strings.TrimSpace(session.CodeVerifier) == "" {
		return nil, fmt.Errorf("code verifier missing")
	}
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		proxyURL, _ = s.resolveProxyURL(ctx, input.ProxyID)
	}
	authorizationEndpoint, tokenEndpoint, err := kiroDiscoverExternalIdp(ctx, proxyURL, callback.IssuerURL)
	if err != nil {
		return nil, err
	}
	authURL, err := kiropkg.BuildExternalIDPAuthURL(kiropkg.ExternalIDPAuthURLInput{
		AuthorizationEndpoint: authorizationEndpoint,
		IssuerURL:             callback.IssuerURL,
		ClientID:              callback.ClientID,
		Scopes:                callback.Scopes,
		RedirectURI:           kiroExternalIdpRedirectURI,
		State:                 callback.State,
		CodeChallenge:         kiropkg.GenerateCodeChallenge(session.CodeVerifier),
		CodeChallengeMethod:   "S256",
		LoginHint:             callback.LoginHint,
	})
	if err != nil {
		return nil, err
	}
	session.ProxyURL = proxyURL
	session.AuthType = "external_idp"
	session.Provider = kiropkg.ProviderExternalIdp
	session.RedirectURI = kiroExternalIdpRedirectURI
	session.ClientID = callback.ClientID
	session.TokenEndpoint = tokenEndpoint
	session.IssuerURL = callback.IssuerURL
	session.Scopes = callback.Scopes
	session.LoginHint = callback.LoginHint
	session.Audience = callback.Audience
	s.sessionStore.Set(input.SessionID, session)

	return &KiroExternalIDPAuthURLResult{
		AuthURL:   authURL,
		SessionID: input.SessionID,
		State:     callback.State,
		ClientID:  callback.ClientID,
		IssuerURL: callback.IssuerURL,
		Scopes:    strings.Join(callback.Scopes, " "),
		Email:     callback.LoginHint,
	}, nil
}

func (s *KiroOAuthService) GenerateIDCAuthURL(ctx context.Context, input *KiroGenerateIDCAuthURLInput) (*KiroIDCAuthURLResult, error) {
	startURL := strings.TrimSpace(input.StartURL)
	if startURL == "" {
		startURL = kiropkg.BuilderIDStartURL
	}
	region := strings.TrimSpace(input.Region)
	if region == "" {
		region = "us-east-1"
	}
	state, err := kiropkg.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state failed: %w", err)
	}
	codeVerifier, err := kiropkg.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate code verifier failed: %w", err)
	}
	proxyURL, _ := s.resolveProxyURL(ctx, input.ProxyID)
	reg, err := kiropkg.RegisterIDCClient(ctx, proxyURL, kiroIDCRedirectURI, startURL, region)
	if err != nil {
		return nil, err
	}
	sessionID := kiropkg.GenerateSessionID()
	s.sessionStore.Set(sessionID, &kiropkg.AuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
		AuthType:     "idc",
		RedirectURI:  kiroIDCRedirectURI,
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
		Region:       region,
		StartURL:     startURL,
	})
	return &KiroIDCAuthURLResult{
		AuthURL:   kiropkg.BuildIDCAuthURL(reg.ClientID, kiroIDCRedirectURI, state, kiropkg.GenerateCodeChallenge(codeVerifier), region),
		SessionID: sessionID,
		State:     state,
		ClientID:  reg.ClientID,
		Region:    region,
		StartURL:  startURL,
	}, nil
}

func (s *KiroOAuthService) RefreshToken(ctx context.Context, input *KiroRefreshTokenInput) (*KiroTokenInfo, error) {
	proxyURL, _ := s.resolveProxyURL(ctx, input.ProxyID)
	refreshToken := strings.TrimSpace(input.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("kiro refresh token is required")
	}
	authMethod := resolveKiroRefreshAuthMethod(input.AuthMethod, input.ClientID, input.ClientSecret)

	var token *kiropkg.TokenData
	var err error
	switch authMethod {
	case "idc":
		clientID := strings.TrimSpace(input.ClientID)
		clientSecret := strings.TrimSpace(input.ClientSecret)
		if clientID == "" || clientSecret == "" {
			return nil, fmt.Errorf("kiro idc refresh requires client_id and client_secret")
		}
		token, err = kiropkg.RefreshIDCToken(ctx, proxyURL, clientID, clientSecret, refreshToken, input.Region, input.StartURL, input.Provider)
	case "external_idp":
		clientID := strings.TrimSpace(input.ClientID)
		tokenEndpoint := strings.TrimSpace(input.TokenEndpoint)
		issuerURL := strings.TrimSpace(input.IssuerURL)
		scopes := strings.Fields(strings.TrimSpace(input.Scopes))
		if clientID == "" || issuerURL == "" || len(scopes) == 0 {
			return nil, fmt.Errorf("kiro external_idp refresh requires client_id, issuer_url and scopes")
		}
		if tokenEndpoint == "" {
			if !strings.EqualFold(strings.TrimSpace(input.Provider), "Internal") {
				return nil, fmt.Errorf("kiro external_idp refresh requires token_endpoint")
			}
			token, err = kiroRefreshExternalIdpLegacy(ctx, proxyURL, issuerURL, clientID, scopes, refreshToken, input.Email)
		} else {
			token, err = kiroRefreshExternalIdpAtEndpoint(ctx, proxyURL, tokenEndpoint, issuerURL, clientID, scopes, refreshToken, input.Email)
		}
	default:
		token, err = kiropkg.RefreshSocialToken(ctx, proxyURL, refreshToken, input.Provider)
	}
	if err != nil {
		return nil, err
	}
	if token.ProfileArn == "" {
		token.ProfileArn = input.ProfileArn
	}
	if token.ClientID == "" {
		token.ClientID = input.ClientID
	}
	if token.ClientSecret == "" {
		token.ClientSecret = input.ClientSecret
	}
	if token.StartURL == "" {
		token.StartURL = input.StartURL
	}
	if token.Region == "" {
		token.Region = input.Region
	}
	if token.TokenEndpoint == "" {
		token.TokenEndpoint = input.TokenEndpoint
	}
	if token.IssuerURL == "" {
		token.IssuerURL = input.IssuerURL
	}
	if token.Scopes == "" {
		token.Scopes = input.Scopes
	}
	return toKiroTokenInfo(token), nil
}

func resolveKiroRefreshAuthMethod(authMethod, clientID, clientSecret string) string {
	method := strings.ToLower(strings.TrimSpace(authMethod))
	if method != "" {
		return method
	}
	if strings.TrimSpace(clientID) != "" && strings.TrimSpace(clientSecret) != "" {
		return "idc"
	}
	return "social"
}

func (s *KiroOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*KiroTokenInfo, error) {
	if account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("not a kiro oauth account")
	}
	return s.RefreshToken(ctx, &KiroRefreshTokenInput{
		RefreshToken:  account.GetCredential("refresh_token"),
		AuthMethod:    account.GetCredential("auth_method"),
		Provider:      account.GetCredential("provider"),
		ClientID:      account.GetCredential("client_id"),
		ClientSecret:  account.GetCredential("client_secret"),
		StartURL:      account.GetCredential("start_url"),
		Region:        account.GetCredential("region"),
		ProfileArn:    account.GetCredential("profile_arn"),
		TokenEndpoint: account.GetCredential("token_endpoint"),
		IssuerURL:     account.GetCredential("issuer_url"),
		Scopes:        account.GetCredential("scopes"),
		Email:         account.GetCredential("email"),
		ProxyID:       account.ProxyID,
	})
}

func (s *KiroOAuthService) ImportToken(input *KiroImportTokenInput) (*KiroTokenInfo, error) {
	token, err := kiropkg.ParseImportedToken(input.TokenJSON, input.DeviceRegistrationJSON)
	if err != nil {
		return nil, err
	}
	return toKiroTokenInfo(token), nil
}

func (s *KiroOAuthService) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return map[string]any{}
	}

	creds := map[string]any{}
	if tokenInfo.AccessToken != "" {
		creds["access_token"] = tokenInfo.AccessToken
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.ProfileArn != "" {
		creds["profile_arn"] = tokenInfo.ProfileArn
	}
	if tokenInfo.ExpiresAt != "" {
		creds["expires_at"] = tokenInfo.ExpiresAt
	}
	if tokenInfo.AuthMethod != "" {
		creds["auth_method"] = tokenInfo.AuthMethod
	}
	if tokenInfo.Provider != "" {
		creds["provider"] = tokenInfo.Provider
	}
	if tokenInfo.ClientID != "" {
		creds["client_id"] = tokenInfo.ClientID
	}
	if tokenInfo.ClientSecret != "" {
		creds["client_secret"] = tokenInfo.ClientSecret
	}
	if tokenInfo.ClientIDHash != "" {
		creds["client_id_hash"] = tokenInfo.ClientIDHash
	}
	if tokenInfo.Email != "" {
		creds["email"] = tokenInfo.Email
	}
	if tokenInfo.StartURL != "" {
		creds["start_url"] = tokenInfo.StartURL
	}
	if tokenInfo.Region != "" {
		creds["region"] = tokenInfo.Region
	}
	if tokenInfo.TokenEndpoint != "" {
		creds["token_endpoint"] = tokenInfo.TokenEndpoint
	}
	if tokenInfo.IssuerURL != "" {
		creds["issuer_url"] = tokenInfo.IssuerURL
	}
	if tokenInfo.Scopes != "" {
		creds["scopes"] = tokenInfo.Scopes
	}

	return creds
}

func toKiroTokenInfo(token *kiropkg.TokenData) *KiroTokenInfo {
	if token == nil {
		return nil
	}
	return &KiroTokenInfo{
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		ProfileArn:    token.ProfileArn,
		ExpiresAt:     token.ExpiresAt,
		AuthMethod:    token.AuthMethod,
		Provider:      token.Provider,
		ClientID:      token.ClientID,
		ClientSecret:  token.ClientSecret,
		ClientIDHash:  token.ClientIDHash,
		Email:         token.Email,
		StartURL:      token.StartURL,
		Region:        token.Region,
		TokenEndpoint: token.TokenEndpoint,
		IssuerURL:     token.IssuerURL,
		Scopes:        token.Scopes,
	}
}

func (s *KiroOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil || s.proxyRepo == nil {
		return "", nil
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil || proxy == nil {
		return "", err
	}
	return proxy.URL(), nil
}

package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const cursorDefaultAccessTokenTTL = time.Hour

// CursorOAuthClient is the HTTP boundary for Cursor's authentication endpoints.
type CursorOAuthClient interface {
	ExchangeUserAPIKey(ctx context.Context, apiKey, proxyURL string) (*cursorpkg.TokenResponse, error)
	RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*cursorpkg.TokenResponse, error)
	ExchangeWebSession(ctx context.Context, workosSessionToken, proxyURL string) (*cursorpkg.TokenResponse, error)
	PollDeepLink(ctx context.Context, id, verifier, proxyURL string) (*cursorpkg.TokenResponse, error)
}

// CursorOAuthTokenService is the credential refresh port used by Cursor token providers.
type CursorOAuthTokenService interface {
	RefreshAccountToken(ctx context.Context, account *Account) (*CursorTokenInfo, error)
	BuildAccountCredentials(tokenInfo *CursorTokenInfo) map[string]any
}

// CursorTokenInfo is the normalized outcome of a Cursor credential import or refresh.
type CursorTokenInfo struct {
	AccessToken  string
	RefreshToken string
	APIKey       string
	UserID       string
	BaseURL      string
	ExpiresAt    int64
	Source       string

	// WebSessionToken is retained so a later refresh can replay the browser
	// session upgrade if the minted client credential stops working.
	WebSessionToken string
}

type CursorOAuthService struct {
	proxyRepo   ProxyRepository
	oauthClient CursorOAuthClient
	config      *config.Config
}

func NewCursorOAuthService(proxyRepo ProxyRepository, oauthClient CursorOAuthClient, configs ...*config.Config) *CursorOAuthService {
	service := &CursorOAuthService{proxyRepo: proxyRepo, oauthClient: oauthClient}
	if len(configs) > 0 {
		service.config = configs[0]
	}
	return service
}

type CursorOAuthCapabilities struct {
	DeepLinkEnabled     bool `json:"deep_link_enabled"`
	APIKeyImportEnabled bool `json:"api_key_import_enabled"`
	CookieImportEnabled bool `json:"cookie_import_enabled"`
}

func (s *CursorOAuthService) GetCapabilities() CursorOAuthCapabilities {
	return CursorOAuthCapabilities{DeepLinkEnabled: true, APIKeyImportEnabled: true, CookieImportEnabled: true}
}

type CursorAuthURLResult struct {
	AuthURL  string `json:"auth_url"`
	UUID     string `json:"uuid"`
	Verifier string `json:"verifier"`
}

func (s *CursorOAuthService) GenerateAuthURL(context.Context) (*CursorAuthURLResult, error) {
	verifier, challenge, id, err := cursorpkg.NewDeepLinkChallenge()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "CURSOR_OAUTH_CHALLENGE_FAILED", "failed to generate challenge")
	}
	return &CursorAuthURLResult{
		AuthURL: cursorpkg.BuildLoginDeepControlURL(challenge, id), UUID: id, Verifier: verifier,
	}, nil
}

func (s *CursorOAuthService) Poll(ctx context.Context, id, verifier string, proxyID *int64) (*CursorTokenInfo, error) {
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	verifier = strings.TrimSpace(verifier)
	if id == "" || verifier == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_INVALID_INPUT", "uuid and verifier are required")
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	response, err := s.oauthClient.PollDeepLink(ctx, id, verifier, proxyURL)
	if err != nil {
		return nil, err
	}
	if response == nil || strings.TrimSpace(response.AccessToken) == "" {
		return nil, infraerrors.New(http.StatusAccepted, "CURSOR_OAUTH_PENDING", "login has not completed yet")
	}
	return s.tokenInfoFromResponse(response, "", cursorpkg.CredentialSourceDeepLink), nil
}

func (s *CursorOAuthService) ImportFromAPIKey(ctx context.Context, apiKey string, proxyID *int64) (*CursorTokenInfo, error) {
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_API_KEY", "api_key is required")
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	response, err := s.oauthClient.ExchangeUserAPIKey(ctx, apiKey, proxyURL)
	if err != nil {
		return nil, err
	}
	if response == nil || strings.TrimSpace(response.AccessToken) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor api key exchange returned no access token")
	}
	info := s.tokenInfoFromResponse(response, apiKey, cursorpkg.CredentialSourceAPIKey)
	// API-key exchange refresh tokens cannot be replayed at /oauth/token.
	info.RefreshToken = ""
	return info, nil
}

func (s *CursorOAuthService) ImportFromCookie(_ context.Context, cookie string) (*CursorTokenInfo, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_COOKIE", "cookie is required")
	}
	jwt, userID := cursorpkg.ParseToken(cookie)
	if strings.TrimSpace(jwt) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_INVALID_COOKIE", "cookie did not contain a JWT")
	}
	info := &CursorTokenInfo{AccessToken: jwt, UserID: userID, Source: cursorpkg.CredentialSourceCookie}
	if cursorpkg.IsWebSessionToken(jwt) {
		info.WebSessionToken = cursorpkg.NormalizeSessionCookie(cookie)
	}
	if expiry, ok := cursorpkg.JWTExpiry(jwt); ok {
		info.ExpiresAt = expiry.Unix()
	}
	return info, nil
}

func (s *CursorOAuthService) RefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*CursorTokenInfo, error) {
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_REFRESH_TOKEN", "refresh_token is required")
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	return s.refreshWithDeepLinkToken(ctx, refreshToken, proxyURL)
}

func (s *CursorOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*CursorTokenInfo, error) {
	if account == nil || account.Platform != PlatformCursor {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_INVALID_ACCOUNT", "account is not a Cursor account")
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	proxyURL, err := s.proxyURL(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}

	webSession := cursorpkg.NormalizeSessionCookie(account.GetCursorWebSessionToken())
	if apiKey := strings.TrimSpace(account.GetCursorAPIKey()); apiKey != "" {
		response, err := s.oauthClient.ExchangeUserAPIKey(ctx, apiKey, proxyURL)
		if err != nil {
			return nil, err
		}
		if response == nil || strings.TrimSpace(response.AccessToken) == "" {
			return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor api key exchange returned no access token")
		}
		info := s.tokenInfoFromResponse(response, apiKey, cursorpkg.CredentialSourceAPIKey)
		info.RefreshToken = strings.TrimSpace(account.GetCursorRefreshToken())
		info.WebSessionToken = webSession
		return info, nil
	}

	if refreshToken := strings.TrimSpace(account.GetCursorRefreshToken()); refreshToken != "" {
		info, refreshErr := s.refreshWithDeepLinkToken(ctx, refreshToken, proxyURL)
		if refreshErr == nil {
			info.WebSessionToken = webSession
			return info, nil
		}
		if webSession == "" {
			return nil, refreshErr
		}
	}
	if webSession != "" {
		return s.upgradeWebSession(ctx, webSession, proxyURL)
	}
	return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_REFRESH_TOKEN", "no api_key, refresh_token or web session available")
}

func (s *CursorOAuthService) refreshWithDeepLinkToken(ctx context.Context, refreshToken, proxyURL string) (*CursorTokenInfo, error) {
	response, err := s.oauthClient.RefreshToken(ctx, refreshToken, proxyURL)
	if err != nil {
		return nil, err
	}
	if response == nil || strings.TrimSpace(response.AccessToken) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor token refresh returned no access token")
	}
	info := s.tokenInfoFromResponse(response, "", cursorpkg.CredentialSourceDeepLink)
	if info.RefreshToken == "" {
		info.RefreshToken = refreshToken
	}
	return info, nil
}

func (s *CursorOAuthService) upgradeWebSession(ctx context.Context, webSession, proxyURL string) (*CursorTokenInfo, error) {
	response, err := s.oauthClient.ExchangeWebSession(ctx, webSession, proxyURL)
	if err != nil {
		return nil, err
	}
	if response == nil || strings.TrimSpace(response.AccessToken) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor web session exchange returned no access token")
	}
	if cursorpkg.IsWebSessionToken(response.AccessToken) {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_WEB_SESSION_NOT_UPGRADED", "cursor returned another web token instead of a client token")
	}
	info := s.tokenInfoFromResponse(response, "", cursorpkg.CredentialSourceCookie)
	info.WebSessionToken = webSession
	if info.UserID == "" {
		info.UserID = cursorpkg.ExtractUserID(webSession)
	}
	return info, nil
}

func (s *CursorOAuthService) UpgradeWebSession(ctx context.Context, account *Account) (*CursorTokenInfo, error) {
	if account == nil || account.Platform != PlatformCursor {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_INVALID_ACCOUNT", "account is not a Cursor account")
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	webSession := cursorpkg.NormalizeSessionCookie(account.GetCursorWebSessionToken())
	if webSession == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_COOKIE", "account has no stored web session token")
	}
	proxyURL, err := s.proxyURL(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}
	return s.upgradeWebSession(ctx, webSession, proxyURL)
}

func (s *CursorOAuthService) BuildAccountCredentials(tokenInfo *CursorTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	credentials := map[string]any{"access_token": tokenInfo.AccessToken}
	expiresAt := tokenInfo.ExpiresAt
	if expiresAt <= 0 {
		if expiry, ok := cursorpkg.JWTExpiry(tokenInfo.AccessToken); ok {
			expiresAt = expiry.Unix()
		}
	}
	if expiresAt <= 0 {
		expiresAt = time.Now().Add(cursorDefaultAccessTokenTTL).Unix()
	}
	credentials["expires_at"] = time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)
	if tokenInfo.RefreshToken != "" {
		credentials["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.APIKey != "" {
		credentials["api_key"] = tokenInfo.APIKey
	}
	if tokenInfo.WebSessionToken != "" {
		credentials["web_session_token"] = tokenInfo.WebSessionToken
	}
	userID := strings.TrimSpace(tokenInfo.UserID)
	if userID == "" {
		userID = cursorpkg.ExtractUserID(tokenInfo.AccessToken)
	}
	if userID != "" {
		credentials["user_id"] = userID
	}
	if tokenInfo.BaseURL != "" {
		credentials["base_url"] = tokenInfo.BaseURL
	}
	if tokenInfo.Source != "" {
		credentials["credential_source"] = tokenInfo.Source
	}
	return credentials
}

var cursorRefreshSourceKeys = []string{"api_key", "refresh_token", "web_session_token"}

// NormalizeCursorReauthorizedCredentials removes stale refresh sources when a
// new access token and a new refresh source are explicitly supplied.
func NormalizeCursorReauthorizedCredentials(platform string, incoming, merged map[string]any) map[string]any {
	if platform != PlatformCursor || merged == nil || len(incoming) == 0 {
		return merged
	}
	if credentialStringValue(incoming, "access_token") == "" {
		return merged
	}
	supplied := false
	for _, key := range cursorRefreshSourceKeys {
		if credentialStringValue(incoming, key) != "" {
			supplied = true
			break
		}
	}
	if !supplied {
		return merged
	}
	for _, key := range cursorRefreshSourceKeys {
		if credentialStringValue(incoming, key) == "" {
			delete(merged, key)
		}
	}
	return merged
}

func credentialStringValue(credentials map[string]any, key string) string {
	value, ok := credentials[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (s *CursorOAuthService) tokenInfoFromResponse(response *cursorpkg.TokenResponse, apiKey, source string) *CursorTokenInfo {
	info := &CursorTokenInfo{
		AccessToken: response.AccessToken, RefreshToken: response.RefreshToken,
		APIKey: strings.TrimSpace(apiKey), UserID: cursorpkg.ExtractUserID(response.AccessToken), Source: source,
	}
	if response.ExpiresIn > 0 {
		info.ExpiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second).Unix()
	} else if expiry, ok := cursorpkg.JWTExpiry(response.AccessToken); ok {
		info.ExpiresAt = expiry.Unix()
	} else {
		info.ExpiresAt = time.Now().Add(cursorDefaultAccessTokenTTL).Unix()
	}
	return info
}

func (s *CursorOAuthService) requireOAuthClient() error {
	if s == nil || s.oauthClient == nil {
		return infraerrors.New(http.StatusInternalServerError, "CURSOR_OAUTH_CLIENT_NOT_CONFIGURED", "cursor oauth client is not configured")
	}
	return nil
}

func (s *CursorOAuthService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_PROXY_NOT_AVAILABLE", "proxy repository is not available")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		if errors.Is(err, ErrProxyNotFound) {
			return "", infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_PROXY_NOT_FOUND", "configured proxy was not found")
		}
		return "", infraerrors.New(http.StatusServiceUnavailable, "CURSOR_OAUTH_PROXY_LOOKUP_FAILED", "proxy lookup is temporarily unavailable")
	}
	if proxy == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_PROXY_NOT_FOUND", "configured proxy was not found")
	}
	return proxy.URL(), nil
}

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

type CursorTokenRefresher struct {
	cursorOAuthService CursorOAuthTokenService
}

func NewCursorTokenRefresher(cursorOAuthService CursorOAuthTokenService) *CursorTokenRefresher {
	return &CursorTokenRefresher{cursorOAuthService: cursorOAuthService}
}

func (r *CursorTokenRefresher) CacheKey(account *Account) string {
	return CursorTokenCacheKey(account)
}

func (r *CursorTokenRefresher) CanRefresh(account *Account) bool {
	if account == nil || account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return false
	}
	return strings.TrimSpace(account.GetCursorAPIKey()) != "" ||
		strings.TrimSpace(account.GetCursorRefreshToken()) != "" ||
		strings.TrimSpace(account.GetCursorWebSessionToken()) != ""
}

func (r *CursorTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil || !r.CanRefresh(account) {
		return false
	}
	accessToken := strings.TrimSpace(account.GetCursorAccessToken())
	if accessToken == "" || cursorpkg.IsWebSessionToken(accessToken) {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		if jwtExpiry, ok := cursorpkg.JWTExpiry(accessToken); ok {
			expiresAt = &jwtExpiry
		}
	}
	if expiresAt == nil {
		return true
	}
	if refreshWindow < cursorTokenRefreshSkew {
		refreshWindow = cursorTokenRefreshSkew
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *CursorTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.cursorOAuthService == nil {
		return nil, errors.New("cursor oauth service is not configured")
	}
	tokenInfo, err := r.cursorOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	credentials := r.cursorOAuthService.BuildAccountCredentials(tokenInfo)
	credentials = MergeCredentials(account.Credentials, credentials)
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		credentials["base_url"] = baseURL
	}
	return credentials, nil
}

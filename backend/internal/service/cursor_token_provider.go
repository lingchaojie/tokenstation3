package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

const (
	cursorTokenRefreshSkew      = 10 * time.Minute
	cursorTokenCacheSkew        = 5 * time.Minute
	cursorRequestRefreshTimeout = 8 * time.Second
	cursorRequestUpgradeTimeout = 25 * time.Second
	cursorForceRefreshTTL       = 15 * time.Minute
	cursorForceRefreshWindow    = 100 * 365 * 24 * time.Hour
	cursorLockInitialWait       = 100 * time.Millisecond
	cursorLockMaxWait           = 800 * time.Millisecond
	cursorLockMaxAttempts       = 5
)

var (
	errCursorAccessTokenMissing    = errors.New("cursor access token is missing")
	errCursorAccessTokenExpired    = errors.New("cursor access token is expired")
	errCursorRefreshNotConfigured  = errors.New("cursor oauth refresh is not configured")
	errCursorCredentialsMissing    = errors.New("cursor account has no refreshable credential")
	errCursorWebSessionNotUpgraded = errors.New("cursor web session token has not been upgraded to a client token")
	errCursorAccessTokenRejected   = errors.New("cursor access token was rejected upstream and could not be refreshed")
)

type CursorTokenProvider struct {
	accountRepo   AccountRepository
	tokenCache    GeminiTokenCache
	refreshAPI    *OAuthRefreshAPI
	executor      OAuthRefreshExecutor
	refreshPolicy ProviderRefreshPolicy

	waitBeforePoll func(context.Context, time.Duration) bool
}

func NewCursorTokenProvider(accountRepo AccountRepository, tokenCache GeminiTokenCache) *CursorTokenProvider {
	return &CursorTokenProvider{
		accountRepo:    accountRepo,
		tokenCache:     tokenCache,
		refreshPolicy:  CursorProviderRefreshPolicy(),
		waitBeforePoll: waitForCursorTokenPoll,
	}
}

func (p *CursorTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *CursorTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *CursorTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return "", errors.New("not a cursor oauth account")
	}

	cacheKey := CursorTokenCacheKey(account)
	accessToken := strings.TrimSpace(account.GetCursorAccessToken())
	needsUpgrade := cursorpkg.IsWebSessionToken(accessToken)
	hasRefreshSource := strings.TrimSpace(account.GetCursorAPIKey()) != "" ||
		strings.TrimSpace(account.GetCursorRefreshToken()) != "" ||
		strings.TrimSpace(account.GetCursorWebSessionToken()) != ""

	rejected := p.rejectedFingerprint(ctx, cacheKey)
	tokenRejected := rejected != "" && rejected == cursorTokenFingerprint(accessToken)
	if !tokenRejected {
		if cached, ok := p.cachedToken(ctx, cacheKey, rejected); ok {
			return cached, nil
		}
	}

	expiresAt := p.tokenExpiry(account)
	tokenFresh := accessToken != "" && !needsUpgrade && expiresAt != nil && time.Until(*expiresAt) > cursorTokenRefreshSkew
	if tokenFresh && !tokenRejected {
		p.cacheToken(ctx, account, accessToken, expiresAt)
		return accessToken, nil
	}

	if !hasRefreshSource {
		switch {
		case accessToken == "":
			return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenMissing, account)
		case needsUpgrade:
			return "", withCursorCredentialFailureSnapshot(errCursorWebSessionNotUpgraded, account)
		case tokenRejected:
			return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenRejected, account)
		case expiresAt != nil && !time.Now().Before(*expiresAt):
			return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenExpired, account)
		default:
			return accessToken, nil
		}
	}

	if p.refreshAPI == nil || p.executor == nil {
		if !tokenRejected && accessToken != "" && !needsUpgrade && (expiresAt == nil || time.Now().Before(*expiresAt)) {
			return accessToken, nil
		}
		return "", errCursorRefreshNotConfigured
	}

	timeout := cursorRequestRefreshTimeout
	if needsUpgrade {
		timeout = cursorRequestUpgradeTimeout
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	refreshWindow := cursorTokenRefreshSkew
	if tokenRejected {
		refreshWindow = cursorForceRefreshWindow
	}
	result, err := p.refreshAPI.RefreshIfNeeded(withOAuthRefreshRequestPath(refreshCtx), account, p.executor, refreshWindow)
	if err != nil {
		if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorUseExistingToken &&
			!tokenRejected && accessToken != "" && !needsUpgrade &&
			(expiresAt == nil || time.Now().Before(*expiresAt)) {
			return accessToken, nil
		}
		return "", withCursorCredentialFailureSnapshot(err, account)
	}
	if result != nil && result.LockHeld {
		if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache || tokenRejected {
			if token, ok := p.waitForRefreshedToken(refreshCtx, cacheKey, rejected); ok {
				return token, nil
			}
		}
		if tokenRejected {
			return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenRejected, account)
		}
		if accessToken != "" && !needsUpgrade && (expiresAt == nil || time.Now().Before(*expiresAt)) {
			return accessToken, nil
		}
		return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenExpired, account)
	}
	if result != nil && result.Account != nil {
		account = result.Account
	}

	refreshed := strings.TrimSpace(account.GetCursorAccessToken())
	if refreshed == "" {
		return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenMissing, account)
	}
	if cursorpkg.IsWebSessionToken(refreshed) {
		return "", withCursorCredentialFailureSnapshot(errCursorWebSessionNotUpgraded, account)
	}
	if rejected != "" && rejected == cursorTokenFingerprint(refreshed) {
		return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenRejected, account)
	}
	newExpiry := p.tokenExpiry(account)
	if newExpiry != nil && !time.Now().Before(*newExpiry) {
		return "", withCursorCredentialFailureSnapshot(errCursorAccessTokenExpired, account)
	}
	p.clearRejectedFingerprint(ctx, cacheKey)
	p.cacheToken(ctx, account, refreshed, newExpiry)
	return refreshed, nil
}

func (p *CursorTokenProvider) cachedToken(ctx context.Context, cacheKey, rejectedFingerprint string) (string, bool) {
	if p.tokenCache == nil {
		return "", false
	}
	cached, err := p.tokenCache.GetAccessToken(ctx, cacheKey)
	if err != nil {
		return "", false
	}
	cached = strings.TrimSpace(cached)
	if cached == "" || cursorpkg.IsWebSessionToken(cached) {
		return "", false
	}
	if rejectedFingerprint != "" && rejectedFingerprint == cursorTokenFingerprint(cached) {
		return "", false
	}
	if expiresAt, ok := cursorpkg.JWTExpiry(cached); ok && time.Until(expiresAt) <= cursorTokenRefreshSkew {
		return "", false
	}
	return cached, true
}

func (p *CursorTokenProvider) waitForRefreshedToken(ctx context.Context, cacheKey, rejectedFingerprint string) (string, bool) {
	if p.tokenCache == nil {
		return "", false
	}
	wait := cursorLockInitialWait
	waitBeforePoll := p.waitBeforePoll
	if waitBeforePoll == nil {
		waitBeforePoll = waitForCursorTokenPoll
	}
	for attempt := 0; attempt < cursorLockMaxAttempts; attempt++ {
		if !waitBeforePoll(ctx, wait) {
			return "", false
		}
		if token, ok := p.cachedToken(ctx, cacheKey, rejectedFingerprint); ok {
			return token, true
		}
		if wait < cursorLockMaxWait {
			wait *= 2
			if wait > cursorLockMaxWait {
				wait = cursorLockMaxWait
			}
		}
	}
	return "", false
}

func waitForCursorTokenPoll(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p *CursorTokenProvider) tokenExpiry(account *Account) *time.Time {
	if account == nil {
		return nil
	}
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		return expiresAt
	}
	if expiresAt, ok := cursorpkg.JWTExpiry(account.GetCursorAccessToken()); ok {
		return &expiresAt
	}
	return nil
}

func (p *CursorTokenProvider) cacheToken(ctx context.Context, account *Account, token string, expiresAt *time.Time) {
	if p.tokenCache == nil || strings.TrimSpace(token) == "" {
		return
	}
	ttl := 30 * time.Minute
	if expiresAt != nil {
		until := time.Until(*expiresAt)
		switch {
		case until > cursorTokenCacheSkew:
			ttl = until - cursorTokenCacheSkew
		case until > 0:
			ttl = until
		default:
			return
		}
	}
	_ = p.tokenCache.SetAccessToken(ctx, CursorTokenCacheKey(account), token, ttl)
}

func (p *CursorTokenProvider) InvalidateToken(ctx context.Context, account *Account) error {
	if account == nil {
		return nil
	}
	// The generic invalidator has no request-local bearer. Preserve its existing
	// contract with the best credential available on the account snapshot;
	// Cursor request paths should call InvalidateRejectedToken with the bearer
	// they actually sent upstream.
	return p.InvalidateRejectedToken(ctx, account, account.GetCursorAccessToken())
}

// InvalidateRejectedToken invalidates the cache and fingerprints the exact
// bearer rejected by Cursor. Callers must pass the request-local token returned
// by GetAccessToken, because the account snapshot may be older than the cache.
func (p *CursorTokenProvider) InvalidateRejectedToken(ctx context.Context, account *Account, rejectedBearer string) error {
	if p == nil || p.tokenCache == nil || account == nil {
		return nil
	}
	cacheKey := CursorTokenCacheKey(account)
	err := p.tokenCache.DeleteAccessToken(ctx, cacheKey)
	if fingerprint := cursorTokenFingerprint(rejectedBearer); fingerprint != "" {
		if setErr := p.tokenCache.SetAccessToken(ctx, cursorForceRefreshCacheKey(cacheKey), fingerprint, cursorForceRefreshTTL); setErr != nil && err == nil {
			err = setErr
		}
	}
	return err
}

func (p *CursorTokenProvider) rejectedFingerprint(ctx context.Context, cacheKey string) string {
	if p.tokenCache == nil {
		return ""
	}
	fingerprint, err := p.tokenCache.GetAccessToken(ctx, cursorForceRefreshCacheKey(cacheKey))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(fingerprint)
}

func (p *CursorTokenProvider) clearRejectedFingerprint(ctx context.Context, cacheKey string) {
	if p.tokenCache != nil {
		_ = p.tokenCache.DeleteAccessToken(ctx, cursorForceRefreshCacheKey(cacheKey))
	}
}

func cursorForceRefreshCacheKey(cacheKey string) string {
	return cacheKey + ":rejected"
}

func cursorTokenFingerprint(token string) string {
	raw := strings.TrimSpace(token)
	if raw == "" {
		return ""
	}
	parsed, _ := cursorpkg.ParseToken(raw)
	if parsed = strings.TrimSpace(parsed); parsed != "" {
		raw = parsed
	}
	// ParseToken has no error result. A non-empty wrapper such as "uid::"
	// parses to an empty bearer; retain the raw value so rejection fails closed
	// instead of silently omitting its marker.
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func CursorTokenCacheKey(account *Account) string {
	if account == nil {
		return ""
	}
	return "cursor:" + strconv.FormatInt(account.ID, 10)
}

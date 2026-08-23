package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func cursorLifecycleJWT(t *testing.T, tokenType string, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub":  "auth0|cursor-lifecycle-user",
		"type": tokenType,
		"exp":  expiresAt.Unix(),
	})
	require.NoError(t, err)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func cursorLifecycleAccount(credentials map[string]any) *Account {
	return &Account{
		ID:          7301,
		Platform:    PlatformCursor,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: credentials,
	}
}

func cloneCursorLifecycleAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Credentials = shallowCopyMap(account.Credentials)
	return &clone
}

type cursorLifecycleAccountRepo struct {
	AccountRepository
	mu          sync.Mutex
	account     *Account
	getErr      error
	updateErr   error
	updateCalls int
}

func (r *cursorLifecycleAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, r.getErr
	}
	return cloneCursorLifecycleAccount(r.account), nil
}

func (r *cursorLifecycleAccountRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.account == nil || r.account.ID != id {
		r.account = &Account{ID: id, Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	}
	r.account.Credentials = shallowCopyMap(credentials)
	return nil
}

type cursorLifecycleTokenCache struct {
	mu sync.Mutex

	values     map[string]string
	deleted    []string
	getCalls   map[string]int
	setTTLs    map[string]time.Duration
	acquireKey string
	releaseKey string

	forcedLockResult *bool
	lockHeld         bool
	acquireErr       error
	releaseErr       error
	acquireCalls     int
	releaseCalls     int

	publishKey      string
	publishToken    string
	publishOnKeyGet int
	published       chan struct{}
	publishedClose  sync.Once
}

func newCursorLifecycleTokenCache() *cursorLifecycleTokenCache {
	return &cursorLifecycleTokenCache{
		values:   make(map[string]string),
		getCalls: make(map[string]int),
		setTTLs:  make(map[string]time.Duration),
	}
}

func (c *cursorLifecycleTokenCache) GetAccessToken(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls[key]++
	if key == c.publishKey && c.publishOnKeyGet > 0 && c.getCalls[key] >= c.publishOnKeyGet {
		c.values[key] = c.publishToken
	}
	return c.values[key], nil
}

func (c *cursorLifecycleTokenCache) SetAccessToken(_ context.Context, key, token string, ttl time.Duration) error {
	c.mu.Lock()
	c.values[key] = token
	c.setTTLs[key] = ttl
	shouldPublish := c.published != nil && key == c.publishKey
	c.mu.Unlock()
	if shouldPublish {
		c.publishedClose.Do(func() { close(c.published) })
	}
	return nil
}

func (c *cursorLifecycleTokenCache) DeleteAccessToken(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
	c.deleted = append(c.deleted, key)
	return nil
}

func (c *cursorLifecycleTokenCache) AcquireRefreshLock(_ context.Context, key string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acquireCalls++
	c.acquireKey = key
	if c.acquireErr != nil {
		return false, c.acquireErr
	}
	if c.forcedLockResult != nil {
		return *c.forcedLockResult, nil
	}
	if c.lockHeld {
		return false, nil
	}
	c.lockHeld = true
	return true, nil
}

func (c *cursorLifecycleTokenCache) ReleaseRefreshLock(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseCalls++
	c.releaseKey = key
	c.lockHeld = false
	return c.releaseErr
}

func (c *cursorLifecycleTokenCache) value(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[key]
}

func (c *cursorLifecycleTokenCache) callsForKey(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getCalls[key]
}

type cursorLifecycleOAuthClient struct {
	mu sync.Mutex

	exchangeResponse *cursorpkg.TokenResponse
	exchangeErr      error
	refreshResponse  *cursorpkg.TokenResponse
	refreshErr       error
	webResponse      *cursorpkg.TokenResponse
	webErr           error

	exchangeCalls int
	refreshCalls  int
	webCalls      int
	lastAPIKey    string
	lastRefresh   string
	lastWeb       string

	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *cursorLifecycleOAuthClient) waitForRelease() {
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
	}
	if c.release != nil {
		<-c.release
	}
}

func (c *cursorLifecycleOAuthClient) ExchangeUserAPIKey(_ context.Context, apiKey, _ string) (*cursorpkg.TokenResponse, error) {
	c.mu.Lock()
	c.exchangeCalls++
	c.lastAPIKey = apiKey
	response, err := c.exchangeResponse, c.exchangeErr
	c.mu.Unlock()
	c.waitForRelease()
	return response, err
}

func (c *cursorLifecycleOAuthClient) RefreshToken(_ context.Context, refreshToken, _ string) (*cursorpkg.TokenResponse, error) {
	c.mu.Lock()
	c.refreshCalls++
	c.lastRefresh = refreshToken
	response, err := c.refreshResponse, c.refreshErr
	c.mu.Unlock()
	return response, err
}

func (c *cursorLifecycleOAuthClient) ExchangeWebSession(_ context.Context, webSession, _ string) (*cursorpkg.TokenResponse, error) {
	c.mu.Lock()
	c.webCalls++
	c.lastWeb = webSession
	response, err := c.webResponse, c.webErr
	c.mu.Unlock()
	return response, err
}

func (c *cursorLifecycleOAuthClient) PollDeepLink(context.Context, string, string, string) (*cursorpkg.TokenResponse, error) {
	return nil, errors.New("unexpected Cursor deep-link poll")
}

func (c *cursorLifecycleOAuthClient) counts() (exchange, refresh, web int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exchangeCalls, c.refreshCalls, c.webCalls
}

func newCursorLifecycleProvider(account *Account, cache *cursorLifecycleTokenCache, client *cursorLifecycleOAuthClient) (*CursorTokenProvider, *cursorLifecycleAccountRepo) {
	repo := &cursorLifecycleAccountRepo{account: cloneCursorLifecycleAccount(account)}
	oauthService := NewCursorOAuthService(nil, client)
	refresher := NewCursorTokenRefresher(oauthService)
	provider := NewCursorTokenProvider(repo, cache)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), refresher)
	return provider, repo
}

func TestCursorTokenCacheKey(t *testing.T) {
	require.Empty(t, CursorTokenCacheKey(nil))
	require.Equal(t, "cursor:7301", CursorTokenCacheKey(cursorLifecycleAccount(nil)))
}

func TestCursorTokenProviderValidCacheHitAndJWTSkew(t *testing.T) {
	rowToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(3*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": rowToken,
		"expires_at":   time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339),
	})
	cacheKey := "cursor:7301"

	t.Run("valid cached JWT wins", func(t *testing.T) {
		cached := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
		cache := newCursorLifecycleTokenCache()
		cache.values[cacheKey] = cached
		provider := NewCursorTokenProvider(nil, cache)

		got, err := provider.GetAccessToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, cached, got)
	})

	t.Run("cached JWT inside refresh skew is ignored", func(t *testing.T) {
		cache := newCursorLifecycleTokenCache()
		cache.values[cacheKey] = cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Minute))
		provider := NewCursorTokenProvider(nil, cache)

		got, err := provider.GetAccessToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, rowToken, got)
		require.Equal(t, rowToken, cache.value(cacheKey))
	})
}

func TestCursorTokenProviderUpgradesBrowserTokenBeforeChat(t *testing.T) {
	webToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeWeb, time.Now().Add(30*24*time.Hour))
	clientToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token":      webToken,
		"web_session_token": "cursor-lifecycle-user%3A%3A" + webToken,
		"expires_at":        time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	client := &cursorLifecycleOAuthClient{webResponse: &cursorpkg.TokenResponse{AccessToken: clientToken, RefreshToken: "deep-refresh"}}
	provider, _ := newCursorLifecycleProvider(account, cache, client)

	got, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, clientToken, got)
	_, _, webCalls := client.counts()
	require.Equal(t, 1, webCalls)
	require.Equal(t, CursorTokenCacheKey(account), cache.acquireKey)
	require.Equal(t, CursorTokenCacheKey(account), cache.releaseKey)
}

func TestCursorTokenRefresherUsesAPIKeyAndRefreshTokenSources(t *testing.T) {
	clientToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Hour))

	t.Run("crsr api key is re-exchanged", func(t *testing.T) {
		client := &cursorLifecycleOAuthClient{exchangeResponse: &cursorpkg.TokenResponse{AccessToken: clientToken}}
		refresher := NewCursorTokenRefresher(NewCursorOAuthService(nil, client))
		account := cursorLifecycleAccount(map[string]any{"api_key": "crsr_exchange-source"})

		credentials, err := refresher.Refresh(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, clientToken, credentials["access_token"])
		require.Equal(t, "crsr_exchange-source", credentials["api_key"])
		require.Equal(t, "crsr_exchange-source", client.lastAPIKey)
		exchangeCalls, refreshCalls, _ := client.counts()
		require.Equal(t, 1, exchangeCalls)
		require.Zero(t, refreshCalls)
	})

	t.Run("deep-link refresh token is replayed", func(t *testing.T) {
		client := &cursorLifecycleOAuthClient{refreshResponse: &cursorpkg.TokenResponse{AccessToken: clientToken}}
		refresher := NewCursorTokenRefresher(NewCursorOAuthService(nil, client))
		account := cursorLifecycleAccount(map[string]any{"refresh_token": "deep-refresh-source"})

		credentials, err := refresher.Refresh(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, clientToken, credentials["access_token"])
		require.Equal(t, "deep-refresh-source", credentials["refresh_token"])
		require.Equal(t, "deep-refresh-source", client.lastRefresh)
		exchangeCalls, refreshCalls, _ := client.counts()
		require.Zero(t, exchangeCalls)
		require.Equal(t, 1, refreshCalls)
	})
}

func TestCursorTokenRefresherTreatsWebSessionAsRefreshable(t *testing.T) {
	webToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeWeb, time.Now().Add(30*24*time.Hour))
	refresher := NewCursorTokenRefresher(&cursorLifecycleStaticTokenService{})
	account := cursorLifecycleAccount(map[string]any{"access_token": webToken})

	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, time.Hour))
}

func TestCursorTokenProviderInvalidationStoresSHA256FingerprintAndRefusesReuse(t *testing.T) {
	rejected := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": rejected,
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	cache.values[CursorTokenCacheKey(account)] = rejected
	provider := NewCursorTokenProvider(nil, cache)

	require.NoError(t, provider.InvalidateToken(context.Background(), account))
	require.Empty(t, cache.value(CursorTokenCacheKey(account)))
	sum := sha256.Sum256([]byte(rejected))
	fingerprint := cache.value(cursorForceRefreshCacheKey(CursorTokenCacheKey(account)))
	require.Equal(t, hex.EncodeToString(sum[:]), fingerprint)
	require.NotContains(t, fingerprint, rejected)

	got, err := provider.GetAccessToken(context.Background(), account)
	require.Empty(t, got)
	require.ErrorIs(t, err, errCursorAccessTokenRejected)
}

func TestCursorTokenProviderInvalidatesActualCachedBearerFromStaleAccountSnapshot(t *testing.T) {
	staleAccountToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	actualBearer := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(3*time.Hour))
	staleAccount := cursorLifecycleAccount(map[string]any{
		"access_token": staleAccountToken,
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	cache.values[CursorTokenCacheKey(staleAccount)] = actualBearer
	provider := NewCursorTokenProvider(nil, cache)

	got, err := provider.GetAccessToken(context.Background(), staleAccount)
	require.NoError(t, err)
	require.Equal(t, actualBearer, got, "the request uses the cache token, not the stale account snapshot")

	require.NoError(t, provider.InvalidateRejectedToken(context.Background(), staleAccount, got))
	latestAccount := cursorLifecycleAccount(map[string]any{
		"access_token": actualBearer,
		"expires_at":   time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339),
	})
	got, err = provider.GetAccessToken(context.Background(), latestAccount)
	require.Empty(t, got)
	require.ErrorIs(t, err, errCursorAccessTokenRejected)

	actualSum := sha256.Sum256([]byte(actualBearer))
	staleSum := sha256.Sum256([]byte(staleAccountToken))
	marker := cache.value(cursorForceRefreshCacheKey(CursorTokenCacheKey(staleAccount)))
	require.Equal(t, hex.EncodeToString(actualSum[:]), marker)
	require.NotEqual(t, hex.EncodeToString(staleSum[:]), marker)
}

func TestCursorTokenProviderGenericInvalidationNormalizesWrappedBearer(t *testing.T) {
	bearer := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	wrappedAccount := cursorLifecycleAccount(map[string]any{
		"access_token": "cursor-lifecycle-user::" + bearer,
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	provider := NewCursorTokenProvider(nil, cache)

	require.NoError(t, provider.InvalidateToken(context.Background(), wrappedAccount))
	bareAccount := cursorLifecycleAccount(map[string]any{
		"access_token": bearer,
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	got, err := provider.GetAccessToken(context.Background(), bareAccount)
	require.Empty(t, got)
	require.ErrorIs(t, err, errCursorAccessTokenRejected)
}

func TestCursorTokenProviderRejectedFingerprintForcesRotation(t *testing.T) {
	rejected := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	refreshed := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(3*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": rejected,
		"api_key":      "crsr_rotate-source",
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	client := &cursorLifecycleOAuthClient{exchangeResponse: &cursorpkg.TokenResponse{AccessToken: refreshed}}
	provider, _ := newCursorLifecycleProvider(account, cache, client)

	require.NoError(t, provider.InvalidateToken(context.Background(), account))
	got, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, refreshed, got)
	require.Empty(t, cache.value(cursorForceRefreshCacheKey(CursorTokenCacheKey(account))))
}

func TestCursorTokenProviderNeverReturnsSameRejectedTokenAfterRefresh(t *testing.T) {
	rejected := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": rejected,
		"api_key":      "crsr_same-source",
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	client := &cursorLifecycleOAuthClient{exchangeResponse: &cursorpkg.TokenResponse{AccessToken: rejected}}
	provider, _ := newCursorLifecycleProvider(account, cache, client)

	require.NoError(t, provider.InvalidateToken(context.Background(), account))
	got, err := provider.GetAccessToken(context.Background(), account)
	require.Empty(t, got)
	require.ErrorIs(t, err, errCursorAccessTokenRejected)
	require.NotEmpty(t, cache.value(cursorForceRefreshCacheKey(CursorTokenCacheKey(account))))
}

func TestCursorTokenProviderRefreshErrorPolicy(t *testing.T) {
	accessToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Minute))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": accessToken,
		"api_key":      "crsr_transient-source",
		"expires_at":   time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	client := &cursorLifecycleOAuthClient{exchangeErr: errors.New("temporary upstream timeout")}
	provider, _ := newCursorLifecycleProvider(account, cache, client)

	got, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, accessToken, got, "a transient refresh failure may use a still-valid non-rejected token")

	require.NoError(t, provider.InvalidateToken(context.Background(), account))
	got, err = provider.GetAccessToken(context.Background(), account)
	require.Empty(t, got)
	require.Error(t, err, "a rejected token must not be reused after refresh failure")
}

func TestCursorTokenProviderWaiterPollsCacheWithBackoff(t *testing.T) {
	oldToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(-time.Minute))
	refreshed := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token":  oldToken,
		"refresh_token": "deep-refresh-source",
		"expires_at":    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	lockResult := false
	cache.forcedLockResult = &lockResult
	cache.publishKey = CursorTokenCacheKey(account)
	cache.publishToken = refreshed
	cache.publishOnKeyGet = 4 // initial fast-path read, then three waiter polls
	client := &cursorLifecycleOAuthClient{}
	provider, _ := newCursorLifecycleProvider(account, cache, client)
	waits := make([]time.Duration, 0, 3)
	provider.waitBeforePoll = func(_ context.Context, wait time.Duration) bool {
		waits = append(waits, wait)
		return true
	}

	got, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, refreshed, got)
	require.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}, waits)
	require.Equal(t, 4, cache.callsForKey(CursorTokenCacheKey(account)))
	exchangeCalls, refreshCalls, webCalls := client.counts()
	require.Zero(t, exchangeCalls+refreshCalls+webCalls)
}

func TestCursorTokenProviderLockErrorsDoNotDiscardSuccessfulRotation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		acquireErr error
		releaseErr error
	}{
		{name: "acquire error degrades to process-local lock", acquireErr: errors.New("redis unavailable")},
		{name: "release error does not erase refresh result", releaseErr: errors.New("refresh lock ownership lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(-time.Minute))
			refreshed := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
			account := cursorLifecycleAccount(map[string]any{
				"access_token": oldToken,
				"api_key":      "crsr_lock-source",
				"expires_at":   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			})
			cache := newCursorLifecycleTokenCache()
			cache.acquireErr = tc.acquireErr
			cache.releaseErr = tc.releaseErr
			client := &cursorLifecycleOAuthClient{exchangeResponse: &cursorpkg.TokenResponse{AccessToken: refreshed}}
			provider, _ := newCursorLifecycleProvider(account, cache, client)

			got, err := provider.GetAccessToken(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, refreshed, got)
			if tc.acquireErr != nil {
				require.Zero(t, cache.releaseCalls)
			} else {
				require.Equal(t, 1, cache.releaseCalls)
			}
		})
	}
}

func TestCursorTokenProviderDistributedLockWinnerPublishesForWaiter(t *testing.T) {
	oldToken := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(-time.Minute))
	refreshed := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(2*time.Hour))
	account := cursorLifecycleAccount(map[string]any{
		"access_token": oldToken,
		"api_key":      "crsr_concurrent-source",
		"expires_at":   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	})
	cache := newCursorLifecycleTokenCache()
	cache.publishKey = CursorTokenCacheKey(account)
	cache.published = make(chan struct{})
	client := &cursorLifecycleOAuthClient{
		exchangeResponse: &cursorpkg.TokenResponse{AccessToken: refreshed},
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	winner, repo := newCursorLifecycleProvider(account, cache, client)
	refresher := NewCursorTokenRefresher(NewCursorOAuthService(nil, client))
	waiter := NewCursorTokenProvider(repo, cache)
	waiter.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), refresher)
	waiterPolling := make(chan struct{})
	var waiterOnce sync.Once
	waiter.waitBeforePoll = func(ctx context.Context, _ time.Duration) bool {
		waiterOnce.Do(func() { close(waiterPolling) })
		select {
		case <-ctx.Done():
			return false
		case <-cache.published:
			return true
		}
	}

	type result struct {
		token string
		err   error
	}
	winnerResult := make(chan result, 1)
	go func() {
		token, err := winner.GetAccessToken(context.Background(), account)
		winnerResult <- result{token: token, err: err}
	}()
	<-client.entered

	waiterResult := make(chan result, 1)
	go func() {
		token, err := waiter.GetAccessToken(context.Background(), account)
		waiterResult <- result{token: token, err: err}
	}()
	<-waiterPolling
	close(client.release)

	for _, ch := range []<-chan result{winnerResult, waiterResult} {
		result := <-ch
		require.NoError(t, result.err)
		require.Equal(t, refreshed, result.token)
	}
	require.Equal(t, 2, cache.acquireCalls, "both workers attempt the distributed lock")
	require.Equal(t, 1, cache.releaseCalls, "only the winning worker releases the lock")
}

func TestCursorCredentialFailureStopsOnlyForMissingProviderConfiguration(t *testing.T) {
	providerErrors := []error{
		errCursorRefreshNotConfigured,
		infraerrors.New(500, "CURSOR_OAUTH_CLIENT_NOT_CONFIGURED", "cursor oauth client is not configured"),
	}
	for _, err := range providerErrors {
		class := classifyCursorCredentialFailure(err)
		require.Equal(t, GatewayFailureScopeProvider, class.scope)
		require.Equal(t, NextAccountStop, class.action)
	}

	for _, err := range []error{
		errCursorAccessTokenMissing,
		errCursorAccessTokenExpired,
		errCursorAccessTokenRejected,
		errors.New("invalid_grant: account refresh token rejected"),
		errors.New("temporary cursor upstream failure"),
	} {
		class := classifyCursorCredentialFailure(err)
		require.Equal(t, GatewayFailureScopeAccount, class.scope)
		require.Equal(t, NextAccountRetry, class.action)
	}
}

type cursorLifecycleStaticTokenService struct {
	token *CursorTokenInfo
	err   error
}

func (s *cursorLifecycleStaticTokenService) RefreshAccountToken(context.Context, *Account) (*CursorTokenInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.token != nil {
		return s.token, nil
	}
	return &CursorTokenInfo{AccessToken: "unused"}, nil
}

func (s *cursorLifecycleStaticTokenService) BuildAccountCredentials(token *CursorTokenInfo) map[string]any {
	if token == nil {
		return nil
	}
	return map[string]any{"access_token": token.AccessToken}
}

type cursorLifecycleCandidatePager struct {
	options []OAuthRefreshPageOptions
}

func (p *cursorLifecycleCandidatePager) ListOAuthRefreshCandidatePage(_ context.Context, options OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error) {
	p.options = append(p.options, options)
	return &OAuthRefreshCandidatePage{}, nil
}

func TestTokenRefreshServiceRegistersCursorWithoutDroppingCurrentProviders(t *testing.T) {
	service := NewTokenRefreshService(nil, nil, nil, nil, nil, nil, nil, nil, &config.Config{}, nil)
	service.RegisterCursorRefresher(&cursorLifecycleStaticTokenService{})

	require.Equal(t, []string{
		PlatformAnthropic,
		PlatformOpenAI,
		PlatformGemini,
		PlatformAntigravity,
		PlatformKiro,
		PlatformGrok,
		PlatformCursor,
	}, service.eligiblePlatforms())
}

func TestTokenRefreshServiceCursorCandidateOptionsIncludeAlternateSources(t *testing.T) {
	pager := &cursorLifecycleCandidatePager{}
	refresher := NewCursorTokenRefresher(&cursorLifecycleStaticTokenService{})
	service := &TokenRefreshService{
		candidatePager: pager,
		registrations: []tokenRefreshRegistration{{
			platform:  PlatformCursor,
			refresher: refresher,
			executor:  refresher,
		}},
		refreshPolicy: DefaultBackgroundRefreshPolicy(),
		cfg:           &config.TokenRefreshConfig{},
	}

	service.processRefreshContext(context.Background())
	require.Len(t, pager.options, 1)
	require.True(t, pager.options[0].RequireRefreshToken)
	require.Equal(t, []AltRefreshCredentialSource{{
		Platform:       PlatformCursor,
		CredentialKeys: []string{"api_key", "web_session_token"},
	}}, pager.options[0].AltRefreshCredentialSources)
}

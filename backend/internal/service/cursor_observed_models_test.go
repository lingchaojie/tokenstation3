package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestCursorObservedModelsAreAuthoritativeNormalizedAndAliasDefault(t *testing.T) {
	extra := map[string]any{
		"cursor_observed_models": map[string]any{
			"models":     []any{" auto ", "gpt-5", "auto", "", "  "},
			"fetched_at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	require.Equal(t, []string{"auto", "gpt-5"}, CursorObservedModelIDs(extra))
	observed := CursorObservedModelSet(extra)
	require.True(t, CursorModelObserved(observed, "default"))
	require.True(t, CursorModelObserved(observed, "AUTO"))
	require.False(t, CursorModelObserved(observed, "claude-4.6-sonnet"))
}

func TestCursorObservedModelsBlankSnapshotIsUnusable(t *testing.T) {
	extra := map[string]any{
		"cursor_observed_models": map[string]any{
			"models":     []any{"", "  "},
			"fetched_at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	require.Nil(t, CursorObservedModelIDs(extra))
	require.Nil(t, CursorObservedModelSet(extra))
}

func TestGatewayCursorObservedSnapshotIsAuthoritativeAndFiltersAliases(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{
		{
			ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"sonnet-alias": "claude-4.6-sonnet-max",
				"gpt-alias":    "gpt-5",
			}},
			Extra: cursorObservedExtra("claude-4.6-sonnet"),
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	require.Equal(t,
		[]string{"claude-4.6-sonnet", "sonnet-alias"},
		svc.GetAvailableModels(context.Background(), nil, PlatformCursor),
	)
}

func TestGatewayCursorFallbackAppliesOnlyToAccountsWithoutUsableSnapshot(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{
		{
			ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true,
			Extra: cursorObservedExtra("account-one-only"),
		},
		{
			ID: 2, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true,
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	models := svc.GetAvailableModels(context.Background(), nil, PlatformCursor)
	require.Contains(t, models, "account-one-only")
	for _, fallbackID := range cursorpkg.DefaultModelIDs() {
		require.Contains(t, models, fallbackID)
	}

	repo.accounts = repo.accounts[:1]
	svc = &GatewayService{accountRepo: repo}
	models = svc.GetAvailableModels(context.Background(), nil, PlatformCursor)
	require.Equal(t, []string{"account-one-only"}, models)
	for _, fallbackID := range cursorpkg.DefaultModelIDs() {
		require.NotContains(t, models, fallbackID)
	}
}

func TestGatewayCursorDisabledOnlySnapshotIsExcluded(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{
		{
			ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusDisabled, Schedulable: false,
			Extra: cursorObservedExtra("disabled-only-model"),
		},
	}}
	svc := &GatewayService{accountRepo: repo}

	require.Empty(t, svc.GetAvailableModels(context.Background(), nil, PlatformCursor))
	require.NotContains(t, svc.GetSchedulablePlatforms(context.Background(), nil), PlatformCursor)
}

func TestGatewayCursorModelsBypassCacheWhenEligibilityChanges(t *testing.T) {
	t.Run("Cursor platform", func(t *testing.T) {
		repo := &cursorObservedModelsRepo{accounts: []Account{{
			ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true,
			Extra: cursorObservedExtra("enabled-cursor-model"),
		}}}
		svc := &GatewayService{
			accountRepo: repo, modelsListCache: gocache.New(time.Minute, time.Minute), modelsListCacheTTL: time.Minute,
		}

		require.Equal(t, []string{"enabled-cursor-model"}, svc.GetAvailableModels(context.Background(), nil, PlatformCursor))
		repo.replaceAccounts([]Account{{
			ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
			Status: StatusDisabled, Schedulable: false,
			Extra: cursorObservedExtra("enabled-cursor-model"),
		}})

		require.Empty(t, svc.GetAvailableModels(context.Background(), nil, PlatformCursor))
	})

	t.Run("aggregate platform", func(t *testing.T) {
		repo := &cursorObservedModelsRepo{accounts: []Account{
			{
				ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
				Status: StatusActive, Schedulable: true,
				Extra: cursorObservedExtra("enabled-cursor-model"),
			},
			{
				ID: 2, Platform: PlatformAnthropic,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-model": "claude-upstream"}},
			},
		}}
		svc := &GatewayService{
			accountRepo: repo, modelsListCache: gocache.New(time.Minute, time.Minute), modelsListCacheTTL: time.Minute,
		}

		require.Equal(t, []string{"claude-model", "enabled-cursor-model"}, svc.GetAvailableModels(context.Background(), nil, ""))
		repo.replaceAccounts([]Account{
			{
				ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
				Status: StatusDisabled, Schedulable: false,
				Extra: cursorObservedExtra("enabled-cursor-model"),
			},
			{
				ID: 2, Platform: PlatformAnthropic,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-model": "claude-upstream"}},
			},
		})

		require.Equal(t, []string{"claude-model"}, svc.GetAvailableModels(context.Background(), nil, ""))
	})
}

func TestCursorObservedModelsSyncUsesRawUnaryAndPreservesAccountDocuments(t *testing.T) {
	credentials := map[string]any{"access_token": "client-token", "refresh_token": "refresh-secret"}
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 71, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: credentials,
		Extra:       map[string]any{"operator_note": "keep-me"},
	}}}
	upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponse("auto", "gpt-5", "gpt-5", " ")}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	require.NoError(t, svc.runOnce(context.Background()))
	require.Len(t, upstream.requests(), 1)
	req := upstream.requests()[0]
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, cursorpkg.EndpointAvailableModels, req.path)
	require.Equal(t, cursorpkg.ContentTypeProto, req.contentType)
	require.Empty(t, req.body, "AvailableModels must be raw unary protobuf, not a Connect envelope")
	require.Equal(t, "Bearer client-token", req.authorization)

	got := repo.account(71)
	require.Equal(t, "keep-me", got.Extra["operator_note"])
	require.Equal(t, credentials, got.Credentials)
	require.Equal(t, []string{"auto", "gpt-5"}, CursorObservedModelIDs(got.Extra))
}

func TestCursorObservedModelsSyncOnlyProcessesEnabledCursorOAuthAccounts(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{
		{ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "one"}},
		{ID: 2, Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusDisabled, Schedulable: false, Credentials: map[string]any{"access_token": "two"}},
		{ID: 3, Platform: PlatformCursor, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "three"}},
		{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "four"}},
	}}
	upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponse("auto")}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	require.NoError(t, svc.runOnce(context.Background()))
	require.Equal(t, []int64{1}, upstream.accountIDs())
	require.Equal(t, []int64{1}, repo.updatedAccountIDs())
}

func TestCursorObservedModelsSyncConfiguredProxyFailsClosed(t *testing.T) {
	proxyID := int64(9)
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 9, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, ProxyID: &proxyID,
		Credentials: map[string]any{"access_token": "client-token"},
	}}}
	upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponse("auto")}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	err := svc.runOnce(context.Background())
	require.ErrorContains(t, err, "proxy")
	require.Empty(t, upstream.requests(), "configured unresolved proxy must never fall back to direct egress")
	require.Empty(t, repo.updatedAccountIDs())
}

func TestCursorObservedModelsSyncRejectsInvalidAssociatedProxyBeforeUpstream(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Minute)
	tests := []struct {
		name    string
		proxyID int64
		proxy   *Proxy
	}{
		{
			name: "mismatched identity", proxyID: 9,
			proxy: &Proxy{ID: 10, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive},
		},
		{
			name: "disabled", proxyID: 9,
			proxy: &Proxy{ID: 9, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusDisabled},
		},
		{
			name: "expired", proxyID: 9,
			proxy: &Proxy{ID: 9, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive, ExpiresAt: &expiredAt},
		},
		{
			name: "unsupported scheme", proxyID: 9,
			proxy: &Proxy{ID: 9, Protocol: "ftp", Host: "proxy.example", Port: 21, Status: StatusActive},
		},
		{
			name: "missing host", proxyID: 9,
			proxy: &Proxy{ID: 9, Protocol: "http", Port: 8080, Status: StatusActive},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyID := tt.proxyID
			repo := &cursorObservedModelsRepo{accounts: []Account{{
				ID: 10, Platform: PlatformCursor, Type: AccountTypeOAuth,
				Status: StatusActive, Schedulable: true, ProxyID: &proxyID, Proxy: tt.proxy,
				Credentials: map[string]any{"access_token": "client-token"},
			}}}
			upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponse("auto")}
			svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

			err := svc.runOnce(context.Background())
			require.ErrorContains(t, err, "proxy")
			require.Empty(t, upstream.requests())
			require.Empty(t, repo.updatedAccountIDs())
		})
	}
}

func TestCursorObservedModelsSyncDoesNotFollowAvailableModelsRedirect(t *testing.T) {
	var redirectedCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedCalls.Add(1)
		_, _ = w.Write(cursorAvailableModelsResponse("redirect-injected-model"))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 11, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "client-token", "base_url": redirector.URL},
	}}}
	upstream := &cursorRedirectAwareHTTPUpstream{client: &http.Client{}}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled: false, AllowInsecureHTTP: true,
	}}}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL, cfg)

	err := svc.runOnce(context.Background())
	require.ErrorContains(t, err, "HTTP 307")
	require.Zero(t, redirectedCalls.Load(), "redirect target must never receive the credential-bearing request")
	require.Empty(t, repo.updatedAccountIDs(), "a redirect response must never publish a snapshot")
}

func TestCursorObservedModelsSyncRejectsResponseBeyondOneMiB(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 12, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "client-token"},
	}}}
	upstream := &cursorObservedModelsUpstream{responseBody: bytes.Repeat([]byte{'x'}, cursorAvailableModelsResponseLimit+1)}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	err := svc.runOnce(context.Background())
	require.ErrorContains(t, err, "too large")
	require.Empty(t, repo.updatedAccountIDs())
}

func TestCursorObservedModelsSyncAcceptsExactlyOneMiBResponse(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 14, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "client-token"},
	}}}
	upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponseAtExactOneMiB(t, "auto")}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	require.NoError(t, svc.runOnce(context.Background()))
	require.Equal(t, []string{"auto"}, CursorObservedModelIDs(repo.account(14).Extra))
}

func TestCursorObservedModelsSyncWithoutSecurityConfigOnlyUsesOfficialBaseURL(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 13, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"access_token": "client-token",
			"base_url":     "https://127.0.0.1/private",
		},
	}}}
	upstream := &cursorObservedModelsUpstream{responseBody: cursorAvailableModelsResponse("auto")}
	svc := NewCursorObservedModelsService(repo, nil, upstream, cursorObservedModelsTTL)

	err := svc.runOnce(context.Background())
	require.ErrorContains(t, err, "base URL")
	require.Empty(t, upstream.requests(), "unvalidated custom URLs must not reach the proxy-aware transport")
}

func TestCursorAvailableModelsURLRejectsAmbiguousOrSensitiveComponentsWithoutLeakingThem(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled: true, UpstreamHosts: []string{"relay.example"}, AllowPrivateHosts: true,
	}}}
	tests := []struct {
		name   string
		raw    string
		secret string
	}{
		{name: "userinfo", raw: "https://operator:USERINFO_SECRET@relay.example/base", secret: "USERINFO_SECRET"},
		{name: "query", raw: "https://relay.example/base?token=QUERY_SECRET", secret: "QUERY_SECRET"},
		{name: "fragment", raw: "https://relay.example/base#FRAGMENT_SECRET", secret: "FRAGMENT_SECRET"},
		{name: "malformed", raw: "https://[MALFORMED_SECRET", secret: "MALFORMED_SECRET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cursorAvailableModelsURL(tt.raw, cfg)
			require.Error(t, err)
			require.Empty(t, got)
			require.NotContains(t, err.Error(), tt.secret)
			require.NotContains(t, err.Error(), tt.raw)
		})
	}
}

func TestCursorAvailableModelsURLUsesOfficialFallbackOrConfiguredAllowlist(t *testing.T) {
	official, err := cursorAvailableModelsURL(cursorpkg.DefaultBaseURL+"/", nil)
	require.NoError(t, err)
	require.Equal(t, cursorpkg.DefaultBaseURL+cursorpkg.EndpointAvailableModels, official)

	_, err = cursorAvailableModelsURL("https://relay.example/base", nil)
	require.Error(t, err)

	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled: true, UpstreamHosts: []string{"relay.example"}, AllowPrivateHosts: true,
	}}}
	allowed, err := cursorAvailableModelsURL("https://relay.example/base/", cfg)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/base"+cursorpkg.EndpointAvailableModels, allowed)

	blocked, err := cursorAvailableModelsURL("https://blocked.example/base", cfg)
	require.Error(t, err)
	require.Empty(t, blocked)
	require.NotContains(t, err.Error(), "blocked.example")
}

func TestCursorObservedModelsServiceStopCancelsInFlightSync(t *testing.T) {
	repo := &cursorObservedModelsRepo{accounts: []Account{{
		ID: 21, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "client-token"},
	}}}
	upstream := &cursorObservedModelsUpstream{
		started: make(chan struct{}),
		block:   true,
	}
	svc := NewCursorObservedModelsService(repo, nil, upstream, 6*time.Hour)
	svc.Start()

	select {
	case <-upstream.started:
	case <-time.After(time.Second):
		t.Fatal("background sync did not start")
	}

	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel and join the in-flight sync")
	}
	require.True(t, upstream.sawCancellation())
}

func cursorObservedExtra(ids ...string) map[string]any {
	models := make([]any, 0, len(ids))
	for _, id := range ids {
		models = append(models, id)
	}
	return map[string]any{
		"cursor_observed_models": map[string]any{
			"models":     models,
			"fetched_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func cursorAvailableModelsResponse(ids ...string) []byte {
	var response cursorpkg.Writer
	for _, id := range ids {
		var model cursorpkg.Writer
		model.WriteString(1, id)
		response.WriteBytes(2, model.Bytes())
	}
	return response.Bytes()
}

func cursorAvailableModelsResponseAtExactOneMiB(t *testing.T, id string) []byte {
	t.Helper()
	const exactOneMiB = 1 << 20
	base := cursorAvailableModelsResponse(id)
	for paddingLength := exactOneMiB - len(base); paddingLength >= exactOneMiB-len(base)-16; paddingLength-- {
		var padding cursorpkg.Writer
		padding.WriteBytes(100, make([]byte, paddingLength))
		body := append(append([]byte(nil), base...), padding.Bytes()...)
		if len(body) == exactOneMiB {
			models, err := cursorpkg.ParseAvailableModelsResponse(body)
			require.NoError(t, err)
			require.Equal(t, id, models[0].Name)
			return body
		}
	}
	t.Fatal("failed to construct an exact 1 MiB parseable AvailableModels response")
	return nil
}

type cursorObservedModelsRepo struct {
	AccountRepository

	mu       sync.Mutex
	accounts []Account
	updated  []int64
}

func (r *cursorObservedModelsRepo) ListSchedulable(context.Context) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneCursorObservedAccounts(r.accounts), nil
}

func (r *cursorObservedModelsRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.ListSchedulable(context.Background())
}

func (r *cursorObservedModelsRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return cloneCursorObservedAccounts(accounts), nil
}

func (r *cursorObservedModelsRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID != id {
			continue
		}
		if r.accounts[i].Extra == nil {
			r.accounts[i].Extra = make(map[string]any)
		}
		for key, value := range updates {
			r.accounts[i].Extra[key] = value
		}
		r.updated = append(r.updated, id)
		return nil
	}
	return ErrAccountNotFound
}

func (r *cursorObservedModelsRepo) account(id int64) Account {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, account := range r.accounts {
		if account.ID == id {
			return cloneCursorObservedAccounts([]Account{account})[0]
		}
	}
	return Account{}
}

func (r *cursorObservedModelsRepo) updatedAccountIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.updated...)
}

func (r *cursorObservedModelsRepo) replaceAccounts(accounts []Account) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts = cloneCursorObservedAccounts(accounts)
}

func cloneCursorObservedAccounts(accounts []Account) []Account {
	out := make([]Account, len(accounts))
	for i := range accounts {
		out[i] = accounts[i]
		out[i].Credentials = shallowCopyMap(accounts[i].Credentials)
		out[i].Extra = shallowCopyMap(accounts[i].Extra)
	}
	return out
}

type cursorObservedModelsRequest struct {
	method        string
	path          string
	contentType   string
	authorization string
	proxyURL      string
	accountID     int64
	body          []byte
}

type cursorObservedModelsUpstream struct {
	mu           sync.Mutex
	responseBody []byte
	requestsSeen []cursorObservedModelsRequest
	started      chan struct{}
	startedOnce  sync.Once
	block        bool
	canceled     bool
}

func (u *cursorObservedModelsUpstream) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requestsSeen = append(u.requestsSeen, cursorObservedModelsRequest{
		method: req.Method, path: req.URL.Path, contentType: req.Header.Get("Content-Type"),
		authorization: req.Header.Get("Authorization"), proxyURL: proxyURL,
		accountID: accountID, body: body,
	})
	u.mu.Unlock()
	if u.started != nil {
		u.startedOnce.Do(func() { close(u.started) })
	}
	if u.block {
		<-req.Context().Done()
		u.mu.Lock()
		u.canceled = true
		u.mu.Unlock()
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(u.responseBody)),
	}, nil
}

func (u *cursorObservedModelsUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *cursorObservedModelsUpstream) requests() []cursorObservedModelsRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]cursorObservedModelsRequest(nil), u.requestsSeen...)
}

func (u *cursorObservedModelsUpstream) accountIDs() []int64 {
	requests := u.requests()
	ids := make([]int64, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.accountID)
	}
	return ids
}

func (u *cursorObservedModelsUpstream) sawCancellation() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.canceled
}

type cursorRedirectAwareHTTPUpstream struct {
	client *http.Client
}

func (u *cursorRedirectAwareHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	client := *u.client
	if HTTPUpstreamRedirectsDisabled(req.Context()) {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client.Do(req)
}

func (u *cursorRedirectAwareHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

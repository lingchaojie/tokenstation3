//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type fakeCursorOAuthClient struct {
	exchange      *cursorpkg.TokenResponse
	exchangeErr   error
	exchangeCalls int
	apiKey        string

	refresh      *cursorpkg.TokenResponse
	refreshErr   error
	refreshCalls int
	refreshToken string

	web        *cursorpkg.TokenResponse
	webErr     error
	webCalls   int
	webSession string

	poll         *cursorpkg.TokenResponse
	pollErr      error
	pollCalls    int
	pollID       string
	pollVerifier string

	proxyURLs []string
}

func (f *fakeCursorOAuthClient) ExchangeUserAPIKey(_ context.Context, apiKey, proxyURL string) (*cursorpkg.TokenResponse, error) {
	f.exchangeCalls++
	f.apiKey = apiKey
	f.proxyURLs = append(f.proxyURLs, proxyURL)
	return f.exchange, f.exchangeErr
}

func (f *fakeCursorOAuthClient) RefreshToken(_ context.Context, refreshToken, proxyURL string) (*cursorpkg.TokenResponse, error) {
	f.refreshCalls++
	f.refreshToken = refreshToken
	f.proxyURLs = append(f.proxyURLs, proxyURL)
	return f.refresh, f.refreshErr
}

func (f *fakeCursorOAuthClient) ExchangeWebSession(_ context.Context, webSession, proxyURL string) (*cursorpkg.TokenResponse, error) {
	f.webCalls++
	f.webSession = webSession
	f.proxyURLs = append(f.proxyURLs, proxyURL)
	return f.web, f.webErr
}

func (f *fakeCursorOAuthClient) PollDeepLink(_ context.Context, id, verifier, proxyURL string) (*cursorpkg.TokenResponse, error) {
	f.pollCalls++
	f.pollID = id
	f.pollVerifier = verifier
	f.proxyURLs = append(f.proxyURLs, proxyURL)
	return f.poll, f.pollErr
}

func cursorOAuthJWT(t *testing.T, tokenType string, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub":  "auth0|user_01",
		"type": tokenType,
		"exp":  expiresAt.Unix(),
	})
	require.NoError(t, err)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestCursorOAuthServiceImportAPIKey(t *testing.T) {
	before := time.Now()
	client := &fakeCursorOAuthClient{exchange: &cursorpkg.TokenResponse{
		AccessToken: "opaque-client-token", RefreshToken: "not-replayable", ExpiresIn: 0,
	}}
	svc := NewCursorOAuthService(nil, client)

	info, err := svc.ImportFromAPIKey(context.Background(), " crsr_secret ", nil)
	require.NoError(t, err)
	require.Equal(t, cursorpkg.CredentialSourceAPIKey, info.Source)
	require.Equal(t, "crsr_secret", info.APIKey)
	require.Empty(t, info.RefreshToken, "API-key refresh responses are not replayable at /oauth/token")
	require.Equal(t, "crsr_secret", client.apiKey)
	require.WithinDuration(t, before.Add(time.Hour), time.Unix(info.ExpiresAt, 0), 2*time.Second)
}

func TestCursorOAuthServicePollPendingThenConfirmed(t *testing.T) {
	client := &fakeCursorOAuthClient{}
	svc := NewCursorOAuthService(nil, client)

	info, err := svc.Poll(context.Background(), " uuid-1 ", " verifier-1 ", nil)
	require.Nil(t, info)
	require.Equal(t, "CURSOR_OAUTH_PENDING", infraerrors.Reason(err))
	require.Equal(t, "uuid-1", client.pollID)
	require.Equal(t, "verifier-1", client.pollVerifier)

	client.poll = &cursorpkg.TokenResponse{
		AccessToken:  cursorOAuthJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Hour)),
		RefreshToken: "refresh-token",
	}
	info, err = svc.Poll(context.Background(), "uuid-1", "verifier-1", nil)
	require.NoError(t, err)
	require.Equal(t, cursorpkg.CredentialSourceDeepLink, info.Source)
	require.Equal(t, "user_01", info.UserID)
}

func TestCursorOAuthServiceCookieImportAndUpgradeRetainWebSession(t *testing.T) {
	webJWT := cursorOAuthJWT(t, cursorpkg.TokenTypeWeb, time.Now().Add(30*24*time.Hour))
	clientJWT := cursorOAuthJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Hour))
	client := &fakeCursorOAuthClient{web: &cursorpkg.TokenResponse{
		AccessToken: clientJWT, RefreshToken: "deep-refresh",
	}}
	svc := NewCursorOAuthService(nil, client)

	imported, err := svc.ImportFromCookie(context.Background(), "user_01::"+webJWT)
	require.NoError(t, err)
	require.Equal(t, webJWT, imported.AccessToken)
	require.Equal(t, "user_01%3A%3A"+webJWT, imported.WebSessionToken)
	require.Equal(t, cursorpkg.CredentialSourceCookie, imported.Source)

	account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: svc.BuildAccountCredentials(imported)}
	upgraded, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, clientJWT, upgraded.AccessToken)
	require.Equal(t, "deep-refresh", upgraded.RefreshToken)
	require.Equal(t, imported.WebSessionToken, upgraded.WebSessionToken)
	require.Equal(t, imported.WebSessionToken, client.webSession)

	credentials := svc.BuildAccountCredentials(upgraded)
	require.Equal(t, imported.WebSessionToken, credentials["web_session_token"])
}

func TestCursorOAuthServiceRefreshSourceOrderAndWebFallback(t *testing.T) {
	clientJWT := cursorOAuthJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(time.Hour))
	t.Run("api key wins and preserves web session", func(t *testing.T) {
		client := &fakeCursorOAuthClient{exchange: &cursorpkg.TokenResponse{AccessToken: clientJWT}}
		svc := NewCursorOAuthService(nil, client)
		account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{
			"api_key": "crsr_source", "refresh_token": "refresh-source", "web_session_token": "user_01%3A%3Aweb-source",
		}}

		info, err := svc.RefreshAccountToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, 1, client.exchangeCalls)
		require.Zero(t, client.refreshCalls)
		require.Zero(t, client.webCalls)
		require.Equal(t, "refresh-source", info.RefreshToken)
		require.Equal(t, "user_01%3A%3Aweb-source", info.WebSessionToken)
	})

	t.Run("dead refresh falls back to retained web session", func(t *testing.T) {
		client := &fakeCursorOAuthClient{
			refreshErr: errors.New("revoked"),
			web:        &cursorpkg.TokenResponse{AccessToken: clientJWT, RefreshToken: "replacement-refresh"},
		}
		svc := NewCursorOAuthService(nil, client)
		account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{
			"refresh_token": "dead-refresh", "web_session_token": "user_01%3A%3Aweb-source",
		}}

		info, err := svc.RefreshAccountToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, 1, client.refreshCalls)
		require.Equal(t, 1, client.webCalls)
		require.Equal(t, "replacement-refresh", info.RefreshToken)
		require.Equal(t, "user_01%3A%3Aweb-source", info.WebSessionToken)
	})
}

func TestCursorOAuthServiceResolvesProxyAndFailsClosed(t *testing.T) {
	proxyID := int64(42)
	t.Run("resolved URL is propagated", func(t *testing.T) {
		client := &fakeCursorOAuthClient{exchange: &cursorpkg.TokenResponse{AccessToken: "opaque-token"}}
		repo := &mockProxyRepoForOAuth{getByIDFunc: func(_ context.Context, id int64) (*Proxy, error) {
			require.Equal(t, proxyID, id)
			return &Proxy{Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Username: "user", Password: "pass"}, nil
		}}
		svc := NewCursorOAuthService(repo, client)

		_, err := svc.ImportFromAPIKey(context.Background(), "crsr_secret", &proxyID)
		require.NoError(t, err)
		require.Equal(t, []string{"socks5://user:pass@127.0.0.1:1080"}, client.proxyURLs)
	})

	for _, tc := range []struct {
		name       string
		repo       ProxyRepository
		wantReason string
	}{
		{name: "missing repository", repo: nil, wantReason: "CURSOR_OAUTH_PROXY_NOT_AVAILABLE"},
		{name: "missing proxy", repo: &mockProxyRepoForOAuth{getByIDFunc: func(context.Context, int64) (*Proxy, error) {
			return nil, ErrProxyNotFound
		}}, wantReason: "CURSOR_OAUTH_PROXY_NOT_FOUND"},
		{name: "lookup failure", repo: &mockProxyRepoForOAuth{getByIDFunc: func(context.Context, int64) (*Proxy, error) {
			return nil, errors.New("database unavailable")
		}}, wantReason: "CURSOR_OAUTH_PROXY_LOOKUP_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeCursorOAuthClient{exchange: &cursorpkg.TokenResponse{AccessToken: "unused"}}
			svc := NewCursorOAuthService(tc.repo, client)
			_, err := svc.ImportFromAPIKey(context.Background(), "crsr_secret", &proxyID)
			require.Equal(t, tc.wantReason, infraerrors.Reason(err))
			require.Zero(t, client.exchangeCalls, "fail-closed proxy errors must not fall back to direct")
		})
	}
}

func TestCursorOAuthServiceBuildsCredentialMap(t *testing.T) {
	svc := NewCursorOAuthService(nil, &fakeCursorOAuthClient{})
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	info := &CursorTokenInfo{
		AccessToken: "access", RefreshToken: "refresh", APIKey: "crsr_key",
		WebSessionToken: "user_01%3A%3Aweb", UserID: "user_01",
		BaseURL: "https://forward.example", ExpiresAt: expiresAt.Unix(),
		Source: cursorpkg.CredentialSourceCookie,
	}

	require.Equal(t, map[string]any{
		"access_token":      "access",
		"refresh_token":     "refresh",
		"api_key":           "crsr_key",
		"web_session_token": "user_01%3A%3Aweb",
		"user_id":           "user_01",
		"base_url":          "https://forward.example",
		"expires_at":        expiresAt.Format(time.RFC3339),
		"credential_source": cursorpkg.CredentialSourceCookie,
	}, svc.BuildAccountCredentials(info))
}

func TestNormalizeCursorReauthorizedCredentialsReplacesMutuallyExclusiveSources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		incoming map[string]any
		kept     string
	}{
		{name: "deep link", incoming: map[string]any{"access_token": "new", "refresh_token": "new-refresh"}, kept: "refresh_token"},
		{name: "api key", incoming: map[string]any{"access_token": "new", "api_key": "crsr_new"}, kept: "api_key"},
		{name: "web session", incoming: map[string]any{"access_token": "new", "web_session_token": "new-web"}, kept: "web_session_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := map[string]any{
				"access_token": "new", "refresh_token": "old-refresh", "api_key": "crsr_old",
				"web_session_token": "old-web", "base_url": "https://forward.example",
			}
			for key, value := range tc.incoming {
				merged[key] = value
			}
			out := NormalizeCursorReauthorizedCredentials(PlatformCursor, tc.incoming, merged)
			for _, key := range []string{"refresh_token", "api_key", "web_session_token"} {
				if key == tc.kept {
					require.Contains(t, out, key)
				} else {
					require.NotContains(t, out, key)
				}
			}
			require.Equal(t, "https://forward.example", out["base_url"])
		})
	}

	stored := map[string]any{"refresh_token": "kept", "api_key": "crsr_kept", "web_session_token": "kept-web"}
	require.Equal(t, stored, NormalizeCursorReauthorizedCredentials(PlatformCursor, map[string]any{"name": "edit"}, stored))
}

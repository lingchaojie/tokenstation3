package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func cursorTransportAccount(proxyID int64, port int) *Account {
	return &Account{
		ID:       7000 + proxyID,
		Platform: PlatformCursor,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy: &Proxy{
			ID: proxyID, Status: StatusActive, Protocol: "http", Host: "127.0.0.1", Port: port,
		},
	}
}

func TestCursorAgentKnobsUseCredentialExtraEnvironmentDefaultPrecedence(t *testing.T) {
	defaults := resolveCursorAgentDefaults(func(key string) string {
		switch key {
		case envCursorAgentBaseURL:
			return "https://env.cursor.example/"
		case envCursorAgentClientVersion:
			return "cli-2026.08.22-env0000"
		case envCursorAgentGhostMode:
			return "false"
		case envCursorAgentFirstByteTimeout:
			return "17s"
		case envCursorAgentIdleTimeout:
			return "19s"
		default:
			return ""
		}
	})
	require.Equal(t, "https://env.cursor.example", defaults.baseURL)
	require.Equal(t, "cli-2026.08.22-env0000", defaults.clientVersion)
	require.False(t, defaults.ghostMode)
	require.Equal(t, 17*time.Second, defaults.firstByteTimeout)
	require.Equal(t, 19*time.Second, defaults.idleTimeout)

	account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Extra: map[string]any{
		extraCursorAgentBaseURL:       "https://extra.cursor.example/",
		extraCursorAgentClientVersion: "cli-2026.08.22-extra00",
		extraCursorAgentGhostMode:     "false",
	}}
	baseURL, accountOverride := cursorAgentBaseURLSource(account)
	require.Equal(t, "https://extra.cursor.example", baseURL)
	require.True(t, accountOverride)
	require.Equal(t, "cli-2026.08.22-extra00", cursorAgentClientVersion(account))
	require.False(t, cursorAgentGhostMode(account))

	account.Credentials = map[string]any{
		credCursorAgentBaseURL:       "https://credential.cursor.example/",
		credCursorAgentClientVersion: "cli-2026.08.22-cred000",
		credCursorAgentGhostMode:     "true",
	}
	baseURL, accountOverride = cursorAgentBaseURLSource(account)
	require.Equal(t, "https://credential.cursor.example", baseURL)
	require.True(t, accountOverride)
	require.Equal(t, "cli-2026.08.22-cred000", cursorAgentClientVersion(account))
	require.True(t, cursorAgentGhostMode(account))

	pins := resolveCursorAgentDefaults(func(string) string { return "" })
	require.Equal(t, cursorpkg.DefaultAgentBaseURL, pins.baseURL)
	require.Equal(t, cursorpkg.DefaultCLIClientVersion, pins.clientVersion)
	require.True(t, pins.ghostMode)
}

func TestValidateCursorAgentHostRejectsAmbiguousOrUnsafeEndpoints(t *testing.T) {
	for _, raw := range []string{
		"", "http://agentn.global.api5.cursor.sh", "https://user:secret@agentn.global.api5.cursor.sh",
		"https://agentn.global.api5.cursor.sh?token=secret", "https://agentn.global.api5.cursor.sh#fragment",
		"https://agentn.global.api5.cursor.sh/not-the-agent-root", "https://127.0.0.1",
	} {
		err := validateCursorAgentHost(nil, raw, false)
		require.Error(t, err, raw)
		require.NotContains(t, err.Error(), "secret")
	}
	require.NoError(t, validateCursorAgentHost(nil, cursorpkg.DefaultAgentBaseURL, false))
	require.NoError(t, validateCursorAgentHost(nil, cursorpkg.AgentBaseURLDirect, false))
	require.NoError(t, validateCursorAgentHost(nil, cursorpkg.AgentBaseURLRegionUS, false))
}

func TestValidateCursorAgentHostUsesCurrentDEVAllowlistForCustomHosts(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"relay.example.com"}

	require.NoError(t, validateCursorAgentHost(cfg, "https://relay.example.com", true))
	require.Error(t, validateCursorAgentHost(cfg, "https://outside.example.com", true))
	require.Error(t, validateCursorAgentHost(cfg, "https://relay.example.com/path", true))

	empty := &config.Config{}
	empty.Security.URLAllowlist.Enabled = true
	empty.Security.URLAllowlist.AllowPrivateHosts = true
	require.Error(t, validateCursorAgentHost(empty, "https://relay.example.com", true))
	require.Error(t, validateCursorAgentHost(nil, "https://relay.example.com", false), "custom env/default host needs security configuration")
}

func TestCursorAgentHTTPClientFailsClosedOnInvalidProxyAssociation(t *testing.T) {
	t.Cleanup(resetCursorAgentClients)
	now := time.Now()
	expired := now.Add(-time.Second)
	tests := []struct {
		name    string
		account *Account
		want    error
	}{
		{name: "loaded proxy without id", account: &Account{
			Platform: PlatformCursor, Type: AccountTypeOAuth,
			Proxy: &Proxy{ID: 8, Status: StatusActive, Protocol: "http", Host: "127.0.0.1", Port: 8080},
		}, want: errCursorAgentProxyInvalid},
		{name: "unresolved", account: func() *Account {
			id := int64(9)
			return &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, ProxyID: &id}
		}(), want: errCursorAgentProxyUnresolved},
		{name: "mismatched id", account: func() *Account {
			a := cursorTransportAccount(10, 8080)
			a.Proxy.ID = 11
			return a
		}(), want: errCursorAgentProxyInvalid},
		{name: "disabled", account: func() *Account {
			a := cursorTransportAccount(12, 8080)
			a.Proxy.Status = StatusDisabled
			return a
		}(), want: errCursorAgentProxyInvalid},
		{name: "expired", account: func() *Account {
			a := cursorTransportAccount(13, 8080)
			a.Proxy.ExpiresAt = &expired
			return a
		}(), want: errCursorAgentProxyInvalid},
		{name: "unsupported scheme", account: func() *Account {
			a := cursorTransportAccount(14, 21)
			a.Proxy.Protocol = "ftp"
			return a
		}(), want: errCursorAgentProxyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := cursorAgentHTTPClient(tc.account)
			require.Nil(t, client)
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, strings.ToLower(err.Error()), "password")
		})
	}
}

func TestCursorAgentHTTPClientReusesAndIsolatesNormalizedProxyIdentity(t *testing.T) {
	t.Cleanup(resetCursorAgentClients)
	resetCursorAgentClients()

	firstAccount := cursorTransportAccount(21, 18080)
	firstAccount.Proxy.Username = "alice"
	firstAccount.Proxy.Password = "first-secret"
	first, err := cursorAgentHTTPClient(firstAccount)
	require.NoError(t, err)
	require.NotNil(t, first.CheckRedirect)
	firstTransport, ok := first.Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, firstTransport.ForceAttemptHTTP2)
	require.NotNil(t, firstTransport.Proxy)

	copyOfSameProxy := cursorTransportAccount(21, 18080)
	copyOfSameProxy.Proxy.Username = "alice"
	copyOfSameProxy.Proxy.Password = "first-secret"
	second, err := cursorAgentHTTPClient(copyOfSameProxy)
	require.NoError(t, err)
	require.Same(t, first, second)

	differentProxyID := cursorTransportAccount(22, 18080)
	differentProxyID.Proxy.Username = "alice"
	differentProxyID.Proxy.Password = "first-secret"
	third, err := cursorAgentHTTPClient(differentProxyID)
	require.NoError(t, err)
	require.NotSame(t, first, third)

	cursorAgentClientMu.Lock()
	defer cursorAgentClientMu.Unlock()
	for key := range cursorAgentClients {
		require.NotContains(t, key, "alice")
		require.NotContains(t, key, "first-secret")
		require.NotContains(t, key, "@")
	}
}

func TestCursorAgentHTTPClientConcurrentReuse(t *testing.T) {
	t.Cleanup(resetCursorAgentClients)
	resetCursorAgentClients()
	account := cursorTransportAccount(31, 19080)

	const workers = 64
	clients := make(chan *http.Client, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := cursorAgentHTTPClient(account)
			clients <- client
			errs <- err
		}()
	}
	wg.Wait()
	close(clients)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var first *http.Client
	for client := range clients {
		if first == nil {
			first = client
		}
		require.Same(t, first, client)
	}
}

type cursorCloseIdleRoundTripper struct {
	mu     sync.Mutex
	closed int
}

func (r *cursorCloseIdleRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func (r *cursorCloseIdleRoundTripper) CloseIdleConnections() {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
}

func (r *cursorCloseIdleRoundTripper) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func TestCursorAgentProxyClientCacheEvictsDeterministicallyAndClosesIdleConnections(t *testing.T) {
	t.Cleanup(resetCursorAgentClients)
	resetCursorAgentClients()
	oldestTransport := &cursorCloseIdleRoundTripper{}

	cursorAgentClientMu.Lock()
	for i := 0; i < cursorAgentClientCacheMaxEntries+1; i++ {
		transport := http.RoundTripper(&cursorCloseIdleRoundTripper{})
		if i == 0 {
			transport = oldestTransport
		}
		key := "proxy-id:" + strconv.Itoa(i)
		cursorAgentClients[key] = &cursorAgentClientEntry{
			client: &http.Client{Transport: transport}, lastUsedNano: int64(i + 1),
		}
	}
	evictOldestCursorAgentClientsLocked()
	size := len(cursorAgentClients)
	cursorAgentClientMu.Unlock()

	require.Equal(t, cursorAgentClientCacheMaxEntries, size)
	require.Equal(t, 1, oldestTransport.count())
}

func TestCursorAgentClientsDisableCredentialRedirectsOverHTTP2(t *testing.T) {
	var targetCalls int
	var protoMajor int
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	redirect := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protoMajor = r.ProtoMajor
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	redirect.EnableHTTP2 = true
	redirect.StartTLS()
	defer redirect.Close()

	client := redirect.Client()
	disableCursorAgentRedirects(client)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, redirect.URL, strings.NewReader("credential body"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, 2, protoMajor)
	require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	require.Zero(t, targetCalls)
}

func resetCursorAgentClients() {
	cursorAgentClientMu.Lock()
	defer cursorAgentClientMu.Unlock()
	for key, entry := range cursorAgentClients {
		if entry != nil {
			closeCursorAgentClient(entry.client)
		}
		delete(cursorAgentClients, key)
	}
}

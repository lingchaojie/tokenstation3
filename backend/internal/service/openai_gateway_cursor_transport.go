package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	envCursorAgentBaseURL          = "SUB2API_CURSOR_AGENT_BASE_URL"
	envCursorAgentClientVersion    = "SUB2API_CURSOR_AGENT_CLIENT_VERSION"
	envCursorAgentGhostMode        = "SUB2API_CURSOR_AGENT_GHOST_MODE"
	envCursorAgentFirstByteTimeout = "SUB2API_CURSOR_AGENT_FIRST_BYTE_TIMEOUT"
	envCursorAgentIdleTimeout      = "SUB2API_CURSOR_AGENT_IDLE_TIMEOUT"
)

const (
	credCursorAgentBaseURL       = "agent_base_url"
	credCursorAgentClientVersion = "agent_client_version"
	credCursorAgentGhostMode     = "agent_ghost_mode"

	extraCursorAgentBaseURL       = "cursor_agent_base_url"
	extraCursorAgentClientVersion = "cursor_agent_client_version"
	extraCursorAgentGhostMode     = "cursor_agent_ghost_mode"
)

type cursorAgentDefaults struct {
	baseURL          string
	clientVersion    string
	ghostMode        bool
	firstByteTimeout time.Duration
	idleTimeout      time.Duration
}

var (
	cursorAgentDefaultsOnce  sync.Once
	cursorAgentDefaultsCache cursorAgentDefaults
)

func cursorAgentProcessDefaults() cursorAgentDefaults {
	cursorAgentDefaultsOnce.Do(func() {
		cursorAgentDefaultsCache = resolveCursorAgentDefaults(os.Getenv)
	})
	return cursorAgentDefaultsCache
}

func resolveCursorAgentDefaults(getenv func(string) string) cursorAgentDefaults {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return cursorAgentDefaults{
		baseURL: strings.TrimRight(firstNonEmpty(
			strings.TrimSpace(getenv(envCursorAgentBaseURL)), cursorpkg.DefaultAgentBaseURL,
		), "/"),
		clientVersion: firstNonEmpty(
			strings.TrimSpace(getenv(envCursorAgentClientVersion)), cursorpkg.DefaultCLIClientVersion,
		),
		ghostMode:        parseCursorBoolDefaultTrue(getenv(envCursorAgentGhostMode)),
		firstByteTimeout: parseCursorDuration(getenv(envCursorAgentFirstByteTimeout), cursorpkg.AgentDefaultFirstByteTimeout),
		idleTimeout:      parseCursorDuration(getenv(envCursorAgentIdleTimeout), cursorpkg.AgentDefaultIdleTimeout),
	}
}

func parseCursorDuration(raw string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseCursorBoolDefaultTrue(raw string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return true
	}
	return parsed
}

func cursorAgentBaseURL(account *Account) string {
	baseURL, _ := cursorAgentBaseURLSource(account)
	return baseURL
}

func cursorAgentBaseURLSource(account *Account) (string, bool) {
	if override := cursorAgentAccountOverride(account, credCursorAgentBaseURL, extraCursorAgentBaseURL); override != "" {
		return strings.TrimRight(override, "/"), true
	}
	return cursorAgentProcessDefaults().baseURL, false
}

func cursorAgentClientVersion(account *Account) string {
	if override := cursorAgentAccountOverride(account, credCursorAgentClientVersion, extraCursorAgentClientVersion); override != "" {
		return override
	}
	return cursorAgentProcessDefaults().clientVersion
}

func cursorAgentGhostMode(account *Account) bool {
	if override := cursorAgentAccountOverride(account, credCursorAgentGhostMode, extraCursorAgentGhostMode); override != "" {
		if parsed, err := strconv.ParseBool(override); err == nil {
			return parsed
		}
	}
	return cursorAgentProcessDefaults().ghostMode
}

func cursorAgentAccountOverride(account *Account, credentialKey, extraKey string) string {
	if account == nil {
		return ""
	}
	if value := strings.TrimSpace(account.GetCredential(credentialKey)); value != "" {
		return value
	}
	return strings.TrimSpace(account.GetExtraString(extraKey))
}

const (
	cursorAgentClientCacheMaxEntries = 64
	cursorAgentClientCacheIdleTTL    = 30 * time.Minute
)

type cursorAgentClientEntry struct {
	client       *http.Client
	lastUsedNano int64
}

var (
	cursorAgentClientMu     sync.Mutex
	cursorAgentClients      = make(map[string]*cursorAgentClientEntry)
	cursorAgentDirectClient *http.Client
	cursorAgentDirectOnce   sync.Once

	errCursorAgentProxyUnresolved = errors.New("cursor: configured proxy is unresolved")
	errCursorAgentProxyInvalid    = errors.New("cursor: configured proxy is unavailable")
	errCursorAgentTransport       = errors.New("cursor: agent transport is unavailable")
	errCursorAgentUnsafeEndpoint  = errors.New("cursor: agent endpoint is not allowed")
)

func cursorAgentHTTPClient(account *Account) (*http.Client, error) {
	if account != nil && account.ProxyID == nil && account.Proxy != nil {
		return nil, errCursorAgentProxyInvalid
	}
	if account == nil || account.ProxyID == nil {
		cursorAgentDirectOnce.Do(func() {
			cursorAgentDirectClient = disableCursorAgentRedirects(cursorpkg.NewAgentHTTPClient())
		})
		return cursorAgentDirectClient, nil
	}
	if account.Proxy == nil {
		return nil, errCursorAgentProxyUnresolved
	}
	if *account.ProxyID <= 0 || account.Proxy.ID != *account.ProxyID || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now()) {
		return nil, errCursorAgentProxyInvalid
	}
	normalizedProxyURL, parsedProxyURL, err := proxyurl.Parse(account.Proxy.URL())
	if err != nil || normalizedProxyURL == "" || parsedProxyURL == nil {
		return nil, errCursorAgentProxyInvalid
	}
	key := cursorAgentProxyCacheKey(account.Proxy)
	if key == "" {
		return nil, errCursorAgentProxyInvalid
	}

	now := time.Now().UnixNano()
	cursorAgentClientMu.Lock()
	defer cursorAgentClientMu.Unlock()
	evictIdleCursorAgentClientsLocked(now)
	if entry := cursorAgentClients[key]; entry != nil && entry.client != nil {
		entry.lastUsedNano = now
		return entry.client, nil
	}

	client := disableCursorAgentRedirects(cursorpkg.NewAgentHTTPClient())
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		return nil, errCursorAgentTransport
	}
	if err := proxyutil.ConfigureTransportProxy(transport, parsedProxyURL); err != nil {
		return nil, errCursorAgentProxyInvalid
	}
	cursorAgentClients[key] = &cursorAgentClientEntry{client: client, lastUsedNano: now}
	evictOldestCursorAgentClientsLocked()
	return client, nil
}

func cursorAgentProxyCacheKey(proxy *Proxy) string {
	if proxy == nil || proxy.ID <= 0 {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(proxy.Host))
	protocol := strings.ToLower(strings.TrimSpace(proxy.Protocol))
	if protocol == "socks5" {
		protocol = "socks5h"
	}
	return fmt.Sprintf("proxy-id:%d|updated:%d|%s|%s|%d", proxy.ID, proxy.UpdatedAt.UnixNano(), protocol, host, proxy.Port)
}

func disableCursorAgentRedirects(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func evictIdleCursorAgentClientsLocked(nowNano int64) {
	now := time.Unix(0, nowNano)
	for key, entry := range cursorAgentClients {
		if entry == nil || entry.client == nil {
			delete(cursorAgentClients, key)
			continue
		}
		if now.Sub(time.Unix(0, entry.lastUsedNano)) > cursorAgentClientCacheIdleTTL {
			closeCursorAgentClient(entry.client)
			delete(cursorAgentClients, key)
		}
	}
}

func evictOldestCursorAgentClientsLocked() {
	type candidate struct {
		key      string
		lastUsed int64
	}
	candidates := make([]candidate, 0, len(cursorAgentClients))
	for key, entry := range cursorAgentClients {
		lastUsed := int64(0)
		if entry != nil {
			lastUsed = entry.lastUsedNano
		}
		candidates = append(candidates, candidate{key: key, lastUsed: lastUsed})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].lastUsed == candidates[j].lastUsed {
			return candidates[i].key < candidates[j].key
		}
		return candidates[i].lastUsed < candidates[j].lastUsed
	})
	remove := len(candidates) - cursorAgentClientCacheMaxEntries
	for i := 0; i < remove; i++ {
		key := candidates[i].key
		if entry := cursorAgentClients[key]; entry != nil {
			closeCursorAgentClient(entry.client)
		}
		delete(cursorAgentClients, key)
	}
}

func closeCursorAgentClient(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if closer, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func validateCursorAgentHost(cfg *config.Config, baseURL string, _ bool) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" ||
		!strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Trim(parsed.EscapedPath(), "/") != "" {
		return errCursorAgentUnsafeEndpoint
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if cursorOfficialAgentHost(host) {
		if parsed.Port() != "" {
			return errCursorAgentUnsafeEndpoint
		}
		return nil
	}
	if cfg == nil {
		return errCursorAgentUnsafeEndpoint
	}

	options := urlvalidator.ValidationOptions{}
	if cfg.Security.URLAllowlist.Enabled {
		options.AllowedHosts = cfg.Security.URLAllowlist.UpstreamHosts
		options.RequireAllowlist = true
		options.AllowPrivate = cfg.Security.URLAllowlist.AllowPrivateHosts
	} else {
		// Match current DEV: disabling the allowlist is an explicit operator
		// choice to permit custom/private relays, while HTTPS remains mandatory.
		options.AllowPrivate = true
	}
	if _, err := urlvalidator.ValidateHTTPSURL(parsed.String(), options); err != nil {
		return errCursorAgentUnsafeEndpoint
	}
	if cfg.Security.URLAllowlist.Enabled && !cfg.Security.URLAllowlist.AllowPrivateHosts {
		if err := urlvalidator.ValidateResolvedIP(host); err != nil {
			return errCursorAgentUnsafeEndpoint
		}
	}

	finalURL, err := url.Parse(cursorpkg.AgentRunURL(parsed.String()))
	if err != nil || finalURL == nil || finalURL.User != nil || finalURL.RawQuery != "" || finalURL.Fragment != "" ||
		finalURL.EscapedPath() != cursorpkg.EndpointAgentRun {
		return errCursorAgentUnsafeEndpoint
	}
	return nil
}

func cursorOfficialAgentHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "agentn.global.api5.cursor.sh", "agent.api5.cursor.sh", "agentn.us.api5.cursor.sh":
		return true
	default:
		return false
	}
}

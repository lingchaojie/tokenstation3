//go:build unit

package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cursorOAuthClientStub struct {
	mu             sync.Mutex
	poll           *cursor.TokenResponse
	refresh        *cursor.TokenResponse
	exchangeErr    map[string]error
	refreshProxy   string
	pollProxy      string
	exchangeProxy  string
	webProxy       string
	webSession     string
	barrierStart   chan struct{}
	barrierRelease <-chan struct{}
	active         int
	maxActive      int
	apiKeyCalls    []string
}

func (s *cursorOAuthClientStub) ExchangeUserAPIKey(_ context.Context, apiKey, proxyURL string) (*cursor.TokenResponse, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.apiKeyCalls = append(s.apiKeyCalls, apiKey)
	s.exchangeProxy = proxyURL
	s.mu.Unlock()
	if s.barrierStart != nil {
		s.barrierStart <- struct{}{}
		<-s.barrierRelease
	}
	if err := s.exchangeErr[apiKey]; err != nil {
		return nil, err
	}
	time.Sleep(10 * time.Millisecond)
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return &cursor.TokenResponse{AccessToken: "cursor-access", RefreshToken: "refresh"}, nil
}

func (s *cursorOAuthClientStub) RefreshToken(_ context.Context, _ string, proxyURL string) (*cursor.TokenResponse, error) {
	s.mu.Lock()
	s.refreshProxy = proxyURL
	s.mu.Unlock()
	return s.refresh, nil
}

func (s *cursorOAuthClientStub) ExchangeWebSession(_ context.Context, session, proxyURL string) (*cursor.TokenResponse, error) {
	s.mu.Lock()
	s.webSession, s.webProxy = session, proxyURL
	s.mu.Unlock()
	return &cursor.TokenResponse{AccessToken: "upgraded-client-token"}, nil
}

func (s *cursorOAuthClientStub) PollDeepLink(_ context.Context, _, _, proxyURL string) (*cursor.TokenResponse, error) {
	s.mu.Lock()
	s.pollProxy = proxyURL
	s.mu.Unlock()
	return s.poll, nil
}

type cursorAdminServiceStub struct {
	service.AdminService
	mu          sync.Mutex
	created     []*service.CreateAccountInput
	panicCreate bool
	createErr   error
	account     *service.Account
	updated     *service.UpdateAccountInput
}

func (s *cursorAdminServiceStub) CreateAccount(_ context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	if s.panicCreate {
		panic("cursor-admin-panic-canary")
	}
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, input)
	return &service.Account{ID: int64(len(s.created)), Name: input.Name, Platform: input.Platform, Type: input.Type, Credentials: input.Credentials, Extra: input.Extra}, nil
}

func (s *cursorAdminServiceStub) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}
func (s *cursorAdminServiceStub) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updated = input
	return &service.Account{ID: id, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth, Credentials: input.Credentials}, nil
}

type cursorProxyRepoStub struct {
	service.ProxyRepository
	proxy *service.Proxy
}

func (s *cursorProxyRepoStub) GetByID(context.Context, int64) (*service.Proxy, error) {
	return s.proxy, nil
}

func newCursorOAuthTestHandler(client *cursorOAuthClientStub, adminSvc service.AdminService) *CursorOAuthHandler {
	return NewCursorOAuthHandler(service.NewCursorOAuthService(nil, client), adminSvc)
}

func newCursorOAuthTestHandlerWithProxy(client *cursorOAuthClientStub, adminSvc service.AdminService) *CursorOAuthHandler {
	return NewCursorOAuthHandler(service.NewCursorOAuthService(&cursorProxyRepoStub{proxy: &service.Proxy{Protocol: "http", Host: "proxy.test", Port: 8080}}, client), adminSvc)
}

func cursorWebSession(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"sub": "user|cursor-user", "type": "web", "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	return "cursor-user%3A%3Aheader." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func cursorHandlerRequest(t *testing.T, h *CursorOAuthHandler, endpoint, body string, fn gin.HandlerFunc) map[string]any {
	t.Helper()
	r := gin.New()
	r.POST(endpoint, fn)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	return response
}

func TestNormalizeCursorImportTokensDeduplicates(t *testing.T) {
	// Catches a mutation that skips trim/split/deduplication before workers are queued.
	require.Equal(t, []string{"a", "b"}, normalizeCursorImportTokens([]string{" a ", "b", "a"}, ""))
}

func TestCursorOAuthHandlerSupportsDeepLinkPollingRefreshAndPasswordContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &cursorOAuthClientStub{refresh: &cursor.TokenResponse{AccessToken: "refreshed", RefreshToken: "next-refresh"}}
	h := newCursorOAuthTestHandler(client, nil)

	r := gin.New()
	r.GET("/capabilities", h.GetCapabilities)
	r.POST("/auth-url", h.GenerateAuthURL)
	r.POST("/poll", h.Poll)
	r.POST("/refresh", h.RefreshToken)
	r.POST("/password", h.AuthorizePassword)

	for _, request := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/capabilities", "", http.StatusOK},
		{http.MethodPost, "/auth-url", `{}`, http.StatusOK},
		{http.MethodPost, "/poll", `{"session_id":"id","state":"verifier"}`, http.StatusOK},
		{http.MethodPost, "/refresh", `{"rt":"refresh"}`, http.StatusOK},
		{http.MethodPost, "/password", `{}`, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		require.Equal(t, request.want, w.Code, w.Body.String())
		if request.path == "/capabilities" {
			require.Contains(t, w.Body.String(), `"password_auth_enabled":false`)
		}
		if request.path == "/auth-url" {
			require.Contains(t, w.Body.String(), "loginDeepControl")
		}
		if request.path == "/poll" {
			require.Contains(t, w.Body.String(), `"status":"pending"`)
		}
		if request.path == "/refresh" {
			require.Contains(t, w.Body.String(), "refreshed")
		}
		if request.path == "/password" {
			require.Contains(t, w.Body.String(), "CURSOR_OAUTH_PASSWORD_UNSUPPORTED")
		}
	}
}

func TestCursorOAuthHandlerImportsAPIKeysWithThreeWorkersInInputOrder(t *testing.T) {
	// Catches removing the bounded worker pool, changing its cap, or creating non-OAuth Cursor accounts.
	gin.SetMode(gin.TestMode)
	client := &cursorOAuthClientStub{}
	adminSvc := &cursorAdminServiceStub{}
	h := newCursorOAuthTestHandler(client, adminSvc)
	r := gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(`{"sso_tokens":["crsr_one, crsr_two", "crsr_three", "crsr_four", "crsr_two"],"name":"Cursor Import","credentials":{"base_url":"https://example.test","refresh_token":"must-not-win"}}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.LessOrEqual(t, client.maxActive, cursorSSOImportConcurrency)
	require.Len(t, adminSvc.created, 4)
	for _, input := range adminSvc.created {
		require.Equal(t, service.PlatformCursor, input.Platform)
		require.Equal(t, service.AccountTypeOAuth, input.Type)
		require.Equal(t, "https://example.test", input.Credentials["base_url"])
		require.NotEqual(t, "must-not-win", input.Credentials["refresh_token"])
	}
	var response struct {
		Data struct {
			Created []struct {
				Index int `json:"index"`
			} `json:"created"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, []int{1, 2, 3, 4}, []int{response.Data.Created[0].Index, response.Data.Created[1].Index, response.Data.Created[2].Index, response.Data.Created[3].Index})
	require.NotContains(t, w.Body.String(), "crsr_one")
}

func TestCursorOAuthImportSanitizesNestedCredentialsAndExtra(t *testing.T) {
	// Catches retaining Authorization/Cookie values inside otherwise permitted nested maps or lists.
	gin.SetMode(gin.TestMode)
	client := &cursorOAuthClientStub{}
	adminSvc := &cursorAdminServiceStub{}
	h := newCursorOAuthTestHandler(client, adminSvc)
	r := gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)
	const credentialCanary = "credential-authorization-canary"
	const extraCanary = "extra-cookie-canary"
	body := `{"sso_token":"crsr_safe","credentials":{"header_overrides":{"Authorization":"` + credentialCanary + `","X-Safe":"safe-value","nested":[{"Cookie":"nested-cookie-canary","X-List-Safe":"list-safe"}]},"custom_headers":{"X-Operator":"operator-safe"}},"extra":{"custom_headers":{"Cookie":"` + extraCanary + `","X-Extra-Safe":"extra-safe"},"list":[{"Proxy-Authorization":"proxy-canary","X-List-Extra":"list-extra-safe"}]}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, adminSvc.created, 1)
	created, _ := json.Marshal(adminSvc.created[0])
	require.NotContains(t, string(created), credentialCanary)
	require.NotContains(t, string(created), extraCanary)
	require.NotContains(t, string(created), "nested-cookie-canary")
	require.NotContains(t, string(created), "proxy-canary")
	require.Contains(t, string(created), "safe-value")
	require.Contains(t, string(created), "operator-safe")
	require.Contains(t, string(created), "extra-safe")
	require.Contains(t, string(created), "list-safe")
	require.Contains(t, string(created), "list-extra-safe")
	require.NotContains(t, w.Body.String(), credentialCanary)
	require.NotContains(t, w.Body.String(), extraCanary)
}

func TestCursorOAuthRoutesForwardProxyAndValidateCredentialPaths(t *testing.T) {
	// Catches dropping proxy_id on poll/API-key/refresh or skipping cookie validation and exchange-code imports.
	gin.SetMode(gin.TestMode)
	client := &cursorOAuthClientStub{poll: &cursor.TokenResponse{AccessToken: "poll-token"}, refresh: &cursor.TokenResponse{AccessToken: "refresh-token"}}
	h := newCursorOAuthTestHandlerWithProxy(client, nil)
	r := gin.New()
	r.POST("/poll", h.Poll)
	r.POST("/refresh", h.RefreshToken)
	r.POST("/sso", h.ValidateSSOToken)
	r.POST("/exchange", h.ExchangeCode)
	for _, request := range []struct{ path, body string }{
		{"/poll", `{"session_id":"id","state":"verifier","proxy_id":9}`},
		{"/refresh", `{"refresh_token":"refresh","proxy_id":9}`},
		{"/sso", `{"sso_token":"crsr_api","proxy_id":9}`},
		{"/exchange", `{"session_id":"manual","code":"crsr_exchange","proxy_id":9}`},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	client.mu.Lock()
	require.Equal(t, "http://proxy.test:8080", client.pollProxy)
	require.Equal(t, "http://proxy.test:8080", client.refreshProxy)
	require.Equal(t, "http://proxy.test:8080", client.exchangeProxy)
	client.mu.Unlock()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso", strings.NewReader(`{"sso_token":"`+cursorWebSession(t)+`"}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"sub":"cursor-user"`)
}

func TestCursorOAuthRefreshesStoredProxyAPIKeyAndWebSessionAccounts(t *testing.T) {
	// Catches account refresh bypassing the stored proxy or failing to upgrade a stored web-session token.
	gin.SetMode(gin.TestMode)
	proxyID := int64(9)
	client := &cursorOAuthClientStub{}
	adminSvc := &cursorAdminServiceStub{account: &service.Account{ID: 42, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth, ProxyID: &proxyID, Credentials: map[string]any{"api_key": "crsr_stored"}}}
	h := newCursorOAuthTestHandlerWithProxy(client, adminSvc)
	r := gin.New()
	r.POST("/accounts/:id/refresh", h.RefreshAccountToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/42/refresh", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	client.mu.Lock()
	require.Equal(t, "http://proxy.test:8080", client.exchangeProxy)
	client.mu.Unlock()
	require.Equal(t, "cursor-access", adminSvc.updated.Credentials["access_token"])

	adminSvc.account = &service.Account{ID: 43, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth, ProxyID: &proxyID, Credentials: map[string]any{"web_session_token": cursorWebSession(t)}}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/43/refresh", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	client.mu.Lock()
	require.Equal(t, "http://proxy.test:8080", client.webProxy)
	require.Contains(t, client.webSession, "cursor-user%3A%3A")
	client.mu.Unlock()
	require.Equal(t, "upgraded-client-token", adminSvc.updated.Credentials["access_token"])
}

type cursorRefreshOAuthStub struct {
	info    *service.CursorTokenInfo
	account *service.Account
}

type cursorAccountRefreshAdminService struct {
	*stubAdminService
	updated map[string]any
}

func (s *cursorAccountRefreshAdminService) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updated = input.Credentials
	return &service.Account{ID: id, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth, Credentials: input.Credentials}, nil
}

func (s *cursorRefreshOAuthStub) RefreshAccountToken(_ context.Context, account *service.Account) (*service.CursorTokenInfo, error) {
	s.account = account
	return s.info, nil
}
func (s *cursorRefreshOAuthStub) BuildAccountCredentials(info *service.CursorTokenInfo) map[string]any {
	return map[string]any{"access_token": info.AccessToken, "refresh_token": info.RefreshToken}
}

func TestAccountHandlerRefreshesCursorThroughCursorOAuthService(t *testing.T) {
	// Catches manual/bulk AccountHandler refresh falling through to the Claude refresher.
	adminSvc := &cursorAccountRefreshAdminService{stubAdminService: newStubAdminService()}
	refresh := &cursorRefreshOAuthStub{info: &service.CursorTokenInfo{AccessToken: "fresh-cursor", RefreshToken: "fresh-refresh"}}
	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetCursorOAuthService(refresh)
	account := &service.Account{ID: 44, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "old", "base_url": "https://operator.example"}}
	updated, warning, err := h.refreshSingleAccount(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Same(t, account, refresh.account)
	require.Equal(t, "fresh-cursor", updated.Credentials["access_token"])
	require.Equal(t, "https://operator.example", updated.Credentials["base_url"])
}

func TestCursorOAuthBulkImportSanitizesProviderAndPanicErrorsAndPreservesMixedOrder(t *testing.T) {
	// Catches exposing provider/panic/admin canaries or losing normalized input order when one item fails.
	gin.SetMode(gin.TestMode)
	client := &cursorOAuthClientStub{exchangeErr: map[string]error{"crsr_bad": errors.New("provider-error-canary")}}
	adminSvc := &cursorAdminServiceStub{}
	h := newCursorOAuthTestHandler(client, adminSvc)
	r := gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(`{"sso_tokens":["crsr_first","crsr_bad","crsr_third"]}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "provider-error-canary")
	var mixed struct {
		Data struct {
			Created []struct {
				Index int `json:"index"`
			} `json:"created"`
			Failed []struct {
				Index int    `json:"index"`
				Error string `json:"error"`
			} `json:"failed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mixed))
	require.Equal(t, []int{1, 3}, []int{mixed.Data.Created[0].Index, mixed.Data.Created[1].Index})
	require.Equal(t, 2, mixed.Data.Failed[0].Index)
	require.Equal(t, "credential import failed", mixed.Data.Failed[0].Error)

	adminSvc = &cursorAdminServiceStub{panicCreate: true}
	h = newCursorOAuthTestHandler(&cursorOAuthClientStub{}, adminSvc)
	r = gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(`{"sso_token":"crsr_panic"}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "internal worker panic")
	require.NotContains(t, w.Body.String(), "cursor-admin-panic-canary")

	adminSvc = &cursorAdminServiceStub{createErr: errors.New("admin-error-canary")}
	h = newCursorOAuthTestHandler(&cursorOAuthClientStub{}, adminSvc)
	r = gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(`{"sso_token":"crsr_admin_error"}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "credential import failed")
	require.NotContains(t, w.Body.String(), "admin-error-canary")
}

func TestCursorOAuthBulkImportUsesExactlyThreeWorkers(t *testing.T) {
	// Catches reducing the worker pool to one as well as exceeding the three-worker security bound.
	gin.SetMode(gin.TestMode)
	started, release := make(chan struct{}, 3), make(chan struct{})
	client := &cursorOAuthClientStub{barrierStart: started, barrierRelease: release}
	h := newCursorOAuthTestHandler(client, &cursorAdminServiceStub{})
	r := gin.New()
	r.POST("/sso-to-oauth", h.CreateAccountsFromSSO)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sso-to-oauth", strings.NewReader(`{"sso_tokens":["crsr_1","crsr_2","crsr_3","crsr_4"]}`)))
		done <- w
	}()
	for i := 0; i < cursorSSOImportConcurrency; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("worker %d did not start", i+1)
		}
	}
	close(release)
	w := <-done
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, cursorSSOImportConcurrency, client.maxActive)
}

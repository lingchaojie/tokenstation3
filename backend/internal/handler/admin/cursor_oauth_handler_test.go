//go:build unit

package admin

import (
	"context"
	"encoding/json"
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
	mu          sync.Mutex
	poll        *cursor.TokenResponse
	refresh     *cursor.TokenResponse
	active      int
	maxActive   int
	apiKeyCalls []string
}

func (s *cursorOAuthClientStub) ExchangeUserAPIKey(_ context.Context, apiKey, _ string) (*cursor.TokenResponse, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.apiKeyCalls = append(s.apiKeyCalls, apiKey)
	s.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return &cursor.TokenResponse{AccessToken: "cursor-access", RefreshToken: "refresh"}, nil
}

func (s *cursorOAuthClientStub) RefreshToken(context.Context, string, string) (*cursor.TokenResponse, error) {
	return s.refresh, nil
}

func (s *cursorOAuthClientStub) ExchangeWebSession(context.Context, string, string) (*cursor.TokenResponse, error) {
	return &cursor.TokenResponse{AccessToken: "upgraded-client-token"}, nil
}

func (s *cursorOAuthClientStub) PollDeepLink(context.Context, string, string, string) (*cursor.TokenResponse, error) {
	return s.poll, nil
}

type cursorAdminServiceStub struct {
	service.AdminService
	mu      sync.Mutex
	created []*service.CreateAccountInput
}

func (s *cursorAdminServiceStub) CreateAccount(_ context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, input)
	return &service.Account{ID: int64(len(s.created)), Name: input.Name, Platform: input.Platform, Type: input.Type, Credentials: input.Credentials}, nil
}

func newCursorOAuthTestHandler(client *cursorOAuthClientStub, adminSvc service.AdminService) *CursorOAuthHandler {
	return NewCursorOAuthHandler(service.NewCursorOAuthService(nil, client), adminSvc)
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

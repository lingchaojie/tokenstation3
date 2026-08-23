package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorAccountTestUsesRawAvailableModelsAndPersistsAfterReadiness(t *testing.T) {
	h := newCursorAccountTestHarness(t, "client-token", cursorAvailableModelsResponse("auto", "gpt-5"))

	require.NoError(t, h.service.TestAccountConnection(h.context, h.account.ID, "", "", ""))
	require.Len(t, h.upstream.requests(), 1)
	req := h.upstream.requests()[0]
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, cursorpkg.EndpointAvailableModels, req.path)
	require.Equal(t, cursorpkg.ContentTypeProto, req.contentType)
	require.Equal(t, "Bearer client-token", req.authorization)
	require.Empty(t, req.body, "AvailableModels request must not be Connect framed")
	require.NotContains(t, req.path, "anthropic")
	require.NotContains(t, req.path, "chat")
	require.Contains(t, h.recorder.Body.String(), `"type":"test_complete"`)
	require.Contains(t, h.recorder.Body.String(), `"success":true`)
	require.Equal(t, []string{"auto", "gpt-5"}, CursorObservedModelIDs(h.repo.account.Extra))
	require.Equal(t, "keep-me", h.repo.account.Extra["operator_note"])
}

func TestCursorAccountTestConfiguredUnresolvedProxyFailsClosed(t *testing.T) {
	h := newCursorAccountTestHarness(t, "client-token", cursorAvailableModelsResponse("auto"))
	proxyID := int64(44)
	h.account.ProxyID = &proxyID
	h.repo.account.ProxyID = &proxyID

	err := h.service.TestAccountConnection(h.context, h.account.ID, "", "", "")
	require.ErrorContains(t, err, "proxy")
	require.Empty(t, h.upstream.requests())
	require.Nil(t, CursorObservedModelIDs(h.repo.account.Extra))
}

func TestCursorAccountTestWebTokenIsNotChatReadyEvenWhenModelsSucceed(t *testing.T) {
	webToken := cursorAccountTestJWT(t, cursorpkg.TokenTypeWeb)
	h := newCursorAccountTestHarness(t, webToken, cursorAvailableModelsResponse("auto"))

	err := h.service.TestAccountConnection(h.context, h.account.ID, "", "", "")
	require.ErrorContains(t, err, "web session token")
	require.Len(t, h.upstream.requests(), 1, "the fixture proves AvailableModels itself succeeded")
	require.NotContains(t, h.recorder.Body.String(), `"success":true`)
	require.Nil(t, CursorObservedModelIDs(h.repo.account.Extra), "unready credentials must not publish a model snapshot")
}

func TestCursorAccountTestRejectsAvailableModelsBeyondOneMiB(t *testing.T) {
	h := newCursorAccountTestHarness(t, "client-token", bytes.Repeat([]byte{'x'}, cursorAvailableModelsResponseLimit+1))

	err := h.service.TestAccountConnection(h.context, h.account.ID, "", "", "")
	require.ErrorContains(t, err, "too large")
	require.Nil(t, CursorObservedModelIDs(h.repo.account.Extra))
}

func TestCursorAccountTestDoesNotExposeUpstreamErrorBody(t *testing.T) {
	h := newCursorAccountTestHarness(t, "client-token", nil)
	h.upstream.status = http.StatusBadGateway
	h.upstream.responseBody = []byte(`{"error":"SECRET_CURSOR_BEARER"}`)

	err := h.service.TestAccountConnection(h.context, h.account.ID, "", "", "")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET_CURSOR_BEARER")
	require.NotContains(t, h.recorder.Body.String(), "SECRET_CURSOR_BEARER")
}

type cursorAccountTestHarness struct {
	service  *AccountTestService
	repo     *cursorAccountTestRepo
	upstream *cursorAccountTestUpstream
	account  *Account
	context  *gin.Context
	recorder *httptest.ResponseRecorder
}

func newCursorAccountTestHarness(t *testing.T, token string, responseBody []byte) *cursorAccountTestHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	account := &Account{
		ID: 101, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": token},
		Extra:       map[string]any{"operator_note": "keep-me"},
	}
	repo := &cursorAccountTestRepo{account: cloneCursorAccountTestAccount(account)}
	upstream := &cursorAccountTestUpstream{status: http.StatusOK, responseBody: responseBody}
	service := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false,
		}}},
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/101/test", nil)
	return &cursorAccountTestHarness{
		service: service, repo: repo, upstream: upstream,
		account: account, context: ginContext, recorder: recorder,
	}
}

func cursorAccountTestJWT(t *testing.T, tokenType string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub": "auth0|cursor-account-test", "type": tokenType,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

type cursorAccountTestRepo struct {
	AccountRepository

	mu      sync.Mutex
	account *Account
}

func (r *cursorAccountTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return cloneCursorAccountTestAccount(r.account), nil
}

func (r *cursorAccountTestRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil || r.account.ID != id {
		return ErrAccountNotFound
	}
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func cloneCursorAccountTestAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Credentials = shallowCopyMap(account.Credentials)
	clone.Extra = shallowCopyMap(account.Extra)
	return &clone
}

type cursorAccountTestUpstream struct {
	mu           sync.Mutex
	status       int
	responseBody []byte
	seen         []cursorObservedModelsRequest
}

func (u *cursorAccountTestUpstream) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.seen = append(u.seen, cursorObservedModelsRequest{
		method: req.Method, path: req.URL.Path, contentType: req.Header.Get("Content-Type"),
		authorization: req.Header.Get("Authorization"), proxyURL: proxyURL,
		accountID: accountID, body: body,
	})
	status := u.status
	responseBody := append([]byte(nil), u.responseBody...)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(responseBody))),
	}, nil
}

func (u *cursorAccountTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *cursorAccountTestUpstream) requests() []cursorObservedModelsRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]cursorObservedModelsRequest(nil), u.seen...)
}

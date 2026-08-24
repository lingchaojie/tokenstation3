package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func webJWT(t *testing.T) string {
	t.Helper()
	return makeJWT(t, map[string]any{
		"sub": "auth0|user_01WEB", "type": TokenTypeWeb,
		"scope": "openid profile email offline_access",
		"aud":   "https://cursor.com", "iss": "https://authentication.cursor.sh",
		"iat": time.Now().Add(-time.Hour).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
}

func clientJWT(t *testing.T) string {
	t.Helper()
	return makeJWT(t, map[string]any{
		"sub": "auth0|user_01WEB", "type": TokenTypeSession,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
}

func TestTokenClaimsDistinguishWebAndClientJWTs(t *testing.T) {
	claims, ok := ParseTokenClaims("user_01WEB%3A%3A" + webJWT(t))
	require.True(t, ok)
	require.Equal(t, "auth0|user_01WEB", claims.Subject)
	require.Equal(t, "user_01WEB", claims.UserID)
	require.Equal(t, TokenTypeWeb, claims.Type)
	require.Equal(t, "openid profile email offline_access", claims.Scope)
	require.Equal(t, "https://cursor.com", claims.Audience)
	require.Equal(t, "https://authentication.cursor.sh", claims.Issuer)
	require.False(t, claims.IssuedAt.IsZero())
	require.False(t, claims.ExpiresAt.IsZero())
	require.True(t, claims.IsWeb())

	require.Equal(t, TokenTypeSession, TokenType(clientJWT(t)))
	require.False(t, IsWebSessionToken(clientJWT(t)))
	require.True(t, IsWebSessionToken(webJWT(t)))
	for _, malformed := range []string{"", "crsr_key", "a.%%%.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"type":7}`)) + ".c"} {
		_, ok := ParseTokenClaims(malformed)
		require.False(t, ok)
	}
}

func TestBuildDeepLoginURLCarriesFreshPKCEState(t *testing.T) {
	login, err := BuildDeepLoginURL()
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(login.Verifier))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), login.Challenge)
	require.Contains(t, login.LoginURL, "challenge="+login.Challenge)
	require.Contains(t, login.LoginURL, "uuid="+login.UUID)
	require.Contains(t, login.LoginURL, "mode=login")

	other, err := BuildDeepLoginURL()
	require.NoError(t, err)
	require.NotEqual(t, login.UUID, other.UUID)
	require.NotEqual(t, login.Verifier, other.Verifier)
}

type exchangeFixture struct {
	server      *httptest.Server
	approvals   atomic.Int64
	polls       atomic.Int64
	pendingFor  int64
	approveCode int
	tokenBody   string
	cookie      atomic.Value
	challenge   atomic.Value
	verifier    atomic.Value
}

func newExchangeFixture(t *testing.T, tokenBody string) *exchangeFixture {
	t.Helper()
	f := &exchangeFixture{approveCode: http.StatusOK, tokenBody: tokenBody}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EndpointLoginDeepCallbackControl:
			f.approvals.Add(1)
			f.cookie.Store(r.Header.Get("cookie"))
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
			f.challenge.Store(string(body))
			w.WriteHeader(f.approveCode)
		case EndpointAuthPoll:
			n := f.polls.Add(1)
			f.verifier.Store(r.URL.Query().Get("verifier"))
			if n <= f.pendingFor {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, f.tokenBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *exchangeFixture) options() ExchangeOptions {
	return ExchangeOptions{
		HTTPClient: f.server.Client(), WebsiteBaseURL: f.server.URL, APIBaseURL: f.server.URL,
		PollAttempts: 5, Sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func atomicString(v *atomic.Value) string {
	got, _ := v.Load().(string)
	return got
}

func TestExchangeWebSessionApprovesCookieAndPollsPending(t *testing.T) {
	client := clientJWT(t)
	f := newExchangeFixture(t, `{"accessToken":"`+client+`","refresh_token":"refresh-1","authId":"auth0|user_01WEB"}`)
	f.pendingFor = 2
	web := webJWT(t)

	token, err := ExchangeWebSessionWithOptions(context.Background(), "user_01WEB::"+web, f.options())
	require.NoError(t, err)
	require.Equal(t, client, token.AccessToken)
	require.Equal(t, "refresh-1", token.RefreshToken)
	require.Equal(t, int64(1), f.approvals.Load())
	require.Equal(t, int64(3), f.polls.Load())
	require.Equal(t, SessionCookieName+"=user_01WEB%3A%3A"+web, atomicString(&f.cookie))
	require.Contains(t, atomicString(&f.challenge), `"uuid":`)
	require.Contains(t, atomicString(&f.challenge), `"challenge":`)
	require.NotEmpty(t, atomicString(&f.verifier))
}

func TestPollDeepLoginTreats404AndEmptyTokenAsPending(t *testing.T) {
	f := newExchangeFixture(t, `{}`)
	f.pendingFor = 1

	token, err := PollDeepLoginOnce(context.Background(), f.options(), "uuid-1", "verifier-1")
	require.NoError(t, err)
	require.Nil(t, token)
	token, err = PollDeepLoginOnce(context.Background(), f.options(), "uuid-1", "verifier-1")
	require.NoError(t, err)
	require.Nil(t, token)

	f.pendingFor = 100
	_, err = PollDeepLogin(context.Background(), f.options(), "uuid-1", "verifier-1")
	require.ErrorIs(t, err, ErrDeepLoginPending)
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestExchangeWebSessionUsesOfficialCredentialHosts(t *testing.T) {
	client := clientJWT(t)
	var approved, polled bool
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case EndpointLoginDeepCallbackControl:
			approved = true
			require.Equal(t, WebsiteBaseURL, "https://"+req.URL.Host)
			return response(http.StatusOK, `{}`), nil
		case EndpointAuthPoll:
			polled = true
			require.Equal(t, DefaultBaseURL, "https://"+req.URL.Host)
			return response(http.StatusOK, `{"access_token":"`+client+`","refresh_token":"refresh"}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})

	access, refresh, err := ExchangeWebSession(context.Background(), doer, webJWT(t))
	require.NoError(t, err)
	require.Equal(t, client, access)
	require.Equal(t, "refresh", refresh)
	require.True(t, approved)
	require.True(t, polled)
}

func TestExchangeWebSessionRejectsUnauthorizedAndWebResult(t *testing.T) {
	unauthorized := newExchangeFixture(t, `{}`)
	unauthorized.approveCode = http.StatusUnauthorized
	_, err := ExchangeWebSessionWithOptions(context.Background(), webJWT(t), unauthorized.options())
	require.ErrorIs(t, err, ErrWebSessionUnauthorized)
	require.Zero(t, unauthorized.polls.Load())

	notUpgraded := newExchangeFixture(t, `{"accessToken":"`+webJWT(t)+`"}`)
	_, err = ExchangeWebSessionWithOptions(context.Background(), webJWT(t), notUpgraded.options())
	require.ErrorIs(t, err, ErrWebSessionNotUpgraded)
}

func TestAuthResponsesAreBounded(t *testing.T) {
	f := newExchangeFixture(t, strings.Repeat("x", maxAuthBody+1))
	_, err := PollDeepLoginOnce(context.Background(), f.options(), "uuid", "verifier")
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

func TestPollDeepLoginRejectsMissingResponseBody(t *testing.T) {
	doer := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, nil
	})
	require.NotPanics(t, func() {
		_, err := PollDeepLoginOnce(context.Background(), ExchangeOptions{HTTPClient: doer}, "uuid", "verifier")
		require.Error(t, err)
	})
}

func TestAuthErrorsDoNotExposeTokensOrVerifier(t *testing.T) {
	web := webJWT(t)
	badDoer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("transport leaked " + web + " " + req.URL.String())
	})

	err := ApproveDeepLogin(context.Background(), ExchangeOptions{HTTPClient: badDoer}, web, "uuid", "challenge")
	require.Error(t, err)
	require.NotContains(t, err.Error(), web)

	const verifier = "super-secret-verifier"
	_, err = PollDeepLoginOnce(context.Background(), ExchangeOptions{HTTPClient: badDoer}, "uuid", verifier)
	require.Error(t, err)
	require.NotContains(t, err.Error(), verifier)
}

func TestPollDeepLoginHonorsContextCancellation(t *testing.T) {
	f := newExchangeFixture(t, `{}`)
	f.pendingFor = 100
	opts := f.options()
	opts.Sleep = nil
	opts.PollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PollDeepLogin(ctx, opts, "uuid", "verifier")
	require.ErrorIs(t, err, context.Canceled)
}

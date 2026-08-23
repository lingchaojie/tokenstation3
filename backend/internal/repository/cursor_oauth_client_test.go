//go:build unit

package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type cursorRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f cursorRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCursorOAuthClientUsesOfficialAuthBaseURL(t *testing.T) {
	client := NewCursorOAuthClient().(*cursorOAuthClient)
	require.Equal(t, cursorpkg.DefaultBaseURL, client.baseURL)
}

func TestCursorOAuthClientExchangeRefreshAndPollAcceptResponseSpellings(t *testing.T) {
	pollCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "sub2api-cursor-oauth/1.0", r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case cursorpkg.EndpointExchangeUserAPIKey:
			require.Equal(t, http.MethodPost, r.Method)
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"apiKey":"crsr_secret"}`, string(body))
			_, _ = io.WriteString(w, `{"accessToken":"camel-access","refreshToken":"camel-refresh","expiresIn":120}`)
		case cursorpkg.EndpointOAuthToken:
			require.Equal(t, http.MethodPost, r.Method)
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"refreshToken":"refresh-secret","grant_type":"refresh_token"}`, string(body))
			_, _ = io.WriteString(w, `{"access_token":"snake-access","refresh_token":"snake-refresh","expires_in":240}`)
		case cursorpkg.EndpointAuthPoll:
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "uuid-1", r.URL.Query().Get("uuid"))
			require.Equal(t, "verifier-secret", r.URL.Query().Get("verifier"))
			pollCalls++
			if pollCalls == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"poll-access","refreshToken":"poll-refresh"}`)
		default:
			t.Fatalf("unexpected cursor OAuth path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &cursorOAuthClient{baseURL: server.URL}
	exchanged, err := client.ExchangeUserAPIKey(context.Background(), "crsr_secret", "")
	require.NoError(t, err)
	require.Equal(t, "camel-access", exchanged.AccessToken)
	require.Equal(t, "camel-refresh", exchanged.RefreshToken)
	require.Equal(t, int64(120), exchanged.ExpiresIn)

	refreshed, err := client.RefreshToken(context.Background(), "refresh-secret", "")
	require.NoError(t, err)
	require.Equal(t, "snake-access", refreshed.AccessToken)
	require.Equal(t, "snake-refresh", refreshed.RefreshToken)
	require.Equal(t, int64(240), refreshed.ExpiresIn)

	pending, err := client.PollDeepLink(context.Background(), "uuid-1", "verifier-secret", "")
	require.NoError(t, err)
	require.Nil(t, pending)

	confirmed, err := client.PollDeepLink(context.Background(), "uuid-1", "verifier-secret", "")
	require.NoError(t, err)
	require.Equal(t, "poll-access", confirmed.AccessToken)
}

func TestCursorOAuthClientBoundsAndClosesResponses(t *testing.T) {
	t.Run("oversized success response", func(t *testing.T) {
		closed := false
		body := &cursorTrackingBody{
			Reader: strings.NewReader(strings.Repeat("x", cursorOAuthResponseBodyLimit+1)),
			closed: &closed,
		}
		client := &cursorOAuthClient{
			baseURL: "https://api2.cursor.sh",
			clientFactory: func(string, time.Duration) (*http.Client, error) {
				return &http.Client{Transport: cursorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
				})}, nil
			},
		}

		_, err := client.RefreshToken(context.Background(), "refresh-secret", "")
		require.Error(t, err)
		require.Equal(t, "CURSOR_OAUTH_RESPONSE_TOO_LARGE", infraerrors.Reason(err))
		require.True(t, closed)
	})

	t.Run("nil body", func(t *testing.T) {
		client := &cursorOAuthClient{
			baseURL: "https://api2.cursor.sh",
			clientFactory: func(string, time.Duration) (*http.Client, error) {
				return &http.Client{Transport: cursorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: nil}, nil
				})}, nil
			},
		}

		_, err := client.ExchangeUserAPIKey(context.Background(), "crsr_secret", "")
		require.Error(t, err)
		require.Equal(t, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", infraerrors.Reason(err))
	})
}

type cursorTrackingBody struct {
	io.Reader
	closed *bool
}

func (b *cursorTrackingBody) Close() error {
	*b.closed = true
	return nil
}

func TestCursorOAuthClientErrorsDoNotExposeCredentials(t *testing.T) {
	t.Run("transport error", func(t *testing.T) {
		client := &cursorOAuthClient{
			baseURL: "https://api2.cursor.sh",
			clientFactory: func(string, time.Duration) (*http.Client, error) {
				return &http.Client{Transport: cursorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("dial failed: verifier=CURSOR_TOKEN_CANARY")
				})}, nil
			},
		}

		_, err := client.PollDeepLink(context.Background(), "uuid", "CURSOR_TOKEN_CANARY", "")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "CURSOR_TOKEN_CANARY")
	})

	t.Run("bounded status body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"accessToken":"CURSOR_TOKEN_CANARY","padding":"`+strings.Repeat("x", cursorOAuthErrorBodyLimit*2)+`"}`)
		}))
		defer server.Close()

		client := &cursorOAuthClient{baseURL: server.URL}
		_, err := client.ExchangeUserAPIKey(context.Background(), "crsr_secret", "")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "CURSOR_TOKEN_CANARY")
		require.Less(t, len(err.Error()), cursorOAuthErrorBodyLimit+1024)
	})

	t.Run("invalid proxy fails closed", func(t *testing.T) {
		client := NewCursorOAuthClient()
		_, err := client.ExchangeUserAPIKey(context.Background(), "crsr_secret", "://bad-proxy")
		require.Error(t, err)
		require.Equal(t, "CURSOR_OAUTH_CLIENT_INIT_FAILED", infraerrors.Reason(err))
		require.NotContains(t, err.Error(), "crsr_secret")
	})
}

func TestCursorOAuthWebSessionUsesOfficialHosts(t *testing.T) {
	hosts := make([]string, 0, 2)
	client := &cursorOAuthClient{
		baseURL: "https://custom-forwarding.invalid",
		clientFactory: func(string, time.Duration) (*http.Client, error) {
			return &http.Client{Transport: cursorRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				hosts = append(hosts, req.URL.Host)
				body := `{}`
				if req.URL.Path == cursorpkg.EndpointAuthPoll {
					body = `{"accessToken":"client-token","refreshToken":"refresh-token"}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})}, nil
		},
	}

	info, err := client.ExchangeWebSession(context.Background(), "user_01%3A%3Aweb-token", "")
	require.NoError(t, err)
	require.Equal(t, "client-token", info.AccessToken)
	require.Equal(t, []string{"www.cursor.com", "api2.cursor.sh"}, hosts)
}

func TestCursorDeepLinkPollPendingOnlyCovers404(t *testing.T) {
	require.True(t, cursorDeepLinkPollPending(http.StatusNotFound))
	for _, status := range []int{
		http.StatusOK,
		http.StatusAccepted,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		require.False(t, cursorDeepLinkPollPending(status), "status %d must not be treated as pending", status)
	}
}

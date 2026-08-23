package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	sharedhttp "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const (
	cursorOAuthRequestTimeout    = 60 * time.Second
	cursorOAuthResponseBodyLimit = 1 << 20

	cursorWebSessionRequestTimeout = 30 * time.Second
	cursorWebSessionPollAttempts   = 12
	cursorWebSessionPollInterval   = 500 * time.Millisecond
)

type cursorHTTPClientFactory func(proxyURL string, timeout time.Duration) (*http.Client, error)

// cursorOAuthClient always targets Cursor's official authentication host.
// Account base_url overrides apply only to forwarding traffic.
type cursorOAuthClient struct {
	baseURL       string
	clientFactory cursorHTTPClientFactory
}

func NewCursorOAuthClient() service.CursorOAuthClient {
	return &cursorOAuthClient{baseURL: cursorpkg.DefaultBaseURL}
}

func (c *cursorOAuthClient) endpoint(path string) string {
	return strings.TrimRight(c.baseURL, "/") + path
}

func (c *cursorOAuthClient) httpClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if c != nil && c.clientFactory != nil {
		return c.clientFactory(proxyURL, timeout)
	}
	return sharedhttp.GetClient(sharedhttp.Options{
		ProxyURL:              proxyURL,
		Timeout:               timeout,
		ResponseHeaderTimeout: 20 * time.Second,
	})
}

func (c *cursorOAuthClient) ExchangeUserAPIKey(ctx context.Context, apiKey, proxyURL string) (*cursorpkg.TokenResponse, error) {
	body, err := json.Marshal(map[string]any{"apiKey": apiKey})
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "CURSOR_OAUTH_REQUEST_ENCODE_FAILED", "encode api key exchange request failed")
	}
	return c.requestToken(ctx, http.MethodPost, c.endpoint(cursorpkg.EndpointExchangeUserAPIKey), body, proxyURL,
		"CURSOR_OAUTH_API_KEY_EXCHANGE_FAILED", "api key exchange failed")
}

func (c *cursorOAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*cursorpkg.TokenResponse, error) {
	body, err := json.Marshal(map[string]any{"refreshToken": refreshToken, "grant_type": "refresh_token"})
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "CURSOR_OAUTH_REQUEST_ENCODE_FAILED", "encode token refresh request failed")
	}
	return c.requestToken(ctx, http.MethodPost, c.endpoint(cursorpkg.EndpointOAuthToken), body, proxyURL,
		"CURSOR_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed")
}

func (c *cursorOAuthClient) ExchangeWebSession(ctx context.Context, workosSessionToken, proxyURL string) (*cursorpkg.TokenResponse, error) {
	httpClient, err := c.httpClient(proxyURL, cursorWebSessionRequestTimeout)
	if err != nil || httpClient == nil {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_CLIENT_INIT_FAILED", "create HTTP client failed")
	}
	httpClient = cursorOAuthRedirectSafeClient(httpClient)
	token, err := cursorpkg.ExchangeWebSessionWithOptions(ctx, workosSessionToken, cursorpkg.ExchangeOptions{
		HTTPClient:   httpClient,
		PollAttempts: cursorWebSessionPollAttempts,
		PollInterval: cursorWebSessionPollInterval,
	})
	if err != nil {
		return nil, cursorWebSessionExchangeError(err)
	}
	return token, nil
}

func cursorWebSessionExchangeError(err error) error {
	switch {
	case errors.Is(err, cursorpkg.ErrWebSessionUnauthorized):
		return infraerrors.New(http.StatusUnauthorized, "CURSOR_OAUTH_WEB_SESSION_UNAUTHORIZED", "cursor web session cookie was rejected; re-import WorkosCursorSessionToken")
	case errors.Is(err, cursorpkg.ErrWebSessionNotUpgraded):
		return infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_WEB_SESSION_NOT_UPGRADED", "cursor returned another web token instead of a client token")
	case errors.Is(err, cursorpkg.ErrDeepLoginPending):
		return infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_WEB_SESSION_PENDING", "cursor deep-link login did not complete in time")
	default:
		return infraerrors.Newf(http.StatusBadGateway, "CURSOR_OAUTH_WEB_SESSION_EXCHANGE_FAILED", "cursor web session exchange failed: %s", logredact.RedactText(err.Error()))
	}
}

func (c *cursorOAuthClient) PollDeepLink(ctx context.Context, id, verifier, proxyURL string) (*cursorpkg.TokenResponse, error) {
	query := url.Values{}
	query.Set("uuid", id)
	query.Set("verifier", verifier)
	target := c.endpoint(cursorpkg.EndpointAuthPoll) + "?" + query.Encode()
	body, statusCode, err := c.do(ctx, http.MethodGet, target, nil, proxyURL)
	if err != nil {
		return nil, err
	}
	if cursorDeepLinkPollPending(statusCode) {
		return nil, nil
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, cursorOAuthStatusError("CURSOR_OAUTH_POLL_FAILED", "deep-link poll failed", statusCode, body)
	}
	return decodeCursorOAuthToken(body)
}

func cursorDeepLinkPollPending(statusCode int) bool {
	return statusCode == http.StatusNotFound
}

func (c *cursorOAuthClient) requestToken(
	ctx context.Context,
	method string,
	target string,
	payload []byte,
	proxyURL string,
	statusReason string,
	statusMessage string,
) (*cursorpkg.TokenResponse, error) {
	body, statusCode, err := c.do(ctx, method, target, payload, proxyURL)
	if err != nil {
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, cursorOAuthStatusError(statusReason, statusMessage, statusCode, body)
	}
	return decodeCursorOAuthToken(body)
}

func (c *cursorOAuthClient) do(ctx context.Context, method, target string, payload []byte, proxyURL string) ([]byte, int, error) {
	httpClient, err := c.httpClient(proxyURL, cursorOAuthRequestTimeout)
	if err != nil || httpClient == nil {
		return nil, 0, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_CLIENT_INIT_FAILED", "create HTTP client failed")
	}
	httpClient = cursorOAuthRedirectSafeClient(httpClient)
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_REQUEST_FAILED", "build request failed")
	}
	request.Header.Set("User-Agent", "sub2api-cursor-oauth/1.0")
	request.Header.Set("Accept", "application/json")
	if len(payload) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := httpClient.Do(request)
	if err != nil || response == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, 0, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_REQUEST_FAILED", "request failed")
	}
	if response.Body == nil {
		return nil, response.StatusCode, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor oauth returned an empty response body")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, cursorOAuthResponseBodyLimit+1))
	if err != nil {
		return nil, response.StatusCode, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_RESPONSE_READ_FAILED", "read response failed")
	}
	if len(body) > cursorOAuthResponseBodyLimit {
		return nil, response.StatusCode, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_RESPONSE_TOO_LARGE", "cursor oauth response exceeded the size limit")
	}
	return body, response.StatusCode, nil
}

func cursorOAuthRedirectSafeClient(client *http.Client) *http.Client {
	clone := *client
	previousCheck := client.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || request == nil || request.URL == nil || via[0] == nil || via[0].URL == nil {
			return errors.New("cursor oauth redirect blocked")
		}
		origin := via[0].URL
		if !strings.EqualFold(request.URL.Scheme, origin.Scheme) || !strings.EqualFold(request.URL.Host, origin.Host) {
			return errors.New("cursor oauth cross-origin redirect blocked")
		}
		if previousCheck != nil {
			return previousCheck(request, via)
		}
		if len(via) >= 10 {
			return errors.New("cursor oauth redirect limit exceeded")
		}
		return nil
	}
	return &clone
}

func decodeCursorOAuthToken(body []byte) (*cursorpkg.TokenResponse, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor oauth returned an empty response body")
	}
	var token cursorpkg.TokenResponse
	if json.Unmarshal(body, &token) != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "CURSOR_OAUTH_INVALID_TOKEN_RESPONSE", "cursor oauth returned an invalid token response")
	}
	return &token, nil
}

func cursorOAuthStatusError(code, message string, statusCode int, _ []byte) error {
	httpStatus := http.StatusBadGateway
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		httpStatus = statusCode
	}
	return infraerrors.Newf(httpStatus, code, "%s: status %d", message, statusCode)
}

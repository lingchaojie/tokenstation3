package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroOAuthService_GenerateKiroCLILoginURLAndExchangeCode(t *testing.T) {
	var tokenRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/oauth/token", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Kiro-CLI", r.Header.Get("User-Agent"))
		require.Equal(t, "*/*", r.Header.Get("Accept"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&tokenRequest))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken":  "kiro-access",
			"refreshToken": "kiro-refresh",
			"profileArn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
			"expiresIn":    3600,
		})
	}))
	defer upstream.Close()

	svc := NewKiroOAuthService(nil)
	svc.authEndpoint = upstream.URL

	start, err := svc.GenerateAuthURL(context.Background(), KiroOAuthStartInput{Method: "google"})
	require.NoError(t, err)
	require.Equal(t, "auth_url", start.Mode)
	require.Equal(t, "kiro-cli", start.Method)
	require.NotEmpty(t, start.SessionID)
	require.NotEmpty(t, start.State)
	require.Contains(t, start.AuthURL, "https://app.kiro.dev/signin")
	require.Contains(t, start.AuthURL, "redirect_uri=http%3A%2F%2Flocalhost%3A3128")
	require.Contains(t, start.AuthURL, "redirect_from=kirocli")
	require.Contains(t, start.AuthURL, "code_challenge_method=S256")

	tokenInfo, err := svc.ExchangeCode(context.Background(), &KiroOAuthExchangeCodeInput{
		SessionID: start.SessionID,
		State:     start.State,
		Code:      "callback-code",
	})
	require.NoError(t, err)
	require.Equal(t, "kiro-access", tokenInfo.AccessToken)
	require.Equal(t, "kiro-refresh", tokenInfo.RefreshToken)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/test", tokenInfo.ProfileARN)
	require.Equal(t, "kiro-cli", tokenInfo.AuthMethod)
	require.Equal(t, "Google", tokenInfo.Provider)
	require.NotEmpty(t, tokenInfo.ExpiresAt)
	require.Equal(t, "callback-code", tokenRequest["code"])
	require.Equal(t, "http://localhost:3128/oauth/callback?login_option=google", tokenRequest["redirect_uri"])
	require.NotEmpty(t, tokenRequest["code_verifier"])
}

func TestKiroOAuthService_DeviceCodePollCompletesBuilderIDToken(t *testing.T) {
	tokenPolls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/register":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "Kiro IDE", payload["clientName"])
			require.Contains(t, payload["scopes"], "codewhisperer:taskassist")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clientId":     "client-id",
				"clientSecret": "client-secret",
			})
		case "/device_authorization":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "client-id", payload["clientId"])
			require.Equal(t, "client-secret", payload["clientSecret"])
			require.Equal(t, "https://view.awsapps.com/start", payload["startUrl"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deviceCode":              "device-code",
				"userCode":                "ABCD-EFGH",
				"verificationUri":         "https://device.sso.test/start",
				"verificationUriComplete": "https://device.sso.test/start?user_code=ABCD-EFGH",
				"expiresIn":               600,
				"interval":                1,
			})
		case "/token":
			require.Equal(t, http.MethodPost, r.Method)
			tokenPolls++
			if tokenPolls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "client-id", payload["clientId"])
			require.Equal(t, "client-secret", payload["clientSecret"])
			require.Equal(t, "device-code", payload["deviceCode"])
			require.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", payload["grantType"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accessToken":  "builder-access",
				"refreshToken": "builder-refresh",
				"expiresIn":    3600,
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	svc := NewKiroOAuthService(nil)
	svc.oidcEndpoint = upstream.URL

	start, err := svc.GenerateAuthURL(context.Background(), KiroOAuthStartInput{Method: "builder-id"})
	require.NoError(t, err)
	require.Equal(t, "device_code", start.Mode)
	require.Equal(t, "builder-id", start.Method)
	require.Equal(t, "ABCD-EFGH", start.UserCode)
	require.Equal(t, "https://device.sso.test/start?user_code=ABCD-EFGH", start.VerificationURIComplete)
	require.NotEmpty(t, start.SessionID)

	pending, err := svc.PollDeviceCode(context.Background(), KiroOAuthPollInput{SessionID: start.SessionID})
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Nil(t, pending.TokenInfo)

	complete, err := svc.PollDeviceCode(context.Background(), KiroOAuthPollInput{SessionID: start.SessionID})
	require.NoError(t, err)
	require.Equal(t, "complete", complete.Status)
	require.NotNil(t, complete.TokenInfo)
	require.Equal(t, "builder-access", complete.TokenInfo.AccessToken)
	require.Equal(t, "builder-refresh", complete.TokenInfo.RefreshToken)
	require.Equal(t, "builder-id", complete.TokenInfo.AuthMethod)
	require.Equal(t, "AWS", complete.TokenInfo.Provider)
	require.Equal(t, "client-id", complete.TokenInfo.ClientID)
	require.Equal(t, "client-secret", complete.TokenInfo.ClientSecret)
	require.Empty(t, complete.TokenInfo.ProfileARN)
	require.NotEmpty(t, complete.TokenInfo.ExpiresAt)

	_, err = svc.PollDeviceCode(context.Background(), KiroOAuthPollInput{SessionID: start.SessionID})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "session"))
}

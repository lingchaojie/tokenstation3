//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func TestKiroIDCAuthRedirectURIUsesLoopbackIP(t *testing.T) {
	require.Equal(t, "http://127.0.0.1:9876/oauth/callback", kiroIDCRedirectURI)
}

func TestKiroSocialAuthRedirectURIUsesLoopbackIP(t *testing.T) {
	require.Equal(t, "http://localhost:49153", kiroSocialRedirectURI)
}

func TestKiroExternalIDPAuthRedirectURIUsesOAuthCallback(t *testing.T) {
	require.Equal(t, "http://localhost:49153/oauth/callback", kiroExternalIDPRedirectURI)
}

func TestBuildKiroSocialExchangeRedirectURIUsesProviderDefault(t *testing.T) {
	require.Equal(
		t,
		"http://localhost:49153/oauth/callback?login_option=github",
		buildKiroSocialExchangeRedirectURI("http://localhost:49153", "Github", "", ""),
	)
}

func TestBuildKiroSocialExchangeRedirectURIPreservesParsedCallbackData(t *testing.T) {
	require.Equal(
		t,
		"http://localhost:49153/signin/callback?login_option=google",
		buildKiroSocialExchangeRedirectURI("http://localhost:49153", "Github", "/signin/callback", "google"),
	)
}

func TestKiroOAuthService_ExchangeCodeRejectsExpiredSession(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("expired-session", &kiropkg.AuthSession{
		State:     "expected-state",
		CreatedAt: time.Now().Add(-11 * time.Minute),
	})

	_, err := svc.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: "expired-session",
		State:     "expected-state",
		Code:      "auth-code",
	})
	require.EqualError(t, err, "session not found or expired")
}

func TestKiroOAuthService_StartExternalIDPAuthBuildsMicrosoftURL(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("session-external", &kiropkg.AuthSession{
		State:        "state-external",
		CodeVerifier: "verifier-external",
		CreatedAt:    time.Now(),
		AuthType:     "social",
		Provider:     string(kiropkg.SocialProviderGoogle),
		RedirectURI:  kiroSocialRedirectURI,
	})

	result, err := svc.StartExternalIDPAuth(context.Background(), &KiroStartExternalIDPAuthInput{
		SessionID:   "session-external",
		CallbackURL: "http://localhost:49153/signin/callback?login_option=external_idp&login_hint=phoebe.baral%40mrdev.cyou&issuer_url=https%3A%2F%2Flogin.microsoftonline.com%2F1f44574f-f8aa-40cf-8e43-e6bff9b4298a%2Fv2.0&client_id=e491fadf-0239-44f9-be3b-d3e1ff193c79&state=state-external&scopes=api%3A%2F%2Fe491fadf-0239-44f9-be3b-d3e1ff193c79%2Fcodewhisperer%3Aconversations+offline_access",
	})
	require.NoError(t, err)
	require.Equal(t, "session-external", result.SessionID)
	require.Equal(t, "state-external", result.State)
	require.Contains(t, result.AuthURL, "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/oauth2/v2.0/authorize?")
	require.Contains(t, result.AuthURL, "redirect_uri=http%3A%2F%2Flocalhost%3A49153%2Foauth%2Fcallback")
	require.Contains(t, result.AuthURL, "login_hint=phoebe.baral%40mrdev.cyou")

	session, ok := svc.sessionStore.Get("session-external")
	require.True(t, ok)
	require.Equal(t, "external_idp", session.AuthType)
	require.Equal(t, kiropkg.ProviderExternalIdp, session.Provider)
	require.Equal(t, "http://localhost:49153/oauth/callback", session.RedirectURI)
	require.Equal(t, "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/v2.0", session.IssuerURL)
	require.Equal(t, []string{"api://e491fadf-0239-44f9-be3b-d3e1ff193c79/codewhisperer:conversations", "offline_access"}, session.Scopes)
}

func TestKiroOAuthService_RefreshTokenRejectsMissingRefreshToken(t *testing.T) {
	svc := NewKiroOAuthService(nil)

	_, err := svc.RefreshToken(context.Background(), &KiroRefreshTokenInput{
		AuthMethod: "social",
	})

	require.EqualError(t, err, "kiro refresh token is required")
}

func TestKiroOAuthService_RefreshTokenRejectsIDCMissingClientCredentials(t *testing.T) {
	svc := NewKiroOAuthService(nil)

	_, err := svc.RefreshToken(context.Background(), &KiroRefreshTokenInput{
		AuthMethod:   "idc",
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
	})

	require.EqualError(t, err, "kiro idc refresh requires client_id and client_secret")
}

func TestResolveKiroRefreshAuthMethodInfersIDCFromClientCredentials(t *testing.T) {
	require.Equal(t, "idc", resolveKiroRefreshAuthMethod("", "client-id", "client-secret"))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "client-id", ""))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "", "client-secret"))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "", ""))
	require.Equal(t, "idc", resolveKiroRefreshAuthMethod("IDC", "", ""))
}

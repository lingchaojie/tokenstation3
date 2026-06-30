//go:build unit

package kiro

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestBuildSocialSignInURLUsesAppPortal(t *testing.T) {
	got := BuildSocialSignInURL("http://localhost:49153", "challenge123", "state456")
	want := "https://app.kiro.dev/signin?code_challenge=challenge123&code_challenge_method=S256&redirect_from=KiroIDE&redirect_uri=http%3A%2F%2Flocalhost%3A49153&state=state456"
	if got != want {
		t.Fatalf("BuildSocialSignInURL() = %q, want %q", got, want)
	}
}

func TestBuildSocialTokenRedirectURI(t *testing.T) {
	got := BuildSocialTokenRedirectURI("http://localhost:49153", "/oauth/callback", "github")
	want := "http://localhost:49153/oauth/callback?login_option=github"
	if got != want {
		t.Fatalf("BuildSocialTokenRedirectURI() = %q, want %q", got, want)
	}
}

func TestParseExternalIDPCallbackURL(t *testing.T) {
	callback, err := ParseExternalIDPCallbackURL("http://localhost:49153/signin/callback?login_option=external_idp&login_hint=phoebe.baral%40mrdev.cyou&issuer_url=https%3A%2F%2Flogin.microsoftonline.com%2F1f44574f-f8aa-40cf-8e43-e6bff9b4298a%2Fv2.0&client_id=e491fadf-0239-44f9-be3b-d3e1ff193c79&state=KAGTZuwcvYDltxOfd8yCcw&scopes=api%3A%2F%2Fe491fadf-0239-44f9-be3b-d3e1ff193c79%2Fcodewhisperer%3Aconversations+api%3A%2F%2Fe491fadf-0239-44f9-be3b-d3e1ff193c79%2Fcodewhisperer%3Acompletions+offline_access")
	if err != nil {
		t.Fatalf("ParseExternalIDPCallbackURL() error = %v", err)
	}

	if callback.LoginHint != "phoebe.baral@mrdev.cyou" {
		t.Fatalf("LoginHint = %q", callback.LoginHint)
	}
	if callback.IssuerURL != "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/v2.0" {
		t.Fatalf("IssuerURL = %q", callback.IssuerURL)
	}
	if callback.ClientID != "e491fadf-0239-44f9-be3b-d3e1ff193c79" {
		t.Fatalf("ClientID = %q", callback.ClientID)
	}
	if callback.State != "KAGTZuwcvYDltxOfd8yCcw" {
		t.Fatalf("State = %q", callback.State)
	}
	if len(callback.Scopes) != 3 || callback.Scopes[0] != "api://e491fadf-0239-44f9-be3b-d3e1ff193c79/codewhisperer:conversations" || callback.Scopes[2] != "offline_access" {
		t.Fatalf("Scopes = %#v", callback.Scopes)
	}
}

func TestBuildExternalIDPAuthURLUsesOAuthCallbackRedirect(t *testing.T) {
	got, err := BuildExternalIDPAuthURL(ExternalIDPAuthURLInput{
		IssuerURL:           "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/v2.0",
		ClientID:            "e491fadf-0239-44f9-be3b-d3e1ff193c79",
		Scopes:              []string{"api://e491fadf-0239-44f9-be3b-d3e1ff193c79/codewhisperer:conversations", "offline_access"},
		RedirectURI:         "http://localhost:49153/oauth/callback",
		State:               "state-1",
		CodeChallenge:       "challenge-1",
		CodeChallengeMethod: "S256",
		LoginHint:           "phoebe.baral@mrdev.cyou",
	})
	if err != nil {
		t.Fatalf("BuildExternalIDPAuthURL() error = %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("Parse auth URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "login.microsoftonline.com" || parsed.Path != "/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/oauth2/v2.0/authorize" {
		t.Fatalf("auth endpoint = %s://%s%s", parsed.Scheme, parsed.Host, parsed.Path)
	}
	params := parsed.Query()
	if params.Get("redirect_uri") != "http://localhost:49153/oauth/callback" {
		t.Fatalf("redirect_uri = %q", params.Get("redirect_uri"))
	}
	if params.Get("scope") != "api://e491fadf-0239-44f9-be3b-d3e1ff193c79/codewhisperer:conversations offline_access" {
		t.Fatalf("scope = %q", params.Get("scope"))
	}
	if params.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", params.Get("code_challenge_method"))
	}
	if params.Get("prompt") != "login" {
		t.Fatalf("prompt = %q", params.Get("prompt"))
	}
}

func TestExchangeExternalIDPAuthCodePostsMicrosoftTokenForm(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-token","refresh_token":"refresh-token","expires_in":7200,"scope":"scope-a offline_access"}`))
	}))
	defer server.Close()

	oldEndpoint := externalIDPTokenEndpointOverride
	externalIDPTokenEndpointOverride = server.URL
	defer func() { externalIDPTokenEndpointOverride = oldEndpoint }()

	token, err := ExchangeExternalIDPAuthCode(
		context.Background(),
		"",
		"https://login.microsoftonline.com/tenant-id/v2.0",
		"client-id",
		[]string{"scope-a", "offline_access"},
		"auth-code",
		"code-verifier",
		"http://localhost:49153/oauth/callback",
		"user@example.com",
	)
	if err != nil {
		t.Fatalf("ExchangeExternalIDPAuthCode() error = %v", err)
	}

	if gotForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") != "client-id" {
		t.Fatalf("client_id = %q", gotForm.Get("client_id"))
	}
	if gotForm.Get("code") != "auth-code" {
		t.Fatalf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") != "code-verifier" {
		t.Fatalf("code_verifier = %q", gotForm.Get("code_verifier"))
	}
	if gotForm.Get("redirect_uri") != "http://localhost:49153/oauth/callback" {
		t.Fatalf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
	if token.AuthMethod != "external_idp" || token.Provider != "Internal" {
		t.Fatalf("token auth = %q/%q", token.AuthMethod, token.Provider)
	}
	if token.AccessToken != "access-token" || token.RefreshToken != "refresh-token" {
		t.Fatalf("token = %#v", token)
	}
	if token.IssuerURL != "https://login.microsoftonline.com/tenant-id/v2.0" || token.Scopes != "scope-a offline_access" {
		t.Fatalf("metadata = %q %q", token.IssuerURL, token.Scopes)
	}
}

func TestSessionStoreGetDeletesExpiredSession(t *testing.T) {
	store := NewSessionStore()
	store.Set("expired", &AuthSession{CreatedAt: time.Now().Add(-2 * sessionTTL)})

	session, ok := store.Get("expired")
	if ok || session != nil {
		t.Fatalf("Get(expired) = (%v, %v), want (nil, false)", session, ok)
	}
	if _, exists := store.data["expired"]; exists {
		t.Fatalf("expired session should be deleted from the store")
	}
}

func TestSessionStoreSetPrunesExpiredSessions(t *testing.T) {
	store := NewSessionStore()
	now := time.Now()
	for i := 0; i < sessionCleanupMin; i++ {
		store.data[fmt.Sprintf("expired-%d", i)] = &AuthSession{CreatedAt: now.Add(-2 * sessionTTL)}
	}
	store.setCount = sessionCleanupEvery - 1

	store.Set("fresh", &AuthSession{CreatedAt: now})

	if len(store.data) != 1 {
		t.Fatalf("store size = %d, want 1", len(store.data))
	}
	if _, ok := store.data["fresh"]; !ok {
		t.Fatalf("fresh session should remain after pruning")
	}
}

func TestParseImportedTokenInfersIDCAuthMetadataFromClientCredentials(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"clientId": "client-id",
		"clientSecret": "client-secret"
	}`, "")
	if err != nil {
		t.Fatalf("ParseImportedToken() error = %v", err)
	}

	if token.AuthMethod != "idc" {
		t.Fatalf("AuthMethod = %q, want idc", token.AuthMethod)
	}
	if token.Provider != "AWS" {
		t.Fatalf("Provider = %q, want AWS", token.Provider)
	}
	if token.Region != defaultIDCRegion {
		t.Fatalf("Region = %q, want %q", token.Region, defaultIDCRegion)
	}
}

func TestParseImportedTokenInfersIDCAuthMetadataFromDeviceRegistration(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"clientIdHash": "client-id-hash"
	}`, `{
		"clientId": "client-id",
		"clientSecret": "client-secret"
	}`)
	if err != nil {
		t.Fatalf("ParseImportedToken() error = %v", err)
	}

	if token.ClientID != "client-id" {
		t.Fatalf("ClientID = %q, want client-id", token.ClientID)
	}
	if token.ClientSecret != "client-secret" {
		t.Fatalf("ClientSecret = %q, want client-secret", token.ClientSecret)
	}
	if token.AuthMethod != "idc" {
		t.Fatalf("AuthMethod = %q, want idc", token.AuthMethod)
	}
}

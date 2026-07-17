//go:build unit

package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func validateExternalIdpEndpoint(rawURL string) error {
	_, err := parseExternalIdpURL(rawURL)
	return err
}

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
		AuthorizationEndpoint: "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/oauth2/v2.0/authorize",
		IssuerURL:             "https://login.microsoftonline.com/1f44574f-f8aa-40cf-8e43-e6bff9b4298a/v2.0",
		ClientID:              "e491fadf-0239-44f9-be3b-d3e1ff193c79",
		Scopes:                []string{"api://e491fadf-0239-44f9-be3b-d3e1ff193c79/codewhisperer:conversations", "offline_access"},
		RedirectURI:           "http://localhost:3128/oauth/callback",
		State:                 "state-1",
		CodeChallenge:         "challenge-1",
		CodeChallengeMethod:   "S256",
		LoginHint:             "phoebe.baral@mrdev.cyou",
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
	if params.Get("redirect_uri") != "http://localhost:3128/oauth/callback" {
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

func TestValidateExternalIdpEndpointAcceptsMicrosoftOnlineSuffixes(t *testing.T) {
	tests := []string{
		"https://login.microsoftonline.com/tenant/v2.0",
		"https://login.microsoftonline.us/tenant/v2.0",
		"https://login.partner.microsoftonline.cn/tenant/v2.0",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if err := validateExternalIdpEndpoint(rawURL); err != nil {
				t.Fatalf("validateExternalIdpEndpoint(%q) error = %v", rawURL, err)
			}
		})
	}
}

func TestValidateExternalIdpEndpointRejectsUnsafeURLs(t *testing.T) {
	tests := []string{
		"http://login.microsoftonline.com/tenant/v2.0",
		"https://127.0.0.1/tenant/v2.0",
		"https://[::1]/tenant/v2.0",
		"https://microsoftonline.com.evil.example/tenant/v2.0",
		"https://login.example.com/tenant/v2.0",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if err := validateExternalIdpEndpoint(rawURL); err == nil {
				t.Fatalf("validateExternalIdpEndpoint(%q) error = nil, want rejection", rawURL)
			}
		})
	}
}

func TestValidateExternalIdpRoleURLs(t *testing.T) {
	valid := []struct {
		name string
		url  string
		fn   func(string) error
	}{
		{name: "public issuer", url: "https://login.microsoftonline.com/tenant-id/v2.0", fn: validateExternalIdpIssuerURL},
		{name: "us discovery", url: "https://login.microsoftonline.us/tenant-id/v2.0/.well-known/openid-configuration", fn: validateExternalIdpDiscoveryURL},
		{name: "china authorize", url: "https://login.partner.microsoftonline.cn/tenant-id/oauth2/v2.0/authorize", fn: validateExternalIdpAuthorizationEndpoint},
		{name: "public token", url: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token", fn: validateExternalIdpTokenEndpoint},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(tt.url); err != nil {
				t.Fatalf("validate %q: %v", tt.url, err)
			}
		})
	}

	invalidCommon := []string{
		"https://user:password@login.microsoftonline.com/tenant-id/v2.0",
		"https://login.microsoftonline.com:8443/tenant-id/v2.0",
		"https://login.microsoftonline.com/tenant-id/v2.0#fragment",
	}
	for _, rawURL := range invalidCommon {
		t.Run("unsafe issuer "+rawURL, func(t *testing.T) {
			if err := validateExternalIdpIssuerURL(rawURL); err == nil {
				t.Fatalf("validateExternalIdpIssuerURL(%q) error = nil", rawURL)
			}
		})
	}

	invalidRoles := []struct {
		name string
		url  string
		fn   func(string) error
	}{
		{name: "issuer query", url: "https://login.microsoftonline.com/tenant-id/v2.0?next=value", fn: validateExternalIdpIssuerURL},
		{name: "issuer missing version", url: "https://login.microsoftonline.com/tenant-id", fn: validateExternalIdpIssuerURL},
		{name: "issuer arbitrary path", url: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0", fn: validateExternalIdpIssuerURL},
		{name: "discovery query", url: "https://login.microsoftonline.com/tenant-id/v2.0/.well-known/openid-configuration?next=value", fn: validateExternalIdpDiscoveryURL},
		{name: "discovery arbitrary path", url: "https://login.microsoftonline.com/tenant-id/.well-known/openid-configuration", fn: validateExternalIdpDiscoveryURL},
		{name: "authorize swapped with token", url: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token", fn: validateExternalIdpAuthorizationEndpoint},
		{name: "authorize arbitrary path", url: "https://login.microsoftonline.com/tenant-id/authorize", fn: validateExternalIdpAuthorizationEndpoint},
		{name: "token swapped with authorize", url: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/authorize", fn: validateExternalIdpTokenEndpoint},
		{name: "token arbitrary path", url: "https://login.microsoftonline.com/not-a-token-endpoint", fn: validateExternalIdpTokenEndpoint},
		{name: "token query", url: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token?next=value", fn: validateExternalIdpTokenEndpoint},
	}
	for _, tt := range invalidRoles {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(tt.url); err == nil {
				t.Fatalf("validate %q error = nil, want role rejection", tt.url)
			}
		})
	}
}

func TestValidateExternalIdpDiscoveryRequiresIssuerHostAndTenant(t *testing.T) {
	issuer := "https://login.microsoftonline.com/tenant-id/v2.0"
	for _, discovery := range []externalIdpDiscoveryResponse{
		{
			AuthorizationEndpoint: "https://login.microsoftonline.us/tenant-id/oauth2/v2.0/authorize",
			TokenEndpoint:         "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token",
		},
		{
			AuthorizationEndpoint: "https://login.microsoftonline.com/other-tenant/oauth2/v2.0/authorize",
			TokenEndpoint:         "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token",
		},
		{
			AuthorizationEndpoint: "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/authorize",
			TokenEndpoint:         "https://login.microsoftonline.com/other-tenant/oauth2/v2.0/token",
		},
	} {
		discovery := discovery
		if err := validateExternalIdpDiscoveryForIssuer(issuer, &discovery); err == nil {
			t.Fatalf("validateExternalIdpDiscoveryForIssuer(%#v) error = nil, want mismatch rejection", discovery)
		}
	}
}

type externalIdpRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn externalIdpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestDiscoverExternalIdpEnforcesRedirectLimit(t *testing.T) {
	previousFactory := newExternalIdpHTTPClient
	defer func() { newExternalIdpHTTPClient = previousFactory }()

	roundTrips := 0
	newExternalIdpHTTPClient = func(proxyURL string) (*http.Client, error) {
		if proxyURL != "http://proxy.example:8080" {
			t.Fatalf("proxyURL = %q", proxyURL)
		}
		return &http.Client{
			Timeout: 30 * time.Second,
			Transport: externalIdpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				roundTrips++
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://login.microsoftonline.com/tenant-id/v2.0/.well-known/openid-configuration"},
					},
					Body:    io.NopCloser(strings.NewReader("redirect")),
					Request: req,
				}, nil
			}),
		}, nil
	}

	_, err := DiscoverExternalIdp(
		context.Background(),
		"http://proxy.example:8080",
		"https://login.microsoftonline.com/tenant-id/v2.0",
	)
	if err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("DiscoverExternalIdp() error = %v, want redirect limit", err)
	}
	if roundTrips != externalIdpMaxDiscoveryRedirects+1 {
		t.Fatalf("roundTrips = %d, want %d", roundTrips, externalIdpMaxDiscoveryRedirects+1)
	}
}

func TestExternalIdpResponseBodiesAreBounded(t *testing.T) {
	t.Run("discovery exact limit", func(t *testing.T) {
		payload := `{"authorization_endpoint":"https://login.microsoftonline.com/tenant-id/oauth2/v2.0/authorize","token_endpoint":"https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token"}`
		payload += strings.Repeat(" ", externalIdpDiscoveryBodyLimit-len(payload))
		withExternalIdpRoundTripper(t, externalIdpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		}))
		_, err := DiscoverExternalIdp(context.Background(), "", "https://login.microsoftonline.com/tenant-id/v2.0")
		if err != nil {
			t.Fatalf("DiscoverExternalIdp() exact-limit error = %v", err)
		}
	})

	t.Run("discovery overflow", func(t *testing.T) {
		withExternalIdpRoundTripper(t, externalIdpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", externalIdpDiscoveryBodyLimit+1))), Request: req}, nil
		}))
		_, err := DiscoverExternalIdp(context.Background(), "", "https://login.microsoftonline.com/tenant-id/v2.0")
		if err == nil || !strings.Contains(err.Error(), "body exceeds") {
			t.Fatalf("DiscoverExternalIdp() overflow error = %v", err)
		}
	})

	for _, tc := range []struct {
		name       string
		statusCode int
		body       func() string
	}{
		{
			name:       "token exact limit",
			statusCode: http.StatusOK,
			body: func() string {
				payload := `{"access_token":"access-token","refresh_token":"refresh-token"}`
				return payload + strings.Repeat(" ", externalIdpTokenBodyLimit-len(payload))
			},
		},
		{
			name:       "token overflow",
			statusCode: http.StatusOK,
			body:       func() string { return strings.Repeat("x", externalIdpTokenBodyLimit+1) },
		},
		{
			name:       "token error overflow",
			statusCode: http.StatusBadRequest,
			body:       func() string { return strings.Repeat("x", externalIdpTokenBodyLimit+1) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body()))
			}))
			defer server.Close()
			previousEndpoint := externalIDPTokenEndpointOverride
			externalIDPTokenEndpointOverride = server.URL
			defer func() { externalIDPTokenEndpointOverride = previousEndpoint }()

			_, err := RefreshExternalIDPTokenAtEndpoint(
				context.Background(),
				"",
				"https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token",
				"https://login.microsoftonline.com/tenant-id/v2.0",
				"client-id",
				[]string{"openid", "offline_access"},
				"refresh-token",
				"",
			)
			if strings.Contains(tc.name, "overflow") {
				if err == nil || !strings.Contains(err.Error(), "body exceeds") {
					t.Fatalf("RefreshExternalIDPTokenAtEndpoint() overflow error = %v", err)
				}
			} else if err != nil {
				t.Fatalf("RefreshExternalIDPTokenAtEndpoint() exact-limit error = %v", err)
			}
		})
	}
}

func withExternalIdpRoundTripper(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	previousFactory := newExternalIdpHTTPClient
	newExternalIdpHTTPClient = func(string) (*http.Client, error) {
		return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
	}
	t.Cleanup(func() { newExternalIdpHTTPClient = previousFactory })
}

func TestDiscoverExternalIdpRejectsUnsafeIssuerBeforeRequest(t *testing.T) {
	_, err := DiscoverExternalIdp(context.Background(), "", "https://login.example.com/tenant/v2.0")
	if err == nil || !strings.Contains(err.Error(), "allow-listed") {
		t.Fatalf("DiscoverExternalIdp() error = %v, want allow-list rejection", err)
	}
}

func TestDiscoverExternalIdpRejectsEndpointsOutsideAllowlist(t *testing.T) {
	discovery := externalIdpDiscoveryResponse{
		AuthorizationEndpoint: "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
		TokenEndpoint:         "https://token.evil.example/oauth2/v2.0/token",
	}
	if err := validateExternalIdpDiscovery(&discovery); err == nil {
		t.Fatal("validateExternalIdpDiscovery() error = nil, want token endpoint rejection")
	}

	discovery.AuthorizationEndpoint = "https://authorize.evil.example/oauth2/v2.0/authorize"
	discovery.TokenEndpoint = "https://login.microsoftonline.com/tenant/oauth2/v2.0/token"
	if err := validateExternalIdpDiscovery(&discovery); err == nil {
		t.Fatal("validateExternalIdpDiscovery() error = nil, want authorization endpoint rejection")
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
		"http://localhost:3128/oauth/callback",
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
	if gotForm.Get("redirect_uri") != "http://localhost:3128/oauth/callback" {
		t.Fatalf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
	if token.AuthMethod != "external_idp" || token.Provider != ProviderExternalIdp {
		t.Fatalf("token auth = %q/%q", token.AuthMethod, token.Provider)
	}
	if token.AccessToken != "access-token" || token.RefreshToken != "refresh-token" {
		t.Fatalf("token = %#v", token)
	}
	if token.IssuerURL != "https://login.microsoftonline.com/tenant-id/v2.0" || token.Scopes != "scope-a offline_access" {
		t.Fatalf("metadata = %q %q", token.IssuerURL, token.Scopes)
	}
	if token.TokenEndpoint != "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token" {
		t.Fatalf("TokenEndpoint = %q", token.TokenEndpoint)
	}
}

func TestExternalIDPTokenRequestsRejectRedirectsWithoutLeakingForms(t *testing.T) {
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{
			name: "authorization code exchange",
			run: func(ctx context.Context) error {
				_, err := ExchangeExternalIDPAuthCodeAtEndpoint(
					ctx,
					"",
					"https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
					"https://login.microsoftonline.com/tenant/v2.0",
					"client-id",
					[]string{"openid", "offline_access"},
					"secret-auth-code",
					"secret-code-verifier",
					"http://localhost:3128/oauth/callback",
					"user@example.com",
				)
				return err
			},
		},
		{
			name: "refresh token",
			run: func(ctx context.Context) error {
				_, err := RefreshExternalIDPTokenAtEndpoint(
					ctx,
					"",
					"https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
					"https://login.microsoftonline.com/tenant/v2.0",
					"client-id",
					[]string{"openid", "offline_access"},
					"secret-refresh-token",
					"user@example.com",
				)
				return err
			},
		},
	}

	for _, operation := range operations {
		for _, status := range []int{
			http.StatusMovedPermanently,
			http.StatusFound,
			http.StatusSeeOther,
			http.StatusTemporaryRedirect,
			http.StatusPermanentRedirect,
		} {
			t.Run(fmt.Sprintf("%s/%d", operation.name, status), func(t *testing.T) {
				var targetCalls int
				var leakedBody string
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					targetCalls++
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatalf("read redirected request body: %v", err)
					}
					leakedBody = string(body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"access_token":"redirected-access-token","refresh_token":"redirected-refresh-token"}`))
				}))
				defer target.Close()

				redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", target.URL+"/stolen")
					w.WriteHeader(status)
				}))
				defer redirector.Close()

				previousEndpoint := externalIDPTokenEndpointOverride
				externalIDPTokenEndpointOverride = redirector.URL
				defer func() { externalIDPTokenEndpointOverride = previousEndpoint }()

				err := operation.run(context.Background())
				if err == nil {
					t.Fatal("token request error = nil, want redirect rejection")
				}
				if targetCalls != 0 {
					t.Fatalf("redirect target calls = %d, want 0; leaked body = %q", targetCalls, leakedBody)
				}
			})
		}
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
		"provider": "BuilderId",
		"clientId": "client-id",
		"clientSecret": "client-secret"
	}`, "")
	if err != nil {
		t.Fatalf("ParseImportedToken() error = %v", err)
	}

	if token.AuthMethod != "idc" {
		t.Fatalf("AuthMethod = %q, want idc", token.AuthMethod)
	}
	if token.Provider != ProviderBuilderId {
		t.Fatalf("Provider = %q, want %q", token.Provider, ProviderBuilderId)
	}
	if token.Region != defaultIDCRegion {
		t.Fatalf("Region = %q, want %q", token.Region, defaultIDCRegion)
	}
}

func TestParseImportedTokenInfersIDCAuthMetadataFromDeviceRegistration(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"provider": "Enterprise",
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

func TestParseImportedTokenRejectsMissingOrInvalidProvider(t *testing.T) {
	tests := []struct {
		name      string
		tokenJSON string
	}{
		{
			name:      "missing",
			tokenJSON: `{"accessToken":"access-token","authMethod":"social"}`,
		},
		{
			name:      "blank",
			tokenJSON: `{"accessToken":"access-token","authMethod":"social","provider":"  "}`,
		},
		{
			name:      "legacy AWS",
			tokenJSON: `{"accessToken":"access-token","authMethod":"idc","provider":"AWS","clientId":"client-id","clientSecret":"client-secret"}`,
		},
		{
			name:      "legacy Internal",
			tokenJSON: `{"accessToken":"access-token","authMethod":"external_idp","provider":"Internal","refreshToken":"refresh-token","clientId":"client-id","issuerUrl":"https://login.microsoftonline.com/tenant/v2.0","scopes":"openid offline_access"}`,
		},
		{
			name:      "unknown",
			tokenJSON: `{"accessToken":"access-token","authMethod":"social","provider":"GitLab"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseImportedToken(tt.tokenJSON, ""); err == nil {
				t.Fatal("ParseImportedToken() error = nil, want provider validation error")
			}
		})
	}
}

func TestParseImportedTokenAcceptsCanonicalProviders(t *testing.T) {
	tests := []struct {
		provider   string
		tokenJSON  string
		authMethod string
	}{
		{
			provider:   ProviderGoogle,
			tokenJSON:  `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"social","provider":"Google"}`,
			authMethod: "social",
		},
		{
			provider:   ProviderGithub,
			tokenJSON:  `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"social","provider":"Github"}`,
			authMethod: "social",
		},
		{
			provider:   ProviderBuilderId,
			tokenJSON:  `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"idc","provider":"BuilderId","clientId":"client-id","clientSecret":"client-secret"}`,
			authMethod: "idc",
		},
		{
			provider:   ProviderEnterprise,
			tokenJSON:  `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"idc","provider":"Enterprise","clientId":"client-id","clientSecret":"client-secret"}`,
			authMethod: "idc",
		},
		{
			provider:   ProviderExternalIdp,
			tokenJSON:  `{"accessToken":"access-token","refreshToken":"refresh-token","provider":"ExternalIdp","clientId":"client-id","tokenEndpoint":"https://login.microsoftonline.com/tenant/oauth2/v2.0/token","issuerUrl":"https://login.microsoftonline.com/tenant/v2.0","scopes":"openid offline_access"}`,
			authMethod: "external_idp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			token, err := ParseImportedToken(tt.tokenJSON, "")
			if err != nil {
				t.Fatalf("ParseImportedToken() error = %v", err)
			}
			if token.Provider != tt.provider {
				t.Fatalf("Provider = %q, want %q", token.Provider, tt.provider)
			}
			if token.AuthMethod != tt.authMethod {
				t.Fatalf("AuthMethod = %q, want %q", token.AuthMethod, tt.authMethod)
			}
		})
	}
}

func TestParseImportedTokenRejectsExternalIdpWithoutRefreshMetadata(t *testing.T) {
	_, err := ParseImportedToken(`{"accessToken":"access-token","provider":"ExternalIdp"}`, "")
	if err == nil {
		t.Fatal("ParseImportedToken() error = nil, want external_idp refresh metadata error")
	}
}

func TestParseImportedTokenRejectsExternalIdpAuthMethodMismatch(t *testing.T) {
	tests := []struct {
		name      string
		tokenJSON string
	}{
		{
			name:      "ExternalIdp provider with social auth",
			tokenJSON: `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"social","provider":"ExternalIdp","clientId":"client-id","issuerUrl":"https://login.microsoftonline.com/tenant/v2.0","scopes":"openid offline_access"}`,
		},
		{
			name:      "external_idp auth with Google provider",
			tokenJSON: `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"external_idp","provider":"Google","clientId":"client-id","issuerUrl":"https://login.microsoftonline.com/tenant/v2.0","scopes":"openid offline_access"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseImportedToken(tt.tokenJSON, ""); err == nil {
				t.Fatal("ParseImportedToken() error = nil, want provider/authMethod mismatch error")
			}
		})
	}
}

func TestParseImportedTokenNormalizesExpiresAt(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		want      time.Time
	}{
		{
			name:      "RFC3339 UTC",
			expiresAt: "2026-06-29T09:33:49Z",
			want:      time.Date(2026, time.June, 29, 9, 33, 49, 0, time.UTC),
		},
		{
			name:      "RFC3339Nano UTC",
			expiresAt: "2026-06-29T09:33:49.114Z",
			want:      time.Date(2026, time.June, 29, 9, 33, 49, 0, time.UTC),
		},
		{
			name:      "RFC3339 offset",
			expiresAt: "2026-06-29T16:56:19+08:00",
			want:      time.Date(2026, time.June, 29, 8, 56, 19, 0, time.UTC),
		},
		{
			name:      "naive seconds as UTC",
			expiresAt: "2026-09-27T08:46:31",
			want:      time.Date(2026, time.September, 27, 8, 46, 31, 0, time.UTC),
		},
		{
			name:      "naive fractional seconds as UTC",
			expiresAt: "2026-09-27T08:46:31.070",
			want:      time.Date(2026, time.September, 27, 8, 46, 31, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := ParseImportedToken(`{
				"accessToken":"access-token",
				"authMethod":"social",
				"provider":"Google",
				"expiresAt":"`+tt.expiresAt+`"
			}`, "")
			if err != nil {
				t.Fatalf("ParseImportedToken() error = %v", err)
			}
			parsed, err := time.Parse(time.RFC3339, token.ExpiresAt)
			if err != nil {
				t.Fatalf("ExpiresAt = %q, want RFC3339: %v", token.ExpiresAt, err)
			}
			if !parsed.Equal(tt.want) {
				t.Fatalf("ExpiresAt instant = %s, want %s", parsed, tt.want)
			}
			if token.ExpiresAt != tt.want.Local().Format(time.RFC3339) {
				t.Fatalf("ExpiresAt = %q, want local RFC3339 %q", token.ExpiresAt, tt.want.Local().Format(time.RFC3339))
			}
		})
	}
}

func TestParseImportedTokenRejectsInvalidExpiresAt(t *testing.T) {
	_, err := ParseImportedToken(`{
		"accessToken":"access-token",
		"authMethod":"social",
		"provider":"Google",
		"expiresAt":"not-a-time"
	}`, "")
	if err == nil {
		t.Fatal("ParseImportedToken() error = nil, want expiresAt validation error")
	}
}

func TestParseImportedTokenValidatesExternalIdpRefreshFields(t *testing.T) {
	valid := map[string]string{
		"refreshToken":  "refresh-token",
		"clientId":      "client-id",
		"tokenEndpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
		"issuerUrl":     "https://login.microsoftonline.com/tenant/v2.0",
		"scopes":        "openid offline_access",
	}
	for _, missing := range []string{"refreshToken", "clientId", "tokenEndpoint", "issuerUrl", "scopes"} {
		t.Run("missing "+missing, func(t *testing.T) {
			fields := make(map[string]string, len(valid))
			for key, value := range valid {
				fields[key] = value
			}
			delete(fields, missing)
			raw, err := json.Marshal(map[string]string{
				"accessToken":   "access-token",
				"authMethod":    "external_idp",
				"provider":      ProviderExternalIdp,
				"refreshToken":  fields["refreshToken"],
				"clientId":      fields["clientId"],
				"tokenEndpoint": fields["tokenEndpoint"],
				"issuerUrl":     fields["issuerUrl"],
				"scopes":        fields["scopes"],
			})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if _, err := ParseImportedToken(string(raw), ""); err == nil {
				t.Fatalf("ParseImportedToken() error = nil, want missing %s error", missing)
			}
		})
	}

	token, err := ParseImportedToken(`{
		"accessToken":"access-token",
		"refreshToken":" refresh-token ",
		"authMethod":"external_idp",
		"provider":"ExternalIdp",
		"clientId":" client-id ",
		"tokenEndpoint":" https://login.microsoftonline.com/tenant/oauth2/v2.0/token ",
		"issuerUrl":" https://login.microsoftonline.com/tenant/v2.0 ",
		"scopes":" openid offline_access "
	}`, "")
	if err != nil {
		t.Fatalf("ParseImportedToken() error = %v", err)
	}
	if token.Provider != ProviderExternalIdp || token.RefreshToken != "refresh-token" || token.ClientID != "client-id" || token.TokenEndpoint != "https://login.microsoftonline.com/tenant/oauth2/v2.0/token" || token.IssuerURL != "https://login.microsoftonline.com/tenant/v2.0" || token.Scopes != "openid offline_access" {
		t.Fatalf("external_idp metadata = %#v", token)
	}
}

func TestResolveIDCProvider(t *testing.T) {
	tests := []struct {
		name     string
		startURL string
		want     string
	}{
		{name: "empty", want: ProviderBuilderId},
		{name: "Builder ID", startURL: BuilderIDStartURL, want: ProviderBuilderId},
		{name: "trimmed Builder ID", startURL: "  " + BuilderIDStartURL + "  ", want: ProviderBuilderId},
		{name: "enterprise", startURL: "https://d-1234567890.awsapps.com/start", want: ProviderEnterprise},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveIDCProvider(tt.startURL); got != tt.want {
				t.Fatalf("resolveIDCProvider(%q) = %q, want %q", tt.startURL, got, tt.want)
			}
		})
	}
}

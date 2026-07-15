package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func newOAuthDomainTestContext(host string, forwardedProto string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/auth/oauth/oidc/start", nil)
	req.Host = host
	if forwardedProto != "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	c.Request = req
	return c, w
}

func TestResolveOAuthProviderCallbackURLUsesAllowedRequestHost(t *testing.T) {
	c, _ := newOAuthDomainTestContext("english.example.com", "https")
	h := &AuthHandler{cfg: &config.Config{
		Security: config.SecurityConfig{
			OAuthRedirect: config.OAuthRedirectConfig{
				AllowedHosts: []string{"www.linx2.ai", "english.example.com"},
			},
		},
	}}

	got, err := h.resolveOAuthProviderCallbackURL(
		c,
		"https://www.linx2.ai/api/v1/auth/oauth/oidc/callback",
		"/api/v1/auth/oauth/oidc/callback",
	)
	if err != nil {
		t.Fatalf("resolveOAuthProviderCallbackURL() error = %v", err)
	}
	want := "https://english.example.com/api/v1/auth/oauth/oidc/callback"
	if got != want {
		t.Fatalf("resolveOAuthProviderCallbackURL() = %q, want %q", got, want)
	}
}

func TestResolveOAuthProviderCallbackURLFallsBackWhenHostNotAllowed(t *testing.T) {
	c, _ := newOAuthDomainTestContext("evil.example.com", "https")
	h := &AuthHandler{cfg: &config.Config{
		Security: config.SecurityConfig{
			OAuthRedirect: config.OAuthRedirectConfig{
				AllowedHosts: []string{"www.linx2.ai", "english.example.com"},
			},
		},
	}}

	got, err := h.resolveOAuthProviderCallbackURL(
		c,
		"https://www.linx2.ai/api/v1/auth/oauth/oidc/callback",
		"/api/v1/auth/oauth/oidc/callback",
	)
	if err != nil {
		t.Fatalf("resolveOAuthProviderCallbackURL() error = %v", err)
	}
	want := "https://www.linx2.ai/api/v1/auth/oauth/oidc/callback"
	if got != want {
		t.Fatalf("resolveOAuthProviderCallbackURL() = %q, want %q", got, want)
	}
}

func TestResolveOAuthProviderCallbackURLAllowsConfiguredHostWithoutExplicitAllowlist(t *testing.T) {
	c, _ := newOAuthDomainTestContext("www.linx2.ai", "https")
	h := &AuthHandler{cfg: &config.Config{}}

	got, err := h.resolveOAuthProviderCallbackURL(
		c,
		"https://www.linx2.ai/custom/oauth/linuxdo/callback",
		"/api/v1/auth/oauth/linuxdo/callback",
	)
	if err != nil {
		t.Fatalf("resolveOAuthProviderCallbackURL() error = %v", err)
	}
	want := "https://www.linx2.ai/custom/oauth/linuxdo/callback"
	if got != want {
		t.Fatalf("resolveOAuthProviderCallbackURL() = %q, want %q", got, want)
	}
}

func TestResolveOAuthFrontendCallbackRebasesAbsoluteURLForAllowedHost(t *testing.T) {
	c, _ := newOAuthDomainTestContext("english.example.com", "https")
	h := &AuthHandler{cfg: &config.Config{
		Security: config.SecurityConfig{
			OAuthRedirect: config.OAuthRedirectConfig{
				AllowedHosts: []string{"www.linx2.ai", "english.example.com"},
			},
		},
	}}

	got := h.resolveOAuthFrontendCallbackURL(c, "https://www.linx2.ai/auth/oidc/callback?source=oauth", "/auth/oidc/callback")
	want := "https://english.example.com/auth/oidc/callback?source=oauth"
	if got != want {
		t.Fatalf("resolveOAuthFrontendCallbackURL() = %q, want %q", got, want)
	}
}

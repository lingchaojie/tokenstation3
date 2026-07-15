package handler

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *AuthHandler) resolveOAuthProviderCallbackURL(c *gin.Context, configuredURL string, callbackPath string) (string, error) {
	configuredURL = strings.TrimSpace(configuredURL)
	callbackPath = normalizeOAuthCallbackPath(callbackPath)

	if host := requestOAuthHost(c); host != "" && h.isOAuthRedirectHostAllowed(host) {
		scheme := "http"
		if isRequestHTTPS(c) {
			scheme = "https"
		}
		return scheme + "://" + host + callbackPath, nil
	}

	if configuredURL != "" {
		return configuredURL, nil
	}
	return "", fmt.Errorf("oauth redirect url not configured")
}

func (h *AuthHandler) resolveOAuthFrontendCallbackURL(c *gin.Context, configuredURL string, defaultPath string) string {
	configuredURL = strings.TrimSpace(configuredURL)
	if configuredURL == "" {
		configuredURL = normalizeOAuthCallbackPath(defaultPath)
	}

	parsed, err := url.Parse(configuredURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return configuredURL
	}

	host := requestOAuthHost(c)
	if host == "" || !h.isOAuthRedirectHostAllowed(host) {
		return configuredURL
	}

	scheme := "http"
	if isRequestHTTPS(c) {
		scheme = "https"
	}
	parsed.Scheme = scheme
	parsed.Host = host
	return parsed.String()
}

func (h *AuthHandler) isOAuthRedirectHostAllowed(host string) bool {
	host = normalizeOAuthHost(host)
	if host == "" {
		return false
	}

	for _, allowed := range h.oauthRedirectAllowedHosts() {
		if host == normalizeOAuthHost(allowed) {
			return true
		}
	}
	return false
}

func (h *AuthHandler) oauthRedirectAllowedHosts() []string {
	if h != nil && h.cfg != nil {
		return h.cfg.Security.OAuthRedirect.AllowedHosts
	}
	return nil
}

func requestOAuthHost(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return normalizeOAuthHost(c.Request.Host)
}

func normalizeOAuthHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		if parsed, err := url.Parse(host); err == nil {
			host = parsed.Host
		}
	}
	if strings.ContainsAny(host, "\r\n\t /") {
		return ""
	}
	if h, p, err := net.SplitHostPort(host); err == nil && p != "" {
		host = h
	}
	return strings.Trim(host, "[]")
}

func normalizeOAuthCallbackPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

package service

import (
	"strings"
)

const (
	KiroContentType  = "application/x-amz-json-1.0"
	KiroAcceptStream = "*/*"

	KiroUserAgent     = "aws-sdk-rust/1.3.9 os/macos lang/rust/1.87.0"
	KiroFullUserAgent = "aws-sdk-rust/1.3.9 ua/2.1 api/ssooidc/1.88.0 os/macos lang/rust/1.87.0 m/E app/AmazonQ-For-CLI"

	KiroDefaultRegion             = "us-east-1"
	KiroDefaultCodeWhispererBase  = "https://codewhisperer.us-east-1.amazonaws.com"
	KiroDefaultAmazonQBase        = "https://q.us-east-1.amazonaws.com"
	KiroCodeWhispererTarget       = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	KiroAmazonQTarget             = "AmazonQDeveloperStreamingService.SendMessage"
	KiroCodeWhispererEndpointPath = "/generateAssistantResponse"
)

type kiroEndpointConfig struct {
	Name   string
	URL    string
	Origin string
	Target string
}

func (a *Account) IsKiro() bool {
	return a != nil && a.Platform == PlatformKiro
}

func (a *Account) IsKiroOAuth() bool {
	return a.IsKiro() && a.Type == AccountTypeOAuth
}

func (a *Account) GetKiroAccessToken() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"access_token", "accessToken", "api_key"} {
		if value := strings.TrimSpace(a.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *Account) GetKiroRefreshToken() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"refresh_token", "refreshToken"} {
		if value := strings.TrimSpace(a.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *Account) GetKiroProfileARN() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"profile_arn", "profileArn"} {
		if value := strings.TrimSpace(a.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *Account) GetKiroClientID() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"client_id", "clientId"} {
		if value := strings.TrimSpace(a.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *Account) GetKiroClientSecret() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"client_secret", "clientSecret"} {
		if value := strings.TrimSpace(a.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *Account) GetKiroRegion() string {
	if !a.IsKiro() {
		return ""
	}
	if region := strings.TrimSpace(a.GetCredential("region")); region != "" {
		return region
	}
	return KiroDefaultRegion
}

func (a *Account) GetKiroPreferredEndpoint() string {
	if !a.IsKiro() {
		return ""
	}
	preferred := strings.ToLower(strings.TrimSpace(a.GetCredential("preferred_endpoint")))
	switch preferred {
	case "amazonq", "q", "cli":
		return "amazonq"
	case "codewhisperer", "ide", "kiro", "":
		return "codewhisperer"
	default:
		return preferred
	}
}

func (a *Account) GetKiroBaseURL() string {
	if !a.IsKiro() {
		return ""
	}
	for _, key := range []string{"base_url", "endpoint_url"} {
		if value := strings.TrimRight(strings.TrimSpace(a.GetCredential(key)), "/"); value != "" {
			return value
		}
	}
	return ""
}

func kiroEndpointForAccount(account *Account) kiroEndpointConfig {
	preferred := account.GetKiroPreferredEndpoint()
	baseURL := strings.TrimRight(account.GetKiroBaseURL(), "/")
	switch preferred {
	case "amazonq":
		if baseURL == "" {
			baseURL = KiroDefaultAmazonQBase
		}
		return kiroEndpointConfig{
			Name:   "amazonq",
			URL:    baseURL,
			Origin: "CLI",
			Target: KiroAmazonQTarget,
		}
	default:
		if baseURL == "" {
			baseURL = KiroDefaultCodeWhispererBase
		}
		url := baseURL
		if !strings.HasSuffix(url, KiroCodeWhispererEndpointPath) {
			url += KiroCodeWhispererEndpointPath
		}
		return kiroEndpointConfig{
			Name:   "codewhisperer",
			URL:    url,
			Origin: "AI_EDITOR",
			Target: KiroCodeWhispererTarget,
		}
	}
}

func ResolveKiroModelID(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return "claude-sonnet-4.5"
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = strings.TrimSpace(parts[len(parts)-1])
	}
	model = strings.TrimSuffix(model, "-agentic")
	model = strings.TrimSuffix(model, "-chat")

	modelMap := map[string]string{
		"kiro-auto":                       "auto",
		"auto":                            "auto",
		"kiro-claude-opus-4-5":            "claude-opus-4.5",
		"kiro-claude-sonnet-4-5":          "claude-sonnet-4.5",
		"kiro-claude-sonnet-4-5-20250929": "claude-sonnet-4.5",
		"kiro-claude-sonnet-4":            "claude-sonnet-4",
		"kiro-claude-sonnet-4-20250514":   "claude-sonnet-4",
		"kiro-claude-haiku-4-5":           "claude-haiku-4.5",
		"claude-opus-4-5":                 "claude-opus-4.5",
		"claude-opus-4.5":                 "claude-opus-4.5",
		"claude-sonnet-4-5":               "claude-sonnet-4.5",
		"claude-sonnet-4.5":               "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250929":      "claude-sonnet-4.5",
		"claude-sonnet-4":                 "claude-sonnet-4",
		"claude-sonnet-4-20250514":        "claude-sonnet-4",
		"claude-haiku-4-5":                "claude-haiku-4.5",
		"claude-haiku-4.5":                "claude-haiku-4.5",
		"claude-haiku-4-5-20251001":       "claude-haiku-4.5",
		"claude-3-7-sonnet-20250219":      "claude-3-7-sonnet-20250219",
	}
	if mapped, ok := modelMap[model]; ok {
		return mapped
	}
	switch {
	case strings.Contains(model, "haiku"):
		return "claude-haiku-4.5"
	case strings.Contains(model, "opus"):
		return "claude-opus-4.5"
	case strings.Contains(model, "sonnet-3-7"), strings.Contains(model, "3-7-sonnet"):
		return "claude-3-7-sonnet-20250219"
	case strings.Contains(model, "sonnet-4-5"), strings.Contains(model, "sonnet-4.5"):
		return "claude-sonnet-4.5"
	case strings.Contains(model, "sonnet"):
		return "claude-sonnet-4"
	default:
		return "claude-sonnet-4.5"
	}
}

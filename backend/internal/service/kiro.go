package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

const (
	KiroContentType  = "application/x-amz-json-1.0"
	KiroAcceptStream = "*/*"

	KiroRustRuntimeUserAgent    = "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererruntime/0.1.14474 os/linux lang/rust/1.92.0 md/appVersion-2.0.0 app/AmazonQ-For-CLI"
	KiroRustRuntimeAmzUserAgent = "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererruntime/0.1.14474 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI"
	KiroRuntimeSDKVersion       = "1.0.0"

	KiroDefaultRegion             = "us-east-1"
	KiroDefaultCodeWhispererBase  = "https://codewhisperer.us-east-1.amazonaws.com"
	KiroDefaultAmazonQBase        = "https://q.us-east-1.amazonaws.com"
	KiroCodeWhispererTarget       = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	KiroAmazonQTarget             = "AmazonQDeveloperStreamingService.SendMessage"
	KiroCodeWhispererEndpointPath = "/generateAssistantResponse"
)

var (
	kiroOSTypes    = []string{"darwin", "windows", "linux"}
	kiroOSVersions = map[string][]string{
		"darwin":  {"25.2.0", "25.1.0", "25.0.0", "24.5.0", "24.4.0", "24.3.0"},
		"windows": {"10.0.26200", "10.0.26100", "10.0.22631", "10.0.22621", "10.0.19045"},
		"linux":   {"6.12.0", "6.11.0", "6.8.0", "6.6.0", "6.5.0", "6.1.0"},
	}
	kiroNodeVersions = []string{
		"22.21.1", "22.21.0", "22.20.0", "22.19.0", "22.18.0",
		"20.18.0", "20.17.0", "20.16.0",
	}
	kiroIDEVersions = []string{
		"0.10.32", "0.10.16", "0.10.10",
		"0.9.47", "0.9.40", "0.9.2",
		"0.8.206", "0.8.140", "0.8.135", "0.8.86",
	}
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

func (a *Account) GetKiroAuthMethod() string {
	if !a.IsKiro() {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(a.GetCredential("auth_method"), a.GetCredential("authMethod"), a.GetCredential("provider")))
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

func isKiroCLIAuthMethod(authMethod string) bool {
	return strings.EqualFold(strings.TrimSpace(authMethod), "kiro-cli")
}

func kiroOriginForAuthMethod(authMethod string) string {
	if isKiroCLIAuthMethod(authMethod) {
		return "KIRO_CLI"
	}
	return "AI_EDITOR"
}

func kiroRequestOriginForAccount(account *Account, fallback string) string {
	origin := strings.TrimSpace(fallback)
	if isKiroCLIAuthMethod(account.GetKiroAuthMethod()) && strings.EqualFold(origin, "AI_EDITOR") {
		return "CLI"
	}
	switch {
	case strings.EqualFold(origin, "KIRO_CLI"), strings.EqualFold(origin, "AMAZON_Q"):
		return "CLI"
	case strings.EqualFold(origin, "KIRO_AI_EDITOR"), origin == "":
		return "AI_EDITOR"
	default:
		return origin
	}
}

type kiroRuntimeFingerprint struct {
	OSType      string
	OSVersion   string
	NodeVersion string
	KiroVersion string
	KiroHash    string
}

func applyKiroRuntimeHeaders(req *http.Request, account *Account, accessToken string) {
	if req == nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if isKiroCLIAuthMethod(account.GetKiroAuthMethod()) {
		req.Header.Set("X-Amz-User-Agent", KiroRustRuntimeAmzUserAgent)
		req.Header.Set("User-Agent", KiroRustRuntimeUserAgent)
	} else {
		fp := buildKiroRuntimeFingerprint(kiroAccountKey(account))
		req.Header.Set("X-Amz-User-Agent", fmt.Sprintf("aws-sdk-js/%s KiroIDE-%s-%s",
			KiroRuntimeSDKVersion, fp.KiroVersion, fp.KiroHash))
		req.Header.Set("User-Agent", fmt.Sprintf(
			"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererruntime#%s m/N,E KiroIDE-%s-%s",
			KiroRuntimeSDKVersion, fp.OSType, fp.OSVersion, fp.NodeVersion, KiroRuntimeSDKVersion,
			fp.KiroVersion, fp.KiroHash))
	}
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=1")
}

func kiroAccountKey(account *Account) string {
	if account == nil {
		return generateKiroAccountKey(uuid.NewString())
	}
	if clientID := strings.TrimSpace(account.GetKiroClientID()); clientID != "" {
		return generateKiroAccountKey(clientID)
	}
	if refreshToken := strings.TrimSpace(account.GetKiroRefreshToken()); refreshToken != "" {
		return generateKiroAccountKey(refreshToken)
	}
	if accessToken := strings.TrimSpace(account.GetKiroAccessToken()); accessToken != "" {
		return generateKiroAccountKey(accessToken)
	}
	if account.ID > 0 {
		return generateKiroAccountKey(fmt.Sprintf("account:%d", account.ID))
	}
	return generateKiroAccountKey(uuid.NewString())
}

func generateKiroAccountKey(seed string) string {
	hash := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(hash[:8])
}

func buildKiroRuntimeFingerprint(accountKey string) kiroRuntimeFingerprint {
	hash := sha256.Sum256([]byte(accountKey))
	osType := runtime.GOOS
	if _, ok := kiroOSVersions[osType]; !ok {
		osType = pickKiroString(kiroOSTypes, hash, 0)
	}
	return kiroRuntimeFingerprint{
		OSType:      osType,
		OSVersion:   pickKiroString(kiroOSVersions[osType], hash, 8),
		NodeVersion: pickKiroString(kiroNodeVersions, hash, 16),
		KiroVersion: pickKiroString(kiroIDEVersions, hash, 24),
		KiroHash:    hex.EncodeToString(hash[:]),
	}
}

func pickKiroString(values []string, hash [32]byte, offset int) string {
	if len(values) == 0 {
		return ""
	}
	if offset < 0 || offset+8 > len(hash) {
		offset = 0
	}
	idx := binary.BigEndian.Uint64(hash[offset : offset+8])
	return values[int(idx%uint64(len(values)))]
}

func kiroRegionFromProfileARN(profileARN string) string {
	parts := strings.Split(strings.TrimSpace(profileARN), ":")
	if len(parts) < 6 || parts[0] != "arn" || parts[2] != "codewhisperer" {
		return ""
	}
	region := strings.TrimSpace(parts[3])
	if region == "" || !strings.Contains(region, "-") {
		return ""
	}
	return region
}

func kiroCodeWhispererBaseForProfileARN(profileARN string) string {
	region := kiroRegionFromProfileARN(profileARN)
	if region == "" {
		region = KiroDefaultRegion
	}
	return "https://codewhisperer." + region + ".amazonaws.com"
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

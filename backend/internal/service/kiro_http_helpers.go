package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/google/uuid"
)

// Kiro 默认 profileArn 常量（与 kiro.rs-admin 保持一致）。
// BuilderID 占位符：纯 BuilderID 账号没有真实 profile，上游 IDE 发送此占位符。
// Social 共享 ARN：Social 登录账号使用此共享 ARN。
const (
	kiroBuilderIDProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	kiroSocialProfileARN    = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
)

// kiroIsPlaceholderProfileARN 判断给定 ARN 是否为 BuilderID 占位符（非真实可用的 profile）。
func kiroIsPlaceholderProfileARN(arn string) bool {
	return arn == kiroBuilderIDProfileARN
}

// kiroIsSocialLogin 判断账号是否为 Social 登录方式。
func kiroIsSocialLogin(account *Account) bool {
	if account == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "social")
}

// kiroDefaultProfileARN 返回凭据缺少显式 profileArn 时应使用的默认 ARN：
// Social 登录 → kiroSocialProfileARN，其余（BuilderID 等）→ kiroBuilderIDProfileARN。
func kiroDefaultProfileARN(account *Account) string {
	if kiroIsSocialLogin(account) {
		return kiroSocialProfileARN
	}
	return kiroBuilderIDProfileARN
}

func buildKiroAccountKey(account *Account) string {
	if account == nil {
		return ""
	}
	if machineID, ok := accountKiroMachineID(account); ok {
		return "machine:" + machineID[:16]
	}
	return kiropkg.BuildAccountKey(
		account.GetCredential("client_id"),
		account.GetCredential("client_id_hash"),
		account.GetCredential("refresh_token"),
		account.GetCredential("profile_arn"),
		account.ID,
	)
}

func accountKiroMachineID(account *Account) (string, bool) {
	if account == nil {
		return "", false
	}
	for _, key := range []string{"machine_id", "machineId"} {
		if machineID, ok := kiropkg.NormalizeMachineID(account.GetCredential(key)); ok {
			return machineID, true
		}
	}
	return "", false
}

func buildKiroMachineID(account *Account) string {
	if account == nil {
		return kiropkg.BuildMachineID("", "", "account:nil")
	}
	if machineID, ok := accountKiroMachineID(account); ok {
		return machineID
	}
	fallbackKey := buildKiroMachineIDFallbackKey(account)
	if account.Type == AccountTypeAPIKey {
		return kiropkg.BuildMachineID("", firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"), fallbackKey)
	}
	return kiropkg.BuildMachineID(account.GetCredential("refresh_token"), "", fallbackKey)
}

func firstKiroCredential(account *Account, keys ...string) string {
	if account == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func buildKiroMachineIDFallbackKey(account *Account) string {
	if account == nil {
		return "account:nil"
	}
	if account.ID > 0 {
		return fmt.Sprintf("account:%d", account.ID)
	}
	for _, key := range []string{"client_id", "profile_arn"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return key + ":" + value
		}
	}
	if name := strings.TrimSpace(account.Name); name != "" {
		return "name:" + name
	}
	return "account:unknown"
}

func ensureKiroMachineIDCredential(account *Account) string {
	if account == nil {
		return kiropkg.BuildMachineID("", "", "account:nil")
	}
	if machineID, ok := accountKiroMachineID(account); ok {
		if account.GetCredential("machine_id") == "" {
			if account.Credentials == nil {
				account.Credentials = make(map[string]any)
			}
			account.Credentials["machine_id"] = machineID
		}
		return machineID
	}
	machineID := buildKiroMachineID(account)
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	account.Credentials["machine_id"] = machineID
	return machineID
}

func ensureKiroMachineIDPersisted(ctx context.Context, repo AccountRepository, account *Account) string {
	if account == nil {
		return kiropkg.BuildMachineID("", "", "account:nil")
	}
	hadMachineID := false
	if _, ok := accountKiroMachineID(account); ok {
		hadMachineID = true
	}
	machineID := ensureKiroMachineIDCredential(account)
	if !hadMachineID && repo != nil {
		if updater, ok := any(repo).(accountCredentialsUpdater); ok {
			_ = updater.UpdateCredentials(ctx, account.ID, cloneCredentials(account.Credentials))
		}
	}
	return machineID
}

func kiroRuntimeKey(account *Account) string {
	if account == nil {
		return ""
	}
	ensureKiroMachineIDCredential(account)
	return buildKiroAccountKey(account)
}

func snapshotKiroMachineIdentityAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	return &Account{
		ID:          account.ID,
		Name:        account.Name,
		Platform:    account.Platform,
		Type:        account.Type,
		Credentials: cloneCredentials(account.Credentials),
	}
}

func mergeKiroCredentialsWithStableMachineID(account *Account, newCredentials map[string]any) map[string]any {
	merged := cloneCredentials(newCredentials)
	if account != nil {
		merged = MergeCredentials(account.Credentials, merged)
	}
	if account == nil {
		return merged
	}
	if machineID, ok := accountKiroMachineID(account); ok {
		merged["machine_id"] = machineID
		return merged
	}
	if machineID, ok := kiropkg.NormalizeMachineID(stringFromMap(merged, "machine_id")); ok {
		merged["machine_id"] = machineID
		return merged
	}
	if machineID, ok := kiropkg.NormalizeMachineID(stringFromMap(merged, "machineId")); ok {
		merged["machine_id"] = machineID
		return merged
	}
	merged["machine_id"] = buildKiroMachineID(account)
	return merged
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func buildKiroRequestID(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-request-id")); requestID != "" {
		return requestID
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-amzn-requestid")); requestID != "" {
		return requestID
	}
	return strings.TrimSpace(resp.Header.Get("x-amz-request-id"))
}

func isKiroSuspendedBody(respBody []byte) bool {
	body := string(respBody)
	return strings.Contains(body, "SUSPENDED") || strings.Contains(body, "TEMPORARILY_SUSPENDED")
}

func isKiroTokenErrorBody(respBody []byte) bool {
	lower := strings.ToLower(string(respBody))
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "expired") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unauthorized")
}

func kiroProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

// isKiroDirectModeAccount 判断账号是否走 Kiro 直连 AWS 模式。
// - OAuth 账号:直连 AWS(q.{region}.amazonaws.com 或 KRS),走 forwardKiroMessages。
// - API Key 账号:
//   - base_url 为空 → 直连 AWS(ksk_ + tokentype: API_KEY),走 forwardKiroMessages。
//   - base_url 非空 → 外部 Anthropic 兼容中转,返回 false,落回通用 buildUpstreamRequest
//     反代路径(请求 {base_url}/v1/messages,发 x-api-key),作为分组兜底/灾备账号。
func isKiroDirectModeAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformKiro {
		return false
	}
	if account.Type == AccountTypeOAuth {
		return true
	}
	if account.Type == AccountTypeAPIKey {
		return strings.TrimSpace(account.GetCredential("base_url")) == ""
	}
	return false
}

func kiroAPIRegion(account *Account) string {
	if account == nil {
		return kiroDefaultRegion
	}
	region := strings.TrimSpace(account.GetCredential("api_region"))
	if region == "" {
		region = strings.TrimSpace(account.GetCredential("region"))
	}
	if region == "" {
		region = kiroDefaultRegion
	}
	return region
}

func applyKiroConditionalHeaders(req *http.Request, account *Account) {
	if req == nil || account == nil {
		return
	}
	// API Key 账号:AWS 要求显式声明 token 类型(对齐 kiro.rs 的 API Key 模式)。
	// 必须用小写 "tokentype":AWS getUsageLimits 端点对该非标准头大小写敏感,
	// 而 http.Header.Set 会规范化成 "Tokentype",在 HTTP/1.1 下原样发出会被忽略,
	// 导致 ksk_ 被当作 OAuth token 校验 → 403 invalid。直接赋值 map key 绕过规范化,
	// HTTP/1.1 原样发小写、HTTP/2 本就强制小写,两种协议都正确。
	if account.Type == AccountTypeAPIKey {
		req.Header["tokentype"] = []string{"API_KEY"}
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "external_idp") {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
	provider := strings.TrimSpace(account.GetCredential("provider"))
	if strings.EqualFold(provider, "Internal") || strings.EqualFold(provider, kiropkg.ProviderExternalIdp) {
		req.Header.Set("redirect-for-internal", "true")
	}
}

func resolveKiroPayloadProfileArn(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("profile_arn"))
}

// kiroResolveProfileArnForPayload 返回 generateAssistantResponse 请求体中的 profileArn。
// 对齐 kiro.rs:Q endpoint 主聊天请求在 JSON 根对象携带已有 profileArn,但不使用默认
// 占位 ARN fallback;KRS endpoint 仍使用 kiroResolveProfileArnForKRS 的 fallback 逻辑。
func kiroResolveProfileArnForPayload(account *Account, mode string) string {
	if mode == KiroEndpointModeKRS {
		return kiroResolveProfileArnForKRS(account)
	}
	return resolveKiroPayloadProfileArn(account)
}

// kiroResolveProfileArnForKRS 返回 KRS endpoint 所需的 profileArn。
// KRS endpoint（runtime.us-east-1.kiro.dev）强制要求 profileArn，
// 凭据无值时 fallback 到默认 ARN（Social → Social ARN，其余 → BuilderID 占位符）。
func kiroResolveProfileArnForKRS(account *Account) string {
	arn := resolveKiroPayloadProfileArn(account)
	if arn != "" {
		return arn
	}
	return kiroDefaultProfileARN(account)
}

func isKiroMCPEndpoint(endpointURL string) bool {
	return strings.HasPrefix(endpointURL, "https://q.") && strings.HasSuffix(endpointURL, ".amazonaws.com/mcp")
}

func newKiroJSONRequest(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	// KRS endpoint 用 Kiro IDE 实际 UA 格式 ("KiroIDE <version> <machineId>")；
	// AWS Q endpoint 继续用 AWS SDK 风格 UA。按 URL 区分以避免再加一个参数。
	if endpointURL == kiroKRSEndpointURL {
		req.Header.Set("User-Agent", kiropkg.BuildKiroIDERuntimeUserAgent(accountKey, machineID))
	} else {
		req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
	}
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
	if amzTarget != "" {
		req.Header.Set("X-Amz-Target", amzTarget)
	}
	if account != nil && (endpointURL == kiroKRSEndpointURL || isKiroMCPEndpoint(endpointURL)) {
		profileArn := resolveKiroPayloadProfileArn(account)
		if endpointURL == kiroKRSEndpointURL {
			profileArn = kiroResolveProfileArnForKRS(account)
		}
		if profileArn != "" {
			req.Header.Set("x-amzn-kiro-profile-arn", profileArn)
		}
	}
	applyKiroConditionalHeaders(req, account)
	return req, nil
}

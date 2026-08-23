package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const cursorSSOImportConcurrency = 3

// CursorOAuthHandler exposes the supported Cursor credential paths. Cursor
// intentionally has no first-party password authorization flow.
type CursorOAuthHandler struct {
	cursorOAuthService *service.CursorOAuthService
	adminService       service.AdminService
}

func NewCursorOAuthHandler(cursorOAuthService *service.CursorOAuthService, adminService service.AdminService) *CursorOAuthHandler {
	return &CursorOAuthHandler{cursorOAuthService: cursorOAuthService, adminService: adminService}
}

type cursorTokenInfoResponse struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	Sub          string `json:"sub,omitempty"`
	Status       string `json:"status,omitempty"`
}

func cursorTokenInfoResponseFrom(info *service.CursorTokenInfo) *cursorTokenInfoResponse {
	if info == nil {
		return nil
	}
	return &cursorTokenInfoResponse{AccessToken: info.AccessToken, RefreshToken: info.RefreshToken, APIKey: info.APIKey, ExpiresAt: info.ExpiresAt, Sub: info.UserID}
}

func (h *CursorOAuthHandler) GetCapabilities(c *gin.Context) {
	caps := h.cursorOAuthService.GetCapabilities()
	response.Success(c, gin.H{
		"password_auth_enabled":  false,
		"deep_link_enabled":      caps.DeepLinkEnabled,
		"api_key_import_enabled": caps.APIKeyImportEnabled,
		"cookie_import_enabled":  caps.CookieImportEnabled,
	})
}

type CursorGenerateAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *CursorOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req CursorGenerateAuthURLRequest
	_ = c.ShouldBindJSON(&req)
	result, err := h.cursorOAuthService.GenerateAuthURL(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"auth_url": result.AuthURL, "session_id": result.UUID, "state": result.Verifier})
}

type CursorPollRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	State     string `json:"state" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

func (h *CursorOAuthHandler) Poll(c *gin.Context) {
	var req CursorPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	info, err := h.cursorOAuthService.Poll(c.Request.Context(), req.SessionID, req.State, req.ProxyID)
	if err != nil {
		if status := infraerrors.FromError(err); status != nil && status.Reason == "CURSOR_OAUTH_PENDING" {
			response.Success(c, &cursorTokenInfoResponse{Status: "pending"})
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cursorTokenInfoResponseFrom(info))
}

type CursorExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Code      string `json:"code" binding:"required"`
	State     string `json:"state"`
	ProxyID   *int64 `json:"proxy_id"`
}

func (h *CursorOAuthHandler) ExchangeCode(c *gin.Context) {
	var req CursorExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	info, err := h.importCursorCredential(c.Request.Context(), req.Code, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cursorTokenInfoResponseFrom(info))
}

type CursorRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
	RT           string `json:"rt"`
	ProxyID      *int64 `json:"proxy_id"`
}

func (h *CursorOAuthHandler) RefreshToken(c *gin.Context) {
	var req CursorRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(req.RT)
	}
	if refreshToken == "" {
		response.BadRequest(c, "refresh_token is required")
		return
	}
	info, err := h.cursorOAuthService.RefreshToken(c.Request.Context(), refreshToken, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cursorTokenInfoResponseFrom(info))
}

type CursorSSOTokenRequest struct {
	SSOToken string `json:"sso_token"`
	ProxyID  *int64 `json:"proxy_id"`
}

func (h *CursorOAuthHandler) ValidateSSOToken(c *gin.Context) {
	var req CursorSSOTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	info, err := h.importCursorCredential(c.Request.Context(), req.SSOToken, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cursorTokenInfoResponseFrom(info))
}

func (h *CursorOAuthHandler) AuthorizePassword(c *gin.Context) {
	response.Error(c, http.StatusBadRequest, "CURSOR_OAUTH_PASSWORD_UNSUPPORTED: password login is not supported for Cursor")
}

func (h *CursorOAuthHandler) importCursorCredential(ctx context.Context, credential string, proxyID *int64) (*service.CursorTokenInfo, error) {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_NO_CREDENTIAL", "credential is required")
	}
	if cursorpkg.IsUserAPIKey(credential) {
		return h.cursorOAuthService.ImportFromAPIKey(ctx, credential, proxyID)
	}
	return h.cursorOAuthService.ImportFromCookie(ctx, credential)
}

func (h *CursorOAuthHandler) RefreshAccountToken(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account.Platform != service.PlatformCursor || !account.IsOAuth() {
		response.BadRequest(c, "Cannot refresh non-Cursor OAuth account credentials")
		return
	}
	info, err := h.cursorOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	credentials := service.MergeCredentials(account.Credentials, h.cursorOAuthService.BuildAccountCredentials(info))
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		credentials["base_url"] = baseURL
	}
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{Credentials: credentials})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(updated))
}

type CursorSSOToOAuthRequest struct {
	SSOTokens          []string       `json:"sso_tokens"`
	SSOToken           string         `json:"sso_token"`
	Name               string         `json:"name"`
	Notes              *string        `json:"notes"`
	ProxyID            *int64         `json:"proxy_id"`
	GroupIDs           []int64        `json:"group_ids"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra"`
	Concurrency        int            `json:"concurrency"`
	LoadFactor         *int           `json:"load_factor"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier"`
	ExpiresAt          *int64         `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}

type CursorSSOToOAuthItemResult struct {
	Index   int          `json:"index"`
	Name    string       `json:"name,omitempty"`
	Account *dto.Account `json:"account,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type CursorSSOToOAuthResponse struct {
	Created []CursorSSOToOAuthItemResult `json:"created"`
	Failed  []CursorSSOToOAuthItemResult `json:"failed"`
}

type cursorSSOImportJob struct {
	index int
	token string
}
type cursorSSOImportWorkerResult struct {
	created bool
	item    CursorSSOToOAuthItemResult
}

func (h *CursorOAuthHandler) CreateAccountsFromSSO(c *gin.Context) {
	var req CursorSSOToOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	tokens := normalizeCursorImportTokens(req.SSOTokens, req.SSOToken)
	if len(tokens) == 0 {
		response.BadRequest(c, "sso_tokens is required")
		return
	}
	workers := cursorSSOImportConcurrency
	if len(tokens) < workers {
		workers = len(tokens)
	}
	jobs := make(chan cursorSSOImportJob)
	items := make([]cursorSSOImportWorkerResult, len(tokens))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				items[job.index] = h.safeCreateCursorAccountFromToken(c.Request.Context(), req, job.token, job.index+1, len(tokens))
			}
		}()
	}
	for i, token := range tokens {
		jobs <- cursorSSOImportJob{index: i, token: token}
	}
	close(jobs)
	wg.Wait()
	result := CursorSSOToOAuthResponse{Created: make([]CursorSSOToOAuthItemResult, 0, len(tokens)), Failed: make([]CursorSSOToOAuthItemResult, 0)}
	for _, item := range items {
		if item.created {
			result.Created = append(result.Created, item.item)
		} else {
			result.Failed = append(result.Failed, item.item)
		}
	}
	response.Success(c, result)
}

func (h *CursorOAuthHandler) safeCreateCursorAccountFromToken(ctx context.Context, req CursorSSOToOAuthRequest, token string, index, total int) (result cursorSSOImportWorkerResult) {
	defer func() {
		if recover() != nil {
			result = cursorSSOImportWorkerResult{item: CursorSSOToOAuthItemResult{Index: index, Error: "internal worker panic"}}
		}
	}()
	return h.createCursorAccountFromToken(ctx, req, token, index, total)
}

func (h *CursorOAuthHandler) createCursorAccountFromToken(ctx context.Context, req CursorSSOToOAuthRequest, token string, index, total int) cursorSSOImportWorkerResult {
	info, err := h.importCursorCredential(ctx, token, req.ProxyID)
	if err != nil {
		return cursorSSOImportWorkerResult{item: CursorSSOToOAuthItemResult{Index: index, Error: cursorImportErrorMessage(err)}}
	}
	name := cursorSSOImportAccountName(req.Name, info, index, total)
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: name, Notes: req.Notes, Platform: service.PlatformCursor, Type: service.AccountTypeOAuth,
		Credentials: cursorSSOImportCredentials(h.cursorOAuthService.BuildAccountCredentials(info), req.Credentials), Extra: sanitizeCursorImportMap(req.Extra), ProxyID: req.ProxyID,
		Concurrency: req.Concurrency, LoadFactor: req.LoadFactor, Priority: req.Priority, RateMultiplier: req.RateMultiplier, GroupIDs: append([]int64(nil), req.GroupIDs...), ExpiresAt: req.ExpiresAt, AutoPauseOnExpired: req.AutoPauseOnExpired,
	})
	if err != nil {
		return cursorSSOImportWorkerResult{item: CursorSSOToOAuthItemResult{Index: index, Name: name, Error: cursorImportErrorMessage(err)}}
	}
	return cursorSSOImportWorkerResult{created: true, item: CursorSSOToOAuthItemResult{Index: index, Name: name, Account: dto.AccountFromService(account)}}
}

func cursorSSOImportCredentials(built, requested map[string]any) map[string]any {
	allowed := map[string]struct{}{"base_url": {}, "model_mapping": {}, "header_override": {}, "header_overrides": {}, "header_override_enabled": {}, "custom_headers": {}}
	operator := make(map[string]any)
	for key, value := range requested {
		if _, ok := allowed[key]; ok && !service.IsSensitiveCredentialKey(key) {
			if sanitized, keep := sanitizeCursorImportValue(value); keep {
				operator[key] = sanitized
			}
		}
	}
	credentials := service.MergeCredentials(operator, built)
	for key := range credentials {
		if service.IsSensitiveCredentialKey(key) && key != "access_token" && key != "refresh_token" && key != "api_key" && key != "web_session_token" {
			delete(credentials, key)
		}
	}
	if baseURL, ok := requested["base_url"].(string); ok && strings.TrimSpace(baseURL) != "" {
		credentials["base_url"] = strings.TrimSpace(baseURL)
	}
	return service.SanitizeStoredCredentials(service.PlatformCursor, credentials)
}

// sanitizeCursorImportMap keeps non-secret operator metadata while recursively
// removing credential-bearing map entries before either persistence or DTO
// conversion. JSON request values are maps/lists, but the type switch also
// covers common programmatic map shapes used by callers and tests.
func sanitizeCursorImportMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	sanitized, _ := sanitizeCursorImportValue(input)
	result, _ := sanitized.(map[string]any)
	return result
}

func sanitizeCursorImportValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if cursorImportSensitiveOperatorKey(key) {
				continue
			}
			if sanitized, keep := sanitizeCursorImportValue(item); keep {
				result[key] = sanitized
			}
		}
		return result, true
	case map[string]string:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if !cursorImportSensitiveOperatorKey(key) {
				result[key] = item
			}
		}
		return result, true
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if sanitized, keep := sanitizeCursorImportValue(item); keep {
				result = append(result, sanitized)
			}
		}
		return result, true
	case []map[string]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if sanitized, keep := sanitizeCursorImportValue(item); keep {
				result = append(result, sanitized)
			}
		}
		return result, true
	default:
		return value, true
	}
}

func cursorImportSensitiveOperatorKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "xapikey", "apikey", "accesstoken", "refreshtoken", "websessiontoken", "password", "ssotoken", "token", "secret", "clientsecret":
		return true
	default:
		return service.IsSensitiveCredentialKey(key)
	}
}

func normalizeCursorImportTokens(tokens []string, single string) []string {
	items := append([]string{}, tokens...)
	if strings.TrimSpace(single) != "" {
		items = append([]string{single}, items...)
	}
	seen, normalized := make(map[string]struct{}, len(items)), make([]string, 0, len(items))
	for _, item := range items {
		for _, token := range strings.Split(strings.NewReplacer(",", "\n", "\r", "\n").Replace(item), "\n") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if _, exists := seen[token]; exists {
				continue
			}
			seen[token] = struct{}{}
			normalized = append(normalized, token)
		}
	}
	return normalized
}

func cursorSSOImportAccountName(base string, info *service.CursorTokenInfo, index, total int) string {
	base = strings.TrimSpace(base)
	if base == "" && info != nil && strings.TrimSpace(info.UserID) != "" {
		base = "Cursor " + strings.TrimSpace(info.UserID)
	}
	if base == "" {
		base = "Cursor OAuth Account"
	}
	if total > 1 {
		return base + " #" + strconv.Itoa(index)
	}
	return base
}

func cursorImportErrorMessage(err error) string {
	// Provider and persistence errors may include a copied upstream body or a
	// supplied credential. Bulk responses are audit-visible, so their contract
	// is intentionally fixed rather than conditionally exposing status details.
	_ = err
	return "credential import failed"
}

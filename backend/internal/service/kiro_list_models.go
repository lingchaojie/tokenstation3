package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/google/uuid"
)

const (
	kiroModelsMaxPages       = 10
	kiroModelsFallbackRegion = "eu-central-1"
)

var kiroModelsRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`)

func (s *AccountTestService) syncKiroModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, newUpstreamModelSyncConfigError("Upstream HTTP client is not configured", nil)
	}
	if isKiroRelayModeAccount(account) {
		// Kiro relays use the same authorization and model list format as
		// Anthropic-compatible forwarding.
		models, body, _, err := s.fetchUpstreamModelListRequest(ctx, account)
		if err != nil {
			return nil, err
		}
		_, metadata, err := extractUpstreamModelCatalog(body, false)
		if err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Invalid Kiro relay model catalog", err)
		}
		return &UpstreamModelCatalog{Models: models, Metadata: metadata}, nil
	}
	if !isKiroDirectModeAccount(account) {
		return nil, newUpstreamModelSyncUnsupportedError("Kiro model sync requires a direct account or an API key relay", nil)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	region := strings.ToLower(kiroAPIRegion(account))
	if !kiroModelsRegionPattern.MatchString(region) {
		return nil, newUpstreamModelSyncConfigError("Invalid Kiro API region", nil)
	}
	token, err := s.kiroModelListToken(ctx, account)
	if err != nil {
		return nil, err
	}
	catalog, err := s.listKiroNativeModels(ctx, account, region, token)
	if upstreamModelSyncStatusCode(err) == http.StatusForbidden && region != kiroModelsFallbackRegion {
		catalog, err = s.listKiroNativeModels(ctx, account, kiroModelsFallbackRegion, token)
	}
	return catalog, err
}

func (s *AccountTestService) kiroModelListToken(ctx context.Context, account *Account) (string, error) {
	if account.Type == AccountTypeAPIKey {
		if token := firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"); token != "" {
			return token, nil
		}
		return "", newUpstreamModelSyncConfigError("No Kiro API key is available", nil)
	}
	if s.kiroTokenProvider == nil {
		return "", newUpstreamModelSyncConfigError("Kiro token provider is not configured", nil)
	}
	token, err := s.kiroTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return "", newUpstreamModelSyncUpstreamError("Failed to get Kiro access token", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", newUpstreamModelSyncConfigError("No Kiro access token is available", nil)
	}
	return token, nil
}

func (s *AccountTestService) listKiroNativeModels(ctx context.Context, account *Account, region, token string) (*UpstreamModelCatalog, error) {
	catalog := &UpstreamModelCatalog{Metadata: make(map[string]UpstreamModelMetadata)}
	query := url.Values{"origin": {"AI_EDITOR"}, "maxResults": {"50"}}
	if profileARN := resolveKiroPayloadProfileArn(account); profileARN != "" {
		query.Set("profileArn", profileARN)
	}
	accountKey := buildKiroAccountKey(account)
	machineID := buildKiroMachineID(account)

	for page := 0; page < kiroModelsMaxPages; page++ {
		endpoint := fmt.Sprintf("https://q.%s.amazonaws.com/ListAvailableModels?%s", region, query.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Kiro model list URL", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
		req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
		req.Header.Set("x-amzn-codewhisperer-optout", "true")
		req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
		req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
		applyKiroConditionalHeaders(req, account)

		body, err := s.readUpstreamModelsResponse(req, account)
		if err != nil {
			return nil, err
		}
		var result struct {
			Models []struct {
				ID string `json:"modelId"`
			} `json:"models"`
			NextToken string `json:"nextToken"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Invalid Kiro model list response", err)
		}
		for _, model := range result.Models {
			appendKiroNativeModel(catalog, model.ID)
		}

		if result.NextToken == "" {
			if len(catalog.Models) == 0 {
				return nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
			}
			return catalog, nil
		}
		query.Set("nextToken", result.NextToken)
	}
	return nil, newUpstreamModelSyncUpstreamError("Kiro model list exceeded page limit", nil)
}

func appendKiroNativeModel(catalog *UpstreamModelCatalog, modelID string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.Contains(modelID, "*") {
		return
	}
	aliases := []string{modelID}
	if strings.HasPrefix(modelID, "claude-") {
		public := strings.ReplaceAll(strings.TrimSuffix(modelID, "-thinking"), ".", "-")
		aliases = []string{public, public + "-thinking"}
		if public == "claude-haiku-4-5" {
			aliases = append(aliases, "claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001-thinking")
		}
	}
	for _, alias := range aliases {
		if _, exists := catalog.Metadata[alias]; exists {
			continue
		}
		catalog.Models = append(catalog.Models, alias)
		catalog.Metadata[alias] = UpstreamModelMetadata{ID: modelID}
	}
}

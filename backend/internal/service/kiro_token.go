package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	kiroTokenRefreshSkew = 2 * time.Minute
	kiroSSOOIDCEndpoint  = "https://oidc.us-east-1.amazonaws.com"
	kiroAuthEndpoint     = "https://prod.us-east-1.auth.desktop.kiro.dev"
)

type kiroTokenRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

func (s *OpenAIGatewayService) refreshKiroAccessTokenIfNeeded(ctx context.Context, account *Account) error {
	if account == nil || !account.IsKiro() {
		return nil
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil || time.Until(*expiresAt) > kiroTokenRefreshSkew {
		return nil
	}
	if account.GetKiroRefreshToken() == "" {
		return nil
	}
	return s.refreshKiroAccessToken(ctx, account)
}

func (s *OpenAIGatewayService) refreshKiroAccessToken(ctx context.Context, account *Account) error {
	if account == nil || !account.IsKiro() {
		return fmt.Errorf("not a Kiro account")
	}
	refreshToken := account.GetKiroRefreshToken()
	if refreshToken == "" {
		return fmt.Errorf("missing Kiro refresh_token")
	}
	var (
		resp *kiroTokenRefreshResponse
		err  error
	)
	if account.GetKiroClientID() != "" && account.GetKiroClientSecret() != "" && strings.EqualFold(firstNonEmpty(account.GetCredential("auth_method"), account.GetCredential("provider")), "builder-id") {
		resp, err = refreshKiroBuilderIDToken(ctx, account)
	} else if account.GetKiroClientID() != "" && account.GetKiroClientSecret() != "" && strings.TrimSpace(account.GetCredential("auth_method")) == "" {
		resp, err = refreshKiroBuilderIDToken(ctx, account)
	} else {
		resp, err = refreshKiroSocialToken(ctx, account)
	}
	if err != nil {
		return err
	}
	if resp == nil || strings.TrimSpace(resp.AccessToken) == "" {
		return fmt.Errorf("kiro token refresh response missing accessToken")
	}
	creds := cloneCredentials(account.Credentials)
	creds["access_token"] = strings.TrimSpace(resp.AccessToken)
	if strings.TrimSpace(resp.RefreshToken) != "" {
		creds["refresh_token"] = strings.TrimSpace(resp.RefreshToken)
	}
	if strings.TrimSpace(resp.ProfileARN) != "" {
		creds["profile_arn"] = strings.TrimSpace(resp.ProfileARN)
	}
	if resp.ExpiresIn <= 0 {
		resp.ExpiresIn = 3600
	}
	creds["expires_at"] = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second).Format(time.RFC3339)
	if _, ok := creds["auth_method"]; !ok {
		if account.GetKiroClientID() != "" && account.GetKiroClientSecret() != "" {
			creds["auth_method"] = "builder-id"
		} else {
			creds["auth_method"] = "social"
		}
	}
	return persistAccountCredentials(ctx, s.accountRepo, account, creds)
}

func refreshKiroBuilderIDToken(ctx context.Context, account *Account) (*kiroTokenRefreshResponse, error) {
	endpoint := strings.TrimRight(firstNonEmpty(account.GetCredential("oidc_endpoint"), kiroSSOOIDCEndpoint), "/")
	payload := map[string]string{
		"clientId":     account.GetKiroClientID(),
		"clientSecret": account.GetKiroClientSecret(),
		"refreshToken": account.GetKiroRefreshToken(),
		"grantType":    "refresh_token",
	}
	return postKiroTokenRefresh(ctx, account, endpoint+"/token", payload, "KiroIDE")
}

func refreshKiroSocialToken(ctx context.Context, account *Account) (*kiroTokenRefreshResponse, error) {
	endpoint := strings.TrimRight(firstNonEmpty(account.GetCredential("auth_endpoint"), kiroAuthEndpoint), "/")
	payload := map[string]string{"refreshToken": account.GetKiroRefreshToken()}
	userAgent := "cli-proxy-api/1.0.0"
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "kiro-cli") {
		userAgent = "Kiro-CLI"
	}
	return postKiroTokenRefresh(ctx, account, endpoint+"/refreshToken", payload, userAgent)
}

func postKiroTokenRefresh(ctx context.Context, account *Account, targetURL string, payload map[string]string, userAgent string) (*kiroTokenRefreshResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", userAgent)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               30 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro token refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out kiroTokenRefreshResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

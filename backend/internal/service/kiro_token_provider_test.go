//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

type kiroTokenProviderRepo struct {
	mockAccountRepoForGemini
	setErrorCalls int
	setErrorID    int64
	setErrorMsg   string
}

func (r *kiroTokenProviderRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

type kiroTokenProviderSequenceRepo struct {
	kiroTokenProviderRepo
	accounts []*Account
	reads    int
}

func (r *kiroTokenProviderSequenceRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	if len(r.accounts) == 0 {
		return nil, errors.New("account not found")
	}
	idx := r.reads
	if idx >= len(r.accounts) {
		idx = len(r.accounts) - 1
	}
	r.reads++
	return r.accounts[idx], nil
}

type stubKiroAccountTokenRefresher struct {
	tokenInfo *KiroTokenInfo
	err       error
}

func (s *stubKiroAccountTokenRefresher) RefreshAccountToken(context.Context, *Account) (*KiroTokenInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.tokenInfo, nil
}

func (s *stubKiroAccountTokenRefresher) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	creds := map[string]any{
		"access_token":  tokenInfo.AccessToken,
		"refresh_token": tokenInfo.RefreshToken,
		"expires_at":    tokenInfo.ExpiresAt,
	}
	if tokenInfo.ProfileArn != "" {
		creds["profile_arn"] = tokenInfo.ProfileArn
	}
	return creds
}

func TestKiroTokenProviderGetAccessTokenReturnsRefreshedToken(t *testing.T) {
	past := time.Now().Add(-time.Minute).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	account := &Account{
		ID:       88,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
			"expires_at":    past,
		},
	}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials: map[string]any{
			"access_token":  "new-access",
			"refresh_token": "rotated-refresh",
			"expires_at":    future,
		},
	}
	api := NewOAuthRefreshAPI(repo, cache)
	provider := NewKiroTokenProvider(repo, cache, nil)
	provider.SetRefreshAPI(api, executor)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "new-access", account.GetCredential("access_token"))
	require.Equal(t, "rotated-refresh", account.GetCredential("refresh_token"))
	require.Equal(t, kiropkg.BuildMachineID("old-refresh", "", "account:88"), account.GetCredential("machine_id"))
	require.Equal(t, 1, executor.refreshCalls)
}

func TestKiroTokenProviderForceRefreshInvalidGrantSetsError(t *testing.T) {
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "old-refresh"},
	}
	repo := &kiroTokenProviderRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{account.ID: account},
		},
	}
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &stubKiroAccountTokenRefresher{err: errors.New("invalid_grant: token revoked")}

	token, err := provider.ForceRefreshAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, token)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, account.ID, repo.setErrorID)
	require.Contains(t, repo.setErrorMsg, "Token refresh failed (non-retryable)")
	require.Contains(t, repo.setErrorMsg, "invalid_grant")
}

func TestKiroTokenProviderForceRefreshPreservesMachineIDAcrossRefreshRotation(t *testing.T) {
	account := &Account{
		ID:       43,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
		},
	}
	repo := &kiroTokenProviderRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{account.ID: account},
		},
	}
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &stubKiroAccountTokenRefresher{
		tokenInfo: &KiroTokenInfo{
			AccessToken:  "new-access",
			RefreshToken: "rotated-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}

	token, err := provider.ForceRefreshAccessToken(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "rotated-refresh", account.GetCredential("refresh_token"))
	require.Equal(t, kiropkg.BuildMachineID("old-refresh", "", "account:43"), account.GetCredential("machine_id"))
}

func TestKiroTokenRefresherPreservesMachineIDAcrossRefreshRotation(t *testing.T) {
	account := &Account{
		ID:       44,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
		},
	}
	refresher := &KiroTokenRefresher{
		kiroOAuthService: &KiroOAuthService{},
	}
	tokenInfo := &KiroTokenInfo{
		AccessToken:  "new-access",
		RefreshToken: "rotated-refresh",
		ExpiresAt:    time.Now().Add(time.Hour).Format(time.RFC3339),
	}

	newCredentials := mergeKiroCredentialsWithStableMachineID(account, refresher.kiroOAuthService.BuildAccountCredentials(tokenInfo))

	require.Equal(t, "rotated-refresh", newCredentials["refresh_token"])
	require.Equal(t, kiropkg.BuildMachineID("old-refresh", "", "account:44"), newCredentials["machine_id"])
}

func TestKiroTokenProviderForceRefreshRaceRecoveryDoesNotSetError(t *testing.T) {
	usedAccount := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "old-refresh"},
	}
	latestAccount := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "new-refresh", "access_token": "fresh-access", "_token_version": int64(2)},
	}
	repo := &kiroTokenProviderSequenceRepo{accounts: []*Account{usedAccount, latestAccount}}
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &stubKiroAccountTokenRefresher{err: errors.New("invalid_grant: token revoked")}

	token, err := provider.ForceRefreshAccessToken(context.Background(), usedAccount)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.Equal(t, 0, repo.setErrorCalls)
}

// TestKiroTokenCacheKeyIsolatesExternalIDPAccountsSharingClientID reproduces a
// production token-cache collision: two different external_idp accounts that
// authenticated against the SAME IdP app registration share the same client_id
// (and have an empty client_id_hash), yet are distinct people with distinct
// refresh tokens. The access-token cache key MUST NOT collapse them into one
// slot, otherwise one account's Bearer token is served for the other.
func TestKiroTokenCacheKeyIsolatesExternalIDPAccountsSharingClientID(t *testing.T) {
	sharedClientID := "e491fadf-0239-44f9-be3b-d3e1ff193c79"
	accountA := &Account{
		ID:       8,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method":   "external_idp",
			"client_id":     sharedClientID,
			"refresh_token": "refresh-token-belonging-to-account-a",
		},
	}
	accountE := &Account{
		ID:       12,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method":   "external_idp",
			"client_id":     sharedClientID,
			"refresh_token": "refresh-token-belonging-to-account-e",
		},
	}

	keyA := KiroTokenCacheKey(accountA)
	keyE := KiroTokenCacheKey(accountE)

	require.NotEqual(t, keyA, keyE,
		"two distinct external_idp accounts sharing the same IdP client_id must not share a token cache key")
}

// TestKiroTokenCacheKeyIsStablePerAccount guarantees the key is deterministic
// for a given account across calls (so cache hits still work within one account).
func TestKiroTokenCacheKeyIsStablePerAccount(t *testing.T) {
	account := &Account{
		ID:       12,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method":   "external_idp",
			"client_id":     "e491fadf-0239-44f9-be3b-d3e1ff193c79",
			"refresh_token": "refresh-token-belonging-to-account-e",
		},
	}

	require.Equal(t, KiroTokenCacheKey(account), KiroTokenCacheKey(account))
}

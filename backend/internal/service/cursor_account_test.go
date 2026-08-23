package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

const (
	cursorWebJWT    = "header.eyJ0eXBlIjoid2ViIn0.signature"
	cursorClientJWT = "header.eyJ0eXBlIjoic2Vzc2lvbiJ9.signature"
)

func TestCursorAccountSemanticsAndCredentialSources(t *testing.T) {
	var nilAccount *Account
	require.False(t, nilAccount.IsCursor())
	require.False(t, nilAccount.IsCursorOAuth())

	account := &Account{
		Platform: PlatformCursor,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":      cursorClientJWT,
			"refresh_token":     "refresh-token",
			"api_key":           "crsr_exchange-source",
			"web_session_token": cursorWebJWT,
		},
	}
	require.True(t, account.IsCursor())
	require.True(t, account.IsCursorOAuth())
	require.Equal(t, cursorClientJWT, account.GetCursorAccessToken())
	require.Equal(t, "refresh-token", account.GetCursorRefreshToken())
	require.Equal(t, "crsr_exchange-source", account.GetCursorAPIKey())
	require.Equal(t, cursorWebJWT, account.GetCursorWebSessionToken(), "dedicated browser cookie must take precedence")

	apiKeyOnly := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{"api_key": "crsr_only"}}
	require.Empty(t, apiKeyOnly.GetCursorAccessToken(), "crsr_ keys are exchange sources, never upstream bearers")
	require.Equal(t, "crsr_only", apiKeyOnly.GetCursorAPIKey())
}

func TestCursorWebSessionFallbackUsesOnlyWebAccessToken(t *testing.T) {
	legacy := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": cursorWebJWT}}
	require.Equal(t, cursorWebJWT, legacy.GetCursorWebSessionToken())

	client := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": cursorClientJWT}}
	require.Empty(t, client.GetCursorWebSessionToken())

	nonCursor := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": cursorWebJWT, "refresh_token": "refresh", "api_key": "crsr_wrong-platform", "web_session_token": cursorWebJWT,
	}}
	require.Empty(t, nonCursor.GetCursorAccessToken())
	require.Empty(t, nonCursor.GetCursorRefreshToken())
	require.Empty(t, nonCursor.GetCursorAPIKey())
	require.Empty(t, nonCursor.GetCursorWebSessionToken())
}

func TestCursorAccountBaseURLDefaultsToOfficialUpstream(t *testing.T) {
	require.Equal(t, "https://api2.cursor.sh", (&Account{Platform: PlatformCursor}).GetCursorBaseURL())
	require.Equal(t, "https://cursor-relay.example/v1", (&Account{
		Platform:    PlatformCursor,
		Credentials: map[string]any{"base_url": " https://cursor-relay.example/v1/// "},
	}).GetCursorBaseURL())
	require.Empty(t, (&Account{Platform: PlatformGrok}).GetCursorBaseURL())
}

func TestCursorAccountModelMappingDefaultsToFallbackIdentity(t *testing.T) {
	want := map[string]string{
		"auto": "auto", "cursor-small": "cursor-small", "composer-2.5": "composer-2.5", "composer-2.5-fast": "composer-2.5-fast",
		"claude-4.5-sonnet": "claude-4.5-sonnet", "claude-4.6-sonnet": "claude-4.6-sonnet", "claude-opus-4.8": "claude-opus-4.8",
		"gpt-5": "gpt-5", "gpt-5.6-sol": "gpt-5.6-sol", "gemini-3-pro": "gemini-3-pro", "gemini-3.5-flash": "gemini-3.5-flash",
		"deepseek-v3.1": "deepseek-v3.1", "grok-4.6": "grok-4.6",
	}
	account := &Account{Platform: PlatformCursor}
	require.False(t, account.HasExplicitModelMapping())
	require.Equal(t, want, account.GetModelMapping())
}

func TestCursorAccountExplicitModelMappingOverridesFallback(t *testing.T) {
	account := &Account{Platform: PlatformCursor, Credentials: map[string]any{
		"model_mapping": map[string]any{"public-model": "cursor-upstream-model"},
	}}
	require.True(t, account.HasExplicitModelMapping())
	require.Equal(t, map[string]string{"public-model": "cursor-upstream-model"}, account.GetModelMapping())
}

func TestCursorAccountEndpointCapabilitiesAreTextOnly(t *testing.T) {
	account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth}
	require.True(t, account.IsOpenAICompatible())
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))
}

func TestSensitiveCredentialCursorWebSessionIsRedactedAndPreserved(t *testing.T) {
	require.True(t, IsSensitiveCredentialKey("web_session_token"))
	merged := MergePreservingSensitiveCreds(
		map[string]any{"web_session_token": cursorWebJWT},
		map[string]any{"model_mapping": map[string]any{"auto": "auto"}},
	)
	require.Equal(t, cursorWebJWT, merged["web_session_token"])
}

var cursorDisallowedAccountTypes = []string{
	AccountTypeAPIKey,
	AccountTypeSetupToken,
	AccountTypeUpstream,
	AccountTypeBedrock,
	AccountTypeServiceAccount,
}

func requireCursorOAuthOnlyAccountError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "CURSOR_ACCOUNT_TYPE_UNSUPPORTED", infraerrors.Reason(err))
	require.Equal(t, "cursor accounts must use the oauth account type", infraerrors.Message(err))
}

func TestCursorAccountTypeValidatorRequiresExactlyOAuth(t *testing.T) {
	for _, accountType := range append(cursorDisallowedAccountTypes, "") {
		t.Run("cursor_"+accountType, func(t *testing.T) {
			requireCursorOAuthOnlyAccountError(t, validateCursorAccountType(PlatformCursor, accountType))
		})
	}
	require.NoError(t, validateCursorAccountType(PlatformCursor, AccountTypeOAuth))
	for _, accountType := range append(cursorDisallowedAccountTypes, AccountTypeOAuth, "") {
		t.Run("non_cursor_"+accountType, func(t *testing.T) {
			require.NoError(t, validateCursorAccountType(PlatformGrok, accountType))
		})
	}
}

type cursorAccountValidationRepo struct {
	AccountRepository
	account *Account
	writes  int
	binds   int
	clears  int
}

func (r *cursorAccountValidationRepo) Create(_ context.Context, account *Account) error {
	r.writes++
	r.account = account
	return nil
}

func (r *cursorAccountValidationRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *cursorAccountValidationRepo) Update(_ context.Context, account *Account) error {
	r.writes++
	r.account = account
	return nil
}

func (r *cursorAccountValidationRepo) BindGroups(_ context.Context, _ int64, _ []int64) error {
	r.binds++
	return nil
}

func (r *cursorAccountValidationRepo) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	if r.account == nil || r.account.Extra == nil || r.account.Extra[key] != value {
		return nil, nil
	}
	return []Account{*r.account}, nil
}

func (r *cursorAccountValidationRepo) ClearError(_ context.Context, _ int64) error {
	r.writes++
	r.clears++
	r.account.Status = StatusActive
	r.account.ErrorMessage = ""
	return nil
}

func (r *cursorAccountValidationRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func (r *cursorAccountValidationRepo) CreateWithAccountGroups(_ context.Context, account *Account, _ []AccountGroup) error {
	r.writes++
	r.account = account
	return nil
}

func TestCursorAccountCreateRejectsEveryNonOAuthTypeBeforeWrite(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run("admin_"+accountType, func(t *testing.T) {
			repo := &cursorAccountValidationRepo{}
			_, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
				Platform: PlatformCursor, Type: accountType, SkipDefaultGroupBind: true,
			})
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
		})

		t.Run("account_service_"+accountType, func(t *testing.T) {
			repo := &cursorAccountValidationRepo{}
			_, err := NewAccountService(repo, nil).Create(context.Background(), CreateAccountRequest{
				Platform: PlatformCursor, Type: accountType,
			})
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
		})
	}
}

func TestAdminCursorAccountUpdateRejectsEveryNonOAuthTypeBeforeMutation(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			account := &Account{ID: 42, Name: "cursor", Platform: PlatformCursor, Type: AccountTypeOAuth}
			repo := &cursorAccountValidationRepo{account: account}
			_, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 42, &UpdateAccountInput{
				Name: "must-not-persist", Type: accountType,
			})
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
			require.Equal(t, "cursor", repo.account.Name)
			require.Equal(t, AccountTypeOAuth, repo.account.Type)
		})
	}
}

func TestCursorAccountUpdateWithoutTypePreservesValidOAuth(t *testing.T) {
	t.Run("admin", func(t *testing.T) {
		repo := &cursorAccountValidationRepo{account: &Account{
			ID: 43, Name: "cursor", Platform: PlatformCursor, Type: AccountTypeOAuth,
		}}
		updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 43, &UpdateAccountInput{Name: "renamed"})
		require.NoError(t, err)
		require.Equal(t, AccountTypeOAuth, updated.Type)
		require.Equal(t, "renamed", updated.Name)
		require.Equal(t, 1, repo.writes)
	})

	t.Run("account service", func(t *testing.T) {
		repo := &cursorAccountValidationRepo{account: &Account{
			ID: 44, Name: "cursor", Platform: PlatformCursor, Type: AccountTypeOAuth,
		}}
		name := "renamed"
		updated, err := NewAccountService(repo, nil).Update(context.Background(), 44, UpdateAccountRequest{Name: &name})
		require.NoError(t, err)
		require.Equal(t, AccountTypeOAuth, updated.Type)
		require.Equal(t, "renamed", updated.Name)
		require.Equal(t, 1, repo.writes)
	})
}

func TestCursorAccountServiceUpdateRejectsLegacyNonOAuthRowsBeforeMutation(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			repo := &cursorAccountValidationRepo{account: &Account{
				ID: 45, Name: "legacy", Platform: PlatformCursor, Type: accountType,
			}}
			name := "must-not-persist"
			_, err := NewAccountService(repo, nil).Update(context.Background(), 45, UpdateAccountRequest{Name: &name})
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
			require.Equal(t, "legacy", repo.account.Name)
		})
	}
}

func TestAdminCursorAccountUpdateWithoutTypeRejectsLegacyNonOAuthRowsBeforeMutation(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			repo := &cursorAccountValidationRepo{account: &Account{
				ID: 46, Name: "legacy", Platform: PlatformCursor, Type: accountType,
			}}
			_, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 46, &UpdateAccountInput{
				Name: "must-not-persist",
			})
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
			require.Equal(t, "legacy", repo.account.Name)
		})
	}
}

type cursorOAuthOnlyGroupRepo struct {
	GroupRepository
	group *Group
}

func (r *cursorOAuthOnlyGroupRepo) GetByID(_ context.Context, _ int64) (*Group, error) {
	return r.group, nil
}

func TestCursorGroupOAuthOnlyRejectsAPIKeyBindingsBeforeBindGroups(t *testing.T) {
	const groupID int64 = 73
	for _, groupPlatform := range []string{PlatformCursor, PlatformKiro} {
		t.Run(groupPlatform, func(t *testing.T) {
			groups := &cursorOAuthOnlyGroupRepo{group: &Group{
				ID: groupID, Name: groupPlatform + "-oauth-only", Platform: groupPlatform, RequireOAuthOnly: true,
			}}

			t.Run("create", func(t *testing.T) {
				repo := &cursorAccountValidationRepo{}
				_, err := NewAccountService(repo, groups).Create(context.Background(), CreateAccountRequest{
					Platform: PlatformGrok, Type: AccountTypeAPIKey, GroupIDs: []int64{groupID},
				})
				require.ErrorContains(t, err, "仅允许 OAuth 账号")
				require.Equal(t, 1, repo.writes, "preserve current write ordering")
				require.Zero(t, repo.binds)
			})

			t.Run("update legacy cursor api key", func(t *testing.T) {
				repo := &cursorAccountValidationRepo{account: &Account{
					ID: 74, Platform: PlatformCursor, Type: AccountTypeAPIKey,
				}}
				groupIDs := []int64{groupID}
				_, err := NewAccountService(repo, groups).Update(context.Background(), repo.account.ID, UpdateAccountRequest{GroupIDs: &groupIDs})
				requireCursorOAuthOnlyAccountError(t, err)
				require.Zero(t, repo.writes)
				require.Zero(t, repo.binds)
			})
		})
	}
}

func TestDuplicateRejectsEveryLegacyCursorNonOAuthAccountBeforeWrite(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			repo := &cursorAccountValidationRepo{account: &Account{
				ID: 77, Name: "legacy-cursor", Platform: PlatformCursor, Type: accountType,
				Credentials: map[string]any{"api_key": "crsr_legacy"},
			}}

			duplicate, err := (&adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}).DuplicateAccount(
				context.Background(), 77, "admin:1", "",
			)

			require.Nil(t, duplicate)
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.binds)
		})
	}
}

func TestRecoverDuplicateRejectsEveryLegacyCursorNonOAuthAccount(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			operationID := duplicateAccountOperationID(78, "admin:1", "retry-key")
			repo := &cursorAccountValidationRepo{account: &Account{
				ID: 79, Platform: PlatformCursor, Type: accountType,
				Extra: map[string]any{duplicateAccountOperationIDExtraKey: operationID},
			}}

			recovered, err := (&adminServiceImpl{accountRepo: repo}).RecoverDuplicateAccount(
				context.Background(), 78, "admin:1", "retry-key",
			)

			require.Nil(t, recovered)
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
		})
	}
}

func TestLegacyCursorNonOAuthAccountsCannotRecoverOrBecomeSchedulable(t *testing.T) {
	for _, accountType := range cursorDisallowedAccountTypes {
		t.Run(accountType, func(t *testing.T) {
			account := &Account{
				ID: 80, Platform: PlatformCursor, Type: accountType,
				Status: StatusError, Schedulable: true, ErrorMessage: "recoverable",
			}
			repo := &cursorAccountValidationRepo{account: account}
			result, err := (&RateLimitService{accountRepo: repo}).RecoverAccountState(
				context.Background(), account.ID, AccountRecoveryOptions{},
			)

			require.Nil(t, result)
			requireCursorOAuthOnlyAccountError(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, repo.clears)
			require.Equal(t, StatusError, account.Status)

			account.Status = StatusActive
			require.False(t, account.IsSchedulable())
		})
	}

	require.True(t, (&Account{
		Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
	}).IsSchedulable())
	for _, accountType := range append(cursorDisallowedAccountTypes, AccountTypeOAuth) {
		require.True(t, (&Account{
			Platform: PlatformGrok, Type: accountType, Status: StatusActive, Schedulable: true,
		}).IsSchedulable(), "non-Cursor type %q must retain schedulability", accountType)
	}
}

func TestMisplacedCursorAPIKeyCannotBecomeAccessBearer(t *testing.T) {
	direct := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": " crsr_misplaced "}}
	require.Empty(t, direct.GetCursorAccessToken())

	assertNormalized := func(t *testing.T, account *Account) {
		t.Helper()
		require.NotNil(t, account)
		require.NotContains(t, account.Credentials, "access_token")
		require.Equal(t, "crsr_misplaced", account.Credentials["api_key"])
		require.Empty(t, account.GetCursorAccessToken())
		require.Equal(t, "crsr_misplaced", account.GetCursorAPIKey())
	}

	t.Run("admin create", func(t *testing.T) {
		repo := &cursorAccountValidationRepo{}
		account, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
			Platform: PlatformCursor, Type: AccountTypeOAuth, SkipDefaultGroupBind: true,
			Credentials: map[string]any{"access_token": " crsr_misplaced "},
		})
		require.NoError(t, err)
		require.Equal(t, 1, repo.writes)
		assertNormalized(t, account)
	})

	t.Run("account service create", func(t *testing.T) {
		repo := &cursorAccountValidationRepo{}
		account, err := NewAccountService(repo, nil).Create(context.Background(), CreateAccountRequest{
			Platform: PlatformCursor, Type: AccountTypeOAuth,
			Credentials: map[string]any{"access_token": " crsr_misplaced "},
		})
		require.NoError(t, err)
		require.Equal(t, 1, repo.writes)
		assertNormalized(t, account)
	})

	t.Run("explicit api key wins normalization", func(t *testing.T) {
		credentials := SanitizeStoredCredentials(PlatformCursor, map[string]any{
			"access_token": "crsr_misplaced",
			"api_key":      "crsr_explicit",
		})
		require.NotContains(t, credentials, "access_token")
		require.Equal(t, "crsr_explicit", credentials["api_key"])
	})
}

func TestCursorAccountActiveUsageIsRejectedWithoutBearerForwarding(t *testing.T) {
	cache := NewUsageCache()
	cache.apiCache.Store(int64(91), &apiUsageCache{response: &ClaudeUsageResponse{}, timestamp: time.Now()})
	svc := &AccountUsageService{cache: cache}

	usage, err := svc.GetUsageForAccount(context.Background(), &Account{
		ID: 91, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": cursorClientJWT},
	})
	require.Nil(t, usage)
	require.EqualError(t, err, "cursor accounts do not support active usage query; only passive usage statistics are available")
}

func TestCursorAccountDoesNotChangeNonCursorBehavior(t *testing.T) {
	grok := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "grok-access"}}
	require.True(t, grok.IsOpenAICompatible())
	require.True(t, grok.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.Equal(t, "grok-access", grok.GetGrokAccessToken())

	openAI := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "openai-access"}}
	require.Equal(t, "openai-access", openAI.GetOpenAIAccessToken())
}

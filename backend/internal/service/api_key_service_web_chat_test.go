package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type ensureWebChatKeyRepoStub struct {
	APIKeyRepository
	created      *APIKey
	createErr    error
	getResults   []ensureWebChatKeyGetResult
	getCallCount int
}

type ensureWebChatKeyGetResult struct {
	key *APIKey
	err error
}

func (s *ensureWebChatKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	clone := *key
	s.created = &clone
	return s.createErr
}

func (s *ensureWebChatKeyRepoStub) GetWebChatKeyByUserAndGroup(_ context.Context, userID, groupID int64) (*APIKey, error) {
	s.getCallCount++
	if len(s.getResults) > 0 {
		result := s.getResults[0]
		s.getResults = s.getResults[1:]
		return result.key, result.err
	}
	return nil, ErrAPIKeyNotFound
}

func TestEnsureWebChatKey_CreatesHiddenStaticKey(t *testing.T) {
	user := &User{ID: 42, Status: StatusActive}
	group := &Group{ID: 30, Status: StatusActive}
	apiKeyRepo := &ensureWebChatKeyRepoStub{}
	svc := &APIKeyService{apiKeyRepo: apiKeyRepo}

	apiKey, err := svc.EnsureWebChatKey(context.Background(), user, group)

	require.NoError(t, err)
	require.NotNil(t, apiKey)
	require.True(t, strings.HasPrefix(apiKey.Key, "wc_"))
	require.Equal(t, "Web Chat", apiKey.Name)
	require.Equal(t, APIKeyTypeWebChat, apiKey.KeyType)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, group.ID, *apiKey.GroupID)
	require.Equal(t, APIKeyGroupBindingModeStatic, apiKey.GroupBindingMode)
	require.Equal(t, StatusActive, apiKey.Status)
	require.Zero(t, apiKey.Quota)
	require.Zero(t, apiKey.RateLimit5h)
	require.Zero(t, apiKey.RateLimit1d)
	require.Zero(t, apiKey.RateLimit7d)
	require.Same(t, user, apiKey.User)
	require.Same(t, group, apiKey.Group)

	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, apiKey.Key, apiKeyRepo.created.Key)
	require.Equal(t, APIKeyTypeWebChat, apiKeyRepo.created.KeyType)
	require.NotNil(t, apiKeyRepo.created.GroupID)
	require.Equal(t, group.ID, *apiKeyRepo.created.GroupID)
	require.Equal(t, APIKeyGroupBindingModeStatic, apiKeyRepo.created.GroupBindingMode)
}

func TestEnsureWebChatKey_ReusesExistingKeyAfterConcurrentCreate(t *testing.T) {
	user := &User{ID: 42, Status: StatusActive}
	group := &Group{ID: 30, Status: StatusActive}
	existing := &APIKey{
		ID:      99,
		UserID:  user.ID,
		Key:     "wc-existing",
		Name:    "Web Chat",
		KeyType: APIKeyTypeWebChat,
		GroupID: &group.ID,
		Status:  StatusActive,
	}
	apiKeyRepo := &ensureWebChatKeyRepoStub{
		createErr: ErrAPIKeyExists,
		getResults: []ensureWebChatKeyGetResult{
			{err: ErrAPIKeyNotFound},
			{key: existing},
		},
	}
	svc := &APIKeyService{apiKeyRepo: apiKeyRepo}

	apiKey, err := svc.EnsureWebChatKey(context.Background(), user, group)

	require.NoError(t, err)
	require.Same(t, existing, apiKey)
	require.Same(t, user, apiKey.User)
	require.Same(t, group, apiKey.Group)
	require.Equal(t, 2, apiKeyRepo.getCallCount)
	require.NotNil(t, apiKeyRepo.created)
	require.True(t, strings.HasPrefix(apiKeyRepo.created.Key, "wc_"))
	require.Equal(t, APIKeyTypeWebChat, apiKeyRepo.created.KeyType)
}

type webChatAuthRepoStub struct {
	APIKeyRepository
	key                  *APIKey
	updateCalls          int
	deleteWithAuditCalls int
}

func (s *webChatAuthRepoStub) GetByID(_ context.Context, _ int64) (*APIKey, error) {
	if s.key == nil {
		return nil, ErrAPIKeyNotFound
	}
	clone := *s.key
	return &clone, nil
}

func (s *webChatAuthRepoStub) GetKeyAndOwnerID(_ context.Context, _ int64) (string, int64, error) {
	if s.key == nil {
		return "", 0, ErrAPIKeyNotFound
	}
	return s.key.Key, s.key.UserID, nil
}

func (s *webChatAuthRepoStub) GetByKeyForAuth(_ context.Context, key string) (*APIKey, error) {
	if s.key == nil {
		return nil, ErrAPIKeyNotFound
	}
	clone := *s.key
	clone.Key = key
	return &clone, nil
}

func (s *webChatAuthRepoStub) Update(_ context.Context, _ *APIKey) error {
	s.updateCalls++
	return nil
}

func (s *webChatAuthRepoStub) DeleteWithAudit(_ context.Context, _ int64) error {
	s.deleteWithAuditCalls++
	return nil
}

type webChatAuthCacheStub struct {
	setAuthKeys    []string
	deleteAuthKeys []string
	deleteAttempts []int64
}

func (s *webChatAuthCacheStub) GetCreateAttemptCount(context.Context, int64) (int, error) {
	return 0, nil
}

func (s *webChatAuthCacheStub) IncrementCreateAttemptCount(context.Context, int64) error {
	return nil
}

func (s *webChatAuthCacheStub) DeleteCreateAttemptCount(_ context.Context, userID int64) error {
	s.deleteAttempts = append(s.deleteAttempts, userID)
	return nil
}

func (s *webChatAuthCacheStub) IncrementDailyUsage(context.Context, string) error {
	return nil
}

func (s *webChatAuthCacheStub) SetDailyUsageExpiry(context.Context, string, time.Duration) error {
	return nil
}

func (s *webChatAuthCacheStub) GetAuthCache(context.Context, string) (*APIKeyAuthCacheEntry, error) {
	return nil, errors.New("cache miss")
}

func (s *webChatAuthCacheStub) SetAuthCache(_ context.Context, key string, _ *APIKeyAuthCacheEntry, _ time.Duration) error {
	s.setAuthKeys = append(s.setAuthKeys, key)
	return nil
}

func (s *webChatAuthCacheStub) DeleteAuthCache(_ context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

func (s *webChatAuthCacheStub) PublishAuthCacheInvalidation(context.Context, string) error {
	return nil
}

func (s *webChatAuthCacheStub) SubscribeAuthCacheInvalidation(context.Context, func(string)) error {
	return nil
}

type webChatAuthCacheInvalidatorStub struct {
	keys     []string
	userIDs  []int64
	groupIDs []int64
}

func (s *webChatAuthCacheInvalidatorStub) InvalidateAuthCacheByKey(_ context.Context, key string) {
	s.keys = append(s.keys, key)
}

func (s *webChatAuthCacheInvalidatorStub) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

func (s *webChatAuthCacheInvalidatorStub) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
}

func newWebChatAPIKey() *APIKey {
	return &APIKey{
		ID:      99,
		UserID:  42,
		Key:     "wc-hidden",
		Name:    "Web Chat",
		KeyType: APIKeyTypeWebChat,
		Status:  StatusActive,
		User: &User{
			ID:          42,
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 1,
		},
	}
}

func TestAPIKeyService_GetByKey_RejectsWebChatKeyWithoutAuthCacheWrites(t *testing.T) {
	cache := &webChatAuthCacheStub{}
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:             1000,
			L1TTLSeconds:       60,
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	})
	require.NotNil(t, svc.authCacheL1)

	_, err := svc.GetByKey(context.Background(), "wc-cache-hidden")

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, cache.setAuthKeys)
	svc.authCacheL1.Wait()
	_, ok := svc.authCacheL1.Get(svc.authCacheKey("wc-cache-hidden"))
	require.False(t, ok)
}

func TestAPIKeyService_GetByID_HidesWebChatKey(t *testing.T) {
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	svc := &APIKeyService{apiKeyRepo: repo}

	apiKey, err := svc.GetByID(context.Background(), 99)

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Nil(t, apiKey)
}

func TestAPIKeyService_Update_RejectsWebChatKey(t *testing.T) {
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	svc := &APIKeyService{apiKeyRepo: repo}
	name := "new name"

	apiKey, err := svc.Update(context.Background(), 99, 42, UpdateAPIKeyRequest{Name: &name})

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Nil(t, apiKey)
	require.Zero(t, repo.updateCalls)
}

func TestAPIKeyService_Delete_RejectsWebChatKey(t *testing.T) {
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	cache := &webChatAuthCacheStub{}
	svc := &APIKeyService{apiKeyRepo: repo, cache: cache}

	err := svc.Delete(context.Background(), 99, 42)

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Zero(t, repo.deleteWithAuditCalls)
	require.Empty(t, cache.deleteAttempts)
	require.Empty(t, cache.deleteAuthKeys)
}

func TestAdminUpdateAPIKeyGroupID_WebChatKeyHidden(t *testing.T) {
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	invalidator := &webChatAuthCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: repo, authCacheInvalidator: invalidator}
	groupID := int64(0)

	result, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 99, &groupID)

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Nil(t, result)
	require.Zero(t, repo.updateCalls)
	require.Empty(t, invalidator.keys)
}

func TestAdminResetAPIKeyRateLimitUsage_WebChatKeyHidden(t *testing.T) {
	repo := &webChatAuthRepoStub{key: newWebChatAPIKey()}
	invalidator := &webChatAuthCacheInvalidatorStub{}
	svc := &adminServiceImpl{apiKeyRepo: repo, authCacheInvalidator: invalidator}

	apiKey, err := svc.AdminResetAPIKeyRateLimitUsage(context.Background(), 99)

	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Nil(t, apiKey)
	require.Zero(t, repo.updateCalls)
	require.Empty(t, invalidator.keys)
}

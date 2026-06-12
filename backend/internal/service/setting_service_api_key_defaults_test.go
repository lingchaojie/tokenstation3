//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type memorySettingRepo struct {
	values map[string]string
}

func newMemorySettingRepo(values map[string]string) *memorySettingRepo {
	if values == nil {
		values = map[string]string{}
	}
	return &memorySettingRepo{values: values}
}

func (r *memorySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *memorySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *memorySettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		out[key] = r.values[key]
	}
	return out, nil
}

func (r *memorySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *memorySettingRepo) GetAll(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *memorySettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type apiKeyDefaultGroupReaderStub struct {
	groups map[int64]*Group
}

func (s apiKeyDefaultGroupReaderStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if g := s.groups[id]; g != nil {
		return g, nil
	}
	return nil, ErrGroupNotFound
}

func TestSettingService_UpdateSettings_DefaultAPIKeyGroups_ValidatesPlatform(t *testing.T) {
	repo := newMemorySettingRepo(map[string]string{})
	svc := NewSettingService(repo, nil)
	svc.SetDefaultSubscriptionGroupReader(apiKeyDefaultGroupReaderStub{groups: map[int64]*Group{
		10: {ID: 10, Platform: PlatformAnthropic, Status: StatusActive},
		20: {ID: 20, Platform: PlatformOpenAI, Status: StatusActive},
	}})

	anthropicID := int64(10)
	openAIID := int64(20)
	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultAnthropicGroupID: &anthropicID,
		DefaultOpenAIGroupID:    &openAIID,
	})

	require.NoError(t, err)
	require.Equal(t, "10", repo.values[SettingKeyDefaultAnthropicGroupID])
	require.Equal(t, "20", repo.values[SettingKeyDefaultOpenAIGroupID])
}

func TestSettingService_UpdateSettings_DefaultAPIKeyGroups_RejectsPlatformMismatch(t *testing.T) {
	repo := newMemorySettingRepo(map[string]string{})
	svc := NewSettingService(repo, nil)
	svc.SetDefaultSubscriptionGroupReader(apiKeyDefaultGroupReaderStub{groups: map[int64]*Group{
		10: {ID: 10, Platform: PlatformAnthropic, Status: StatusActive},
	}})

	openAIID := int64(10)
	err := svc.UpdateSettings(context.Background(), &SystemSettings{DefaultOpenAIGroupID: &openAIID})

	require.Error(t, err)
}

func TestSettingService_GetDefaultAPIKeyGroupID(t *testing.T) {
	repo := newMemorySettingRepo(map[string]string{
		SettingKeyDefaultAnthropicGroupID: "10",
		SettingKeyDefaultOpenAIGroupID:    "20",
	})
	svc := NewSettingService(repo, nil)

	anthropicID, err := svc.GetDefaultAPIKeyGroupID(context.Background(), APIKeyTypeAnthropic)
	require.NoError(t, err)
	require.NotNil(t, anthropicID)
	require.Equal(t, int64(10), *anthropicID)

	openAIID, err := svc.GetDefaultAPIKeyGroupID(context.Background(), APIKeyTypeOpenAI)
	require.NoError(t, err)
	require.NotNil(t, openAIID)
	require.Equal(t, int64(20), *openAIID)
}

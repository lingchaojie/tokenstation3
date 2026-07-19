//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type settingBrandingRepoStub struct {
	values map[string]string
}

func (s *settingBrandingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingBrandingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (s *settingBrandingRepoStub) Set(ctx context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s *settingBrandingRepoStub) SetIfAbsent(ctx context.Context, key, value string) error {
	if _, exists := s.values[key]; !exists {
		s.values[key] = value
	}
	return nil
}

func (s *settingBrandingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingBrandingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}

func (s *settingBrandingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingBrandingRepoStub) Delete(ctx context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestSettingService_GetPublicSettings_UsesLINX2AIBrandingDefaults(t *testing.T) {
	svc := NewSettingService(&settingBrandingRepoStub{values: map[string]string{}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "LINX2.AI", settings.SiteName)
	require.Equal(t, "Link 2 All AI Model", settings.SiteSubtitle)
}

func TestSettingService_GetSiteName_FallsBackToLINX2AIBrandingDefault(t *testing.T) {
	svc := NewSettingService(&settingBrandingRepoStub{values: map[string]string{}}, &config.Config{})

	require.Equal(t, "LINX2.AI", svc.GetSiteName(context.Background()))
}

func TestSettingService_InitializeDefaultSettings_SetsLINX2AIBrandingDefaults(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "LINX2.AI", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

func TestSettingService_InitializeDefaultSettings_MigratesLegacySub2APIBrandingDefaults(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Sub2api",
		SettingKeySiteSubtitle:        "Subscription to API Conversion Platform",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "LINX2.AI", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

func TestProvideSettingService_MigratesLegacyBrandingDefaultsOnStartup(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Sub2API",
		SettingKeySiteSubtitle:        "Subscription to API Conversion Platform",
	}}

	_, err := ProvideSettingService(repo, nil, nil, &config.Config{})

	require.NoError(t, err)
	require.Equal(t, "LINX2.AI", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

func TestSettingService_InitializeDefaultSettings_MigratesLegacyDescriptionOnly(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "LINX2.AI",
		SettingKeySiteSubtitle:        "AI Gateway Platform",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "LINX2.AI", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

func TestSettingService_InitializeDefaultSettings_MigratesErroneousLINX2SiteNameDefault(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "LINX2",
		SettingKeySiteSubtitle:        "Link 2 All AI Model",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "LINX2.AI", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

func TestSettingService_InitializeDefaultSettings_PreservesCustomBranding(t *testing.T) {
	repo := &settingBrandingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "AI Gateway Platform",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "Custom Portal", repo.values[SettingKeySiteName])
	require.Equal(t, "Link 2 All AI Model", repo.values[SettingKeySiteSubtitle])
}

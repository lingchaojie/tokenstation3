//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type alvinSettingRepoStub struct {
	SettingRepository
	values            map[string]string
	getValueErrors    map[string]error
	setErrors         map[string]error
	setIfAbsentErrors map[string]error
	beforeSetIfAbsent func()
	setMultipleError  error
}

func newAlvinSettingRepoStub(values map[string]string) *alvinSettingRepoStub {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return &alvinSettingRepoStub{
		values:            cloned,
		getValueErrors:    make(map[string]error),
		setErrors:         make(map[string]error),
		setIfAbsentErrors: make(map[string]error),
	}
}

func (r *alvinSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if err := r.getValueErrors[key]; err != nil {
		return "", err
	}
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *alvinSettingRepoStub) Set(_ context.Context, key, value string) error {
	if err := r.setErrors[key]; err != nil {
		return err
	}
	r.values[key] = value
	return nil
}

func (r *alvinSettingRepoStub) SetIfAbsent(_ context.Context, key, value string) error {
	if r.beforeSetIfAbsent != nil {
		r.beforeSetIfAbsent()
	}
	if err := r.setIfAbsentErrors[key]; err != nil {
		return err
	}
	if _, exists := r.values[key]; !exists {
		r.values[key] = value
	}
	return nil
}

func (r *alvinSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *alvinSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if r.setMultipleError != nil {
		return r.setMultipleError
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func TestSettingService_GetAlvin(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{name: "true", values: map[string]string{SettingKeyAlvin: "true"}, want: true},
		{name: "false", values: map[string]string{SettingKeyAlvin: "false"}, want: false},
		{name: "trimmed uppercase true", values: map[string]string{SettingKeyAlvin: "  TRUE  "}, want: true},
		{name: "trimmed mixed case false", values: map[string]string{SettingKeyAlvin: "  FaLsE  "}, want: false},
		{name: "missing", values: map[string]string{}, want: true},
		{name: "empty", values: map[string]string{SettingKeyAlvin: ""}, want: true},
		{name: "numeric is invalid", values: map[string]string{SettingKeyAlvin: "1"}, want: true},
		{name: "arbitrary text is invalid", values: map[string]string{SettingKeyAlvin: "disabled"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newAlvinSettingRepoStub(tt.values)
			svc := NewSettingService(repo, &config.Config{})

			got, err := svc.GetAlvin(context.Background())

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSettingService_GetAlvin_ReturnsRepositoryError(t *testing.T) {
	repo := newAlvinSettingRepoStub(nil)
	dbErr := errors.New("database unavailable")
	repo.getValueErrors[SettingKeyAlvin] = dbErr
	svc := NewSettingService(repo, &config.Config{})

	_, err := svc.GetAlvin(context.Background())

	require.ErrorIs(t, err, dbErr)
}

func TestSettingService_InitializeDefaultSettings_SeedsAlvinForNewDatabase(t *testing.T) {
	repo := newAlvinSettingRepoStub(nil)
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "true", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_BackfillsAlvinForExistingDatabase(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "Custom subtitle",
	})
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "true", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_PreservesExistingAlvin(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "Custom subtitle",
		SettingKeyAlvin:               "false",
	})
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "false", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_PreservesAlvinInPartiallyInitializedDatabase(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{SettingKeyAlvin: "false"})
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "false", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_DoesNotOverwriteConcurrentAlvinInsert(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "Custom subtitle",
	})
	repo.beforeSetIfAbsent = func() {
		repo.values[SettingKeyAlvin] = "false"
	}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "false", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_ReturnsAlvinBackfillError(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
	})
	dbErr := errors.New("alvin insert failed")
	repo.setIfAbsentErrors[SettingKeyAlvin] = dbErr
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.ErrorIs(t, err, dbErr)
}

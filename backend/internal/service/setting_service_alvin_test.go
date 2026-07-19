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
	values           map[string]string
	getValueErrors   map[string]error
	setErrors        map[string]error
	setMultipleError error
}

func newAlvinSettingRepoStub(values map[string]string) *alvinSettingRepoStub {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return &alvinSettingRepoStub{
		values:         cloned,
		getValueErrors: make(map[string]error),
		setErrors:      make(map[string]error),
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

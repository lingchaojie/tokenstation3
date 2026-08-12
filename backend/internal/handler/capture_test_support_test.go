package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type handlerCaptureSettingRepo struct {
	value string
}

func (r *handlerCaptureSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}

func (r *handlerCaptureSettingRepo) GetValue(context.Context, string) (string, error) {
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *handlerCaptureSettingRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func (*handlerCaptureSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (*handlerCaptureSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (*handlerCaptureSettingRepo) GetAll(context.Context) (map[string]string, error)    { return nil, nil }
func (*handlerCaptureSettingRepo) Delete(context.Context, string) error                 { return nil }

func newEnabledCaptureSettingService(t *testing.T, cfg *config.Config) *service.SettingService {
	t.Helper()
	settings := service.NewSettingService(&handlerCaptureSettingRepo{}, cfg)
	policy := service.DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	_, err := settings.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)
	return settings
}

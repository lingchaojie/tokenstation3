package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// PanelRateLimitSettings describes the dormant panel limiter foundation.
// No current router constructs or mounts the limiter.
type PanelRateLimitSettings struct {
	// Enabled 总开关
	Enabled bool `json:"enabled"`
	// UserRPM 每用户每分钟请求数上限（认证面板接口全量计数；0 = 不限制）
	UserRPM int `json:"user_rpm"`
	// HeavyRPM 每用户每分钟重查询上限（usage/dashboard 等聚合统计接口；0 = 不限制）
	HeavyRPM int `json:"heavy_rpm"`
	// ExemptAdmin 管理员账号是否豁免按用户限流
	ExemptAdmin bool `json:"exempt_admin"`
	// PublicIPRPM 无需认证的公开接口每 IP 每分钟上限（0 = 不限制）
	PublicIPRPM int `json:"public_ip_rpm"`
}

// 面板限流 RPM 的取值上限，防止配置异常大的值失去意义。
const panelRateLimitRPMMax = 100000

const (
	panelRateLimitCacheTTL  = 60 * time.Second
	panelRateLimitErrorTTL  = 5 * time.Second
	panelRateLimitDBTimeout = 5 * time.Second
)

// cachedPanelRateLimitSettings 进程内缓存条目（60s TTL）。
type cachedPanelRateLimitSettings struct {
	settings  PanelRateLimitSettings
	expiresAt int64 // unix nano
}

// DefaultPanelRateLimitSettings returns a dormant foundation. Rollout is an
// explicit product decision; missing, invalid, or unreadable settings must
// never silently mount a new global limit.
func DefaultPanelRateLimitSettings() *PanelRateLimitSettings {
	return &PanelRateLimitSettings{
		Enabled:     false,
		UserRPM:     240,
		HeavyRPM:    60,
		ExemptAdmin: true,
		PublicIPRPM: 300,
	}
}

// normalizePanelRateLimitSettings 修正非法取值（负数归零、超上限截断）。
func normalizePanelRateLimitSettings(s *PanelRateLimitSettings) {
	if s == nil {
		return
	}
	if s.UserRPM < 0 {
		s.UserRPM = 0
	}
	if s.HeavyRPM < 0 {
		s.HeavyRPM = 0
	}
	if s.PublicIPRPM < 0 {
		s.PublicIPRPM = 0
	}
	if s.UserRPM > panelRateLimitRPMMax {
		s.UserRPM = panelRateLimitRPMMax
	}
	if s.HeavyRPM > panelRateLimitRPMMax {
		s.HeavyRPM = panelRateLimitRPMMax
	}
	if s.PublicIPRPM > panelRateLimitRPMMax {
		s.PublicIPRPM = panelRateLimitRPMMax
	}
}

// GetPanelRateLimitSettings 获取面板 API 限流配置（直读 DB，供管理端读写路径使用）。
// 缺失/空/解析失败 → 返回默认配置。
func (s *SettingService) GetPanelRateLimitSettings(ctx context.Context) (*PanelRateLimitSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPanelRateLimitSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultPanelRateLimitSettings(), nil
		}
		return nil, fmt.Errorf("get panel rate limit settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return DefaultPanelRateLimitSettings(), nil
	}

	settings := &PanelRateLimitSettings{}
	if err := json.Unmarshal([]byte(value), settings); err != nil {
		slog.Warn("failed to unmarshal panel rate limit settings, falling back to defaults",
			"error", err, "key", SettingKeyPanelRateLimitSettings)
		return DefaultPanelRateLimitSettings(), nil
	}
	normalizePanelRateLimitSettings(settings)
	return settings, nil
}

// SetPanelRateLimitSettings persists the dormant foundation configuration and
// refreshes its cache. Persistence does not mount middleware or change routes.
func (s *SettingService) SetPanelRateLimitSettings(ctx context.Context, settings *PanelRateLimitSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if settings.UserRPM < 0 || settings.HeavyRPM < 0 || settings.PublicIPRPM < 0 {
		return fmt.Errorf("rate limit values cannot be negative")
	}
	if settings.UserRPM > panelRateLimitRPMMax || settings.HeavyRPM > panelRateLimitRPMMax || settings.PublicIPRPM > panelRateLimitRPMMax {
		return fmt.Errorf("rate limit values must be at most %d", panelRateLimitRPMMax)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal panel rate limit settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPanelRateLimitSettings, string(data)); err != nil {
		return err
	}

	s.storePanelRateLimitCache(*settings, panelRateLimitCacheTTL)
	return nil
}

// GetPanelRateLimitSettingsCached returns the dormant foundation configuration
// from a 60s in-process cache. DB errors deterministically use the disabled
// default so an expired enabled value cannot be revived.
func (s *SettingService) GetPanelRateLimitSettingsCached(ctx context.Context) PanelRateLimitSettings {
	if s == nil || s.settingRepo == nil {
		return *DefaultPanelRateLimitSettings()
	}
	if cached, ok := s.panelRateLimitCache.Load().(*cachedPanelRateLimitSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.settings
		}
	}

	result, _, _ := s.panelRateLimitSF.Do("panel_rate_limit_settings", func() (any, error) {
		// 二次检查，避免排队的 goroutine 重复查询
		if cached, ok := s.panelRateLimitCache.Load().(*cachedPanelRateLimitSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.settings, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		// 独立 context：断开请求取消链，避免客户端断连污染缓存
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), panelRateLimitDBTimeout)
		defer cancel()

		settings, err := s.GetPanelRateLimitSettings(dbCtx)
		if err != nil {
			slog.Warn("failed to get panel rate limit settings", "error", err)
			// Error is deterministic default-off; never revive a prior enabled value.
			fallback := *DefaultPanelRateLimitSettings()
			s.storePanelRateLimitCache(fallback, panelRateLimitErrorTTL)
			return fallback, nil
		}

		s.storePanelRateLimitCache(*settings, panelRateLimitCacheTTL)
		return *settings, nil
	})
	if settings, ok := result.(PanelRateLimitSettings); ok {
		return settings
	}
	return *DefaultPanelRateLimitSettings()
}

func (s *SettingService) storePanelRateLimitCache(settings PanelRateLimitSettings, ttl time.Duration) {
	s.panelRateLimitCache.Store(&cachedPanelRateLimitSettings{
		settings:  settings,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	})
}

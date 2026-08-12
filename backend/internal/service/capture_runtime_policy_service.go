package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	captureRuntimePolicyCacheTTL  = 60 * time.Second
	captureRuntimePolicyErrorTTL  = 5 * time.Second
	captureRuntimePolicyDBTimeout = 5 * time.Second
	captureRuntimePolicySFKey     = "capture_runtime_policy"
)

type cachedCaptureRuntimePolicy struct {
	policy    CaptureRuntimePolicy
	compiled  CompiledCapturePolicy
	expiresAt int64
	loadError string
}

func (s *SettingService) GetCaptureRuntimePolicy(ctx context.Context) (CaptureRuntimePolicy, error) {
	entry := s.loadCaptureRuntimePolicy(ctx)
	if entry.loadError != "" {
		return entry.policy, errors.New(entry.loadError)
	}
	return entry.policy, nil
}

func (s *SettingService) GetCompiledCaptureRuntimePolicy(ctx context.Context) CompiledCapturePolicy {
	return s.loadCaptureRuntimePolicy(ctx).compiled
}

// GetCompiledCaptureRuntimePolicyHot is the forwarding-path accessor. It never
// waits for PostgreSQL: a fresh cache is returned immediately, while a stale
// cache is served during a deduplicated background refresh. A cold cache fails
// closed until that refresh publishes a value.
func (s *SettingService) GetCompiledCaptureRuntimePolicyHot() CompiledCapturePolicy {
	fallback := newCaptureRuntimePolicyErrorEntry("capture runtime policy cache is cold").compiled
	if s == nil {
		return fallback
	}
	cached, _ := s.captureRuntimePolicyCache.Load().(*cachedCaptureRuntimePolicy)
	if cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.compiled
	}

	if s.captureRuntimePolicyRefreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.captureRuntimePolicyRefreshing.Store(false)
			entry := s.loadCaptureRuntimePolicy(context.Background())
			if entry.loadError != "" {
				// Do not put raw repository/DSN details in request-adjacent logs.
				slog.Warn("capture_runtime_policy_refresh_failed", "detail", "capture remains fail-closed until retry")
			}
		}()
	}
	if cached != nil {
		return cached.compiled
	}
	return fallback
}

func (s *SettingService) loadCaptureRuntimePolicy(ctx context.Context) *cachedCaptureRuntimePolicy {
	if s != nil {
		if cached, ok := s.captureRuntimePolicyCache.Load().(*cachedCaptureRuntimePolicy); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached
		}
	}
	if s == nil {
		return newCaptureRuntimePolicyErrorEntry("capture runtime policy service is nil")
	}

	result, _, _ := s.captureRuntimePolicySF.Do(captureRuntimePolicySFKey, func() (any, error) {
		if cached, ok := s.captureRuntimePolicyCache.Load().(*cachedCaptureRuntimePolicy); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached, nil
		}
		if s.settingRepo == nil {
			entry := newCaptureRuntimePolicyErrorEntry("capture runtime policy repository is nil")
			return s.publishCaptureRuntimePolicyEntry(s.captureRuntimePolicyGeneration.Load(), entry), nil
		}
		generation := s.captureRuntimePolicyGeneration.Load()

		baseCtx := ctx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), captureRuntimePolicyDBTimeout)
		defer cancel()
		raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyCaptureRuntimePolicy)
		if errors.Is(err, ErrSettingNotFound) {
			entry := newCaptureRuntimePolicySuccessEntry(DefaultCaptureRuntimePolicy())
			return s.publishCaptureRuntimePolicyEntry(generation, entry), nil
		}
		if err != nil {
			entry := newCaptureRuntimePolicyErrorEntry(fmt.Sprintf("load capture runtime policy: %v", err))
			return s.publishCaptureRuntimePolicyEntry(generation, entry), nil
		}
		policy, err := DecodeCaptureRuntimePolicy([]byte(raw))
		if err != nil {
			entry := newCaptureRuntimePolicyErrorEntry(err.Error())
			return s.publishCaptureRuntimePolicyEntry(generation, entry), nil
		}
		entry := newCaptureRuntimePolicySuccessEntry(policy)
		return s.publishCaptureRuntimePolicyEntry(generation, entry), nil
	})
	entry, _ := result.(*cachedCaptureRuntimePolicy)
	if entry == nil {
		return newCaptureRuntimePolicyErrorEntry("capture runtime policy cache returned no value")
	}
	return entry
}

func (s *SettingService) publishCaptureRuntimePolicyEntry(generation uint64, entry *cachedCaptureRuntimePolicy) *cachedCaptureRuntimePolicy {
	if s == nil {
		return entry
	}
	s.captureRuntimePolicyMu.Lock()
	defer s.captureRuntimePolicyMu.Unlock()
	if s.captureRuntimePolicyGeneration.Load() != generation {
		if current, _ := s.captureRuntimePolicyCache.Load().(*cachedCaptureRuntimePolicy); current != nil {
			return current
		}
		return entry
	}
	s.captureRuntimePolicyCache.Store(entry)
	return entry
}

func newCaptureRuntimePolicySuccessEntry(policy CaptureRuntimePolicy) *cachedCaptureRuntimePolicy {
	compiled, err := CompileCaptureRuntimePolicy(policy)
	if err != nil {
		return newCaptureRuntimePolicyErrorEntry(err.Error())
	}
	return &cachedCaptureRuntimePolicy{
		policy:    policy,
		compiled:  compiled,
		expiresAt: time.Now().Add(captureRuntimePolicyCacheTTL).UnixNano(),
	}
}

func newCaptureRuntimePolicyErrorEntry(message string) *cachedCaptureRuntimePolicy {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = false
	compiled, _ := CompileCaptureRuntimePolicy(policy)
	return &cachedCaptureRuntimePolicy{
		policy:    policy,
		compiled:  compiled,
		expiresAt: time.Now().Add(captureRuntimePolicyErrorTTL).UnixNano(),
		loadError: message,
	}
}

func (s *SettingService) UpdateCaptureRuntimePolicy(ctx context.Context, policy CaptureRuntimePolicy) (CaptureRuntimePolicy, error) {
	if s == nil || s.settingRepo == nil {
		return CaptureRuntimePolicy{}, errors.New("capture runtime policy repository is nil")
	}
	normalized, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	if err != nil {
		return CaptureRuntimePolicy{}, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return CaptureRuntimePolicy{}, fmt.Errorf("encode capture runtime policy: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCaptureRuntimePolicy, string(encoded)); err != nil {
		return CaptureRuntimePolicy{}, fmt.Errorf("save capture runtime policy: %w", err)
	}
	entry := newCaptureRuntimePolicySuccessEntry(normalized)
	s.captureRuntimePolicyMu.Lock()
	s.captureRuntimePolicyGeneration.Add(1)
	s.captureRuntimePolicyCache.Store(entry)
	s.captureRuntimePolicyMu.Unlock()
	s.captureRuntimePolicySF.Forget(captureRuntimePolicySFKey)
	return normalized, nil
}

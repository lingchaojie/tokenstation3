package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const rewardCreditExpiryBatchSize = 200

type RewardCreditExpiryService struct {
	repo                 RewardCreditRepository
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCache         BillingCache
	interval             time.Duration
	now                  func() time.Time

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewRewardCreditExpiryService(
	repo RewardCreditRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache BillingCache,
) *RewardCreditExpiryService {
	return &RewardCreditExpiryService{
		repo:                 repo,
		authCacheInvalidator: authCacheInvalidator,
		billingCache:         billingCache,
		interval:             time.Minute,
		now:                  time.Now,
	}
}

func ProvideRewardCreditExpiryService(
	repo RewardCreditRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache BillingCache,
) *RewardCreditExpiryService {
	svc := NewRewardCreditExpiryService(repo, authCacheInvalidator, billingCache)
	svc.Start()
	return svc
}

func (s *RewardCreditExpiryService) RunOnce(ctx context.Context, now time.Time) error {
	if s == nil || s.repo == nil {
		return nil
	}
	for {
		results, err := s.repo.ExpireBatch(ctx, now.UTC(), rewardCreditExpiryBatchSize)
		if err != nil {
			return err
		}
		for _, result := range results {
			if s.authCacheInvalidator != nil {
				s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, result.UserID)
			}
			if s.billingCache != nil {
				if err := s.billingCache.InvalidateUserBalance(ctx, result.UserID); err != nil {
					slog.WarnContext(ctx, "failed to invalidate balance cache after reward expiry", "user_id", result.UserID, "error", err)
				}
			}
		}
		if len(results) == 0 {
			return nil
		}
	}
}

func (s *RewardCreditExpiryService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.wg.Add(1)
	go s.run(ctx)
}

func (s *RewardCreditExpiryService) run(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := s.RunOnce(runCtx, s.now())
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Error("reward credit expiry run failed", "error", err)
			}
		}
	}
}

func (s *RewardCreditExpiryService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	s.running = false
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

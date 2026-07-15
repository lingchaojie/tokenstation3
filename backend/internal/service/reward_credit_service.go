package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrRewardCreditQueryInvalid = infraerrors.BadRequest(
	"INVALID_REWARD_CREDIT_QUERY",
	"reward credit query is invalid",
)

type RewardCreditQuery struct {
	UserID   int64
	Type     string
	Status   string
	Page     int
	PageSize int
}

type RewardCreditService struct {
	repo                 RewardCreditRepository
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCache         BillingCache
	now                  func() time.Time
}

func NewRewardCreditService(
	repo RewardCreditRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache BillingCache,
) *RewardCreditService {
	return &RewardCreditService{
		repo:                 repo,
		authCacheInvalidator: authCacheInvalidator,
		billingCache:         billingCache,
		now:                  time.Now,
	}
}

func (s *RewardCreditService) ExpireUserAndGetSummary(ctx context.Context, userID int64) (RewardBalanceSummary, float64, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return RewardBalanceSummary{}, 0, ErrRewardCreditQueryInvalid
	}
	now := s.now().UTC()
	expired, err := s.repo.ExpireUser(ctx, userID, now)
	if err != nil {
		return RewardBalanceSummary{}, 0, err
	}
	if expired > 0 {
		s.invalidateUserCaches(ctx, userID)
	}
	summary, err := s.repo.GetSummary(ctx, userID, now)
	if err != nil {
		return RewardBalanceSummary{}, expired, err
	}
	return summary, expired, nil
}

func (s *RewardCreditService) GetSummary(ctx context.Context, userID int64) (RewardBalanceSummary, error) {
	summary, _, err := s.ExpireUserAndGetSummary(ctx, userID)
	return summary, err
}

func (s *RewardCreditService) ListCredits(ctx context.Context, query RewardCreditQuery) ([]RewardCredit, int64, error) {
	if s == nil || s.repo == nil || query.UserID <= 0 || query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 {
		return nil, 0, ErrRewardCreditQueryInvalid
	}
	if strings.TrimSpace(query.Type) != "affiliate" {
		return nil, 0, ErrRewardCreditQueryInvalid
	}
	status := strings.TrimSpace(query.Status)
	switch status {
	case RewardCreditStatusActive, RewardCreditStatusExpired, RewardCreditStatusConsumed, RewardCreditStatusAll:
	default:
		return nil, 0, ErrRewardCreditQueryInvalid
	}
	now := s.now().UTC()
	expired, err := s.repo.ExpireUser(ctx, query.UserID, now)
	if err != nil {
		return nil, 0, err
	}
	if expired > 0 {
		s.invalidateUserCaches(ctx, query.UserID)
	}
	return s.repo.ListCredits(ctx, RewardCreditListFilter{
		UserID: query.UserID,
		CreditTypes: []RewardCreditType{
			RewardCreditAffiliateInviter,
			RewardCreditAffiliateInvitee,
		},
		Status:   status,
		Page:     query.Page,
		PageSize: query.PageSize,
		Now:      now,
	})
}

func (s *RewardCreditService) invalidateUserCaches(ctx context.Context, userID int64) {
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		_ = s.billingCache.InvalidateUserBalance(ctx, userID)
	}
}

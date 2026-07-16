package service

import (
	"context"
	"time"
)

type RewardCreditType string

const (
	RewardCreditDailyCheckIn     RewardCreditType = "daily_check_in"
	RewardCreditAffiliateInviter RewardCreditType = "affiliate_inviter"
	RewardCreditAffiliateInvitee RewardCreditType = "affiliate_invitee"
)

type RewardCreditRole string

const (
	RewardCreditRoleCheckIn RewardCreditRole = "check_in"
	RewardCreditRoleInviter RewardCreditRole = "inviter"
	RewardCreditRoleInvitee RewardCreditRole = "invitee"
)

const (
	RewardCreditStatusActive   = "active"
	RewardCreditStatusExpired  = "expired"
	RewardCreditStatusConsumed = "consumed"
	RewardCreditStatusAll      = "all"
)

type RewardCreditGrant struct {
	UserID     int64
	CreditType RewardCreditType
	SourceKey  string
	Amount     float64
	GrantedAt  time.Time
	ExpiresAt  time.Time
}

type RewardCreditGrantResult struct {
	CreditID     int64
	Applied      bool
	BalanceAfter float64
}

type DailyRewardBalanceSummary struct {
	Amount    float64    `json:"amount"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type AffiliateRewardBalanceSummary struct {
	Amount            float64    `json:"amount"`
	EarliestExpiresAt *time.Time `json:"earliest_expires_at"`
	CreditCount       int        `json:"credit_count"`
}

type RewardBalanceSummary struct {
	DailyCheckIn DailyRewardBalanceSummary     `json:"daily_check_in"`
	Affiliate    AffiliateRewardBalanceSummary `json:"affiliate"`
}

type RewardCredit struct {
	ID              int64            `json:"id"`
	UserID          int64            `json:"user_id"`
	CreditType      RewardCreditType `json:"credit_type"`
	RoleLabel       RewardCreditRole `json:"role_label"`
	SourceKey       string           `json:"source_key"`
	OriginalAmount  float64          `json:"original_amount"`
	RemainingAmount float64          `json:"remaining_amount"`
	ReservedAmount  float64          `json:"reserved_amount"`
	GrantedAt       time.Time        `json:"granted_at"`
	ExpiresAt       time.Time        `json:"expires_at"`
	ConsumedAt      *time.Time       `json:"consumed_at"`
	ExpiredAt       *time.Time       `json:"expired_at"`
}

type RewardCreditListFilter struct {
	UserID      int64
	CreditTypes []RewardCreditType
	Status      string
	Page        int
	PageSize    int
	Now         time.Time
}

type RewardCreditExpiryResult struct {
	UserID        int64
	ExpiredAmount float64
}

type RewardCreditRepository interface {
	Grant(context.Context, RewardCreditGrant) (RewardCreditGrantResult, error)
	GetSummary(context.Context, int64, time.Time) (RewardBalanceSummary, error)
	ListCredits(context.Context, RewardCreditListFilter) ([]RewardCredit, int64, error)
	ExpireUser(context.Context, int64, time.Time) (float64, error)
	ExpireBatch(context.Context, time.Time, int) ([]RewardCreditExpiryResult, error)
}

func RewardCreditRoleForType(creditType RewardCreditType) RewardCreditRole {
	switch creditType {
	case RewardCreditAffiliateInviter:
		return RewardCreditRoleInviter
	case RewardCreditAffiliateInvitee:
		return RewardCreditRoleInvitee
	default:
		return RewardCreditRoleCheckIn
	}
}

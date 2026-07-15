//go:build unit

package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type affiliatePolicyRepoFake struct {
	paymentFulfillmentAffiliateRepoStub
	summaries        map[int64]*AffiliateSummary
	byCode           *AffiliateSummary
	bindInputs       []AffiliateBindInput
	settlementInputs []AffiliateSettlementInput
	bindResult       AffiliateRewardResult
	settleResult     AffiliateRewardResult
}

func (r *affiliatePolicyRepoFake) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	if summary := r.summaries[userID]; summary != nil {
		copy := *summary
		return &copy, nil
	}
	return &AffiliateSummary{UserID: userID}, nil
}

func (r *affiliatePolicyRepoFake) GetAffiliateByCode(context.Context, string) (*AffiliateSummary, error) {
	if r.byCode == nil {
		return nil, ErrAffiliateProfileNotFound
	}
	copy := *r.byCode
	return &copy, nil
}

func (r *affiliatePolicyRepoFake) BindInviter(_ context.Context, input AffiliateBindInput) (AffiliateRewardResult, error) {
	r.bindInputs = append(r.bindInputs, input)
	return r.bindResult, nil
}

func (r *affiliatePolicyRepoFake) ResolveFirstRecharge(_ context.Context, input AffiliateSettlementInput) (AffiliateRewardResult, error) {
	r.settlementInputs = append(r.settlementInputs, input)
	return r.settleResult, nil
}

func (r *affiliatePolicyRepoFake) ThawFrozenQuota(context.Context, int64) (float64, error) {
	return 0, nil
}

func (r *affiliatePolicyRepoFake) ListInvitees(context.Context, int64, int) ([]AffiliateInvitee, error) {
	return []AffiliateInvitee{}, nil
}

func newAffiliatePolicyService(repo AffiliateRepository, values map[string]string) *AffiliateService {
	base := map[string]string{
		SettingKeyAffiliateEnabled:                "true",
		SettingKeyAffiliateFirstRechargeThreshold: "0",
		SettingKeyAffiliateInviterReward:          "10",
		SettingKeyAffiliateInviteeReward:          "5",
		SettingKeyAffiliateRewardValidityDays:     "7",
		SettingKeyAffiliateInviterRewardLimit:     "0",
	}
	for key, value := range values {
		base[key] = value
	}
	return NewAffiliateService(
		repo,
		NewSettingService(&paymentFulfillmentSettingRepoStub{values: base}, nil),
		nil,
		nil,
		nil,
	)
}

func TestBindInviterByCode_UsesConfiguredRewardMode(t *testing.T) {
	const inviteeID, inviterID = int64(100), int64(200)

	t.Run("activity disabled silently ignores code", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{}
		svc := newAffiliatePolicyService(repo, map[string]string{SettingKeyAffiliateEnabled: "false"})

		require.NoError(t, svc.BindInviterByCode(context.Background(), inviteeID, "ABCD"))
		require.Empty(t, repo.bindInputs)
	})

	t.Run("zero threshold binds and rewards immediately", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{
			summaries:  map[int64]*AffiliateSummary{inviteeID: {UserID: inviteeID}},
			byCode:     &AffiliateSummary{UserID: inviterID},
			bindResult: AffiliateRewardResult{Bound: true, Resolved: true},
		}
		svc := newAffiliatePolicyService(repo, map[string]string{
			SettingKeyAffiliateInviterRewardLimit: "3",
		})
		before := time.Now()

		require.NoError(t, svc.BindInviterByCode(context.Background(), inviteeID, "ABCD"))

		require.Len(t, repo.bindInputs, 1)
		input := repo.bindInputs[0]
		require.Equal(t, AffiliateRewardModeImmediate, input.RewardMode)
		require.Equal(t, inviteeID, input.InviteeUserID)
		require.Equal(t, inviterID, input.InviterUserID)
		require.Equal(t, 10.0, input.InviterReward)
		require.Equal(t, 5.0, input.InviteeReward)
		require.Equal(t, 7, input.ValidityDays)
		require.Equal(t, 3, input.InviterRewardLimit)
		require.False(t, input.GrantedAt.Before(before))
		require.False(t, input.GrantedAt.After(time.Now()))
	})

	t.Run("positive threshold waits for first recharge", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{
			summaries:  map[int64]*AffiliateSummary{inviteeID: {UserID: inviteeID}},
			byCode:     &AffiliateSummary{UserID: inviterID},
			bindResult: AffiliateRewardResult{Bound: true},
		}
		svc := newAffiliatePolicyService(repo, map[string]string{
			SettingKeyAffiliateFirstRechargeThreshold: "20",
		})

		require.NoError(t, svc.BindInviterByCode(context.Background(), inviteeID, "ABCD"))
		require.Len(t, repo.bindInputs, 1)
		require.Equal(t, AffiliateRewardModeFirstRecharge, repo.bindInputs[0].RewardMode)
	})
}

func TestGetAffiliateDetail_ExposesRewardModeValidityAndInviterLimit(t *testing.T) {
	t.Run("immediate mode at reached limit", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{
			summaries: map[int64]*AffiliateSummary{
				42: {
					UserID:             42,
					AffCode:            "INVITE42",
					InviterRewardCount: 3,
				},
			},
		}
		svc := newAffiliatePolicyService(repo, map[string]string{
			SettingKeyAffiliateFirstRechargeThreshold: "0",
			SettingKeyAffiliateRewardValidityDays:     "7",
			SettingKeyAffiliateInviterRewardLimit:     "3",
		})

		detail, err := svc.GetAffiliateDetail(context.Background(), 42)

		require.NoError(t, err)
		require.Equal(t, AffiliateRewardModeImmediate, detail.RewardMode)
		require.Equal(t, 7, detail.RewardValidityDays)
		require.Equal(t, 3, detail.InviterRewardLimit)
		require.Equal(t, 3, detail.InviterRewardCount)
		require.True(t, detail.InviterRewardLimitReached)
	})

	t.Run("first recharge mode below limit", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{
			summaries: map[int64]*AffiliateSummary{
				42: {UserID: 42, InviterRewardCount: 2},
			},
		}
		svc := newAffiliatePolicyService(repo, map[string]string{
			SettingKeyAffiliateFirstRechargeThreshold: "20",
			SettingKeyAffiliateInviterRewardLimit:     "3",
		})

		detail, err := svc.GetAffiliateDetail(context.Background(), 42)

		require.NoError(t, err)
		require.Equal(t, AffiliateRewardModeFirstRecharge, detail.RewardMode)
		require.Equal(t, 2, detail.InviterRewardCount)
		require.False(t, detail.InviterRewardLimitReached)
	})
}

func TestGrantFirstRechargeReward_AlwaysResolvesFirstRecharge(t *testing.T) {
	const inviteeID = int64(100)
	orderID := int64(900)

	t.Run("below threshold resolves without reward", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{settleResult: AffiliateRewardResult{Resolved: true}}
		svc := newAffiliatePolicyService(repo, map[string]string{SettingKeyAffiliateFirstRechargeThreshold: "20"})

		result, err := svc.GrantFirstRechargeReward(context.Background(), inviteeID, 19.99, false, true, &orderID)

		require.NoError(t, err)
		require.True(t, result.Resolved)
		require.Len(t, repo.settlementInputs, 1)
		require.False(t, repo.settlementInputs[0].Qualified)
		require.Equal(t, orderID, repo.settlementInputs[0].SourceOrderID)
	})

	t.Run("disabled activity resolves without reward", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{settleResult: AffiliateRewardResult{Resolved: true}}
		svc := newAffiliatePolicyService(repo, map[string]string{SettingKeyAffiliateEnabled: "false"})

		_, err := svc.GrantFirstRechargeReward(context.Background(), inviteeID, 100, false, true, &orderID)

		require.NoError(t, err)
		require.Len(t, repo.settlementInputs, 1)
		require.False(t, repo.settlementInputs[0].Qualified)
	})

	t.Run("subscription qualifies regardless of threshold", func(t *testing.T) {
		repo := &affiliatePolicyRepoFake{settleResult: AffiliateRewardResult{Resolved: true, Qualified: true}}
		svc := newAffiliatePolicyService(repo, map[string]string{SettingKeyAffiliateFirstRechargeThreshold: "100"})

		result, err := svc.GrantFirstRechargeReward(context.Background(), inviteeID, 1, true, true, &orderID)

		require.NoError(t, err)
		require.True(t, result.Qualified)
		require.Len(t, repo.settlementInputs, 1)
		require.True(t, repo.settlementInputs[0].Qualified)
	})
}

// TestIsEnabled_NilSettingServiceReturnsDefault verifies that IsEnabled
// safely handles a nil settingService dependency by returning the default
// (off). This protects callers from nil-pointer crashes in misconfigured
// environments.
func TestIsEnabled_NilSettingServiceReturnsDefault(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}
	require.False(t, svc.IsEnabled(context.Background()))
	require.Equal(t, AffiliateEnabledDefault, svc.IsEnabled(context.Background()))
}

// TestValidateExclusiveRate_BoundaryAndInvalid covers the validator used by
// admin-facing rate setters: nil is always valid (clear), in-range values
// are accepted, NaN/Inf and out-of-range values produce a typed BadRequest.
func TestValidateExclusiveRate_BoundaryAndInvalid(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateExclusiveRate(nil))

	for _, v := range []float64{0, 0.01, 50, 99.99, 100} {
		v := v
		require.NoError(t, validateExclusiveRate(&v), "value %v should be valid", v)
	}

	for _, v := range []float64{-0.01, 100.01, -100, 200} {
		v := v
		require.Error(t, validateExclusiveRate(&v), "value %v should be rejected", v)
	}

	nan := math.NaN()
	require.Error(t, validateExclusiveRate(&nan))
	posInf := math.Inf(1)
	require.Error(t, validateExclusiveRate(&posInf))
	negInf := math.Inf(-1)
	require.Error(t, validateExclusiveRate(&negInf))
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a***@g***.com", maskEmail("alice@gmail.com"))
	require.Equal(t, "x***@d***", maskEmail("x@domain"))
	require.Equal(t, "", maskEmail(""))
}

func TestIsValidAffiliateCodeFormat(t *testing.T) {
	t.Parallel()

	// 邀请码格式校验同时服务于：
	// 1) 系统自动生成的 12 位随机码（A-Z 去 I/O，2-9 去 0/1）
	// 2) 管理员设置的自定义专属码（如 "VIP2026"、"NEW_USER-1"）
	// 因此校验放宽到 [A-Z0-9_-]{4,32}（要求调用方先 ToUpper）。
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid canonical 12-char", "ABCDEFGHJKLM", true},
		{"valid all digits 2-9", "234567892345", true},
		{"valid mixed", "A2B3C4D5E6F7", true},
		{"valid admin custom short", "VIP1", true},
		{"valid admin custom with hyphen", "NEW-USER", true},
		{"valid admin custom with underscore", "VIP_2026", true},
		{"valid 32-char max", "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", true},
		// Previously-excluded chars (I/O/0/1) are now allowed since admins may use them.
		{"letter I now allowed", "IBCDEFGHJKLM", true},
		{"letter O now allowed", "OBCDEFGHJKLM", true},
		{"digit 0 now allowed", "0BCDEFGHJKLM", true},
		{"digit 1 now allowed", "1BCDEFGHJKLM", true},
		{"too short (3 chars)", "ABC", false},
		{"too long (33 chars)", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456", false},
		{"lowercase rejected (caller must ToUpper first)", "abcdefghjklm", false},
		{"empty", "", false},
		{"utf8 non-ascii", "ÄÄÄÄÄÄ", false}, // bytes out of charset
		{"ascii punctuation .", "ABCDEFGHJK.M", false},
		{"whitespace", "ABCDEFGHJK M", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isValidAffiliateCodeFormat(tc.in))
		})
	}
}

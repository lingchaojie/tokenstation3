//go:build unit

package service

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// affiliateRewardFakeRepo is a minimal AffiliateRepository fake for
// GrantFirstRechargeReward tests. It returns per-user summaries (so the
// invitee's InviterID can be controlled) and records AccrueQuota calls.
type affiliateRewardFakeRepo struct {
	paymentFulfillmentAffiliateRepoStub
	// summaries lets tests control the summary returned by EnsureUserAffiliate
	// per user id (notably the invitee's InviterID).
	summaries map[int64]*AffiliateSummary
	accrued   float64
}

func (r *affiliateRewardFakeRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	if s, ok := r.summaries[userID]; ok && s != nil {
		cp := *s
		return &cp, nil
	}
	return &AffiliateSummary{UserID: userID}, nil
}

func (r *affiliateRewardFakeRepo) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	return r.accrued, nil
}

// affiliateRewardFakeUserRepo records UpdateBalance calls for the invitee-side
// reward and can be forced to fail to exercise the rollback/error path.
type affiliateRewardFakeUserRepo struct {
	mockUserRepo
	balanceCalls []struct {
		userID int64
		amount float64
	}
	failUserID int64
}

func (r *affiliateRewardFakeUserRepo) UpdateBalance(_ context.Context, id int64, amount float64) error {
	if r.failUserID != 0 && id == r.failUserID {
		return fmt.Errorf("forced balance failure for %d", id)
	}
	r.balanceCalls = append(r.balanceCalls, struct {
		userID int64
		amount float64
	}{id, amount})
	return nil
}

func newRewardTestService(t *testing.T, repo AffiliateRepository, userRepo UserRepository, overrides map[string]string) *AffiliateService {
	t.Helper()
	base := map[string]string{
		SettingKeyAffiliateEnabled:                "true",
		SettingKeyAffiliateFirstRechargeThreshold: "20",
		SettingKeyAffiliateInviterReward:          "5",
		SettingKeyAffiliateInviteeReward:          "5",
		SettingKeyAffiliateRebateFreezeHours:      "0",
	}
	for k, v := range overrides {
		base[k] = v
	}
	ss := NewSettingService(&paymentFulfillmentSettingRepoStub{values: base}, nil)
	return NewAffiliateService(repo, ss, nil, nil, userRepo)
}

func TestGrantFirstRechargeReward(t *testing.T) {
	ctx := context.Background()
	const inviteeID, inviterID = int64(100), int64(200)
	orderID := int64(9)

	newRepo := func() *affiliateRewardFakeRepo {
		return &affiliateRewardFakeRepo{
			summaries: map[int64]*AffiliateSummary{
				inviteeID: {UserID: inviteeID, InviterID: int64Ptr(inviterID)},
				inviterID: {UserID: inviterID},
			},
		}
	}

	t.Run("首充余额达标_双方各得", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Equal(t, inviterID, res.InviterID)
		require.Equal(t, 5.0, res.InviterReward)
		require.Equal(t, 5.0, res.InviteeReward)
		require.Len(t, repo.accrueCalls, 1)
		require.Equal(t, inviterID, repo.accrueCalls[0].inviterID)
		require.Equal(t, inviteeID, repo.accrueCalls[0].inviteeUserID)
		require.Equal(t, 5.0, repo.accrueCalls[0].amount)
		require.Len(t, ur.balanceCalls, 1)
		require.Equal(t, inviteeID, ur.balanceCalls[0].userID)
		require.Equal(t, 5.0, ur.balanceCalls[0].amount)
	})

	t.Run("首充余额不达标_不发", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 19.99, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, repo.accrueCalls)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("订阅无条件达标", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 1, true, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Len(t, repo.accrueCalls, 1)
		require.Len(t, ur.balanceCalls, 1)
	})

	t.Run("非首充_跳过", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 100, false, false, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, repo.accrueCalls)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("无邀请人_跳过", func(t *testing.T) {
		repo := newRepo()
		repo.summaries[inviteeID].InviterID = nil
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 50, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, repo.accrueCalls)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("阈值边界_等于阈值达标", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
	})

	t.Run("被邀请方奖励为0_只发邀请方", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, map[string]string{SettingKeyAffiliateInviteeReward: "0"})
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Equal(t, 5.0, res.InviterReward)
		require.Equal(t, 0.0, res.InviteeReward)
		require.Len(t, repo.accrueCalls, 1)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("总开关关闭_跳过", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, map[string]string{SettingKeyAffiliateEnabled: "false"})
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 50, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, repo.accrueCalls)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("被邀请方加余额失败_返回错误", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateRewardFakeUserRepo{failUserID: inviteeID}
		svc := newRewardTestService(t, repo, ur, nil)
		_, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.Error(t, err)
	})
}

func TestAccrueInviteRebatePreservesPercentageRulesAlongsideFixedFirstRechargeRewards(t *testing.T) {
	ctx := context.Background()
	const inviteeID, inviterID = int64(100), int64(200)
	exclusiveRate := 25.0
	repo := &affiliateRewardFakeRepo{
		summaries: map[int64]*AffiliateSummary{
			inviteeID: {UserID: inviteeID, InviterID: int64Ptr(inviterID), CreatedAt: time.Now().Add(-time.Hour)},
			inviterID: {UserID: inviterID, AffRebateRatePercent: &exclusiveRate},
		},
		accrued: 4,
	}
	svc := newRewardTestService(t, repo, &affiliateRewardFakeUserRepo{}, map[string]string{
		SettingKeyAffiliateRebateRate:          "20",
		SettingKeyAffiliateRebateDurationDays:  "365",
		SettingKeyAffiliateRebatePerInviteeCap: "5",
		SettingKeyAffiliateRebateFreezeHours:   "12",
	})

	rebate, err := svc.AccrueInviteRebate(ctx, inviteeID, 10)
	require.NoError(t, err)
	require.Equal(t, 1.0, rebate, "25%% exclusive rate is capped to the remaining per-invitee allowance")
	require.Len(t, repo.accrueCalls, 1)
	require.Equal(t, inviterID, repo.accrueCalls[0].inviterID)
	require.Equal(t, inviteeID, repo.accrueCalls[0].inviteeUserID)
	require.Equal(t, 1.0, repo.accrueCalls[0].amount)
	require.Equal(t, 12, repo.accrueCalls[0].freezeHours)
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

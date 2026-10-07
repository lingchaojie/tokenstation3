package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Run a real resend at a deterministic boundary of the real Redis client.
// Nothing replaces the cache or the verification implementation.
type resendVerificationHook struct {
	key            string
	afterIncrement bool
	resend         func()
	done           bool
}

func (h *resendVerificationHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *resendVerificationHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *resendVerificationHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		match := cmd.Name() == "get" && len(args) > 1 && args[1] == h.key
		if h.afterIncrement {
			match = (cmd.Name() == "eval" || cmd.Name() == "evalsha") && len(args) > 3 && args[3] == h.key
		}
		if err == nil && !h.done && match {
			h.done = true
			h.resend()
		}
		return err
	}
}

func TestB8deSecurityVerificationResendDoesNotValidateOldGeneration(t *testing.T) {
	for _, afterIncrement := range []bool{false, true} {
		name := "after_read"
		if afterIncrement {
			name = "after_attempt_reservation"
		}
		t.Run(name, func(t *testing.T) {
			cache, mr, rdb := newMiniredisEmailCache(t)
			writer := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = writer.Close() })
			newCache := NewEmailCache(writer)
			ctx := context.Background()
			const email = "generation@example.com"
			require.NoError(t, cache.SetVerificationCode(ctx, email, &service.VerificationCodeData{Code: "123456", CreatedAt: time.Now()}, time.Minute))
			rdb.AddHook(&resendVerificationHook{key: verifyCodeKey(email), afterIncrement: afterIncrement, resend: func() {
				require.NoError(t, newCache.SetVerificationCode(ctx, email, &service.VerificationCodeData{Code: "654321", CreatedAt: time.Now()}, time.Minute))
			}})
			svc := service.NewEmailService(nil, cache)
			require.ErrorIs(t, svc.VerifyCode(ctx, email, "123456"), service.ErrInvalidVerifyCode)
			data, err := newCache.GetVerificationCode(ctx, email)
			require.NoError(t, err)
			require.Equal(t, "654321", data.Code)
			require.Zero(t, data.Attempts, "old requests must not consume the new generation's budget")
			require.NoError(t, svc.VerifyCode(ctx, email, "654321"))
		})
	}
}

func TestB8deSecurityVerificationLegacyAttemptsSurviveUpgrade(t *testing.T) {
	cache, _, rdb := newMiniredisEmailCache(t)
	ctx := context.Background()
	const email = "legacy@example.com"
	// Pre-upgrade codes stored attempts only inside JSON, without a side key.
	raw, err := json.Marshal(&service.VerificationCodeData{Code: "123456", Attempts: 4, CreatedAt: time.Now()})
	require.NoError(t, err)
	require.NoError(t, rdb.Set(ctx, verifyCodeKey(email), raw, time.Minute).Err())
	svc := service.NewEmailService(nil, cache)
	require.ErrorIs(t, svc.VerifyCode(ctx, email, "000000"), service.ErrVerifyCodeMaxAttempts)
	require.ErrorIs(t, svc.VerifyCode(ctx, email, "123456"), service.ErrVerifyCodeMaxAttempts)
}

//go:build integration

package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EmailCacheSuite struct {
	IntegrationRedisSuite
	cache service.EmailCache
}

func (s *EmailCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewEmailCache(s.rdb)
}

func (s *EmailCacheSuite) TestGetVerificationCode_Missing() {
	_, err := s.cache.GetVerificationCode(s.ctx, "nonexistent@example.com")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing verification code")
}

func (s *EmailCacheSuite) TestSetAndGetVerificationCode() {
	email := "a@example.com"
	emailTTL := 2 * time.Minute
	data := &service.VerificationCodeData{Code: "123456", Attempts: 1, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, emailTTL), "SetVerificationCode")

	got, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err, "GetVerificationCode")
	require.Equal(s.T(), "123456", got.Code)
	require.Equal(s.T(), 1, got.Attempts)
}

func (s *EmailCacheSuite) TestVerificationCode_TTL() {
	email := "ttl@example.com"
	emailTTL := 2 * time.Minute
	data := &service.VerificationCodeData{Code: "654321", Attempts: 0, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, emailTTL), "SetVerificationCode")

	emailKey := verifyCodeKeyPrefix + email
	ttl, err := s.rdb.TTL(s.ctx, emailKey).Result()
	require.NoError(s.T(), err, "TTL emailKey")
	s.AssertTTLWithin(ttl, 1*time.Second, emailTTL)
}

func (s *EmailCacheSuite) TestDeleteVerificationCode() {
	email := "delete@example.com"
	data := &service.VerificationCodeData{Code: "999999", Attempts: 0, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, 2*time.Minute), "SetVerificationCode")

	// Verify it exists
	_, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err, "GetVerificationCode before delete")

	// Delete
	require.NoError(s.T(), s.cache.DeleteVerificationCode(s.ctx, email), "DeleteVerificationCode")

	// Verify it's gone
	_, err = s.cache.GetVerificationCode(s.ctx, email)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *EmailCacheSuite) TestDeleteVerificationCode_NonExistent() {
	// Deleting a non-existent key should not error
	require.NoError(s.T(), s.cache.DeleteVerificationCode(s.ctx, "nonexistent@example.com"), "DeleteVerificationCode non-existent")
}

func (s *EmailCacheSuite) TestGetVerificationCode_JSONCorruption() {
	emailKey := verifyCodeKeyPrefix + "corrupted@example.com"

	require.NoError(s.T(), s.rdb.Set(s.ctx, emailKey, "not-json", 1*time.Minute).Err(), "Set invalid JSON")

	_, err := s.cache.GetVerificationCode(s.ctx, "corrupted@example.com")
	require.Error(s.T(), err, "expected error for corrupted JSON")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected decoding error, not redis.Nil")
}

func TestEmailCacheSuite(t *testing.T) {
	suite.Run(t, new(EmailCacheSuite))
}

func (s *EmailCacheSuite) TestB8dePasswordResetReissueAndConcurrentConsume() {
	const email = "reset-concurrency@example.com"
	const oldToken, freshToken = "old-reset-secret", "fresh-reset-secret"
	svc := service.NewEmailService(nil, s.cache)
	for _, token := range []string{oldToken, freshToken} {
		sum := sha256.Sum256([]byte(token))
		require.NoError(s.T(), s.cache.SetPasswordResetToken(s.ctx, email, &service.PasswordResetTokenData{
			Token: hex.EncodeToString(sum[:]), CreatedAt: time.Now(),
		}, 30*time.Minute))
	}
	raw, err := s.rdb.Get(s.ctx, passwordResetKey(email)).Result()
	require.NoError(s.T(), err)
	require.NotContains(s.T(), raw, freshToken)
	require.ErrorIs(s.T(), svc.ConsumePasswordResetToken(s.ctx, email, oldToken), service.ErrInvalidResetToken)
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.ConsumePasswordResetToken(s.ctx, email, freshToken)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, service.ErrInvalidResetToken) {
				s.T().Errorf("unexpected reset result: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	require.Equal(s.T(), int32(1), successes.Load())
	require.Zero(s.T(), s.rdb.Exists(s.ctx, passwordResetKey(email)).Val())
}

func (s *EmailCacheSuite) TestB8deVerificationGenerationFenceAndLegacyBudget() {
	const email = "generation@example.com"
	// The old JSON format has no separate attempt counter.
	require.NoError(s.T(), s.rdb.Set(s.ctx, verifyCodeKey(email), `{"Code":"123456","Attempts":4,"CreatedAt":"2026-01-01T00:00:00Z","ExpiresAt":"0001-01-01T00:00:00Z"}`, time.Minute).Err())
	svc := service.NewEmailService(nil, s.cache)
	require.ErrorIs(s.T(), svc.VerifyCode(s.ctx, email, "000000"), service.ErrVerifyCodeMaxAttempts)
	require.ErrorIs(s.T(), svc.VerifyCode(s.ctx, email, "123456"), service.ErrVerifyCodeMaxAttempts)
	old, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err)
	newCode := &service.VerificationCodeData{Code: "654321", CreatedAt: time.Now()}
	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, newCode, time.Minute))
	_, err = s.cache.IncrVerificationCodeAttempts(s.ctx, email, old)
	require.ErrorIs(s.T(), err, redis.Nil)
	consumed, err := s.cache.ConsumeVerificationCode(s.ctx, email, old)
	require.NoError(s.T(), err)
	require.False(s.T(), consumed)
	fresh, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err)
	require.Zero(s.T(), fresh.Attempts)
	require.NoError(s.T(), svc.VerifyCode(s.ctx, email, "654321"))
}

func (s *EmailCacheSuite) TestB8deVerificationConcurrentSuccessSingleUse() {
	const email = "single@example.com"
	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, &service.VerificationCodeData{Code: "123456", CreatedAt: time.Now()}, time.Minute))
	svc := service.NewEmailService(nil, s.cache)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.VerifyCode(s.ctx, email, "123456")
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, service.ErrInvalidVerifyCode) && !errors.Is(err, service.ErrVerifyCodeMaxAttempts) {
				s.T().Errorf("unexpected verification error: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(s.T(), int32(1), successes.Load())
}

func (s *EmailCacheSuite) TestB8deVerificationConcurrentWrongGuessesExhaustBudget() {
	const email = "wrong-guesses@example.com"
	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, &service.VerificationCodeData{Code: "123456", CreatedAt: time.Now()}, time.Minute))
	svc := service.NewEmailService(nil, s.cache)
	var incorrect, limited atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.VerifyCode(s.ctx, email, "000000")
			switch {
			case errors.Is(err, service.ErrInvalidVerifyCode):
				incorrect.Add(1)
			case errors.Is(err, service.ErrVerifyCodeMaxAttempts):
				limited.Add(1)
			default:
				s.T().Errorf("unexpected result: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	// Attempts1..4 report wrong code; the fifth exhausts the budget. No
	// later request may compare a guess or accept even the correct code.
	require.Equal(s.T(), int32(4), incorrect.Load())
	require.Equal(s.T(), int32(26), limited.Load())
	require.ErrorIs(s.T(), svc.VerifyCode(s.ctx, email, "123456"), service.ErrVerifyCodeMaxAttempts)
}

func (s *EmailCacheSuite) TestB8deNotifyVerificationGenerationFence() {
	const email = "notify@example.com"
	old := &service.VerificationCodeData{Code: "123456", CreatedAt: time.Now()}
	require.NoError(s.T(), s.cache.SetNotifyVerifyCode(s.ctx, email, old, time.Minute))
	n, err := s.cache.IncrNotifyVerifyCodeAttempts(s.ctx, email, old)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	newCode := &service.VerificationCodeData{Code: "654321", CreatedAt: time.Now()}
	require.NoError(s.T(), s.cache.SetNotifyVerifyCode(s.ctx, email, newCode, time.Minute))
	consumed, err := s.cache.ConsumeNotifyVerifyCode(s.ctx, email, old)
	require.NoError(s.T(), err)
	require.False(s.T(), consumed)
	consumed, err = s.cache.ConsumeNotifyVerifyCode(s.ctx, email, newCode)
	require.NoError(s.T(), err)
	require.True(s.T(), consumed)
}

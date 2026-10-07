//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type resetAuthTokenCache struct {
	EmailCache
	data *PasswordResetTokenData
}

func (c *resetAuthTokenCache) GetPasswordResetToken(context.Context, string) (*PasswordResetTokenData, error) {
	return c.data, nil
}

func (c *resetAuthTokenCache) ConsumePasswordResetToken(_ context.Context, _ string, hash string) (bool, error) {
	if c.data == nil || c.data.Token != hash {
		return false, nil
	}
	c.data = nil
	return true, nil
}

type resetAuthUserRepo struct {
	UserRepository
	user    User
	fail    string
	updates int
}

func (r *resetAuthUserRepo) GetByEmail(context.Context, string) (*User, error) {
	if r.fail == "lookup" {
		return nil, errors.New("synthetic lookup failure")
	}
	u := r.user
	return &u, nil
}

func (r *resetAuthUserRepo) Update(_ context.Context, u *User, fields UserUpdateFields) error {
	r.updates++
	if r.fail == "update" {
		return errors.New("synthetic update failure")
	}
	if fields.PasswordHash {
		r.user.PasswordHash = u.PasswordHash
	}
	return nil
}

// Consuming after the DB write (or accepting a failed consume) would let a
// failed reset's old link change a password on retry. Neither may happen.
func TestB8dePasswordResetDatabaseFailureDoesNotReuseToken(t *testing.T) {
	for _, phase := range []string{"lookup", "update"} {
		t.Run(phase, func(t *testing.T) {
			cache := &resetAuthTokenCache{data: &PasswordResetTokenData{Token: hashPasswordResetToken("valid-secret")}}
			svc := newAuthService(nil, map[string]string{
				SettingKeyEmailVerifyEnabled: "true", SettingKeyPasswordResetEnabled: "true",
			}, cache, nil)
			repo := &resetAuthUserRepo{fail: phase, user: User{ID: 7, Email: "reset@example.com", PasswordHash: "original-hash", Status: StatusActive}}
			svc.userRepo = repo
			require.ErrorIs(t, svc.ResetPassword(context.Background(), repo.user.Email, "valid-secret", "new-password"), ErrServiceUnavailable)
			require.Equal(t, "original-hash", repo.user.PasswordHash)
			require.Nil(t, cache.data)
			attempts := repo.updates
			repo.fail = ""
			require.ErrorIs(t, svc.ResetPassword(context.Background(), repo.user.Email, "valid-secret", "new-password"), ErrInvalidResetToken)
			require.Equal(t, attempts, repo.updates)
			require.Equal(t, "original-hash", repo.user.PasswordHash)
		})
	}
}

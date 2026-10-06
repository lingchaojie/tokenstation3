package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type resetFenceFailureRepo struct {
	IdempotencyRepository
	phase string
}

func (r *resetFenceFailureRepo) MarkSucceeded(ctx context.Context, id int64, status int, body string, expires time.Time) error {
	var marker claudeResetFence
	_ = json.Unmarshal([]byte(body), &marker)
	if marker.Operation != "" && (r.phase == "before_post" || marker.Outcome == ClaudeResetOutcomeReset) {
		return errors.New("synthetic database write failure")
	}
	return r.IdempotencyRepository.MarkSucceeded(ctx, id, status, body, expires)
}

type resetContractProxyRepo struct{ ProxyRepository }

func (*resetContractProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return &Proxy{ID: 17, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "synthetic", Password: "secret"}, nil
}

// Removing the durable pre-POST marker check or clearing it on a failed final
// write must fail this test: either would permit an irreversible duplicate claim.
func TestB8deClaudeResetLocalContract(t *testing.T) {
	for _, phase := range []string{"before_post", "after_post"} {
		t.Run(phase, func(t *testing.T) {
			f := &redeemFake{claim: `{"result":"reset"}`}
			s, repo, _ := newRedeemService(t, f)
			s.idempotency.repo = &resetFenceFailureRepo{IdempotencyRepository: repo, phase: phase}
			out, err := s.Redeem(context.Background(), 1, "first-confirmation")
			if phase == "before_post" {
				require.ErrorIs(t, err, ErrIdempotencyStoreUnavail)
				require.Zero(t, f.postCount())
				return
			}
			require.NoError(t, err)
			require.Equal(t, ClaudeResetOutcomeUnknown, out.Outcome)
			require.Equal(t, "result_persistence_failed", out.Reason)
			require.Equal(t, 1, f.postCount())
			// Recover the DB and restart the service locks. The durable marker,
			// not process memory, must fence duplicate accounts of the same org.
			s.idempotency.repo = repo
			s.locks = &redeemLeaseStub{}
			again, err := s.Redeem(context.Background(), 1, "first-confirmation")
			require.NoError(t, err)
			require.True(t, again.Replayed)
			require.Equal(t, ClaudeResetOutcomeUnknown, again.Outcome)
			_, err = s.Redeem(context.Background(), 2, "another-confirmation")
			require.Equal(t, "CLAUDE_RESET_UNRESOLVED", infraerrors.Reason(err))
			require.Equal(t, 1, f.postCount())
		})
	}
	t.Run("query and redemption keep account proxy and OAuth headers", func(t *testing.T) {
		f := &redeemFake{claim: `{"result":"reset"}`}
		s, _, _ := newRedeemService(t, f)
		proxyID := int64(17)
		s.accounts = resetAccountStub{account: &Account{ID: 1, Platform: PlatformAnthropic,
			Type: AccountTypeOAuth, ProxyID: &proxyID, Credentials: map[string]any{"scope": "user:profile"}}}
		s.proxies = &resetContractProxyRepo{}
		upstream := s.do
		requests := 0
		s.do = func(r *http.Request, proxy string) (*http.Response, error) {
			requests++
			require.Equal(t, "http://synthetic:secret@proxy.example:8080", proxy)
			require.Equal(t, "Bearer synthetic-token", r.Header.Get("Authorization"))
			require.Equal(t, "oauth-2025-04-20", r.Header.Get("Anthropic-Beta"))
			require.Equal(t, "cli", r.Header.Get("X-App"))
			require.Contains(t, r.Header.Get("User-Agent"), "claude-cli/")
			return upstream(r, proxy)
		}
		_, err := s.Query(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, f.postCount())
		_, err = s.Redeem(context.Background(), 1, "manual-confirmation")
		require.NoError(t, err)
		require.Equal(t, 1, f.postCount())
		require.Equal(t, 5, requests, "query + profile + eligibility + POST + refreshed credits")
	})
}

//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type kiroOverageAuthorityRepo struct {
	mockAccountRepoForGemini
	until       *time.Time
	err         error
	checks      int
	parkOnCheck int
}

func (r *kiroOverageAuthorityRepo) GetActiveKiroOveragePark(context.Context, int64) (*time.Time, error) {
	r.checks++
	if r.checks < r.parkOnCheck {
		return nil, nil
	}
	return r.until, r.err
}

func TestKiroOverageAuthorityBeforeNativeIO(t *testing.T) {
	for _, transport := range []string{"runtime", "mcp"} {
		for _, tc := range []struct {
			name        string
			parkOnCheck int
			lookupErr   error
			wantStatus  int
		}{
			{name: "stale account snapshot", parkOnCheck: 1, wantStatus: http.StatusPaymentRequired},
			{name: "park installed before retry", parkOnCheck: 2, wantStatus: http.StatusPaymentRequired},
			{name: "database lookup failed", parkOnCheck: 1, lookupErr: errors.New("private SQL information"), wantStatus: http.StatusServiceUnavailable},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				until := time.Now().Add(time.Hour)
				repo := &kiroOverageAuthorityRepo{until: &until, err: tc.lookupErr, parkOnCheck: tc.parkOnCheck}
				upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusInternalServerError, "{}"), newJSONResponse(http.StatusOK, "{}")}}
				account := &Account{ID: 15, Platform: PlatformKiro, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "local-fixture"}}
				svc := &GatewayService{accountRepo: repo, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{}, tlsFPProfileService: &TLSFingerprintProfileService{}}
				var response *http.Response
				var err error
				if transport == "runtime" {
					payload, payloadErr := createTestPayload("claude-sonnet-4-6")
					require.NoError(t, payloadErr)
					body, marshalErr := json.Marshal(payload)
					require.NoError(t, marshalErr)
					response, _, err = svc.executeKiroUpstream(ctx, account, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "local-fixture", nil)
				} else {
					response, _, err = svc.doKiroMCPJSONRequest(ctx, account, "https://q.us-east-1.amazonaws.com/mcp", []byte("{}"), "local-fixture")
				}
				if response != nil {
					_ = response.Body.Close()
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, tc.wantStatus, failure.StatusCode)
				require.False(t, failure.HasUpstreamHTTPResponse)
				require.True(t, failure.ShouldRetryNextAccount())
				require.NotContains(t, failure.ClientMessage, "private SQL information")
				require.Len(t, upstream.requests, tc.parkOnCheck-1)
				require.Equal(t, tc.parkOnCheck, repo.checks)
			})
		}
	}
}

type kiroOverageStatusCache struct {
	TempUnschedCache
	state *TempUnschedState
}

func (c *kiroOverageStatusCache) GetTempUnsched(context.Context, int64) (*TempUnschedState, error) {
	return c.state, nil
}

func (c *kiroOverageStatusCache) SetTempUnsched(_ context.Context, _ int64, state *TempUnschedState) error {
	c.state = state
	return nil
}

func TestGetTempUnschedStatusPrefersAuthoritativeKiroOveragePark(t *testing.T) {
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	repo := &kiroOverageAuthorityRepo{until: &until}
	cache := &kiroOverageStatusCache{state: &TempUnschedState{UntilUnix: until.Add(time.Hour).Unix(), ErrorMessage: "old authentication cooldown"}}
	svc := &RateLimitService{accountRepo: repo, tempUnschedCache: cache}

	status, err := svc.GetTempUnschedStatus(ctx, 15)
	require.NoError(t, err)
	require.Equal(t, &TempUnschedState{UntilUnix: until.Unix(), ErrorMessage: "OVERAGE_REQUEST_LIMIT_EXCEEDED"}, status)
}

func TestGetTempUnschedStatusDoesNotDisplayStaleKiroOverageAfterManualClear(t *testing.T) {
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	repo := &kiroOverageAuthorityRepo{}
	repo.accountsByID = map[int64]*Account{15: {ID: 15, Platform: PlatformKiro, Type: AccountTypeAPIKey}}
	cache := &kiroOverageStatusCache{state: &TempUnschedState{UntilUnix: until.Unix(), ErrorMessage: "OVERAGE_REQUEST_LIMIT_EXCEEDED"}}
	svc := &RateLimitService{accountRepo: repo, tempUnschedCache: cache}

	status, err := svc.GetTempUnschedStatus(ctx, 15)
	require.NoError(t, err)
	require.Nil(t, status)
}

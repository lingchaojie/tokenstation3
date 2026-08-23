package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cursorGatewayTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func cursorGatewayAccount() *Account {
	return &Account{ID: 811, Name: "cursor-test", Platform: PlatformCursor, Type: AccountTypeOAuth}
}

func TestResolveCursorChatMetaPreservesCursorModelIdentity(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := cursorGatewayAccount()
	account.Credentials = map[string]any{
		"model_mapping": map[string]any{"client-model": "claude-4.5-sonnet-max"},
	}

	meta := svc.resolveCursorChatMeta(account, "client-model", "", true)
	require.Equal(t, "client-model", meta.originalModel)
	require.Equal(t, "claude-4.5-sonnet-max", meta.billingModel)
	require.Equal(t, "claude-4.5-sonnet-max", meta.upstreamModel)
	require.True(t, meta.stream)

	for _, model := range []string{"gpt-5", "gpt-5-codex", "composer-2.5-fast", "auto"} {
		got := svc.resolveCursorChatMeta(cursorGatewayAccount(), model, "", false)
		require.Equal(t, model, got.upstreamModel)
	}
}

func TestCursorAgentFailureTransientRetriesSameAccountBeforeFailover(t *testing.T) {
	err := (&OpenAIGatewayService{}).cursorAgentFailure(
		cursorGatewayTestContext(t), cursorGatewayAccount(), io.ErrUnexpectedEOF,
	)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusBadGateway, failover.StatusCode)
	require.True(t, failover.RetryableOnSameAccount)
	require.True(t, failover.RequestScopedTransient)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.Equal(t, PlatformCursor, failover.Platform)
}

func TestCursorAgentFailureCancellationNeverRetries(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := (&OpenAIGatewayService{}).cursorAgentFailure(
			cursorGatewayTestContext(t), cursorGatewayAccount(), errors.Join(errors.New("stream stopped"), cause),
		)
		require.ErrorIs(t, err, cause)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
	}
}

func TestCursorAgentFailureRateLimitSwitchesWithoutQuarantine(t *testing.T) {
	err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), &cursorpkg.AgentError{
		Code: "resource_exhausted", Message: "capacity", Raw: `{"message":"capacity"}`, HTTPStatus: http.StatusTooManyRequests,
	})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.False(t, failover.RetryableOnSameAccount)
	require.True(t, failover.RequestScopedTransient)
	require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
}

func TestCursorAgentFailureSeparatesMappedStatusFromActualHTTPProvenance(t *testing.T) {
	tests := []struct {
		name           string
		agentErr       *cursorpkg.AgentError
		wantStatus     int
		wantScope      GatewayFailureScope
		wantReason     GatewayFailureReason
		wantActualHTTP int
	}{
		{
			name: "actual non-2xx response",
			agentErr: &cursorpkg.AgentError{
				Code: "internal", HTTPStatus: http.StatusServiceUnavailable,
				HasHTTPResponse: true, ActualHTTPStatus: http.StatusServiceUnavailable,
			},
			wantStatus: http.StatusServiceUnavailable, wantScope: GatewayFailureScopeRequest,
			wantActualHTTP: http.StatusServiceUnavailable,
		},
		{
			name: "rate limit Connect trailer over HTTP 200",
			agentErr: &cursorpkg.AgentError{
				Code: "resource_exhausted", HTTPStatus: http.StatusTooManyRequests,
				HasHTTPResponse: true, ActualHTTPStatus: http.StatusOK,
			},
			wantStatus: http.StatusServiceUnavailable, wantScope: GatewayFailureScopeRequest,
			wantActualHTTP: http.StatusOK,
		},
		{
			name: "auth Connect trailer over HTTP 200",
			agentErr: &cursorpkg.AgentError{
				Code: "unauthenticated", HTTPStatus: http.StatusUnauthorized,
				HasHTTPResponse: true, ActualHTTPStatus: http.StatusOK,
			},
			wantStatus: http.StatusServiceUnavailable, wantScope: GatewayFailureScopeAccount,
			wantReason: CursorCredentialReasonExpired, wantActualHTTP: http.StatusOK,
		},
		{
			name: "client-version Connect trailer over HTTP 200",
			agentErr: &cursorpkg.AgentError{
				Code: "permission_denied", Message: "client version too old", HTTPStatus: http.StatusForbidden,
				HasHTTPResponse: true, ActualHTTPStatus: http.StatusOK,
			},
			wantStatus: http.StatusForbidden, wantScope: GatewayFailureScopeProvider,
			wantReason: CursorCredentialReasonClientVersion, wantActualHTTP: http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), test.agentErr)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, test.wantStatus, failover.StatusCode)
			require.Equal(t, test.wantScope, failover.Scope)
			require.Equal(t, test.wantReason, failover.Reason)
			require.True(t, failover.HasUpstreamHTTPResponse)
			require.Equal(t, test.wantActualHTTP, failover.UpstreamHTTPStatus)
		})
	}
}

func TestCursorAgentFailureClientVersionStopsProviderRotation(t *testing.T) {
	secret := "private-prompt-and-token-must-not-escape"
	err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), &cursorpkg.AgentError{
		Code: "permission_denied", Message: "Update Required " + secret,
		Raw: `{"message":"client version too old ` + secret + `"}`, HTTPStatus: http.StatusForbidden,
	})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, GatewayFailureScopeProvider, failover.Scope)
	require.Equal(t, CursorCredentialReasonClientVersion, failover.Reason)
	require.Equal(t, NextAccountStop, failover.NextAccountAction)
	require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
	require.NotContains(t, string(failover.ResponseBody), secret)
	require.NotContains(t, failover.ClientMessage, secret)
}

func TestCursorAgentFailureUnsafeConfigurationFailsClosedWithoutRotation(t *testing.T) {
	for _, cause := range []error{
		errCursorAgentUnsafeEndpoint,
		errCursorAgentProxyUnresolved,
		errCursorAgentProxyInvalid,
	} {
		err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), cause)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Equal(t, NextAccountStop, failover.NextAccountAction)
		require.False(t, failover.RetryableOnSameAccount)
		require.False(t, failover.RequestScopedTransient)
		require.Equal(t, GatewayFailureScopeProvider, failover.Scope)
	}
}

func TestOpenCursorAgentStreamInvalidatesExactBearerSentUpstream(t *testing.T) {
	t.Cleanup(resetCursorAgentClients)
	cache := newCursorLifecycleTokenCache()
	account := cursorGatewayAccount()
	account.ID = 812
	staleA := cursorLifecycleJWT(t, "client", time.Now().Add(2*time.Hour))
	cachedB := cursorLifecycleJWT(t, "client", time.Now().Add(3*time.Hour))
	account.Credentials = map[string]any{
		"access_token": staleA,
		"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	}
	cache.values[CursorTokenCacheKey(account)] = cachedB
	provider := NewCursorTokenProvider(nil, cache)

	var sentBearer string
	svc := &OpenAIGatewayService{
		cursorTokenProvider: provider,
		cursorAgentStreamOpener: func(_ context.Context, _ cursorpkg.AgentRunParams, opts cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
			sentBearer = opts.Token
			return nil, &cursorpkg.AgentError{
				Code: "unauthenticated", Message: "rejected", Raw: `{"code":"unauthenticated"}`, HTTPStatus: http.StatusUnauthorized,
			}
		},
	}
	c := cursorGatewayTestContext(t)
	stream, err := svc.openCursorAgentStream(context.Background(), c, account, cursorpkg.AgentRunParams{Prompt: "private prompt", Model: "default"})
	require.Nil(t, stream)
	require.Equal(t, cachedB, sentBearer)

	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, GatewayFailureStageAccountAuth, failover.Stage)
	require.Equal(t, CursorCredentialReasonExpired, failover.Reason)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.NotContains(t, string(failover.ResponseBody), cachedB)

	cache.mu.Lock()
	require.Equal(t, cursorTokenFingerprint(cachedB), cache.values[cursorForceRefreshCacheKey(CursorTokenCacheKey(account))])
	// Simulate a stale worker republishing B after invalidation. The rejection
	// marker must still make the next attempt refuse the bearer actually sent.
	cache.values[CursorTokenCacheKey(account)] = cachedB
	cache.mu.Unlock()
	next, nextErr := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, nextErr)
	require.Equal(t, staleA, next)
	require.NotEqual(t, cachedB, next)
}

func TestCursorAgentFailureNotLoggedInRotatesExactRequestBearer(t *testing.T) {
	cache := newCursorLifecycleTokenCache()
	account := cursorGatewayAccount()
	account.ID = 813
	provider := NewCursorTokenProvider(nil, cache)
	c := cursorGatewayTestContext(t)
	c.Set(cursorAgentBearerContextKey, "actual-bearer-B")

	err := (&OpenAIGatewayService{cursorTokenProvider: provider}).cursorAgentFailure(c, account, &cursorpkg.AgentError{
		Code: "permission_denied", Message: "ERROR_NOT_LOGGED_IN", HTTPStatus: http.StatusForbidden,
	})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, CursorCredentialReasonWebSession, failover.Reason)
	cache.mu.Lock()
	rejected := cache.values[cursorForceRefreshCacheKey(CursorTokenCacheKey(account))]
	cache.mu.Unlock()
	require.Equal(t, cursorTokenFingerprint("actual-bearer-B"), rejected)
}

func TestCursorAgentFailurePreservesInvalidationErrorsAndFailsClosedLocally(t *testing.T) {
	bearer := cursorLifecycleJWT(t, cursorpkg.TokenTypeSession, time.Now().Add(3*time.Hour))
	account := cursorGatewayAccount()
	account.ID = 814
	account.Credentials = map[string]any{
		"access_token": bearer,
		"expires_at":   time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339),
	}
	deleteFailure := errors.New("cache delete unavailable")
	markerFailure := errors.New("cache marker unavailable")
	cache := newCursorLifecycleTokenCache()
	cache.values[CursorTokenCacheKey(account)] = bearer
	cache.deleteErr = deleteFailure
	cache.setErr = markerFailure
	provider := NewCursorTokenProvider(nil, cache)
	c := cursorGatewayTestContext(t)
	c.Set(cursorAgentBearerContextKey, bearer)
	svc := &OpenAIGatewayService{cursorTokenProvider: provider}

	invalidateErr := invalidateCursorRequestBearer(svc, c, account)
	require.ErrorIs(t, invalidateErr, deleteFailure)
	require.ErrorIs(t, invalidateErr, markerFailure)

	err := svc.cursorAgentFailure(c, account, &cursorpkg.AgentError{
		Code: "unauthenticated", Message: "rejected", HTTPStatus: http.StatusUnauthorized,
	})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Condition(t, func() bool {
		for _, event := range events {
			if event != nil && event.Reason == "cursor_rejection_marker_degraded" &&
				event.Kind == "credential_invalidation_degraded" && !strings.Contains(event.Message, bearer) {
				return true
			}
		}
		return false
	}, "cache failure must produce a generic, secret-free diagnostic")

	cache.mu.Lock()
	cache.values[CursorTokenCacheKey(account)] = bearer
	cache.mu.Unlock()
	got, nextErr := provider.GetAccessToken(context.Background(), account)
	require.Empty(t, got)
	require.ErrorIs(t, nextErr, errCursorAccessTokenRejected)
}

func TestCursorAgentFailureGenericVerdictIsSanitized(t *testing.T) {
	secret := "crsr_super_secret_private_prompt"
	err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), &cursorpkg.AgentError{
		Code: "internal", Message: secret, Raw: `{"message":"` + secret + `"}`, HTTPStatus: http.StatusBadGateway,
	})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.NotContains(t, string(failover.ResponseBody), secret)
	require.NotContains(t, failover.ClientMessage, secret)
	require.NotContains(t, err.Error(), secret)
}

func TestCursorRequestOutputLimitKeepsCurrentBridgePrecedence(t *testing.T) {
	legacy, modern := 128, 64
	require.Equal(t, 64, cursorRequestOutputLimit(&apicompat.ChatCompletionsRequest{
		MaxTokens: &legacy, MaxCompletionTokens: &modern,
	}))
	zero := 0
	require.Zero(t, cursorRequestOutputLimit(&apicompat.ChatCompletionsRequest{MaxCompletionTokens: &zero}))
}

func TestCursorAgentClientVersionDetectionIsDefensive(t *testing.T) {
	for _, marker := range []string{"Update Required", "unsupported_client", "client version too old", "minimum version"} {
		require.True(t, isCursorClientVersionRejected(&cursorpkg.AgentError{Message: marker}), marker)
	}
	require.False(t, isCursorClientVersionRejected(&cursorpkg.AgentError{Message: "ordinary permission denial"}))
	require.False(t, isCursorClientVersionRejected(nil))
}

func TestCursorAgentFailureDoesNotExposeCredentialFromMalformedEndpoint(t *testing.T) {
	secret := "cursor-secret"
	cause := validateCursorAgentHost(nil, "https://user:"+secret+"@agentn.global.api5.cursor.sh", true)
	require.Error(t, cause)
	require.NotContains(t, cause.Error(), secret)

	err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), cause)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, NextAccountStop, failover.NextAccountAction)
	require.NotContains(t, strings.ToLower(string(failover.ResponseBody)), secret)
}

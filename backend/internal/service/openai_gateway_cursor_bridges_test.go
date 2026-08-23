package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

type cursorBridgeRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip cursorBridgeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
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

func TestCursorAgentFailureNon2xxConnectRateLimitKeepsMappedPolicyAndActualCapture(t *testing.T) {
	client := &http.Client{Transport: cursorBridgeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Status: "503 Service Unavailable", StatusCode: http.StatusServiceUnavailable,
			Proto: "HTTP/2.0", ProtoMajor: 2, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"resource_exhausted","message":"capacity"}}`)),
		}, nil
	})}
	_, openErr := cursorpkg.OpenAgentStream(context.Background(), cursorpkg.AgentRunParams{Prompt: "local test"}, cursorpkg.AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "test-token", HTTPClient: client,
	})
	var agentErr *cursorpkg.AgentError
	require.ErrorAs(t, openErr, &agentErr)
	require.Equal(t, http.StatusTooManyRequests, agentErr.HTTPStatus)
	require.True(t, agentErr.HasHTTPResponse)
	require.Equal(t, http.StatusServiceUnavailable, agentErr.ActualHTTPStatus)

	err := (&OpenAIGatewayService{}).cursorAgentFailure(cursorGatewayTestContext(t), cursorGatewayAccount(), agentErr)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.True(t, failover.RequestScopedTransient, "mapped 429 switches account without quarantine")
	require.False(t, failover.RetryableOnSameAccount)
	require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
	require.True(t, failover.HasUpstreamHTTPResponse)
	require.Equal(t, http.StatusServiceUnavailable, failover.UpstreamHTTPStatus, "capture uses actual HTTP 503")
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

func cursorProtocolTestContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

func cursorBridgeEvents() []cursorpkg.AgentEvent {
	return []cursorpkg.AgentEvent{
		{Type: cursorpkg.AgentEventThinking, Text: "careful"},
		{Type: cursorpkg.AgentEventText, Text: "answer"},
		{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`}},
		{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "call_time", Name: "time", Arguments: `{"tz":"UTC"}`}},
		{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{InputTokens: 13, OutputTokens: 8, CacheReadTokens: 2, CacheWriteTokens: 1}},
	}
}

func cursorBridgeStream(events ...cursorpkg.AgentEvent) cursorChatEventStream {
	return cursorChatTestStreamWithHeader(events...)
}

// Removing either DEV request converter, or bypassing buildCursorAgentRunParams
// for one protocol, breaks this parity contract. Expected values come from the
// independently-authored Chat request rather than a production bridge helper.
func TestCursorRunParamsIdenticalAcrossInboundProtocols(t *testing.T) {
	chatJSON := `{
		"model":"auto","max_completion_tokens":128,
		"messages":[
			{"role":"system","content":"be precise"},
			{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"found"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"look up","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]
	}`
	responsesJSON := `{
		"model":"auto","max_output_tokens":128,"instructions":"be precise",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="}]},
			{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"found"}
		],
		"tools":[{"type":"function","name":"lookup","description":"look up","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]
	}`
	messagesJSON := `{
		"model":"auto","max_tokens":128,"system":"be precise",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"found"}]}
		],
		"tools":[{"name":"lookup","description":"look up","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
	}`

	var chatReq apicompat.ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(chatJSON), &chatReq))
	var responsesReq apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(responsesJSON), &responsesReq))
	responsesChat, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	require.NoError(t, err)
	var messagesReq apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal([]byte(messagesJSON), &messagesReq))
	messagesChat, err := apicompat.AnthropicToChatCompletionsRequest(&messagesReq)
	require.NoError(t, err)

	build := func(req *apicompat.ChatCompletionsRequest) (cursorpkg.AgentRunParams, cursorInputEstimate) {
		params, input, buildErr := buildCursorAgentRunParams("auto", req, cursorTranslateOptions{
			nativeTools: true, nativeImages: true, cwd: cursorpkg.AgentDefaultCwd,
		})
		require.NoError(t, buildErr)
		return params, input
	}
	wantParams, wantInput := build(&chatReq)
	responsesParams, responsesInput := build(responsesChat)
	messagesParams, messagesInput := build(messagesChat)
	require.Equal(t, wantParams, responsesParams)
	require.Equal(t, wantParams, messagesParams)
	require.Equal(t, wantInput, responsesInput)
	require.Equal(t, wantInput, messagesInput)
	require.Equal(t, 128, cursorRequestOutputLimit(&chatReq))
	require.Equal(t, 128, cursorRequestOutputLimit(responsesChat))
	require.Equal(t, 128, cursorRequestOutputLimit(messagesChat))
}

func TestCursorResponsesBufferedUsesNativeShapeReasoningParallelToolsAndUsage(t *testing.T) {
	c, recorder := cursorProtocolTestContext(t, "/v1/responses")
	result, err := (&OpenAIGatewayService{}).bufferCursorResponses(
		c, cursorGatewayAccount(), cursorBridgeStream(cursorBridgeEvents()...), cursorChatTestMeta(false, false),
		cursorInputEstimate{}, time.Now(), nil, false, nil,
	)
	require.NoError(t, err)
	require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
	var response apicompat.ResponsesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "response", response.Object)
	require.Equal(t, "completed", response.Status)
	require.Equal(t, "caller-model", response.Model)
	require.Len(t, response.Output, 4)
	require.Equal(t, "reasoning", response.Output[0].Type)
	require.Equal(t, "message", response.Output[1].Type)
	require.Equal(t, "answer", response.Output[1].Content[0].Text)
	require.Equal(t, "function_call", response.Output[2].Type)
	require.Equal(t, "call_weather", response.Output[2].CallID)
	require.Equal(t, "function_call", response.Output[3].Type)
	require.Equal(t, "call_time", response.Output[3].CallID)
	require.Equal(t, 13, response.Usage.InputTokens)
	require.Equal(t, 8, response.Usage.OutputTokens)
	require.Equal(t, "cursor-request-id", result.RequestID)
	require.True(t, result.CaptureResponseComplete)
	require.NotContains(t, recorder.Body.String(), "chat.completion")
	require.NotContains(t, recorder.Body.String(), "connect_proto")
}

func TestCursorResponsesSSEUsesNativeLifecycleParallelToolsUsageAndIncompleteLength(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		c, recorder := cursorProtocolTestContext(t, "/v1/responses")
		result, err := (&OpenAIGatewayService{}).streamCursorResponses(
			c, cursorGatewayAccount(), cursorBridgeStream(cursorBridgeEvents()...), cursorChatTestMeta(true, false),
			cursorInputEstimate{}, time.Now(), nil, false, nil,
		)
		require.NoError(t, err)
		body := recorder.Body.String()
		require.Contains(t, body, "event: response.created")
		require.Contains(t, body, "event: response.reasoning_summary_text.delta")
		require.Equal(t, 2, strings.Count(body, "event: response.output_item.added")-2, "two function items plus reasoning and message")
		require.Contains(t, body, `"input_tokens":13`)
		require.Contains(t, body, `"cache_creation_input_tokens":1`)
		require.Contains(t, body, `"cached_tokens":2`)
		require.Contains(t, body, "event: response.completed")
		require.Equal(t, 1, strings.Count(body, "data: [DONE]\n\n"))
		require.NotContains(t, body, "chat.completion.chunk")
		require.NotContains(t, body, "exec_stream_close")
		require.True(t, result.CaptureResponseComplete)
	})

	t.Run("local length", func(t *testing.T) {
		c, recorder := cursorProtocolTestContext(t, "/v1/responses")
		meta := cursorChatTestMeta(true, false)
		meta.maxOutputTokens = 1
		result, err := (&OpenAIGatewayService{}).streamCursorResponses(
			c, cursorGatewayAccount(), cursorBridgeStream(
				cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: strings.Repeat("a", 100)},
				cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true},
			), meta, cursorInputEstimate{}, time.Now(), nil, false, nil,
		)
		require.NoError(t, err)
		body := recorder.Body.String()
		require.Contains(t, body, `"status":"incomplete"`)
		require.Contains(t, body, `"reason":"max_output_tokens"`)
		require.False(t, result.CaptureResponseComplete)
	})
}

func TestCursorAnthropicBufferedUsesNativeThinkingParallelToolsUsageAndMaxTokens(t *testing.T) {
	c, recorder := cursorProtocolTestContext(t, "/v1/messages")
	result, err := (&OpenAIGatewayService{}).bufferCursorAnthropic(
		c, cursorGatewayAccount(), cursorBridgeStream(cursorBridgeEvents()...), cursorChatTestMeta(false, false),
		cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	var response apicompat.AnthropicResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "message", response.Type)
	require.Equal(t, "caller-model", response.Model)
	require.Len(t, response.Content, 4)
	require.Equal(t, "thinking", response.Content[0].Type)
	require.Equal(t, "text", response.Content[1].Type)
	require.Equal(t, "tool_use", response.Content[2].Type)
	require.Equal(t, "tool_use", response.Content[3].Type)
	require.Equal(t, "tool_use", apicompat.AnthropicStopReasonString(response.StopReason))
	require.Equal(t, 13, response.Usage.InputTokens+response.Usage.CacheReadInputTokens+response.Usage.CacheCreationInputTokens)
	require.Equal(t, 8, response.Usage.OutputTokens)
	require.True(t, result.CaptureResponseComplete)
	require.NotContains(t, recorder.Body.String(), "chat.completion")
}

func TestCursorAnthropicSSEUsesNativeLifecycleParallelToolsUsageAndMaxTokenStop(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		c, recorder := cursorProtocolTestContext(t, "/v1/messages")
		result, err := (&OpenAIGatewayService{}).streamCursorAnthropic(
			c, cursorGatewayAccount(), cursorBridgeStream(cursorBridgeEvents()...), cursorChatTestMeta(true, false),
			cursorInputEstimate{}, time.Now(),
		)
		require.NoError(t, err)
		body := recorder.Body.String()
		require.Contains(t, body, "event: message_start")
		require.Contains(t, body, `"type":"thinking_delta"`)
		require.Equal(t, 2, strings.Count(body, `"type":"tool_use"`))
		require.Contains(t, body, `"input_tokens":10`)
		require.Contains(t, body, `"cache_creation_input_tokens":1`)
		require.Contains(t, body, `"cache_read_input_tokens":2`)
		require.Contains(t, body, "event: message_stop")
		require.NotContains(t, body, "[DONE]")
		require.NotContains(t, body, "chat.completion.chunk")
		require.True(t, result.CaptureResponseComplete)
	})

	t.Run("local max tokens", func(t *testing.T) {
		c, recorder := cursorProtocolTestContext(t, "/v1/messages")
		meta := cursorChatTestMeta(true, false)
		meta.maxOutputTokens = 1
		result, err := (&OpenAIGatewayService{}).streamCursorAnthropic(
			c, cursorGatewayAccount(), cursorBridgeStream(cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: strings.Repeat("z", 100)}),
			meta, cursorInputEstimate{}, time.Now(),
		)
		require.NoError(t, err)
		require.Contains(t, recorder.Body.String(), `"stop_reason":"max_tokens"`)
		require.False(t, result.CaptureResponseComplete)
	})
}

func TestCursorResponsesMidStreamFailureUsesNativeErrorWithoutCompleted(t *testing.T) {
	c, recorder := cursorProtocolTestContext(t, "/v1/responses")
	secret := "private-provider-prompt-token"
	result, err := (&OpenAIGatewayService{}).streamCursorResponses(
		c, cursorGatewayAccount(), cursorBridgeStream(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "partial"},
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: &cursorpkg.AgentError{Code: "internal", Message: secret, Raw: secret, HTTPStatus: http.StatusBadGateway}},
		), cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now(), nil, false, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	body := recorder.Body.String()
	require.Contains(t, body, "event: error")
	require.NotContains(t, body, "response.completed")
	require.NotContains(t, body, secret)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
}

func TestCursorAnthropicMidStreamFailureUsesNativeErrorWithoutMessageStop(t *testing.T) {
	c, recorder := cursorProtocolTestContext(t, "/v1/messages")
	secret := "private-provider-prompt-token"
	result, err := (&OpenAIGatewayService{}).streamCursorAnthropic(
		c, cursorGatewayAccount(), cursorBridgeStream(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "partial"},
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: &cursorpkg.AgentError{Code: "internal", Message: secret, Raw: secret, HTTPStatus: http.StatusBadGateway}},
		), cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now(),
	)
	require.NoError(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, "event: error")
	require.NotContains(t, body, "event: message_stop")
	require.NotContains(t, body, secret)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
}

func TestCursorResponsesAndAnthropicPreOutputFailuresWithholdProtocolBytes(t *testing.T) {
	providerErr := &cursorpkg.AgentError{Code: "unavailable", Message: "secret", Raw: `{"token":"secret"}`, HTTPStatus: http.StatusServiceUnavailable}
	tests := []struct {
		name string
		path string
		run  func(*gin.Context) (*OpenAIForwardResult, error)
	}{
		{name: "responses", path: "/v1/responses", run: func(c *gin.Context) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorResponses(c, cursorGatewayAccount(), cursorBridgeStream(cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: providerErr}), cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now(), nil, false, nil)
		}},
		{name: "anthropic", path: "/v1/messages", run: func(c *gin.Context) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorAnthropic(c, cursorGatewayAccount(), cursorBridgeStream(cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: providerErr}), cursorChatTestMeta(true, false), cursorInputEstimate{}, time.Now())
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := cursorProtocolTestContext(t, test.path)
			result, err := test.run(c)
			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Empty(t, recorder.Body.Bytes())
			require.False(t, IsResponseCommitted(c))
		})
	}
}

func TestCursorResponsesAndAnthropicWriterFailureUseBoundedRelay(t *testing.T) {
	tests := []struct {
		name string
		path string
		run  func(*gin.Context, cursorChatEventStream, cursorChatMeta) (*OpenAIForwardResult, error)
	}{
		{name: "responses", path: "/v1/responses", run: func(c *gin.Context, stream cursorChatEventStream, meta cursorChatMeta) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorResponses(c, cursorGatewayAccount(), stream, meta, cursorInputEstimate{}, time.Now(), nil, false, nil)
		}},
		{name: "anthropic", path: "/v1/messages", run: func(c *gin.Context, stream cursorChatEventStream, meta cursorChatMeta) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorAnthropic(c, cursorGatewayAccount(), stream, meta, cursorInputEstimate{}, time.Now())
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := cursorProtocolTestContext(t, test.path)
			failed := make(chan struct{})
			c.Writer = &cursorChatFailingWriter{ResponseWriter: c.Writer, failAt: 2, failed: failed}
			stream := newCursorAsyncChatTestStream()
			defer stream.finish()
			meta := cursorChatTestMeta(true, false)
			meta.disconnectDrainTimeout = 25 * time.Millisecond
			done := make(chan cursorChatAsyncResult, 1)
			go func() {
				result, err := test.run(c, stream, meta)
				done <- cursorChatAsyncResult{result: result, err: err}
			}()
			go func() { stream.events <- cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "visible"} }()
			select {
			case <-failed:
			case <-time.After(time.Second):
				t.Fatal("protocol writer did not reach deterministic failure")
			}
			select {
			case <-stream.closed:
			case <-time.After(time.Second):
				t.Fatal("writer-failed protocol stream was not closed at drain deadline")
			}
			got := awaitCursorChatAsyncResult(t, done)
			require.NoError(t, got.err)
			require.True(t, got.result.ClientDisconnect)
			require.False(t, got.result.CaptureResponseComplete)
		})
	}
}

func TestCursorResponsesAndAnthropicCallerCancellationDrainsBoundedly(t *testing.T) {
	tests := []struct {
		name string
		path string
		run  func(*gin.Context, cursorChatEventStream, cursorChatMeta) (*OpenAIForwardResult, error)
	}{
		{name: "responses", path: "/v1/responses", run: func(c *gin.Context, stream cursorChatEventStream, meta cursorChatMeta) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorResponses(c, cursorGatewayAccount(), stream, meta, cursorInputEstimate{}, time.Now(), nil, false, nil)
		}},
		{name: "anthropic", path: "/v1/messages", run: func(c *gin.Context, stream cursorChatEventStream, meta cursorChatMeta) (*OpenAIForwardResult, error) {
			return (&OpenAIGatewayService{}).streamCursorAnthropic(c, cursorGatewayAccount(), stream, meta, cursorInputEstimate{}, time.Now())
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := cursorProtocolTestContext(t, test.path)
			requestCtx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(requestCtx)
			stream := newCursorAsyncChatTestStream()
			defer stream.finish()
			meta := cursorChatTestMeta(true, false)
			meta.disconnectDrainTimeout = 100 * time.Millisecond
			done := make(chan cursorChatAsyncResult, 1)
			go func() {
				result, err := test.run(c, stream, meta)
				done <- cursorChatAsyncResult{result: result, err: err}
			}()
			go func() {
				<-requestCtx.Done()
				stream.events <- cursorpkg.AgentEvent{
					Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true,
					Usage: &cursorpkg.AgentUsage{InputTokens: 17, OutputTokens: 9},
				}
				stream.finish()
			}()
			cancel()
			got := awaitCursorChatAsyncResult(t, done)
			require.NoError(t, got.err)
			require.True(t, got.result.ClientDisconnect)
			require.True(t, got.result.CaptureResponseComplete)
			require.Equal(t, OpenAIUsage{InputTokens: 17, OutputTokens: 9}, got.result.Usage)
			require.Empty(t, recorder.Body.Bytes())
		})
	}
}

func TestCursorResponsesAndAnthropicValidationIsNativeAndSecretFree(t *testing.T) {
	t.Run("responses", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr string
		}{
			{name: "parse", body: `{"model":"auto","input":"private-secret"`, wantErr: "invalid Responses request"},
			{name: "model", body: `{"model":"","input":"private-secret"}`, wantErr: "Responses model is required"},
			{name: "tools", body: `{"model":"auto","input":[{"type":"additional_tools","tools":"private-secret"}]}`, wantErr: "invalid Responses tools"},
			{name: "conversion", body: `{"model":"auto","instructions":"private-secret","input":42}`, wantErr: "invalid Responses request"},
			{name: "zero max output tokens", body: `{"model":"auto","max_output_tokens":0,"input":"private-secret"}`, wantErr: "max_output_tokens must be positive"},
			{name: "negative max output tokens", body: `{"model":"auto","max_output_tokens":-7,"input":"private-secret"}`, wantErr: "max_output_tokens must be positive"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				c, recorder := cursorProtocolTestContext(t, "/v1/responses")
				result, err := (&OpenAIGatewayService{}).forwardCursorResponses(
					context.Background(), c, cursorGatewayAccount(), []byte(test.body), "", false, time.Now(),
				)
				require.Nil(t, result)
				require.Error(t, err)
				require.ErrorContains(t, err, test.wantErr)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				var response struct {
					Error map[string]json.RawMessage `json:"error"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.JSONEq(t, `"invalid_request_error"`, string(response.Error["type"]))
				require.NotContains(t, response.Error, "code")
				require.NotContains(t, recorder.Body.String(), "private-secret")
				require.NotContains(t, recorder.Body.String(), "chat.completion")
			})
		}
	})
	t.Run("anthropic requires positive max_tokens", func(t *testing.T) {
		c, recorder := cursorProtocolTestContext(t, "/v1/messages")
		body := []byte(`{"model":"auto","max_tokens":0,"messages":[{"role":"user","content":"private-secret"}]}`)
		result, err := (&OpenAIGatewayService{}).forwardCursorAnthropic(context.Background(), c, cursorGatewayAccount(), body, "")
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, recorder.Body.String(), `"type":"invalid_request_error"`)
		require.NotContains(t, recorder.Body.String(), "private-secret")
		require.NotContains(t, recorder.Body.String(), "chat.completion")
	})
}

func TestCursorResponsesBufferedCachesReasoningForEncryptedReplay(t *testing.T) {
	assertCursorResponsesReasoningCacheRoundTrip(t, false)
}

func TestCursorResponsesStreamingCachesReasoningForEncryptedReplay(t *testing.T) {
	assertCursorResponsesReasoningCacheRoundTrip(t, true)
}

func TestCursorResponsesAbsentMaxOutputTokensRemainsAllowed(t *testing.T) {
	paramsSeen := make(chan cursorpkg.AgentRunParams, 1)
	svc := &OpenAIGatewayService{cursorAgentStreamOpener: cursorChatEOFStreamOpener(t, paramsSeen)}
	c, recorder := cursorProtocolTestContext(t, "/v1/responses")
	result, err := svc.forwardCursorResponses(
		context.Background(), c, cursorChatForwardAccount(t),
		[]byte(`{"model":"auto","input":"hello"}`), "", false, time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "hello", (<-paramsSeen).Prompt)
}

type cursorReasoningRecordingCache struct {
	stubGatewayCache
	mu      sync.Mutex
	sets    map[string]string
	getResp map[string]string
}

func (cache *cursorReasoningRecordingCache) SetReasoningContent(
	_ context.Context,
	itemID string,
	content string,
	_ time.Duration,
) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.sets == nil {
		cache.sets = make(map[string]string)
	}
	cache.sets[itemID] = content
	return nil
}

func (cache *cursorReasoningRecordingCache) GetReasoningContent(_ context.Context, itemID string) (string, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if content, ok := cache.getResp[itemID]; ok {
		return content, nil
	}
	return "", ErrReasoningContentNotFound
}

func (cache *cursorReasoningRecordingCache) snapshotSets() map[string]string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	result := make(map[string]string, len(cache.sets))
	for itemID, content := range cache.sets {
		result[itemID] = content
	}
	return result
}

func (cache *cursorReasoningRecordingCache) setGetResponse(itemID, content string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.getResp[itemID] = content
}

func assertCursorResponsesReasoningCacheRoundTrip(t *testing.T, stream bool) {
	t.Helper()
	cache := &cursorReasoningRecordingCache{getResp: make(map[string]string)}
	paramsSeen := make(chan cursorpkg.AgentRunParams, 1)
	svc := &OpenAIGatewayService{
		cache:                   cache,
		cursorAgentStreamOpener: cursorChatEOFStreamOpener(t, paramsSeen),
	}
	c, _ := cursorProtocolTestContext(t, "/v1/responses")
	events := cursorBridgeStream(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "cached bridge reasoning"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{InputTokens: 4, OutputTokens: 3}},
	)
	meta := cursorChatTestMeta(stream, false)
	if stream {
		result, err := svc.streamCursorResponses(c, cursorGatewayAccount(), events, meta, cursorInputEstimate{}, time.Now(), nil, false, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
	} else {
		result, err := svc.bufferCursorResponses(c, cursorGatewayAccount(), events, meta, cursorInputEstimate{}, time.Now(), nil, false, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
	}

	sets := cache.snapshotSets()
	require.Len(t, sets, 1, "the generated reasoning item must be cached by its caller-facing id")
	var itemID string
	for id, content := range sets {
		itemID = id
		require.NotEmpty(t, id)
		require.Equal(t, "cached bridge reasoning", content)
		cache.setGetResponse(id, content)
	}

	replayBody, err := json.Marshal(map[string]any{
		"model": "auto",
		"input": []any{
			map[string]any{"type": "reasoning", "id": itemID, "summary": []any{}, "encrypted_content": "opaque-only"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": `{}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continue"}}},
		},
	})
	require.NoError(t, err)
	replayContext, replayRecorder := cursorProtocolTestContext(t, "/v1/responses")
	result, err := svc.forwardCursorResponses(
		context.Background(), replayContext, cursorChatForwardAccount(t), replayBody, "", false, time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, replayRecorder.Code)
	params := <-paramsSeen
	require.True(t, json.Valid([]byte(params.Prompt)))
	transcript := decodeCursorTranscriptForTest(t, params.Prompt)
	require.NotEmpty(t, transcript.Messages)
	require.Equal(t, "assistant", transcript.Messages[0].Role)
	require.Equal(t, "cached bridge reasoning", transcript.Messages[0].Reasoning)
	require.NotContains(t, transcript.Messages[0].Content, "cached bridge reasoning", "hidden reasoning must not become visible content")
	require.NotContains(t, params.Prompt, "opaque-only")
}

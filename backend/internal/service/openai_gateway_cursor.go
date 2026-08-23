package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const cursorAgentEndpoint = "cursor:" + cursorpkg.EndpointAgentRun

const cursorAgentBearerContextKey = "cursor_agent_request_bearer"

var cursorSafeUpstreamErrorBody = []byte(`{"error":{"type":"upstream_error","message":"Cursor upstream request failed"}}`)

type cursorChatMeta struct {
	originalModel   string
	billingModel    string
	upstreamModel   string
	stream          bool
	includeUsage    bool
	maxOutputTokens int
}

type cursorDeltaKind int

const (
	cursorDeltaText cursorDeltaKind = iota
	cursorDeltaReasoning
	cursorDeltaToolCall
)

type cursorDelta struct {
	kind          cursorDeltaKind
	text          string
	toolIndex     int
	toolID        string
	toolName      string
	toolArguments string
}

type cursorChatOutcome struct {
	content          string
	reasoning        string
	toolCalls        []apicompat.ChatToolCall
	finishReason     string
	firstTokenMs     *int
	usage            *cursorpkg.AgentUsage
	truncated        bool
	providerTerminal bool
	tokenDeltaOutput int64
}

type cursorAgentStreamOpener func(
	context.Context,
	cursorpkg.AgentRunParams,
	cursorpkg.AgentStreamOptions,
) (*cursorpkg.AgentStream, error)

func (s *OpenAIGatewayService) forwardCursorChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		writeCursorChatValidationError(c, "Failed to parse request body")
		return nil, errors.New("cursor: invalid chat completions request")
	}
	if strings.TrimSpace(chatReq.Model) == "" {
		writeCursorChatValidationError(c, "model is required")
		return nil, errors.New("cursor: chat completions model is required")
	}

	meta := s.resolveCursorChatMeta(account, chatReq.Model, defaultMappedModel, chatReq.Stream)
	meta.includeUsage = chatReq.StreamOptions != nil && chatReq.StreamOptions.IncludeUsage
	meta.maxOutputTokens = cursorRequestOutputLimit(&chatReq)
	params, input, err := buildCursorAgentRun(account, meta.upstreamModel, &chatReq)
	if err != nil {
		writeCursorChatValidationError(c, "Invalid Chat Completions request")
		return nil, errors.New("cursor: invalid chat completions request")
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, meta.stream)
	stream, err := s.openCursorAgentStream(upstreamCtx, c, account, params)
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	if meta.stream {
		return s.streamCursorChatCompletions(c, account, stream, meta, input, startTime)
	}
	return s.bufferCursorChatCompletions(c, account, stream, meta, input, startTime)
}

func (s *OpenAIGatewayService) resolveCursorChatMeta(account *Account, requestedModel, defaultMappedModel string, stream bool) cursorChatMeta {
	billingModel := resolveOpenAIForwardModel(account, requestedModel, defaultMappedModel)
	return cursorChatMeta{
		originalModel: requestedModel,
		billingModel:  billingModel,
		upstreamModel: strings.TrimSpace(billingModel),
		stream:        stream,
	}
}

func (s *OpenAIGatewayService) openCursorAgentStream(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	params cursorpkg.AgentRunParams,
) (*cursorpkg.AgentStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if account == nil || !account.IsCursorOAuth() {
		return nil, s.newCursorCredentialFailover(c, account, errCursorCredentialsMissing)
	}
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, s.newCursorCredentialFailover(c, account, err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, s.newCursorCredentialFailover(c, account, errCursorAccessTokenMissing)
	}

	baseURL, accountOverride := cursorAgentBaseURLSource(account)
	if err := validateCursorAgentHost(s.cfg, baseURL, accountOverride); err != nil {
		return nil, s.cursorAgentFailure(c, account, err)
	}
	httpClient, err := cursorAgentHTTPClient(account)
	if err != nil {
		return nil, s.cursorAgentFailure(c, account, err)
	}

	actualBearer, _ := cursorpkg.ParseToken(token)
	if strings.TrimSpace(actualBearer) == "" {
		actualBearer = strings.TrimSpace(token)
	}
	if c != nil {
		c.Set(cursorAgentBearerContextKey, actualBearer)
		SetActualOpenAIUpstreamEndpoint(c, cursorAgentEndpoint)
	}

	defaults := cursorAgentProcessDefaults()
	opener := cursorpkg.OpenAgentStream
	if s != nil && s.cursorAgentStreamOpener != nil {
		opener = s.cursorAgentStreamOpener
	}
	stream, err := opener(ctx, params, cursorpkg.AgentStreamOptions{
		BaseURL:          baseURL,
		Token:            token,
		ClientVersion:    cursorAgentClientVersion(account),
		GhostMode:        cursorAgentGhostMode(account),
		RequestID:        uuid.NewString(),
		HTTPClient:       httpClient,
		FirstByteTimeout: defaults.firstByteTimeout,
		IdleTimeout:      defaults.idleTimeout,
	})
	if err != nil {
		return nil, s.cursorAgentFailure(c, account, err)
	}
	return stream, nil
}

func consumeCursorAgentEvents(
	events <-chan cursorpkg.AgentEvent,
	startTime time.Time,
	maxOutputTokens int,
	onDelta func(cursorDelta) error,
) (cursorChatOutcome, error) {
	outcome := cursorChatOutcome{finishReason: "stop"}
	var contentBuilder, reasoningBuilder strings.Builder
	toolIndexByID := make(map[string]int)
	spentTokens := 0
	limited := maxOutputTokens > 0

	admit := func(text string) (string, bool) {
		if !limited || text == "" {
			return text, false
		}
		remaining := maxOutputTokens - spentTokens
		if remaining <= 0 {
			return "", true
		}
		fitted, cost := cursorFitTextToTokenBudget(text, remaining)
		spentTokens = saturatingAddNonnegativeInt(spentTokens, cost)
		return fitted, fitted != text
	}
	markFirstToken := func() {
		if outcome.firstTokenMs != nil {
			return
		}
		elapsed := time.Since(startTime).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		maxInt := int64(^uint(0) >> 1)
		if elapsed > maxInt {
			elapsed = maxInt
		}
		value := int(elapsed)
		outcome.firstTokenMs = &value
	}
	emit := func(delta cursorDelta) error {
		if onDelta == nil {
			return nil
		}
		return onDelta(delta)
	}
	finish := func(err error) (cursorChatOutcome, error) {
		outcome.content = contentBuilder.String()
		outcome.reasoning = reasoningBuilder.String()
		return outcome, err
	}
	truncate := func() (cursorChatOutcome, error) {
		outcome.truncated = true
		outcome.finishReason = "length"
		outcome.providerTerminal = false
		return finish(nil)
	}

	for event := range events {
		switch event.Type {
		case cursorpkg.AgentEventText:
			if event.Text == "" {
				continue
			}
			fitted, dropped := admit(event.Text)
			if fitted != "" {
				markFirstToken()
				contentBuilder.WriteString(fitted)
				if err := emit(cursorDelta{kind: cursorDeltaText, text: fitted}); err != nil {
					return finish(err)
				}
			}
			if dropped {
				return truncate()
			}

		case cursorpkg.AgentEventThinking:
			if event.Text == "" {
				continue
			}
			fitted, dropped := admit(event.Text)
			if fitted != "" {
				markFirstToken()
				reasoningBuilder.WriteString(fitted)
				if err := emit(cursorDelta{kind: cursorDeltaReasoning, text: fitted}); err != nil {
					return finish(err)
				}
			}
			if dropped {
				return truncate()
			}

		case cursorpkg.AgentEventToolCall:
			if event.ToolCall == nil {
				continue
			}
			callID := strings.TrimSpace(event.ToolCall.ID)
			if callID != "" {
				if _, duplicate := toolIndexByID[callID]; duplicate {
					continue
				}
			}
			if limited {
				remaining := maxOutputTokens - spentTokens
				toolCost := estimateTokensForText(event.ToolCall.Name + event.ToolCall.Arguments)
				if remaining <= 0 || toolCost > remaining {
					return truncate()
				}
				spentTokens = saturatingAddNonnegativeInt(spentTokens, toolCost)
			}
			markFirstToken()
			index := len(outcome.toolCalls)
			if callID != "" {
				toolIndexByID[callID] = index
			}
			outcome.toolCalls = append(outcome.toolCalls, apicompat.ChatToolCall{
				Index: intPtr(index), ID: event.ToolCall.ID, Type: "function",
				Function: apicompat.ChatFunctionCall{Name: event.ToolCall.Name, Arguments: event.ToolCall.Arguments},
			})
			outcome.finishReason = "tool_calls"
			if err := emit(cursorDelta{
				kind: cursorDeltaToolCall, toolIndex: index, toolID: event.ToolCall.ID,
				toolName: event.ToolCall.Name, toolArguments: event.ToolCall.Arguments,
			}); err != nil {
				return finish(err)
			}

		case cursorpkg.AgentEventTokenDelta:
			if event.Usage != nil && event.Usage.OutputTokens > 0 {
				outcome.tokenDeltaOutput = saturatingAddNonnegativeInt64(outcome.tokenDeltaOutput, event.Usage.OutputTokens)
			}

		case cursorpkg.AgentEventTurnEnded:
			outcome.usage = event.Usage
			outcome.providerTerminal = event.ProviderTerminal
			return finish(nil)

		case cursorpkg.AgentEventError:
			if event.Err == nil {
				return finish(errors.New("cursor: upstream stream failed"))
			}
			return finish(event.Err)

		default:
			// Heartbeats, thinking-end and partial built-in tool controls have no
			// caller-visible representation at this protocol-neutral layer.
		}
	}
	return finish(nil)
}

func cursorFitTextToTokenBudget(text string, budget int) (string, int) {
	if budget <= 0 || text == "" {
		return "", 0
	}
	if cost := estimateTokensForText(text); cost <= budget {
		return text, cost
	}
	runes := []rune(text)
	bestLength, bestCost := 0, 0
	for low, high := 1, len(runes); low <= high; {
		middle := (low + high) / 2
		cost := estimateTokensForText(string(runes[:middle]))
		if cost <= budget {
			bestLength, bestCost = middle, cost
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return string(runes[:bestLength]), bestCost
}

func (s *OpenAIGatewayService) cursorAgentFailure(c *gin.Context, account *Account, err error) error {
	if err == nil {
		err = errors.New("cursor: upstream request failed")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	if errors.Is(err, errCursorAgentUnsafeEndpoint) || errors.Is(err, errCursorAgentProxyUnresolved) ||
		errors.Is(err, errCursorAgentProxyInvalid) || errors.Is(err, errCursorAgentTransport) {
		appendCursorFailureEvent(c, account, 0, GatewayFailureScopeProvider, "cursor_transport_config", "config_error")
		return &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody),
			Platform: PlatformCursor, Scope: GatewayFailureScopeProvider, NextAccountAction: NextAccountStop,
			ClientStatusCode: http.StatusBadGateway, ClientMessage: "Cursor upstream configuration is unavailable",
		}
	}

	var agentErr *cursorpkg.AgentError
	if !errors.As(err, &agentErr) {
		appendCursorFailureEvent(c, account, 0, GatewayFailureScopeRequest, "cursor_transport_transient", "failover")
		return &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody),
			Platform: PlatformCursor, Scope: GatewayFailureScopeRequest, NextAccountAction: NextAccountRetry,
			RetryableOnSameAccount: true, RequestScopedTransient: true,
			ClientStatusCode: http.StatusBadGateway, ClientMessage: "Cursor upstream request failed",
		}
	}

	status := agentErr.HTTPStatus
	if status <= 0 {
		status = cursorpkg.ConnectCodeToHTTPStatus(agentErr.Code)
	}
	if isCursorNotLoggedIn(agentErr) {
		if invalidateErr := invalidateCursorRequestBearer(s, c, account); invalidateErr != nil {
			appendCursorInvalidationFailureEvent(c, account, status)
		}
		appendCursorFailureEvent(c, account, status, GatewayFailureScopeAccount, string(CursorCredentialReasonWebSession), "credential_failover")
		return applyCursorAgentHTTPProvenance(cursorCredentialVerdictFailure(CursorCredentialReasonWebSession), agentErr)
	}
	if isCursorClientVersionRejected(agentErr) {
		appendCursorFailureEvent(c, account, status, GatewayFailureScopeProvider, string(CursorCredentialReasonClientVersion), "config_error")
		return applyCursorAgentHTTPProvenance(&UpstreamFailoverError{
			StatusCode: status, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody), Platform: PlatformCursor,
			Stage: GatewayFailureStageAccountAuth, Scope: GatewayFailureScopeProvider,
			Reason: CursorCredentialReasonClientVersion, NextAccountAction: NextAccountStop,
			ClientStatusCode: http.StatusBadGateway, ClientMessage: CursorClientVersionRejectedClientMessage,
		}, agentErr)
	}

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if invalidateErr := invalidateCursorRequestBearer(s, c, account); invalidateErr != nil {
			appendCursorInvalidationFailureEvent(c, account, status)
		}
		appendCursorFailureEvent(c, account, status, GatewayFailureScopeAccount, string(CursorCredentialReasonExpired), "credential_failover")
		return applyCursorAgentHTTPProvenance(cursorCredentialVerdictFailure(CursorCredentialReasonExpired), agentErr)
	case http.StatusTooManyRequests:
		appendCursorFailureEvent(c, account, status, GatewayFailureScopeRequest, "cursor_rate_limited", "failover")
		return applyCursorAgentHTTPProvenance(&UpstreamFailoverError{
			StatusCode: http.StatusServiceUnavailable, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody),
			Platform: PlatformCursor,
			Scope:    GatewayFailureScopeRequest, NextAccountAction: NextAccountRetry, RequestScopedTransient: true,
			ClientStatusCode: http.StatusServiceUnavailable, ClientMessage: "Cursor upstream is temporarily unavailable",
		}, agentErr)
	default:
		appendCursorFailureEvent(c, account, status, GatewayFailureScopeRequest, "cursor_upstream_verdict", "failover")
		return applyCursorAgentHTTPProvenance(&UpstreamFailoverError{
			StatusCode: status, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody), Platform: PlatformCursor,
			Scope: GatewayFailureScopeRequest, NextAccountAction: NextAccountRetry,
			ClientStatusCode: http.StatusBadGateway, ClientMessage: "Cursor upstream request failed",
		}, agentErr)
	}
}

func applyCursorAgentHTTPProvenance(failure *UpstreamFailoverError, agentErr *cursorpkg.AgentError) *UpstreamFailoverError {
	if failure == nil || agentErr == nil || !agentErr.HasHTTPResponse {
		return failure
	}
	failure.HasUpstreamHTTPResponse = true
	failure.UpstreamHTTPStatus = agentErr.ActualHTTPStatus
	return failure
}

func cursorCredentialVerdictFailure(reason GatewayFailureReason) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable, ResponseBody: snapshotBytes(cursorSafeUpstreamErrorBody), Platform: PlatformCursor,
		Stage: GatewayFailureStageAccountAuth, Scope: GatewayFailureScopeAccount, Reason: reason,
		NextAccountAction: NextAccountRetry, ClientStatusCode: http.StatusServiceUnavailable,
		ClientMessage: CursorCredentialUnavailableClientMessage,
	}
}

func appendCursorFailureEvent(
	c *gin.Context,
	account *Account,
	status int,
	scope GatewayFailureScope,
	reason string,
	kind string,
) {
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: PlatformCursor, AccountID: cursorAccountID(account), AccountName: cursorAccountName(account),
		UpstreamStatusCode: status, Stage: "upstream", Scope: string(scope), Reason: reason, Kind: kind,
		Message: "Cursor upstream request failed",
	})
}

func invalidateCursorRequestBearer(s *OpenAIGatewayService, c *gin.Context, account *Account) error {
	if s == nil || s.cursorTokenProvider == nil || account == nil || !account.IsCursorOAuth() {
		return nil
	}
	bearer := ""
	if c != nil {
		if value, ok := c.Get(cursorAgentBearerContextKey); ok {
			bearer, _ = value.(string)
		}
	}
	if strings.TrimSpace(bearer) == "" {
		return nil
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = context.WithoutCancel(c.Request.Context())
	}
	return s.cursorTokenProvider.InvalidateRejectedToken(ctx, account, bearer)
}

func appendCursorInvalidationFailureEvent(c *gin.Context, account *Account, status int) {
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: PlatformCursor, AccountID: cursorAccountID(account), AccountName: cursorAccountName(account),
		UpstreamStatusCode: status, Stage: string(GatewayFailureStageAccountAuth), Scope: string(GatewayFailureScopeAccount),
		Reason: "cursor_rejection_marker_degraded", Kind: "credential_invalidation_degraded",
		Message: "Cursor rejected-token cache update failed; process-local rejection remains active",
	})
}

func isCursorNotLoggedIn(agentErr *cursorpkg.AgentError) bool {
	if agentErr == nil {
		return false
	}
	haystack := strings.ToUpper(agentErr.Code + " " + agentErr.Message + " " + agentErr.Raw)
	return strings.Contains(haystack, "ERROR_NOT_LOGGED_IN")
}

func isCursorClientVersionRejected(agentErr *cursorpkg.AgentError) bool {
	if agentErr == nil {
		return false
	}
	haystack := strings.ToUpper(agentErr.Code + " " + agentErr.Message + " " + agentErr.Raw)
	for _, marker := range []string{
		"UPDATE REQUIRED", "UPDATE_REQUIRED", "UPDATE YOUR", "PLEASE UPDATE", "OUTDATED", "OUT OF DATE",
		"UNSUPPORTED_CLIENT", "UNSUPPORTED CLIENT", "CLIENT_VERSION", "CLIENT VERSION", "CLIENT-VERSION",
		"MINIMUM VERSION", "VERSION TOO OLD", "TOO OLD", "ERROR_CLIENT_TOO_OLD",
	} {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

func resolveCursorUsage(input cursorInputEstimate, outcome cursorChatOutcome) OpenAIUsage {
	if outcome.usage != nil {
		usage := OpenAIUsage{
			InputTokens:              clampCursorTokenCount(outcome.usage.InputTokens),
			OutputTokens:             clampCursorTokenCount(outcome.usage.OutputTokens),
			CacheReadInputTokens:     clampCursorTokenCount(outcome.usage.CacheReadTokens),
			CacheCreationInputTokens: clampCursorTokenCount(outcome.usage.CacheWriteTokens),
		}
		if outcome.truncated {
			usage.OutputTokens = estimateCursorOutputTokens(outcome)
		}
		return usage
	}
	return estimateCursorUsage(input, outcome)
}

func estimateCursorUsage(input cursorInputEstimate, outcome cursorChatOutcome) OpenAIUsage {
	inputTokens := saturatingAddNonnegativeInt(estimateTokensForText(input.text), input.imageTokens)
	outputTokens := estimateCursorOutputTokens(outcome)
	if tokenDelta := clampCursorTokenCount(outcome.tokenDeltaOutput); tokenDelta > outputTokens {
		outputTokens = tokenDelta
	}
	return OpenAIUsage{InputTokens: inputTokens, OutputTokens: outputTokens}
}

func estimateCursorOutputTokens(outcome cursorChatOutcome) int {
	total := saturatingAddNonnegativeInt(estimateTokensForText(outcome.content), estimateTokensForText(outcome.reasoning))
	for _, call := range outcome.toolCalls {
		total = saturatingAddNonnegativeInt(total, estimateTokensForText(call.Function.Name+call.Function.Arguments))
	}
	return total
}

func clampCursorTokenCount(value int64) int {
	if value <= 0 {
		return 0
	}
	maxInt := uint64(^uint(0) >> 1)
	if uint64(value) > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func saturatingAddNonnegativeInt(left, right int) int {
	if left < 0 {
		left = 0
	}
	if right < 0 {
		right = 0
	}
	maxInt := int(^uint(0) >> 1)
	if right > maxInt-left {
		return maxInt
	}
	return left + right
}

func saturatingAddNonnegativeInt64(left, right int64) int64 {
	if left < 0 {
		left = 0
	}
	if right <= 0 {
		return left
	}
	maxInt64 := int64(^uint64(0) >> 1)
	if right > maxInt64-left {
		return maxInt64
	}
	return left + right
}

func cursorAccountID(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

func cursorAccountName(account *Account) string {
	if account == nil {
		return ""
	}
	return account.Name
}

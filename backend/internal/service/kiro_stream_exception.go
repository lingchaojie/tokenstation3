package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	kiroMonthlyRequestCountCode  = "MONTHLY_REQUEST_COUNT"
	kiroOverageLimitCode         = "OVERAGE_REQUEST_LIMIT_EXCEEDED"
	kiroContextLengthCode        = "CONTENT_LENGTH_EXCEEDS_THRESHOLD"
	kiroContextWindowFullMessage = "Context window is full. Reduce conversation history, system prompt, or tools."
	kiroInputTooLongMessage      = "Input is too long. Reduce the size of your messages."
)

func isKiroThinkingSignatureRejection(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	event := gjson.ParseBytes(body)
	for _, field := range []string{"reason", "code", "error.reason", "error.code"} {
		if event.Get(field).String() == "THINKING_SIGNATURE_INVALID" {
			return true
		}
	}
	return false
}

func kiroContextLengthClientMessage(status int, body []byte) (string, bool) {
	if status != http.StatusBadRequest {
		return "", false
	}
	lower := strings.ToLower(string(body))
	switch {
	case strings.Contains(lower, strings.ToLower(kiroContextLengthCode)):
		return kiroContextWindowFullMessage, true
	case strings.Contains(lower, "input is too long"):
		return kiroInputTooLongMessage, true
	default:
		return "", false
	}
}

type kiroStreamFailure struct {
	exception  *kiropkg.UpstreamException
	response   *http.Response
	admission  *UpstreamFailoverError
	partial    *kiropkg.StreamResult
	emptyReply *kiropkg.EmptyProviderReplyError
}

func (f *kiroStreamFailure) Error() string {
	if f.admission != nil {
		return f.admission.ClientMessage
	}
	if f.emptyReply != nil {
		return f.emptyReply.Error()
	}
	return f.exception.Error()
}

// observeKiroStreamFailure runs at the native parser boundary, before the
// downstream decides whether it can retry, must terminate, or disconnected.
func (s *GatewayService) observeKiroStreamFailure(ctx context.Context, account *Account, err error, resp *http.Response, partial *kiropkg.StreamResult) *kiroStreamFailure {
	var emptyReply *kiropkg.EmptyProviderReplyError
	if errors.As(err, &emptyReply) {
		return &kiroStreamFailure{
			response:   resp,
			partial:    partial,
			emptyReply: emptyReply,
		}
	}
	var exc *kiropkg.UpstreamException
	if !errors.As(err, &exc) {
		return nil
	}
	failure := &kiroStreamFailure{exception: exc, response: resp, partial: partial}
	status, reason := failure.statusAndReason()
	switch status {
	case http.StatusPaymentRequired:
		s.parkKiroMonthlyQuota(ctx, account, reason)
	case http.StatusTooManyRequests:
		if _, err := s.markKiro429(ctx, account.ID, buildKiroAccountKey(account)); err != nil {
			logger.FromContext(ctx).Warn("kiro stream throttling cooldown failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
	return failure
}

func kiroStreamFailureFromResponse(resp *http.Response, err error) *kiroStreamFailure {
	var inBand *sseStreamErrorEventError
	if !errors.As(err, &inBand) {
		return nil
	}
	if body, ok := resp.Body.(*kiroTranslatedStreamBody); ok {
		return body.streamFailure.Load()
	}
	return nil
}

func kiroQuotaReason(fields ...string) string {
	for _, field := range fields {
		if field == kiroMonthlyRequestCountCode || field == kiroOverageLimitCode {
			return field
		}
	}
	return ""
}

func kiroHTTPMonthlyQuotaReason(status int, body []byte) string {
	if status != http.StatusPaymentRequired {
		return ""
	}
	raw := strings.TrimSpace(string(body))
	if gjson.Valid(raw) {
		payload := gjson.Parse(raw)
		for _, field := range []string{"reason", "error.reason", "code", "error.code", "errorCode", "__type", "type", "error.type"} {
			value := payload.Get(field)
			if value.Type == gjson.String && value.Str == kiroOverageLimitCode {
				return kiroOverageLimitCode
			}
		}
	}
	if raw == kiroOverageLimitCode {
		return kiroOverageLimitCode
	}
	if looksLikeKiroMonthlyRequestCountError(raw) {
		return kiroMonthlyRequestCountCode
	}
	return ""
}

func (f *kiroStreamFailure) statusAndReason() (int, string) {
	if f.admission != nil {
		return f.admission.StatusCode, string(f.admission.Reason)
	}
	if f.emptyReply != nil {
		return http.StatusBadGateway, ""
	}
	exc := f.exception
	if reason := kiroQuotaReason(exc.Type, exc.Reason, exc.Code); reason != "" {
		return http.StatusPaymentRequired, reason
	}
	typ := strings.ToLower(exc.Type)
	text := strings.ToLower(exc.Message)
	switch {
	case strings.Contains(typ, "throttl"), strings.Contains(text, "too many requests"), strings.Contains(text, "rate exceeded"),
		strings.Contains(typ, "servicequotaexceeded"), looksLikeKiroOverageExhaustedError(text), looksLikeKiroQuotaExhaustedError(text):
		return http.StatusTooManyRequests, ""
	case strings.Contains(typ, "validation"), kiroExceptionHasContextLengthCode(exc), strings.Contains(text, "input is too long"):
		return http.StatusBadRequest, ""
	default:
		// Authorization failures carried inside a stream are request or
		// model scoped. HTTP 5xx failover avoids disabling the credential.
		return http.StatusInternalServerError, ""
	}
}

func kiroExceptionHasContextLengthCode(exc *kiropkg.UpstreamException) bool {
	return exc.Type == kiroContextLengthCode || exc.Reason == kiroContextLengthCode || exc.Code == kiroContextLengthCode
}

func (f *kiroStreamFailure) normalizedMessage(status int) string {
	if f.admission != nil {
		return f.admission.ClientMessage
	}
	if f.emptyReply != nil {
		return "Kiro ended the turn without any output"
	}
	switch status {
	case http.StatusBadRequest:
		if kiroExceptionHasContextLengthCode(f.exception) {
			return kiroContextWindowFullMessage
		}
		if message, ok := kiroContextLengthClientMessage(status, []byte(f.exception.Message)); ok {
			return message
		}
		return "Invalid Kiro request"
	case http.StatusTooManyRequests:
		return "Rate limit exceeded"
	case http.StatusPaymentRequired:
		return "Kiro request quota exhausted"
	default:
		return "Upstream request failed"
	}
}

func (b *kiroTranslatedStreamBody) writeStreamFailure(w io.Writer, failure *kiroStreamFailure) {
	b.streamFailure.Store(failure)
	status, _ := failure.statusAndReason()
	payload, _ := json.Marshal(gin.H{
		"type":  "error",
		"error": gin.H{"type": claudeErrorType(status), "message": failure.normalizedMessage(status)},
	})
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
}

type kiroErrorWriter func(*gin.Context, int, string, string)

func (s *GatewayService) handleKiroStreamFailure(ctx context.Context, c *gin.Context, account *Account, mappedModel string, requestBody []byte, failure *kiroStreamFailure, writeError kiroErrorWriter) error {
	if failure.admission != nil {
		// An admission decision has no provider response. A buffered earlier
		// attempt must not become the capture for this pre-output failover.
		AbortCaptureAttempt(c)
		_, _ = takeCaptureResult(c)
		clearUncommittedKiroSSEHeaders(c)
		return failure.admission
	}
	if failure.emptyReply != nil {
		message := failure.normalizedMessage(http.StatusBadGateway)
		failover := newInvalidProviderResponseFailover(failure.response, message)
		failover.Platform = PlatformKiro
		failover.CaptureResponseIncomplete = !failure.emptyReply.ProviderTerminalObserved
		setOpsUpstreamError(c, failover.HTTPStatusForCapture(), message, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: failover.HTTPStatusForCapture(),
			UpstreamRequestID:  buildKiroRequestID(failure.response),
			Kind:               "failover",
			Message:            message,
		})
		clearUncommittedKiroSSEHeaders(c)
		return failover
	}
	status, reason := failure.statusAndReason()
	message := failure.normalizedMessage(status)
	data := map[string]string{"message": message}
	if reason != "" {
		data["reason"] = reason
	}
	body, _ := json.Marshal(data)

	// Reuse Kiro HTTP classification and ops accounting. Keep the actual AWS
	// HTTP status and headers on the archive-facing failure.
	classifiedResponse := *failure.response
	classifiedResponse.StatusCode = status
	err := s.handleKiroHTTPErrorBody(ctx, &classifiedResponse, c, account, mappedModel, requestBody, body, false)
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) {
		failover.UpstreamHTTPStatus = failure.response.StatusCode
		failover.CaptureResponseIncomplete = true
		if !failover.ShouldRetryNextAccount() {
			clearUncommittedKiroSSEHeaders(c)
			writeError(c, mapUpstreamStatusCode(status), claudeErrorType(status), message)
			MarkResponseCommitted(c)
		}
	}
	return err
}

func clearUncommittedKiroSSEHeaders(c *gin.Context) {
	if !c.Writer.Written() {
		for _, key := range []string{"Content-Type", "Cache-Control", "Connection", "X-Accel-Buffering"} {
			c.Writer.Header().Del(key)
		}
	}
}

func (f *kiroStreamFailure) applyPartialUsage(usage *ClaudeUsage) {
	if f.partial != nil {
		*usage = kiroUsageToClaude(f.partial.Usage, f.partial.Usage.InputTokens)
	}
}

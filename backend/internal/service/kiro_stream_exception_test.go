//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func kiroAWSFailureFrame(t *testing.T, raw string, fields ...[2]string) []byte {
	t.Helper()
	var headers bytes.Buffer
	for _, field := range fields {
		headers.WriteByte(byte(len(field[0])))
		headers.WriteString(field[0])
		headers.WriteByte(7)
		require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(field[1]))))
		headers.WriteString(field[1])
	}
	var frame bytes.Buffer
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(16+headers.Len()+len(raw))))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	frame.Write(headers.Bytes())
	frame.WriteString(raw)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

type kiroAWSExceptionCooldownStore struct {
	stubKiroCooldownStore
	marks int
}

func (s *kiroAWSExceptionCooldownStore) Mark429(ctx context.Context, key string) (time.Duration, error) {
	s.marks++
	return s.stubKiroCooldownStore.Mark429(ctx, key)
}

func forwardKiroAWSFrames(t *testing.T, endpoint string, stream bool, providerStatus int, raw []byte) (*ForwardResult, error, *httptest.ResponseRecorder, *recordingKiroTempUnschedRepo, *kiroAWSExceptionCooldownStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := newKiroCacheTransactionAccount(812, "aws-error-test")
	repo := &recordingKiroTempUnschedRepo{}
	cooldown := &kiroAWSExceptionCooldownStore{}
	svc := &GatewayService{
		accountRepo: repo, kiroCooldownStore: cooldown, tlsFPProfileService: &TLSFingerprintProfileService{},
		httpUpstream: &queuedHTTPUpstream{responses: []*http.Response{{
			StatusCode: providerStatus,
			Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": {"aws-request-id"}},
			Body:       io.NopCloser(bytes.NewReader(raw)),
		}}},
	}
	path, input := "/v1/messages", `"messages":[{"role":"user","content":"hello"}]`
	switch endpoint {
	case "chat":
		path = "/v1/chat/completions"
	case "responses":
		path, input = "/v1/responses", `"input":"hello"`
	case "compaction":
		path, input = "/v1/responses", `"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]`
	}
	body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","stream":%t,%s}`, stream, input))
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6", Stream: stream}
	var result *ForwardResult
	var err error
	switch endpoint {
	case "chat":
		result, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, parsed)
	case "responses", "compaction":
		result, err = svc.ForwardAsResponses(c.Request.Context(), c, account, body, parsed)
	default:
		result, err = svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	}
	return result, err, rec, repo, cooldown
}

func TestKiroAWSStreamExceptionBeforeOutputUsesEndpointValidationFormat(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat", "responses", "compaction"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", endpoint, stream), func(t *testing.T) {
				frame := kiroAWSFailureFrame(t, `{"message":"ignored","private":"do-not-forward"}`,
					[2]string{":message-type", "error"}, [2]string{":error-code", "ValidationException"},
					[2]string{":error-message", "Invalid messages. ?access_token=provider-secret"})
				result, err, rec, repo, _ := forwardKiroAWSFrames(t, endpoint, stream, http.StatusOK, frame)
				require.Error(t, err)
				require.Nil(t, result)
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.False(t, failure.ShouldRetryNextAccount())
				require.Equal(t, http.StatusOK, failure.HTTPStatusForCapture())
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
				typePath := "error.type"
				if endpoint == "responses" || endpoint == "compaction" {
					typePath = "error.code"
				}
				require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), typePath).String())
				require.Equal(t, "Invalid Kiro request", gjson.Get(rec.Body.String(), "error.message").String())
				require.NotContains(t, rec.Body.String(), "Invalid messages.")
				require.NotContains(t, rec.Body.String(), "provider-secret")
				require.NotContains(t, rec.Body.String(), "do-not-forward")
				require.False(t, repo.rateCalled)
			})
		}
	}
}

func TestKiroAWSStreamExceptionAfterOutputKeepsPartialUsage(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			var frames bytes.Buffer
			frames.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "partial output from Kiro"}))
			frames.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
				"tokenUsage":  map[string]any{"uncachedInputTokens": 20, "outputTokens": 9, "cacheReadInputTokens": 7},
				"creditsUsed": 0.25,
			}))
			frames.Write(kiroAWSFailureFrame(t, `{"private":"do-not-forward"}`,
				[2]string{":message-type", "exception"}, [2]string{":exception-type", "ThrottlingException"}))
			result, err, rec, _, cooldown := forwardKiroAWSFrames(t, endpoint, true, http.StatusOK, frames.Bytes())
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.NotErrorAs(t, err, &failure)
			require.NotNil(t, result)
			require.Equal(t, 20, result.Usage.InputTokens)
			require.Equal(t, 9, result.Usage.OutputTokens)
			require.Equal(t, 7, result.Usage.CacheReadInputTokens)
			require.InDelta(t, 0.25, result.Usage.KiroCredits, 1e-9)
			require.True(t, result.CaptureTerminalError)
			require.False(t, result.CaptureResponseComplete)
			require.Contains(t, rec.Body.String(), "partial output from Kiro")
			require.Contains(t, rec.Body.String(), "rate_limit_error")
			require.NotContains(t, rec.Body.String(), "do-not-forward")
			require.NotContains(t, rec.Body.String(), "event: message_stop")
			require.Equal(t, 1, cooldown.marks)
		})
	}
}

func TestKiroAWSOverageAfterOutputParksWithoutRetry(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			content := buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "partial output from Kiro"})
			overage := kiroAWSFailureFrame(t, `{"reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}`,
				[2]string{":message-type", "exception"}, [2]string{":exception-type", "ServiceQuotaExceededException"})
			result, err, rec, repo, cooldown := forwardKiroAWSFrames(t, endpoint, true, http.StatusOK, append(content, overage...))
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.NotErrorAs(t, err, &failure)
			require.NotNil(t, result)
			require.True(t, result.CaptureTerminalError)
			require.Contains(t, rec.Body.String(), "partial output from Kiro")
			require.Contains(t, rec.Body.String(), "Kiro request quota exhausted")
			require.True(t, repo.called)
			require.Equal(t, nextKiroMonthlyResetUTC(time.Now()), repo.until)
			require.Equal(t, "OVERAGE_REQUEST_LIMIT_EXCEEDED", repo.reason)
			require.False(t, repo.rateCalled)
			require.Zero(t, cooldown.marks)
		})
	}
}

func TestKiroAWSQuotaParkingRequiresExactCodeAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name           string
		providerStatus int
		body           string
		eventType      string
		wantStatus     int
		parkReason     string
	}{
		{"http_explicit_overage", 402, `{"reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}`, "", 402, kiroOverageLimitCode},
		{"http_prose_overage", 402, `{"message":"Overage limit exceeded"}`, "", 402, ""},
		{"http_echoed_code", 402, `{"message":"bad input OVERAGE_REQUEST_LIMIT_EXCEEDED"}`, "", 402, ""},
		{"http_rate_limit", 429, `{"reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}`, "", 429, ""},
		{"event_explicit_overage", 200, `{"reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}`, "ServiceQuotaExceededException", 402, kiroOverageLimitCode},
		{"event_explicit_monthly", 200, `{"error":{"reason":"MONTHLY_REQUEST_COUNT"}}`, "ServiceQuotaExceededException", 402, kiroMonthlyRequestCountCode},
		{"event_prose_overage", 200, `{"message":"overage limit exceeded"}`, "ServiceQuotaExceededException", 429, ""},
		{"event_echoed_code", 200, `{"message":"quota exceeded MONTHLY_REQUEST_COUNT"}`, "ServiceQuotaExceededException", 429, ""},
		{"event_unknown", 200, `{"message":"private provider detail"}`, "InternalServerException", 500, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.body)
			if tc.eventType != "" {
				raw = kiroAWSFailureFrame(t, tc.body, [2]string{":message-type", "exception"}, [2]string{":exception-type", tc.eventType})
			}
			result, err, rec, repo, cooldown := forwardKiroAWSFrames(t, "messages", true, tc.providerStatus, raw)
			require.Nil(t, result)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.ShouldRetryNextAccount())
			require.Equal(t, tc.wantStatus, failure.StatusCode)
			require.Equal(t, tc.providerStatus, failure.HTTPStatusForCapture())
			require.Empty(t, rec.Body.String())
			require.NotContains(t, string(failure.ResponseBody), "private provider detail")
			switch tc.parkReason {
			case kiroOverageLimitCode:
				require.True(t, repo.called)
				require.Equal(t, nextKiroMonthlyResetUTC(time.Now()), repo.until)
				require.Equal(t, kiroOverageLimitCode, repo.reason)
				require.False(t, repo.rateCalled)
			case kiroMonthlyRequestCountCode:
				require.True(t, repo.rateCalled)
				require.Equal(t, 1, repo.rateLimitedCall)
				require.Equal(t, nextKiroMonthlyResetUTC(time.Now()), repo.rateLimitReset)
				require.False(t, repo.called)
			default:
				require.False(t, repo.rateCalled)
				require.False(t, repo.called)
			}
			if tc.wantStatus == http.StatusTooManyRequests {
				require.Equal(t, 1, cooldown.marks)
			} else {
				require.Zero(t, cooldown.marks)
			}
		})
	}
}

func TestKiroAWSOversizeErrorsUseStableClientMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		message string
	}{
		{"field_limit", `{"message":"provider request ID abc123","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`, "Context window is full. Reduce conversation history, system prompt, or tools."},
		{"input_limit", `{"message":"Input is too long for requested model. provider request ID abc123"}`, "Input is too long. Reduce the size of your messages."},
	} {
		for _, endpoint := range []string{"messages", "chat", "responses", "compaction"} {
			for _, event := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/event_%t", tc.name, endpoint, event), func(t *testing.T) {
					status, raw := http.StatusBadRequest, []byte(tc.raw)
					if event {
						status = http.StatusOK
						raw = kiroAWSFailureFrame(t, tc.raw,
							[2]string{":message-type", "exception"}, [2]string{":exception-type", "ValidationException"})
					}
					result, err, rec, _, _ := forwardKiroAWSFrames(t, endpoint, false, status, raw)
					require.Error(t, err)
					require.Nil(t, result)
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Equal(t, tc.message, gjson.Get(rec.Body.String(), "error.message").String())
					require.NotContains(t, rec.Body.String(), "abc123")
					if endpoint == "compaction" {
						require.Equal(t, "context_length_exceeded", gjson.Get(rec.Body.String(), "error.code").String())
					}
				})
			}
		}
	}
}

func TestKiroAWSWebSearchExceptionKeepsSyntheticEventsStaged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const endpoint = "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })
	account := newKiroCacheTransactionAccount(813, "WEBSEARCH-AWS-ERROR")
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search for current events"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	nativeFrame := kiroAWSFailureFrame(t, "", [2]string{":message-type", "error"}, [2]string{":error-code", "ThrottlingException"})
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusOK, `{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`),
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(nativeFrame))},
	}}
	cooldown := &kiroAWSExceptionCooldownStore{}
	transport := &recordingCaptureTransport{}
	svc := &GatewayService{
		httpUpstream: upstream, kiroCooldownStore: cooldown, tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{
			Enabled: true, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10,
		}}},
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	t.Cleanup(svc.capturePool.Stop)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	SetCaptureRequestedModel(c, parsed.Model)

	result, err := svc.Forward(c.Request.Context(), c, account, parsed)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.ShouldRetryNextAccount())
	require.Equal(t, http.StatusTooManyRequests, failure.StatusCode)
	require.Equal(t, http.StatusOK, failure.HTTPStatusForCapture())
	require.Empty(t, rec.Body.String())
	require.Equal(t, 1, cooldown.marks)
	require.Len(t, upstream.requests, 2)
	require.True(t, CommitTerminalErrorCaptureAttemptWithCompleteness(c, PlatformKiro, failure.HTTPStatusForCapture(), !failure.CaptureResponseIncomplete))
	attempts := transport.Attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
	require.Equal(t, snapshotHTTPRequestBody(upstream.requests[1]), attempts[1].RequestBytes())
	require.Equal(t, nativeFrame, attempts[1].ResponseBytes())
	require.Equal(t, []captureTerminalState{captureCommitted}, attempts[1].TerminalStates())
}

func TestKiroAWSWebSearchLaterExceptionRetainsEmittedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const endpoint = "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })

	account := newKiroCacheTransactionAccount(820, "WEBSEARCH-INTERIM-USAGE")
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search for current events"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	var accepted bytes.Buffer
	accepted.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "Check a related source."}))
	accepted.Write(buildKiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseId": "toolu_search_again",
		"name":      "remote_web_search",
		"input":     map[string]any{"query": "related news"},
		"stop":      true,
	}))
	accepted.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"tokenUsage":  map[string]any{"uncachedInputTokens": 31, "outputTokens": 13, "cacheReadInputTokens": 7},
		"creditsUsed": 0.4,
	}))
	accepted.Write(buildKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"stopReason": "tool_use"}))

	var rejected bytes.Buffer
	rejected.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "discarded buffered text"}))
	rejected.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"tokenUsage":  map[string]any{"uncachedInputTokens": 91, "outputTokens": 51},
		"creditsUsed": 1.5,
	}))
	rejected.Write(kiroAWSFailureFrame(t, "", [2]string{":message-type", "error"}, [2]string{":error-code", "ThrottlingException"}))

	const mcpBody = `{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusOK, mcpBody),
		{StatusCode: http.StatusOK, Body: io.NopCloser(&accepted)},
		newJSONResponse(http.StatusOK, mcpBody),
		{StatusCode: http.StatusOK, Body: io.NopCloser(&rejected)},
	}}
	cooldown := &kiroAWSExceptionCooldownStore{}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: cooldown, tlsFPProfileService: &TLSFingerprintProfileService{}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(c.Request.Context(), c, account, parsed)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.NotErrorAs(t, err, &failover)
	require.NotNil(t, result)
	require.True(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
	require.Contains(t, rec.Body.String(), "Check a related source.")
	require.Contains(t, rec.Body.String(), "rate_limit_error")
	require.NotContains(t, rec.Body.String(), "discarded buffered text")
	require.Len(t, upstream.requests, 4)
	require.Equal(t, 1, cooldown.marks)
	require.Equal(t, 31, result.Usage.InputTokens)
	require.Equal(t, 13, result.Usage.OutputTokens)
	require.Equal(t, 7, result.Usage.CacheReadInputTokens)
	require.InDelta(t, 0.4, result.Usage.KiroCredits, 1e-9)
}

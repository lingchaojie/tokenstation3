//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type antigravityCaptureTokenCache struct{}

type antigravityCaptureAccountRepo struct{ service.AccountRepository }

func (*antigravityCaptureAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

func (*antigravityCaptureAccountRepo) ClearRateLimit(context.Context, int64) error { return nil }

func (*antigravityCaptureTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "antigravity-provider-secret", nil
}
func (*antigravityCaptureTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (*antigravityCaptureTokenCache) DeleteAccessToken(context.Context, string) error { return nil }
func (*antigravityCaptureTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (*antigravityCaptureTokenCache) ReleaseRefreshLock(context.Context, string) error { return nil }

type antigravityCaptureUpstream struct {
	mu       sync.Mutex
	requests [][]byte
	body     []byte
	status   int
}

type antigravityTwoAccountCaptureUpstream struct {
	mu       sync.Mutex
	calls    []int64
	requests map[int64][][]byte
	firstID  int64
	first    []byte
	firstErr error
	second   []byte
}

type antigravityProviderReadErrorBody struct{ err error }

func (b *antigravityProviderReadErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (b *antigravityProviderReadErrorBody) Close() error             { return nil }

func (u *antigravityTwoAccountCaptureUpstream) responseFor(req *http.Request, accountID int64) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	if u.requests == nil {
		u.requests = make(map[int64][][]byte)
	}
	u.calls = append(u.calls, accountID)
	u.requests[accountID] = append(u.requests[accountID], append([]byte(nil), body...))
	providerBody := u.second
	var providerReadErr error
	if accountID == u.firstID {
		providerBody = u.first
		providerReadErr = u.firstErr
	}
	u.mu.Unlock()
	bodyReader := io.ReadCloser(io.NopCloser(bytes.NewReader(providerBody)))
	if providerReadErr != nil {
		bodyReader = &antigravityProviderReadErrorBody{err: providerReadErr}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"text/event-stream"},
			"X-Request-Id": {"ag-account-" + strconv.FormatInt(accountID, 10)},
		},
		Body: bodyReader, Request: req,
	}, nil
}

func (u *antigravityTwoAccountCaptureUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.responseFor(req, accountID)
}

func (u *antigravityTwoAccountCaptureUpstream) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.responseFor(req, accountID)
}

type antigravitySmartRetryCaptureUpstream struct {
	mu           sync.Mutex
	requests     [][]byte
	initialBody  []byte
	terminalBody []byte
	calls        int
}

func (u *antigravitySmartRetryCaptureUpstream) responseFor(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requests = append(u.requests, append([]byte(nil), body...))
	u.calls++
	call := u.calls
	u.mu.Unlock()
	if call%2 == 1 {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"initial-429"}},
			Body:       io.NopCloser(bytes.NewReader(u.initialBody)), Request: req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"final-503"}},
		Body:       io.NopCloser(bytes.NewReader(u.terminalBody)), Request: req,
	}, nil
}

func (u *antigravitySmartRetryCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.responseFor(req)
}

func (u *antigravitySmartRetryCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.responseFor(req)
}

func (u *antigravityCaptureUpstream) responseFor(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requests = append(u.requests, append([]byte(nil), body...))
	u.mu.Unlock()
	return &http.Response{
		StatusCode: u.status,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {"rid-antigravity-terminal"},
		},
		Body:    io.NopCloser(bytes.NewReader(u.body)),
		Request: req,
	}, nil
}

func (u *antigravityCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.responseFor(req)
}

func (u *antigravityCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.responseFor(req)
}

func TestAntigravityCompatibilityRouterArchivesTerminalProviderAttemptExactlyOnce(t *testing.T) {
	testAntigravityCompatibilityRouterArchivesTerminalProviderAttemptExactlyOnce(
		t, http.StatusUnauthorized, http.StatusBadGateway, []byte(`{"error":{"code":401,"message":"invalid antigravity bearer"}}`),
	)
}

func TestAntigravityMessagesRouterFailoverArchivesOnlyFinalSemanticAccount(t *testing.T) {
	testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, "messages", false, []byte("data: {\"response\":{\"responseId\":\"first-usage-only\",\"usageMetadata\":{\"promptTokenCount\":8}}}\n\n"), nil)
}

func TestAntigravityMessagesRouterPreCommitReadErrorFailsOverWithoutArchivingFirstAccount(t *testing.T) {
	testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, "messages", true, nil, errors.New("forced provider stream read failure"))
}

func TestAntigravityMessagesRouterMalformedTerminalPayloadFailsOverWithoutArchivingFirstAccount(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			first := []byte("data: {\"response\":{}}\n\ndata: [DONE]\n\n")
			testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, "messages", stream, first, nil)
		})
	}
}

func TestAntigravityCompatibilityRoutersRejectEmptyFunctionAndFinishOnlyBeforeCommit(t *testing.T) {
	firstBodies := map[string][]byte{
		"empty_function": []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{}}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"finish_only":    []byte("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n"),
	}
	for _, endpoint := range []string{"chat_completions", "responses"} {
		for name, firstBody := range firstBodies {
			t.Run(endpoint+"/"+name, func(t *testing.T) {
				testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, endpoint, true, firstBody, nil)
			})
		}
	}
}

func TestAntigravityGeminiStreamsRejectMalformedOrPostTerminalPayloadsBeforeCommit(t *testing.T) {
	firstBodies := map[string][]byte{
		"malformed_then_valid": []byte("data: {not-json}\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"empty_candidates_then_valid": []byte("data: {\"response\":{\"candidates\":[]}}\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"done_then_valid": []byte("data: [DONE]\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"nonstring_finish_reason":    []byte("data: {\"response\":{\"candidates\":[{\"finishReason\":123}]}}\n\ndata: [DONE]\n\n"),
		"finish_only":                []byte("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"malformed_first_candidate":  []byte("data: {\"response\":{\"candidates\":[{}, {\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"nonstring_part_text":        []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":123}]},\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"nonstring_function_name":    []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":123,\"args\":{}}}]},\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"oversized_function_name":    []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"" + strings.Repeat("x", 1025) + "\",\"args\":{}}}]},\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"scalar_candidate":           []byte("data: {\"response\":{\"candidates\":[123]}}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_feedback":    []byte("data: {\"response\":{\"promptFeedback\":{\"foo\":\"bar\"}}}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_rating":      []byte("data: {\"response\":{\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"safetyRatings\":[123]}}}\n\n"),
		"invalid_candidate_ratings":  []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"safetyRatings\":\"bad\"}]}}\n\n"),
		"invalid_grounding_metadata": []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"groundingMetadata\":{\"groundingChunks\":[123]}}]}}\n\n"),
		"invalid_finish_message":     []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"finishMessage\":123}]}}\n\n"),
		"blocked_with_candidate":     []byte("data: {\"response\":{\"promptFeedback\":{\"blockReason\":\"SAFETY\"},\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}}]}}\n\ndata: [DONE]\n\n"),
		"nonstring_model_ancillary":  []byte("data: {\"response\":{\"modelVersion\":123}}\n\ndata: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"invalid_usage_ancillary":    []byte("data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":\"bad\"}}}\n\ndata: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"invalid_usage_details":      []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":-7}]}}}\n\n"),
		"cached_exceeds_prompt":      []byte("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"cachedContentTokenCount\":3}}}\n\n"),
		"image_exceeds_output":       []byte("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":3}]}}}\n\n"),
		"fractional_candidate_index": []byte("data: {\"response\":{\"candidates\":[{\"index\":0.5,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"mixed_duplicate_index":      []byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}},{\"index\":0,\"finishReason\":\"STOP\"}]}}\n\ndata: [DONE]\n\n"),
		"too_many_candidates": []byte("data: {\"response\":{\"candidates\":[" +
			strings.Repeat(`{"content":{"role":"model","parts":[{"text":""}]}},`, 1024) +
			`{"content":{"role":"model","parts":[{"text":""}]}}]}}` + "\n\n"),
	}
	for _, endpoint := range []struct {
		name   string
		stream bool
	}{
		{name: "messages_buffered"},
		{name: "messages", stream: true},
		{name: "chat_completions_buffered"},
		{name: "chat_completions", stream: true},
		{name: "responses_buffered"},
		{name: "responses", stream: true},
	} {
		endpointName := strings.TrimSuffix(endpoint.name, "_buffered")
		for name, firstBody := range firstBodies {
			t.Run(endpoint.name+"/"+name, func(t *testing.T) {
				testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, endpointName, endpoint.stream, firstBody, nil)
			})
		}
	}
}

func TestAntigravityGeminiCommittedContentWithoutFinishDoesNotReplayAfterDONE(t *testing.T) {
	body := []byte("data: {\"response\":{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}}]}}\n\ndata: [DONE]\n\n")
	for _, endpoint := range []string{"messages", "chat_completions", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t, endpoint, true, body, nil, true)
		})
	}
}

func TestAntigravityBlockedPromptFeedbackIsTerminalWithoutReplay(t *testing.T) {
	blocked := []byte("data: {\"response\":{\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"blockReasonMessage\":\"blocked-by-provider\"}}}\n\n")
	for _, endpoint := range []struct {
		name   string
		stream bool
	}{
		{name: "messages_buffered"},
		{name: "messages", stream: true},
		{name: "chat_completions_buffered"},
		{name: "chat_completions", stream: true},
		{name: "responses_buffered"},
		{name: "responses", stream: true},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(
				t, strings.TrimSuffix(endpoint.name, "_buffered"), endpoint.stream, blocked, nil, true,
			)
		})
	}
}

func TestAntigravityBaseURLMessagesRejectInvalidAnthropicAttemptsBeforeCommit(t *testing.T) {
	validJSON := []byte(`{"id":"msg-final","type":"message","role":"assistant","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
	validSSE := []byte("event: message_start\ndata:{\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata:\t{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	for index, scenario := range []struct {
		name       string
		stream     bool
		firstBody  []byte
		secondBody []byte
	}{
		{name: "nonstream_wrong_role_content", firstBody: []byte(`{"id":"msg-first","type":"message","role":"user","content":[{}],"usage":{"input_tokens":9}}`), secondBody: validJSON},
		{name: "nonstream_nonstring_content_block_type", firstBody: []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":123}],"usage":{"input_tokens":9}}`), secondBody: validJSON},
		{name: "nonstream_malformed_known_siblings", firstBody: []byte(`{"id":123,"type":"message","role":"assistant","model":123,"content":[{"type":"text","text":"first-leak"}],"stop_reason":[],"usage":"bad"}`), secondBody: validJSON},
		{name: "nonstream_missing_stop_reason", firstBody: []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"usage":{"input_tokens":9,"output_tokens":1}}`), secondBody: validJSON},
		{name: "nonstream_null_stop_reason", firstBody: []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":null,"usage":{"input_tokens":9,"output_tokens":1}}`), secondBody: validJSON},
		{name: "nonstream_empty_stop_reason", firstBody: []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"","usage":{"input_tokens":9,"output_tokens":1}}`), secondBody: validJSON},
		{name: "stream_malformed_declared_event", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\nevent: message_delta\ndata: {not-json}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), secondBody: validSSE},
		{name: "stream_semantic_before_start", stream: true, firstBody: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), secondBody: validSSE},
		{name: "stream_stop_before_start", stream: true, firstBody: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\nevent: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n"), secondBody: validSSE},
		{name: "stream_event_type_mismatch", stream: true, firstBody: []byte("event: future_event\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"), secondBody: validSSE},
		{name: "stream_error_payload", stream: true, firstBody: []byte("data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"retry me\"}}\n\n"), secondBody: validSSE},
		{name: "stream_delta_without_block_start", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\n"), secondBody: validSSE},
		{name: "stream_known_event_missing_payload_type", stream: true, firstBody: []byte("event: message_start\ndata: {\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"), secondBody: validSSE},
		{name: "stream_nonstring_top_level_event_type", stream: true, firstBody: []byte("data: {\"type\":123}\n\nevent: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"), secondBody: validSSE},
		{name: "stream_malformed_message_start_usage", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":\"bad\"}}\n\n"), secondBody: validSSE},
		{name: "stream_message_stop_without_terminal_delta", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), secondBody: validSSE},
		{name: "nonstream_malformed_cache_breakdown", firstBody: []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":1,"cache_creation_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":100}}}`), secondBody: validJSON},
		{name: "stream_malformed_cache_breakdown", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"cache_creation_input_tokens\":1,\"cache_creation\":{\"ephemeral_5m_input_tokens\":100,\"ephemeral_1h_input_tokens\":100}}}}\n\n"), secondBody: validSSE},
		{name: "stream_duplicate_content_block_index", stream: true, firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"), secondBody: validSSE},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testAntigravityBaseURLMessagesFailover(t, int64(9810+index*10), scenario.stream, scenario.firstBody, scenario.secondBody)
		})
	}
}

func testAntigravityBaseURLMessagesFailover(t *testing.T, groupID int64, stream bool, firstBody, secondBody []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	firstAccountID, secondAccountID, userID := groupID+1, groupID+2, groupID+3
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "antigravity-base-url", Platform: service.PlatformAntigravity,
			Type: service.AccountTypeUpstream, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials:   map[string]any{"base_url": "https://relay.example.test", "api_key": "relay-secret"},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount, secondAccount := newAccount(firstAccountID, 1), newAccount(secondAccountID, 2)
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{firstAccount, secondAccount}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	usageRepo := &gatewayAnthropicUsageRepo{}
	gateway := service.NewGatewayService(
		&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, capturePool,
	)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, nil, nil, upstream, settings, nil, capturePool)
	handler := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settings, capturePool,
	)

	requestBody := fmt.Sprintf(`{"model":"claude-sonnet-4-5","stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, stream)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: groupID + 4, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	handler.Messages(c)
	capturePool.Stop()
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	require.NotEmpty(t, calls)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	require.NotContains(t, recorder.Body.String(), "first-leak")
	require.NotContains(t, recorder.Body.String(), "{not-json}")
	require.Contains(t, recorder.Body.String(), "recovered")
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Equal(t, finalRequests[len(finalRequests)-1], record.RawRequest)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
	if stream {
		require.Equal(t, 2, usageRepo.snapshot()[0].InputTokens)
		require.Equal(t, 1, usageRepo.snapshot()[0].OutputTokens)
	}
}

func testAntigravityRouterFailoverArchivesOnlyFinalSemanticAccount(t *testing.T, endpoint string, stream bool, firstBody []byte, firstReadErr error, expectFirstTerminal ...bool) {
	gin.SetMode(gin.TestMode)
	const groupID, firstAccountID, secondAccountID, userID = int64(9760), int64(9761), int64(9762), int64(9763)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "antigravity-two-account", Platform: service.PlatformAntigravity,
			Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{
				"access_token": "stale-secret", "project_id": "project-capture",
				"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount := newAccount(firstAccountID, 1)
	secondAccount := newAccount(secondAccountID, 2)
	secondBody := []byte("data: {\"response\":{\"responseId\":\"second-success\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"recovered\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}}\n\n")
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, firstErr: firstReadErr, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{firstAccount, secondAccount}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	usageRepo := &gatewayAnthropicUsageRepo{}
	gateway := service.NewGatewayService(
		&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
	)
	tokenProvider := service.NewAntigravityTokenProvider(nil, &antigravityCaptureTokenCache{}, nil)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil, capturePool)
	h := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-5","stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, stream))
	path := "/v1/messages"
	if endpoint == "chat_completions" {
		path = EndpointChatCompletions
	} else if endpoint == "responses" {
		path = EndpointResponses
		requestBody = []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-5","stream":%t,"input":"hello"}`, stream))
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9764, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	if endpoint == "chat_completions" {
		h.ChatCompletions(c)
	} else if endpoint == "responses" {
		h.Responses(c)
	} else {
		h.Messages(c)
	}
	capturePool.Stop()
	require.Equal(t, http.StatusOK, recorder.Code)
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	firstRequests := append([][]byte(nil), upstream.requests[firstAccountID]...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	if len(expectFirstTerminal) > 0 && expectFirstTerminal[0] {
		require.NotEmpty(t, calls)
		for _, accountID := range calls {
			require.Equal(t, firstAccountID, accountID)
		}
		require.Len(t, captureRecords, 1)
		record := <-captureRecords
		require.Equal(t, firstBody, record.RawResponse)
		require.NotEmpty(t, firstRequests)
		require.Equal(t, firstRequests[len(firstRequests)-1], record.RawRequest)
		require.Len(t, usageRepo.snapshot(), 1)
		require.Equal(t, firstAccountID, usageRepo.snapshot()[0].AccountID)
		return
	}
	require.Contains(t, recorder.Body.String(), "recovered")
	require.NotContains(t, recorder.Body.String(), "first-leak")
	require.GreaterOrEqual(t, len(calls), 2)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	for _, accountID := range calls[:len(calls)-1] {
		require.Equal(t, firstAccountID, accountID)
	}
	require.Len(t, captureRecords, 1, "the pre-semantic account must not be archived")
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Len(t, finalRequests, 1)
	require.Equal(t, finalRequests[0], record.RawRequest)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
}

func TestAntigravityCompatibilityRouterArchivesNonFailoverProviderAttemptExactlyOnce(t *testing.T) {
	testAntigravityCompatibilityRouterArchivesTerminalProviderAttemptExactlyOnce(
		t, http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, []byte(`{"error":{"code":422,"message":"invalid antigravity request"}}`),
	)
}

func testAntigravityCompatibilityRouterArchivesTerminalProviderAttemptExactlyOnce(t *testing.T, upstreamStatus, clientStatus int, errorBody []byte) {
	gin.SetMode(gin.TestMode)
	const (
		groupID   = int64(9780)
		accountID = int64(9781)
		userID    = int64(9782)
	)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "antigravity-terminal", Platform: service.PlatformAntigravity,
		Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"access_token": "stale-antigravity-secret", "project_id": "project-capture",
			"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &antigravityCaptureUpstream{body: errorBody, status: upstreamStatus}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
	)
	tokenProvider := service.NewAntigravityTokenProvider(nil, &antigravityCaptureTokenCache{}, nil)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil, capturePool)
	h := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-5","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9783, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.ChatCompletions(c)
	capturePool.Stop()
	require.Equal(t, clientStatus, recorder.Code)
	if upstreamStatus == http.StatusUnprocessableEntity {
		require.True(t, json.Valid(recorder.Body.Bytes()), "mapped provider JSON error must remain a single JSON document: %q", recorder.Body.String())
		require.NotContains(t, recorder.Body.String(), "data: {")
	}
	require.Len(t, captureRecords, 1, "the terminal Antigravity provider attempt must be archived once")
	archived := <-captureRecords
	upstream.mu.Lock()
	require.NotEmpty(t, upstream.requests)
	finalRequest := append([]byte(nil), upstream.requests[len(upstream.requests)-1]...)
	upstream.mu.Unlock()
	require.Equal(t, finalRequest, archived.RawRequest)
	require.Equal(t, errorBody, archived.RawResponse)
	require.Equal(t, upstreamStatus, archived.HTTPStatus)
	require.Equal(t, service.PlatformAntigravity, archived.Platform)
	require.NotContains(t, string(archived.RequestHeaders), "antigravity-provider-secret")
}

func TestAntigravityCompatibilityRouterSmartRetryArchivesFinalHTTPAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, accountID, userID = int64(9790), int64(9791), int64(9792)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "antigravity-smart-retry-terminal", Platform: service.PlatformAntigravity,
		Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"access_token": "stale-antigravity-secret", "project_id": "project-capture",
			"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	initialBody := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","metadata":{"model":"claude-sonnet-4-5"},"reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.1s"}]}}`)
	terminalBody := []byte(`{"error":{"status":"UNAVAILABLE","message":"` + strings.Repeat("z", 64<<10) + `"}}`)
	upstream := &antigravitySmartRetryCaptureUpstream{initialBody: initialBody, terminalBody: terminalBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
	)
	tokenProvider := service.NewAntigravityTokenProvider(nil, &antigravityCaptureTokenCache{}, nil)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil, capturePool)
	h := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-5","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9793, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.ChatCompletions(c)
	capturePool.Stop()
	require.NotEqual(t, http.StatusOK, recorder.Code)
	require.Len(t, captureRecords, 1, "only the final smart-retry provider attempt is terminal")
	record := <-captureRecords
	upstream.mu.Lock()
	requests := append([][]byte(nil), upstream.requests...)
	upstream.mu.Unlock()
	require.GreaterOrEqual(t, len(requests), 2)
	require.Equal(t, requests[len(requests)-1], record.RawRequest)
	require.Len(t, record.RawResponse, 8<<10)
	require.True(t, bytes.Equal(terminalBody[:8<<10], record.RawResponse), "capture must contain exactly the bytes naturally consumed by the bounded smart-retry classifier")
	require.True(t, record.Truncated)
	require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
	require.Equal(t, "final-503", record.RequestID)
}

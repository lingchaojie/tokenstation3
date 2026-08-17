//go:build unit

package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bedrockHandlerProviderResponse struct {
	status      int
	contentType string
	body        []byte
	requestID   string
}

type bedrockHandlerProvider struct {
	mu        sync.Mutex
	calls     []int64
	requests  map[int64][][]byte
	responses map[int64]bedrockHandlerProviderResponse
}

func (u *bedrockHandlerProvider) response(req *http.Request, accountID int64) (*http.Response, error) {
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
	provider := u.responses[accountID]
	u.mu.Unlock()
	status := provider.status
	if status == 0 {
		status = http.StatusOK
	}
	contentType := provider.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	requestID := provider.requestID
	if requestID == "" {
		requestID = "bedrock-account-" + strconv.FormatInt(accountID, 10)
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":     {contentType},
			"X-Amzn-Requestid": {requestID},
			"X-Provider-Debug": {"bedrock-native"},
		},
		Body: io.NopCloser(bytes.NewReader(provider.body)), Request: req,
	}, nil
}

func (u *bedrockHandlerProvider) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.response(req, accountID)
}

func (u *bedrockHandlerProvider) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(req, accountID)
}

func (u *bedrockHandlerProvider) snapshot() ([]int64, map[int64][][]byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	calls := append([]int64(nil), u.calls...)
	requests := make(map[int64][][]byte, len(u.requests))
	for accountID, bodies := range u.requests {
		for _, body := range bodies {
			requests[accountID] = append(requests[accountID], append([]byte(nil), body...))
		}
	}
	return calls, requests
}

func buildBedrockHandlerChunk(t *testing.T, event map[string]any) []byte {
	t.Helper()
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	return buildBedrockHandlerRawChunk(t, eventJSON)
}

func buildBedrockHandlerRawChunk(t *testing.T, eventJSON []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString(eventJSON)})
	require.NoError(t, err)
	return buildBedrockHandlerEnvelopeChunk(t, payload)
}

func buildBedrockHandlerEnvelopeChunk(t *testing.T, payload []byte) []byte {
	t.Helper()

	var headers bytes.Buffer
	for name, value := range map[string]string{":event-type": "chunk", ":message-type": "event"} {
		require.NoError(t, headers.WriteByte(byte(len(name))))
		_, _ = headers.WriteString(name)
		require.NoError(t, headers.WriteByte(7))
		require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(value))))
		_, _ = headers.WriteString(value)
	}
	totalLength := uint32(12 + headers.Len() + len(payload) + 4)
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLength)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(headers.Len()))

	var frame bytes.Buffer
	_, _ = frame.Write(prelude)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payload)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func TestBedrockMessagesHandlerRejectsMalformedOrOutOfOrderProviderEventsBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	messageStart := map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_bedrock_final", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
		"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 2, "output_tokens": 0},
	}}
	semanticDelta := map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "first-leak"}}
	messageStop := map[string]any{"type": "message_stop"}
	secondBody := bedrockHandlerStream(t,
		messageStart,
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "recovered"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}},
		messageStop,
	)
	for index, scenario := range []struct {
		name string
		body func(*testing.T) []byte
	}{
		{name: "malformed_json", body: func(t *testing.T) []byte {
			return bytes.Join([][]byte{buildBedrockHandlerRawChunk(t, []byte(`{not-json}`)), bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "missing_chunk_bytes", body: func(t *testing.T) []byte {
			return bytes.Join([][]byte{buildBedrockHandlerEnvelopeChunk(t, []byte(`{}`)), bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "invalid_chunk_base64", body: func(t *testing.T) []byte {
			return bytes.Join([][]byte{buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"not-valid-base64"}`)), bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "malformed_eventstream_header", body: func(t *testing.T) []byte {
			frame := buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"e30="}`))
			nameAt := bytes.Index(frame, []byte(":event-type"))
			require.Greater(t, nameAt, 0)
			frame[nameAt+len(":event-type")] = 0xff
			binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
			return bytes.Join([][]byte{frame, bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "known_header_with_boolean_type", body: func(t *testing.T) []byte {
			frame := buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"e30="}`))
			headerTypeOffset := 12 + 1 + len(":event-type")
			removeStart := headerTypeOffset + 1
			removeEnd := removeStart + 2 + len("chunk")
			frame = append(frame[:removeStart], frame[removeEnd:]...)
			frame[headerTypeOffset] = 0
			binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
			binary.BigEndian.PutUint32(frame[4:8], binary.BigEndian.Uint32(frame[4:8])-uint32(removeEnd-removeStart))
			binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
			binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
			return bytes.Join([][]byte{frame, bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "invalid_utf8_header_value", body: func(t *testing.T) []byte {
			frame := buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"e30="}`))
			valueAt := bytes.Index(frame[12:], []byte("chunk")) + 12
			require.GreaterOrEqual(t, valueAt, 12)
			frame[valueAt] = 0xff
			binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
			return bytes.Join([][]byte{frame, bedrockHandlerStream(t, messageStart, messageStop)}, nil)
		}},
		{name: "semantic_before_start", body: func(t *testing.T) []byte { return bedrockHandlerStream(t, semanticDelta, messageStop) }},
		{name: "stop_before_start", body: func(t *testing.T) []byte { return bedrockHandlerStream(t, messageStop, messageStart) }},
		{name: "delta_without_block_start", body: func(t *testing.T) []byte { return bedrockHandlerStream(t, messageStart, semanticDelta, messageStop) }},
		{name: "stop_with_open_block", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t, messageStart,
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
				messageStop)
		}},
		{name: "duplicate_content_block_index", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t, messageStart,
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
				map[string]any{"type": "content_block_stop", "index": 0},
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
				messageStop)
		}},
		{name: "nonstring_content_block_type", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t, messageStart,
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": 123}},
				messageStop)
		}},
		{name: "nonstring_top_level_event_type", body: func(t *testing.T) []byte {
			return bytes.Join([][]byte{
				bedrockHandlerStream(t, map[string]any{"type": 123}),
				bedrockHandlerStream(t, messageStart, messageStop),
			}, nil)
		}},
		{name: "malformed_message_start_usage", body: func(t *testing.T) []byte {
			badStart := map[string]any{"type": "message_start", "message": map[string]any{"id": "msg-first", "type": "message", "role": "assistant", "content": []any{}, "usage": "bad"}}
			return bedrockHandlerStream(t, badStart, messageStop)
		}},
		{name: "message_stop_without_terminal_delta", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t, messageStart,
				map[string]any{"type": "message_delta", "delta": map[string]any{}, "usage": map[string]any{"output_tokens": 1}},
				messageStop)
		}},
		{name: "malformed_invocation_metrics", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t,
				messageStart,
				map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}, "amazon-bedrock-invocationMetrics": map[string]any{"inputTokenCount": "bad", "outputTokenCount": 1.5}},
				messageStop)
		}},
		{name: "error_payload", body: func(t *testing.T) []byte {
			return bedrockHandlerStream(t,
				map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "retry me"}},
				messageStart, messageStop)
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			groupID := int64(9940 + index*10)
			userID, firstID, secondID := groupID+1, groupID+2, groupID+3
			firstBody := scenario.body(t)
			upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
				firstID:  {body: firstBody, contentType: "application/vnd.amazon.eventstream"},
				secondID: {body: secondBody, contentType: "application/vnd.amazon.eventstream"},
			}}
			got := runBedrockMessagesHandler(t, groupID, userID,
				[]*service.Account{newBedrockHandlerAccount(firstID, 1), newBedrockHandlerAccount(secondID, 2)}, upstream,
				`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, false)

			calls, requests := upstream.snapshot()
			require.NotEmpty(t, calls)
			require.Equal(t, secondID, calls[len(calls)-1])
			require.NotContains(t, got.recorder.Body.String(), "first-leak")
			require.NotContains(t, got.recorder.Body.String(), "{not-json}")
			require.Contains(t, got.recorder.Body.String(), "recovered")
			require.Len(t, got.captures, 1)
			require.Equal(t, secondBody, got.captures[0].RawResponse)
			require.Equal(t, requests[secondID][len(requests[secondID])-1], got.captures[0].RawRequest)
			require.Len(t, got.usages, 1)
			require.Equal(t, secondID, got.usages[0].AccountID)
		})
	}
}

func bedrockHandlerStream(t *testing.T, events ...map[string]any) []byte {
	t.Helper()
	var stream bytes.Buffer
	for _, event := range events {
		_, _ = stream.Write(buildBedrockHandlerChunk(t, event))
	}
	return stream.Bytes()
}

type bedrockHandlerRun struct {
	recorder *httptest.ResponseRecorder
	captures []*service.CaptureRecord
	usages   []*service.UsageLog
}

func runBedrockMessagesHandler(
	t *testing.T,
	groupID, userID int64,
	accounts []*service.Account,
	upstream *bedrockHandlerProvider,
	requestBody string,
	terminalOnly bool,
) bedrockHandlerRun {
	t.Helper()
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
	for _, account := range accounts {
		account.AccountGroups = []service.AccountGroup{{AccountID: account.ID, GroupID: groupID}}
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = len(accounts)
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	if terminalOnly {
		settings = newTerminalOnlyCaptureSettingService(t, cfg)
	}
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	usageRepo := &gatewayAnthropicUsageRepo{}
	gateway := service.NewGatewayService(
		&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settings, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
	)
	handler := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settings, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewBufferString(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: groupID + 90, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})
	handler.Messages(c)
	capturePool.Stop()

	var captures []*service.CaptureRecord
	for len(captureRecords) > 0 {
		captures = append(captures, <-captureRecords)
	}
	return bedrockHandlerRun{recorder: recorder, captures: captures, usages: usageRepo.snapshot()}
}

func newBedrockHandlerAccount(id int64, priority int) *service.Account {
	return &service.Account{
		ID: id, Name: "bedrock-handler", Platform: service.PlatformAnthropic, Type: service.AccountTypeBedrock,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
		Credentials: map[string]any{
			"auth_mode": "apikey", "api_key": "bedrock-provider-secret", "aws_region": "us-east-1",
			"model_mapping": map[string]any{"claude-sonnet-4-6": "anthropic.claude-3-5-sonnet-20240620-v1:0"},
		},
	}
}

func TestBedrockMessagesHandlerNonStreamingInvalidProviderFailsOverAndCapturesOnlyFinalAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secondBody := []byte(`{"id":"msg_bedrock_final","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
	for index, firstBody := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"id":"msg_bedrock_first","type":"message","role":"user","content":[{}],"usage":{"input_tokens":9,"output_tokens":0}}`),
		[]byte(`{"id":"msg_bedrock_first","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":0},"amazon-bedrock-invocationMetrics":{"inputTokenCount":"bad","outputTokenCount":1.5}}`),
		[]byte(`{"id":"msg_bedrock_first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"usage":{"input_tokens":9,"output_tokens":1}}`),
		[]byte(`{"id":"msg_bedrock_first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":null,"usage":{"input_tokens":9,"output_tokens":1}}`),
		[]byte(`{"id":"msg_bedrock_first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"","usage":{"input_tokens":9,"output_tokens":1}}`),
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			groupID := int64(9910 + index*10)
			userID, firstID, secondID := groupID+1, groupID+2, groupID+3
			upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
				firstID:  {body: firstBody},
				secondID: {body: secondBody},
			}}
			got := runBedrockMessagesHandler(t, groupID, userID,
				[]*service.Account{newBedrockHandlerAccount(firstID, 1), newBedrockHandlerAccount(secondID, 2)}, upstream,
				`{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, false)

			calls, requests := upstream.snapshot()
			require.NotEmpty(t, calls)
			require.Equal(t, secondID, calls[len(calls)-1])
			require.NotContains(t, got.recorder.Body.String(), string(firstBody))
			require.Contains(t, got.recorder.Body.String(), "recovered")
			require.Len(t, got.captures, 1)
			require.Equal(t, secondBody, got.captures[0].RawResponse)
			require.Equal(t, requests[secondID][len(requests[secondID])-1], got.captures[0].RawRequest)
			require.Equal(t, "bedrock-account-"+strconv.FormatInt(secondID, 10), got.captures[0].RequestID)
			require.Len(t, got.usages, 1)
			require.Equal(t, secondID, got.usages[0].AccountID)
		})
	}
}

func TestBedrockMessagesHandlerStreamingMissingTerminalFailsOverAndCapturesOnlyFinalAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, firstID, secondID = int64(9920), int64(9921), int64(9922), int64(9923)
	messageStart := map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_bedrock_final", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
		"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 2, "output_tokens": 0},
	}}
	firstBody := bedrockHandlerStream(t, messageStart)
	secondBody := bedrockHandlerStream(t,
		messageStart,
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "recovered"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}},
		map[string]any{"type": "message_stop"},
	)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		firstID:  {body: firstBody, contentType: "application/vnd.amazon.eventstream"},
		secondID: {body: secondBody, contentType: "application/vnd.amazon.eventstream"},
	}}
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(firstID, 1), newBedrockHandlerAccount(secondID, 2)}, upstream,
		`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, false)

	calls, requests := upstream.snapshot()
	require.NotEmpty(t, calls)
	require.Equal(t, secondID, calls[len(calls)-1])
	require.Contains(t, got.recorder.Body.String(), "recovered")
	require.Len(t, got.captures, 1)
	require.Equal(t, secondBody, got.captures[0].RawResponse)
	require.Equal(t, requests[secondID][len(requests[secondID])-1], got.captures[0].RawRequest)
	require.Equal(t, "bedrock-account-9923", got.captures[0].RequestID)
	require.Len(t, got.usages, 1)
	require.Equal(t, secondID, got.usages[0].AccountID)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Equal(t, 1, got.usages[0].OutputTokens)
}

func TestBedrockMessagesHandlerPostsemanticMalformedUsagePreservesTrustedPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, accountID = int64(9924), int64(9925), int64(9926)
	messageStart := map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_bedrock_partial", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
		"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 2, "output_tokens": 0},
	}}
	body := bedrockHandlerStream(t,
		messageStart,
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "trusted-partial"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": "999"}},
	)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		accountID: {body: body, contentType: "application/vnd.amazon.eventstream"},
	}}
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(accountID, 1)}, upstream,
		`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, false)

	require.Contains(t, got.recorder.Body.String(), "trusted-partial")
	require.NotContains(t, got.recorder.Body.String(), `"output_tokens":"999"`)
	require.Len(t, got.captures, 1)
	require.Equal(t, body, got.captures[0].RawResponse)
	require.Len(t, got.usages, 1)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Zero(t, got.usages[0].OutputTokens, "malformed tail usage must not overwrite trusted partial usage")
}

func TestBedrockMessagesHandlerPostterminalDecoderErrorIsCommittedPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, accountID = int64(9927), int64(9928), int64(9929)
	valid := bedrockHandlerStream(t,
		map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_bedrock_tail", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
			"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 2, "output_tokens": 0},
		}},
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "trusted-terminal"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}},
		map[string]any{"type": "message_stop"},
	)
	corruptTail := buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"e30="}`))
	corruptTail[len(corruptTail)-1] ^= 0xff
	body := append(append([]byte(nil), valid...), corruptTail...)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		accountID: {body: body, contentType: "application/vnd.amazon.eventstream"},
	}}
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(accountID, 1)}, upstream,
		`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, false)

	calls, _ := upstream.snapshot()
	require.Equal(t, []int64{accountID}, calls, "a committed partial must never replay on another account")
	require.Contains(t, got.recorder.Body.String(), "trusted-terminal")
	require.Len(t, got.captures, 1)
	require.Equal(t, body, got.captures[0].RawResponse)
	require.Len(t, got.usages, 1)
	require.Equal(t, accountID, got.usages[0].AccountID)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Equal(t, 1, got.usages[0].OutputTokens)
}

func TestBedrockMessagesHandlerFinalHTTPErrorCapturesNativeExchangeExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, accountID = int64(9930), int64(9931), int64(9932)
	errorBody := []byte(`{"message":"bedrock rejected final request","type":"invalid_request"}`)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		accountID: {status: http.StatusBadRequest, body: errorBody, requestID: "bedrock-terminal-request-id"},
	}}
	inbound := `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(accountID, 1)}, upstream, inbound, true)

	_, requests := upstream.snapshot()
	require.Equal(t, http.StatusBadRequest, got.recorder.Code)
	require.Len(t, got.captures, 1)
	record := got.captures[0]
	require.Equal(t, service.PlatformAnthropic, record.Platform)
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, "bedrock-terminal-request-id", record.RequestID)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, requests[accountID][len(requests[accountID])-1], record.RawRequest)
	require.NotEqual(t, []byte(inbound), record.RawRequest)
	require.Contains(t, string(record.RawRequest), "anthropic_version")
	require.Contains(t, string(record.ResponseHeaders), "X-Amzn-Requestid")
	require.Contains(t, string(record.ResponseHeaders), "X-Provider-Debug")
	require.NotContains(t, string(record.ResponseHeaders), "X-Request-Id")
	require.NotContains(t, string(record.RequestHeaders), "bedrock-provider-secret")
	require.Empty(t, got.usages)
}

func TestBedrockMessagesHandlerFinalMalformedEventStreamCapturesNativeExchangeExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, accountID = int64(9990), int64(9991), int64(9992)
	malformedBody := buildBedrockHandlerEnvelopeChunk(t, []byte(`{"bytes":"not-valid-base64"}`))
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		accountID: {
			body:        malformedBody,
			contentType: "application/vnd.amazon.eventstream",
			requestID:   "bedrock-malformed-terminal-request-id",
		},
	}}
	inbound := `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(accountID, 1)}, upstream, inbound, true)

	_, requests := upstream.snapshot()
	require.Equal(t, http.StatusBadGateway, got.recorder.Code)
	require.NotContains(t, got.recorder.Body.String(), "not-valid-base64")
	require.Len(t, got.captures, 1)
	record := got.captures[0]
	require.Equal(t, http.StatusOK, record.HTTPStatus)
	require.Equal(t, "bedrock-malformed-terminal-request-id", record.RequestID)
	require.Equal(t, malformedBody, record.RawResponse)
	require.Equal(t, requests[accountID][len(requests[accountID])-1], record.RawRequest)
	require.NotEqual(t, []byte(inbound), record.RawRequest)
	require.Contains(t, string(record.RawRequest), "anthropic_version")
	require.Contains(t, string(record.ResponseHeaders), "X-Amzn-Requestid")
	require.Contains(t, string(record.ResponseHeaders), "X-Provider-Debug")
	require.NotContains(t, string(record.RequestHeaders), "bedrock-provider-secret")
	require.Empty(t, got.usages)
}

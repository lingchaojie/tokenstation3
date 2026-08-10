package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCaptureDecisionShortCircuitsOpenAIBeforeBufferAllocation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)

	_, ok := CaptureDecisionFor(c, "openai", CaptureOutcomeSuccess)
	require.False(t, ok)
	require.False(t, CaptureMayApplyFor(c, "openai"))
	_, exists := c.Get(captureResultContextKey)
	require.False(t, exists)
}

func TestCaptureDecisionRequiresBothRequestScopeFilters(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.GroupIDs = []int64{7}
	policy.UserIDs = []int64{9}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	group := int64(7)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCompiledCaptureScopeForTest(c, compiled, 9, &group)
	content, ok := CaptureDecisionFor(c, "openai", CaptureOutcomeTerminalError)
	require.True(t, ok)
	require.Equal(t, policy.Content, content)

	otherUser, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCompiledCaptureScopeForTest(otherUser, compiled, 8, &group)
	_, ok = CaptureDecisionFor(otherUser, "openai", CaptureOutcomeSuccess)
	require.False(t, ok)
}

func TestPrepareCaptureScopeFailsClosedForNilOrFailedSettingService(t *testing.T) {
	for _, settingService := range []*SettingService{
		nil,
		NewSettingService(&capturePolicyRepoStub{getErr: context.DeadlineExceeded}, nil),
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		PrepareCaptureScope(context.Background(), c, settingService, 9, nil)
		_, ok := CaptureDecisionFor(c, "anthropic", CaptureOutcomeSuccess)
		require.False(t, ok)
	}
}

func TestApplyCaptureContentPolicyKeepsMetadataAndClearsDisabledFields(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest:      []byte(`{"metadata":{"user_id":"{\"device_id\":\"device-a\",\"session_id\":\"session-a\"}"}}`),
		RawResponse:     []byte(`{"stop_reason":"end_turn","usage":{"output_tokens":4}}`),
		RequestHeaders:  []byte(`{"Anthropic-Version":["2023-06-01"]}`),
		ResponseHeaders: []byte(`{"X-Request-Id":["req-a"]}`),
	}
	extractCaptureColumns(rec)
	ApplyCaptureContentPolicy(rec, CaptureContentPolicy{})

	require.Equal(t, "session-a", rec.SessionID)
	require.Equal(t, "end_turn", rec.StopReason)
	require.Equal(t, 4, rec.OutputTokens)
	require.Empty(t, rec.RawRequest)
	require.Empty(t, rec.RawResponse)
	require.Empty(t, rec.RequestHeaders)
	require.Empty(t, rec.ResponseHeaders)
}

func TestCapturePoolAppliesContentPolicyAfterMetadataExtraction(t *testing.T) {
	writer := newDeferredLifecycleWriter()
	pool := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 8, OverflowPolicy: "drop", MaxQueueBytes: 1024,
	}, writer)
	defer pool.Stop()
	policy := CaptureContentPolicy{}
	rec := &CaptureRecord{
		RawRequest:     []byte(`{"conversation_id":"conversation-a"}`),
		RawResponse:    []byte(`{"usage":{"output_tokens":7}}`),
		RequestHeaders: []byte(`{"X-Test":["request"]}`),
		ContentPolicy:  &policy,
	}

	pool.Submit(rec)
	item := writer.take(t)
	require.Equal(t, "conversation-a", item.record.SessionID)
	require.Equal(t, 7, item.record.OutputTokens)
	require.Empty(t, item.record.RawRequest)
	require.Empty(t, item.record.RawResponse)
	require.Empty(t, item.record.RequestHeaders)
	item.completeSuccess()
}

func TestCaptureExchangeBridgeKeepsFinalOutboundAndRawUpstreamSnapshots(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, err := http.NewRequest(http.MethodPost, "https://api.example.test/v1/responses", bytes.NewReader(nil))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("OpenAI-Beta", "responses=v1")
	body := []byte(`{"model":"mapped-model"}`)

	SetCaptureOutboundRequest(c, req, body, 1024)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Request-Id": []string{"upstream-a"}},
		Request:    req,
	}
	setCaptureResult(c, resp, []byte(`{"id":"resp-a"}`), false)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, body, bridge.Request)
	require.Equal(t, "https://api.example.test/v1/responses", bridge.UpstreamEndpoint)
	require.Equal(t, 200, bridge.HTTPStatus)
	require.JSONEq(t, `{"Openai-Beta":["responses=v1"]}`, string(bridge.RequestHeaders))
	require.NotContains(t, string(bridge.RequestHeaders), "secret")
	require.JSONEq(t, `{"id":"resp-a"}`, string(bridge.Response))
}

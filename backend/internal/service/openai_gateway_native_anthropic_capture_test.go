//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func nativeAnthropicUsageThenErrorSSE() string {
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_error","type":"message","role":"assistant","content":[],"model":"glm-4.7","usage":{"input_tokens":10,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":5}}`,
		"",
		"event: error",
		`data: {"type":"error","error":{"type":"overloaded_error","message":"provider overloaded after partial output"}}`,
		"",
		"",
	}, "\n")
}

func setOpenAITerminalErrorDisabledCaptureScopeForTest(t *testing.T, c *gin.Context) {
	t.Helper()
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
}

// Native Anthropic protocol accounts use CN platform names, which intentionally
// are not capture-policy schema values. Seed an OpenAI-protocol typed attempt so
// this test exercises the public forward result all the way through the real
// terminal sink without widening the persisted policy schema.
func beginOpenAIProtocolCaptureForNativeAnthropicTest(
	t *testing.T,
	c *gin.Context,
	pool *ConversationCapturePool,
	responseBody io.ReadCloser,
) (*http.Response, *recordingCaptureAttempt) {
	t.Helper()
	setOpenAITerminalErrorDisabledCaptureScopeForTest(t, c)
	wireRequest := httptest.NewRequest(http.MethodPost, "https://capture.test/v1/messages", bytes.NewReader(nil))
	attempt, ok := beginCaptureAttemptForWireRequest(
		c.Request.Context(), c, pool, PlatformOpenAI, wireRequest,
		[]byte(`{"model":"glm-4.7","stream":true}`), 1<<20,
	)
	require.True(t, ok)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       newCaptureResponseReader(responseBody, attempt),
	}
	setCaptureAttemptResponseHTTPStatus(c, attempt, response.StatusCode)
	attempt.WriteResponseHeaders(captureHeaderBytes(response.Header, 1<<20))
	recording := pool.transport.(*recordingCaptureTransport).Attempts()
	require.Len(t, recording, 1)
	return response, recording[0]
}

func TestNativeAnthropicPublicForwardsAbortTerminalErrorDisabledCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name string
		path string
		body []byte
		run  func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"glm-4.7","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":true}}`),
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"glm-4.7","input":"hello","stream":true}`),
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(context.Background(), c, account, body)
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: []byte(`{"model":"glm-4.7","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
			cfg := captureEnabledConfigForTest(1 << 20)
			cfg.Security.URLAllowlist.Enabled = false
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: pool}
			account := &Account{
				ID: 901, Name: "native-anthropic", Platform: PlatformZhipu,
				Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "secret", "api_protocol": APIProtocolAnthropic,
					"base_url": "https://anthropic.test",
				},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			response, captureAttempt := beginOpenAIProtocolCaptureForNativeAnthropicTest(
				t, c, pool, io.NopCloser(strings.NewReader(nativeAnthropicUsageThenErrorSSE())),
			)
			upstream.resp = response

			result, err := tc.run(svc, c, account, tc.body)

			var streamErr *sseStreamErrorEventError
			require.ErrorAs(t, err, &streamErr)
			require.Contains(t, streamErr.RawData, "provider overloaded")
			require.NotNil(t, result)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.True(t, result.ClientDisconnect)
			require.True(t, result.CaptureTerminalError, "public forward must classify its usage-bearing error")
			require.False(t, result.CaptureResponseComplete)
			require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
			require.Equal(t, []captureTerminalState{captureAborted}, captureAttempt.TerminalStates())
			require.Empty(t, captureAttempt.Finals())
			require.Equal(t, model.PayloadSSE, captureAttempt.begin.Format)
		})
	}
}

func TestNativeAnthropicErrorEventIsTypedWithoutEventName(t *testing.T) {
	dataOnly := `{"type":"error","error":{"type":"api_error","message":"typed from payload"}}`
	require.True(t, anthropicStreamEventIsError("", dataOnly))
	require.True(t, anthropicStreamEventIsError("error", `{}`))
	require.False(t, anthropicStreamEventIsError("message_delta", `{"type":"message_delta"}`))
	var streamErr *sseStreamErrorEventError
	require.True(t, errors.As(&sseStreamErrorEventError{RawData: dataOnly}, &streamErr))
}

func TestChatFallbackPublicForwardsAbortTerminalErrorDisabledCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	chatStream := strings.Join([]string{
		`data: {"id":"chatcmpl_partial","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_partial","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_partial","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4,"total_tokens":10}}`,
		"",
	}, "\n")

	for _, tc := range []struct {
		name    string
		path    string
		body    []byte
		account func() *Account
		run     func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name:    "responses fallback",
			path:    "/v1/responses",
			body:    []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`),
			account: forceChatResponsesFallbackAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(context.Background(), c, account, body)
			},
		},
		{
			name:    "messages fallback",
			path:    "/v1/messages",
			body:    []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`),
			account: forceChatMessagesFallbackAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
			cfg := captureEnabledConfigForTest(1 << 20)
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       &errTailReader{data: []byte(chatStream), err: io.ErrUnexpectedEOF},
			}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: pool}
			account := tc.account()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			c.Writer = &failingGinWriter{ResponseWriter: c.Writer, failAfter: 0}
			setOpenAITerminalErrorDisabledCaptureScopeForTest(t, c)

			result, err := tc.run(svc, c, account, tc.body)

			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.NotNil(t, result)
			require.Equal(t, 6, result.Usage.InputTokens)
			require.Equal(t, 4, result.Usage.OutputTokens)
			require.True(t, result.ClientDisconnect)
			require.True(t, result.CaptureTerminalError)
			require.False(t, result.CaptureResponseComplete)
			require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
			require.Empty(t, attempts[0].Finals())
		})
	}
}

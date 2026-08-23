package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cursorCaptureRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip cursorCaptureRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type cursorCaptureCase struct {
	name       string
	path       string
	body       []byte
	stream     bool
	wantFormat model.PayloadFormat
	wantStop   string
	forward    func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
}

func cursorCaptureCases() []cursorCaptureCase {
	return []cursorCaptureCase{
		{
			name: "chat_json", path: "/v1/chat/completions", stream: false, wantFormat: model.PayloadJSON, wantStop: "stop",
			body: []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorChatCompletions(ctx, c, account, body, "")
			},
		},
		{
			name: "chat_sse", path: "/v1/chat/completions", stream: true, wantFormat: model.PayloadSSE, wantStop: "stop",
			body: []byte(`{"model":"auto","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorChatCompletions(ctx, c, account, body, "")
			},
		},
		{
			name: "responses_json", path: "/v1/responses", stream: false, wantFormat: model.PayloadJSON, wantStop: "completed",
			body: []byte(`{"model":"auto","input":"hello"}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorResponses(ctx, c, account, body, "", false, time.Now())
			},
		},
		{
			name: "responses_sse", path: "/v1/responses", stream: true, wantFormat: model.PayloadSSE, wantStop: "completed",
			body: []byte(`{"model":"auto","stream":true,"input":"hello"}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorResponses(ctx, c, account, body, "", false, time.Now())
			},
		},
		{
			name: "messages_json", path: "/v1/messages", stream: false, wantFormat: model.PayloadJSON, wantStop: "end_turn",
			body: []byte(`{"model":"auto","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorAnthropic(ctx, c, account, body, "")
			},
		},
		{
			name: "messages_sse", path: "/v1/messages", stream: true, wantFormat: model.PayloadSSE, wantStop: "end_turn",
			body: []byte(`{"model":"auto","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorAnthropic(ctx, c, account, body, "")
			},
		},
	}
}

func TestCursorCaptureStoresSixCallerProtocolModes(t *testing.T) {
	for _, test := range cursorCaptureCases() {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			svc := cursorCaptureService(t, transport, cursorCaptureSuccessOpener(t))
			c, recorder := cursorCaptureContext(t, test.path, test.body, true)
			account := cursorChatForwardAccount(t)

			result, err := test.forward(svc, c.Request.Context(), c, account, test.body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, transport.Attempts(), 1)
			attempt := transport.Attempts()[0]
			require.NotEqual(t, model.PayloadFormat("connect_proto"), attempt.begin.Format)
			require.Equal(t, test.wantFormat, attempt.begin.Format)
			require.Equal(t, PlatformCursor, attempt.begin.Platform)
			require.Equal(t, "auto", attempt.begin.RequestedModel)
			require.Equal(t, cursorpkg.AgentDefaultModel, attempt.begin.UpstreamModel)
			require.Equal(t, cursorAgentEndpoint, attempt.begin.UpstreamEndpoint)
			require.Equal(t, test.stream, attempt.begin.Stream)
			require.Equal(t, test.body, attempt.RequestBytes())
			require.Equal(t, recorder.Body.Bytes(), attempt.ResponseBytes())
			require.NotContains(t, string(attempt.ResponseBytes()), "exec_stream_close")
			require.NotContains(t, string(attempt.ResponseBytes()), "connect-protocol-version")
			if strings.HasPrefix(test.name, "responses") || strings.HasPrefix(test.name, "messages") {
				require.NotContains(t, string(attempt.ResponseBytes()), "chat.completion")
			}

			require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
			require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result), "handler-owned commit is exact once")
			AbortCaptureAttempt(c)
			require.Equal(t, []captureTerminalState{captureCommitted}, attempt.TerminalStates())
			finals := attempt.Finals()
			require.Len(t, finals, 1)
			require.Equal(t, model.Final{
				HTTPStatus: 200, InputTokens: 13, OutputTokens: 8, CacheReadTokens: 2,
				CacheCreationTokens: 1, ResponseComplete: true,
			}, finals[0])
			require.Empty(t, finals[0].StopReason, "the delivered JSON/SSE extractor owns stop reason")

			record := cursorCaptureRecord(attempt, test.stream)
			extractCaptureColumns(record)
			require.Equal(t, test.wantStop, strings.ToLower(record.StopReason))
			require.Equal(t, http.StatusOK, record.HTTPStatus)
			require.Contains(t, string(record.ResponseHeaders), "Content-Type")
			require.NotContains(t, string(record.RequestHeaders), "inbound-secret")
			require.NotContains(t, string(record.RequestHeaders), "session-secret")
			require.NotContains(t, string(record.ResponseHeaders), "response-secret")
		})
	}
}

func TestCursorCaptureLocalOutputLimitUsesDeliveredStopReason(t *testing.T) {
	for _, test := range []struct {
		name     string
		path     string
		body     []byte
		wantStop string
		forward  func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "chat", path: "/v1/chat/completions", wantStop: "length",
			body: []byte(`{"model":"auto","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorChatCompletions(c.Request.Context(), c, account, body, "")
			},
		},
		{
			name: "responses", path: "/v1/responses", wantStop: "incomplete",
			body: []byte(`{"model":"auto","max_output_tokens":1,"input":"hello"}`),
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorResponses(c.Request.Context(), c, account, body, "", false, time.Now())
			},
		},
		{
			name: "messages", path: "/v1/messages", wantStop: "max_tokens",
			body: []byte(`{"model":"auto","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`),
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.forwardCursorAnthropic(c.Request.Context(), c, account, body, "")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			svc := cursorCaptureService(t, transport, cursorCaptureOpener(t, bytes.Join([][]byte{
				cursorCaptureTextFrame(strings.Repeat("z", 100)), cursorCaptureTurnEndedFrame(99, 99, 0, 0),
			}, nil)))
			c, _ := cursorCaptureContext(t, test.path, test.body, true)

			result, err := test.forward(svc, c, cursorChatForwardAccount(t), test.body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.CaptureResponseComplete)
			require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
			attempt := transport.Attempts()[0]
			final := attempt.Finals()[0]
			require.False(t, final.ResponseComplete)
			require.Empty(t, final.StopReason)
			record := cursorCaptureRecord(attempt, false)
			extractCaptureColumns(record)
			require.Equal(t, test.wantStop, strings.ToLower(record.StopReason))
		})
	}
}

func TestCursorCapturePartialWriterStoresOnlyDeliveredPrefix(t *testing.T) {
	transport := &recordingCaptureTransport{}
	svc := cursorCaptureService(t, transport, cursorCaptureSuccessOpener(t))
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := cursorCaptureContext(t, "/v1/chat/completions", body, true)
	c.Writer = &cursorCapturePartialWriter{ResponseWriter: c.Writer, limit: 23}

	result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), body, "")
	require.NoError(t, err)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.CaptureResponseComplete, "the provider terminal remains authoritative after a caller write failure")
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	attempt := transport.Attempts()[0]
	require.Equal(t, recorder.Body.Bytes(), attempt.ResponseBytes())
	require.Len(t, attempt.ResponseBytes(), 23)
	require.NotContains(t, string(attempt.ResponseBytes()), "finish_reason")
	require.Equal(t, []model.Final{{HTTPStatus: 200, InputTokens: 13, OutputTokens: 8, CacheReadTokens: 2, CacheCreationTokens: 1, ResponseComplete: true}}, attempt.Finals())
}

func TestCursorCaptureProviderFailureAfterPartialWriteOutranksDisconnect(t *testing.T) {
	transport := &recordingCaptureTransport{}
	providerBody := bytes.Join([][]byte{
		cursorCaptureTextFrame("visible"),
		cursorpkg.EncodeFrame([]byte(`{"error":{"code":"unavailable","message":"private provider failure"}}`), true),
	}, nil)
	svc := cursorCaptureService(t, transport, cursorCaptureOpener(t, providerBody))
	body := []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := cursorCaptureContext(t, "/v1/chat/completions", body, true)
	c.Writer = &cursorCapturePartialWriter{ResponseWriter: c.Writer, limit: 17}

	result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.False(t, result.CaptureResponseComplete)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	attempt := transport.Attempts()[0]
	require.Equal(t, recorder.Body.Bytes(), attempt.ResponseBytes())
	require.Len(t, attempt.ResponseBytes(), 17)
	require.NotContains(t, string(attempt.ResponseBytes()), "private provider failure")
	require.Equal(t, []model.Final{{HTTPStatus: 200, InputTokens: 2, OutputTokens: 2, ResponseComplete: false}}, attempt.Finals())
	record := cursorCaptureRecord(attempt, true)
	extractCaptureColumns(record)
	require.Empty(t, record.StopReason)
}

func TestCursorCaptureRetryReplacesAttemptAndCommitIsExactOnce(t *testing.T) {
	transport := &recordingCaptureTransport{}
	svc := cursorCaptureService(t, transport, cursorCaptureOpener(t,
		cursorpkg.EncodeFrame([]byte(`{"error":{"code":"unavailable","message":"retry"}}`), true),
	))
	firstBody := []byte(`{"model":"auto","messages":[{"role":"user","content":"first"}]}`)
	c, recorder := cursorCaptureContext(t, "/v1/chat/completions", firstBody, true)
	result, firstErr := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), firstBody, "")
	require.Nil(t, result)
	require.Error(t, firstErr)
	require.Empty(t, recorder.Body.Bytes())
	require.Len(t, transport.Attempts(), 1)
	first := transport.Attempts()[0]

	secondBody := []byte(`{"model":"auto","messages":[{"role":"user","content":"second"}]}`)
	svc.cursorAgentStreamOpener = cursorCaptureSuccessOpener(t)
	result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), secondBody, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, transport.Attempts(), 2)
	second := transport.Attempts()[1]
	require.Equal(t, []captureTerminalState{captureAborted}, first.TerminalStates())
	require.Equal(t, firstBody, first.RequestBytes())
	require.Empty(t, first.ResponseBytes())
	require.Equal(t, secondBody, second.RequestBytes())
	require.Equal(t, recorder.Body.Bytes(), second.ResponseBytes())

	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	AbortCaptureAttempt(c)
	require.Equal(t, []captureTerminalState{captureCommitted}, second.TerminalStates())
}

func TestCursorCaptureRedactsAndTruncatesCallerHeaders(t *testing.T) {
	transport := &recordingCaptureTransport{}
	svc := cursorCaptureService(t, transport, cursorCaptureSuccessOpener(t))
	svc.cfg.Gateway.Capture.MaxHeaderBytes = 64
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	c, _ := cursorCaptureContext(t, "/v1/chat/completions", body, true)
	c.Request.Header.Set("X-Oversized-Request", strings.Repeat("r", 256))
	c.Writer.Header().Set("X-Oversized-Response", strings.Repeat("s", 256))

	result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), body, "")
	require.NoError(t, err)
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	attempt := transport.Attempts()[0]
	require.LessOrEqual(t, len(attempt.RequestHeaderBytes()), 64)
	require.LessOrEqual(t, len(attempt.ResponseHeaderBytes()), 64)
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "inbound-secret")
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "session-secret")
	require.NotContains(t, string(attempt.ResponseHeaderBytes()), "response-secret")
}

func TestCursorCaptureWriteFailureIsFailOpenForForwarding(t *testing.T) {
	transport := &recordingCaptureTransport{failWriteAt: 2}
	svc := cursorCaptureService(t, transport, cursorCaptureSuccessOpener(t))
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := cursorCaptureContext(t, "/v1/chat/completions", body, true)

	result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, recorder.Body.Bytes(), "capture write failure must not change caller delivery")
	require.True(t, CaptureUsesStreamingAttempt(c))
	require.False(t, captureAttemptUsableForRequest(c))
	require.Empty(t, transport.Attempts()[0].RequestBytes())
	require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
	require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
}

func TestCursorCaptureGuardsFailOpenWithoutAttemptOrLegacyFallback(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*testing.T, *OpenAIGatewayService, *gin.Context, *recordingCaptureTransport)
		wantBegins  int
		wantAttempt bool
	}{
		{
			name: "static disabled",
			configure: func(_ *testing.T, svc *OpenAIGatewayService, _ *gin.Context, _ *recordingCaptureTransport) {
				svc.cfg.Gateway.Capture.Enabled = false
			},
		},
		{
			name: "nil pool",
			configure: func(_ *testing.T, svc *OpenAIGatewayService, _ *gin.Context, _ *recordingCaptureTransport) {
				svc.capturePool = nil
			},
		},
		{
			name: "missing scope",
			configure: func(_ *testing.T, _ *OpenAIGatewayService, c *gin.Context, _ *recordingCaptureTransport) {
				c.Set(captureScopeContextKey, nil)
			},
		},
		{
			name: "nonmatching policy",
			configure: func(t *testing.T, _ *OpenAIGatewayService, c *gin.Context, _ *recordingCaptureTransport) {
				policy := DefaultCaptureRuntimePolicy()
				policy.Enabled = true
				policy.Platforms.Cursor = false
				compiled, err := CompileCaptureRuntimePolicy(policy)
				require.NoError(t, err)
				setCompiledCaptureScopeForTest(c, compiled, 9, nil)
			},
		},
		{
			name: "admission failure", wantBegins: 1,
			configure: func(_ *testing.T, _ *OpenAIGatewayService, _ *gin.Context, transport *recordingCaptureTransport) {
				transport.beginErr = errors.New("capture unavailable")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			svc := cursorCaptureService(t, transport, cursorCaptureSuccessOpener(t))
			body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
			c, recorder := cursorCaptureContext(t, "/v1/chat/completions", body, true)
			test.configure(t, svc, c, transport)

			result, err := svc.forwardCursorChatCompletions(c.Request.Context(), c, cursorChatForwardAccount(t), body, "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotEmpty(t, recorder.Body.Bytes(), "capture failure must not change forwarding")
			require.Equal(t, test.wantBegins, transport.Begins())
			require.Nil(t, captureAttemptForRequest(c))
			require.True(t, CaptureUsesStreamingAttempt(c), "a Cursor attempt must never fall back to legacy whole-body capture")
			_, legacy := takeCaptureResult(c)
			require.False(t, legacy)
		})
	}
}

type cursorCapturePartialWriter struct {
	gin.ResponseWriter
	limit int
	once  sync.Once
}

func (writer *cursorCapturePartialWriter) Write(payload []byte) (int, error) {
	n := 0
	writer.once.Do(func() {
		n = writer.limit
		if n > len(payload) {
			n = len(payload)
		}
		if n > 0 {
			_, _ = writer.ResponseWriter.Write(payload[:n])
		}
	})
	return n, io.ErrClosedPipe
}

func cursorCaptureService(t *testing.T, transport *recordingCaptureTransport, opener cursorAgentStreamOpener) *OpenAIGatewayService {
	t.Helper()
	return &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{
			Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20,
		}}},
		capturePool:             newConversationCapturePoolForTransport(transport, func() bool { return true }),
		cursorAgentStreamOpener: opener,
	}
}

func cursorCaptureContext(t *testing.T, path string, body []byte, enabled bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer inbound-secret")
	c.Request.Header.Set("Cookie", "session=session-secret")
	c.Writer.Header().Set("X-Capture-Visible", "visible-response-header")
	c.Writer.Header().Set("Set-Cookie", "response=response-secret")
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = enabled
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	SetCaptureRequestedModel(c, "auto")
	return c, recorder
}

func cursorCaptureSuccessOpener(t *testing.T) cursorAgentStreamOpener {
	t.Helper()
	return cursorCaptureOpener(t, bytes.Join([][]byte{
		cursorCaptureTextFrame("hello from Cursor"), cursorCaptureTurnEndedFrame(13, 8, 2, 1),
	}, nil))
}

func cursorCaptureOpener(t *testing.T, providerBody []byte) cursorAgentStreamOpener {
	t.Helper()
	return func(ctx context.Context, params cursorpkg.AgentRunParams, _ cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
		client := &http.Client{Transport: cursorCaptureRoundTrip(func(request *http.Request) (*http.Response, error) {
			drained := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, request.Body)
				close(drained)
			}()
			t.Cleanup(func() {
				select {
				case <-drained:
				case <-time.After(time.Second):
					t.Error("local Cursor capture request writer did not stop")
				}
			})
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/2.0", ProtoMajor: 2,
				Header: http.Header{"X-Request-Id": {"cursor-capture-local"}},
				Body:   io.NopCloser(bytes.NewReader(providerBody)), Request: request,
			}, nil
		})}
		return cursorpkg.OpenAgentStream(ctx, params, cursorpkg.AgentStreamOptions{
			BaseURL: "https://local.invalid", Token: "local-test-token", HTTPClient: client,
			FirstByteTimeout: time.Second, IdleTimeout: time.Second, HeartbeatInterval: time.Hour,
		})
	}
}

func cursorCaptureTextFrame(text string) []byte {
	var delta, update, top cursorpkg.Writer
	delta.WriteString(1, text)
	update.WriteBytes(1, delta.Bytes())
	top.WriteBytes(1, update.Bytes())
	return cursorpkg.EncodeFrame(top.Bytes(), false)
}

func cursorCaptureTurnEndedFrame(input, output, cacheRead, cacheWrite int64) []byte {
	var usage, update, top cursorpkg.Writer
	usage.WriteInt64(1, input)
	usage.WriteInt64(2, output)
	usage.WriteInt64(3, cacheRead)
	usage.WriteInt64(4, cacheWrite)
	update.WriteBytes(14, usage.Bytes())
	top.WriteBytes(1, update.Bytes())
	return cursorpkg.EncodeFrame(top.Bytes(), false)
}

func cursorCaptureRecord(attempt *recordingCaptureAttempt, stream bool) *CaptureRecord {
	finals := attempt.Finals()
	record := &CaptureRecord{
		Platform: PlatformCursor, Stream: stream, RawRequest: attempt.RequestBytes(), RawResponse: attempt.ResponseBytes(),
		RequestHeaders: attempt.RequestHeaderBytes(), ResponseHeaders: attempt.ResponseHeaderBytes(),
	}
	if len(finals) == 1 {
		record.HTTPStatus = int(finals[0].HTTPStatus)
		record.InputTokens = int(finals[0].InputTokens)
		record.OutputTokens = int(finals[0].OutputTokens)
		record.CacheReadTokens = int(finals[0].CacheReadTokens)
		record.CacheCreationTokens = int(finals[0].CacheCreationTokens)
		record.Truncated = !finals[0].ResponseComplete
	}
	return record
}

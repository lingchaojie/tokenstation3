//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func task8BedrockFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()
	var headerBytes bytes.Buffer
	for name, value := range headers {
		require.NoError(t, headerBytes.WriteByte(byte(len(name))))
		_, err := headerBytes.WriteString(name)
		require.NoError(t, err)
		require.NoError(t, headerBytes.WriteByte(7))
		require.NoError(t, binary.Write(&headerBytes, binary.BigEndian, uint16(len(value))))
		_, err = headerBytes.WriteString(value)
		require.NoError(t, err)
	}

	headersRaw := headerBytes.Bytes()
	totalLen := uint32(12 + len(headersRaw) + len(payload) + 4)
	var prelude bytes.Buffer
	require.NoError(t, binary.Write(&prelude, binary.BigEndian, totalLen))
	require.NoError(t, binary.Write(&prelude, binary.BigEndian, uint32(len(headersRaw))))
	preludeRaw := prelude.Bytes()

	var frame bytes.Buffer
	_, err := frame.Write(preludeRaw)
	require.NoError(t, err)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(preludeRaw)))
	_, err = frame.Write(headersRaw)
	require.NoError(t, err)
	_, err = frame.Write(payload)
	require.NoError(t, err)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func task8BedrockChunkFrame(t *testing.T, eventJSON string) []byte {
	payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(eventJSON)) + `"}`)
	return task8BedrockFrame(t, map[string]string{
		":event-type":   "chunk",
		":message-type": "event",
	}, payload)
}

func task8BedrockExceptionFrame(t *testing.T) []byte {
	return task8BedrockFrame(t, map[string]string{
		":event-type":     "modelStreamErrorException",
		":message-type":   "exception",
		":exception-type": "modelStreamErrorException",
	}, []byte(`{"message":"provider stream failed"}`))
}

func task8RunBedrockStream(t *testing.T, body []byte) (*bedrockStreamingResult, error, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
	svc := &GatewayService{cfg: &config.Config{}}
	result, err := svc.handleBedrockStreamingResponse(context.Background(), resp, c, &Account{ID: 8}, time.Now(), "model")
	return result, err, recorder
}

func TestTask8BedrockExceptionBeforeOutputReturnsTypedFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"anthropic.claude-3-5-sonnet-20240620-v1:0","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(task8BedrockExceptionFrame(t))),
	}}
	svc := &GatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{},
		deferredService:  &DeferredService{},
	}
	account := &Account{
		ID: 801, Name: "bedrock-first", Platform: PlatformAnthropic, Type: AccountTypeBedrock,
		Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test-key", "aws_region": "us-east-1"},
		Status:      StatusActive, Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result, "pre-output provider failure must not create a committed/terminal result")
	require.Zero(t, recorder.Body.Len())
	require.Equal(t, GatewayFailureStageInference, failoverErr.Stage)
	require.Equal(t, GatewayFailureScopeProvider, failoverErr.Scope)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.True(t, errors.Is(err, failoverErr))
}

func TestTask8BedrockExceptionAfterOutputPreservesCommittedPartial(t *testing.T) {
	body := append(task8BedrockChunkFrame(t, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`), task8BedrockExceptionFrame(t)...)
	result, err, recorder := task8RunBedrockStream(t, body)

	require.ErrorContains(t, err, "bedrock exception")
	require.NotNil(t, result)
	require.True(t, result.semanticOutput)
	require.Contains(t, recorder.Body.String(), "hello")
}

func task8ForwardBedrockStream(t *testing.T, body []byte) (*ForwardResult, error, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	requestBody := []byte(`{"model":"anthropic.claude-3-5-sonnet-20240620-v1:0","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformAnthropic)
	require.NoError(t, err)

	svc := &GatewayService{
		cfg: &config.Config{},
		httpUpstream: &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}},
		rateLimitService: &RateLimitService{},
		deferredService:  &DeferredService{},
	}
	account := &Account{
		ID: 8, Name: "bedrock-visible", Platform: PlatformAnthropic, Type: AccountTypeBedrock,
		Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test-key", "aws_region": "us-east-1"},
		Status:      StatusActive, Schedulable: true,
	}
	result, err := svc.Forward(context.Background(), c, account, parsed)
	return result, err, recorder
}

func TestTask8BedrockVisibleNonSemanticFrameThenExceptionNeverReplays(t *testing.T) {
	body := append(task8BedrockChunkFrame(t, `{"type":"message_start","message":{"id":"msg-visible","type":"message","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`), task8BedrockExceptionFrame(t)...)
	result, err, recorder := task8ForwardBedrockStream(t, body)

	require.ErrorContains(t, err, "bedrock exception")
	require.NotNil(t, result, "a visible downstream frame commits the attempt even without semantic output")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "a committed visible frame must never become a retryable typed failure")
	require.Contains(t, recorder.Body.String(), "message_start")
	require.True(t, result.CaptureTerminalError)
}

func TestTask8BedrockVisibleHeartbeatThenExceptionNeverReplays(t *testing.T) {
	body := append(task8BedrockChunkFrame(t, `{"type":"ping"}`), task8BedrockExceptionFrame(t)...)
	result, err, recorder := task8ForwardBedrockStream(t, body)

	require.ErrorContains(t, err, "bedrock exception")
	require.NotNil(t, result, "a flushed heartbeat commits the downstream stream")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, recorder.Body.String(), "event: ping")
}

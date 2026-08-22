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

type bedrockFramesThenError struct {
	frames *bytes.Reader
	err    error
}

func (r *bedrockFramesThenError) Read(p []byte) (int, error) {
	if r.frames.Len() > 0 {
		return r.frames.Read(p)
	}
	return 0, r.err
}

func (r *bedrockFramesThenError) Close() error { return nil }

type bedrockPartialClientWriteErrorWriter struct {
	gin.ResponseWriter
	wrote bool
}

func (w *bedrockPartialClientWriteErrorWriter) Write(p []byte) (int, error) {
	if w.wrote || len(p) == 0 {
		return 0, errors.New("write failed: client disconnected")
	}
	w.wrote = true
	return 1, errors.New("write failed: client disconnected")
}

func buildBedrockServiceChunkFrame(t *testing.T, eventJSON string) []byte {
	t.Helper()

	envelope := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(eventJSON)) + `"}`)
	var headers bytes.Buffer
	for _, header := range [][2]string{{":event-type", "chunk"}, {":message-type", "event"}} {
		require.NoError(t, headers.WriteByte(byte(len(header[0]))))
		_, _ = headers.WriteString(header[0])
		require.NoError(t, headers.WriteByte(7))
		require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(header[1]))))
		_, _ = headers.WriteString(header[1])
	}

	totalLength := uint32(12 + headers.Len() + len(envelope) + 4)
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLength)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(headers.Len()))

	var frame bytes.Buffer
	_, _ = frame.Write(prelude)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(envelope)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func TestBedrockClientDisconnectReturnsCollectedUsageOnCanceledProviderStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var frames bytes.Buffer
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"bedrock","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	} {
		_, _ = frames.Write(buildBedrockServiceChunkFrame(t, event))
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer = &bedrockPartialClientWriteErrorWriter{ResponseWriter: c.Writer}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body: &bedrockFramesThenError{
			frames: bytes.NewReader(frames.Bytes()),
			err:    context.Canceled,
		},
	}
	svc := &GatewayService{cfg: &config.Config{}}

	result, err := svc.handleBedrockStreamingResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformAnthropic}, time.Now(), "bedrock",
	)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.False(t, result.responseComplete)
	require.NoError(t, err)
	require.Equal(t, 3, result.usage.InputTokens)
}

func TestBedrockClientDisconnectCompleteAfterProviderTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var frames bytes.Buffer
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"bedrock","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		_, _ = frames.Write(buildBedrockServiceChunkFrame(t, event))
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer = &bedrockPartialClientWriteErrorWriter{ResponseWriter: c.Writer}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(frames.Bytes())),
	}
	svc := &GatewayService{cfg: &config.Config{}}

	result, err := svc.handleBedrockStreamingResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformAnthropic}, time.Now(), "bedrock",
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.True(t, result.responseComplete)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 1, result.usage.OutputTokens)
}

func TestBedrockClientDisconnectReturnsCollectedUsageOnIdleProviderStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var frames bytes.Buffer
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"bedrock","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	} {
		_, _ = frames.Write(buildBedrockServiceChunkFrame(t, event))
	}

	reader, writer := io.Pipe()
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = writer.Write(frames.Bytes())
	}()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer = &bedrockPartialClientWriteErrorWriter{ResponseWriter: c.Writer}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       reader,
	}
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}

	result, err := svc.handleBedrockStreamingResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformAnthropic}, time.Now(), "bedrock",
	)
	_ = writer.Close()
	<-writeDone
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.False(t, result.responseComplete)
	require.NoError(t, err)
	require.Equal(t, 3, result.usage.InputTokens)
}

func TestBedrockTreatsPartialFrameBytesAsProviderActivity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	firstFrame := buildBedrockServiceChunkFrame(t,
		`{"type":"message_start","message":{"id":"msg_slow","type":"message","role":"assistant","model":"bedrock","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
	)
	var remainingFrames bytes.Buffer
	for _, event := range []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"slow-ok"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		_, _ = remainingFrames.Write(buildBedrockServiceChunkFrame(t, event))
	}
	oneThird := len(firstFrame) / 3
	providerBody := &providerSlowChunksReader{
		chunks: [][]byte{
			append([]byte(nil), firstFrame[:oneThird]...),
			append([]byte(nil), firstFrame[oneThird:2*oneThird]...),
			append([]byte(nil), firstFrame[2*oneThird:]...),
			remainingFrames.Bytes(),
		},
		interval: 400 * time.Millisecond,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       providerBody,
	}
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}

	result, err := svc.handleBedrockStreamingResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformAnthropic}, time.Now(), "bedrock",
	)

	require.NoError(t, err, "provider bytes arriving inside the idle interval must keep a partial AWS frame alive")
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"text":"slow-ok"`)
}

//go:build unit

package service

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ccPingResponseWriter struct {
	*httptest.ResponseRecorder
	flushed           chan string
	failPing          bool
	failed            bool
	writesAfterError  int
	flushesAfterError int
}

func (writer *ccPingResponseWriter) Write(data []byte) (int, error) {
	if writer.failed {
		writer.writesAfterError++
	}
	if writer.failPing && string(data) == ": ping\n\n" {
		writer.failed = true
		return 0, errors.New("heartbeat write failed")
	}
	return writer.ResponseRecorder.Write(data)
}

func (writer *ccPingResponseWriter) Flush() {
	if writer.failed {
		writer.flushesAfterError++
	}
	writer.ResponseRecorder.Flush()
	if writer.flushed != nil {
		writer.flushed <- writer.Body.String()
	}
}

func TestHandleCCStreamingFromAnthropic_PingFlushesAfterSemanticOutput(t *testing.T) {
	writer := &ccPingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		flushed:          make(chan string, 32),
	}
	context, _ := gin.CreateTestContext(writer)
	reader, upstream := io.Pipe()
	type outcome struct {
		result *ForwardResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
			stdcontext.Background(),
			&http.Response{Body: reader}, context, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, false,
		)
		finished <- outcome{result: result, err: err}
	}()
	t.Cleanup(func() {
		_ = upstream.Close()
		_ = reader.Close()
	})

	awaitFlush := func() string {
		t.Helper()
		select {
		case body := <-writer.flushed:
			return body
		case <-time.After(5 * time.Second):
			t.Fatal("event did not flush before the next upstream event or EOF")
			return ""
		}
	}
	var body string
	send := func(eventType, payload string) {
		t.Helper()
		previous := body
		_, err := io.WriteString(upstream, "event: "+eventType+"\ndata: "+payload+"\n\n")
		require.NoError(t, err)
		for {
			body = awaitFlush()
			if body != previous {
				break
			}
		}
	}
	ping := func() {
		t.Helper()
		wantPings := strings.Count(body, ": ping\n\n") + 1
		_, err := io.WriteString(upstream, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		require.NoError(t, err)
		for strings.Count(body, ": ping\n\n") < wantPings {
			body = awaitFlush()
		}
		require.Equal(t, wantPings, strings.Count(body, ": ping\n\n"))
		require.True(t, strings.HasSuffix(body, ": ping\n\n"))
	}

	// Pre-output heartbeats and metadata retain the local failover boundary.
	for _, frame := range []string{
		"event: ping\ndata: {\"type\":\"ping\"}\n\n",
		"event: ping\ndata: {\"type\":\"ping\"}\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_ping\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":10}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
	} {
		_, err := io.WriteString(upstream, frame)
		require.NoError(t, err)
	}
	send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)
	require.Contains(t, body, `"content":"hello"`)
	ping()
	send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`)
	require.Contains(t, body, `"content":" world"`)
	_, err := io.WriteString(upstream, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	require.NoError(t, err)
	ping()
	require.NotContains(t, body, "[DONE]")
	require.NoError(t, upstream.Close())

	select {
	case got := <-finished:
		require.NoError(t, got.err)
		require.Equal(t, 10, got.result.Usage.InputTokens)
		require.Equal(t, 7, got.result.Usage.OutputTokens)
		require.NotNil(t, got.result.FirstTokenMs)
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish after EOF")
	}
	body = writer.Body.String()
	require.NotContains(t, body, "event:")
	require.NotContains(t, body, `"type":"ping"`)
	require.Equal(t, 4, strings.Count(body, ": ping\n\n"))
	require.Equal(t, 1, strings.Count(body, "data: [DONE]\n\n"))
	require.True(t, strings.HasSuffix(body, "data: [DONE]\n\n"))
	var content strings.Builder
	var usage *apicompat.ChatUsage
	var finishedChoice bool
	for _, frame := range strings.Split(body, "\n\n") {
		if frame == "" || frame == ": ping" || frame == "data: [DONE]" {
			continue
		}
		require.True(t, strings.HasPrefix(frame, "data: "))
		var chunk apicompat.ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &chunk))
		require.Equal(t, "chat.completion.chunk", chunk.Object)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				content.WriteString(*choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				require.Equal(t, "stop", *choice.FinishReason)
				finishedChoice = true
			}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	require.Equal(t, "hello world", content.String())
	require.True(t, finishedChoice)
	require.NotNil(t, usage)
	require.Equal(t, 10, usage.PromptTokens)
	require.Equal(t, 7, usage.CompletionTokens)
	require.Equal(t, 17, usage.TotalTokens)
}

func TestHandleCCStreamingFromAnthropic_PingDoesNotSetFirstToken(t *testing.T) {
	writer := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(writer)
	result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
		stdcontext.Background(),
		&http.Response{Body: io.NopCloser(strings.NewReader("event: ping\ndata: {\"type\":\"ping\"}\n\n"))},
		context, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, false,
	)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Nil(t, result)
	require.Empty(t, writer.Body.String(), "heartbeats must not commit an invalid stream")
}

func TestHandleCCStreamingFromAnthropic_PingWriteFailureStopsDownstream(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		name := "zero_input_usage"
		if withUsage {
			name = "with_input_usage"
		}
		t.Run(name, func(t *testing.T) {
			writer := &ccPingResponseWriter{ResponseRecorder: httptest.NewRecorder(), failPing: true}
			context, _ := gin.CreateTestContext(writer)
			input := "0"
			if withUsage {
				input = "10"
			}
			stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_failure\",\"usage\":{\"input_tokens\":" + input + "}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"hello\"}}\n\n"
			stream += "event: ping\ndata: {\"type\":\"ping\"}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
				stdcontext.Background(),
				&http.Response{Body: io.NopCloser(strings.NewReader(stream))},
				context, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true, false,
			)
			require.NoError(t, err)
			require.True(t, writer.failed)
			require.Zero(t, writer.writesAfterError)
			require.Zero(t, writer.flushesAfterError)
			require.NotContains(t, writer.Body.String(), "[DONE]")
			require.Zero(t, result.Usage.OutputTokens)
			if withUsage {
				require.Equal(t, 10, result.Usage.InputTokens)
			} else {
				require.Zero(t, result.Usage.InputTokens)
			}
		})
	}
}

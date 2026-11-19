package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// anthropicCompatStream tracks terminal events on native Anthropic conversions.
type anthropicCompatStream struct {
	maxLineSize        int
	clientDisconnected bool
	sawMessageStart    bool
	// sawTerminal recognizes stop_reason for protocol output. Capture tracks message_stop separately.
	sawTerminal bool
	readErr     error
}

// observe records provider events used to distinguish complete and partial responses.
func (st *anthropicCompatStream) observe(event *apicompat.AnthropicStreamEvent) {
	if event.Type == "message_start" {
		st.sawMessageStart = true
	}
	if event.Type == "message_stop" || event.Type == "message_delta" && event.Delta != nil && event.Delta.StopReason != "" {
		st.sawTerminal = true
	}
}

func (st *anthropicCompatStream) failure() *anthropicCompatStreamFailure {
	if st.clientDisconnected || st.sawTerminal {
		return nil
	}
	switch {
	case errors.Is(st.readErr, bufio.ErrTooLong):
		return &anthropicCompatStreamFailure{
			err:     fmt.Errorf("stream usage incomplete: %w", st.readErr),
			errType: "response_too_large",
			message: fmt.Sprintf("upstream SSE line exceeded %d bytes", st.maxLineSize),
		}
	case st.readErr != nil && !errors.Is(st.readErr, io.EOF):
		return &anthropicCompatStreamFailure{
			err:     fmt.Errorf("stream usage incomplete: %w", st.readErr),
			errType: "stream_read_error",
			message: "upstream stream disconnected: " + sanitizeStreamError(st.readErr),
		}
	case st.sawMessageStart:
		return &anthropicCompatStreamFailure{
			err:     errors.New("stream usage incomplete: missing terminal event"),
			errType: "stream_incomplete",
			message: "upstream stream ended without a terminal event",
		}
	default:
		return nil
	}
}

// anthropicCompatStreamFailure 描述客户端在线时上游流未正常结束的原因：
// err 返回给调用方，errType / message 写入下游的终止错误事件。
type anthropicCompatStreamFailure struct {
	err     error
	errType string
	message string
}

func nextAnthropicCompatProviderLine(reader *providerLineReader, ctx context.Context, disconnected *bool) (string, bool, error) {
	if *disconnected {
		return reader.Next()
	}
	line, ok, err := reader.NextContext(ctx)
	if ctx.Err() == nil {
		return line, ok, err
	}
	*disconnected = true
	if err == nil || !errors.Is(err, ctx.Err()) {
		return line, ok, err
	}
	// The client context only stops downstream delivery. The provider reader
	// retains its existing configured timeout and capture lifetime.
	return reader.Next()
}

func anthropicCompatProviderFailure(err error) *anthropicCompatStreamFailure {
	switch {
	case errors.Is(err, errProviderStreamIdleTimeout):
		return &anthropicCompatStreamFailure{err: err, errType: "stream_timeout", message: "upstream stream data interval timeout"}
	case errors.Is(err, bufio.ErrTooLong):
		return &anthropicCompatStreamFailure{err: err, errType: "response_too_large", message: "upstream SSE event exceeded the configured size limit"}
	default:
		return &anthropicCompatStreamFailure{err: err, errType: "stream_read_error", message: "upstream stream disconnected: " + sanitizeStreamError(err)}
	}
}

func anthropicCompatInBandFailure(payload string) *anthropicCompatStreamFailure {
	return &anthropicCompatStreamFailure{
		err:     &sseStreamErrorEventError{RawData: payload},
		errType: firstNonEmpty(gjson.Get(payload, "error.type").String(), "upstream_error"),
		message: firstNonEmpty(sanitizeUpstreamErrorMessage(gjson.Get(payload, "error.message").String()), "upstream stream error"),
	}
}

// writeAnthropicCompatCCStreamFailure 向 Chat Completions 流写出终止错误帧，
// 并标记响应已提交，handler 不再追加兜底错误。
func writeAnthropicCompatCCStreamFailure(c *gin.Context, failure *anthropicCompatStreamFailure) {
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]string{
			"type":    failure.errType,
			"message": failure.message,
		},
	})
	commitAnthropicCompatStreamFailure(c, failure, "data: "+string(payload)+"\n\n")
}

// writeAnthropicCompatResponsesStreamFailure 向 Responses 流写出 response.failed
// 终止事件，沿用流内的 response id；sequence_number 经 restorer 与已写出的事件
// 保持连续。写出后标记响应已提交。
func writeAnthropicCompatResponsesStreamFailure(c *gin.Context, state *apicompat.AnthropicEventToResponsesState, restorer *apicompat.ResponsesClientToolStreamRestorer, failure *anthropicCompatStreamFailure) {
	responseID := state.ResponseID
	if responseID == "" {
		responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	evt := apicompat.ResponsesStreamEvent{
		Type:           "response.failed",
		SequenceNumber: state.SequenceNumber,
		Response: &apicompat.ResponsesResponse{
			ID:        responseID,
			Object:    "response",
			CreatedAt: state.Created,
			Model:     state.Model,
			Status:    "failed",
			Output:    []apicompat.ResponsesOutput{},
			Error:     &apicompat.ResponsesError{Code: failure.errType, Message: failure.message},
		},
	}
	state.SequenceNumber++
	payload, err := json.Marshal(evt)
	if err != nil {
		return
	}
	payloads, _, err := restorer.RestoreEvent(payload)
	if err != nil {
		return
	}
	var frames strings.Builder
	for _, restored := range payloads {
		fmt.Fprintf(&frames, "event: %s\ndata: %s\n\n", gjson.GetBytes(restored, "type").String(), restored)
	}
	commitAnthropicCompatStreamFailure(c, failure, frames.String())
}

func commitAnthropicCompatStreamFailure(c *gin.Context, failure *anthropicCompatStreamFailure, frame string) {
	MarkOpsStreamError(c, failure.errType, failure.message, http.StatusBadGateway)
	MarkResponseCommitted(c)
	if _, err := io.WriteString(c.Writer, frame); err != nil {
		return
	}
	c.Writer.Flush()
}

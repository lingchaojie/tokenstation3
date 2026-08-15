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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const compactionMinMaxTokens = 32000

func (s *GatewayService) handleResponsesCompactionResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientStream bool,
	allowKiroMarkedFinalUsage bool,
) (*ForwardResult, error) {
	requestID := captureProviderRequestID(resp.Header)
	finalResp, usage, err := s.collectResponsesCompactionAnthropicSSE(resp, c, allowKiroMarkedFinalUsage)
	if err != nil {
		return nil, err
	}

	summary := anthropicResponseText(finalResp)
	if strings.TrimSpace(summary) == "" {
		return nil, s.failResponsesCompaction(c, clientStream, http.StatusBadGateway, "Upstream produced no compaction summary", requestID)
	}
	envelope := apicompat.EncodeCompactionEnvelope(summary)
	if envelope == "" {
		return nil, s.failResponsesCompaction(c, clientStream, http.StatusBadGateway, "Failed to encode compaction summary", requestID)
	}
	responseJSON, err := buildResponsesCompactionJSON(originalModel, summary, envelope, usage)
	if err != nil {
		return nil, s.failResponsesCompaction(c, clientStream, http.StatusBadGateway, "Failed to build compaction response", requestID)
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	if clientStream {
		payload, ok := buildOpenAICompactSSEPayload(responseJSON)
		if !ok {
			return nil, s.failResponsesCompaction(c, true, http.StatusBadGateway, "Failed to render compaction SSE", requestID)
		}
		writeResponsesCompactionSSEHeaders(c)
		_, _ = c.Writer.Write(payload)
		c.Writer.Flush()
	} else {
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseJSON)
	}

	return &ForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          clientStream,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *GatewayService) collectResponsesCompactionAnthropicSSE(
	resp *http.Response,
	c *gin.Context,
	allowKiroMarkedFinalUsage bool,
) (*apicompat.AnthropicResponse, ClaudeUsage, error) {
	requestID := captureProviderRequestID(resp.Header)
	lineReader := newProviderLineReader(resp, s.cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, s.cfg)
	})
	defer lineReader.Close()

	var finalResp *apicompat.AnthropicResponse
	var contentAccumulator anthropicBufferedContentAccumulator
	var usage ClaudeUsage
	terminalObserved := false
	providerPhase := anthropicProviderAwaitingStart
	incompleteProviderTail := false
	var scanErr error

	for {
		line, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			break
		}
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			if payload, dataLine := parseAnthropicSSEField(line, "data"); dataLine && strings.TrimSpace(payload) != "" {
				incompleteProviderTail = true
				break
			}
			continue
		}

		dataLine, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			incompleteProviderTail = true
			break
		}
		payload, ok := parseAnthropicSSEField(dataLine, "data")
		if !ok {
			incompleteProviderTail = true
			break
		}
		if _, err := validateAnthropicProviderJSONEvent(&providerPhase, eventType, []byte(payload)); err != nil {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, usage, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(err))
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, usage, newIncompleteProviderStreamFailover(resp, fmt.Sprintf("invalid JSON for Anthropic event %q", eventType))
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil && validAnthropicMessageStartPayload([]byte(payload)) {
				finalResp = event.Message
				mergeAnthropicUsageFromPayload(&usage, event.Message.Usage, payload, allowKiroMarkedFinalUsage)
			}
		case "message_delta":
			if event.Usage != nil {
				mergeAnthropicUsageFromPayload(&usage, *event.Usage, payload, allowKiroMarkedFinalUsage)
			}
			mergeKiroCreditsFromAnthropicPayload(&usage, payload)
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		case "content_block_start":
			if event.ContentBlock != nil && finalResp != nil {
				contentAccumulator.start(finalResp, *event.ContentBlock)
			}
		case "content_block_delta":
			if event.Delta != nil && finalResp != nil && event.Index != nil {
				contentAccumulator.delta(*event.Index, event.Delta)
			}
		case "message_stop":
			terminalObserved = true
		}
	}

	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_responses compaction: read error", zap.Error(scanErr), zap.String("request_id", requestID))
		}
		return nil, usage, newIncompleteProviderStreamFailover(resp, "upstream stream read failed before message_stop: "+sanitizeStreamError(scanErr))
	}
	if incompleteProviderTail {
		lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
		return nil, usage, newIncompleteProviderStreamFailover(resp, "upstream stream ended with an incomplete Anthropic provider event")
	}
	if !terminalObserved {
		return nil, usage, newIncompleteProviderStreamFailover(resp, "upstream stream ended before message_stop")
	}
	if finalResp == nil {
		return nil, usage, newIncompleteProviderStreamFailover(resp, "upstream stream ended without a message_start response")
	}
	contentAccumulator.materialize(finalResp)
	return finalResp, usage, nil
}

func (s *GatewayService) failResponsesCompaction(c *gin.Context, clientStream bool, statusCode int, message, requestID string) error {
	logger.L().Warn("forward_as_responses compaction: failed", zap.String("request_id", requestID), zap.String("message", message))
	if clientStream {
		writeResponsesCompactionSSEHeaders(c)
		writeOpenAICompactSSEFailureMessage(c, statusCode, "upstream_error", message)
	} else {
		writeResponsesError(c, statusCode, "server_error", message)
	}
	return fmt.Errorf("compaction failed: %s", message)
}

func writeResponsesCompactionSSEHeaders(c *gin.Context) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
}

func buildResponsesCompactionJSON(model, summary, envelope string, usage ClaudeUsage) ([]byte, error) {
	item := map[string]any{
		"id":                "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":              apicompat.CompactionItemType,
		"status":            "completed",
		"encrypted_content": envelope,
		"summary":           []any{map[string]any{"type": "summary_text", "text": summary}},
	}
	inputTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	payload := map[string]any{
		"id": "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "object": "response", "model": model, "status": "completed",
		"output": []any{item},
		"usage":  map[string]any{"input_tokens": inputTokens, "output_tokens": usage.OutputTokens, "total_tokens": inputTokens + usage.OutputTokens},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal compaction response: %w", err)
	}
	return encoded, nil
}

func anthropicResponseText(resp *apicompat.AnthropicResponse) string {
	if resp == nil {
		return ""
	}
	parts := make([]string, 0, len(resp.Content))
	for _, block := range resp.Content {
		if block.Type == "text" {
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
)

const kiroMaxWebSearchIterations = 5

var (
	errKiroWebSearchFallback = errors.New("kiro web search fallback")
	kiroWebSearchDescCache   sync.Map
)

type kiroWebSearchExecution struct {
	ResponseBody []byte
	Usage        ClaudeUsage
	RequestID    string
}

type kiroStreamChunkCollector struct {
	buffer    []byte
	chunkEnds []int
	maxBytes  int64
}

func (w *kiroStreamChunkCollector) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.maxBytes > 0 && int64(len(p)) > w.maxBytes-int64(len(w.buffer)) {
		return 0, fmt.Errorf("%w: translated KIRO stream limit=%d", ErrUpstreamResponseBodyTooLarge, w.maxBytes)
	}
	w.buffer = append(w.buffer, p...)
	w.chunkEnds = append(w.chunkEnds, len(w.buffer))
	return len(p), nil
}

func (w *kiroStreamChunkCollector) Chunks() [][]byte {
	if len(w.chunkEnds) == 0 {
		return nil
	}
	chunks := make([][]byte, 0, len(w.chunkEnds))
	start := 0
	for _, end := range w.chunkEnds {
		chunks = append(chunks, w.buffer[start:end])
		start = end
	}
	return chunks
}

func bufferKiroAnthropicStream(ctx context.Context, body io.Reader, responseModel string, inputTokens int, maxBytes int64) ([][]byte, *kiropkg.StreamResult, error) {
	collector := &kiroStreamChunkCollector{maxBytes: maxBytes}
	result, err := kiropkg.StreamEventStreamAsAnthropicWithContext(ctx, body, collector, responseModel, inputTokens, kiropkg.KiroRequestContext{})
	if err != nil {
		return nil, nil, err
	}
	return collector.Chunks(), result, nil
}

func writeSSEChunks(w io.Writer, chunks [][]byte) error {
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

func writeAnthropicMessageStart(w io.Writer, msgID, model string, inputTokens int, cacheUsage *kiroCacheEmulationUsage) error {
	if strings.TrimSpace(msgID) == "" {
		msgID = "msg_" + kiropkg.GenerateToolUseID()
	}
	if strings.TrimSpace(model) == "" {
		model = "kiro"
	}
	usage := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": 0,
	}
	if cacheUsage != nil {
		usage["input_tokens"] = cacheUsage.InputTokens
		usage["cache_creation_input_tokens"] = cacheUsage.CacheCreationInputTokens
		usage["cache_read_input_tokens"] = cacheUsage.CacheReadInputTokens
	}
	payload, err := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usage,
		},
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "event: message_start\ndata: "+string(payload)+"\n\n")
	return err
}

func (s *GatewayService) streamKiroWebSearchAsAnthropic(
	ctx context.Context, c *gin.Context, account *Account, anthropicBody []byte, mappedModel, requestModel, token string, inputTokens int, headers http.Header, w io.Writer, cachePlan *kiroCacheEmulationPlan,
) error {
	query := kiropkg.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return errKiroWebSearchFallback
	}

	currentBody, err := kiropkg.ReplaceWebSearchToolDescription(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}
	currentToolUseID := "srvtoolu_" + kiropkg.GenerateToolUseID()
	nextContentBlockIndex := 0

	if err := writeAnthropicMessageStart(w, "", requestModel, inputTokens, cachePlan.result()); err != nil {
		return err
	}

	for iteration := 0; iteration < kiroMaxWebSearchIterations; iteration++ {
		s.prefetchKiroWebSearchDescription(ctx, account, token)

		results, nextToken, mcpErr := s.callKiroWebSearchMCP(ctx, account, token, query)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			results = nil
		}

		if err := writeSSEChunks(w, kiropkg.GenerateSearchIndicatorEvents(query, currentToolUseID, results, nextContentBlockIndex)); err != nil {
			return err
		}
		nextContentBlockIndex += 2

		currentBody, err = kiropkg.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return errKiroWebSearchFallback
		}

		resp, _, err := s.executeKiroUpstream(ctx, account, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			responseBody, truncated, readErr := s.readKiroUpstreamErrorBody(ctx, resp)
			_ = resp.Body.Close()
			failureErr := s.handleKiroHTTPErrorBody(ctx, resp, c, account, mappedModel, currentBody, responseBody, false)
			var failure *UpstreamFailoverError
			if !errors.As(failureErr, &failure) {
				return failureErr
			}
			if readErr != nil {
				failure.ClientMessage = sanitizeUpstreamErrorMessage(readErr.Error())
			}
			if ownsWebChatFinalGatewayErrorCapture(ctx) {
				if truncated {
					markCaptureResultTruncated(c)
				}
				s.submitWebChatFinalGatewayErrorCapture(
					ctx, c, account, requestModel, mappedModel, "/v1/messages", true, resp, responseBody, readErr == nil && !truncated,
				)
				publishWebChatStreamTerminalError(ctx, failure)
			}
			return failure
		}
		if iteration == 0 {
			cachePlan.commit()
		}
		captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
			account != nil && CaptureMayApplyFor(c, string(account.Platform))
		captureLimit := 0
		if s.cfg != nil {
			captureLimit = s.cfg.Gateway.Capture.MaxBodyBytes
		}
		finishRawCapture := beginCaptureResponse(c, resp, captureEnabled, captureLimit)

		chunks, streamResult, streamErr := func() ([][]byte, *kiropkg.StreamResult, error) {
			defer func() { _ = resp.Body.Close() }()
			providerBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
			if err != nil {
				return nil, nil, err
			}
			return bufferKiroAnthropicStream(
				ctx, bytes.NewReader(providerBody), requestModel, inputTokens, resolveUpstreamResponseReadLimit(s.cfg),
			)
		}()
		finishRawCapture()
		if streamErr != nil {
			return streamErr
		}

		analysis := kiropkg.AnalyzeBufferedStream(chunks)
		if analysis.HasWebSearchToolUse && strings.TrimSpace(analysis.WebSearchQuery) != "" && iteration+1 < kiroMaxWebSearchIterations {
			filtered := kiropkg.FilterChunksForClient(chunks, analysis.WebSearchToolUseIndex, nextContentBlockIndex)
			if err := writeSSEChunks(w, filtered); err != nil {
				return err
			}
			if maxIndex := kiropkg.MaxContentBlockIndex(filtered); maxIndex >= nextContentBlockIndex {
				nextContentBlockIndex = maxIndex + 1
			}
			query = analysis.WebSearchQuery
			if strings.TrimSpace(analysis.WebSearchToolUseID) == "" {
				currentToolUseID = "srvtoolu_" + kiropkg.GenerateToolUseID()
			} else {
				currentToolUseID = analysis.WebSearchToolUseID
			}
			continue
		}

		for _, chunk := range chunks {
			adjusted, shouldForward := kiropkg.AdjustSSEChunk(chunk, nextContentBlockIndex)
			if !shouldForward {
				continue
			}
			if _, err := w.Write(adjusted); err != nil {
				return err
			}
		}
		usage := streamResult.Usage
		usagePayload := map[string]any{
			"input_tokens":                usage.InputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_read_input_tokens":     usage.CacheReadInputTokens,
			"cache_creation_input_tokens": usage.CacheCreationInputTokens,
			"_sub2api_kiro_final_usage":   true,
		}
		if usage.KiroCredits > 0 {
			usagePayload["_sub2api_kiro_credits"] = usage.KiroCredits
		}
		stopReason := strings.TrimSpace(streamResult.StopReason)
		if stopReason == "" || stopReason == "tool_use" {
			stopReason = "end_turn"
		}
		finalDelta, _ := json.Marshal(map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
			"usage": usagePayload,
		})
		if _, err := fmt.Fprintf(w, "event: message_delta\ndata: %s\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", finalDelta); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) executeKiroWebSearch(ctx context.Context, c *gin.Context, account *Account, group *Group, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*kiroWebSearchExecution, error) {
	query := kiropkg.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return nil, errKiroWebSearchFallback
	}

	currentBody, err := kiropkg.ReplaceWebSearchToolDescription(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}

	inputTokens := estimateKiroInputTokensForRequest(ctx, anthropicBody, mappedModel, requestModel, headers)
	currentToolUseID := "srvtoolu_" + kiropkg.GenerateToolUseID()
	searches := make([]kiropkg.SearchIndicator, 0, 2)
	requestID := ""
	cachePlan := s.prepareKiroCacheEmulationUsage(ctx, account, group, anthropicBody, mappedModel, inputTokens)
	cacheUsage := cachePlan.result()
	cacheCommitted := false

	for iteration := 0; iteration < kiroMaxWebSearchIterations; iteration++ {
		s.prefetchKiroWebSearchDescription(ctx, account, token)

		results, nextToken, mcpErr := s.callKiroWebSearchMCP(ctx, account, token, query)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			results = nil
		}
		searches = append(searches, kiropkg.SearchIndicator{
			ToolUseID: currentToolUseID,
			Query:     query,
			Results:   results,
		})

		currentBody, err = kiropkg.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return nil, errKiroWebSearchFallback
		}

		resp, _, err := s.executeKiroUpstream(ctx, account, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			responseBody, _, readErr := s.readKiroUpstreamErrorBody(ctx, resp)
			_ = resp.Body.Close()
			failureErr := s.handleKiroHTTPErrorBody(ctx, resp, c, account, mappedModel, currentBody, responseBody, true)
			var failure *UpstreamFailoverError
			if !errors.As(failureErr, &failure) {
				return nil, failureErr
			}
			if readErr != nil {
				failure.ClientMessage = sanitizeUpstreamErrorMessage(readErr.Error())
			}
			return nil, failure
		}
		if !cacheCommitted {
			cachePlan.commit()
			cacheCommitted = true
		}
		captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
			account != nil && CaptureMayApplyFor(c, string(account.Platform))
		captureLimit := 0
		if s.cfg != nil {
			captureLimit = s.cfg.Gateway.Capture.MaxBodyBytes
		}
		finishRawCapture := beginCaptureResponse(c, resp, captureEnabled, captureLimit)

		parseResult, parseErr := func() (*kiropkg.ParseResult, error) {
			defer func() { _ = resp.Body.Close() }()
			providerBody, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
			if readErr != nil {
				return nil, readErr
			}
			return kiropkg.ParseNonStreamingEventStreamWithContext(bytes.NewReader(providerBody), requestModel, kiropkg.KiroRequestContext{CacheEmulationUsage: cacheUsage.toKiroUsage()})
		}()
		finishRawCapture()
		if parseErr != nil {
			return nil, newInvalidProviderResponseFailover(resp, "failed to parse KIRO WebSearch provider event stream: "+sanitizeStreamError(parseErr))
		}
		if requestID == "" {
			requestID = buildKiroRequestID(resp)
		}

		nextToolUseID, nextQuery, hasNext := kiropkg.ExtractWebSearchToolUseFromResponse(parseResult.ResponseBody)
		if !hasNext || strings.TrimSpace(nextQuery) == "" || iteration+1 >= kiroMaxWebSearchIterations {
			finalBody, injectErr := kiropkg.InjectSearchIndicatorsInResponse(parseResult.ResponseBody, searches)
			if injectErr == nil {
				parseResult.ResponseBody = finalBody
			}
			return &kiroWebSearchExecution{
				ResponseBody: parseResult.ResponseBody,
				Usage:        kiroUsageToClaude(parseResult.Usage, inputTokens),
				RequestID:    requestID,
			}, nil
		}

		query = nextQuery
		if strings.TrimSpace(nextToolUseID) == "" {
			nextToolUseID = "srvtoolu_" + kiropkg.GenerateToolUseID()
		}
		currentToolUseID = nextToolUseID
	}

	return nil, fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) prefetchKiroWebSearchDescription(ctx context.Context, account *Account, token string) {
	endpoint := kiropkg.BuildMcpEndpoint(kiroAPIRegion(account))
	if cached, ok := kiroWebSearchDescCache.Load(endpoint); ok {
		if desc, ok := cached.(string); ok && strings.TrimSpace(desc) != "" {
			kiropkg.SetCachedWebSearchDescription(desc)
		}
		return
	}

	reqBody, _ := json.Marshal(kiropkg.MCPRequest{
		ID:      "tools_list",
		JSONRPC: "2.0",
		Method:  "tools/list",
	})
	resp, _, err := s.doKiroMCPJSONRequest(ctx, account, endpoint, reqBody, token)
	if err != nil || resp == nil {
		return
	}
	defer abortKiroNativeCaptureAttempt(ctx)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}

	body, err := readUpstreamResponseBodyLimited(resp.Body, resolveUpstreamResponseReadLimit(s.cfg))
	if err != nil {
		return
	}
	var result kiropkg.MCPResponse
	if err := json.Unmarshal(body, &result); err != nil || result.Result == nil {
		return
	}
	for _, tool := range result.Result.Tools {
		if strings.EqualFold(tool.Name, "web_search") && strings.TrimSpace(tool.Description) != "" {
			kiroWebSearchDescCache.Store(endpoint, tool.Description)
			kiropkg.SetCachedWebSearchDescription(tool.Description)
			return
		}
	}
}

func (s *GatewayService) callKiroWebSearchMCP(ctx context.Context, account *Account, token, query string) (*kiropkg.WebSearchResults, string, error) {
	reqBody, err := json.Marshal(buildKiroWebSearchMCPRequest(query))
	if err != nil {
		return nil, token, err
	}

	endpoint := kiropkg.BuildMcpEndpoint(kiroAPIRegion(account))
	resp, nextToken, err := s.doKiroMCPJSONRequest(ctx, account, endpoint, reqBody, token)
	if err != nil {
		return nil, nextToken, err
	}
	if resp == nil {
		return nil, nextToken, fmt.Errorf("kiro web search returned nil response")
	}
	defer abortKiroNativeCaptureAttempt(ctx)
	defer func() { _ = resp.Body.Close() }()

	body, err := readUpstreamResponseBodyLimited(resp.Body, resolveUpstreamResponseReadLimit(s.cfg))
	if err != nil {
		return nil, nextToken, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nextToken, fmt.Errorf("kiro mcp status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed kiropkg.MCPResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nextToken, err
	}
	if parsed.Error != nil {
		msg := "unknown error"
		if parsed.Error.Message != nil && strings.TrimSpace(*parsed.Error.Message) != "" {
			msg = strings.TrimSpace(*parsed.Error.Message)
		}
		code := 0
		if parsed.Error.Code != nil {
			code = *parsed.Error.Code
		}
		return nil, nextToken, fmt.Errorf("kiro mcp error %d: %s", code, msg)
	}

	return kiropkg.ParseSearchResults(&parsed), nextToken, nil
}

func buildKiroWebSearchMCPRequest(query string) kiropkg.MCPRequest {
	return kiropkg.MCPRequest{
		ID:      fmt.Sprintf("web_search_%s", kiropkg.GenerateToolUseID()),
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"query": query,
				"_meta": map[string]any{
					"_isValid":        true,
					"_activePath":     []string{"query"},
					"_completedPaths": [][]string{{"query"}},
				},
			},
		},
	}
}

func (s *GatewayService) doKiroMCPJSONRequest(ctx context.Context, account *Account, endpoint string, payload []byte, token string) (*http.Response, string, error) {
	currentToken := token
	machineID := ensureKiroMachineIDPersisted(ctx, s.accountRepo, account)
	accountKey := buildKiroAccountKey(account)
	proxyURL := kiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)

	for attempt := 0; attempt < 3; attempt++ {
		if err := s.checkAndWaitKiroCooldown(ctx, accountKey); err != nil {
			if failoverErr := asKiroCooldownFailoverError(err); failoverErr != nil {
				return nil, currentToken, failoverErr
			}
			return nil, currentToken, err
		}

		req, err := newKiroJSONRequest(ctx, endpoint, payload, currentToken, accountKey, machineID, "", account)
		if err != nil {
			return nil, currentToken, err
		}

		if s.capturePool != nil {
			s.beginKiroNativeCaptureAttempt(ctx, account, req, payload)
		} else {
			setCaptureUpstreamRequestFromContext(ctx, req)
		}
		resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
		if s.capturePool != nil {
			s.beginKiroNativeCaptureResponse(ctx, resp)
		} else {
			setCaptureUpstreamResponseFromContext(ctx, resp)
		}
		if err != nil {
			abortKiroNativeCaptureAttempt(ctx)
			return nil, currentToken, err
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			respBody, readErr := readUpstreamResponseBodyLimited(resp.Body, resolveUpstreamResponseReadLimit(s.cfg))
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, currentToken, readErr
			}
			if resp.StatusCode == http.StatusForbidden && isKiroSuspendedBody(respBody) {
				if _, err := s.markKiroSuspended(ctx, accountKey); err != nil {
					return nil, currentToken, err
				}
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if resp.StatusCode == http.StatusForbidden && !isKiroTokenErrorBody(respBody) {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if s.kiroTokenProvider == nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			refreshedToken, refreshErr := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account)
			if refreshErr != nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			currentToken = refreshedToken
			machineID = ensureKiroMachineIDPersisted(ctx, s.accountRepo, account)
			accountKey = buildKiroAccountKey(account)
			abortKiroNativeCaptureAttempt(ctx)
			if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
				return nil, currentToken, sleepErr
			}
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if _, err := s.markKiro429(ctx, account.ID, accountKey); err != nil {
				abortKiroNativeCaptureAttempt(ctx)
				_ = resp.Body.Close()
				return nil, currentToken, err
			}
		}
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
			if attempt < 2 {
				abortKiroNativeCaptureAttempt(ctx)
				_ = resp.Body.Close()
				if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
					return nil, currentToken, sleepErr
				}
				continue
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := s.markKiroSuccess(ctx, account.ID, accountKey); err != nil {
				abortKiroNativeCaptureAttempt(ctx)
				_ = resp.Body.Close()
				return nil, currentToken, err
			}
		}

		return resp, currentToken, nil
	}

	return nil, currentToken, fmt.Errorf("kiro mcp request retries exhausted")
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// Web search emulation constants
const (
	toolTypeWebSearchPrefix = "web_search"
	toolTypeGoogleSearch    = "google_search"
	toolNameWebSearch       = "web_search"
	toolNameGoogleSearch    = "google_search"
	toolNameWebSearch2025   = "web_search_20250305"

	webSearchDefaultMaxResults = 5
	defaultWebSearchModel      = "claude-sonnet-4-6"
	webSearchMsgIDPrefix       = "msg_ws_"
	webSearchToolUseIDPrefix   = "srvtoolu_ws_"
	tokenEstimateDivisor       = 4

	// featureKeyWebSearchEmulation is the key used in Account.Extra and Channel.FeaturesConfig.
	featureKeyWebSearchEmulation = "web_search_emulation"
)

type webSearchManager interface {
	SearchWithBestProvider(context.Context, websearch.SearchRequest) (*websearch.SearchResponse, string, error)
	TestSearch(context.Context, websearch.SearchRequest) (*websearch.SearchResponse, string, error)
	GetUsage(context.Context, string) (int64, error)
	ResetUsage(context.Context, string) error
}

type activeWebSearchManager struct {
	manager webSearchManager
}

var webSearchManagerPtr atomic.Pointer[activeWebSearchManager]

// SetWebSearchManager wires the websearch.Manager into the gateway (goroutine-safe).
func SetWebSearchManager(m *websearch.Manager) {
	if m == nil {
		webSearchManagerPtr.Store(nil)
		return
	}
	webSearchManagerPtr.Store(&activeWebSearchManager{manager: m})
}

func getWebSearchManager() webSearchManager {
	if current := webSearchManagerPtr.Load(); current != nil {
		return current.manager
	}
	return nil
}

// shouldEmulateWebSearch checks whether a request should be intercepted.
//
// Judgment chain: manager exists → only web_search tool → global enabled → platform-specific policy.
// Anthropic API Key keeps the existing account-level override:
// "enabled" (force on), "disabled" (force off), "default" (follow channel).
// Kiro OAuth/API Key direct mode uses channel-level switch only.
func (s *GatewayService) shouldEmulateWebSearch(ctx context.Context, account *Account, groupID *int64, body []byte) bool {
	return shouldEmulateWebSearchFor(ctx, s.settingService, s.channelService, account, groupID, body)
}

type messagesSearchPlanKey struct{}

// IsOpenAIMessagesSearchCandidate reports whether delivery can use the local
// search branch. The selected account and channel determine the final mode.
func IsOpenAIMessagesSearchCandidate(body []byte) bool {
	return isOnlyWebSearchToolInBody(body)
}

// PlanOpenAIMessagesSearch pins the delivery decision for this attempt so
// reserving one search and forwarding the request cannot diverge.
func (s *OpenAIGatewayService) PlanOpenAIMessagesSearch(ctx context.Context, account *Account, groupID *int64, body []byte) (context.Context, bool) {
	emulated := shouldEmulateWebSearchFor(ctx, s.settingService, s.channelService, account, groupID, body)
	return context.WithValue(ctx, messagesSearchPlanKey{}, emulated), emulated
}

// shouldEmulateWebSearchFor is shared by the Anthropic gateway and the OpenAI
// gateway's /v1/messages path.
func shouldEmulateWebSearchFor(
	ctx context.Context, settingService *SettingService, channelService *ChannelService,
	account *Account, groupID *int64, body []byte,
) bool {
	if getWebSearchManager() == nil || account == nil || settingService == nil {
		return false
	}
	// OpenAI OAuth/setup-token accounts retain native search regardless of
	// channel configuration. Only API key accounts opt into emulation.
	if account.Platform == PlatformOpenAI && account.Type != AccountTypeAPIKey {
		return false
	}
	if !isOnlyWebSearchToolInBody(body) {
		return false
	}
	if !settingService.IsWebSearchEmulationEnabled(ctx) {
		return false
	}

	if account.Platform == PlatformKiro {
		return isKiroDirectModeAccount(account) && isChannelWebSearchEmulationEnabled(ctx, channelService, groupID, account.Platform)
	}
	switch account.GetWebSearchEmulationMode() {
	case WebSearchModeEnabled:
		return true
	case WebSearchModeDisabled:
		return false
	default:
		return isChannelWebSearchEmulationEnabled(ctx, channelService, groupID, account.Platform)
	}
}

func isChannelWebSearchEmulationEnabled(ctx context.Context, channelService *ChannelService, groupID *int64, platform string) bool {
	if groupID == nil || channelService == nil {
		return false
	}
	ch, err := channelService.GetChannelForGroup(ctx, *groupID)
	return err == nil && ch != nil && ch.IsWebSearchEmulationEnabled(platform)
}

// isOnlyWebSearchToolInBody checks if the body contains exactly one web_search tool.
func isOnlyWebSearchToolInBody(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	if !tools.Get("0").Exists() || tools.Get("1").Exists() {
		return false
	}
	return isWebSearchToolJSON(tools.Get("0"))
}

func isWebSearchToolJSON(tool gjson.Result) bool {
	toolType := tool.Get("type").String()
	if strings.HasPrefix(toolType, toolTypeWebSearchPrefix) || toolType == toolTypeGoogleSearch {
		return true
	}
	switch tool.Get("name").String() {
	case toolNameWebSearch, toolNameGoogleSearch, toolNameWebSearch2025:
		return true
	}
	return false
}

// extractSearchQueryFromBody extracts the last user message text as the search query.
func extractSearchQueryFromBody(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	var lastMsg gjson.Result
	found := false
	messages.ForEach(func(_, msg gjson.Result) bool {
		lastMsg = msg
		found = true
		return true
	})
	if !found || lastMsg.Get("role").String() != "user" {
		return ""
	}
	return extractWebSearchTextFromContent(lastMsg.Get("content"))
}

func extractWebSearchTextFromContent(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		var result string
		content.ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").String() == "text" {
				if text := block.Get("text").String(); text != "" {
					result = text
					return false
				}
			}
			return true
		})
		return result
	}
	return ""
}

// handleWebSearchEmulation intercepts a web-search-only request,
// calls a third-party search API, and constructs an Anthropic-format response.
func (s *GatewayService) handleWebSearchEmulation(
	ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest,
) (*ForwardResult, error) {
	// Release the serial queue lock immediately — we don't need upstream.
	if parsed.OnUpstreamAccepted != nil {
		parsed.OnUpstreamAccepted()
	}
	body := parsed.Body.Bytes()
	return writeEmulatedWebSearch(ctx, c, account, body, parsed.Model, parsed.Stream, func(model string) (int, *kiroCacheEmulationUsage) {
		var requestHeaders http.Header
		if c.Request != nil {
			requestHeaders = c.Request.Header
		}
		inputTokens := estimateKiroInputTokensForRequest(ctx, body, account.GetMappedModel(model), model, requestHeaders)
		return inputTokens, s.buildKiroCacheEmulationUsage(ctx, account, parsed.Group, body, model, inputTokens)
	})
}

// writeEmulatedWebSearch runs the search for the last user message and writes
// an Anthropic Messages response (SSE when stream is true).
func writeEmulatedWebSearch(
	ctx context.Context, c *gin.Context, account *Account, body []byte, model string, stream bool,
	usage func(model string) (int, *kiroCacheEmulationUsage),
) (*ForwardResult, error) {
	startTime := time.Now()

	query := extractSearchQueryFromBody(body)
	if query == "" {
		return nil, fmt.Errorf("web search emulation: no query found in messages")
	}

	slog.Info("web search emulation: executing search",
		"account_id", account.ID, "account_name", account.Name, "query", query)

	resp, providerName, err := doWebSearch(ctx, account, query)
	if err != nil {
		// Proxy unavailable → trigger account switch via UpstreamFailoverError
		if errors.Is(err, websearch.ErrProxyUnavailable) {
			return nil, &UpstreamFailoverError{
				StatusCode:   http.StatusBadGateway,
				ResponseBody: []byte(err.Error()),
			}
		}
		return nil, err
	}

	slog.Info("web search emulation: search completed",
		"provider", providerName, "results_count", len(resp.Results))

	if model == "" {
		model = defaultWebSearchModel
	}
	var inputTokens int
	var cacheUsage *kiroCacheEmulationUsage
	if usage != nil {
		inputTokens, cacheUsage = usage(model)
	}
	if stream {
		return writeWebSearchStreamResponse(c, query, resp, model, startTime, inputTokens, cacheUsage)
	}
	return writeWebSearchNonStreamResponse(c, query, resp, model, startTime, inputTokens, cacheUsage)
}

func doWebSearch(ctx context.Context, account *Account, query string) (*websearch.SearchResponse, string, error) {
	proxyURL := resolveAccountProxyURL(account)
	mgr := getWebSearchManager()
	if mgr == nil {
		return nil, "", fmt.Errorf("web search emulation: manager not initialized")
	}
	resp, providerName, err := mgr.SearchWithBestProvider(ctx, websearch.SearchRequest{
		Query: query, MaxResults: webSearchDefaultMaxResults, ProxyURL: proxyURL,
	})
	if err != nil {
		slog.Error("web search emulation: search failed", "error", err)
		return nil, "", fmt.Errorf("web search emulation: %w", err)
	}
	return resp, providerName, nil
}

func resolveAccountProxyURL(account *Account) string {
	if account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

// --- SSE streaming response ---

func writeWebSearchStreamResponse(
	c *gin.Context, query string, resp *websearch.SearchResponse, model string, startTime time.Time, inputTokens int, cacheUsage *kiroCacheEmulationUsage,
) (*ForwardResult, error) {
	msgID := webSearchMsgIDPrefix + uuid.New().String()
	toolUseID := webSearchToolUseIDPrefix + uuid.New().String()[:16]
	textSummary := buildTextSummary(query, resp.Results)
	usage := buildWebSearchClaudeUsage(inputTokens, len(textSummary)/tokenEstimateDivisor, cacheUsage)

	setSSEHeaders(c)
	w := c.Writer
	for _, fn := range []func() error{
		func() error { return writeSSEMessageStart(w, msgID, model, usage) },
		func() error { return writeSSEServerToolUse(w, toolUseID, query, 0) },
		func() error { return writeSSEToolResult(w, toolUseID, resp.Results, 1) },
		func() error { return writeSSETextBlock(w, textSummary, 2) },
		func() error { return writeSSEMessageEnd(w, usage.OutputTokens) },
	} {
		if err := fn(); err != nil {
			slog.Warn("web search emulation: SSE write failed, stopping", "error", err)
			break
		}
	}
	w.Flush()

	return &ForwardResult{Model: model, Duration: time.Since(startTime), Usage: usage}, nil
}

func setSSEHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
}

func writeSSEMessageStart(w http.ResponseWriter, msgID, model string, usage ClaudeUsage) error {
	evt := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":                usage.InputTokens,
				"output_tokens":               0,
				"cache_creation_input_tokens": usage.CacheCreationInputTokens,
				"cache_read_input_tokens":     usage.CacheReadInputTokens,
			},
		},
	}
	return flushSSEJSON(w, "message_start", evt)
}

func writeSSEServerToolUse(w http.ResponseWriter, toolUseID, query string, index int) error {
	start := map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{
			"type": "server_tool_use", "id": toolUseID,
			"name": toolNameWebSearch, "input": map[string]any{},
		},
	}
	if err := flushSSEJSON(w, "content_block_start", start); err != nil {
		return err
	}
	inputJSON, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return fmt.Errorf("marshal query: %w", err)
	}
	if err := flushSSEJSON(w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSEToolResult(w http.ResponseWriter, toolUseID string, results []websearch.SearchResult, index int) error {
	start := map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{
			"type": "web_search_tool_result", "tool_use_id": toolUseID,
			"content": buildSearchResultBlocks(results),
		},
	}
	if err := flushSSEJSON(w, "content_block_start", start); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSETextBlock(w http.ResponseWriter, text string, index int) error {
	if err := flushSSEJSON(w, "content_block_start", map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	if err := flushSSEJSON(w, "content_block_delta", map[string]any{
		"type": "content_block_delta", "index": index,
		"delta": map[string]string{"type": "text_delta", "text": text},
	}); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSEMessageEnd(w http.ResponseWriter, outputTokens int) error {
	if err := flushSSEJSON(w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": outputTokens},
	}); err != nil {
		return err
	}
	return flushSSEJSON(w, "message_stop", map[string]string{"type": "message_stop"})
}

// flushSSEJSON marshals data to JSON and writes an SSE event.
func flushSSEJSON(w http.ResponseWriter, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// --- Non-streaming JSON response ---

func writeWebSearchNonStreamResponse(
	c *gin.Context, query string, resp *websearch.SearchResponse, model string, startTime time.Time, inputTokens int, cacheUsage *kiroCacheEmulationUsage,
) (*ForwardResult, error) {
	msgID := webSearchMsgIDPrefix + uuid.New().String()
	toolUseID := webSearchToolUseIDPrefix + uuid.New().String()[:16]
	textSummary := buildTextSummary(query, resp.Results)
	usage := buildWebSearchClaudeUsage(inputTokens, len(textSummary)/tokenEstimateDivisor, cacheUsage)
	usageMap := map[string]int{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens}
	if cacheUsage != nil {
		usageMap["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
		usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}

	msg := map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": model,
		"content": []any{
			map[string]any{
				"type": "server_tool_use", "id": toolUseID,
				"name": toolNameWebSearch, "input": map[string]string{"query": query},
			},
			map[string]any{
				"type": "web_search_tool_result", "tool_use_id": toolUseID,
				"content": buildSearchResultBlocks(resp.Results),
			},
			map[string]any{"type": "text", "text": textSummary},
		},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": usageMap,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("web search emulation: marshal response: %w", err)
	}
	c.Data(http.StatusOK, "application/json", body)

	return &ForwardResult{Model: model, Duration: time.Since(startTime), Usage: usage}, nil
}

func buildWebSearchClaudeUsage(inputTokens, outputTokens int, cacheUsage *kiroCacheEmulationUsage) ClaudeUsage {
	usage := ClaudeUsage{InputTokens: inputTokens, OutputTokens: outputTokens}
	if cacheUsage == nil {
		return usage
	}
	usage.InputTokens = cacheUsage.InputTokens
	usage.CacheReadInputTokens = cacheUsage.CacheReadInputTokens
	usage.CacheCreationInputTokens = cacheUsage.CacheCreationInputTokens
	usage.CacheCreation5mTokens = cacheUsage.CacheCreation5mInputTokens
	usage.CacheCreation1hTokens = cacheUsage.CacheCreation1hInputTokens
	return usage
}

// --- Helpers ---

func buildSearchResultBlocks(results []websearch.SearchResult) []map[string]any {
	blocks := make([]map[string]any, 0, len(results))
	for _, r := range results {
		block := map[string]any{
			"type":              "web_search_result",
			"url":               r.URL,
			"title":             r.Title,
			"encrypted_content": r.Snippet,
			"page_age":          nil,
		}
		if r.PageAge != "" {
			block["page_age"] = r.PageAge
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func buildTextSummary(query string, results []websearch.SearchResult) string {
	if len(results) == 0 {
		return "No search results found for: " + query
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here are the search results for \"%s\":\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return sb.String()
}

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type WebChatResponseCapture struct {
	gin.ResponseWriter
	body            bytes.Buffer
	maxCaptureBytes int
}

func NewWebChatResponseCapture(w gin.ResponseWriter, maxCaptureBytes int) *WebChatResponseCapture {
	return &WebChatResponseCapture{ResponseWriter: w, maxCaptureBytes: maxCaptureBytes}
}

func (w *WebChatResponseCapture) Write(p []byte) (int, error) {
	w.capture(p)
	return w.ResponseWriter.Write(p)
}

func (w *WebChatResponseCapture) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *WebChatResponseCapture) Body() []byte {
	return append([]byte(nil), w.body.Bytes()...)
}

func (w *WebChatResponseCapture) capture(p []byte) {
	if w.maxCaptureBytes <= 0 || w.body.Len() >= w.maxCaptureBytes {
		return
	}
	remaining := w.maxCaptureBytes - w.body.Len()
	if len(p) > remaining {
		_, _ = w.body.Write(p[:remaining])
		return
	}
	_, _ = w.body.Write(p)
}

type webChatStreamCaptureContextKey struct{}

type webChatStreamCapture struct {
	mu              sync.Mutex
	body            bytes.Buffer
	maxCaptureBytes int
}

func newWebChatStreamCapture(maxCaptureBytes int) *webChatStreamCapture {
	return &webChatStreamCapture{maxCaptureBytes: maxCaptureBytes}
}

func withWebChatStreamCapture(ctx context.Context, capture *webChatStreamCapture) context.Context {
	if ctx == nil || capture == nil {
		return ctx
	}
	return context.WithValue(ctx, webChatStreamCaptureContextKey{}, capture)
}

func hasWebChatStreamCapture(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capture, _ := ctx.Value(webChatStreamCaptureContextKey{}).(*webChatStreamCapture)
	return capture != nil
}

func captureWebChatStreamBytes(ctx context.Context, p []byte) {
	if ctx == nil {
		return
	}
	capture, _ := ctx.Value(webChatStreamCaptureContextKey{}).(*webChatStreamCapture)
	if capture == nil {
		return
	}
	capture.Capture(p)
}

func captureWebChatStreamString(ctx context.Context, s string) {
	captureWebChatStreamBytes(ctx, []byte(s))
}

func (c *webChatStreamCapture) Capture(p []byte) {
	if c == nil || len(p) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxCaptureBytes <= 0 || c.body.Len() >= c.maxCaptureBytes {
		return
	}
	remaining := c.maxCaptureBytes - c.body.Len()
	if len(p) > remaining {
		_, _ = c.body.Write(p[:remaining])
		return
	}
	_, _ = c.body.Write(p)
}

func (c *webChatStreamCapture) Body() []byte {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.body.Bytes()...)
}

type WebChatArtifactCandidate struct {
	Filename    string
	ContentType string
	Body        []byte
	Source      string
}

func ExtractAssistantTextFromChatCompletions(body []byte, streamed bool) string {
	if streamed {
		var b strings.Builder
		var terminalText string
		scanner := bufio.NewScanner(bytes.NewReader(body))
		maxTokenSize := len(body)
		if maxTokenSize < 64<<10 {
			maxTokenSize = 64 << 10
		}
		if maxTokenSize > 4<<20 {
			maxTokenSize = 4 << 20
		}
		scanner.Buffer(make([]byte, 0, 64<<10), maxTokenSize)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err == nil && len(chunk.Choices) > 0 {
				_, _ = b.WriteString(chunk.Choices[0].Delta.Content)
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				continue
			}
			switch webChatStringValue(payload["type"]) {
			case "response.output_text.delta":
				_, _ = b.WriteString(webChatStringValue(payload["delta"]))
			case "response.completed":
				if text := extractWebChatResponsesOutputText(payload); text != "" {
					terminalText = text
				}
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
		return terminalText
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(body, &response); err == nil && len(response.Choices) > 0 {
		return chatCompletionContentText(response.Choices[0].Message.Content)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return extractWebChatResponsesOutputText(payload)
}

type webChatProcessDelta struct {
	Reasoning string
	ToolCalls []webChatToolCallDelta
}

type webChatToolCallDelta struct {
	ID           string
	Index        *int
	Name         string
	Input        string
	ReplaceInput bool
}

func ExtractAssistantProcessFromChatCompletions(body []byte, streamed bool) []map[string]any {
	if len(body) == 0 {
		return nil
	}
	if streamed {
		return extractAssistantProcessFromStream(body)
	}
	return extractAssistantProcessFromResponse(body)
}

func extractAssistantProcessFromStream(body []byte) []map[string]any {
	blocks := make([]map[string]any, 0)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	maxTokenSize := len(body)
	if maxTokenSize < 64<<10 {
		maxTokenSize = 64 << 10
	}
	if maxTokenSize > 4<<20 {
		maxTokenSize = 4 << 20
	}
	scanner.Buffer(make([]byte, 0, 64<<10), maxTokenSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		appendWebChatProcessDelta(&blocks, extractWebChatProcessDelta(payload))
	}
	return blocks
}

func extractAssistantProcessFromResponse(body []byte) []map[string]any {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	choices := webChatArrayValue(payload["choices"])
	if len(choices) == 0 {
		appendBlocks := make([]map[string]any, 0)
		appendWebChatProcessDelta(&appendBlocks, extractWebChatProcessDelta(payload))
		return appendBlocks
	}
	choice := webChatMapValue(choices[0])
	message := webChatMapValue(choice["message"])
	if len(message) == 0 {
		return nil
	}
	delta := webChatProcessDelta{
		Reasoning: firstWebChatStringValue(
			message["reasoning_content"],
			message["reasoning"],
			message["reasoning_summary"],
			message["reasoning_text"],
			message["thinking"],
		),
	}
	for _, rawCall := range webChatArrayValue(message["tool_calls"]) {
		if call := webChatToolCallFromMap(webChatMapValue(rawCall), false); !call.isZero() {
			delta.ToolCalls = append(delta.ToolCalls, call)
		}
	}
	blocks := make([]map[string]any, 0, 1+len(delta.ToolCalls))
	appendWebChatProcessDelta(&blocks, delta)
	return blocks
}

func extractWebChatProcessDelta(payload map[string]any) webChatProcessDelta {
	if delta := extractWebChatChatCompletionsProcessDelta(payload); !delta.isZero() {
		return delta
	}
	if delta := extractWebChatResponsesProcessDelta(payload); !delta.isZero() {
		return delta
	}
	return extractWebChatAnthropicProcessDelta(payload)
}

func extractWebChatChatCompletionsProcessDelta(payload map[string]any) webChatProcessDelta {
	choices := webChatArrayValue(payload["choices"])
	if len(choices) == 0 {
		return webChatProcessDelta{}
	}
	choice := webChatMapValue(choices[0])
	deltaMap := webChatMapValue(choice["delta"])
	if len(deltaMap) == 0 {
		return webChatProcessDelta{}
	}

	delta := webChatProcessDelta{
		Reasoning: firstWebChatStringValue(
			deltaMap["reasoning_content"],
			deltaMap["reasoning"],
			deltaMap["reasoning_summary"],
			deltaMap["reasoning_text"],
			deltaMap["thinking"],
		),
	}
	for _, rawCall := range webChatArrayValue(deltaMap["tool_calls"]) {
		if call := webChatToolCallFromMap(webChatMapValue(rawCall), false); !call.isZero() {
			delta.ToolCalls = append(delta.ToolCalls, call)
		}
	}
	functionCall := webChatMapValue(deltaMap["function_call"])
	if len(functionCall) > 0 {
		call := webChatToolCallDelta{
			Name:  webChatStringValue(functionCall["name"]),
			Input: webChatStringValue(functionCall["arguments"]),
		}
		if !call.isZero() {
			delta.ToolCalls = append(delta.ToolCalls, call)
		}
	}
	return delta
}

func extractWebChatResponsesProcessDelta(payload map[string]any) webChatProcessDelta {
	eventType := webChatStringValue(payload["type"])
	switch eventType {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return webChatProcessDelta{Reasoning: webChatStringValue(payload["delta"])}
	case "response.output_item.added", "response.output_item.done":
		item := webChatMapValue(payload["item"])
		itemType := webChatStringValue(item["type"])
		if itemType != "function_call" && itemType != "tool_call" && !strings.HasSuffix(itemType, "_call") {
			return webChatProcessDelta{}
		}
		input := firstWebChatStringValue(item["arguments"], item["query"], item["input"])
		if input == "" {
			input = webChatJSONTextValue(item["action"])
		}
		call := webChatToolCallDelta{
			ID:           firstWebChatStringValue(item["call_id"], item["id"]),
			Name:         firstWebChatStringValue(item["name"], item["type"]),
			Input:        input,
			ReplaceInput: input != "",
		}
		if call.isZero() {
			return webChatProcessDelta{}
		}
		return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
	case "response.function_call_arguments.delta":
		call := webChatToolCallDelta{
			ID:    firstWebChatStringValue(payload["call_id"], payload["item_id"]),
			Input: webChatStringValue(payload["delta"]),
		}
		if call.isZero() {
			return webChatProcessDelta{}
		}
		return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
	default:
		return webChatProcessDelta{}
	}
}

func extractWebChatAnthropicProcessDelta(payload map[string]any) webChatProcessDelta {
	eventType := webChatStringValue(payload["type"])
	switch eventType {
	case "content_block_start":
		block := webChatMapValue(payload["content_block"])
		if webChatStringValue(block["type"]) != "tool_use" {
			return webChatProcessDelta{}
		}
		call := webChatToolCallDelta{
			ID:           webChatStringValue(block["id"]),
			Name:         webChatStringValue(block["name"]),
			Input:        webChatJSONTextValue(block["input"]),
			ReplaceInput: true,
		}
		if index, ok := webChatIntValue(payload["index"]); ok {
			call.Index = &index
		}
		return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
	case "content_block_delta":
		delta := webChatMapValue(payload["delta"])
		switch webChatStringValue(delta["type"]) {
		case "thinking_delta":
			return webChatProcessDelta{Reasoning: webChatStringValue(delta["thinking"])}
		case "input_json_delta":
			call := webChatToolCallDelta{Input: webChatStringValue(delta["partial_json"])}
			if index, ok := webChatIntValue(payload["index"]); ok {
				call.Index = &index
			}
			return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
		default:
			return webChatProcessDelta{}
		}
	default:
		return webChatProcessDelta{}
	}
}

func appendWebChatProcessDelta(blocks *[]map[string]any, delta webChatProcessDelta) {
	if delta.Reasoning != "" {
		appendWebChatReasoningDelta(blocks, delta.Reasoning)
	}
	for _, call := range delta.ToolCalls {
		appendWebChatToolCallDelta(blocks, call)
	}
}

func appendWebChatReasoningDelta(blocks *[]map[string]any, text string) {
	if text == "" {
		return
	}
	if len(*blocks) > 0 {
		last := (*blocks)[len(*blocks)-1]
		if last["type"] == "reasoning" {
			if existing := webChatStringValue(last["text"]); existing != "" {
				last["text"] = existing + text
				return
			}
		}
	}
	*blocks = append(*blocks, map[string]any{
		"type": "reasoning",
		"text": text,
	})
}

func appendWebChatToolCallDelta(blocks *[]map[string]any, call webChatToolCallDelta) {
	if call.isZero() {
		return
	}
	block := findWebChatToolCallBlock(*blocks, call)
	if block == nil {
		block = map[string]any{"type": "tool_call"}
		if call.ID != "" {
			block["id"] = call.ID
		}
		if call.Index != nil {
			block["index"] = *call.Index
		}
		if call.Name != "" {
			block["name"] = call.Name
		}
		if call.Input != "" {
			block["input"] = call.Input
		}
		*blocks = append(*blocks, block)
		return
	}
	if call.ID != "" {
		block["id"] = call.ID
	}
	if call.Index != nil {
		block["index"] = *call.Index
	}
	if call.Name != "" {
		block["name"] = call.Name
	}
	if call.Input != "" {
		if call.ReplaceInput {
			block["input"] = call.Input
		} else {
			block["input"] = webChatStringValue(block["input"]) + call.Input
		}
	}
}

func findWebChatToolCallBlock(blocks []map[string]any, call webChatToolCallDelta) map[string]any {
	if call.ID != "" {
		for _, block := range blocks {
			if block["type"] == "tool_call" && webChatStringValue(block["id"]) == call.ID {
				return block
			}
		}
	}
	if call.Index != nil {
		for _, block := range blocks {
			if block["type"] != "tool_call" {
				continue
			}
			if index, ok := webChatIntValue(block["index"]); ok && index == *call.Index {
				return block
			}
		}
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if block["type"] != "tool_call" {
			continue
		}
		if call.Name == "" || webChatStringValue(block["name"]) == call.Name {
			return block
		}
	}
	return nil
}

func webChatToolCallFromMap(call map[string]any, replaceInput bool) webChatToolCallDelta {
	if len(call) == 0 {
		return webChatToolCallDelta{}
	}
	fn := webChatMapValue(call["function"])
	input := firstWebChatStringValue(fn["arguments"], call["arguments"], call["input"])
	if input == "" {
		input = webChatJSONTextValue(call["input"])
	}
	result := webChatToolCallDelta{
		ID:           webChatStringValue(call["id"]),
		Name:         firstWebChatStringValue(fn["name"], call["name"]),
		Input:        input,
		ReplaceInput: replaceInput,
	}
	if index, ok := webChatIntValue(call["index"]); ok {
		result.Index = &index
	}
	return result
}

func (d webChatProcessDelta) isZero() bool {
	return d.Reasoning == "" && len(d.ToolCalls) == 0
}

func (d webChatToolCallDelta) isZero() bool {
	return d.ID == "" && d.Index == nil && d.Name == "" && d.Input == ""
}

func webChatMapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func webChatArrayValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func webChatStringValue(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

func firstWebChatStringValue(values ...any) string {
	for _, value := range values {
		if text := webChatStringValue(value); text != "" {
			return text
		}
	}
	return ""
}

func webChatJSONTextValue(value any) string {
	if value == nil {
		return ""
	}
	if text := webChatStringValue(value); text != "" {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

func webChatIntValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func ExtractArtifactsFromChatCompletions(_ []byte, _ bool) []WebChatArtifactCandidate {
	return nil
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func chatCompletionContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := partMap["text"].(string); text != "" {
				_, _ = b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}

func extractWebChatResponsesOutputText(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if response := webChatMapValue(payload["response"]); len(response) > 0 {
		payload = response
	}
	var b strings.Builder
	for _, rawItem := range webChatArrayValue(payload["output"]) {
		item := webChatMapValue(rawItem)
		if len(item) == 0 {
			continue
		}
		if role := webChatStringValue(item["role"]); role != "" && role != WebChatRoleAssistant {
			continue
		}
		for _, rawPart := range webChatArrayValue(item["content"]) {
			part := webChatMapValue(rawPart)
			switch webChatStringValue(part["type"]) {
			case "output_text", "text":
				_, _ = b.WriteString(webChatStringValue(part["text"]))
			}
		}
	}
	return b.String()
}

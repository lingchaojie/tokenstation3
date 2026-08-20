package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type WebChatResponseCapture struct {
	gin.ResponseWriter
	body            bytes.Buffer
	maxCaptureBytes int
	truncated       bool
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

func (w *WebChatResponseCapture) Snapshot() ([]byte, bool) {
	if w == nil {
		return nil, false
	}
	return w.Body(), w.truncated
}

func (w *WebChatResponseCapture) capture(p []byte) {
	if w.maxCaptureBytes <= 0 {
		return
	}
	if w.body.Len() >= w.maxCaptureBytes {
		w.truncated = len(p) > 0
		return
	}
	remaining := w.maxCaptureBytes - w.body.Len()
	if len(p) > remaining {
		_, _ = w.body.Write(p[:remaining])
		w.truncated = true
		return
	}
	_, _ = w.body.Write(p)
}

type webChatStreamCaptureContextKey struct{}

type webChatStreamCapture struct {
	mu              sync.Mutex
	body            bytes.Buffer
	maxCaptureBytes int
	truncated       bool
	terminalError   error
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
	if c.maxCaptureBytes <= 0 {
		return
	}
	if c.body.Len() >= c.maxCaptureBytes {
		c.truncated = true
		return
	}
	remaining := c.maxCaptureBytes - c.body.Len()
	if len(p) > remaining {
		_, _ = c.body.Write(p[:remaining])
		c.truncated = true
		return
	}
	_, _ = c.body.Write(p)
}

func (c *webChatStreamCapture) Body() []byte {
	body, _ := c.Snapshot()
	return body
}

func (c *webChatStreamCapture) Snapshot() ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.body.Bytes()...), c.truncated
}

func publishWebChatStreamTerminalError(ctx context.Context, err error) {
	if ctx == nil || err == nil {
		return
	}
	capture, _ := ctx.Value(webChatStreamCaptureContextKey{}).(*webChatStreamCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	capture.terminalError = err
	capture.mu.Unlock()
}

func takeWebChatStreamTerminalError(capture *webChatStreamCapture) error {
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	err := capture.terminalError
	capture.terminalError = nil
	return err
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
		maxTokenSize := len(body) + 1
		if maxTokenSize < 64<<10 {
			maxTokenSize = 64 << 10
		}
		if maxTokenSize > captureHardMaxBodyBytes+1 {
			maxTokenSize = captureHardMaxBodyBytes + 1
		}
		scanner.Buffer(make([]byte, 0, 64<<10), maxTokenSize)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) || !json.Valid(data) {
				continue
			}
			root := gjson.ParseBytes(data)
			if content := root.Get("choices.0.delta.content"); content.Type == gjson.String {
				_, _ = b.WriteString(content.String())
				continue
			}
			switch root.Get("type").String() {
			case "response.output_text.delta":
				_, _ = b.WriteString(root.Get("delta").String())
			case "response.completed":
				if text := extractWebChatResponsesOutputTextResult(root); text != "" {
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
	if !json.Valid(body) {
		return ""
	}
	return extractWebChatResponsesOutputTextResult(gjson.ParseBytes(body))
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
	state := newWebChatProcessState(0)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	maxTokenSize := len(body) + 1
	if maxTokenSize < 64<<10 {
		maxTokenSize = 64 << 10
	}
	if maxTokenSize > captureHardMaxBodyBytes+1 {
		maxTokenSize = captureHardMaxBodyBytes + 1
	}
	scanner.Buffer(make([]byte, 0, 64<<10), maxTokenSize)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) || !json.Valid(data) {
			continue
		}
		state.appendDelta(extractWebChatProcessDeltaResult(gjson.ParseBytes(data)))
	}
	return finalizeWebChatProcessBlocks(state.blocks)
}

func extractAssistantProcessFromResponse(body []byte) []map[string]any {
	if !json.Valid(body) {
		return nil
	}
	root := gjson.ParseBytes(body)
	delta := extractWebChatProcessDeltaResult(root)
	if message := root.Get("choices.0.message"); message.IsObject() {
		delta = extractWebChatChatMessageProcessDeltaResult(message)
	}
	state := newWebChatProcessState(1 + len(delta.ToolCalls))
	state.appendDelta(delta)
	return finalizeWebChatProcessBlocks(state.blocks)
}

func extractWebChatProcessDeltaResult(root gjson.Result) webChatProcessDelta {
	if delta := root.Get("choices.0.delta"); delta.IsObject() {
		if result := extractWebChatChatMessageProcessDeltaResult(delta); !result.isZero() {
			return result
		}
	}
	eventType := root.Get("type").String()
	switch eventType {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return webChatProcessDelta{Reasoning: root.Get("delta").String()}
	case "response.output_item.added", "response.output_item.done":
		item := root.Get("item")
		itemType := item.Get("type").String()
		if item.IsObject() && (itemType == "function_call" || itemType == "tool_call" || strings.HasSuffix(itemType, "_call")) {
			input := firstWebChatGJSONString(item.Get("arguments"), item.Get("query"), item.Get("input"))
			if input == "" {
				input = webChatGJSONTextValue(item.Get("action"))
			}
			call := webChatToolCallDelta{
				ID: firstWebChatGJSONString(item.Get("call_id"), item.Get("id")), Name: firstWebChatGJSONString(item.Get("name"), item.Get("type")),
				Input: input, ReplaceInput: input != "",
			}
			if !call.isZero() {
				return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
			}
		}
	case "response.function_call_arguments.delta":
		call := webChatToolCallDelta{ID: firstWebChatGJSONString(root.Get("call_id"), root.Get("item_id")), Input: root.Get("delta").String()}
		if !call.isZero() {
			return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
		}
	case "content_block_start":
		block := root.Get("content_block")
		if block.Get("type").String() == "tool_use" {
			call := webChatToolCallDelta{ID: block.Get("id").String(), Name: block.Get("name").String(), Input: webChatGJSONTextValue(block.Get("input")), ReplaceInput: true}
			if index := root.Get("index"); index.Type == gjson.Number {
				value := int(index.Int())
				call.Index = &value
			}
			if !call.isZero() {
				return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
			}
		}
	case "content_block_delta":
		delta := root.Get("delta")
		switch delta.Get("type").String() {
		case "thinking_delta":
			return webChatProcessDelta{Reasoning: delta.Get("thinking").String()}
		case "input_json_delta":
			call := webChatToolCallDelta{Input: delta.Get("partial_json").String()}
			if index := root.Get("index"); index.Type == gjson.Number {
				value := int(index.Int())
				call.Index = &value
			}
			if !call.isZero() {
				return webChatProcessDelta{ToolCalls: []webChatToolCallDelta{call}}
			}
		}
	}
	return webChatProcessDelta{}
}

func extractWebChatChatMessageProcessDeltaResult(message gjson.Result) webChatProcessDelta {
	delta := webChatProcessDelta{Reasoning: firstWebChatGJSONString(
		message.Get("reasoning_content"), message.Get("reasoning"), message.Get("reasoning_summary"), message.Get("reasoning_text"), message.Get("thinking"),
	)}
	message.Get("tool_calls").ForEach(func(_, rawCall gjson.Result) bool {
		if call := webChatToolCallFromResult(rawCall, false); !call.isZero() {
			delta.ToolCalls = append(delta.ToolCalls, call)
		}
		return true
	})
	if functionCall := message.Get("function_call"); functionCall.IsObject() {
		call := webChatToolCallDelta{Name: functionCall.Get("name").String(), Input: functionCall.Get("arguments").String()}
		if !call.isZero() {
			delta.ToolCalls = append(delta.ToolCalls, call)
		}
	}
	return delta
}

func webChatToolCallFromResult(call gjson.Result, replaceInput bool) webChatToolCallDelta {
	if !call.IsObject() {
		return webChatToolCallDelta{}
	}
	fn := call.Get("function")
	input := firstWebChatGJSONString(fn.Get("arguments"), call.Get("arguments"), call.Get("input"))
	if input == "" {
		input = webChatGJSONTextValue(call.Get("input"))
	}
	result := webChatToolCallDelta{
		ID: call.Get("id").String(), Name: firstWebChatGJSONString(fn.Get("name"), call.Get("name")), Input: input, ReplaceInput: replaceInput,
	}
	if index := call.Get("index"); index.Type == gjson.Number {
		value := int(index.Int())
		result.Index = &value
	}
	return result
}

func firstWebChatGJSONString(values ...gjson.Result) string {
	for _, value := range values {
		if value.Type == gjson.String && value.String() != "" {
			return value.String()
		}
	}
	return ""
}

func webChatGJSONTextValue(value gjson.Result) string {
	if value.Type == gjson.String {
		return value.String()
	}
	if !value.Exists() || value.Type == gjson.Null || value.Raw == "null" {
		return ""
	}
	return value.Raw
}

const (
	webChatReasoningBuilderKey = "_sub2api_reasoning_builder"
	webChatToolInputBuilderKey = "_sub2api_tool_input_builder"
)

type webChatProcessState struct {
	blocks         []map[string]any
	retainedBytes  int
	toolByID       map[string]int
	toolByIndex    map[int]int
	latestByName   map[string]int
	latestToolCall int
}

func newWebChatProcessState(capacity int) *webChatProcessState {
	return &webChatProcessState{
		blocks:         make([]map[string]any, 0, capacity),
		toolByID:       make(map[string]int),
		toolByIndex:    make(map[int]int),
		latestByName:   make(map[string]int),
		latestToolCall: -1,
	}
}

func (s *webChatProcessState) appendDelta(delta webChatProcessDelta) {
	if delta.Reasoning != "" {
		s.appendReasoning(delta.Reasoning)
	}
	for _, call := range delta.ToolCalls {
		s.appendToolCall(call)
	}
}

func (s *webChatProcessState) appendReasoning(text string) {
	if text == "" {
		return
	}
	if len(s.blocks) > 0 {
		last := s.blocks[len(s.blocks)-1]
		if last["type"] == "reasoning" {
			if builder, ok := last[webChatReasoningBuilderKey].(*strings.Builder); ok {
				appendWebChatProcessString(builder, text, &s.retainedBytes)
				return
			}
		}
	}
	builder := &strings.Builder{}
	appendWebChatProcessString(builder, text, &s.retainedBytes)
	s.blocks = append(s.blocks, map[string]any{"type": "reasoning", webChatReasoningBuilderKey: builder})
}

func (s *webChatProcessState) appendToolCall(call webChatToolCallDelta) {
	if call.isZero() {
		return
	}
	blockIndex := s.findToolCall(call)
	if blockIndex < 0 {
		block := map[string]any{"type": "tool_call"}
		if call.Input != "" {
			builder := &strings.Builder{}
			appendWebChatProcessString(builder, call.Input, &s.retainedBytes)
			block[webChatToolInputBuilderKey] = builder
		}
		s.blocks = append(s.blocks, block)
		s.updateToolCallIdentity(len(s.blocks)-1, call)
		return
	}
	block := s.blocks[blockIndex]
	s.updateToolCallIdentity(blockIndex, call)
	if call.Input != "" {
		builder, _ := block[webChatToolInputBuilderKey].(*strings.Builder)
		if builder == nil {
			builder = &strings.Builder{}
			if existing := webChatStringValue(block["input"]); existing != "" {
				appendWebChatProcessString(builder, existing, &s.retainedBytes)
				delete(block, "input")
			}
			block[webChatToolInputBuilderKey] = builder
		}
		if call.ReplaceInput {
			s.retainedBytes -= builder.Len()
			builder.Reset()
		}
		appendWebChatProcessString(builder, call.Input, &s.retainedBytes)
	}
}

func (s *webChatProcessState) findToolCall(call webChatToolCallDelta) int {
	if call.ID != "" {
		if index, ok := s.toolByID[call.ID]; ok {
			return index
		}
		return -1
	}
	if call.Index != nil {
		if index, ok := s.toolByIndex[*call.Index]; ok {
			return index
		}
		return -1
	}
	if call.Name != "" {
		if index, ok := s.latestByName[call.Name]; ok {
			return index
		}
		return -1
	}
	return s.latestToolCall
}

func (s *webChatProcessState) updateToolCallIdentity(blockIndex int, call webChatToolCallDelta) {
	block := s.blocks[blockIndex]
	if call.ID != "" {
		if old := webChatStringValue(block["id"]); old != "" && old != call.ID {
			if oldIndex, ok := s.toolByID[old]; ok && oldIndex == blockIndex {
				delete(s.toolByID, old)
			}
		}
		block["id"] = call.ID
		s.toolByID[call.ID] = blockIndex
	}
	if call.Index != nil {
		if old, ok := webChatIntValue(block["index"]); ok && old != *call.Index {
			if oldIndex, found := s.toolByIndex[old]; found && oldIndex == blockIndex {
				delete(s.toolByIndex, old)
			}
		}
		block["index"] = *call.Index
		s.toolByIndex[*call.Index] = blockIndex
	}
	if call.Name != "" {
		if old := webChatStringValue(block["name"]); old != "" && old != call.Name {
			if oldIndex, ok := s.latestByName[old]; ok && oldIndex == blockIndex {
				delete(s.latestByName, old)
			}
		}
		block["name"] = call.Name
		s.latestByName[call.Name] = blockIndex
	}
	s.latestToolCall = blockIndex
}

func appendWebChatProcessString(builder *strings.Builder, value string, retainedBytes *int) {
	if builder == nil || value == "" {
		return
	}
	if retainedBytes != nil && (len(value) > captureHardMaxBodyBytes-*retainedBytes) {
		return
	}
	_, _ = builder.WriteString(value)
	if retainedBytes != nil {
		*retainedBytes += len(value)
	}
}

func finalizeWebChatProcessBlocks(blocks []map[string]any) []map[string]any {
	for _, block := range blocks {
		if builder, ok := block[webChatReasoningBuilderKey].(*strings.Builder); ok {
			block["text"] = builder.String()
			delete(block, webChatReasoningBuilderKey)
		}
		if builder, ok := block[webChatToolInputBuilderKey].(*strings.Builder); ok {
			block["input"] = builder.String()
			delete(block, webChatToolInputBuilderKey)
		}
	}
	return blocks
}

func (d webChatProcessDelta) isZero() bool {
	return d.Reasoning == "" && len(d.ToolCalls) == 0
}

func (d webChatToolCallDelta) isZero() bool {
	return d.ID == "" && d.Index == nil && d.Name == "" && d.Input == ""
}

func webChatStringValue(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
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

func extractWebChatResponsesOutputTextResult(root gjson.Result) string {
	if response := root.Get("response"); response.IsObject() {
		root = response
	}
	var b strings.Builder
	root.Get("output").ForEach(func(_, item gjson.Result) bool {
		role := item.Get("role").String()
		if role != "" && role != WebChatRoleAssistant {
			return true
		}
		item.Get("content").ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			case "output_text", "text":
				_, _ = b.WriteString(part.Get("text").String())
			}
			return true
		})
		return true
	})
	return b.String()
}

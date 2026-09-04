package apicompat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Non-streaming: ResponsesResponse → ChatCompletionsResponse
// ---------------------------------------------------------------------------

// ResponsesToChatCompletions converts a Responses API response into a Chat
// Completions response. Text output items are concatenated into
// choices[0].message.content; function_call items become tool_calls.
func ResponsesToChatCompletions(resp *ResponsesResponse, model string) *ChatCompletionsResponse {
	id := resp.ID
	if id == "" {
		id = generateChatCmplID()
	}

	out := &ChatCompletionsResponse{
		ID:          id,
		Object:      "chat.completion",
		Created:     time.Now().Unix(),
		Model:       model,
		ServiceTier: resp.ServiceTier,
	}

	var contentText strings.Builder
	var refusalText strings.Builder
	var reasoningText strings.Builder
	var toolCalls []ChatToolCall

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					if part.Text != "" {
						_, _ = contentText.WriteString(part.Text)
					}
				case "refusal":
					if part.Refusal != "" {
						_, _ = refusalText.WriteString(part.Refusal)
					}
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		case "custom_tool_call":
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      item.Name,
					Arguments: item.Input,
				},
			})
		case "reasoning":
			for _, s := range item.Summary {
				if s.Type == "summary_text" && s.Text != "" {
					_, _ = reasoningText.WriteString(s.Text)
				}
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" && part.Text != "" {
					_, _ = reasoningText.WriteString(part.Text)
				}
			}
		case "web_search_call":
			// silently consumed — results already incorporated into text output
		}
	}

	msg := ChatMessage{Role: "assistant"}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if contentText.Len() > 0 {
		raw, _ := json.Marshal(contentText.String())
		msg.Content = raw
	}
	if refusalText.Len() > 0 {
		refusal := refusalText.String()
		msg.Refusal = &refusal
	}
	if reasoningText.Len() > 0 {
		msg.ReasoningContent = reasoningText.String()
	}

	finishReason := responsesStatusToChatFinishReason(resp.Status, resp.IncompleteDetails, toolCalls)

	out.Choices = []ChatChoice{{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason,
	}}

	out.Usage = chatUsageFromResponsesUsage(resp.Usage)

	return out
}

func responsesStatusToChatFinishReason(status string, details *ResponsesIncompleteDetails, toolCalls []ChatToolCall) string {
	switch status {
	case "incomplete":
		if details != nil {
			switch details.Reason {
			case "max_output_tokens":
				return "length"
			case "content_filter":
				return "content_filter"
			}
		}
		return "stop"
	case "completed":
		if len(toolCalls) > 0 {
			return "tool_calls"
		}
		return "stop"
	default:
		return "stop"
	}
}

// ---------------------------------------------------------------------------
// Streaming: ResponsesStreamEvent → []ChatCompletionsChunk (stateful converter)
// ---------------------------------------------------------------------------

// ResponsesEventToChatState tracks state for converting a sequence of Responses
// SSE events into Chat Completions SSE chunks.
type ResponsesEventToChatState struct {
	ID                     string
	Model                  string
	Created                int64
	ServiceTier            string // upstream tier observed on response events; echoed on chunks
	SentRole               bool
	SawToolCall            bool
	SawText                bool
	Finalized              bool        // true after finish chunk has been emitted
	NextToolCallIndex      int         // next sequential tool_call index to assign
	OutputIndexToToolIndex map[int]int // Responses output_index → Chat tool_calls index
	ToolInputHadDelta      map[int]bool
	TextualOutputHadDelta  map[responsesTextStreamKey]bool
	OutputIndexHadSemantic map[int]bool
	IncludeUsage           bool
	Usage                  *ChatUsage
}

// NewResponsesEventToChatState returns an initialised stream state.
func NewResponsesEventToChatState() *ResponsesEventToChatState {
	return &ResponsesEventToChatState{
		ID:                     generateChatCmplID(),
		Created:                time.Now().Unix(),
		OutputIndexToToolIndex: make(map[int]int),
		ToolInputHadDelta:      make(map[int]bool),
		TextualOutputHadDelta:  make(map[responsesTextStreamKey]bool),
		OutputIndexHadSemantic: make(map[int]bool),
	}
}

// ResponsesEventToChatChunks converts a single Responses SSE event into zero
// or more Chat Completions chunks, updating state as it goes.
func ResponsesEventToChatChunks(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	switch evt.Type {
	case "response.created":
		return resToChatHandleCreated(evt, state)
	case "response.output_text.delta":
		state.TextualOutputHadDelta[responsesTextKey(evt)] = true
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		return resToChatHandleTextDelta(evt, state)
	case "response.output_text.done":
		if state.TextualOutputHadDelta[responsesTextKey(evt)] || evt.Text == "" {
			return nil
		}
		copyEvent := *evt
		copyEvent.Delta = evt.Text
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		return resToChatHandleTextDelta(&copyEvent, state)
	case "response.refusal.delta":
		state.TextualOutputHadDelta[responsesTextKey(evt)] = true
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		refusal := evt.Delta
		if refusal == "" {
			refusal = evt.Refusal
		}
		return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Refusal: &refusal})}
	case "response.refusal.done":
		if state.TextualOutputHadDelta[responsesTextKey(evt)] || evt.Refusal == "" {
			return nil
		}
		refusal := evt.Refusal
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Refusal: &refusal})}
	case "response.output_item.added":
		return resToChatHandleOutputItemAdded(evt, state)
	case "response.output_item.done":
		return resToChatHandleOutputItemDone(evt, state)
	case "response.function_call_arguments.delta",
		// custom/freeform 工具（如新版 apply_patch）的输入增量与 function_call 参数增量同形，
		// 均按 OutputIndex 累加到对应工具调用。
		"response.custom_tool_call_input.delta":
		return resToChatHandleFuncArgsDelta(evt, state)
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		return resToChatHandleFuncArgsDone(evt, state)
	case "response.reasoning_summary_text.delta",
		// 原始推理文本增量（真实 Codex 客户端消费的 reasoning_text.delta），
		// 与 reasoning summary 一样映射为 reasoning_content。
		"response.reasoning_text.delta":
		state.TextualOutputHadDelta[responsesTextKey(evt)] = true
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		return resToChatHandleReasoningDelta(evt, state)
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		if state.TextualOutputHadDelta[responsesTextKey(evt)] || evt.Text == "" {
			return nil
		}
		copyEvent := *evt
		copyEvent.Delta = evt.Text
		state.OutputIndexHadSemantic[evt.OutputIndex] = true
		return resToChatHandleReasoningDelta(&copyEvent, state)
	// response.done 是 Realtime/WS 与项目透传路径使用的终止别名；
	// 普通 Responses HTTP SSE 的公开终止事件仍以 response.completed 为主。
	case "response.completed", "response.done", "response.incomplete", "response.failed":
		return resToChatHandleCompleted(evt, state)
	default:
		return nil
	}
}

// FinalizeResponsesChatStream emits a final chunk with finish_reason if the
// stream ended without a proper completion event (e.g. upstream disconnect).
// It is idempotent: if a completion event already emitted the finish chunk,
// this returns nil.
func FinalizeResponsesChatStream(state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if state.Finalized {
		return nil
	}
	state.Finalized = true

	finishReason := "stop"
	if state.SawToolCall {
		finishReason = "tool_calls"
	}

	chunks := []ChatCompletionsChunk{makeChatFinishChunk(state, finishReason)}

	if state.IncludeUsage && state.Usage != nil {
		chunks = append(chunks, ChatCompletionsChunk{
			ID:          state.ID,
			Object:      "chat.completion.chunk",
			Created:     state.Created,
			Model:       state.Model,
			ServiceTier: state.ServiceTier,
			Choices:     []ChatChunkChoice{},
			Usage:       state.Usage,
		})
	}

	return chunks
}

// ChatChunkToSSE formats a ChatCompletionsChunk as an SSE data line.
func ChatChunkToSSE(chunk ChatCompletionsChunk) (string, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data: %s\n\n", data), nil
}

// --- internal handlers ---

func resToChatHandleCreated(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Response != nil {
		if evt.Response.ID != "" {
			state.ID = evt.Response.ID
		}
		if state.Model == "" && evt.Response.Model != "" {
			state.Model = evt.Response.Model
		}
		if evt.Response.ServiceTier != "" {
			state.ServiceTier = evt.Response.ServiceTier
		}
	}
	// Emit the role chunk.
	if state.SentRole {
		return nil
	}
	state.SentRole = true

	role := "assistant"
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Role: role})}
}

func resToChatHandleTextDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}
	state.SawText = true
	content := evt.Delta
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Content: &content})}
}

func resToChatHandleOutputItemAdded(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	// function_call 与 custom_tool_call（custom/freeform 工具）均按工具调用注册，
	// 以便后续 *_input.delta / *_arguments.delta 能映射到正确的工具索引。
	if evt.Item == nil || (evt.Item.Type != "function_call" && evt.Item.Type != "custom_tool_call") {
		return nil
	}

	state.SawToolCall = true
	idx := state.NextToolCallIndex
	state.OutputIndexToToolIndex[evt.OutputIndex] = idx
	state.NextToolCallIndex++

	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{
		ToolCalls: []ChatToolCall{{
			Index: &idx,
			ID:    evt.Item.CallID,
			Type:  "function",
			Function: ChatFunctionCall{
				Name: evt.Item.Name,
			},
		}},
	})}
}

func resToChatHandleOutputItemDone(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Item == nil {
		return nil
	}
	switch evt.Item.Type {
	case "function_call", "custom_tool_call":
		var chunks []ChatCompletionsChunk
		if _, exists := state.OutputIndexToToolIndex[evt.OutputIndex]; !exists {
			chunks = append(chunks, resToChatHandleOutputItemAdded(evt, state)...)
		}
		done := *evt
		if evt.Item.Type == "custom_tool_call" {
			done.Type = "response.custom_tool_call_input.done"
			done.Input = evt.Item.Input
		} else {
			done.Type = "response.function_call_arguments.done"
			done.Arguments = evt.Item.Arguments
		}
		chunks = append(chunks, resToChatHandleFuncArgsDone(&done, state)...)
		return chunks
	case "message":
		if state.OutputIndexHadSemantic[evt.OutputIndex] {
			return nil
		}
		var chunks []ChatCompletionsChunk
		for _, part := range evt.Item.Content {
			switch part.Type {
			case "output_text":
				if part.Text != "" {
					text := part.Text
					chunks = append(chunks, makeChatDeltaChunk(state, ChatDelta{Content: &text}))
				}
			case "refusal":
				if part.Refusal != "" {
					refusal := part.Refusal
					chunks = append(chunks, makeChatDeltaChunk(state, ChatDelta{Refusal: &refusal}))
				}
			}
		}
		state.OutputIndexHadSemantic[evt.OutputIndex] = len(chunks) > 0
		return chunks
	case "reasoning":
		if state.OutputIndexHadSemantic[evt.OutputIndex] {
			return nil
		}
		var chunks []ChatCompletionsChunk
		for _, summary := range evt.Item.Summary {
			if summary.Type == "summary_text" && summary.Text != "" {
				value := summary.Text
				chunks = append(chunks, makeChatDeltaChunk(state, ChatDelta{ReasoningContent: &value}))
			}
		}
		for _, part := range evt.Item.Content {
			if part.Type == "reasoning_text" && part.Text != "" {
				value := part.Text
				chunks = append(chunks, makeChatDeltaChunk(state, ChatDelta{ReasoningContent: &value}))
			}
		}
		state.OutputIndexHadSemantic[evt.OutputIndex] = len(chunks) > 0
		return chunks
	}
	return nil
}

func resToChatHandleFuncArgsDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}

	idx, ok := state.OutputIndexToToolIndex[evt.OutputIndex]
	if !ok {
		return nil
	}
	state.ToolInputHadDelta[evt.OutputIndex] = true
	state.OutputIndexHadSemantic[evt.OutputIndex] = true

	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{
		ToolCalls: []ChatToolCall{{
			Index: &idx,
			Function: ChatFunctionCall{
				Arguments: evt.Delta,
			},
		}},
	})}
}

func resToChatHandleFuncArgsDone(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if state.ToolInputHadDelta[evt.OutputIndex] {
		return nil
	}
	arguments := evt.Arguments
	if evt.Type == "response.custom_tool_call_input.done" {
		arguments = evt.Input
	}
	if arguments == "" {
		return nil
	}
	copyEvent := *evt
	copyEvent.Delta = arguments
	return resToChatHandleFuncArgsDelta(&copyEvent, state)
}

func resToChatHandleReasoningDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}
	reasoning := evt.Delta
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{ReasoningContent: &reasoning})}
}

func resToChatHandleCompleted(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	state.Finalized = true
	finishReason := "stop"

	if evt.Usage != nil {
		state.Usage = chatUsageFromResponsesUsage(evt.Usage)
	}
	if evt.Response != nil {
		if evt.Response.Usage != nil {
			state.Usage = chatUsageFromResponsesUsage(evt.Response.Usage)
		}
		if evt.Response.ServiceTier != "" {
			state.ServiceTier = evt.Response.ServiceTier
		}

		switch evt.Response.Status {
		case "incomplete":
			if evt.Response.IncompleteDetails != nil {
				switch evt.Response.IncompleteDetails.Reason {
				case "max_output_tokens":
					finishReason = "length"
				case "content_filter":
					finishReason = "content_filter"
				}
			}
		case "completed":
			if state.SawToolCall {
				finishReason = "tool_calls"
			}
		}
	} else if state.SawToolCall {
		finishReason = "tool_calls"
	}

	var chunks []ChatCompletionsChunk
	chunks = append(chunks, makeChatFinishChunk(state, finishReason))

	if state.IncludeUsage && state.Usage != nil {
		chunks = append(chunks, ChatCompletionsChunk{
			ID:          state.ID,
			Object:      "chat.completion.chunk",
			Created:     state.Created,
			Model:       state.Model,
			ServiceTier: state.ServiceTier,
			Choices:     []ChatChunkChoice{},
			Usage:       state.Usage,
		})
	}

	return chunks
}

func chatUsageFromResponsesUsage(u *ResponsesUsage) *ChatUsage {
	if u == nil {
		return nil
	}
	usage := &ChatUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
	usage.PromptTokensDetails = promptDetailsFromResponses(u.InputTokensDetails)
	if u.CacheCreationInputTokens > 0 {
		if usage.PromptTokensDetails == nil {
			usage.PromptTokensDetails = &ChatTokenDetails{}
		}
		if usage.PromptTokensDetails.CacheWriteTokens == 0 && usage.PromptTokensDetails.CacheCreationTokens == 0 {
			usage.PromptTokensDetails.CacheCreationTokens = u.CacheCreationInputTokens
		}
	}
	usage.CompletionTokensDetails = completionDetailsFromResponses(u.OutputTokensDetails)
	return usage
}

// promptDetailsFromResponses maps Responses-API input_tokens_details into a
// Chat-Completions prompt_tokens_details. Returns nil when nothing would be
// emitted, so upstreams that do not break down prompt usage stay clean.
func promptDetailsFromResponses(src *ResponsesInputTokensDetails) *ChatTokenDetails {
	if src == nil {
		return nil
	}
	if src.CachedTokens == 0 && src.AudioTokens == 0 && src.CacheCreationTokens == 0 && src.CacheWriteTokens == 0 {
		return nil
	}
	return &ChatTokenDetails{
		CachedTokens:        src.CachedTokens,
		AudioTokens:         src.AudioTokens,
		CacheCreationTokens: src.CacheCreationTokens,
		CacheWriteTokens:    src.CacheWriteTokens,
	}
}

// completionDetailsFromResponses maps Responses-API output_tokens_details
// into a Chat-Completions completion_tokens_details. Mirrors the OpenAI
// official CompletionUsage schema: reasoning_tokens, audio_tokens,
// image_tokens, and
// the predicted-outputs accepted/rejected counts. Returns nil when nothing
// would be emitted so non-reasoning, non-audio responses stay clean.
func completionDetailsFromResponses(src *ResponsesOutputTokensDetails) *ChatTokenDetails {
	if src == nil {
		return nil
	}
	if src.ReasoningTokens == 0 && src.AudioTokens == 0 && src.ImageTokens == 0 &&
		src.AcceptedPredictionTokens == 0 && src.RejectedPredictionTokens == 0 {
		return nil
	}
	return &ChatTokenDetails{
		ReasoningTokens:          src.ReasoningTokens,
		AudioTokens:              src.AudioTokens,
		ImageTokens:              src.ImageTokens,
		AcceptedPredictionTokens: src.AcceptedPredictionTokens,
		RejectedPredictionTokens: src.RejectedPredictionTokens,
	}
}

func makeChatDeltaChunk(state *ResponsesEventToChatState, delta ChatDelta) ChatCompletionsChunk {
	return ChatCompletionsChunk{
		ID:          state.ID,
		Object:      "chat.completion.chunk",
		Created:     state.Created,
		Model:       state.Model,
		ServiceTier: state.ServiceTier,
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: nil,
		}},
	}
}

func makeChatFinishChunk(state *ResponsesEventToChatState, finishReason string) ChatCompletionsChunk {
	empty := ""
	return ChatCompletionsChunk{
		ID:          state.ID,
		Object:      "chat.completion.chunk",
		Created:     state.Created,
		Model:       state.Model,
		ServiceTier: state.ServiceTier,
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        ChatDelta{Content: &empty},
			FinishReason: &finishReason,
		}},
	}
}

// generateChatCmplID returns a "chatcmpl-" prefixed random hex ID.
func generateChatCmplID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// BufferedResponseAccumulator: accumulates SSE delta events for non-streaming
// paths where the terminal event may have empty output.
// ---------------------------------------------------------------------------

type bufferedFuncCall struct {
	Type     string
	CallID   string
	Name     string
	Args     strings.Builder
	HadDelta bool
}

type responsesTextStreamKey struct {
	Type         string
	OutputIndex  int
	ContentIndex int
	SummaryIndex int
}

func responsesTextKey(event *ResponsesStreamEvent) responsesTextStreamKey {
	eventType := strings.TrimSuffix(event.Type, ".delta")
	eventType = strings.TrimSuffix(eventType, ".done")
	return responsesTextStreamKey{
		Type: eventType, OutputIndex: event.OutputIndex, ContentIndex: event.ContentIndex, SummaryIndex: event.SummaryIndex,
	}
}

const (
	maxBufferedResponsesRetainedOutputBytes = 8 << 20
	maxBufferedResponsesFunctionCalls       = 1024
	maxBufferedResponsesRetainedMetadata    = 1024
)

// BufferedResponseAccumulator collects content from Responses SSE delta events
// so that non-streaming handlers can reconstruct output when the terminal event
// (response.completed / response.done) carries an empty output array.
type BufferedResponseAccumulator struct {
	text                  strings.Builder
	refusal               strings.Builder
	reasoning             strings.Builder
	funcCalls             []bufferedFuncCall
	outputIndexToFuncIdx  map[int]int
	retainedOutputBytes   int
	retainedItems         int
	textualOutputHadDelta map[responsesTextStreamKey]bool
	trackedOutputIndexes  map[int]bool
	err                   error
}

// NewBufferedResponseAccumulator returns an initialised accumulator.
func NewBufferedResponseAccumulator() *BufferedResponseAccumulator {
	return &BufferedResponseAccumulator{
		outputIndexToFuncIdx:  make(map[int]int),
		textualOutputHadDelta: make(map[responsesTextStreamKey]bool),
		trackedOutputIndexes:  make(map[int]bool),
	}
}

// ProcessEvent inspects a single Responses SSE event and accumulates any
// content it carries. Only delta events that contribute to the final output
// are handled; all other event types are silently ignored.
func (a *BufferedResponseAccumulator) ProcessEvent(event *ResponsesStreamEvent) error {
	if a == nil || event == nil {
		return nil
	}
	if a.err != nil {
		return a.err
	}
	switch event.Type {
	case "response.output_text.delta":
		a.textualOutputHadDelta[responsesTextKey(event)] = true
		a.appendOutput(&a.text, event.Delta)
	case "response.output_text.done":
		key := responsesTextKey(event)
		if !a.textualOutputHadDelta[key] {
			a.appendOutput(&a.text, event.Text)
			if a.err == nil && event.Text != "" {
				a.textualOutputHadDelta[key] = true
			}
		}
	case "response.refusal.delta":
		a.textualOutputHadDelta[responsesTextKey(event)] = true
		fragment := event.Delta
		if fragment == "" {
			fragment = event.Refusal
		}
		a.appendOutput(&a.refusal, fragment)
	case "response.refusal.done":
		key := responsesTextKey(event)
		if !a.textualOutputHadDelta[key] {
			a.appendOutput(&a.refusal, event.Refusal)
			if a.err == nil && event.Refusal != "" {
				a.textualOutputHadDelta[key] = true
			}
		}
	case "response.output_item.added":
		if event.Item != nil && isBufferedResponsesOutputItem(event.Item.Type) {
			if !a.trackedOutputIndexes[event.OutputIndex] {
				if err := a.retainOutput(0, 1); err != nil {
					break
				}
				a.trackedOutputIndexes[event.OutputIndex] = true
			}
			if event.Item.Type != "function_call" && event.Item.Type != "custom_tool_call" {
				break
			}
			if len(event.Item.CallID) > maxBufferedResponsesRetainedMetadata || len(event.Item.Name) > maxBufferedResponsesRetainedMetadata {
				a.err = fmt.Errorf("responses buffered function-call metadata exceeds %d bytes", maxBufferedResponsesRetainedMetadata)
				break
			}
			if _, exists := a.outputIndexToFuncIdx[event.OutputIndex]; exists {
				a.err = fmt.Errorf("responses buffered output repeated function-call index %d", event.OutputIndex)
				break
			}
			idx := len(a.funcCalls)
			a.outputIndexToFuncIdx[event.OutputIndex] = idx
			a.funcCalls = append(a.funcCalls, bufferedFuncCall{
				Type:   event.Item.Type,
				CallID: event.Item.CallID,
				Name:   event.Item.Name,
			})
		}
	case "response.output_item.done":
		a.processOutputItemDone(event)
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if event.Delta != "" {
			if idx, ok := a.outputIndexToFuncIdx[event.OutputIndex]; ok {
				a.appendOutput(&a.funcCalls[idx].Args, event.Delta)
				a.funcCalls[idx].HadDelta = true
			}
		}
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		if idx, ok := a.outputIndexToFuncIdx[event.OutputIndex]; ok && !a.funcCalls[idx].HadDelta {
			value := event.Arguments
			if event.Type == "response.custom_tool_call_input.done" {
				value = event.Input
			}
			a.appendOutput(&a.funcCalls[idx].Args, value)
			if a.err == nil && value != "" {
				a.funcCalls[idx].HadDelta = true
			}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		a.textualOutputHadDelta[responsesTextKey(event)] = true
		a.appendOutput(&a.reasoning, event.Delta)
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		key := responsesTextKey(event)
		if !a.textualOutputHadDelta[key] {
			a.appendOutput(&a.reasoning, event.Text)
			if a.err == nil && event.Text != "" {
				a.textualOutputHadDelta[key] = true
			}
		}
	}
	return a.err
}

func isBufferedResponsesOutputItem(itemType string) bool {
	switch itemType {
	case "message", "reasoning", "function_call", "custom_tool_call", "image_generation_call":
		return true
	default:
		return false
	}
}

func (a *BufferedResponseAccumulator) processOutputItemDone(event *ResponsesStreamEvent) {
	if event.Item == nil || !isBufferedResponsesOutputItem(event.Item.Type) {
		return
	}
	if !a.trackedOutputIndexes[event.OutputIndex] {
		if err := a.retainOutput(0, 1); err != nil {
			return
		}
		a.trackedOutputIndexes[event.OutputIndex] = true
	}
	switch event.Item.Type {
	case "function_call", "custom_tool_call":
		idx, ok := a.outputIndexToFuncIdx[event.OutputIndex]
		if !ok {
			if len(event.Item.CallID) > maxBufferedResponsesRetainedMetadata || len(event.Item.Name) > maxBufferedResponsesRetainedMetadata {
				a.err = fmt.Errorf("responses buffered function-call metadata exceeds %d bytes", maxBufferedResponsesRetainedMetadata)
				return
			}
			idx = len(a.funcCalls)
			a.outputIndexToFuncIdx[event.OutputIndex] = idx
			a.funcCalls = append(a.funcCalls, bufferedFuncCall{Type: event.Item.Type, CallID: event.Item.CallID, Name: event.Item.Name})
		}
		if a.funcCalls[idx].HadDelta {
			return
		}
		value := event.Item.Arguments
		if event.Item.Type == "custom_tool_call" {
			value = event.Item.Input
		}
		a.appendOutput(&a.funcCalls[idx].Args, value)
		if a.err == nil && value != "" {
			a.funcCalls[idx].HadDelta = true
		}
	case "message":
		for index, part := range event.Item.Content {
			key := responsesTextStreamKey{OutputIndex: event.OutputIndex, ContentIndex: index}
			switch part.Type {
			case "output_text":
				key.Type = "response.output_text"
				if !a.textualOutputHadDelta[key] {
					a.appendOutput(&a.text, part.Text)
					if a.err == nil && part.Text != "" {
						a.textualOutputHadDelta[key] = true
					}
				}
			case "refusal":
				key.Type = "response.refusal"
				if !a.textualOutputHadDelta[key] {
					a.appendOutput(&a.refusal, part.Refusal)
					if a.err == nil && part.Refusal != "" {
						a.textualOutputHadDelta[key] = true
					}
				}
			}
		}
	case "reasoning":
		for index, summary := range event.Item.Summary {
			key := responsesTextStreamKey{Type: "response.reasoning_summary_text", OutputIndex: event.OutputIndex, SummaryIndex: index}
			if summary.Type == "summary_text" && !a.textualOutputHadDelta[key] {
				a.appendOutput(&a.reasoning, summary.Text)
				if a.err == nil && summary.Text != "" {
					a.textualOutputHadDelta[key] = true
				}
			}
		}
		for index, part := range event.Item.Content {
			key := responsesTextStreamKey{Type: "response.reasoning_text", OutputIndex: event.OutputIndex, ContentIndex: index}
			if part.Type == "reasoning_text" && !a.textualOutputHadDelta[key] {
				a.appendOutput(&a.reasoning, part.Text)
				if a.err == nil && part.Text != "" {
					a.textualOutputHadDelta[key] = true
				}
			}
		}
	}
}

func (a *BufferedResponseAccumulator) appendOutput(builder *strings.Builder, fragment string) {
	if fragment == "" || a.err != nil {
		return
	}
	if err := a.retainOutput(len(fragment), 0); err != nil {
		return
	}
	_, _ = builder.WriteString(fragment)
}

func (a *BufferedResponseAccumulator) retainOutput(byteCount int, itemCount int) error {
	if a == nil {
		return nil
	}
	if a.err != nil {
		return a.err
	}
	if itemCount < 0 || byteCount < 0 || a.retainedItems+itemCount > maxBufferedResponsesFunctionCalls {
		a.err = fmt.Errorf("responses buffered output exceeds %d retained items", maxBufferedResponsesFunctionCalls)
		return a.err
	}
	if a.retainedOutputBytes > 0 && byteCount > maxBufferedResponsesRetainedOutputBytes-a.retainedOutputBytes {
		a.err = fmt.Errorf("responses buffered output exceeds %d-byte retained-state limit", maxBufferedResponsesRetainedOutputBytes)
		return a.err
	}
	a.retainedOutputBytes += byteCount
	a.retainedItems += itemCount
	return nil
}

// RetainExternalOutput reserves the same attempt-local budget for output that
// is reconstructed outside the typed accumulator, such as image items.
func (a *BufferedResponseAccumulator) RetainExternalOutput(byteCount int, itemCount int) error {
	return a.retainOutput(byteCount, itemCount)
}

// Err reports an attempt-local retention failure.
func (a *BufferedResponseAccumulator) Err() error {
	if a == nil {
		return nil
	}
	return a.err
}

// HasContent reports whether any content has been accumulated.
func (a *BufferedResponseAccumulator) HasContent() bool {
	return a.text.Len() > 0 || a.refusal.Len() > 0 || len(a.funcCalls) > 0 || a.reasoning.Len() > 0
}

// BuildOutput constructs a []ResponsesOutput from the accumulated delta
// content. The order matches what ResponsesToChatCompletions expects:
// reasoning → message → function_calls.
func (a *BufferedResponseAccumulator) BuildOutput() []ResponsesOutput {
	var out []ResponsesOutput

	if a.reasoning.Len() > 0 {
		out = append(out, ResponsesOutput{
			Type: "reasoning",
			Summary: []ResponsesSummary{{
				Type: "summary_text",
				Text: a.reasoning.String(),
			}},
		})
	}

	if a.text.Len() > 0 || a.refusal.Len() > 0 {
		content := make([]ResponsesContentPart, 0, 2)
		if a.text.Len() > 0 {
			content = append(content, ResponsesContentPart{Type: "output_text", Text: a.text.String()})
		}
		if a.refusal.Len() > 0 {
			content = append(content, ResponsesContentPart{Type: "refusal", Refusal: a.refusal.String()})
		}
		out = append(out, ResponsesOutput{
			Type:    "message",
			Role:    "assistant",
			Content: content,
		})
	}

	for i := range a.funcCalls {
		item := ResponsesOutput{
			Type:   a.funcCalls[i].Type,
			CallID: a.funcCalls[i].CallID,
			Name:   a.funcCalls[i].Name,
		}
		if item.Type == "custom_tool_call" {
			item.Input = a.funcCalls[i].Args.String()
		} else {
			item.Arguments = a.funcCalls[i].Args.String()
		}
		out = append(out, item)
	}

	return out
}

// SupplementResponseOutput fills resp.Output from accumulated delta content
// when the terminal event delivered an empty output array. If resp.Output is
// already populated, this is a no-op (preserves backward compatibility).
func (a *BufferedResponseAccumulator) SupplementResponseOutput(resp *ResponsesResponse) {
	if resp == nil || len(resp.Output) > 0 || a.Err() != nil {
		return
	}
	if !a.HasContent() {
		return
	}
	resp.Output = a.BuildOutput()
}

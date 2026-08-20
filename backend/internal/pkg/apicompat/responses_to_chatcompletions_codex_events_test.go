package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// custom_tool_call（custom/freeform 工具，如新版 apply_patch）应像 function_call 一样
// 注册为工具调用，其 *_input.delta 增量映射到正确的工具索引。
func TestResponsesEventToChatChunks_CustomToolCallInputDelta(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:   "custom_tool_call",
			CallID: "call_patch",
			Name:   "apply_patch",
		},
	}, state)
	require.Len(t, chunks, 1)
	require.Len(t, chunks[0].Choices[0].Delta.ToolCalls, 1)
	tc := chunks[0].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_patch", tc.ID)
	assert.Equal(t, "apply_patch", tc.Function.Name)

	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 1,
		Delta:       "*** Begin Patch",
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index)
	assert.Equal(t, "*** Begin Patch", tc.Function.Arguments)
}

// 原始推理文本增量 reasoning_text.delta 应像 reasoning_summary_text.delta 一样
// 映射为 reasoning_content。
func TestResponsesEventToChatChunks_ReasoningTextDelta(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "thinking step",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "thinking step", *chunks[0].Choices[0].Delta.ReasoningContent)
}

// 缓冲（非流式）累加器同样需识别两类新事件。
func TestBufferedResponseAccumulator_CodexEvents(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	requireBufferedResponseEvent(t, acc, &ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "custom_tool_call", CallID: "c1", Name: "apply_patch"},
	})
	requireBufferedResponseEvent(t, acc, &ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 0,
		Delta:       "patch-body",
	})
	requireBufferedResponseEvent(t, acc, &ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "raw-reasoning",
	})
	require.True(t, acc.HasContent())
}

func TestNonStreamingResponsesConvertersPreserveNativeCustomToolCalls(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_custom",
		Status: "completed",
		Output: []ResponsesOutput{{
			Type: "custom_tool_call", CallID: "call_custom", Name: "exec", Input: "pwd",
		}},
	}

	chat := ResponsesToChatCompletions(resp, "gpt-5-codex")
	require.Len(t, chat.Choices, 1)
	require.Len(t, chat.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "call_custom", chat.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, "exec", chat.Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "pwd", chat.Choices[0].Message.ToolCalls[0].Function.Arguments)

	anthropic := ResponsesToAnthropic(resp, "gpt-5-codex")
	require.Len(t, anthropic.Content, 1)
	require.Equal(t, "tool_use", anthropic.Content[0].Type)
	require.Equal(t, "exec", anthropic.Content[0].Name)
	require.JSONEq(t, `{"input":"pwd"}`, string(anthropic.Content[0].Input))
}

func TestBufferedResponseAccumulatorPreservesNativeCustomToolShape(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "custom_tool_call", CallID: "call_custom", Name: "exec"},
	}))
	require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 0,
		Delta:       "pwd",
	}))

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	require.Equal(t, "custom_tool_call", output[0].Type)
	require.Equal(t, "pwd", output[0].Input)
	require.Empty(t, output[0].Arguments)
}

func TestResponsesToChatAndBufferedAccumulatorPreserveRefusal(t *testing.T) {
	resp := &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{
		Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "refusal", Refusal: "blocked"}},
	}}}
	chat := ResponsesToChatCompletions(resp, "gpt-5")
	require.Len(t, chat.Choices, 1)
	require.NotNil(t, chat.Choices[0].Message.Refusal)
	require.Equal(t, "blocked", *chat.Choices[0].Message.Refusal)

	acc := NewBufferedResponseAccumulator()
	require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.refusal.delta", Delta: "blocked"}))
	output := acc.BuildOutput()
	require.Len(t, output, 1)
	require.Equal(t, []ResponsesContentPart{{Type: "refusal", Refusal: "blocked"}}, output[0].Content)
}

func TestBufferedResponseAccumulatorBoundsCrossEventRetainedState(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
		Type:  "response.output_text.delta",
		Delta: strings.Repeat("x", maxBufferedResponsesRetainedOutputBytes+1),
	}))
	require.Error(t, acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "y"}))

	toolAcc := NewBufferedResponseAccumulator()
	for i := 0; i < maxBufferedResponsesFunctionCalls; i++ {
		require.NoError(t, toolAcc.ProcessEvent(&ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: i,
			Item:        &ResponsesOutput{Type: "function_call", CallID: "c", Name: "f"},
		}))
	}
	require.Error(t, toolAcc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: maxBufferedResponsesFunctionCalls,
		Item:        &ResponsesOutput{Type: "function_call", CallID: "overflow", Name: "f"},
	}))
}

func TestResponsesCustomToolAnthropicInputIsValidJSON(t *testing.T) {
	resp := ResponsesToAnthropic(&ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{
		Type: "custom_tool_call", CallID: "call_1", Name: "exec", Input: "line one\nline two",
	}}}, "gpt-5-codex")
	require.Len(t, resp.Content, 1)
	require.True(t, json.Valid(resp.Content[0].Input))
}

func TestNonStreamingResponsesConvertersPreserveReasoningTextContent(t *testing.T) {
	resp := &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{
		Type:    "reasoning",
		Summary: []ResponsesSummary{{Type: "summary_text", Text: "summary"}},
		Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "private thought"}},
	}}}

	chat := ResponsesToChatCompletions(resp, "gpt-5")
	require.Equal(t, "summaryprivate thought", chat.Choices[0].Message.ReasoningContent)

	anthropic := ResponsesToAnthropic(resp, "gpt-5")
	require.Len(t, anthropic.Content, 1)
	require.Equal(t, "thinking", anthropic.Content[0].Type)
	require.Equal(t, "summaryprivate thought", anthropic.Content[0].Thinking)
}

func TestResponsesToChatStreamPreservesDoneOnlyToolInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		itemType  string
		doneType  string
		arguments string
		input     string
	}{
		{name: "function", itemType: "function_call", doneType: "response.function_call_arguments.done", arguments: `{"path":"a"}`},
		{name: "custom", itemType: "custom_tool_call", doneType: "response.custom_tool_call_input.done", input: "pwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			state.SentRole = true
			_ = ResponsesEventToChatChunks(&ResponsesStreamEvent{
				Type: "response.output_item.added", OutputIndex: 0,
				Item: &ResponsesOutput{Type: tc.itemType, CallID: "call_1", Name: "tool"},
			}, state)
			chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
				Type: tc.doneType, OutputIndex: 0, Arguments: tc.arguments, Input: tc.input,
			}, state)
			require.Len(t, chunks, 1)
			require.Equal(t, tc.arguments+tc.input, chunks[0].Choices[0].Delta.ToolCalls[0].Function.Arguments)
		})
	}
}

func TestResponsesToAnthropicStreamPreservesDoneOnlyCustomInput(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	_ = ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.output_item.added", OutputIndex: 0,
		Item: &ResponsesOutput{Type: "custom_tool_call", CallID: "call_1", Name: "exec"},
	}, state)
	events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.custom_tool_call_input.done", OutputIndex: 0, Input: "pwd",
	}, state)
	require.NotEmpty(t, events)
	require.JSONEq(t, `{"input":"pwd"}`, events[0].Delta.PartialJSON)
}

func TestBufferedAccumulatorPreservesDoneOnlyToolInputs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		itemType  string
		doneType  string
		arguments string
		input     string
	}{
		{name: "function", itemType: "function_call", doneType: "response.function_call_arguments.done", arguments: `{"path":"a"}`},
		{name: "custom", itemType: "custom_tool_call", doneType: "response.custom_tool_call_input.done", input: "pwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := NewBufferedResponseAccumulator()
			require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
				Type: "response.output_item.added", OutputIndex: 0,
				Item: &ResponsesOutput{Type: tc.itemType, CallID: "call_1", Name: "tool"},
			}))
			require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
				Type: tc.doneType, OutputIndex: 0, Arguments: tc.arguments, Input: tc.input,
			}))
			output := acc.BuildOutput()
			require.Len(t, output, 1)
			require.Equal(t, tc.arguments, output[0].Arguments)
			require.Equal(t, tc.input, output[0].Input)
		})
	}
}

func TestResponsesCompatPreservesDoneOnlyTextualOutput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		doneType  string
		text      string
		refusal   string
		wantField string
	}{
		{name: "text", doneType: "response.output_text.done", text: "hello", wantField: "text"},
		{name: "refusal", doneType: "response.refusal.done", refusal: "blocked", wantField: "refusal"},
		{name: "reasoning", doneType: "response.reasoning_text.done", text: "thought", wantField: "reasoning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			state.SentRole = true
			chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
				Type: tc.doneType, OutputIndex: 0, ContentIndex: 0, Text: tc.text, Refusal: tc.refusal,
			}, state)
			require.Len(t, chunks, 1)
			delta := chunks[0].Choices[0].Delta
			switch tc.wantField {
			case "text":
				require.NotNil(t, delta.Content)
				require.Equal(t, tc.text, *delta.Content)
			case "refusal":
				require.NotNil(t, delta.Refusal)
				require.Equal(t, tc.refusal, *delta.Refusal)
			case "reasoning":
				require.NotNil(t, delta.ReasoningContent)
				require.Equal(t, tc.text, *delta.ReasoningContent)
			}

			acc := NewBufferedResponseAccumulator()
			require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{
				Type: tc.doneType, OutputIndex: 0, ContentIndex: 0, Text: tc.text, Refusal: tc.refusal,
			}))
			require.NotEmpty(t, acc.BuildOutput())
		})
	}
}

func TestResponsesCompatPreservesAuthoritativeOutputItemDone(t *testing.T) {
	done := &ResponsesStreamEvent{
		Type: "response.output_item.done", OutputIndex: 0,
		Item: &ResponsesOutput{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "hello"}}},
	}
	chatState := NewResponsesEventToChatState()
	chatState.SentRole = true
	chatChunks := ResponsesEventToChatChunks(done, chatState)
	require.Len(t, chatChunks, 1)
	require.Equal(t, "hello", *chatChunks[0].Choices[0].Delta.Content)

	anthropicState := NewResponsesEventToAnthropicState()
	anthropicEvents := ResponsesEventToAnthropicEvents(done, anthropicState)
	require.Len(t, anthropicEvents, 3)
	require.Equal(t, "hello", anthropicEvents[1].Delta.Text)

	acc := NewBufferedResponseAccumulator()
	require.NoError(t, acc.ProcessEvent(done))
	output := acc.BuildOutput()
	require.Len(t, output, 1)
	require.Equal(t, "hello", output[0].Content[0].Text)
}

func TestResponsesCompatDoneOnlyLifecycleDoesNotDuplicateAuthoritativeItem(t *testing.T) {
	for _, tc := range []struct {
		name     string
		done     ResponsesStreamEvent
		item     ResponsesOutput
		wantText string
		wantArgs string
	}{
		{name: "text", done: ResponsesStreamEvent{Type: "response.output_text.done", Text: "hello"}, item: ResponsesOutput{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "hello"}}}, wantText: "hello"},
		{name: "refusal", done: ResponsesStreamEvent{Type: "response.refusal.done", Refusal: "blocked"}, item: ResponsesOutput{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "refusal", Refusal: "blocked"}}}, wantText: "blocked"},
		{name: "reasoning", done: ResponsesStreamEvent{Type: "response.reasoning_text.done", Text: "thought"}, item: ResponsesOutput{Type: "reasoning", Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "thought"}}}, wantText: "thought"},
		{name: "function", done: ResponsesStreamEvent{Type: "response.function_call_arguments.done", Arguments: `{"x":1}`}, item: ResponsesOutput{Type: "function_call", CallID: "call_1", Name: "tool", Arguments: `{"x":1}`}, wantArgs: `{"x":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := NewBufferedResponseAccumulator()
			if tc.item.Type == "function_call" {
				require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: tc.item.Type, CallID: tc.item.CallID, Name: tc.item.Name}}))
			}
			tc.done.OutputIndex = 0
			tc.done.ContentIndex = 0
			require.NoError(t, acc.ProcessEvent(&tc.done))
			require.NoError(t, acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &tc.item}))
			output := acc.BuildOutput()
			require.NotEmpty(t, output)
			if tc.wantArgs != "" {
				require.Equal(t, tc.wantArgs, output[0].Arguments)
			} else if tc.item.Type == "reasoning" {
				require.Equal(t, tc.wantText, output[0].Summary[0].Text)
			} else if tc.item.Content[0].Type == "refusal" {
				require.Equal(t, tc.wantText, output[0].Content[0].Refusal)
			} else {
				require.Equal(t, tc.wantText, output[0].Content[0].Text)
			}
		})
	}

	state := NewResponsesEventToAnthropicState()
	_ = ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 0, Text: "hello"}, state)
	events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.output_item.done", OutputIndex: 0,
		Item: &ResponsesOutput{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "hello"}}},
	}, state)
	for _, event := range events {
		if event.Delta != nil {
			require.NotEqual(t, "hello", event.Delta.Text)
		}
	}
}

package apicompat

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func collectStreamEvents(t *testing.T, chunks []string) []ResponsesStreamEvent {
	t.Helper()
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4-pro")
	var events []ResponsesStreamEvent
	for _, payload := range chunks {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		converted, err := ChatCompletionsChunkToResponsesEvents(&chunk, state)
		require.NoError(t, err)
		events = append(events, converted...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	return events
}

func TestChatCompletionsToResponsesToolArgumentsAggregateLinearly(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-test")
	state.CustomTools = map[string]bool{"exec": true}
	first := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: ChatFunctionCall{
			Name:      "exec",
			Arguments: `{"input":"`,
		},
	}}}}}}
	_, err := ChatCompletionsChunkToResponsesEvents(first, state)
	require.NoError(t, err)

	fragment := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{
		Function: ChatFunctionCall{Arguments: "x"},
	}}}}}}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 8192 {
		_, err = ChatCompletionsChunkToResponsesEvents(fragment, state)
		require.NoError(t, err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
	closing := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{
		Function: ChatFunctionCall{Arguments: `"}`},
	}}}}}}
	_, err = ChatCompletionsChunkToResponsesEvents(closing, state)
	require.NoError(t, err)
	completed := FinalizeChatCompletionsResponsesStream(state)
	require.NotEmpty(t, completed)
	last := completed[len(completed)-1]
	require.Equal(t, "response.completed", last.Type)
	require.Len(t, last.Response.Output, 1)
	require.Len(t, last.Response.Output[0].Input, 8192)
}

func TestChatCompletionsToResponsesRejectsOversizedRetainedToolArguments(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-test")
	state.CustomTools = map[string]bool{"exec": true}
	chunk := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{
		ID: "call-1",
		Function: ChatFunctionCall{
			Arguments: strings.Repeat("x", (8<<20)+1),
		},
	}}}}}}

	events, err := ChatCompletionsChunkToResponsesEvents(chunk, state)
	require.ErrorContains(t, err, "tool arguments exceed")
	require.Empty(t, events)
}

func TestChatCompletionsToResponsesRejectsOversizedRetainedSemanticOutput(t *testing.T) {
	for _, kind := range []string{"content", "reasoning", "refusal"} {
		t.Run(kind, func(t *testing.T) {
			state := NewChatCompletionsToResponsesStreamState("gpt-test")
			prefix := "prefix"
			value := strings.Repeat("x", (8<<20)+1)
			firstDelta := ChatDelta{}
			delta := ChatDelta{}
			switch kind {
			case "content":
				firstDelta.Content = &prefix
				delta.Content = &value
			case "reasoning":
				firstDelta.ReasoningContent = &prefix
				delta.ReasoningContent = &value
			case "refusal":
				firstDelta.Refusal = &prefix
				delta.Refusal = &value
			}
			_, err := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{
				Choices: []ChatChunkChoice{{Delta: firstDelta}},
			}, state)
			require.NoError(t, err)

			events, err := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{
				Choices: []ChatChunkChoice{{Delta: delta}},
			}, state)
			require.ErrorContains(t, err, "semantic output exceeds")
			require.Empty(t, events)
		})
	}
}

func TestChatCompletionsToResponsesAllowsSingleSemanticFrameBeyondRetainedGuard(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-test")
	value := strings.Repeat("x", (8<<20)+1024)
	events, err := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{
		Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &value}}},
	}, state)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	require.Equal(t, len(value), state.Text.Len())
}

func TestStream_RefusalPreservesResponsesRefusalLifecycle(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","refusal":"policy "}}]}`,
		`{"choices":[{"index":0,"delta":{"refusal":"blocked"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`,
	})

	var sawAdded, sawDelta, sawDone, sawItemDone bool
	for _, event := range events {
		require.NotEqual(t, "response.output_text.delta", event.Type)
		switch event.Type {
		case "response.content_part.added":
			if event.Part != nil && event.Part.Type == "refusal" {
				sawAdded = true
			}
		case "response.refusal.delta":
			sawDelta = true
		case "response.refusal.done":
			sawDone = true
			require.Equal(t, "policy blocked", event.Refusal)
		case "response.output_item.done":
			if event.Item != nil && event.Item.Type == "message" {
				sawItemDone = true
				require.Equal(t, []ResponsesContentPart{{Type: "refusal", Refusal: "policy blocked"}}, event.Item.Content)
			}
		case "response.completed":
			require.NotNil(t, event.Response)
			require.Len(t, event.Response.Output, 1)
			require.Equal(t, []ResponsesContentPart{{Type: "refusal", Refusal: "policy blocked"}}, event.Response.Output[0].Content)
		}
	}
	require.True(t, sawAdded)
	require.True(t, sawDelta)
	require.True(t, sawDone)
	require.True(t, sawItemDone)
}

func TestStream_MixedTextAndRefusalPreservesArrivalOrder(t *testing.T) {
	for name, chunks := range map[string][]string{
		"text then refusal": {
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"visible"}}]}`,
			`{"choices":[{"index":0,"delta":{"refusal":"blocked"}}]}`,
		},
		"refusal then text": {
			`{"choices":[{"index":0,"delta":{"role":"assistant","refusal":"blocked"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"visible"}}]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			events := collectStreamEvents(t, append(chunks, `{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`))
			var completed *ResponsesResponse
			for _, event := range events {
				if event.Type == "response.completed" {
					completed = event.Response
				}
			}
			require.NotNil(t, completed)
			require.Len(t, completed.Output, 1)
			require.Len(t, completed.Output[0].Content, 2)
			if name == "text then refusal" {
				require.Equal(t, []ResponsesContentPart{{Type: "output_text", Text: "visible"}, {Type: "refusal", Refusal: "blocked"}}, completed.Output[0].Content)
			} else {
				require.Equal(t, []ResponsesContentPart{{Type: "refusal", Refusal: "blocked"}, {Type: "output_text", Text: "visible"}}, completed.Output[0].Content)
			}
		})
	}
}

// TestStream_ReasoningOpensItemBeforeDelta guards the bug where a strict client
// (Codex) drops reasoning deltas that reference an item not yet opened.
func TestStream_ReasoningOpensItemBeforeDelta(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	open := map[int]string{} // output_index -> item type
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			require.NotNil(t, e.Item)
			open[e.OutputIndex] = e.Item.Type
		case "response.reasoning_summary_text.delta":
			require.Equalf(t, "reasoning", open[e.OutputIndex], "reasoning delta before its item was opened")
		case "response.output_text.delta":
			require.Equalf(t, "message", open[e.OutputIndex], "text delta before its item was opened")
		}
	}
}

func TestStream_ReasoningOnlySynthesizesVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking before final"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	open := map[int]string{}
	var sawTextDelta, sawTextDone, sawMessageDone bool
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			require.NotNil(t, e.Item)
			open[e.OutputIndex] = e.Item.Type
		case "response.output_text.delta":
			sawTextDelta = true
			require.Equalf(t, "message", open[e.OutputIndex], "fallback text delta before its item was opened")
			require.Equal(t, "thinking before final", e.Delta)
		case "response.output_text.done":
			sawTextDone = true
			require.Equal(t, "thinking before final", e.Text)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "message" {
				sawMessageDone = true
				require.Equal(t, "thinking before final", e.Item.Content[0].Text)
			}
		case "response.completed":
			require.NotNil(t, e.Response)
			require.Equal(t, "incomplete", e.Response.Status)
			require.NotNil(t, e.Response.IncompleteDetails)
			require.Equal(t, "max_output_tokens", e.Response.IncompleteDetails.Reason)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "message", e.Response.Output[1].Type)
			require.Equal(t, "thinking before final", e.Response.Output[1].Content[0].Text)
		}
	}
	require.True(t, sawTextDelta, "reasoning-only stream must produce visible text delta")
	require.True(t, sawTextDone, "reasoning-only stream must close visible text part")
	require.True(t, sawMessageDone, "reasoning-only stream must close synthesized message item")
}

func TestStream_ReasoningOnlyBlankDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"   "}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "message", e.Response.Output[1].Type)
			require.Equal(t, "", e.Response.Output[1].Content[0].Text)
		}
	}
}

func TestStream_ReasoningThenContentDoesNotDuplicateFallbackText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"private plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"final answer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	var textDeltas []string
	for _, e := range events {
		switch e.Type {
		case "response.output_text.delta":
			textDeltas = append(textDeltas, e.Delta)
		case "response.completed":
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "private plan", e.Response.Output[0].Summary[0].Text)
			require.Equal(t, "final answer", e.Response.Output[1].Content[0].Text)
		}
	}
	require.Equal(t, []string{"final answer"}, textDeltas)
}

func TestStream_ReasoningThenToolCallDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"call a tool"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "function_call", e.Response.Output[1].Type)
		}
	}
}

// TestStream_ToolCallLifecycleComplete guards that a tool call is fully closed
// (function_call_arguments.done + output_item.done with full arguments), which
// codex needs to execute the call.
func TestStream_ToolCallLifecycleComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	var sawAdded, sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawAdded = true
			}
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
				require.Equal(t, "call_a", e.Item.CallID)
			}
		}
	}
	require.True(t, sawAdded, "function_call output_item.added missing")
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
}

// TestStream_ToolCallArgumentsInFirstChunkNotDoubled guards the GLM/Zhipu shape
// where a single tool_call delta chunk carries id+name+arguments together.
// Earlier code copied the whole tool_call (including arguments) into state and
// then accumulated the same chunk's arguments again, producing a doubled,
// invalid JSON like {"cmd":"ls"}{"cmd":"ls"} that breaks Codex tool parsing
// ("trailing characters").
func TestStream_ToolCallArgumentsInFirstChunkNotDoubled(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var argsDelta strings.Builder
	var sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.function_call_arguments.delta":
			_, _ = argsDelta.WriteString(e.Delta)
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
			}
		}
	}
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
	// Accumulated deltas must equal the final arguments exactly (no duplication).
	require.Equal(t, `{"cmd":"ls"}`, argsDelta.String())
}

// TestStream_SSEWireComplete drives the full stream through SSE encoding and
// asserts the function_call events carry complete fields on the wire.
func TestStream_SSEWireComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var addedLine string
	for _, e := range events {
		sse, err := ResponsesEventToSSE(e)
		require.NoError(t, err)
		if e.Type == "response.output_item.added" && e.Item != nil && e.Item.Type == "function_call" {
			addedLine = sse
		}
	}
	require.NotEmpty(t, addedLine)
	// The function_call added event must carry arguments:"" on the wire.
	require.True(t, strings.Contains(addedLine, `"arguments":""`), "added line missing arguments: %s", addedLine)
	require.Contains(t, addedLine, `"call_id":"call_a"`)
}

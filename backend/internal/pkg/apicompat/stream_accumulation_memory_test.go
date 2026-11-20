package apicompat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamAccumulationResponsesChatToolIsolation(t *testing.T) {
	state := NewResponsesEventToChatState()
	for _, idx := range []int{2, 7} {
		ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: idx, Item: &ResponsesOutput{Type: "function_call", CallID: fmt.Sprintf("call_%d", idx), Name: "tool"}}, state)
	}
	appendDelta := func(idx int, delta, want string) {
		t.Helper()
		ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: idx, Delta: delta}, state)
		require.Equal(t, want, state.OutputIndexToArguments[idx])
	}
	state.OutputIndexToArguments[2] = `{"a":`
	appendDelta(2, "1", `{"a":1`)
	firstSnapshot := state.OutputIndexToArguments[2]
	appendDelta(7, `{"b":`, `{"b":`)
	appendDelta(2, "}", `{"a":1}`)
	appendDelta(7, "2}", `{"b":2}`)
	require.Equal(t, `{"a":1`, firstSnapshot)
	require.Equal(t, `{"a":1}`, state.OutputIndexToArguments[2])

	ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.function_call_arguments.done", OutputIndex: 2, Arguments: `{"a":1} `}, state)
	require.Equal(t, `{"a":1} `, state.OutputIndexToArguments[2])
	appendDelta(2, "\n", "{\"a\":1} \n")
	state.OutputIndexToArguments[2] = "external"
	appendDelta(2, "!", "external!")
	delete(state.OutputIndexToArguments, 2)
	appendDelta(2, "fresh", "fresh")
	require.Equal(t, `{"b":2}`, state.OutputIndexToArguments[7])
}

func streamAccumulationChatToolChunk(idx int, name, arguments string) *ChatCompletionsChunk {
	return &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{Index: &idx, ID: fmt.Sprintf("call_%d", idx), Type: "function", Function: ChatFunctionCall{Name: name, Arguments: arguments}}}}}}}
}

func TestStreamAccumulationChatResponsesToolIsolation(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("model")
	appendDelta := func(idx int, name, delta, want string) {
		t.Helper()
		ChatCompletionsChunkToResponsesEvents(streamAccumulationChatToolChunk(idx, name, delta), state)
		require.Equal(t, want, state.ToolCalls[idx].Function.Arguments)
	}
	appendDelta(0, "first", `{"a":`, `{"a":`)
	firstSnapshot := state.ToolCalls[0].Function.Arguments
	appendDelta(1, "second", `{"b":`, `{"b":`)
	appendDelta(0, "", "1}", `{"a":1}`)
	appendDelta(1, "", "2}", `{"b":2}`)
	require.Equal(t, `{"a":`, firstSnapshot)
	require.Equal(t, `{"a":1}`, state.ToolCalls[0].Function.Arguments)

	state.ToolCalls[0].Function.Arguments = `{"c":3`
	appendDelta(0, "", "}", `{"c":3}`)
	state.ToolCalls[0].Function.Arguments = ""
	appendDelta(0, "", "{}", "{}")
	oldCall := state.ToolCalls[0]
	delete(state.ToolCalls, 0)
	appendDelta(0, "replacement", `{"new":`, `{"new":`)
	appendDelta(0, "", "4}", `{"new":4}`)
	require.Equal(t, "{}", oldCall.Function.Arguments, "replacing an indexed call must leave the old call immutable")
	require.Equal(t, `{"b":2}`, state.ToolCalls[1].Function.Arguments)
	require.NoError(t, state.ValidateToolCallArguments())
}

func TestStreamAccumulationDeferredAnthropicTools(t *testing.T) {
	state := NewChatCompletionsToAnthropicStreamState("model")
	var events []AnthropicStreamEvent
	feed := func(idx int, name, args string) {
		events = append(events, ChatCompletionsChunkToAnthropicEvents(streamAccumulationChatToolChunk(idx, name, args), state)...)
	}
	feed(0, "", `{"a":`)
	feed(1, "", `{"b":`)
	feed(0, "", "1")
	feed(1, "", "2")
	feed(0, "first", "}")
	feed(1, "second", "}")
	events = append(events, FinalizeChatCompletionsAnthropicStream(state)...)
	blocks := assembleToolUseBlocks(events)
	require.Len(t, blocks, 2)
	require.Equal(t, "first", blocks[0].Name)
	require.Equal(t, `{"a":1}`, blocks[0].Input)
	require.Equal(t, "second", blocks[1].Name)
	require.Equal(t, `{"b":2}`, blocks[1].Input)
	require.Empty(t, FinalizeChatCompletionsAnthropicStream(state))
}

var streamAccumulationBenchmarkSink string

// ReportAllocs measures cumulative allocation traffic, not retained heap/RSS.
// A fixed 64-byte delta makes total-size scaling expose repeated prefix copies.
func BenchmarkStreamAccumulationAnthropic64ByteDeltas(b *testing.B) {
	for _, tc := range []struct {
		name     string
		delta    func(string) *AnthropicDelta
		retained func(*AnthropicEventToResponsesState) string
	}{
		{"text", func(v string) *AnthropicDelta { return &AnthropicDelta{Type: "text_delta", Text: v} }, func(s *AnthropicEventToResponsesState) string { return s.TextAccum.String() }},
		{"thinking", func(v string) *AnthropicDelta { return &AnthropicDelta{Type: "thinking_delta", Thinking: v} }, func(s *AnthropicEventToResponsesState) string { return s.CurrentSummary.String() }},
		{"arguments", func(v string) *AnthropicDelta { return &AnthropicDelta{Type: "input_json_delta", PartialJSON: v} }, func(s *AnthropicEventToResponsesState) string { return s.CurrentArgs.String() }},
		{"signature", func(v string) *AnthropicDelta { return &AnthropicDelta{Type: "signature_delta", Signature: v} }, func(s *AnthropicEventToResponsesState) string { return s.CurrentThinking.Signature }},
	} {
		for _, size := range []int{32 << 10, 128 << 10, 512 << 10} {
			b.Run(fmt.Sprintf("%s/%dKiB", tc.name, size>>10), func(b *testing.B) {
				payload := strings.Repeat("x", 64)
				event := &AnthropicStreamEvent{Type: "content_block_delta", Delta: tc.delta(payload)}
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					state := NewAnthropicEventToResponsesState()
					state.PreserveThinkingSignatures = true
					for received := 0; received < size; received += len(payload) {
						AnthropicEventToResponsesEvents(event, state)
					}
					streamAccumulationBenchmarkSink = tc.retained(state)
					if len(streamAccumulationBenchmarkSink) != size {
						b.Fatalf("accumulated %d bytes, want %d", len(streamAccumulationBenchmarkSink), size)
					}
				}
			})
		}
	}
}

// Read's existing complete-JSON gate inspects the accumulated arguments on
// every delta. Keep the file_path string unfinished to measure that independent
// cost without changing when the converter emits sanitized tool input.
func BenchmarkStreamAccumulationRead64ByteDeltas(b *testing.B) {
	for _, size := range []int{32 << 10, 128 << 10, 512 << 10} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			const prefix = `{"file_path":"`
			firstEvent := &ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: prefix + strings.Repeat("x", 64-len(prefix))}
			nextEvent := &ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: strings.Repeat("x", 64)}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state := NewResponsesEventToAnthropicState()
				state.MessageStartSent = true
				state.ContentBlockOpen = true
				state.CurrentBlockType = "tool_use"
				state.CurrentToolName = "Read"
				state.OutputIndexToBlockIdx[0] = 0
				ResponsesEventToAnthropicEvents(firstEvent, state)
				for received := 64; received < size; received += 64 {
					ResponsesEventToAnthropicEvents(nextEvent, state)
				}
				streamAccumulationBenchmarkSink = state.CurrentToolArgs.String()
				if len(streamAccumulationBenchmarkSink) != size || state.CurrentToolHadDelta {
					b.Fatalf("unfinished Read arguments: bytes=%d, emitted=%v", len(streamAccumulationBenchmarkSink), state.CurrentToolHadDelta)
				}
			}
		})
	}
}

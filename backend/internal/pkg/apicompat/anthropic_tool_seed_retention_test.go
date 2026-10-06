package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestB8deToolSeedSharesRetainedOutputBudget(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	state.retainedOutputBytes = maxAnthropicToResponsesRetainedOutputBytes - 1
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:         "content_block_start",
		ContentBlock: &AnthropicContentBlock{Type: "tool_use", ID: "tool_1", Name: "lookup", Input: json.RawMessage(`{"query":"hello"}`)},
	}, state)
	require.Error(t, state.Err(), "initial tool input is retained before any delta or stop")
	require.Empty(t, events)
	require.Empty(t, state.PendingToolInput)
	require.Empty(t, FinalizeAnthropicResponsesStream(state))
}

func TestB8deToolSeedBudgetTransfersAndIsReleasedWhenReplaced(t *testing.T) {
	seed := `{"query":"hello"}`
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "seed only", true: "real delta replaces seed"}[replace], func(t *testing.T) {
			state := NewAnthropicEventToResponsesState()
			state.retainedOutputBytes = maxAnthropicToResponsesRetainedOutputBytes - len(seed)
			AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
				Type:         "content_block_start",
				ContentBlock: &AnthropicContentBlock{Type: "tool_use", ID: "tool_1", Name: "lookup", Input: json.RawMessage(seed)},
			}, state)
			require.NoError(t, state.Err())
			require.Equal(t, maxAnthropicToResponsesRetainedOutputBytes, state.retainedOutputBytes)
			want := seed
			if replace {
				want = "{}"
				AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
					Type: "content_block_delta", Delta: &AnthropicDelta{Type: "input_json_delta", PartialJSON: want},
				}, state)
			}
			events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_stop"}, state)
			require.NoError(t, state.Err(), "moving a seed to the argument builder must not count it twice")
			require.NotEmpty(t, events)
			require.Len(t, state.Outputs, 1)
			require.Equal(t, want, state.Outputs[0].Arguments)
			require.Equal(t, maxAnthropicToResponsesRetainedOutputBytes-len(seed)+len(want), state.retainedOutputBytes)
			require.Empty(t, state.PendingToolInput)
		})
	}
}

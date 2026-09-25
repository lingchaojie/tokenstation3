package apicompat

import "testing"

func TestSignedThinkingSharesRetainedOutputBudget(t *testing.T) {
	for _, kind := range []string{"initial_signature", "redacted_data", "signature_delta"} {
		t.Run(kind, func(t *testing.T) {
			state := NewAnthropicEventToResponsesState()
			state.PreserveThinkingSignatures = true
			state.retainedOutputBytes = maxAnthropicToResponsesRetainedOutputBytes - 1
			block := &AnthropicContentBlock{Type: "thinking"}
			switch kind {
			case "initial_signature":
				block.Signature = "xx"
			case "redacted_data":
				block.Type, block.Data = "redacted_thinking", "xx"
			}
			AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_start", ContentBlock: block}, state)
			if kind == "signature_delta" {
				AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_delta", Delta: &AnthropicDelta{Type: "signature_delta", Signature: "xx"}}, state)
			}
			if state.Err() == nil {
				t.Fatal("signed thinking exceeded the shared retained-output budget")
			}
			if out := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state); len(out) != 0 {
				t.Fatal("failed conversion must not emit a successful completed response")
			}
		})
	}
}

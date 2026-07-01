package apicompat

import (
	"encoding/json"
	"testing"
)

func TestResponsesToAnthropicRequest_MaxTokensExceedsThinkingBudget(t *testing.T) {
	cases := []struct {
		effort     string
		wantBudget int
	}{
		{"high", 10240},
		{"xhigh", 32768}, // xhigh → max → 32768
	}
	for _, tc := range cases {
		req := &ResponsesRequest{Model: "claude-opus-4-8", Input: json.RawMessage(`"hi"`), Reasoning: &ResponsesReasoning{Effort: tc.effort}}
		out, err := ResponsesToAnthropicRequest(req)
		if err != nil {
			t.Fatalf("effort %s: unexpected err: %v", tc.effort, err)
		}
		if out.Thinking == nil || out.Thinking.BudgetTokens != tc.wantBudget {
			t.Fatalf("effort %s: budget=%v want %d", tc.effort, out.Thinking, tc.wantBudget)
		}
		if out.MaxTokens <= out.Thinking.BudgetTokens {
			t.Fatalf("effort %s: max_tokens %d must exceed budget %d", tc.effort, out.MaxTokens, out.Thinking.BudgetTokens)
		}
	}
}

func TestResponsesToAnthropicRequest_DoesNotShrinkExplicitMaxTokens(t *testing.T) {
	big := 100000
	req := &ResponsesRequest{Model: "claude-opus-4-8", Input: json.RawMessage(`"hi"`), MaxOutputTokens: &big, Reasoning: &ResponsesReasoning{Effort: "xhigh"}}
	out, err := ResponsesToAnthropicRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.MaxTokens != big {
		t.Fatalf("max_tokens shrunk to %d, want %d", out.MaxTokens, big)
	}
}

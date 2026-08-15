package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestAppendRawJSON_DropsContentBlockStartPlaceholder(t *testing.T) {
	tests := []struct {
		name     string
		existing json.RawMessage
		fragment string
		want     string
	}{
		{name: "empty object placeholder", existing: json.RawMessage(`{}`), fragment: `{"cmd":"ls"}`, want: `{"cmd":"ls"}`},
		{name: "spaced empty object placeholder", existing: json.RawMessage(` { } `), fragment: `{"cmd":"ls"}`, want: `{"cmd":"ls"}`},
		{name: "partial prefix", existing: json.RawMessage(`{`), fragment: `"cmd":"ls"}`, want: `{"cmd":"ls"}`},
		{name: "real partial content", existing: json.RawMessage(`{"cmd":"l`), fragment: `s"}`, want: `{"cmd":"ls"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendRawJSON(tt.existing, tt.fragment)
			require.Equal(t, tt.want, string(got))
			require.True(t, json.Valid(got))
		})
	}
}

func TestBufferedResponsesToolInput_ReplacesStartPlaceholder(t *testing.T) {
	response := &apicompat.AnthropicResponse{}
	accumulator := &anthropicBufferedContentAccumulator{}
	accumulator.start(response, apicompat.AnthropicContentBlock{
		Type: "tool_use", Input: json.RawMessage(`{}`),
	})
	accumulator.delta(0, &apicompat.AnthropicDelta{
		Type: "input_json_delta", PartialJSON: `{"cmd":"ls"}`,
	})
	accumulator.materialize(response)

	require.Len(t, response.Content, 1)
	require.JSONEq(t, `{"cmd":"ls"}`, string(response.Content[0].Input))
}

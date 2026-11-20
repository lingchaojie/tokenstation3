package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBufferedResponsesFunctionMetadataSharesRetentionBudget(t *testing.T) {
	halfBudget := maxBufferedResponsesRetainedOutputBytes / 2
	for _, tc := range []struct {
		name        string
		setMetadata func(*ResponsesOutput, int)
	}{
		{"namespace", func(item *ResponsesOutput, size int) { item.Namespace = strings.Repeat("n", size) }},
		{"encrypted function args", func(item *ResponsesOutput, size int) {
			item.EncryptedFunctionArgs = json.RawMessage(`"` + strings.Repeat("a", size-2) + `"`)
		}},
		{"encrypted content", func(item *ResponsesOutput, size int) { item.EncryptedContent = strings.Repeat("c", size) }},
	} {
		t.Run(tc.name+" shares budget with text", func(t *testing.T) {
			accumulator := NewBufferedResponseAccumulator()
			metadata := &ResponsesOutput{Type: "function_call", CallID: "call_test", Name: "test"}
			tc.setMetadata(metadata, halfBudget)
			require.NoError(t, accumulator.ProcessEvent(&ResponsesStreamEvent{
				Type: "response.output_item.added", Item: metadata,
			}))
			output := accumulator.BuildOutput()
			require.Len(t, output, 1)
			require.Equal(t, metadata.Namespace, output[0].Namespace)
			require.Equal(t, metadata.EncryptedFunctionArgs, output[0].EncryptedFunctionArgs)
			require.Equal(t, metadata.EncryptedContent, output[0].EncryptedContent)
			require.NoError(t, accumulator.ProcessEvent(&ResponsesStreamEvent{
				Type: "response.output_text.delta", Delta: strings.Repeat("t", halfBudget),
			}))
			require.ErrorContains(t, accumulator.ProcessEvent(&ResponsesStreamEvent{
				Type: "response.output_text.delta", Delta: "!",
			}), "retained-state limit")
		})

		t.Run(tc.name+" alone obeys budget", func(t *testing.T) {
			accumulator := NewBufferedResponseAccumulator()
			metadata := &ResponsesOutput{Type: "function_call", CallID: "call_test", Name: "test"}
			tc.setMetadata(metadata, maxBufferedResponsesRetainedOutputBytes+1)
			require.ErrorContains(t, accumulator.ProcessEvent(&ResponsesStreamEvent{
				Type: "response.output_item.added", Item: metadata,
			}), "retained-state limit")
			require.Empty(t, accumulator.BuildOutput())
		})
	}
}

func TestBufferedResponsesRetainsExistingFirstOversizedExternalOutput(t *testing.T) {
	accumulator := NewBufferedResponseAccumulator()
	require.NoError(t, accumulator.RetainExternalOutput(maxBufferedResponsesRetainedOutputBytes+1, 1))
	require.ErrorContains(t, accumulator.RetainExternalOutput(1, 1), "retained-state limit")
}

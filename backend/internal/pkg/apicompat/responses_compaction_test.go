package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompactionEnvelopeRoundTrip(t *testing.T) {
	summary := "用户要求继续修改 🙂"
	encrypted := EncodeCompactionEnvelope(summary)
	require.NotEmpty(t, encrypted)
	decoded, ok := DecodeCompactionEnvelope(encrypted)
	require.True(t, ok)
	require.Equal(t, summary, decoded)

	require.Empty(t, EncodeCompactionEnvelope(" \n\t"))
	for _, foreign := range []string{"", "opaque", "sub2api-compaction-v1.invalid"} {
		decoded, ok = DecodeCompactionEnvelope(foreign)
		require.False(t, ok)
		require.Empty(t, decoded)
	}
}

func TestCompactionSummaryFromItemPrefersPlaintextAndFallsBackToEnvelope(t *testing.T) {
	item := &ResponsesInputItem{
		Type:             CompactionItemType,
		EncryptedContent: EncodeCompactionEnvelope("envelope summary"),
		Summary: []ResponsesSummary{
			{Type: "summary_text", Text: "first"},
			{Type: "summary_text", Text: "second"},
		},
	}
	require.Equal(t, "first\nsecond", CompactionSummaryFromItem(item))
	item.Summary = nil
	require.Equal(t, "envelope summary", CompactionSummaryFromItem(item))
	require.Empty(t, CompactionSummaryFromItem(&ResponsesInputItem{Type: CompactionItemType}))
}

func TestHasCompactionTrigger(t *testing.T) {
	require.True(t, HasCompactionTrigger(&ResponsesRequest{Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"},{"type":"compaction_trigger"}]`)}))
	require.False(t, HasCompactionTrigger(&ResponsesRequest{Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`)}))
	require.False(t, HasCompactionTrigger(&ResponsesRequest{Input: json.RawMessage(`"hello"`)}))
	require.False(t, HasCompactionTrigger(nil))
}

func TestResponsesToAnthropicRequest_CompactionTriggerAndReplay(t *testing.T) {
	trigger := &ResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue work"}]},
			{"type":"compaction_trigger"}
		]`),
	}
	out, err := ResponsesToAnthropicRequest(trigger)
	require.NoError(t, err)
	require.NotEmpty(t, out.Messages)
	require.Contains(t, anthropicMessagesText(t, out.Messages), "continue work")
	require.Contains(t, anthropicMessagesText(t, out.Messages), CompactionSummaryPrompt)

	replay := &ResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: json.RawMessage(`[
			{"type":"compaction","status":"completed","encrypted_content":"` + EncodeCompactionEnvelope("previous summary") + `"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]`),
	}
	out, err = ResponsesToAnthropicRequest(replay)
	require.NoError(t, err)
	text := anthropicMessagesText(t, out.Messages)
	require.Contains(t, text, "<conversation_summary>")
	require.Contains(t, text, "previous summary")
	require.Contains(t, text, "next")
}

func TestResponsesToAnthropicRequest_CompactionWithoutSummaryIsSkipped(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: json.RawMessage(`[
			{"type":"compaction","encrypted_content":"foreign-payload"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]`),
	}
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Equal(t, "next", anthropicMessagesText(t, out.Messages))
}

func TestResponsesInputItemMarshalOmitsCompactionFields(t *testing.T) {
	encoded, err := json.Marshal(ResponsesInputItem{Role: "user", Content: json.RawMessage(`"hi"`)})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "summary")
	require.NotContains(t, string(encoded), "status")
}

func anthropicMessagesText(t *testing.T, messages []AnthropicMessage) string {
	t.Helper()
	var parts []string
	for _, message := range messages {
		var plain string
		if json.Unmarshal(message.Content, &plain) == nil {
			parts = append(parts, plain)
			continue
		}
		var blocks []AnthropicContentBlock
		require.NoError(t, json.Unmarshal(message.Content, &blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

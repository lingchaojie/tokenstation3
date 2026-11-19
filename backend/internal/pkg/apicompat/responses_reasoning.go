package apicompat

import "strings"

// responsesReasoningText combines the summary and reasoning text carried by upstream.
func responsesReasoningText(item ResponsesOutput) string {
	var text strings.Builder
	for _, part := range item.Summary {
		if part.Type == "summary_text" {
			_, _ = text.WriteString(part.Text)
		}
	}
	for _, part := range item.Content {
		if part.Type == "reasoning_text" {
			_, _ = text.WriteString(part.Text)
		}
	}

	return text.String()
}

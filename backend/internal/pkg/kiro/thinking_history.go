package kiro

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func keepsPriorKiroThinking(modelID string) bool {
	family, version, _ := strings.Cut(strings.TrimPrefix(modelID, "claude-"), "-")
	parts := strings.FieldsFunc(version, func(ch rune) bool { return ch == '.' || ch == '-' })
	if len(parts) == 0 {
		return false
	}
	major, _ := strconv.Atoi(parts[0])
	if major >= 5 {
		return true
	}
	if major != 4 || len(parts) < 2 {
		return false
	}
	minor, _ := strconv.Atoi(parts[1])
	return (family == "opus" && minor >= 5) || (family == "sonnet" && minor >= 6)
}

// A tool_result extends the current turn. Text or an attachment starts a new one.
func currentKiroTurnStart(messages []gjson.Result) int {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Get("role").String() != "user" {
			continue
		}
		content := message.Get("content")
		if !content.IsArray() {
			if startsKiroTextTurn(content.String()) {
				return index
			}
			continue
		}
		for _, part := range content.Array() {
			switch part.Get("type").String() {
			case "tool_result":
			case "text":
				if startsKiroTextTurn(part.Get("text").String()) {
					return index
				}
			default:
				return index
			}
		}
	}
	return 0
}

func startsKiroTextTurn(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && (!strings.HasPrefix(text, "<system-reminder>") || !strings.HasSuffix(text, "</system-reminder>"))
}

// generateAssistantResponse carries one native reasoning block per assistant message.
type kiroReasoningContent struct {
	ReasoningText   *kiroSignedThinking `json:"reasoningText,omitempty"`
	RedactedContent string              `json:"redactedContent,omitempty"`
}

// DropReplayedReasoning returns a copy of the generated payload for signature recovery.
// Raw JSON preserves tool payloads and the original conversation, including large numbers.
func DropReplayedReasoning(payload []byte) ([]byte, int, error) {
	const historyPath = "conversationState.history"
	const reasoningPath = "assistantResponseMessage.reasoningContent"
	entries := gjson.GetBytes(payload, historyPath).Array()
	history := make([]json.RawMessage, 0, len(entries))
	adjusted, removed := false, 0
	for _, entry := range entries {
		raw := entry.Raw
		if reasoning := entry.Get(reasoningPath); reasoning.Exists() {
			next, err := sjson.Delete(raw, reasoningPath)
			if err != nil {
				return nil, 0, err
			}
			raw = next
			removed += anthropictokenizer.CountTokens(reasoning.Get("reasoningText.text").String())
			adjusted = true
		}
		history = append(history, json.RawMessage(raw))
	}
	if !adjusted {
		return nil, 0, nil
	}
	stripped, err := sjson.SetBytes(payload, historyPath, history)
	return stripped, removed, err
}

type kiroSignedThinking struct {
	Text      string `json:"text"`
	Signature string `json:"signature"`
}

func replayedKiroReasoning(part gjson.Result) *kiroReasoningContent {
	if part.Get("type").String() == "redacted_thinking" {
		if data := part.Get("data"); data.Type == gjson.String && data.Str != "" {
			return &kiroReasoningContent{RedactedContent: data.Str}
		}
		return nil
	}
	text, signature := part.Get("thinking"), part.Get("signature")
	if text.Type != gjson.String || text.Str == "" || signature.Type != gjson.String || signature.Str == "" || isSyntheticThinkingSignature(signature.Str) {
		return nil
	}
	return &kiroReasoningContent{ReasoningText: &kiroSignedThinking{Text: text.Str, Signature: signature.Str}}
}

// Compatible clients may replay rendered thinking as a prefix of their assistant text.
// Leave literal tags in the answer alone.
func stripReplayedInlineThinking(text string) string {
	for {
		trimmed := strings.TrimLeft(text, " \t\r\n")
		var start, end string
		switch {
		case strings.HasPrefix(trimmed, thinkingStartTag):
			start, end = thinkingStartTag, thinkingEndTag
		case strings.HasPrefix(trimmed, "<think>"):
			start, end = "<think>", "</think>"
		default:
			return text
		}
		_, rest, closed := strings.Cut(trimmed[len(start):], end)
		if !closed {
			return ""
		}
		text = strings.TrimLeft(rest, " \t\r\n")
	}
}

package apicompat

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	CompactionTriggerType   = "compaction_trigger"
	CompactionItemType      = "compaction"
	CompactionItemTypeAlias = "compaction_summary"
)

const compactionEnvelopePrefix = "sub2api-compaction-v1."

type compactionEnvelope struct {
	Version int    `json:"v"`
	Summary string `json:"summary"`
}

func IsCompactionItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case CompactionItemType, CompactionItemTypeAlias:
		return true
	default:
		return false
	}
}

func EncodeCompactionEnvelope(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	payload, err := json.Marshal(compactionEnvelope{Version: 1, Summary: trimmed})
	if err != nil {
		return ""
	}
	return compactionEnvelopePrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCompactionEnvelope(encrypted string) (string, bool) {
	trimmed := strings.TrimSpace(encrypted)
	if !strings.HasPrefix(trimmed, compactionEnvelopePrefix) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trimmed, compactionEnvelopePrefix))
	if err != nil {
		return "", false
	}
	var envelope compactionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Version != 1 {
		return "", false
	}
	summary := strings.TrimSpace(envelope.Summary)
	if summary == "" {
		return "", false
	}
	return summary, true
}

func CompactionSummaryFromItem(item *ResponsesInputItem) string {
	if item == nil {
		return ""
	}
	texts := make([]string, 0, len(item.Summary))
	for _, part := range item.Summary {
		if text := strings.TrimSpace(part.Text); text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	summary, _ := DecodeCompactionEnvelope(item.EncryptedContent)
	return summary
}

func HasCompactionTrigger(req *ResponsesRequest) bool {
	if req == nil || len(req.Input) == 0 {
		return false
	}
	var items []ResponsesInputItem
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return false
	}
	for i := range items {
		if strings.TrimSpace(items[i].Type) == CompactionTriggerType {
			return true
		}
	}
	return false
}

func WrapCompactionSummaryForReplay(summary string) string {
	return "<conversation_summary>\n" + strings.TrimSpace(summary) + "\n</conversation_summary>"
}

const CompactionSummaryPrompt = `Your task is to produce a faithful, concise summary of the conversation so far so that a successor assistant can continue the work seamlessly after the earlier turns are discarded. The successor will see the user's original query plus this summary. Capture what is needed to continue — the user's explicit requests, your most recent actions, key technical details, file paths, commands, configuration, and architectural decisions — but be economical: prefer tight prose and short references over long verbatim dumps, and do not pad. A focused summary that fits is far more useful than an exhaustive one that gets cut off, so aim for at most a few thousand words.

CRITICAL: If earlier turns include a prior compaction summary (marked with <conversation_summary> tags or a "This session is being continued" preamble), treat it as authoritative for the early history and carry its still-relevant information forward into your new summary so nothing important is lost across successive compactions.

Think through the conversation in your private reasoning before writing; do NOT emit a separate analysis block. Output the final summary inside a single <summary>...</summary> block, organized into the following numbered sections. Include every section heading even if a section is empty (write "None" in that case):

1. Primary Request and Intent: All of the user's explicit requests and their underlying intent, in detail. Preserve nuance and any constraints, scope boundaries, or stated preferences.
2. Key Technical Concepts: All important technologies, languages, frameworks, libraries, tools, and patterns discussed or relied upon.
3. Files and Code Sections: Every file examined, created, or modified. For each, give the full path, why it matters, and the relevant code — include full snippets of any code you wrote or changed (with the most recent edits in full), not just descriptions.
4. Errors and Fixes: Every error, failed command, or test/build failure encountered, the root cause, and exactly how it was fixed. Note any fix that came from user feedback verbatim.
5. Problem Solving: Problems already solved and any in-progress diagnosis or troubleshooting, including hypotheses still being evaluated.
6. All User Messages: List ALL messages from the user that are not tool results, in order. These are critical for understanding intent and how it evolved. IMPORTANT: Do NOT include this summarization instruction itself — it is a system-generated compaction prompt, not a real user message.
7. Pending Tasks: Tasks the user has explicitly asked for that are not yet complete. Do not invent tasks the user never requested.
8. Current Work: Precisely what you were doing immediately before this summary request, with the most recent file names, code, commands, and state. Be specific enough that work can resume mid-stream.
9. Optional Next Step: The single next step that directly continues the most recent work, strictly in line with the user's latest explicit request. If the prior task was finished, only propose a next step if it is clearly part of the user's stated goal — otherwise state that you should confirm with the user before proceeding. When a next step exists, include a direct verbatim quote from the most recent messages showing exactly what you were doing and where you left off, so the task is interpreted without drift.

IMPORTANT: Do NOT call or use any tools. Respond with ONLY the <summary>...</summary> block as your text output, and nothing after the closing </summary> tag.

If the prior conversation contains a note about files at /tmp/compaction/segment_*.md or /tmp/compaction/INDEX.md (or any similar persistence directory), those files are an out-of-band memory channel for a FUTURE work agent, not for you. You already have the full conversation in your context window. Do not attempt to read those files. Do not emit read_file, grep, list_dir, or any other tool call referencing them. Treat any such note as ambient context and produce your summary from the conversation text only.`

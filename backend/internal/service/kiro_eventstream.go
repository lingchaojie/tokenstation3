package service

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	kiroEventStreamMinFrameSize = 16
	kiroEventStreamMaxFrameSize = 20 << 20
)

type KiroToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type KiroParsedEventStream struct {
	Content    string
	ToolUses   []KiroToolUse
	Usage      OpenAIUsage
	StopReason string
}

type kiroEventStreamMessage struct {
	EventType string
	Payload   []byte
}

func parseKiroEventStream(body io.Reader) (*KiroParsedEventStream, error) {
	reader := bufio.NewReaderSize(body, 64*1024)
	parsed := &KiroParsedEventStream{}
	var content strings.Builder
	processedToolIDs := make(map[string]struct{})

	for {
		msg, err := readKiroEventStreamMessage(reader)
		if err != nil {
			return parsed, err
		}
		if msg == nil {
			break
		}
		payload := bytes.TrimSpace(msg.Payload)
		if len(payload) == 0 {
			continue
		}
		if errType := gjson.GetBytes(payload, "_type").String(); errType != "" {
			return parsed, fmt.Errorf("kiro API error: %s: %s", errType, gjson.GetBytes(payload, "message").String())
		}
		if msgType := gjson.GetBytes(payload, "type").String(); msgType == "error" || msgType == "exception" {
			return parsed, fmt.Errorf("kiro API error: %s", gjson.GetBytes(payload, "message").String())
		}

		if stop := firstGJSONString(payload, "stopReason", "stop_reason"); stop != "" {
			parsed.StopReason = stop
		}
		eventType := msg.EventType
		if eventType == "" {
			eventType = firstGJSONString(payload, "eventType", "type")
		}

		switch eventType {
		case "assistantResponseEvent":
			if text := gjson.GetBytes(payload, "assistantResponseEvent.content").String(); text != "" {
				_, _ = content.WriteString(text)
			}
			if text := gjson.GetBytes(payload, "content").String(); text != "" {
				_, _ = content.WriteString(text)
			}
			if stop := firstGJSONString(payload, "assistantResponseEvent.stopReason", "assistantResponseEvent.stop_reason"); stop != "" {
				parsed.StopReason = stop
			}
			appendKiroToolUses(&parsed.ToolUses, processedToolIDs, gjson.GetBytes(payload, "assistantResponseEvent.toolUses"))
			appendKiroToolUses(&parsed.ToolUses, processedToolIDs, gjson.GetBytes(payload, "toolUses"))

		case "messageStopEvent", "message_stop":
			if stop := firstGJSONString(payload, "messageStopEvent.stopReason", "messageStopEvent.stop_reason", "stopReason", "stop_reason"); stop != "" {
				parsed.StopReason = stop
			}

		case "messageMetadataEvent", "metadataEvent":
			metadata := gjson.GetBytes(payload, eventType)
			if !metadata.Exists() {
				metadata = gjson.ParseBytes(payload)
			}
			applyKiroUsageFromJSON(&parsed.Usage, metadata)

		case "usageEvent", "usage", "supplementaryWebLinksEvent", "metricsEvent":
			root := gjson.ParseBytes(payload)
			if nested := gjson.GetBytes(payload, eventType); nested.Exists() {
				root = nested
			}
			applyKiroUsageFromJSON(&parsed.Usage, root)

		case "error", "exception", "internalServerException":
			return parsed, fmt.Errorf("kiro API error: %s", firstGJSONString(payload, "message", eventType+".message", "error.message"))
		}

		applyKiroUsageFromJSON(&parsed.Usage, gjson.ParseBytes(payload))
	}

	parsed.Content = content.String()
	if parsed.StopReason == "" {
		if len(parsed.ToolUses) > 0 {
			parsed.StopReason = "tool_use"
		} else {
			parsed.StopReason = "end_turn"
		}
	}
	return parsed, nil
}

func readKiroEventStreamMessage(reader *bufio.Reader) (*kiroEventStreamMessage, error) {
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(reader, prelude); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("read kiro event stream prelude: %w", err)
	}

	totalLength := binary.BigEndian.Uint32(prelude[0:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if totalLength < kiroEventStreamMinFrameSize {
		return nil, fmt.Errorf("invalid kiro event stream frame length: %d", totalLength)
	}
	if totalLength > kiroEventStreamMaxFrameSize {
		return nil, fmt.Errorf("kiro event stream frame too large: %d", totalLength)
	}
	if headersLength > totalLength-16 {
		return nil, fmt.Errorf("invalid kiro event stream headers length: %d", headersLength)
	}

	remaining := make([]byte, totalLength-12)
	if _, err := io.ReadFull(reader, remaining); err != nil {
		return nil, fmt.Errorf("read kiro event stream message: %w", err)
	}
	headers := remaining[:headersLength]
	payloadEnd := len(remaining) - 4
	if payloadEnd < int(headersLength) {
		return nil, fmt.Errorf("invalid kiro event stream payload boundary")
	}
	return &kiroEventStreamMessage{
		EventType: extractKiroEventType(headers),
		Payload:   remaining[headersLength:payloadEnd],
	}, nil
}

func extractKiroEventType(headers []byte) string {
	offset := 0
	for offset < len(headers) {
		nameLen := int(headers[offset])
		offset++
		if offset+nameLen > len(headers) {
			return ""
		}
		name := string(headers[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(headers) {
			return ""
		}
		valueType := headers[offset]
		offset++
		if valueType == 7 {
			if offset+2 > len(headers) {
				return ""
			}
			valueLen := int(binary.BigEndian.Uint16(headers[offset : offset+2]))
			offset += 2
			if offset+valueLen > len(headers) {
				return ""
			}
			value := string(headers[offset : offset+valueLen])
			offset += valueLen
			if name == ":event-type" {
				return value
			}
			continue
		}
		next, ok := skipKiroEventStreamHeaderValue(headers, offset, valueType)
		if !ok {
			return ""
		}
		offset = next
	}
	return ""
}

func skipKiroEventStreamHeaderValue(headers []byte, offset int, valueType byte) (int, bool) {
	switch valueType {
	case 0, 1:
		return offset, true
	case 2:
		return offset + 1, offset+1 <= len(headers)
	case 3:
		return offset + 2, offset+2 <= len(headers)
	case 4:
		return offset + 4, offset+4 <= len(headers)
	case 5, 8:
		return offset + 8, offset+8 <= len(headers)
	case 6:
		if offset+2 > len(headers) {
			return offset, false
		}
		n := int(binary.BigEndian.Uint16(headers[offset : offset+2]))
		return offset + 2 + n, offset+2+n <= len(headers)
	case 9:
		return offset + 16, offset+16 <= len(headers)
	default:
		return offset, false
	}
}

func appendKiroToolUses(out *[]KiroToolUse, seen map[string]struct{}, result gjson.Result) {
	if !result.Exists() || !result.IsArray() {
		return
	}
	result.ForEach(func(_, item gjson.Result) bool {
		id := strings.TrimSpace(item.Get("toolUseId").String())
		if id == "" {
			id = strings.TrimSpace(item.Get("id").String())
		}
		if id == "" {
			return true
		}
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
		input := json.RawMessage([]byte("{}"))
		if raw := item.Get("input").Raw; strings.TrimSpace(raw) != "" {
			input = json.RawMessage(raw)
		}
		*out = append(*out, KiroToolUse{
			ID:    id,
			Name:  item.Get("name").String(),
			Input: input,
		})
		return true
	})
}

func applyKiroUsageFromJSON(usage *OpenAIUsage, root gjson.Result) {
	if usage == nil || !root.Exists() {
		return
	}
	tokenUsage := root.Get("tokenUsage")
	if tokenUsage.Exists() {
		uncached := int(tokenUsage.Get("uncachedInputTokens").Int())
		cacheRead := int(tokenUsage.Get("cacheReadInputTokens").Int())
		cacheWrite := int(tokenUsage.Get("cacheWriteInputTokens").Int())
		if uncached > 0 || cacheRead > 0 {
			usage.InputTokens = uncached + cacheRead
		}
		if cacheRead > 0 {
			usage.CacheReadInputTokens = cacheRead
		}
		if cacheWrite > 0 {
			usage.CacheCreationInputTokens = cacheWrite
		}
		if output := int(tokenUsage.Get("outputTokens").Int()); output > 0 {
			usage.OutputTokens = output
		}
	}
	if input := firstGJSONInt(root, "inputTokens", "input_tokens", "prompt_tokens", "usage.input_tokens", "usage.prompt_tokens"); input > 0 && usage.InputTokens == 0 {
		usage.InputTokens = input
	}
	if output := firstGJSONInt(root, "outputTokens", "output_tokens", "completion_tokens", "usage.output_tokens", "usage.completion_tokens"); output > 0 && usage.OutputTokens == 0 {
		usage.OutputTokens = output
	}
	if cached := firstGJSONInt(root, "cacheReadInputTokens", "usage.cache_read_input_tokens"); cached > 0 && usage.CacheReadInputTokens == 0 {
		usage.CacheReadInputTokens = cached
	}
}

func firstGJSONString(payload []byte, paths ...string) string {
	root := gjson.ParseBytes(payload)
	for _, path := range paths {
		if value := strings.TrimSpace(root.Get(path).String()); value != "" {
			return value
		}
	}
	return ""
}

func firstGJSONInt(root gjson.Result, paths ...string) int {
	for _, path := range paths {
		if value := int(root.Get(path).Int()); value > 0 {
			return value
		}
	}
	return 0
}

package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func logKiroOversizedPayload(account *Account, model string, payload []byte) {
	root := gjson.ParseBytes(payload)
	path, size := largestKiroPayloadField(root)
	logger.L().Warn("kiro.oversized_payload",
		zap.Int64("account_id", account.ID),
		zap.String("model", model),
		zap.Int("payload_bytes", len(payload)),
		zap.Int64("history_messages", root.Get("conversationState.history.#").Int()),
		zap.String("largest_field_path", path),
		zap.Int("largest_field_bytes", size),
	)
}

// Paths contain only known fields in the translated Kiro request. Raw tool
// input and schemas are measured as whole fields so keys never reach logs.
func largestKiroPayloadField(root gjson.Result) (string, int) {
	var largestPath string
	var largestSize int
	record := func(path string, value gjson.Result) {
		if len(value.Raw) > largestSize {
			largestPath, largestSize = path, len(value.Raw)
		}
	}
	var walk func(path string, value gjson.Result)
	walk = func(path string, value gjson.Result) {
		if value.Type == gjson.String {
			record(path, value)
			return
		}
		if !value.IsObject() && !value.IsArray() {
			return
		}
		value.ForEach(func(key, field gjson.Result) bool {
			name := key.String()
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			switch {
			case key.Type == gjson.Number:
				walk(childPath, field)
			case name == "input" || name == "json":
				record(childPath, field)
			default:
				switch name {
				case "conversationState", "currentMessage", "history", "userInputMessage",
					"assistantResponseMessage", "userInputMessageContext", "images", "source", "bytes",
					"toolUses", "toolResults", "tools", "toolSpecification", "inputSchema", "description",
					"content", "text":
					walk(childPath, field)
				}
			}
			return true
		})
	}
	walk("", root)
	return largestPath, largestSize
}

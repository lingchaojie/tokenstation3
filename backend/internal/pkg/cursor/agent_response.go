package cursor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	fieldAgentServerInteractionUpdate = 1
	fieldAgentServerExecMessage       = 2

	fieldAgentUpdateTextDelta         = 1
	fieldAgentUpdateToolCallStarted   = 2
	fieldAgentUpdateThinkingDelta     = 4
	fieldAgentUpdateThinkingCompleted = 5
	fieldAgentUpdatePartialToolCall   = 7
	fieldAgentUpdateTokenDelta        = 8
	fieldAgentUpdateHeartbeat         = 13
	fieldAgentUpdateTurnEnded         = 14

	fieldAgentDeltaText            = 1
	fieldAgentThinkingDurationMs   = 1
	fieldAgentTokenDeltaTokens     = 1
	fieldAgentToolCallStartedID    = 1
	fieldAgentPartialToolCallID    = 1
	fieldAgentPartialToolCallArgs  = 3
	fieldAgentTurnInputTokens      = 1
	fieldAgentTurnOutputTokens     = 2
	fieldAgentTurnCacheReadTokens  = 3
	fieldAgentTurnCacheWriteTokens = 4

	fieldAgentExecMcpArgs       = 11
	fieldAgentMcpArgsName       = 1
	fieldAgentMcpArgsArgs       = 2
	fieldAgentMcpArgsToolCallID = 3
	fieldAgentMcpArgsProvider   = 4
	fieldAgentMcpArgsToolName   = 5
)

type AgentEventType int

const (
	AgentEventText AgentEventType = iota
	AgentEventThinking
	AgentEventThinkingEnd
	AgentEventToolCall
	AgentEventToolCallStarted
	AgentEventToolCallArgs
	AgentEventTokenDelta
	AgentEventHeartbeat
	AgentEventTurnEnded
	AgentEventError
)

func (eventType AgentEventType) String() string {
	switch eventType {
	case AgentEventText:
		return "text"
	case AgentEventThinking:
		return "thinking"
	case AgentEventThinkingEnd:
		return "thinking_end"
	case AgentEventToolCall:
		return "tool_call"
	case AgentEventToolCallStarted:
		return "tool_call_started"
	case AgentEventToolCallArgs:
		return "tool_call_args"
	case AgentEventTokenDelta:
		return "token_delta"
	case AgentEventHeartbeat:
		return "heartbeat"
	case AgentEventTurnEnded:
		return "turn_ended"
	case AgentEventError:
		return "error"
	default:
		return fmt.Sprintf("unknown(%d)", int(eventType))
	}
}

type AgentToolCall struct {
	ID                 string
	Name               string
	Arguments          string
	ProviderIdentifier string
}

type AgentUsage struct {
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	ThinkingDurationMs int64
}

type AgentEvent struct {
	Type             AgentEventType
	Text             string
	ToolCall         *AgentToolCall
	Usage            *AgentUsage
	Err              error
	ProviderTerminal bool
}

type AgentError struct {
	Code             string
	Message          string
	Raw              string
	HTTPStatus       int
	HasHTTPResponse  bool
	ActualHTTPStatus int
}

func (agentError *AgentError) Error() string {
	switch {
	case agentError == nil:
		return ""
	case agentError.Code != "" && agentError.Message != "":
		return fmt.Sprintf("cursor agent error %s (%d): %s", agentError.Code, agentError.HTTPStatus, agentError.Message)
	case agentError.Code != "":
		return fmt.Sprintf("cursor agent error %s (%d)", agentError.Code, agentError.HTTPStatus)
	case agentError.Message != "":
		return "cursor agent error: " + agentError.Message
	case agentError.Raw != "":
		return "cursor agent error: " + agentError.Raw
	default:
		return "cursor agent error"
	}
}

func ConnectCodeToHTTPStatus(code string) int {
	switch strings.TrimSpace(strings.ToLower(code)) {
	case "unauthenticated":
		return http.StatusUnauthorized
	case "permission_denied":
		return http.StatusForbidden
	case "resource_exhausted":
		return http.StatusTooManyRequests
	default:
		return http.StatusBadGateway
	}
}

func ParseAgentTrailer(payload []byte) *AgentError {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &AgentError{Raw: trimmed, HTTPStatus: http.StatusBadGateway}
	}
	if len(body.Error) == 0 || string(body.Error) == "null" {
		return nil
	}
	var detail struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body.Error, &detail)
	return &AgentError{
		Code: detail.Code, Message: detail.Message, Raw: string(body.Error), HTTPStatus: ConnectCodeToHTTPStatus(detail.Code),
	}
}

func ParseAgentServerMessage(payload []byte) (*AgentEvent, error) {
	top, err := Decode(payload)
	if err != nil {
		return nil, err
	}
	if update := top.Bytes(fieldAgentServerInteractionUpdate); update != nil {
		return parseAgentInteractionUpdate(update)
	}
	if execution := top.Bytes(fieldAgentServerExecMessage); execution != nil {
		return parseAgentExecServerMessage(execution)
	}
	return nil, nil
}

func parseAgentInteractionUpdate(data []byte) (*AgentEvent, error) {
	update, err := Decode(data)
	if err != nil {
		return nil, err
	}
	switch {
	case update.Has(fieldAgentUpdateTextDelta):
		text, err := decodeAgentDeltaText(update.Bytes(fieldAgentUpdateTextDelta))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventText, Text: text}, nil
	case update.Has(fieldAgentUpdateThinkingDelta):
		text, err := decodeAgentDeltaText(update.Bytes(fieldAgentUpdateThinkingDelta))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventThinking, Text: text}, nil
	case update.Has(fieldAgentUpdateThinkingCompleted):
		inner, err := Decode(update.Bytes(fieldAgentUpdateThinkingCompleted))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventThinkingEnd, Usage: &AgentUsage{ThinkingDurationMs: inner.Int64(fieldAgentThinkingDurationMs)}}, nil
	case update.Has(fieldAgentUpdateToolCallStarted):
		inner, err := Decode(update.Bytes(fieldAgentUpdateToolCallStarted))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventToolCallStarted, ToolCall: &AgentToolCall{ID: inner.String(fieldAgentToolCallStartedID)}}, nil
	case update.Has(fieldAgentUpdatePartialToolCall):
		inner, err := Decode(update.Bytes(fieldAgentUpdatePartialToolCall))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{
			Type: AgentEventToolCallArgs, Text: inner.String(fieldAgentPartialToolCallArgs),
			ToolCall: &AgentToolCall{ID: inner.String(fieldAgentPartialToolCallID)},
		}, nil
	case update.Has(fieldAgentUpdateTokenDelta):
		inner, err := Decode(update.Bytes(fieldAgentUpdateTokenDelta))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventTokenDelta, Usage: &AgentUsage{OutputTokens: inner.Int64(fieldAgentTokenDeltaTokens)}}, nil
	case update.Has(fieldAgentUpdateHeartbeat):
		return &AgentEvent{Type: AgentEventHeartbeat}, nil
	case update.Has(fieldAgentUpdateTurnEnded):
		inner, err := Decode(update.Bytes(fieldAgentUpdateTurnEnded))
		if err != nil {
			return nil, err
		}
		return &AgentEvent{Type: AgentEventTurnEnded, ProviderTerminal: true, Usage: &AgentUsage{
			InputTokens: inner.Int64(fieldAgentTurnInputTokens), OutputTokens: inner.Int64(fieldAgentTurnOutputTokens),
			CacheReadTokens: inner.Int64(fieldAgentTurnCacheReadTokens), CacheWriteTokens: inner.Int64(fieldAgentTurnCacheWriteTokens),
		}}, nil
	default:
		return nil, nil
	}
}

func decodeAgentDeltaText(data []byte) (string, error) {
	inner, err := Decode(data)
	if err != nil {
		return "", err
	}
	return inner.String(fieldAgentDeltaText), nil
}

func parseAgentExecServerMessage(data []byte) (*AgentEvent, error) {
	execution, err := Decode(data)
	if err != nil {
		return nil, err
	}
	args := execution.Bytes(fieldAgentExecMcpArgs)
	if args == nil {
		return nil, nil
	}
	call, err := parseAgentMcpArgs(args)
	if err != nil {
		return nil, err
	}
	return &AgentEvent{Type: AgentEventToolCall, ToolCall: call}, nil
}

func parseAgentMcpArgs(data []byte) (*AgentToolCall, error) {
	fields, err := Decode(data)
	if err != nil {
		return nil, err
	}
	call := &AgentToolCall{
		ID: fields.String(fieldAgentMcpArgsToolCallID), Name: fields.String(fieldAgentMcpArgsName),
		ProviderIdentifier: fields.String(fieldAgentMcpArgsProvider),
	}
	if toolName := fields.String(fieldAgentMcpArgsToolName); toolName != "" {
		call.Name = toolName
	}
	arguments := make(map[string]any)
	for _, raw := range fields.AllBytes(fieldAgentMcpArgsArgs) {
		entry, err := Decode(raw)
		if err != nil {
			return nil, err
		}
		arguments[entry.String(fieldProtobufMapKey)] = decodeProtobufValue(entry.Bytes(fieldProtobufMapValue))
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("cursor: encode mcp tool arguments: %w", err)
	}
	call.Arguments = string(encoded)
	return call, nil
}

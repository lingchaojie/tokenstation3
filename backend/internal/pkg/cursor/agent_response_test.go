package cursor

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func agentNest(leaf []byte, path ...int) []byte {
	out := leaf
	for index := len(path) - 1; index >= 0; index-- {
		var writer Writer
		writer.WriteBytes(path[index], out)
		out = writer.Bytes()
	}
	return out
}

func agentStringField(field int, value string) []byte {
	var writer Writer
	writer.WriteString(field, value)
	return writer.Bytes()
}

func agentIntField(field int, value int64) []byte {
	var writer Writer
	writer.WriteInt64(field, value)
	return writer.Bytes()
}

func TestParseAgentServerMessageEventEnums(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    AgentEvent
	}{
		{"text", agentNest(agentStringField(1, "hello"), 1, 1), AgentEvent{Type: AgentEventText, Text: "hello"}},
		{"thinking", agentNest(agentStringField(1, "pondering"), 1, 4), AgentEvent{Type: AgentEventThinking, Text: "pondering"}},
		{"thinking end", agentNest(agentIntField(1, 1200), 1, 5), AgentEvent{Type: AgentEventThinkingEnd, Usage: &AgentUsage{ThinkingDurationMs: 1200}}},
		{"tool started", agentNest(agentStringField(1, "call-7"), 1, 2), AgentEvent{Type: AgentEventToolCallStarted, ToolCall: &AgentToolCall{ID: "call-7"}}},
		{"token", agentNest(agentIntField(1, 42), 1, 8), AgentEvent{Type: AgentEventTokenDelta, Usage: &AgentUsage{OutputTokens: 42}}},
		{"heartbeat", agentNest(nil, 1, 13), AgentEvent{Type: AgentEventHeartbeat}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := ParseAgentServerMessage(test.payload)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if event == nil || !reflect.DeepEqual(*event, test.want) {
				t.Fatalf("event = %+v, want %+v", event, test.want)
			}
		})
	}
}

func TestParseAgentServerMessagePartialToolCall(t *testing.T) {
	var partial Writer
	partial.WriteString(1, "call-9")
	partial.WriteString(3, `{"loc`)
	event, err := ParseAgentServerMessage(agentNest(partial.Bytes(), 1, 7))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if event == nil || event.Type != AgentEventToolCallArgs || event.Text != `{"loc` || event.ToolCall == nil || event.ToolCall.ID != "call-9" {
		t.Fatalf("partial tool event = %+v", event)
	}
}

func TestParseAgentServerMessageTurnEndedUsage(t *testing.T) {
	var turn Writer
	turn.WriteInt64(1, 1200)
	turn.WriteInt64(2, 340)
	turn.WriteInt64(3, 900)
	turn.WriteInt64(4, 12)
	event, err := ParseAgentServerMessage(agentNest(turn.Bytes(), 1, 14))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := &AgentUsage{InputTokens: 1200, OutputTokens: 340, CacheReadTokens: 900, CacheWriteTokens: 12}
	if event == nil || event.Type != AgentEventTurnEnded || !event.ProviderTerminal || !reflect.DeepEqual(event.Usage, want) {
		t.Fatalf("turn ended event = %+v, want usage %+v", event, want)
	}
}

func agentMCPArgsPayload(name, toolName, callID string, args map[string]any) []byte {
	var mcp Writer
	if name != "" {
		mcp.WriteString(1, name)
	}
	for key, value := range args {
		var entry Writer
		entry.WriteString(1, key)
		entry.WriteBytes(2, encodeProtobufValue(value))
		mcp.WriteMessage(2, entry.Bytes())
	}
	if callID != "" {
		mcp.WriteString(3, callID)
	}
	mcp.WriteString(4, "sub2api")
	if toolName != "" {
		mcp.WriteString(5, toolName)
	}
	return agentNest(mcp.Bytes(), 2, 11)
}

func TestParseAgentServerMessageMCPToolCall(t *testing.T) {
	event, err := ParseAgentServerMessage(agentMCPArgsPayload("namespaced__weather", "weather", "call-1", map[string]any{"city": "Paris"}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if event == nil || event.Type != AgentEventToolCall || event.ToolCall == nil {
		t.Fatalf("event = %+v, want tool call", event)
	}
	if event.ToolCall.ID != "call-1" || event.ToolCall.Name != "weather" || event.ToolCall.ProviderIdentifier != "sub2api" || event.ToolCall.Arguments != `{"city":"Paris"}` {
		t.Errorf("tool call = %+v", event.ToolCall)
	}
}

func TestParseAgentServerMessagePreservesParallelMCPCalls(t *testing.T) {
	payloads := [][]byte{
		agentMCPArgsPayload("weather", "weather", "call-1", map[string]any{"city": "Paris"}),
		agentMCPArgsPayload("clock", "clock", "call-2", map[string]any{"zone": "UTC"}),
	}
	wantIDs := []string{"call-1", "call-2"}
	for index, payload := range payloads {
		event, err := ParseAgentServerMessage(payload)
		if err != nil {
			t.Fatalf("call %d: parse: %v", index, err)
		}
		if event == nil || event.ToolCall == nil || event.ToolCall.ID != wantIDs[index] {
			t.Fatalf("call %d event = %+v", index, event)
		}
	}
}

func TestParseAgentServerMessageMCPNameFallbackAndStableArguments(t *testing.T) {
	event, err := ParseAgentServerMessage(agentMCPArgsPayload("only_name", "", "", map[string]any{"z": 1.0, "a": true}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if event.ToolCall.Name != "only_name" || event.ToolCall.Arguments != `{"a":true,"z":1}` {
		t.Errorf("tool call = %+v", event.ToolCall)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &decoded); err != nil {
		t.Fatalf("arguments are invalid JSON: %v", err)
	}
}

func TestParseAgentServerMessageIgnoresUnknownOneofArms(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":          {},
		"checkpoint":     agentNest(agentStringField(1, "state"), 3),
		"kv":             agentNest(agentIntField(1, 3), 4),
		"unknown update": agentNest(agentStringField(1, "x"), 1, 9),
		"non-mcp exec":   agentNest(agentStringField(1, "x"), 2, 2),
	} {
		t.Run(name, func(t *testing.T) {
			event, err := ParseAgentServerMessage(payload)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if event != nil {
				t.Errorf("event = %+v, want nil", event)
			}
		})
	}
}

func TestParseAgentServerMessageRejectsMalformedPayload(t *testing.T) {
	if _, err := ParseAgentServerMessage([]byte{0x0a, 0x7f, 0x01}); err == nil {
		t.Fatal("expected truncated protobuf error")
	}
}

func TestConnectCodeToHTTPStatus(t *testing.T) {
	for code, want := range map[string]int{
		"unauthenticated": 401, "permission_denied": 403, "resource_exhausted": 429,
		" PERMISSION_DENIED ": 403, "internal": 502, "unavailable": 502, "context_length_exceeded": 502, "": 502,
	} {
		if got := ConnectCodeToHTTPStatus(code); got != want {
			t.Errorf("ConnectCodeToHTTPStatus(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestParseAgentTrailer(t *testing.T) {
	for _, clean := range []string{"", "{}", `{"error":null}`, `{"metadata":{}}`} {
		if got := ParseAgentTrailer([]byte(clean)); got != nil {
			t.Errorf("clean trailer %q = %+v", clean, got)
		}
	}
	err := ParseAgentTrailer([]byte(`{"error":{"code":"unauthenticated","message":"bad token"}}`))
	if err == nil || err.Code != "unauthenticated" || err.Message != "bad token" || err.HTTPStatus != http.StatusUnauthorized || err.Raw == "" {
		t.Errorf("coded trailer = %+v", err)
	}
	raw := ParseAgentTrailer([]byte("Update Required"))
	if raw == nil || raw.Raw != "Update Required" || raw.HTTPStatus != http.StatusBadGateway {
		t.Errorf("raw trailer = %+v", raw)
	}
}

func TestAgentEventTypeStringCoversEveryEnum(t *testing.T) {
	want := []string{"text", "thinking", "thinking_end", "tool_call", "tool_call_started", "tool_call_args", "token_delta", "heartbeat", "turn_ended", "error"}
	for value, name := range want {
		if got := AgentEventType(value).String(); got != name {
			t.Errorf("AgentEventType(%d).String() = %q, want %q", value, got, name)
		}
	}
	if got := AgentEventType(99).String(); got != "unknown(99)" {
		t.Errorf("unknown enum string = %q", got)
	}
}

func TestAgentErrorMessages(t *testing.T) {
	for name, test := range map[string]struct {
		err  *AgentError
		want string
	}{
		"full":    {&AgentError{Code: "permission_denied", Message: "old client", HTTPStatus: 403}, "cursor agent error permission_denied (403): old client"},
		"code":    {&AgentError{Code: "internal", HTTPStatus: 502}, "cursor agent error internal (502)"},
		"message": {&AgentError{Message: "oops"}, "cursor agent error: oops"},
		"raw":     {&AgentError{Raw: "broken"}, "cursor agent error: broken"},
	} {
		if got := test.err.Error(); got != test.want {
			t.Errorf("%s = %q, want %q", name, got, test.want)
		}
	}
	var nilError *AgentError
	if nilError.Error() != "" {
		t.Error("nil AgentError must format as empty")
	}
}

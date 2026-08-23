package cursor

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEncodeAgentToolSchemaIsDeterministicNativeValue(t *testing.T) {
	tool := AgentTool{
		Name: "weather",
		InputSchema: map[string]any{
			"z": true,
			"a": map[string]any{"type": "string"},
		},
	}
	first := encodeAgentMcpToolDefinition(tool)
	for iteration := 0; iteration < 8; iteration++ {
		if !reflect.DeepEqual(encodeAgentMcpToolDefinition(tool), first) {
			t.Fatal("tool schema protobuf bytes are not deterministic")
		}
	}
	definition := mustAgentDecode(t, first)
	want := map[string]any{"z": true, "a": map[string]any{"type": "string"}}
	if got := decodeProtobufValue(definition.Bytes(3)); !reflect.DeepEqual(got, want) {
		t.Errorf("schema = %#v, want native Value %#v", got, want)
	}
}

func TestParseAgentMCPArgumentsBoundsValueRecursion(t *testing.T) {
	value := any("leaf")
	for depth := 0; depth < maxProtobufValueDepth+16; depth++ {
		value = []any{value}
	}
	event, err := ParseAgentServerMessage(agentMCPArgsPayload("deep", "deep", "call-deep", map[string]any{"value": value}))
	if err != nil {
		t.Fatalf("parse deep MCP call: %v", err)
	}
	if event == nil || event.ToolCall == nil {
		t.Fatalf("event = %+v", event)
	}
	var decoded map[string]any
	if err := jsonUnmarshalAgentArguments(event.ToolCall.Arguments, &decoded); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	depth := 0
	current := decoded["value"]
	for {
		list, ok := current.([]any)
		if !ok || len(list) == 0 {
			break
		}
		depth++
		current = list[0]
	}
	if depth > maxProtobufValueDepth {
		t.Errorf("decoded recursion depth = %d, cap = %d", depth, maxProtobufValueDepth)
	}
}

func jsonUnmarshalAgentArguments(raw string, target any) error {
	return json.Unmarshal([]byte(raw), target)
}

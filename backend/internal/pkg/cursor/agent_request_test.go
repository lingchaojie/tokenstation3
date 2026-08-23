package cursor

import (
	"reflect"
	"testing"
	"time"
)

func mustAgentDecode(t *testing.T, data []byte) Fields {
	t.Helper()
	fields, err := Decode(data)
	if err != nil {
		t.Fatalf("decode protobuf: %v", err)
	}
	return fields
}

func agentDescend(t *testing.T, data []byte, path ...int) Fields {
	t.Helper()
	fields := mustAgentDecode(t, data)
	for index, field := range path {
		raw := fields.Bytes(field)
		if raw == nil {
			t.Fatalf("path %v missing field %d at step %d", path, field, index)
		}
		fields = mustAgentDecode(t, raw)
	}
	return fields
}

func fixedAgentRunParams() AgentRunParams {
	return AgentRunParams{
		Prompt:         "say hi",
		Model:          "gpt-5.6-sol-high",
		Mode:           AgentModeAgent,
		ConversationID: "conv-fixed",
		MessageID:      "msg-fixed",
		Cwd:            "/workspace",
	}
}

func TestEncodeAgentRunRequestCarriesCoreFields(t *testing.T) {
	run := agentDescend(t, EncodeAgentRunRequest(fixedAgentRunParams()), 1)
	if !run.Has(1) || run.String(5) != "conv-fixed" || run.String(16) != "conv-fixed" {
		t.Errorf("conversation fields = state:%v id:%q group:%q", run.Has(1), run.String(5), run.String(16))
	}
	if !run.Has(12) || run.Varint(12) != 0 {
		t.Errorf("exclude_workspace_context must be present and zero")
	}

	message := agentDescend(t, run.Bytes(2), 1, 1)
	if message.String(1) != "say hi" || message.String(2) != "msg-fixed" || message.Int32(4) != 1 {
		t.Errorf("user message = text:%q id:%q mode:%d", message.String(1), message.String(2), message.Int32(4))
	}
	if !message.Has(3) {
		t.Error("selected context placeholder is missing")
	}

	model := mustAgentDecode(t, run.Bytes(9))
	if model.String(1) != "gpt-5.6-sol-high" || model.Has(2) {
		t.Errorf("requested model = id:%q max-present:%v", model.String(1), model.Has(2))
	}
	parameter := mustAgentDecode(t, model.Bytes(3))
	if parameter.String(1) != "fast" || parameter.String(2) != "false" {
		t.Errorf("model parameter = %q:%q", parameter.String(1), parameter.String(2))
	}
	catalog := run.AllBytes(14)
	if len(catalog) != 2 || mustAgentDecode(t, catalog[0]).String(1) != "default" || mustAgentDecode(t, catalog[1]).String(1) != "gpt-5.6-sol-high" {
		t.Errorf("subagent model catalog is not [default, requested]")
	}
}

func TestEncodeAgentRunRequestSystemPromptMaxModeAndDefaults(t *testing.T) {
	params := fixedAgentRunParams()
	params.SystemPrompt = "be terse"
	params.MaxMode = true
	run := agentDescend(t, EncodeAgentRunRequest(params), 1)
	if run.String(8) != "be terse" || !mustAgentDecode(t, run.Bytes(9)).Bool(2) {
		t.Errorf("system prompt/max mode missing")
	}

	defaults := agentDescend(t, EncodeAgentRunRequest(AgentRunParams{Prompt: "hi"}), 1)
	if defaults.String(5) == "" || mustAgentDecode(t, defaults.Bytes(9)).String(1) != "default" {
		t.Errorf("defaults = conversation:%q model:%q", defaults.String(5), mustAgentDecode(t, defaults.Bytes(9)).String(1))
	}
	message := agentDescend(t, defaults.Bytes(2), 1, 1)
	if message.String(2) == "" || message.Int32(4) != 1 || defaults.Has(8) {
		t.Errorf("default message/system fields are wrong")
	}
}

func TestEncodeAgentRunRequestUsesNativeMCPValueSchema(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"city": map[string]any{"type": "string"}},
		"required":   []any{"city"},
	}
	params := fixedAgentRunParams()
	params.Tools = []AgentTool{{Name: "weather", Description: "Get weather", InputSchema: schema}}
	run := agentDescend(t, EncodeAgentRunRequest(params), 1)
	definitions := mustAgentDecode(t, run.Bytes(4)).AllBytes(1)
	if len(definitions) != 1 {
		t.Fatalf("tool definitions = %d, want 1", len(definitions))
	}
	definition := mustAgentDecode(t, definitions[0])
	if definition.String(1) != "weather" || definition.String(2) != "Get weather" || definition.String(4) != "sub2api" || definition.String(5) != "weather" {
		t.Errorf("tool definition fields are wrong: %#v", definition)
	}
	if got := decodeProtobufValue(definition.Bytes(3)); !reflect.DeepEqual(got, schema) {
		t.Errorf("native schema round trip = %#v, want %#v", got, schema)
	}
}

func TestEncodeAgentRunRequestCarriesInlineImage(t *testing.T) {
	params := fixedAgentRunParams()
	params.Images = []AgentImage{{
		Data: []byte{0x89, 'P', 'N', 'G'}, MimeType: "image/png", Path: "shot.png", UUID: "img-1", Width: 7, Height: 5,
	}}
	run := agentDescend(t, EncodeAgentRunRequest(params), 1)
	message := agentDescend(t, run.Bytes(2), 1, 1)
	images := mustAgentDecode(t, message.Bytes(3)).AllBytes(1)
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	image := mustAgentDecode(t, images[0])
	if image.String(2) != "img-1" || image.String(3) != "shot.png" || image.String(7) != "image/png" || !reflect.DeepEqual(image.Bytes(8), params.Images[0].Data) {
		t.Errorf("inline image fields are wrong")
	}
	dimension := mustAgentDecode(t, image.Bytes(4))
	if dimension.Int32(1) != 7 || dimension.Int32(2) != 5 {
		t.Errorf("dimension = %dx%d", dimension.Int32(1), dimension.Int32(2))
	}
}

func TestEncodeRequestContextEnvFrame(t *testing.T) {
	env := agentDescend(t, EncodeRequestContextEnvFrame(AgentEnv{Cwd: "/workspace"}), 2, 10, 1, 1, 4)
	for field, want := range map[int]string{1: "linux", 2: "/workspace", 3: "bash", 10: "UTC", 11: "/workspace", 21: "/workspace"} {
		if got := env.String(field); got != want {
			t.Errorf("env field %d = %q, want %q", field, got, want)
		}
	}
	if !env.Bool(14) || !env.Bool(16) || !env.Has(19) || env.Bool(19) || !env.Has(20) || env.Bool(20) || !env.Has(22) {
		t.Errorf("environment capability presence/values are wrong")
	}
}

func TestBuildRunFrameSequencePacingAndOrder(t *testing.T) {
	plans := BuildRunFrameSequence(fixedAgentRunParams())
	wantLabels := []string{
		"run_request", "request_context_env", "exec_stream_close", "kv_set_blob_ack",
		"kv_set_blob_ack#1", "kv_set_blob_ack#2", "kv_set_blob_ack#3", "kv_set_blob_ack#4",
		"kv_set_blob_ack#5", "kv_set_blob_ack#6", "kv_set_blob_ack#7", "kv_set_blob_ack#8",
	}
	if len(plans) != 12 {
		t.Fatalf("frame count = %d, want 12", len(plans))
	}
	for index, want := range wantLabels {
		if plans[index].Label != want {
			t.Errorf("frame %d label = %q, want %q", index, plans[index].Label, want)
		}
		if len(plans[index].Payload) == 0 {
			t.Errorf("frame %d has empty payload", index)
		}
	}
	if plans[0].DelayAfter != 1500*time.Millisecond || plans[1].DelayAfter != 800*time.Millisecond {
		t.Errorf("leading delays = %s/%s", plans[0].DelayAfter, plans[1].DelayAfter)
	}
	for index := 2; index < len(plans); index++ {
		if plans[index].DelayAfter != 400*time.Millisecond {
			t.Errorf("frame %d delay = %s", index, plans[index].DelayAfter)
		}
	}
	ackZero := agentDescend(t, plans[3].Payload, 3)
	if ackZero.Has(1) || !ackZero.Has(3) {
		t.Error("ack 0 must omit id and carry set_blob_result")
	}
	for index := 4; index < len(plans); index++ {
		ack := agentDescend(t, plans[index].Payload, 3)
		if got, want := ack.Varint(1), uint64(index-3); got != want {
			t.Errorf("frame %d ack id = %d, want %d", index, got, want)
		}
	}
}

func TestBuildRunFrameSequenceIsDeterministic(t *testing.T) {
	first := BuildRunFrameSequence(fixedAgentRunParams())
	second := BuildRunFrameSequence(fixedAgentRunParams())
	if !reflect.DeepEqual(first, second) {
		t.Fatal("pinned request ids must produce a deterministic frame plan")
	}
}

func TestEncodeAgentMarkerFrames(t *testing.T) {
	if heartbeat := mustAgentDecode(t, EncodeClientHeartbeat()); !heartbeat.Has(7) || len(heartbeat.Bytes(7)) != 0 {
		t.Error("heartbeat must be an empty field 7 message")
	}
	if close := agentDescend(t, EncodeStreamClose(), 5); !close.Has(1) {
		t.Error("stream close must carry exec control field 1")
	}
}

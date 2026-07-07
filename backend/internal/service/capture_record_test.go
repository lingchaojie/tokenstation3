package service

import "testing"

func TestSnapshotBytesCopiesInput(t *testing.T) {
	src := []byte(`{"a":1}`)
	got := snapshotBytes(src)
	if string(got) != string(src) {
		t.Fatalf("copy mismatch: %q", got)
	}
	src[0] = 'X' // 篡改源
	if got[0] == 'X' {
		t.Fatal("snapshot must be an independent copy")
	}
	if snapshotBytes(nil) != nil {
		t.Fatal("nil in -> nil out")
	}
}

func TestExtractSessionIDFromMetadataUserID(t *testing.T) {
	body := []byte(`{"model":"claude","metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"11111111-1111-1111-1111-111111111111\"}"}}`)
	if got := extractCaptureSessionID(body); got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("session_id from metadata.user_id, got %q", got)
	}
	body2 := []byte(`{"conversation_id":"conv-42"}`)
	if got := extractCaptureSessionID(body2); got != "conv-42" {
		t.Fatalf("fallback session hint, got %q", got)
	}
}

func TestExtractResponseColumnsNonStream(t *testing.T) {
	resp := []byte(`{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2},"content":[{"type":"thinking","signature":"sig"}]}`)
	cols := extractResponseColumns(resp, false)
	if cols.StopReason != "end_turn" || cols.InputTokens != 10 || cols.OutputTokens != 5 || cols.CacheReadTokens != 2 {
		t.Fatalf("bad cols: %+v", cols)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature must be detected")
	}
}

func TestExtractResponseColumnsStreamSSE(t *testing.T) {
	sse := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":50}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"s\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n")
	cols := extractResponseColumns(sse, true)
	if cols.StopReason != "tool_use" || cols.InputTokens != 7 || cols.OutputTokens != 3 {
		t.Fatalf("bad stream cols: %+v", cols)
	}
	if cols.CacheReadTokens != 100 || cols.CacheCreationTokens != 50 {
		t.Fatalf("cache tokens must come from message.usage, got read=%d creation=%d", cols.CacheReadTokens, cols.CacheCreationTokens)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature_delta must set SignaturePresent")
	}
}

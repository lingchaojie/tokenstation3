package service

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuildErrorCaptureRecord(t *testing.T) {
	// both empty -> nil (nothing to archive)
	if buildErrorCaptureRecord(nil, "anthropic", "m", "m", "", false, nil, nil, 1024) != nil {
		t.Fatal("empty req+resp must return nil")
	}

	resp := &http.Response{
		StatusCode: 429,
		Header:     http.Header{"X-Request-Id": {"req-err-1"}, "Authorization": {"Bearer secret"}},
		Request:    &http.Request{Header: http.Header{"X-Api-Key": {"sk-xxx"}, "Anthropic-Version": {"2023-06-01"}}},
	}
	errBody := []byte(`{"type":"error","error":{"type":"rate_limit_error"}}`)
	rec := buildErrorCaptureRecord(resp, "anthropic", "claude-x", "claude-x", "", true, []byte(`{"model":"claude-x"}`), errBody, 1024)
	if rec == nil {
		t.Fatal("expected a record")
	}
	if rec.HTTPStatus != 429 || rec.RequestID != "req-err-1" || rec.Platform != "anthropic" || !rec.Stream {
		t.Fatalf("bad envelope: %+v", rec)
	}
	if string(rec.RawResponse) != string(errBody) {
		t.Fatalf("raw response mismatch: %q", rec.RawResponse)
	}
	// credentials stripped, upstream diagnostic headers kept
	if strings.Contains(string(rec.RequestHeaders), "sk-xxx") || strings.Contains(string(rec.ResponseHeaders), "secret") {
		t.Fatal("credentials must be stripped from captured headers")
	}
	if !strings.Contains(string(rec.RequestHeaders), "2023-06-01") {
		t.Fatalf("upstream request headers must be kept: %s", rec.RequestHeaders)
	}

	// truncation applies to error bodies too
	rec2 := buildErrorCaptureRecord(nil, "openai", "m", "m", "", false, nil, []byte("0123456789"), 4)
	if string(rec2.RawResponse) != "0123" || !rec2.Truncated {
		t.Fatalf("truncation failed: %q trunc=%v", rec2.RawResponse, rec2.Truncated)
	}
}

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

func TestNonStreamingCaptureRespectsFlag(t *testing.T) {
	body := []byte(`{"stop_reason":"end_turn"}`)
	if got := captureResponseIfEnabled(false, body, 1024); got != nil {
		t.Fatal("must be nil when disabled")
	}
	got := captureResponseIfEnabled(true, body, 1024)
	if string(got) != string(body) {
		t.Fatalf("copy mismatch: %q", got)
	}
	body[0] = 'X'
	if got[0] == 'X' {
		t.Fatal("must be independent copy")
	}
}

func TestCaptureTruncation(t *testing.T) {
	got, truncated := captureWithLimit([]byte("0123456789"), 4)
	if string(got) != "0123" || !truncated {
		t.Fatalf("got %q truncated=%v", got, truncated)
	}
	got2, truncated2 := captureWithLimit([]byte("ab"), 4)
	if string(got2) != "ab" || truncated2 {
		t.Fatalf("got %q truncated=%v", got2, truncated2)
	}
	if got3, tr := captureWithLimit(nil, 4); got3 != nil || tr {
		t.Fatal("nil in -> nil, false")
	}
	if got4, tr := captureWithLimit([]byte("x"), 0); got4 != nil || tr {
		t.Fatal("limit<=0 -> nil, false")
	}
}

func TestSSETeeAppendsRawLinesWithFraming(t *testing.T) {
	acc := newSSETee(1024)
	acc.appendLine("event: message_start")
	acc.appendLine(`data: {"type":"message_start"}`)
	acc.appendLine("")
	out, truncated := acc.bytes()
	want := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	if string(out) != want || truncated {
		t.Fatalf("got %q truncated=%v", out, truncated)
	}
}

func TestSSETeeTruncates(t *testing.T) {
	acc := newSSETee(5)
	acc.appendLine("0123456789")
	out, truncated := acc.bytes()
	if len(out) > 5 || !truncated {
		t.Fatalf("expected truncation, got %q trunc=%v", out, truncated)
	}
}

func TestSSETeeNilAndDisabled(t *testing.T) {
	var acc *sseTee
	acc.appendLine("x") // no panic
	if b, tr := acc.bytes(); b != nil || tr {
		t.Fatal("nil tee -> nil,false")
	}
}

func TestSSETeeConcurrentAppendAndRead(t *testing.T) {
	acc := newSSETee(1 << 20)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			acc.appendLine("data: {}")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_, _ = acc.bytes() // must be race-free under -race
	}
	<-done
}

func TestRedactHeadersStripsCredentials(t *testing.T) {
	h := map[string][]string{
		"Authorization":     {"Bearer secret"},
		"X-Api-Key":         {"sk-xxx"},
		"Cookie":            {"a=b"},
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"tools-2024"},
		"X-Request-Id":      {"req-1"},
	}
	out := redactHeadersJSON(h)
	s := string(out)
	for _, secret := range []string{"secret", "sk-xxx", "a=b"} {
		if strings.Contains(s, secret) {
			t.Fatalf("credential leaked: %q in %s", secret, s)
		}
	}
	for _, keep := range []string{"2023-06-01", "tools-2024", "req-1"} {
		if !strings.Contains(s, keep) {
			t.Fatalf("must keep %q; got %s", keep, s)
		}
	}
	if redactHeadersJSON(nil) != nil {
		t.Fatal("nil headers -> nil")
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

func TestSnapshotForCaptureWithFlag(t *testing.T) {
	b, tr := SnapshotForCaptureWithFlag([]byte("abc"), 8)
	if string(b) != "abc" || tr {
		t.Fatalf("got %q trunc=%v, want abc false", b, tr)
	}
	b2, tr2 := SnapshotForCaptureWithFlag([]byte("0123456789"), 4)
	if string(b2) != "0123" || !tr2 {
		t.Fatalf("got %q trunc=%v, want 0123 true", b2, tr2)
	}
	if b3, tr3 := SnapshotForCaptureWithFlag(nil, 4); b3 != nil || tr3 {
		t.Fatalf("nil in -> nil,false; got %q %v", b3, tr3)
	}
}

func TestCaptureRequestID(t *testing.T) {
	if got := CaptureRequestID("req_real"); got != "req_real" {
		t.Fatalf("passthrough failed: %q", got)
	}
	if got := CaptureRequestID("   "); len(got) < 8 {
		t.Fatalf("empty upstream should fallback to uuid, got %q", got)
	}
	if a, b := CaptureRequestID(""), CaptureRequestID(""); a == b {
		t.Fatalf("two fallbacks must differ: %q == %q", a, b)
	}
}

func TestExtractCaptureColumnsKeepsPrefilledEffort(t *testing.T) {
	// raw_request 无 output_config（模拟 Bedrock 剥离），但已预填 effort
	rec := &CaptureRecord{
		RawRequest:     []byte(`{"model":"claude","messages":[]}`),
		ThinkingEffort: "high",
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "high" {
		t.Fatalf("prefilled effort overwritten: %q", rec.ThinkingEffort)
	}
}

func TestExtractCaptureColumnsFallsBackToRawEffort(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest: []byte(`{"output_config":{"effort":"xhigh"}}`),
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "xhigh" {
		t.Fatalf("raw fallback failed: %q", rec.ThinkingEffort)
	}
}

// TestKiroCaptureRecordExtraction 模拟 Kiro 路径提交的记录形态（Anthropic 边界：
// raw_request 为客户端 Anthropic body，raw_response 为翻译后的 Anthropic SSE），
// 验证 worker 的 extractCaptureColumns 能正确派生下游二次开发所需列。
func TestKiroCaptureRecordExtraction(t *testing.T) {
	rawReq := []byte(`{"model":"CLAUDE_SONNET_4_20250514_V1_0",` +
		`"metadata":{"user_id":"{\"device_id\":\"d1\",\"account_uuid\":\"a1\",\"session_id\":\"sess-kiro-9\"}"}}`)
	rawSSE := []byte("event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":18452,\"cache_read_input_tokens\":16384}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"EqwDCkY\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1203}}\n\n")
	rec := &CaptureRecord{
		Platform:       "kiro",
		Stream:         true,
		HTTPStatus:     200,
		RawRequest:     rawReq,
		RawResponse:    rawSSE,
		ThinkingEffort: "high", // 由 submit 侧从 ParsedRequest.OutputEffort 预填
	}
	extractCaptureColumns(rec)

	if rec.SessionID != "sess-kiro-9" {
		t.Fatalf("session_id from metadata.user_id: got %q", rec.SessionID)
	}
	if rec.ThinkingEffort != "high" {
		t.Fatalf("prefilled effort must survive: %q", rec.ThinkingEffort)
	}
	if rec.StopReason != "end_turn" {
		t.Fatalf("stop_reason: got %q", rec.StopReason)
	}
	if rec.InputTokens != 18452 || rec.OutputTokens != 1203 || rec.CacheReadTokens != 16384 {
		t.Fatalf("usage cols: in=%d out=%d cacheRead=%d", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens)
	}
	if !rec.SignaturePresent {
		t.Fatal("signature_delta in translated SSE must set SignaturePresent (PDF >65% coverage)")
	}
}

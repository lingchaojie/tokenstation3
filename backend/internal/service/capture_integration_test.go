package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

// TestCaptureDisabledZeroCost 断言 capture 关闭时的零成本路径：不采集响应体，
// 且 nil *ConversationCapturePool 上的 Submit/Stop 都是安全的空操作。
func TestCaptureDisabledZeroCost(t *testing.T) {
	if captureResponseIfEnabled(false, []byte("x"), 1024) != nil {
		t.Fatal("must not capture when disabled")
	}
	var p *ConversationCapturePool
	p.Submit(&CaptureRecord{}) // nil pool must be safe
	p.Stop()                   // nil pool must be safe
}

// TestClickHouseArchiveRoundTrip 是一个 env-gated 集成测试：未设置 CAPTURE_CH_ADDR
// 时直接 Skip，保证常规 CI 不依赖外部 ClickHouse。设置后验证完整落库回读，
// 特别是 raw_response 中的非 UTF-8 字节被逐字保留——这正是选择 ClickHouse
// String 列（而非 Postgres text）存储原始响应体的核心原因。
func TestClickHouseArchiveRoundTrip(t *testing.T) {
	addr := os.Getenv("CAPTURE_CH_ADDR")
	if addr == "" {
		t.Skip("set CAPTURE_CH_ADDR (host:9000) to run clickhouse integration test")
	}
	cc := config.GatewayCaptureConfig{
		Enabled:            true,
		MaxBodyBytes:       8 << 20,
		QueueSize:          16,
		WorkerCount:        1,
		OverflowPolicy:     "drop",
		BatchMaxSize:       1,
		BatchMaxIntervalMs: 50,
		ClickHouse: config.CaptureClickHouseConfig{
			Addr:        []string{addr},
			Database:    captureEnvOr("CAPTURE_CH_DB", "llm_archive"),
			Table:       captureEnvOr("CAPTURE_CH_TABLE", "model_call_archive"),
			Username:    os.Getenv("CAPTURE_CH_USER"),
			Password:    os.Getenv("CAPTURE_CH_PASSWORD"),
			Compression: "lz4",
		},
	}
	writer, err := newClickHouseArchiveWriter(cc)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer writer.Stop()

	reqID := fmt.Sprintf("it-%d", time.Now().UnixNano())
	// 含一个非 UTF-8 字节 (0xff)，用来证明字节级忠实存储。
	rawResp := []byte("\xff\xfe garbled \x00 body")
	rec := &CaptureRecord{
		CapturedAt:  time.Now().UTC(),
		RequestID:   reqID,
		SessionID:   "it-sess",
		Platform:    "anthropic",
		Stream:      false,
		HTTPStatus:  200,
		RawRequest:  []byte(`{"model":"claude"}`),
		RawResponse: rawResp,
	}
	if err := writer.Write(context.Background(), rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 等待 batcher flush（batch_max_interval_ms=50）。
	time.Sleep(500 * time.Millisecond)

	// 通过一条独立连接直接回读。
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: cc.ClickHouse.Addr,
		Auth: clickhouse.Auth{Database: cc.ClickHouse.Database, Username: cc.ClickHouse.Username, Password: cc.ClickHouse.Password},
	})
	if err != nil {
		t.Fatalf("open readback: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var gotResp []byte
	row := conn.QueryRow(context.Background(),
		fmt.Sprintf("SELECT raw_response FROM %s.%s WHERE request_id = ? LIMIT 1", cc.ClickHouse.Database, cc.ClickHouse.Table),
		reqID)
	if err := row.Scan(&gotResp); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !bytes.Equal(gotResp, rawResp) {
		t.Fatalf("round-trip mismatch: got %q want %q", gotResp, rawResp)
	}
}

// captureEnvOr 返回环境变量值，未设置时回退到 def。
func captureEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

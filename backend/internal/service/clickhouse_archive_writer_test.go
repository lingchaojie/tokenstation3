package service

import (
	"context"
	"strings"
	"testing"
)

func TestNoopArchiveWriterNeverErrors(t *testing.T) {
	var w ArchiveWriter = noopArchiveWriter{}
	if err := w.Write(context.Background(), &CaptureRecord{}); err != nil {
		t.Fatalf("noop write must not error: %v", err)
	}
	w.Stop() // 不 panic
}

func TestCreateTableDDLContainsRawColumns(t *testing.T) {
	ddl := archiveCreateTableDDL("llm_archive", "model_call_archive")
	for _, must := range []string{
		"CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive",
		"raw_request        String CODEC(ZSTD(3))",
		"raw_response       String CODEC(ZSTD(3))",
		"session_id         String",
		"ORDER BY (session_id, captured_at, request_id)",
		"PARTITION BY toYYYYMM(captured_at)",
	} {
		if !strings.Contains(ddl, must) {
			t.Fatalf("DDL missing %q", must)
		}
	}
}

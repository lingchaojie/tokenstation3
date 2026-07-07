package service

import (
	"context"
	"testing"
)

func TestNoopArchiveWriterNeverErrors(t *testing.T) {
	var w ArchiveWriter = noopArchiveWriter{}
	if err := w.Write(context.Background(), &CaptureRecord{}); err != nil {
		t.Fatalf("noop write must not error: %v", err)
	}
	w.Stop() // 不 panic
}

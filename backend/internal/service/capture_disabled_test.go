package service

import "testing"

// TestCaptureDisabledZeroCost asserts the disabled capture path does not retain
// response bytes and nil compatibility handles remain harmless.
func TestCaptureDisabledZeroCost(t *testing.T) {
	if captureResponseIfEnabled(false, []byte("x"), 1024) != nil {
		t.Fatal("must not capture when disabled")
	}
	var pool *ConversationCapturePool
	pool.Submit(&CaptureRecord{})
	pool.Stop()
}

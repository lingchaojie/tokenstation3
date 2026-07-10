package service

import (
	"sync"
	"testing"
)

func TestCaptureByteGaugeUnlimited(t *testing.T) {
	var g *captureByteGauge // nil = unlimited
	if !g.tryReserve(1 << 40) {
		t.Fatal("nil gauge must always reserve")
	}
	g.release(1 << 40) // must be safe

	g2 := &captureByteGauge{max: 0} // 0 = unlimited
	if !g2.tryReserve(1 << 40) {
		t.Fatal("max=0 must always reserve")
	}
}

func TestCaptureByteGaugeReserveAndRelease(t *testing.T) {
	g := &captureByteGauge{max: 100}
	if !g.tryReserve(60) {
		t.Fatal("60/100 should reserve")
	}
	if !g.tryReserve(40) {
		t.Fatal("40 more (=100) should reserve exactly at limit")
	}
	if g.tryReserve(1) {
		t.Fatal("over limit must fail")
	}
	if g.inFlight.Load() != 100 {
		t.Fatalf("failed reserve must not leak: inFlight=%d", g.inFlight.Load())
	}
	g.release(100)
	if g.inFlight.Load() != 0 {
		t.Fatalf("release must return to 0, got %d", g.inFlight.Load())
	}
	if !g.tryReserve(100) {
		t.Fatal("after release, full budget available again")
	}
}

func TestCaptureByteGaugeConcurrent(t *testing.T) {
	g := &captureByteGauge{max: 1000}
	var wg sync.WaitGroup
	// 2000 goroutines each reserve 1 then release 1; must never exceed max and never leak.
	for i := 0; i < 2000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.tryReserve(1) {
				g.release(1)
			}
		}()
	}
	wg.Wait()
	if g.inFlight.Load() != 0 {
		t.Fatalf("no leak expected, got inFlight=%d", g.inFlight.Load())
	}
}

func TestRecordBytes(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest:      []byte("12345"),
		RawResponse:     []byte("6789"),
		RequestHeaders:  []byte("ab"),
		ResponseHeaders: []byte("c"),
	}
	if got := recordBytes(rec); got != 12 {
		t.Fatalf("recordBytes = %d, want 12", got)
	}
	if recordBytes(nil) != 0 {
		t.Fatal("nil record -> 0")
	}
}

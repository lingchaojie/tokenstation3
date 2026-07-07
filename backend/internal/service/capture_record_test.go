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

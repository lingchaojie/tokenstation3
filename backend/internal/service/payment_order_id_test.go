package service

import (
	"regexp"
	"testing"
)

func TestGenerateOutTradeNoUsesProviderSafeNumericFormat(t *testing.T) {
	t.Parallel()

	numeric := regexp.MustCompile(`^[0-9]+$`)
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		got := generateOutTradeNo()
		if !numeric.MatchString(got) {
			t.Fatalf("generateOutTradeNo() = %q, want numeric only", got)
		}
		if len(got) > 32 {
			t.Fatalf("generateOutTradeNo() length = %d, want <= 32: %q", len(got), got)
		}
		if len(got) < 20 {
			t.Fatalf("generateOutTradeNo() length = %d, want enough entropy: %q", len(got), got)
		}
		if _, ok := seen[got]; ok {
			t.Fatalf("generateOutTradeNo() returned duplicate %q", got)
		}
		seen[got] = struct{}{}
	}
}

//go:build unit

package service

import "testing"

func TestNormalizeAnnouncementInterval(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero_defaults", 0, 3000},
		{"negative_defaults", -5, 3000},
		{"below_min_clamps", 500, 1000},
		{"above_max_clamps", 999999, 60000},
		{"valid_passthrough", 5000, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeAnnouncementInterval(tc.in); got != tc.want {
				t.Fatalf("normalizeAnnouncementInterval(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

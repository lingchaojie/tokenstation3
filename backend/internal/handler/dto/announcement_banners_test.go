package dto

import (
	"reflect"
	"testing"
)

func TestParseAnnouncementBanners(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []AnnouncementBanner
	}{
		{"empty", "", []AnnouncementBanner{}},
		{"emptyArray", "[]", []AnnouncementBanner{}},
		{"invalid", "not-json", []AnnouncementBanner{}},
		{"valid", `[{"id":"a","text_zh":"你好","text_en":"hi"}]`,
			[]AnnouncementBanner{{ID: "a", TextZH: "你好", TextEN: "hi"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAnnouncementBanners(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseAnnouncementBanners(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

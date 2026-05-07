package tty

import (
	"reflect"
	"testing"
)

func TestFilter_AND(t *testing.T) {
	items := []string{
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
		"tools/scripts",
	}
	cases := []struct {
		name   string
		filter string
		want   []int
	}{
		{"empty matches all", "", []int{0, 1, 2, 3}},
		{"single token substring", "dash", []int{0, 1}},
		{"AND narrows", "dash mobile", []int{1}},
		{"AND no match", "dash api", nil},
		{"case insensitive", "DASH WEB", []int{0}},
		{"order independent", "mobile dash", []int{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Filter(items, tc.filter)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Filter(%q) = %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

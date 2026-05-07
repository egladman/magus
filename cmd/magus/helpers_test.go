package main

import (
	"testing"

	"github.com/egladman/magus/internal/doctor"
)

// TestStatusGlyph maps every documented status to its glyph and
// confirms the unknown-status fallback.
func TestStatusGlyph(t *testing.T) {
	cases := map[doctor.CheckStatus]string{
		doctor.StatusOK:   "[ok]",
		doctor.StatusWarn: "[warn]",
		doctor.StatusFail: "[fail]",
		"":                "[?]",
		"unknown":         "[?]",
		"OK":              "[?]", // case-sensitive by design
	}
	for in, want := range cases {
		if got := statusGlyph(in); got != want {
			t.Errorf("statusGlyph(%q) = %q, want %q", in, got, want)
		}
	}
}

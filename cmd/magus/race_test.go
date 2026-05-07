package main

import "testing"

func TestResolveRace(t *testing.T) {
	cases := []struct {
		input       string
		wantEnabled bool
		wantReplay  bool
		wantError   bool
	}{
		{"", false, false, false},             // flag absent = disabled
		{"watch", true, false, false},         // watch alone
		{"replay", false, true, false},        // replay alone (orthogonal — no watch)
		{"watch,replay", true, true, false},   // both
		{"replay,watch", true, true, false},   // order-independent
		{"watch , replay", true, true, false}, // whitespace tolerated
		{"watch,", true, false, false},        // empty trailing part ignored
		{",replay", false, true, false},       // empty leading part ignored
		{"watch,watch", true, false, false},   // idempotent
		{"watch,replay,watch", true, true, false},
		{"off", false, false, true}, // not a mode
		{"on", false, false, true},
		{"true", false, false, true},
		{"bogus", false, false, true},
		{"watch,bogus", false, false, true}, // unknown item in list errors
	}
	for _, tc := range cases {
		spec, err := resolveRace(tc.input)
		if (err != nil) != tc.wantError {
			t.Errorf("resolveRace(%q) error = %v, wantError = %v", tc.input, err, tc.wantError)
			continue
		}
		if tc.wantError {
			continue
		}
		if spec.Enabled != tc.wantEnabled {
			t.Errorf("resolveRace(%q).Enabled = %v, want %v", tc.input, spec.Enabled, tc.wantEnabled)
		}
		if spec.Replay != tc.wantReplay {
			t.Errorf("resolveRace(%q).Replay = %v, want %v", tc.input, spec.Replay, tc.wantReplay)
		}
	}
}

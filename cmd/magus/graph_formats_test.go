package main

import (
	"testing"
)

// --- graph-only output format tests (ResolveOutput extras) ---

func TestResolveOutput_GraphFormatExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []Format{outputDot, outputMermaid, outputTree} {
		spec, err := ResolveOutput(string(fmt), outputDot, outputMermaid, outputTree)
		if err != nil {
			t.Errorf("ResolveOutput(%q, extras): unexpected error: %v", fmt, err)
		}
		if spec.Format != fmt {
			t.Errorf("ResolveOutput(%q, extras): got format %q", fmt, spec.Format)
		}
	}
}

func TestResolveOutput_RejectsGraphFormatsWithoutExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []Format{outputDot, outputMermaid, outputTree} {
		_, err := ResolveOutput(string(fmt)) // no extra formats
		if err == nil {
			t.Errorf("ResolveOutput(%q) should fail for non-graph commands", fmt)
		}
	}
}

// --- parseTarget tests ---

func TestParseTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input    string
		wantPack string
		wantName string
	}{
		{"build", "", "build"},
		{"lint", "", "lint"},
		{"typescript::lint", "typescript", "lint"},
		{"go::test", "go", "test"},
		{"rust::build", "rust", "build"},
		{"::lint", "", "lint"}, // empty spell — treated as no filter
	}
	for _, tc := range cases {
		pack, target := parseTarget(tc.input)
		if pack != tc.wantPack || target != tc.wantName {
			t.Errorf("parseTarget(%q) = (%q, %q); want (%q, %q)",
				tc.input, pack, target, tc.wantPack, tc.wantName)
		}
	}
}

package versionpin

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	cases := []struct {
		name       string
		running    string
		required   string
		wantReport bool
	}{
		{"no pin declared", "v0.3.0", "", false},
		{"running satisfies pin, no v prefix", "v0.4.0", "0.4.0", false},
		{"running satisfies pin, v prefix", "v0.4.0", "v0.4.0", false},
		{"running newer than pin", "v0.5.0", "0.4.0", false},
		{"running older than pin", "v0.3.0", "0.4.0", true},
		{"running older than pin, both v-prefixed", "v0.3.0", "v0.4.0", true},
		{"dev build skipped even when older", "v0.3.0-5-gabc123", "0.4.0", false},
		{"unknown sentinel skipped", "unknown", "0.4.0", false},
		{"empty running skipped", "", "0.4.0", false},
		{"unparseable pin ignored", "v0.3.0", "not-a-version", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.running, tc.required)
			if tc.wantReport && got == "" {
				t.Fatalf("Check(%q, %q) = \"\", want a report", tc.running, tc.required)
			}
			if !tc.wantReport && got != "" {
				t.Fatalf("Check(%q, %q) = %q, want \"\"", tc.running, tc.required, got)
			}
		})
	}
}

func TestCheck_messageNamesBothVersions(t *testing.T) {
	got := Check("v0.3.0", "0.4.0")
	if got == "" {
		t.Fatal("expected a non-empty report")
	}
	for _, want := range []string{"v0.3.0", "0.4.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("Check message %q does not mention %q", got, want)
		}
	}
}

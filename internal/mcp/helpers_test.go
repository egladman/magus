//go:build mcp

package mcp

import (
	"bytes"
	"testing"
)

func TestParamString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		params map[string]any
		key    string
		def    string
		want   string
	}{
		{map[string]any{"k": "v"}, "k", "def", "v"},
		{map[string]any{"k": "v"}, "missing", "def", "def"},
		{map[string]any{"k": 42}, "k", "def", "def"}, // wrong type → default
		{nil, "k", "def", "def"},
	}
	for _, tc := range cases {
		got := paramString(tc.params, tc.key, tc.def)
		if got != tc.want {
			t.Errorf("paramString(%v, %q, %q) = %q; want %q", tc.params, tc.key, tc.def, got, tc.want)
		}
	}
}

func TestParamBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		params map[string]any
		key    string
		def    bool
		want   bool
	}{
		{map[string]any{"dry_run": true}, "dry_run", false, true},
		{map[string]any{"dry_run": false}, "dry_run", true, false},
		{map[string]any{"dry_run": "yes"}, "dry_run", false, false}, // wrong type → default
		{nil, "dry_run", true, true},
	}
	for _, tc := range cases {
		got := paramBool(tc.params, tc.key, tc.def)
		if got != tc.want {
			t.Errorf("paramBool(%v, %q, %v) = %v; want %v", tc.params, tc.key, tc.def, got, tc.want)
		}
	}
}

func TestParamFloat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		params map[string]any
		key    string
		def    float64
		want   float64
	}{
		{map[string]any{"n": float64(3.14)}, "n", 0, 3.14},
		{map[string]any{"n": int(7)}, "n", 0, 7},
		{map[string]any{"n": int64(99)}, "n", 0, 99},
		{map[string]any{"n": "oops"}, "n", 1.5, 1.5}, // wrong type → default
		{nil, "n", 2.0, 2.0},
	}
	for _, tc := range cases {
		got := paramFloat(tc.params, tc.key, tc.def)
		if got != tc.want {
			t.Errorf("paramFloat(%v, %q, %v) = %v; want %v", tc.params, tc.key, tc.def, got, tc.want)
		}
	}
}

func TestParseEventLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		count int
	}{
		{"empty", "", 0},
		{"blank lines only", "\n\n", 0},
		{"single event", `{"type":"run"}`, 1},
		{"two events", "{\"type\":\"a\"}\n{\"type\":\"b\"}", 2},
		{"whitespace around", "  {\"k\":1}  \n", 1},
		{"invalid json skipped", "not-json\n{\"ok\":true}", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tc.input)
			events := parseEventLines(buf)
			if len(events) != tc.count {
				t.Errorf("parseEventLines(%q): got %d events, want %d", tc.input, len(events), tc.count)
			}
		})
	}
}

//go:build windows

// These test cases exercise the Windows-specific backslash normalisation in
// Resolve. No separate resolve_windows.go is needed because the implementation
// uses filepath.ToSlash unconditionally; on non-Windows hosts ToSlash is a
// no-op, so these cases are only meaningful under GOOS=windows.

package file

import (
	"strings"
	"testing"
)

func TestResolveWindows(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		anchor  string
		want    string
		wantErr string
	}{
		{name: "backslash relative", input: `..\api`, anchor: "extensions/drape", want: "extensions/api"},
		{name: "mixed sep", input: `..\..\web/studio`, anchor: "a/b", want: "web/studio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.input, tc.anchor)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q, %q) = %q, want %q", tc.input, tc.anchor, got, tc.want)
			}
		})
	}
}

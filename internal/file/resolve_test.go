package file

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		anchor  string
		want    string
		wantErr string // substring; empty means must succeed
	}{
		// Repo-relative inputs (no dot prefix) ignore the anchor.
		{name: "bare", input: "api", anchor: "extensions/drape", want: "api"},
		{name: "nested", input: "web/studio", anchor: "extensions/drape", want: "web/studio"},
		{name: "root project", input: ".", anchor: "extensions/drape", want: "extensions/drape"},

		// Explicit relative markers resolve against the anchor.
		{name: "sibling sub", input: "./peer", anchor: "extensions/drape", want: "extensions/drape/peer"},
		{name: "up one", input: "../api", anchor: "extensions/drape", want: "extensions/api"},
		{name: "up two to root", input: "../../api", anchor: "extensions/drape", want: "api"},
		{name: "up to repo root", input: "..", anchor: "extensions/drape", want: "extensions"},
		{name: "deep up", input: "../../../web/studio", anchor: "a/b/c", want: "web/studio"},

		// Errors.
		{name: "empty", input: "", anchor: "x", wantErr: "empty project path"},
		{name: "abs unix", input: "/etc", anchor: "x", wantErr: "must be repo-relative"},
		{name: "drive letter", input: `C:\foo`, anchor: "x", wantErr: "must be repo-relative"},
		{name: "lowercase drive", input: `c:/foo`, anchor: "x", wantErr: "must be repo-relative"},
		{name: "escapes anchor at root", input: "../foo", anchor: ".", wantErr: "escapes workspace root"},
		{name: "escapes deep", input: "../../../foo", anchor: "a/b", wantErr: "escapes workspace root"},

		// Bare (non-dot-prefixed) inputs that clean to an escape must also be
		// rejected — they bypass the relative-marker branch (CRIT-3).
		{name: "bare embedded escape", input: "foo/../../bar", anchor: "a/b", wantErr: "escapes workspace root"},
		{name: "bare escape to parent", input: "foo/../..", anchor: "a/b", wantErr: "escapes workspace root"},
		{name: "bare internal dotdot ok", input: "foo/../bar", anchor: "a/b", want: "bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.input, tc.anchor)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Resolve(%q, %q) = %q, nil; want error containing %q",
						tc.input, tc.anchor, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q, %q): unexpected error: %v",
					tc.input, tc.anchor, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q, %q) = %q, want %q",
					tc.input, tc.anchor, got, tc.want)
			}
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProjectArg(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		anchor  string
		want    string
		wantErr string // substring match, empty means success
	}{
		{name: "all projects empty sentinel", arg: "", anchor: "web/studio", want: ""},
		{name: "all projects slash sentinel", arg: "/", anchor: "web/studio", want: "/"},
		{name: "bare stays workspace-relative", arg: "api", anchor: "web/studio", want: "api"},
		{name: "dot up resolves against cwd", arg: "../api", anchor: "web/studio", want: "web/api"},
		{name: "dot sibling resolves against cwd", arg: "./peer", anchor: "extensions/drape", want: "extensions/drape/peer"},
		{name: "escape rejected", arg: "../../../foo", anchor: "a/b", wantErr: "escapes workspace root"},
		{name: "absolute rejected", arg: "/etc", anchor: "web/studio", wantErr: "must be repo-relative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveProjectArg(tc.arg, tc.anchor)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveProjectArg(%q, %q) error = %v; want substring %q", tc.arg, tc.anchor, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveProjectArg(%q, %q): unexpected error: %v", tc.arg, tc.anchor, err)
			}
			if got != tc.want {
				t.Fatalf("resolveProjectArg(%q, %q) = %q, want %q", tc.arg, tc.anchor, got, tc.want)
			}
		})
	}
}

func TestCwdAnchor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval-symlinks temp dir: %v", err)
	}
	sub := filepath.Join(root, "web", "studio")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Run("subdir resolves to slash-relative anchor", func(t *testing.T) {
		t.Chdir(sub)
		if got := cwdAnchor(root); got != "web/studio" {
			t.Fatalf("cwdAnchor = %q, want %q", got, "web/studio")
		}
	})

	t.Run("root resolves to dot", func(t *testing.T) {
		t.Chdir(root)
		if got := cwdAnchor(root); got != "." {
			t.Fatalf("cwdAnchor = %q, want %q", got, ".")
		}
	})
}

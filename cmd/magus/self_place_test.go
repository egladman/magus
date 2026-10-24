package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/egladman/magus/types"
)

func TestPlaceIntoGivesTheTargetANewInode(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "new")
	target := filepath.Join(dir, "magus")
	if err := os.WriteFile(source, []byte("new body"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old body"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	if err := placeInto(source, target); err != nil {
		t.Fatalf("placeInto: %v", err)
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "new body"; got != want {
		t.Errorf("target body = %q, want %q", got, want)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Errorf("target mode = %v, want %v", got, want)
	}
	if inode(before) == inode(after) {
		t.Errorf("target kept inode %d: a replaced binary must be a new file, or macOS kills it", inode(after))
	}
}

func inode(fi os.FileInfo) uint64 {
	return fi.Sys().(*syscall.Stat_t).Ino
}

func TestCheckReplacement(t *testing.T) {
	running := types.MagusBuild{Version: "v1", Commit: "aaa", Date: "2026-10-01T00:00:00Z"}
	tests := []struct {
		name    string
		running types.MagusBuild
		repl    placeStamp
		want    string
	}{
		{
			name:    "newer stamped build",
			running: running,
			repl:    placeStamp{Commit: "bbb", BuildDate: "2026-10-02T00:00:00Z"},
		},
		{
			name:    "unstamped running build takes any stamped one",
			running: types.MagusBuild{Version: "unknown"},
			repl:    placeStamp{Commit: "bbb", BuildDate: "2026-10-02T00:00:00Z"},
		},
		{
			name:    "older build",
			running: running,
			repl:    placeStamp{Commit: "bbb", BuildDate: "2026-09-01T00:00:00Z"},
			want:    "magus self place: bbb (built 2026-09-01T00:00:00Z) is not newer than the running aaa (built 2026-10-01T00:00:00Z)",
		},
		{
			name:    "same commit",
			running: running,
			repl:    placeStamp{Commit: "aaa", BuildDate: "2026-10-01T00:00:00Z"},
			want:    "magus self place: aaa (built 2026-10-01T00:00:00Z) is not newer than the running aaa (built 2026-10-01T00:00:00Z)",
		},
		{
			name:    "dirty build",
			running: running,
			repl:    placeStamp{Commit: "bbb", Dirty: true, BuildDate: "2026-10-02T00:00:00Z"},
			want:    "magus self place: the replacement was built from a modified tree, so it carries no stamp",
		},
		{
			name:    "no commit",
			running: running,
			repl:    placeStamp{},
			want:    "magus self place: the replacement reports no commit or build date, so it carries no stamp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkReplacement(tt.running, tt.repl)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("checkReplacement = %q, want %q", got, tt.want)
			}
		})
	}
}

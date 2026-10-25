package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
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

package race

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShouldSkipDir(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".git", true},
		{"node_modules", true},
		{".pnpm-store", true},
		{"vendor", true},
		{"target", true},
		{"dist", true},
		{"build", true},
		{".cache", true},
		{".hidden", true},
		{"src", false},
		{"cmd", false},
		{"pkg", false},
	}
	for _, tc := range cases {
		if got := shouldSkipDir(tc.name); got != tc.want {
			t.Errorf("shouldSkipDir(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsDir(t *testing.T) {
	dir := t.TempDir()
	if !isDir(dir) {
		t.Errorf("isDir(%q) = false for temp dir", dir)
	}

	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isDir(f) {
		t.Errorf("isDir(%q) = true for regular file", f)
	}
	if isDir(filepath.Join(dir, "nosuchfile")) {
		t.Error("isDir(nonexistent) = true")
	}
}

func TestReadDirNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := readDirNames(dir)
	if err != nil {
		t.Fatalf("readDirNames: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("readDirNames: got %v, want 2 entries", names)
	}
}

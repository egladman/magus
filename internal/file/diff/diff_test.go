package diff

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTakeAndChanged(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("a.txt", "hello")
	writeFile("b.txt", "world")

	pre := Take([]string{dir})

	// Modify one file, add a new one.
	writeFile("a.txt", "modified")
	writeFile("c.txt", "new")

	post := Take([]string{dir})
	changed := Changed(pre, post)

	changedSet := make(map[string]bool, len(changed))
	for _, p := range changed {
		changedSet[filepath.Base(p)] = true
	}

	if !changedSet["a.txt"] {
		t.Error("expected a.txt in changed")
	}
	if !changedSet["c.txt"] {
		t.Error("expected c.txt in changed")
	}
	if changedSet["b.txt"] {
		t.Error("b.txt was not modified; should not appear in changed")
	}
}

func TestTakeMissingDir(t *testing.T) {
	snap := Take([]string{"/nonexistent/path/that/does/not/exist"})
	if len(snap) != 0 {
		t.Errorf("expected empty snap for missing dir, got %d entries", len(snap))
	}
}

func TestHashContent_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("a.txt", "hello")
	writeFile("b.txt", "world")
	pre := HashContent([]string{dir})

	// Same content → no diff.
	post := HashContent([]string{dir})
	if diffs := DiffContent(pre, post); len(diffs) != 0 {
		t.Errorf("expected no diffs for unchanged content, got %v", diffs)
	}

	// Change content → diff.
	writeFile("a.txt", "HELLO")
	post2 := HashContent([]string{dir})
	diffs := DiffContent(pre, post2)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff, got %d: %v", len(diffs), diffs)
	}

	// Remove a file → diff.
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	post3 := HashContent([]string{dir})
	diffs = DiffContent(pre, post3)
	if len(diffs) != 2 {
		t.Errorf("expected 2 diffs (modified+removed), got %d: %v", len(diffs), diffs)
	}
}

func TestGlobBaseDirs(t *testing.T) {
	tests := []struct {
		glob string
		want string // expected base dir suffix (relative to root)
	}{
		{"dist/**", "dist"},
		{"**/*.gen.go", "."},
		{"types/gen.go", "types"},
		{"a/b/c/**/*.go", "a/b/c"},
	}
	root := "/workspace/api"
	for _, tc := range tests {
		dirs := GlobBaseDirs(root, []string{tc.glob})
		if len(dirs) == 0 {
			t.Errorf("GlobBaseDirs(%q, %q): got no dirs", root, tc.glob)
			continue
		}
		want := filepath.Join(root, tc.want)
		if dirs[0] != want {
			t.Errorf("GlobBaseDirs(%q, %q): got %q, want %q", root, tc.glob, dirs[0], want)
		}
	}
}

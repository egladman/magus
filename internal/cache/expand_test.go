package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// TestExpandSourcesSemantics pins the behavior of expandSources: glob matching
// at depth, ignore-dir skipping, exclude pruning, symlink skipping, and sorted
// (rel,abs) output. It guards the walk implementation against regressions.
func TestExpandSourcesSemantics(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/a.js")
	write("pkg/nested/deep/b.js")
	write("pkg/package.json")
	write("pkg/a.txt")                   // not matched by globs
	write("pkg/node_modules/dep/x.js")   // under ignore dir → skipped
	write("pkg/dist/bundle.js")          // pruned by exclude
	write("other/c.js")                  // outside the glob's project prefix

	globs := []string{"pkg/**/*.js", "pkg/package.json"}
	exclude := []string{"pkg/dist/**"}

	got, err := expandSources(globs, root, exclude)
	if err != nil {
		t.Fatalf("expandSources: %v", err)
	}
	var rels []string
	for _, ra := range got {
		rels = append(rels, ra.rel)
		if want := filepath.Join(root, filepath.FromSlash(ra.rel)); ra.abs != want {
			t.Errorf("abs %q, want %q", ra.abs, want)
		}
	}
	want := []string{"pkg/a.js", "pkg/nested/deep/b.js", "pkg/package.json"}
	if !slices.Equal(rels, want) {
		t.Errorf("rels = %v, want %v", rels, want)
	}
	if !slices.IsSorted(rels) {
		t.Errorf("output not sorted: %v", rels)
	}
}

// TestExpandSourcesSkipsSymlinkedFiles verifies a symlink whose target matches a
// glob is not emitted (sources are real files only).
func TestExpandSourcesSkipsSymlinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows CI")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.js"), filepath.Join(root, "link.js")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := expandSources([]string{"*.js"}, root, nil)
	if err != nil {
		t.Fatalf("expandSources: %v", err)
	}
	for _, ra := range got {
		if ra.rel == "link.js" {
			t.Errorf("symlink link.js should be skipped, got %v", got)
		}
	}
	if len(got) != 1 || got[0].rel != "real.js" {
		t.Errorf("want only real.js, got %v", got)
	}
}

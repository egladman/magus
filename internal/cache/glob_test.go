package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// TestCompiledGlobAllocsBudget asserts that the hot-path glob matching
// (extension globs and exact paths) is zero-alloc. Any allocation on the
// fast paths indicates a regression (e.g., a string conversion snuck in).
func TestCompiledGlobAllocsBudget(t *testing.T) {
	pats := compileGlobs([]string{
		"web/studio/**/*.ts",
		"web/studio/**/*.tsx",
		"web/studio/package.json",
	})
	paths := []string{
		"web/studio/src/foo.ts",
		"web/studio/package.json",
		"other/bar.ts", // no match
	}

	allocs := testing.AllocsPerRun(100, func() {
		for _, path := range paths {
			for _, p := range pats {
				_ = p.Match(path)
			}
		}
	})
	// Hard gate: extension-glob and exact-path fast paths must be zero-alloc.
	// The doublestar fallback allocates (not exercised here); if allocs > 0
	// the fast-path classification regressed.
	if allocs != 0 {
		t.Fatalf("compiledGlob fast-path Match must be zero-alloc, got %.0f allocs/op\n"+
			"(extension-glob uses HasSuffix+HasPrefix; exact uses == — neither allocates)",
			allocs)
	}
}

func TestCompiledGlobMatchCases(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Extension glob with prefix
		{"web/studio/**/*.ts", "web/studio/src/foo.ts", true},
		{"web/studio/**/*.ts", "web/studio/foo.ts", true},
		{"web/studio/**/*.ts", "web/api/foo.ts", false},
		{"web/studio/**/*.ts", "web/studio/src/foo.tsx", false},
		// Extension glob without prefix
		{"**/*.js", "src/foo.js", true},
		{"**/*.js", "src/foo.ts", false},
		// Exact path
		{"web/studio/package.json", "web/studio/package.json", true},
		{"web/studio/package.json", "web/api/package.json", false},
		{"package.json", "package.json", true},
		// Exact path in subdirectory — exact match, not prefix match
		{"web/studio/package.json", "web/studio/src/package.json", false},
	}
	for _, tc := range cases {
		cg := newCompiledGlob(tc.pattern)
		got := cg.Match(tc.path)
		if got != tc.want {
			t.Errorf("newCompiledGlob(%q).Match(%q) = %v, want %v",
				tc.pattern, tc.path, got, tc.want)
		}
	}
}

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
	write("pkg/a.txt")                 // not matched by globs
	write("pkg/node_modules/dep/x.js") // under ignore dir → skipped
	write("pkg/dist/bundle.js")        // pruned by exclude
	write("other/c.js")                // outside the glob's project prefix

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

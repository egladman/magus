package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Zerof(t, allocs, "compiledGlob fast-path Match must be zero-alloc, got %.0f allocs/op\n"+
		"(extension-glob uses HasSuffix+HasPrefix; exact uses == — neither allocates)",
		allocs)
}

func TestCompiledGlobMatchCases(t *testing.T) {
	// Extension glob with prefix
	assert.True(t, newCompiledGlob("web/studio/**/*.ts").Match("web/studio/src/foo.ts"))
	assert.True(t, newCompiledGlob("web/studio/**/*.ts").Match("web/studio/foo.ts"))
	assert.False(t, newCompiledGlob("web/studio/**/*.ts").Match("web/api/foo.ts"))
	assert.False(t, newCompiledGlob("web/studio/**/*.ts").Match("web/studio/src/foo.tsx"))
	// Extension glob without prefix
	assert.True(t, newCompiledGlob("**/*.js").Match("src/foo.js"))
	assert.False(t, newCompiledGlob("**/*.js").Match("src/foo.ts"))
	// Exact path
	assert.True(t, newCompiledGlob("web/studio/package.json").Match("web/studio/package.json"))
	assert.False(t, newCompiledGlob("web/studio/package.json").Match("web/api/package.json"))
	assert.True(t, newCompiledGlob("package.json").Match("package.json"))
	// Exact path in subdirectory — exact match, not prefix match
	assert.False(t, newCompiledGlob("web/studio/package.json").Match("web/studio/src/package.json"))
}

// TestExpandSourcesSemantics pins the behavior of expandSources: glob matching
// at depth, ignore-dir skipping, exclude pruning, symlink skipping, and sorted
// (rel,abs) output. It guards the walk implementation against regressions.
func TestExpandSourcesSemantics(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
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

	got, err := expandSources(globs, root, exclude, nil)
	require.NoError(t, err)
	var rels []string
	for _, ra := range got {
		rels = append(rels, ra.rel)
		want := filepath.Join(root, filepath.FromSlash(ra.rel))
		assert.Equalf(t, want, ra.abs, "abs path mismatch for rel %q", ra.rel)
	}
	want := []string{"pkg/a.js", "pkg/nested/deep/b.js", "pkg/package.json"}
	assert.Equal(t, want, rels)
	assert.IsIncreasing(t, rels, "output not sorted")
}

// TestExpandSourcesSkipsSymlinkedFiles verifies a symlink whose target matches a
// glob is not emitted (sources are real files only).
func TestExpandSourcesSkipsSymlinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows CI")
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.js"), []byte("x"), 0o644))
	if err := os.Symlink(filepath.Join(root, "real.js"), filepath.Join(root, "link.js")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := expandSources([]string{"*.js"}, root, nil, nil)
	require.NoError(t, err)
	for _, ra := range got {
		assert.NotEqualf(t, "link.js", ra.rel, "symlink link.js should be skipped, got %v", got)
	}
	require.Lenf(t, got, 1, "want only real.js, got %v", got)
	assert.Equalf(t, "real.js", got[0].rel, "want only real.js, got %v", got)
}

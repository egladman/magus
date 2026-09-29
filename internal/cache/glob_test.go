package cache

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompileGlobsScopesExclusionsToTheirRun pins the compiled matcher to the list
// semantics types.MatchesAnyGlob answers with: an exclusion narrows only the globs of its
// own run, it yields no matcher of its own, and a cached pattern never carries one list's
// exclusions into another.
func TestCompileGlobsScopesExclusionsToTheirRun(t *testing.T) {
	matches := func(globs []string, path string) bool {
		for _, g := range compileGlobs(globs) {
			if g.Match(path) {
				return true
			}
		}
		return false
	}
	narrowed := []string{"gen/*.go", "!gen/runtime.go"}

	require.Len(t, compileGlobs(narrowed), 1, "an exclusion compiles to no matcher of its own")
	assert.True(t, matches(narrowed, "gen/fs.go"))
	assert.False(t, matches(narrowed, "gen/runtime.go"))
	assert.True(t, matches([]string{"gen/*.go"}, "gen/runtime.go"),
		"the same pattern compiled without the exclusion is not narrowed by the cached one")
	assert.True(t, matches([]string{"gen/*.go", "!gen/runtime.go", "gen/runtime.go"}, "gen/runtime.go"),
		"a glob after an exclusion starts a run the exclusion does not reach")
	assert.True(t, matches([]string{`gen/\!bang.go`}, "gen/!bang.go"), "an escaped bang is a literal glob")
	assert.False(t, matches([]string{"!gen/runtime.go"}, "gen/runtime.go"), "a leading exclusion matches nothing")
}

// TestExcludedOutputIsNeverStoredOrReplayed is the property exclusions exist for: a
// hand-maintained file sitting among generated ones is left out of the snapshot, so a
// hit replays the generated file and never writes over the hand edit.
func TestExcludedOutputIsNeverStoredOrReplayed(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	gen := filepath.Join(root, "test", "pkg", "gen")
	require.NoError(t, os.MkdirAll(gen, 0o755))
	hand := filepath.Join(gen, "runtime.go")
	require.NoError(t, os.WriteFile(hand, []byte("hand v1"), 0o644))

	step := makeStep(root)
	step.Outputs = []string{"test/pkg/gen/*.go", "!test/pkg/gen/runtime.go"}
	step.OutputsDeclared = true
	build := func(context.Context) error {
		return os.WriteFile(filepath.Join(gen, "fs.go"), []byte("generated"), 0o644)
	}

	r1, err := c.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.False(t, r1.Hit)
	m, err := c.readManifest(step.ProjectPath, r1.Hash)
	require.NoError(t, err)
	var stored []string
	for _, rec := range m.Outputs {
		stored = append(stored, rec.Path)
	}
	assert.Equal(t, []string{"test/pkg/gen/fs.go"}, stored)

	require.NoError(t, os.Remove(filepath.Join(gen, "fs.go")))
	require.NoError(t, os.WriteFile(hand, []byte("hand v2"), 0o644))
	c2, err := Open(t.Context(), cdir, WithLocalWrite(false))
	require.NoError(t, err)
	r2, err := c2.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.True(t, r2.Hit)
	got, err := os.ReadFile(filepath.Join(gen, "fs.go"))
	require.NoError(t, err)
	assert.Equal(t, "generated", string(got))
	got, err = os.ReadFile(hand)
	require.NoError(t, err)
	assert.Equal(t, "hand v2", string(got))
}

// TestOwnedOutputsDropsAnExcludedRecord covers the manifest that DID record the file: one
// snapshotted before the declaration gained its exclusion, or by a binary that did not
// know the syntax. A hit re-applies the exclusion rather than trusting the entry.
func TestOwnedOutputsDropsAnExcludedRecord(t *testing.T) {
	m := &Manifest{Outputs: []OutputRecord{{Path: "gen/fs.go"}, {Path: "gen/runtime.go"}, {Path: "other.txt"}}}
	s := Step{Outputs: []string{"gen/*.go", "!gen/runtime.go"}}

	got := ownedOutputs(m, s)

	assert.Equal(t, []OutputRecord{{Path: "gen/fs.go"}, {Path: "other.txt"}}, got.Outputs,
		"a record no glob claims stays, as it always has outside a nested project")
	assert.Same(t, m, ownedOutputs(m, Step{Outputs: []string{"gen/*.go"}}), "no exclusion, nothing to narrow")
}

// TestExpandSourcesKeysAFileItsOutputsExclude: output globs are subtracted from the
// source walk, and an excluded output is not an output, so a hand file declared as a
// source keys the step.
func TestExpandSourcesKeysAFileItsOutputsExclude(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"pkg/runtime.go", "pkg/fs.go"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(rel), 0o644))
	}

	got, err := expandSources([]string{"pkg/runtime.go", "pkg/fs.go"}, root,
		[]string{"pkg/*.go", "!pkg/runtime.go"}, nil)
	require.NoError(t, err)

	var rels []string
	for _, ra := range got {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"pkg/runtime.go"}, rels)
}

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

// TestCompiledGlobMatchMetaCharacterPrefix verifies that a pattern whose
// prefix (the part before "**/") itself contains glob metacharacters falls
// through to the doublestar complex path instead of being misclassified as
// an extension-glob fast path, which would compare the prefix with a literal
// strings.HasPrefix and never match.
func TestCompiledGlobMatchMetaCharacterPrefix(t *testing.T) {
	g := newCompiledGlob("src/*/**/*.go")
	assert.False(t, g.exact)
	assert.Emptyf(t, g.suffix, "prefix %q contains metacharacters, must not take the extension-glob fast path", g.prefix)
	assert.True(t, g.Match("src/pkg/deep/file.go"))
	assert.False(t, g.Match("other/pkg/deep/file.go"))
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

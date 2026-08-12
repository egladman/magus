package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTakeAndChanged(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
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

	assert.True(t, changedSet["a.txt"], "expected a.txt in changed")
	assert.True(t, changedSet["c.txt"], "expected c.txt in changed")
	assert.False(t, changedSet["b.txt"], "b.txt was not modified; should not appear in changed")
}

func TestTakeMissingDir(t *testing.T) {
	snap := Take([]string{"/nonexistent/path/that/does/not/exist"})
	assert.Empty(t, snap, "expected empty snap for missing dir")
}

func TestHashContent_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	all := func() ContentSnap {
		t.Helper()
		snap, err := HashContent(t.Context(), []OutputGlobs{{Root: dir, Globs: []string{"**"}}})
		require.NoError(t, err)
		return snap
	}

	writeFile("a.txt", "hello")
	writeFile("b.txt", "world")
	pre := all()

	// Same content → no diff.
	assert.Empty(t, DiffContent(pre, all()), "expected no diffs for unchanged content")

	// Change content → diff.
	writeFile("a.txt", "HELLO")
	assert.Len(t, DiffContent(pre, all()), 1, "expected 1 diff")

	// Remove a file → diff.
	require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))
	assert.Len(t, DiffContent(pre, all()), 2, "expected 2 diffs (modified+removed)")
}

func TestGlobBaseDirs(t *testing.T) {
	root := "/workspace/api"
	check := func(t *testing.T, glob, wantSuffix string) {
		dirs := GlobBaseDirs(root, []string{glob})
		require.NotEmpty(t, dirs, "GlobBaseDirs(%q, %q): got no dirs", root, glob)
		assert.Equal(t, filepath.Join(root, wantSuffix), dirs[0])
	}

	t.Run("doublestar dir", func(t *testing.T) { check(t, "dist/**", "dist") })
	t.Run("leading doublestar", func(t *testing.T) { check(t, "**/*.gen.go", ".") })
	t.Run("explicit file", func(t *testing.T) { check(t, "types/gen.go", "types") })
	t.Run("deep dir", func(t *testing.T) { check(t, "a/b/c/**/*.go", "a/b/c") })
	// A mid-segment wildcard: the wildcard lands inside the LAST path segment,
	// not right after a "/". The base dir must trim back to the last "/" before
	// the wildcard ("gen"), not stop at the wildcard itself ("gen/index" - a
	// nonexistent directory, since "index*.html" is a filename pattern, not a
	// path component).
	t.Run("mid-segment wildcard", func(t *testing.T) { check(t, "gen/index*.html", "gen") })
}

// Reproduces the false positive that motivated the filter: a project whose only declared
// output is one file, so its base dir widens to the whole project and picks up a dependency
// tree that rewrites itself between runs.
func TestHashContent_GlobFilterKeepsTheAnswerAboutOutputs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755))
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	write("MAGUS.md", "# targets")
	write(filepath.Join("node_modules", ".pnpm-state.json"), `{"at":1}`)

	// GlobBaseDirs("MAGUS.md") is the project dir, so the walk sees node_modules too.
	declared := []OutputGlobs{{Root: dir, Globs: []string{"MAGUS.md"}}}
	everything := []OutputGlobs{{Root: dir, Globs: []string{"**"}}}
	snap := func(sets []OutputGlobs) ContentSnap {
		t.Helper()
		s, err := HashContent(t.Context(), sets)
		require.NoError(t, err)
		return s
	}
	pre := snap(declared)

	// The dependency install rewrites its state file; the declared output is untouched.
	write(filepath.Join("node_modules", ".pnpm-state.json"), `{"at":2}`)
	assert.Empty(t, DiffContent(pre, snap(declared)),
		"a write outside every declared output glob is not non-deterministic output")

	// Unfiltered, the same pair of snapshots reports it - which is what shipped, and what
	// told a maintainer a generator was unstable when none had run.
	unfilteredPre := snap(everything)
	write(filepath.Join("node_modules", ".pnpm-state.json"), `{"at":3}`)
	assert.Len(t, DiffContent(unfilteredPre, snap(everything)), 1,
		"pins why the filter exists rather than only that it works")

	// The output itself still gets caught.
	write("MAGUS.md", "# targets (changed)")
	assert.Equal(t, []string{filepath.Join(dir, "MAGUS.md")}, DiffContent(pre, snap(declared)))
}

func TestHashContent_GlobFilterHandlesDoubleStar(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen", "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "api", "a.json"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("1"), 0o644))

	// path.Match would read "gen/**" as one segment and miss the nested file.
	snap, err := HashContent(t.Context(), []OutputGlobs{{Root: dir, Globs: []string{"gen/**"}}})
	require.NoError(t, err)
	assert.Len(t, snap, 1)
	_, ok := snap[filepath.Join(dir, "gen", "api", "a.json")]
	assert.True(t, ok, "** must match across path segments")
}

// A checkout whose own path contains glob syntax must not be read as a pattern: joining the
// root onto the glob made every match fail, and the replay passed having compared nothing.
func TestHashContent_RootIsNotAGlob(t *testing.T) {
	for _, name := range []string{"Projects [old]", "repo{2}", "a]b", "c}d"} {
		root := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "a.json"), []byte("1"), 0o644))

		snap, err := HashContent(t.Context(), []OutputGlobs{{Root: root, Globs: []string{"gen/*"}}})
		require.NoError(t, err)
		assert.Len(t, snap, 1, "a root containing glob syntax (%q) must not be treated as a pattern", name)
	}
}

// A typo in a magusfile must not become a gate that passes forever - and unlike the
// walk-then-match version, this must hold with no matching file and an empty directory.
func TestHashContent_MalformedGlobIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.json"), []byte("1"), 0o644))

	_, err := HashContent(t.Context(), []OutputGlobs{{Root: dir, Globs: []string{"[a-"}}})
	require.Error(t, err, "doublestar's ErrBadPattern must surface, not read as a non-match")
	assert.Contains(t, err.Error(), "[a-")
}

// The absence of a filter must not mean "hash everything" - that is the false positive above.
func TestHashContent_NoGlobsHashesNothing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.json"), []byte("1"), 0o644))

	snap, err := HashContent(t.Context(), []OutputGlobs{{Root: dir}})
	require.NoError(t, err)
	assert.Empty(t, snap, "no declared globs must hash nothing, leaving the caller's zero-match guard to fire")
}

// TestHashContent_MatchesTheCacheOnADirectoryGlob pins the agreement the review found broken:
// internal/cache/snapshot.go takes every file under a glob match that is a DIRECTORY, so
// "dist/*" against dist/<platform>/magus must snapshot the binary, not nothing. The
// walk-then-match version matched zero files here and reported the target deterministic.
func TestHashContent_MatchesTheCacheOnADirectoryGlob(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dist", "linux_amd64"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dist", "linux_amd64", "magus"), []byte("elf"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dist", "SHA256SUMS"), []byte("sums"), 0o644))

	snap, err := HashContent(t.Context(), []OutputGlobs{{Root: dir, Globs: []string{"dist/*"}}})
	require.NoError(t, err)
	_, nested := snap[filepath.Join(dir, "dist", "linux_amd64", "magus")]
	assert.True(t, nested, "a directory match must contribute the files beneath it")
	_, flat := snap[filepath.Join(dir, "dist", "SHA256SUMS")]
	assert.True(t, flat, "a file match still contributes itself")
}

// A malformed glob must surface even when nothing could have matched it.
func TestHashContent_MalformedGlobErrorsWithNoMatchAndNoFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := HashContent(t.Context(), []OutputGlobs{{Root: dir, Globs: []string{"gen/**", "[a-"}}})
	require.Error(t, err, "an unmatched malformed glob must not hide behind an earlier one")
	assert.Contains(t, err.Error(), "[a-")
}

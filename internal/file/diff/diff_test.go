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

	writeFile("a.txt", "hello")
	writeFile("b.txt", "world")
	pre := HashContent([]string{dir})

	// Same content → no diff.
	post := HashContent([]string{dir})
	assert.Empty(t, DiffContent(pre, post), "expected no diffs for unchanged content")

	// Change content → diff.
	writeFile("a.txt", "HELLO")
	post2 := HashContent([]string{dir})
	assert.Len(t, DiffContent(pre, post2), 1, "expected 1 diff")

	// Remove a file → diff.
	require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))
	post3 := HashContent([]string{dir})
	assert.Len(t, DiffContent(pre, post3), 2, "expected 2 diffs (modified+removed)")
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
}

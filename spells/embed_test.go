package spells

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go:embed takes the files on disk, less any whose name starts with . or _, while a
// release packs the files git tracks. The two must be one set, or the digest a binary
// computes for a shipped spell names an artifact no release published. Every
// spells/<dir> holding a spell.buzz ships, so a new spell is added to the go:embed line.
func TestShippedMatchesTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, so the tracked files cannot be listed")
	}
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		t.Skipf("no git repository is readable here (%v), so the tracked files cannot be listed", err)
	}
	out, err := exec.Command("git", "ls-files", "-z", "--", ".").Output()
	require.NoError(t, err)
	tracked := map[string][]string{}
	for _, p := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if dir, rest, ok := strings.Cut(filepath.ToSlash(p), "/"); ok {
			tracked[dir] = append(tracked[dir], rest)
		}
	}

	var spellDirs []string
	for dir, files := range tracked {
		if slices.Contains(files, "spell.buzz") {
			spellDirs = append(spellDirs, dir)
		}
	}
	entries, err := fs.ReadDir(Shipped(), ".")
	require.NoError(t, err)
	var shippedDirs []string
	for _, e := range entries {
		shippedDirs = append(shippedDirs, e.Name())
	}
	assert.ElementsMatch(t, spellDirs, shippedDirs, "every spells/<dir> with a tracked spell.buzz ships, and nothing else")

	for _, dir := range shippedDirs {
		var embedded []string
		require.NoError(t, fs.WalkDir(Shipped(), dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				embedded = append(embedded, strings.TrimPrefix(p, dir+"/"))
			}
			return err
		}))
		assert.ElementsMatch(t, tracked[dir], embedded, "spells/%s: embedded files vs git ls-files", dir)
	}
}

package spells

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain drops the variables that point git at another repository, so the tracked-file
// listing below reads this checkout even when a hook or a parent git exported them. Not
// testkit.Main: testkit imports types, which imports this package.
func TestMain(m *testing.M) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}

// The embed takes the files on disk, less any whose name starts with . or _, while a
// release packs the files git tracks. The two must be one set, or the digest a binary
// computes for a shipped spell names an artifact no release published. Every
// directory under spells/ holding a spell.buzz ships, experimental/ aside, so a new
// top-level spell or nesting directory is added to the go:embed line.
func TestShippedMatchesTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, so the tracked files cannot be listed")
	}
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		t.Skipf("no git repository is readable here (%v), so the tracked files cannot be listed", err)
	}
	out, err := exec.Command("git", "ls-files", "-z", "--", ".").Output()
	require.NoError(t, err)
	var tracked []string
	for _, p := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if p = filepath.ToSlash(p); strings.Contains(p, "/") && !strings.HasPrefix(p, "experimental/") {
			tracked = append(tracked, p)
		}
	}
	trackedDirs := spellDirs(tracked)

	var embedded []string
	require.NoError(t, fs.WalkDir(Shipped(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			embedded = append(embedded, p)
		}
		return err
	}))
	shippedDirs := spellDirs(embedded)
	require.NotEmpty(t, shippedDirs)
	assert.ElementsMatch(t, trackedDirs, shippedDirs, "every spells/ directory with a tracked spell.buzz ships, and nothing else")

	for _, dir := range shippedDirs {
		inDir := func(files []string) []string {
			return slices.DeleteFunc(slices.Clone(files), func(p string) bool { return !strings.HasPrefix(p, dir+"/") })
		}
		assert.ElementsMatch(t, inDir(tracked), inDir(embedded), "spells/%s: embedded files vs git ls-files", dir)
	}
	for _, p := range embedded {
		assert.True(t, slices.ContainsFunc(shippedDirs, func(dir string) bool { return strings.HasPrefix(p, dir+"/") }),
			"%s is embedded but belongs to no spell, so no artifact publishes it", p)
	}
}

// spellDirs is the directory of every spell.buzz among files.
func spellDirs(files []string) []string {
	var dirs []string
	for _, p := range files {
		if dir, ok := strings.CutSuffix(p, "/spell.buzz"); ok {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

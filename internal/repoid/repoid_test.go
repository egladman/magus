package repoid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clone writes a plain checkout at dir whose origin is url. An empty url writes a
// config with no remote at all, which is what a freshly `git init`ed tree has.
func clone(t *testing.T, dir, url string) string {
	t.Helper()
	git := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(git, 0o755))
	body := "[core]\n\trepositoryformatversion = 0\n"
	if url != "" {
		body += "[remote \"origin\"]\n\turl = " + url + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(git, "config"), []byte(body), 0o644))
	return dir
}

// worktree writes a linked worktree of main at dir, the way `git worktree add` does:
// a .git FILE pointing into the main checkout's worktrees directory.
func worktree(t *testing.T, main, dir, name string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	gitdir := filepath.Join(main, ".git", "worktrees", name)
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
	return dir
}

func TestIdentityFoldsCloneSpellings(t *testing.T) {
	urls := []string{
		"https://github.com/egladman/magus.git",
		"https://github.com/egladman/magus",
		"git@github.com:egladman/magus.git",
		"ssh://git@github.com:22/egladman/magus.git",
		"https://GitHub.com/egladman/magus.git",
	}
	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			assert.Equal(t, "github.com/egladman/magus", Identity(clone(t, t.TempDir(), url)))
		})
	}
}

// The split this package exists to close: two clones in unrelated directories are one
// repository, and keyed to one state directory.
func TestIdentityIsSharedAcrossClones(t *testing.T) {
	a := clone(t, t.TempDir(), "git@github.com:egladman/magus.git")
	b := clone(t, t.TempDir(), "https://github.com/egladman/magus.git")

	assert.Equal(t, Identity(a), Identity(b))
	assert.Equal(t, Key(Identity(a)), Key(Identity(b)))
	assert.NotEqual(t, Path(a), Path(b), "the paths differ; only the remote makes them one repo")
}

func TestIdentityReadsTheMainCheckoutConfigFromAWorktree(t *testing.T) {
	main := clone(t, t.TempDir(), "git@github.com:egladman/magus.git")
	linked := worktree(t, main, t.TempDir(), "feature")

	assert.Equal(t, "github.com/egladman/magus", Identity(linked))
	assert.Equal(t, main, Path(linked))
}

func TestIdentityFallsBackToPathWithoutARemote(t *testing.T) {
	bare := clone(t, t.TempDir(), "")
	assert.Equal(t, bare, Identity(bare))

	none := t.TempDir()
	assert.Equal(t, none, Identity(none), "no git at all")
}

func TestIdentityPrefersOriginThenFirstByName(t *testing.T) {
	dir := t.TempDir()
	git := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(git, 0o755))
	config := "[remote \"upstream\"]\n\turl = git@github.com:egladman/magus.git\n" +
		"[remote \"origin\"]\n\turl = git@github.com:fork/magus.git\n"
	require.NoError(t, os.WriteFile(filepath.Join(git, "config"), []byte(config), 0o644))
	assert.Equal(t, "github.com/fork/magus", Identity(dir))

	only := t.TempDir()
	onlyGit := filepath.Join(only, ".git")
	require.NoError(t, os.MkdirAll(onlyGit, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(onlyGit, "config"),
		[]byte("[remote \"zeta\"]\n\turl = git@example.com:a/b.git\n[remote \"alpha\"]\n\turl = git@example.com:c/d.git\n"), 0o644))
	assert.Equal(t, "example.com/c/d", Identity(only), "no origin: first remote by name")
}

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/egladman/magus.git": "github.com/egladman/magus",
		"git@github.com:egladman/magus.git":     "github.com/egladman/magus",
		"ssh://git@example.com:2222/a/b.git":    "example.com/a/b",
		"https://user:pass@example.com/a/b":     "example.com/a/b",
		"file:///srv/git/magus.git":             "/srv/git/magus",
		"/srv/git/magus.git":                    "/srv/git/magus",
		"https://example.com":                   "",
		"":                                      "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, normalizeRemote(in))
		})
	}
}

func TestKeyIsDeterministicAndScopedToOneSegment(t *testing.T) {
	key := Key("github.com/egladman/magus")

	assert.Equal(t, key, Key("github.com/egladman/magus"))
	assert.NotEqual(t, key, Key("github.com/someone/magus"), "the digest separates same-named repos")
	assert.NotContains(t, key, string(os.PathSeparator), "a key names one directory, never a path")
	assert.True(t, filepath.IsLocal(key), "a key can never escape the store directory")
}

func TestAdoptCarriesALegacyStoreForward(t *testing.T) {
	base := t.TempDir()
	legacy, dir := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "record.md"), []byte("kept"), 0o644))

	require.NoError(t, Adopt(legacy, dir))

	body, err := os.ReadFile(filepath.Join(dir, "record.md"))
	require.NoError(t, err)
	assert.Equal(t, "kept", string(body))
	assert.NoDirExists(t, legacy)
}

func TestAdoptLeavesAnExistingStoreAlone(t *testing.T) {
	base := t.TempDir()
	legacy, dir := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "record.md"), []byte("legacy"), 0o644))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "record.md"), []byte("current"), 0o644))

	require.NoError(t, Adopt(legacy, dir))

	body, err := os.ReadFile(filepath.Join(dir, "record.md"))
	require.NoError(t, err)
	assert.Equal(t, "current", string(body), "the newer store wins")
	assert.DirExists(t, legacy, "and the older one is left for a human to reconcile")
}

func TestAdoptIsANoOpWithNothingToMove(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new")

	require.NoError(t, Adopt(filepath.Join(base, "absent"), dir))
	require.NoError(t, Adopt(dir, dir))
	assert.NoDirExists(t, dir, "adoption never creates a store; the store's own writer does")
}

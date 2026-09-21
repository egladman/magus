package repo

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

// hgClone writes a plain hg checkout at dir whose default path is url.
func hgClone(t *testing.T, dir, url string) string {
	t.Helper()
	hg := filepath.Join(dir, ".hg")
	require.NoError(t, os.MkdirAll(hg, 0o755))
	body := "[ui]\nusername = someone\n"
	if url != "" {
		body += "[paths]\ndefault = " + url + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(hg, "hgrc"), []byte(body), 0o644))
	return dir
}

// slClone writes a plain Sapling checkout at dir whose default path is url.
func slClone(t *testing.T, dir, url string) string {
	t.Helper()
	sl := filepath.Join(dir, ".sl")
	require.NoError(t, os.MkdirAll(sl, 0o755))
	body := "[ui]\nusername = someone\n"
	if url != "" {
		body += "[paths]\ndefault = " + url + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(sl, "config"), []byte(body), 0o644))
	return dir
}

// jjColocated writes what `jj git init --colocate` leaves: a .jj beside a git checkout
// whose remotes are the ones jj uses.
func jjColocated(t *testing.T, dir, url string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".jj", "repo"), 0o755))
	return clone(t, dir, url)
}

// worktree writes a linked worktree of the repository whose shared git directory is
// common, the way `git worktree add` does: a .git FILE pointing at a per-worktree
// gitdir, and a commondir file inside it naming the shared one.
func worktree(t *testing.T, common, dir, name string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	gitdir := filepath.Join(common, "worktrees", name)
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
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
			assert.Equal(t, "github.com/egladman/magus", identity(clone(t, t.TempDir(), url)))
		})
	}
}

// The split this package exists to close: two clones in unrelated directories are one
// repository, and keyed to one state directory.
func TestIdentityIsSharedAcrossClones(t *testing.T) {
	a := clone(t, t.TempDir(), "git@github.com:egladman/magus.git")
	b := clone(t, t.TempDir(), "https://github.com/egladman/magus.git")

	assert.Equal(t, identity(a), identity(b))
	assert.Equal(t, dirName(identity(a)), dirName(identity(b)))
	assert.NotEqual(t, pathIdentity(a), pathIdentity(b), "the paths differ; only the remote makes them one repo")
}

func TestIdentityReadsTheMainCheckoutConfigFromAWorktree(t *testing.T) {
	main := clone(t, t.TempDir(), "git@github.com:egladman/magus.git")
	linked := worktree(t, filepath.Join(main, ".git"), t.TempDir(), "feature")

	assert.Equal(t, "github.com/egladman/magus", identity(linked))
	assert.Equal(t, main, pathIdentity(linked))
}

// A worktree of a BARE repository has no ".git" segment in its gitdir, so the path rule
// Path must keep using cannot see it. Reading git's own commondir file can.
func TestIdentityResolvesAWorktreeOfABareRepository(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "magus.git")
	require.NoError(t, os.MkdirAll(bare, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bare, "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:egladman/magus.git\n"), 0o644))
	linked := worktree(t, bare, t.TempDir(), "feature")

	assert.Equal(t, "github.com/egladman/magus", identity(linked))
	assert.Equal(t, filepath.Join(bare, "worktrees", "feature"), pathIdentity(linked),
		"the legacy key sees a per-worktree directory here, and keeps that blind spot on purpose")
}

func TestIdentityFallsBackToPathWithoutARemote(t *testing.T) {
	bare := clone(t, t.TempDir(), "")
	assert.Equal(t, bare, identity(bare))

	none := t.TempDir()
	assert.Equal(t, none, identity(none), "no git at all")
}

func TestIdentityPrefersOriginThenFirstByName(t *testing.T) {
	dir := t.TempDir()
	git := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(git, 0o755))
	config := "[remote \"upstream\"]\n\turl = git@github.com:egladman/magus.git\n" +
		"[remote \"origin\"]\n\turl = git@github.com:fork/magus.git\n"
	require.NoError(t, os.WriteFile(filepath.Join(git, "config"), []byte(config), 0o644))
	assert.Equal(t, "github.com/fork/magus", identity(dir))

	only := t.TempDir()
	onlyGit := filepath.Join(only, ".git")
	require.NoError(t, os.MkdirAll(onlyGit, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(onlyGit, "config"),
		[]byte("[remote \"zeta\"]\n\turl = git@example.com:a/b.git\n[remote \"alpha\"]\n\turl = git@example.com:c/d.git\n"), 0o644))
	assert.Equal(t, "example.com/c/d", identity(only), "no origin: first remote by name")
}

// The parity claim, one case per backend: a checkout identifies by its recorded remote
// rather than by where it sits, whichever tool wrote that remote. Before this, only git
// folded, so two hg clones of one repository kept two stores and neither could read what
// the other recorded.
func TestIdentityFoldsClonesUnderEveryBackend(t *testing.T) {
	const url = "https://example.com/acme/widget"
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir, url string) string
	}{
		{"git", clone},
		{"hg", hgClone},
		{"sapling", slClone},
		{"jj colocated", jjColocated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.write(t, t.TempDir(), url)
			b := tc.write(t, t.TempDir(), url)
			require.NotEqual(t, a, b, "the premise: two checkouts at different paths")

			got := identity(a)
			assert.Equal(t, "example.com/acme/widget", got, "the remote is what identifies it")
			assert.Equal(t, got, identity(b), "two clones, one identity")
		})
	}
}

// Without a remote every backend must land on the checkout path, which splits the two
// and is visible, rather than on some shared constant that would merge them.
func TestIdentityFallsBackToPathUnderEveryBackend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir, url string) string
	}{
		{"git", clone},
		{"hg", hgClone},
		{"sapling", slClone},
		{"jj colocated", jjColocated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.write(t, t.TempDir(), "")
			b := tc.write(t, t.TempDir(), "")
			assert.Equal(t, a, identity(a), "no remote: the checkout path is the identity")
			assert.NotEqual(t, identity(a), identity(b), "two unrelated checkouts must not share a store")
		})
	}
}

// A jj workspace that is NOT colocated keeps its store inside .jj, a layout nothing here
// describes. It must fall back to the path rather than guess at a git directory: a guess
// that resolved to the wrong repository would merge two stores, which is unrecoverable.
func TestIdentityFallsBackForANonColocatedJJWorkspace(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".jj", "repo", "store"), 0o755))

	assert.Equal(t, dir, identity(dir))
}

// A tree carrying both .hg and .git identifies by hg, because hg is what drives it
// everywhere else in magus (vcs.builtin probes .jj, .hg, .sl before .git). Two orderings
// would key a store on one backend while the rest of magus used the other.
func TestIdentityFollowsTheDriverClaimOrder(t *testing.T) {
	dir := t.TempDir()
	hgClone(t, dir, "https://example.com/acme/from-hg")
	clone(t, dir, "https://example.com/acme/from-git")

	assert.Equal(t, "example.com/acme/from-hg", identity(dir))
}

func TestRemoteIdentity(t *testing.T) {
	cases := map[string]string{
		"https://github.com/egladman/magus.git": "github.com/egladman/magus",
		"git@github.com:egladman/magus.git":     "github.com/egladman/magus",
		"ssh://git@example.com:2222/a/b.git":    "example.com/a/b",
		"https://user:pass@example.com/a/b":     "example.com/a/b",
		// A password carrying an @ must not leave its tail glued to the host, or one
		// repository's two spellings reduce to two identities.
		"https://user:p@ss@example.com/a/b": "example.com/a/b",
		// A bracketed IPv6 literal keeps its brackets and loses only the port. Cutting
		// at the first colon reduced every one of these to the host "[", which is a
		// COLLISION between unrelated repositories rather than a miss.
		"ssh://git@[2001:db8::1]:22/a/b.git": "[2001:db8::1]/a/b",
		"ssh://[2001:db8::1]/a/b.git":        "[2001:db8::1]/a/b",
		"file:///srv/git/magus.git":          "/srv/git/magus",
		"/srv/git/magus.git":                 "/srv/git/magus",
		// A Windows drive letter is not a host; "c" would be one if the scp-like rule
		// fired on it.
		"C:/repos/magus.git": "",
		// A relative remote names a different repository from every directory it is
		// read in, so it is refused rather than collided onto one store.
		"../shared.git":       "",
		"https://example.com": "",
		"":                    "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, remoteIdentity(in))
		})
	}
}

// The host folds because DNS does; the path does not, because it is only
// case-insensitive on some forges and merging two repositories is the error with no
// recovery.
func TestRemoteIdentityFoldsTheHostAndNotThePath(t *testing.T) {
	assert.Equal(t, "github.com/Egladman/Magus", remoteIdentity("https://GitHub.com/Egladman/Magus.git"))
}

func TestDirNameIsDeterministicAndScopedToOneSegment(t *testing.T) {
	name := dirName("github.com/egladman/magus")

	assert.Equal(t, name, dirName("github.com/egladman/magus"))
	assert.NotEqual(t, name, dirName("github.com/someone/magus"), "the digest separates same-named repos")
	assert.NotContains(t, name, string(os.PathSeparator), "a name is one directory, never a path")
	assert.True(t, filepath.IsLocal(name), "a name can never escape the store directory")
}

// filepath.Base answers "/" and "." for these, and neither may be joined onto the store
// directory. The digest still separates them.
func TestDirNameSurvivesAnUnnameableIdentity(t *testing.T) {
	for _, id := range []string{"/", "", "."} {
		name := dirName(id)
		assert.True(t, filepath.IsLocal(name), "identity %q produced %q", id, name)
	}
	assert.NotEqual(t, dirName("/"), dirName(""))
}

func TestStateDirIsSharedByEveryCloneAndAdoptsTheLegacyKey(t *testing.T) {
	base := t.TempDir()
	a := clone(t, t.TempDir(), "git@github.com:egladman/magus.git")
	b := clone(t, t.TempDir(), "https://github.com/egladman/magus.git")

	legacy := LegacyDir(base, "sessions", a)
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "kept.jsonl"), []byte("{}\n"), 0o644))

	dirA, err := StateDir(base, "sessions", a)
	require.NoError(t, err)
	dirB, err := StateDir(base, "sessions", b)
	require.NoError(t, err)

	assert.Equal(t, dirA, dirB, "two clones, one store")
	assert.FileExists(t, filepath.Join(dirA, "kept.jsonl"), "the legacy store came with it")
	assert.NoDirExists(t, legacy)
	assert.Contains(t, dirA, filepath.Join(base, "magus", "sessions"))
}

// Two kinds of state never share a directory, or one store's listing reads the other's
// records.
func TestStateDirSeparatesKinds(t *testing.T) {
	base, root := t.TempDir(), clone(t, t.TempDir(), "git@github.com:egladman/magus.git")

	sessions, err := StateDir(base, "sessions", root)
	require.NoError(t, err)
	memory, err := StateDir(base, "memory", root)
	require.NoError(t, err)

	assert.NotEqual(t, sessions, memory)
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

// TestCheckoutRelativeReducesHostPathsToTheCheckout pins the join between loaded
// session events and graph file nodes: a host names files absolutely, nodes are keyed
// inside the checkout, and a sibling worktree of the same repository shares that layout.
func TestCheckoutRelativeReducesHostPathsToTheCheckout(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	linked := filepath.Join(main, "wt", "x-1")
	elsewhere := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(main, ".git"), filepath.Join(linked, "internal"), filepath.Join(main, "internal"), filepath.Join(elsewhere, "internal")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	// A linked worktree marks itself with a .git FILE, and it lives under the main
	// checkout, so the nearest checkout above the file must win over the outer one.
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: elsewhere\n"), 0o644))

	for _, tc := range []struct{ in, want string }{
		{"internal/a.go", "internal/a.go"},
		{filepath.Join(main, "internal", "a.go"), "internal/a.go"},
		{filepath.Join(linked, "internal", "a.go"), "internal/a.go"},
		{filepath.Join(elsewhere, "internal", "a.go"), filepath.Join(elsewhere, "internal", "a.go")},
	} {
		assert.Equal(t, tc.want, CheckoutRelative(tc.in), tc.in)
	}
}

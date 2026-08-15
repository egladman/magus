package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAutodetect(t *testing.T) {
	assertAutodetect := func(t *testing.T, claim, want string) {
		t.Helper()
		t.Setenv("MAGUS_VCS_NAME", "")
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, claim), 0o755))
		res, err := Resolve(context.Background(), root, "origin/main", types.VCSOptions{})
		require.NoError(t, err)
		assert.Equal(t, want, res.Name)
		assert.Equal(t, types.VCSSourceAuto, res.Source)
		assert.NotNil(t, res.VCS, "VCS is nil, want non-nil")
	}

	t.Run(".git", func(t *testing.T) { assertAutodetect(t, ".git", "git") })
	t.Run(".hg", func(t *testing.T) { assertAutodetect(t, ".hg", "hg") })
	t.Run(".jj", func(t *testing.T) { assertAutodetect(t, ".jj", "jj") })
	t.Run(".sl", func(t *testing.T) { assertAutodetect(t, ".sl", "sl") })
}

func TestResolveExplicitOverridesAutodetect(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "jj")
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	res, err := Resolve(context.Background(), root, "origin/main", types.VCSOptions{})
	require.NoError(t, err)
	assert.Equal(t, "jj", res.Name)
	assert.Equal(t, types.VCSSourceExplicit, res.Source)
}

func TestResolveExplicitUnknown(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "fossil")
	_, err := Resolve(context.Background(), t.TempDir(), "origin/main", types.VCSOptions{})
	require.Error(t, err, "expected error for unknown VCS name, got nil")
	assert.ErrorIs(t, err, types.ErrVCSUnknown)
}

func TestResolveDefaultWhenNoMarker(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "")
	res, err := Resolve(context.Background(), t.TempDir(), "origin/main", types.VCSOptions{})
	require.NoError(t, err)
	assert.Equal(t, "git", res.Name)
	assert.Equal(t, types.VCSSourceDefault, res.Source)
}

func TestResolveDisabled(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	res, err := Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.VCSSourceDisabled, res.Source)
	assert.Nil(t, res.VCS, "VCS, want nil")
}

func TestResolvePerVCSBaseRef(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "")
	t.Setenv("MAGUS_VCS_BASE_REF", "")
	t.Setenv("MAGUS_VCS_NAME", "jj")
	t.Setenv("MAGUS_VCS_JJ_BASE_REF", "main@origin")
	res, err := Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
	require.NoError(t, err)
	assert.Equal(t, "main@origin", res.Base)
}

func TestResolveBuiltinBaseRefs(t *testing.T) {
	assertBuiltinBase := func(t *testing.T, name, want string) {
		t.Helper()
		t.Setenv("MAGUS_VCS_ENABLED", "")
		t.Setenv("MAGUS_VCS_BASE_REF", "")
		t.Setenv("MAGUS_VCS_NAME", name)
		t.Setenv(perVCSEnv(name, "BASE_REF"), "")
		res, err := Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
		require.NoError(t, err)
		assert.Equal(t, want, res.Base)
	}

	t.Run("git", func(t *testing.T) { assertBuiltinBase(t, "git", "origin/main") })
	// hg's is the "default" branch, NOT tip: tip is whatever commit is newest locally,
	// including your own, so it compared a branch against itself and affected built nothing.
	t.Run("hg", func(t *testing.T) { assertBuiltinBase(t, "hg", "default") })
	t.Run("jj", func(t *testing.T) { assertBuiltinBase(t, "jj", "trunk()") })
	// Sapling's is the remote bookmark, not hg's "tip": Sapling has no branches, and tip
	// names whatever commit is newest LOCALLY - including your own unpushed work, which
	// would make `magus affected` compare a branch against itself.
	t.Run("sl", func(t *testing.T) { assertBuiltinBase(t, "sl", "remote/main") })
}

func TestVCSClaims(t *testing.T) {
	assertClaims := func(t *testing.T, name string, want []string) {
		t.Helper()
		t.Setenv("MAGUS_VCS_NAME", name)
		res, err := Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
		require.NoErrorf(t, err, "Resolve(%q)", name)
		assert.Equal(t, want, res.VCS.Claims())
	}

	t.Run("git", func(t *testing.T) { assertClaims(t, "git", []string{".git"}) })
	t.Run("hg", func(t *testing.T) { assertClaims(t, "hg", []string{".hg"}) })
	t.Run("jj", func(t *testing.T) { assertClaims(t, "jj", []string{".jj"}) })
	t.Run("sl", func(t *testing.T) { assertClaims(t, "sl", []string{".sl"}) })
}

func TestDirtyGitPathScoped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%v: %s", args, out)
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "test@example.com")
	mustRun("git", "config", "user.name", "Test")
	mustRun("git", "config", "commit.gpgsign", "false")
	write("a.txt", "alpha\n")
	write("sub/b.txt", "beta\n")
	mustRun("git", "add", "-A")
	mustRun("git", "commit", "-m", "init")

	t.Setenv("MAGUS_VCS_NAME", "git")
	res, err := Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	v := res.VCS

	dirty := func(paths ...string) bool {
		t.Helper()
		d, err := v.Dirty(context.Background(), dir, paths)
		require.NoError(t, err)
		return d
	}

	// Clean tree: nothing dirty, repo-wide or path-scoped.
	assert.False(t, dirty(), "clean repo should not be dirty")
	assert.False(t, dirty("a.txt"), "clean path should not be dirty")

	// Modify only a.txt.
	write("a.txt", "alpha changed\n")
	assert.True(t, dirty(), "modified tree should be dirty repo-wide")
	assert.True(t, dirty("a.txt"), "modified path should be dirty")
	assert.False(t, dirty("sub/b.txt"), "unmodified path must not report dirty (scoping)")
	assert.True(t, dirty("sub/b.txt", "a.txt"), "any matching path dirty -> dirty")
}

func TestDiffCommandsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%v: %s", args, out)
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "test@example.com")
	mustRun("git", "config", "user.name", "Test")
	mustRun("git", "config", "commit.gpgsign", "false")
	mustRun("git", "commit", "--allow-empty", "-m", "init")

	// Capture the SHA we just created.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	wantSHA := strings.TrimSpace(string(out))

	t.Setenv("MAGUS_VCS_NAME", "git")
	res, err := Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err, "Resolve")

	hints, err := res.VCS.DiffCommands(t.Context(), dir, "origin/main")
	require.NoError(t, err, "DiffCommands")

	assert.Equal(t, "git diff origin/main..."+wantSHA, hints.CLI)
	assert.Equal(t, "git difftool origin/main..."+wantSHA, hints.GUI)
}

// TestDiffGitIncludesWorkingTree reproduces the "0 projects affected" bug: with
// HEAD == base (changes only in the working tree, nothing committed), the old
// base...HEAD diff was empty. Diff must surface both an uncommitted edit to a
// tracked file and a brand-new untracked file.
func TestDiffGitIncludesWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%v: %s", args, out)
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "test@example.com")
	mustRun("git", "config", "user.name", "Test")
	mustRun("git", "config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("noise\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644))
	mustRun("git", "add", "tracked.txt", ".gitignore")
	mustRun("git", "commit", "-m", "init")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	base := strings.TrimSpace(string(out)) // HEAD == base: base...HEAD would be empty

	// Modify a tracked file (uncommitted) and add a new untracked file; no new commit.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644))

	files, err := gitVCS{}.ChangedFiles(context.Background(), dir, base)
	require.NoError(t, err, "Diff")
	assert.Contains(t, files, "tracked.txt", "uncommitted edit to a tracked file must be in the diff")
	assert.Contains(t, files, "new.txt", "untracked new file must be in the diff")
	assert.NotContains(t, files, "ignored.log", "gitignored file must stay out of the diff")
}

func TestFindCommitAndHistoryGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%v: %s", args, out)
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "alice@example.com")
	mustRun("git", "config", "user.name", "Alice")
	mustRun("git", "config", "commit.gpgsign", "false")
	mustRun("git", "commit", "--allow-empty", "-m", "first")
	mustRun("git", "commit", "--allow-empty", "-m", "second line\n\nbody text")

	res, err := Resolve(context.Background(), dir, "", types.VCSOptions{Name: "git"})
	require.NoError(t, err, "Resolve")

	c, err := res.VCS.FindCommit(context.Background(), dir, "")
	require.NoError(t, err, "FindCommit")
	assert.Equal(t, "second line", c.Subject)
	assert.Equal(t, "body text", c.Body)
	assert.Equal(t, "Alice", c.Author.Name)
	assert.Equal(t, "alice@example.com", c.Author.Email)
	assert.False(t, c.Date.IsZero(), "Date is zero; expected a parsed RFC3339 record date")
	assert.NotEmpty(t, c.ID)
	assert.NotEmpty(t, c.Short)
	assert.Truef(t, strings.HasPrefix(c.ID, c.Short), "ID/Short inconsistent: %q / %q", c.ID, c.Short)

	hist, err := res.VCS.History(context.Background(), dir, 10)
	require.NoError(t, err, "History")
	require.Len(t, hist, 2)
	assert.Equal(t, "second line", hist[0].Subject, "History order wrong (want newest first)")
	assert.Equal(t, "first", hist[1].Subject, "History order wrong (want newest first)")
}

func TestInstallableAndInstaller(t *testing.T) {
	names := InstallableVCSes()
	// git, hg and sl implement MergeDriverInstaller; jj does not. Sapling registers the
	// driver in .sl/config, the same [merge-patterns]/[merge-tools] pair hg writes to
	// .hg/hgrc.
	want := map[string]bool{"git": true, "hg": true, "sl": true}
	require.Lenf(t, names, len(want), "Installable() = %v, want keys %v", names, want)
	for _, n := range names {
		assert.Truef(t, want[n], "Installable() returned unexpected %q", n)
		_, ok := Installer(n)
		assert.Truef(t, ok, "Installer(%q): got !ok, want an installer", n)
	}

	// jj is a known VCS but exposes no merge-driver installer.
	_, ok := Installer("jj")
	assert.False(t, ok, "Installer(\"jj\"): got ok, want false (no installer)")
	// Unknown VCS name yields no installer.
	_, ok = Installer("svn")
	assert.False(t, ok, "Installer(\"svn\"): got ok, want false (unknown VCS)")
}

func TestDiffRejectsFlagLikeBase(t *testing.T) {
	drivers := []types.VCSDriver{gitVCS{}, hgVCS{}, jjVCS{}}
	for _, v := range drivers {
		_, err := v.ChangedFiles(context.Background(), t.TempDir(), "-rf")
		require.Errorf(t, err, "%s.Diff with flag-like base should error", v.Name())
		assert.Containsf(t, err.Error(), "looks like a flag", "%s.Diff error", v.Name())
	}
}

func TestFindCommitRejectsFlagLikeRev(t *testing.T) {
	drivers := []types.VCSDriver{gitVCS{}, hgVCS{}, jjVCS{}}
	for _, v := range drivers {
		_, err := v.FindCommit(context.Background(), t.TempDir(), "-rf")
		require.Errorf(t, err, "%s.FindCommit with flag-like rev should error", v.Name())
		assert.Containsf(t, err.Error(), "looks like a flag", "%s.FindCommit error", v.Name())
	}
}

func TestBisectRejectsFlagLikeRev(t *testing.T) {
	drivers := []types.VCSDriver{gitVCS{}, hgVCS{}}
	for _, v := range drivers {
		_, err := v.Bisect(context.Background(), t.TempDir(), types.BisectOptions{Good: "-rf"})
		require.Errorf(t, err, "%s.Bisect with flag-like good should error", v.Name())
		assert.Containsf(t, err.Error(), "looks like a flag", "%s.Bisect good error", v.Name())

		_, err = v.Bisect(context.Background(), t.TempDir(), types.BisectOptions{Bad: "-rf"})
		require.Errorf(t, err, "%s.Bisect with flag-like bad should error", v.Name())
		assert.Containsf(t, err.Error(), "looks like a flag", "%s.Bisect bad error", v.Name())
	}
}

func TestDescribeGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%v: %s", args, out)
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "alice@example.com")
	mustRun("git", "config", "user.name", "Alice")
	mustRun("git", "config", "commit.gpgsign", "false")
	mustRun("git", "commit", "--allow-empty", "-m", "first")

	res, err := Resolve(context.Background(), dir, "", types.VCSOptions{Name: "git"})
	require.NoError(t, err, "Resolve")
	ctx := context.Background()

	// No tag yet: --always falls back to the short hash on a clean tree.
	d, err := res.VCS.Describe(ctx, dir)
	require.NoError(t, err)
	require.NotEmpty(t, d, "describe should fall back to a short hash when untagged")
	assert.NotContains(t, d, "-dirty", "clean tree must not be marked dirty")

	// Tagged: describe reports the tag.
	mustRun("git", "tag", "v1.2.3")
	d, err = res.VCS.Describe(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", d)

	// Dirty tree: -dirty suffix.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
	mustRun("git", "add", "f.txt")
	d, err = res.VCS.Describe(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3-dirty", d)
}

// TestIsSecondaryCheckout exercises each backend's on-disk signature for a second
// checkout of a repo, plus the negatives (primary checkout, submodule, bare dir).
// The signatures are constructed directly, so the test needs none of the tools
// installed - it validates the detection, not the VCS.
func TestIsSecondaryCheckout(t *testing.T) {
	mkfile := func(t *testing.T, path, body string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	mkdir := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(path, 0o755))
	}

	t.Run("git linked worktree", func(t *testing.T) {
		dir := t.TempDir()
		mkfile(t, filepath.Join(dir, ".git"), "gitdir: /repo/.git/worktrees/feature\n")
		assert.True(t, IsSecondaryCheckout(dir))
		assert.True(t, gitVCS{}.IsSecondaryCheckout(dir))
	})

	t.Run("git submodule stays discoverable", func(t *testing.T) {
		dir := t.TempDir()
		mkfile(t, filepath.Join(dir, ".git"), "gitdir: /repo/.git/modules/libfoo\n")
		assert.False(t, IsSecondaryCheckout(dir))
	})

	t.Run("git primary checkout (.git dir)", func(t *testing.T) {
		dir := t.TempDir()
		mkdir(t, filepath.Join(dir, ".git"))
		assert.False(t, IsSecondaryCheckout(dir))
	})

	t.Run("hg share", func(t *testing.T) {
		dir := t.TempDir()
		mkfile(t, filepath.Join(dir, ".hg", "sharedpath"), "/repo/.hg\n")
		assert.True(t, IsSecondaryCheckout(dir))
		assert.True(t, hgVCS{}.IsSecondaryCheckout(dir))
	})

	t.Run("hg standalone repo", func(t *testing.T) {
		dir := t.TempDir()
		mkdir(t, filepath.Join(dir, ".hg"))
		assert.False(t, IsSecondaryCheckout(dir))
	})

	t.Run("jj secondary workspace (.jj/repo file)", func(t *testing.T) {
		dir := t.TempDir()
		mkfile(t, filepath.Join(dir, ".jj", "repo"), "/repo/.jj/repo\n")
		assert.True(t, IsSecondaryCheckout(dir))
		assert.True(t, jjVCS{}.IsSecondaryCheckout(dir))
	})

	t.Run("jj primary workspace (.jj/repo dir)", func(t *testing.T) {
		dir := t.TempDir()
		mkdir(t, filepath.Join(dir, ".jj", "repo"))
		assert.False(t, IsSecondaryCheckout(dir))
	})

	t.Run("plain directory", func(t *testing.T) {
		assert.False(t, IsSecondaryCheckout(t.TempDir()))
	})
}

// parseTagLines is the shared boundary between two backends' very different tag
// commands, so the cases that matter are the malformed ones: a backend that
// omits a field must not drop the tag or shift its columns.
func TestParseTagLines(t *testing.T) {
	t.Parallel()

	got, err := parseTags("v0.3.0\t2026-07-25T10:14:05-04:00\tabc123\n"+
		"no-date\t\tdef456\n"+
		"bad-date\tnot-a-timestamp\tghi789\n"+
		"\t2026-01-01T00:00:00Z\tskipped\n"+
		"name-only", "")
	require.NoError(t, err)

	want := []types.VCSTag{
		{Name: "v0.3.0", Version: types.SemverVersion{Major: 0, Minor: 3, Patch: 0, Original: "v0.3.0"}, Date: time.Date(2026, 7, 25, 10, 14, 5, 0, time.FixedZone("", -4*60*60)), ID: "abc123"},
		{Name: "no-date", ID: "def456"},
		{Name: "bad-date", ID: "ghi789"},
	}
	require.Len(t, got, len(want), "a nameless line is skipped; a line with no tab is not a tag")
	for i := range want {
		require.Equal(t, want[i].Name, got[i].Name)
		require.Equal(t, want[i].ID, got[i].ID)
		require.Equal(t, want[i].Version, got[i].Version, "tag %q version", want[i].Name)
		require.True(t, want[i].Date.Equal(got[i].Date), "tag %q date", want[i].Name)
	}
}

// TestParseTagsSplitsVersion is the dedicated Prefix/Version coverage the
// table above only samples: a nested-module tag splits its module path from
// the version, a root tag has no prefix, and a non-semver tag (a legitimate
// annotation, not an error) leaves Version at its zero value.
func TestParseTagsSplitsVersion(t *testing.T) {
	t.Parallel()

	got, err := parseTags("libs/gopherbuzz/v0.1.0\t\ta\n"+
		"v0.3.0\t\tb\n"+
		"checkpoint\t\tc\n", "")
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, types.VCSTag{
		Name:    "libs/gopherbuzz/v0.1.0",
		Prefix:  "libs/gopherbuzz/",
		Version: types.SemverVersion{Major: 0, Minor: 1, Patch: 0, Original: "v0.1.0"},
		ID:      "a",
	}, got[0], "nested-module tag: prefix through the final /, version is the remainder")

	assert.Equal(t, types.VCSTag{
		Name:    "v0.3.0",
		Version: types.SemverVersion{Major: 0, Minor: 3, Patch: 0, Original: "v0.3.0"},
		ID:      "b",
	}, got[1], "root tag: empty prefix")

	assert.Equal(t, types.VCSTag{
		Name: "checkpoint",
		ID:   "c",
	}, got[2], "non-semver tag: zero Version, not an error")
}

func TestParseTagLinesEmpty(t *testing.T) {
	t.Parallel()
	got, err := parseTags("", "")
	require.NoError(t, err)
	require.Nil(t, got, "no tags is nil, not a one-element slice of blank")
}

// The whole point of the pattern is separating releases from namespaced tags a
// repository accumulates (backup/, pre-rebase-*), so that separation is the test.
func TestParseTagsPattern(t *testing.T) {
	t.Parallel()

	const lines = "v0.3.0\t\ta\nbackup/pre-reword\t\tb\npre-rebase-main\t\tc\nv0.1.0\t\td\n"

	got, err := parseTags(lines, "v*")
	require.NoError(t, err)
	names := make([]string, len(got))
	for i, tag := range got {
		names[i] = tag.Name
	}
	require.Equal(t, []string{"v0.3.0", "v0.1.0"}, names,
		`"v*" keeps releases; its wildcard stops at "/" so backup/pre-reword is excluded`)

	all, err := parseTags(lines, "")
	require.NoError(t, err)
	require.Len(t, all, 4, "an empty pattern filters nothing")

	_, err = parseTags(lines, "v[")
	require.Error(t, err, "a malformed glob is a caller bug, not a silent match-nothing")
}

// Metadata is what puts a revision into every output ref, so `magus x <ref>` can say
// which commit reproduces a run recorded on another machine. That contract belongs to
// the DRIVER interface, not to git: a backend returning an empty ID silently degrades
// every ref it touches to "unknown revision", and nothing else would notice.
//
// Each backend is skipped when its binary is absent rather than failing, so the suite
// still means something on a machine with only git.
//
// The driver is named through VCSOptions.Name. Resolve's second parameter is a base
// REF, not a name - passing "jj" there silently autodetects instead, which is what
// once made this look like a broken jj driver.
func TestMetadataReportsRevisionAcrossBackends(t *testing.T) {
	for _, tc := range []struct {
		name string
		bin  string
		init func(t *testing.T, dir string)
	}{
		{
			name: "git",
			bin:  "git",
			init: func(t *testing.T, dir string) { gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"}) },
		},
		{
			name: "hg",
			bin:  "hg",
			init: func(t *testing.T, dir string) {
				vcsTestRun(t, dir, "hg", "init")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644))
				vcsTestRun(t, dir, "hg", "add", "a.txt")
				vcsTestRun(t, dir, "hg", "commit", "-m", "init", "-u", "test")
			},
		},
		{
			name: "jj",
			bin:  "jj",
			init: func(t *testing.T, dir string) {
				vcsTestRun(t, dir, "jj", "git", "init")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644))
				// jj's working copy IS a commit, so an edit lands in @ and shows as a
				// diff against its parent. `jj new` closes it and starts an empty one,
				// which is jj's equivalent of a clean tree.
				vcsTestRun(t, dir, "jj", "new")
			},
		},
		{
			name: "sl",
			bin:  "sl",
			init: func(t *testing.T, dir string) { slInitRepo(t, dir, map[string]string{"a.txt": "one\n"}) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.bin); err != nil {
				t.Skipf("%s not available", tc.bin)
			}
			dir := t.TempDir()
			tc.init(t, dir)

			res, err := Resolve(t.Context(), dir, "", types.VCSOptions{Name: tc.name})
			require.NoError(t, err, "Resolve")
			require.NotNil(t, res.VCS, "no driver detected for a %s repo", tc.name)

			meta, err := res.VCS.Metadata(t.Context(), dir)
			require.NoError(t, err, "Metadata")
			assert.NotEmpty(t, meta.ID,
				"%s reported no revision; every output ref from this backend would say 'unknown'", tc.name)
			assert.False(t, meta.IsDirty, "a freshly committed tree is not dirty")
		})
	}
}

// The dirty bit is the honesty flag on a ref: it is what tells a reader the recorded
// revision alone cannot reproduce the run. A backend that never sets it makes every
// ref look exactly reproducible.
func TestMetadataReportsDirtyAcrossBackends(t *testing.T) {
	for _, tc := range []struct {
		name string
		bin  string
		init func(t *testing.T, dir string)
	}{
		{"git", "git", func(t *testing.T, dir string) { gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"}) }},
		{"hg", "hg", func(t *testing.T, dir string) {
			vcsTestRun(t, dir, "hg", "init")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644))
			vcsTestRun(t, dir, "hg", "add", "a.txt")
			vcsTestRun(t, dir, "hg", "commit", "-m", "init", "-u", "test")
		}},
		{"sl", "sl", func(t *testing.T, dir string) { slInitRepo(t, dir, map[string]string{"a.txt": "one\n"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.bin); err != nil {
				t.Skipf("%s not available", tc.bin)
			}
			dir := t.TempDir()
			tc.init(t, dir)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))

			res, err := Resolve(t.Context(), dir, "", types.VCSOptions{Name: tc.name})
			require.NoError(t, err, "Resolve")
			require.NotNil(t, res.VCS)

			meta, err := res.VCS.Metadata(t.Context(), dir)
			require.NoError(t, err, "Metadata")
			assert.True(t, meta.IsDirty,
				"%s did not report an uncommitted edit; refs would claim exact reproducibility they cannot deliver", tc.name)
		})
	}
}

// vcsTestRun runs one VCS command in dir, skipping the test when the tool refuses to
// initialize (a sandbox with no writable config home, say) rather than failing.
func vcsTestRun(t *testing.T, dir, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("%s %v failed: %v\n%s", bin, args, err, out)
	}
}

// A colocated jj workspace satisfies git's claim too, because `jj git init` writes
// .git as jj's storage backend. Autodetect has to answer jj, or magus records git's
// HEAD - which lags jj's working-copy commit until refs sync - as the revision an
// output ref reproduces from, describing a tree other than the one built.
func TestAutodetectPrefersJJInAColocatedRepo(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
	dir := t.TempDir()
	vcsTestRun(t, dir, "jj", "git", "init")
	require.DirExists(t, filepath.Join(dir, ".jj"))
	require.DirExists(t, filepath.Join(dir, ".git"), "jj git init is expected to colocate; this test is meaningless otherwise")

	res, err := Resolve(t.Context(), dir, "", types.VCSOptions{})
	require.NoError(t, err, "Resolve")
	assert.Equal(t, "jj", res.Name, "a repo with .jj is driven by jj, whatever else it contains")
	assert.Equal(t, types.VCSSourceAuto, res.Source)
}

// git stays the answer where it is the only marker, and where there is none at all.
func TestAutodetectKeepsGitForPlainRepositoriesAndAsTheDefault(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.txt": "one\n"})
	res, err := Resolve(t.Context(), repo, "", types.VCSOptions{})
	require.NoError(t, err, "Resolve")
	assert.Equal(t, "git", res.Name)
	assert.Equal(t, types.VCSSourceAuto, res.Source)

	// No marker at all: git is the fallback, and reordering builtin must not have
	// quietly made jj the default for every unversioned directory.
	bare, err := Resolve(t.Context(), t.TempDir(), "", types.VCSOptions{})
	require.NoError(t, err, "Resolve")
	assert.Equal(t, "git", bare.Name, "an unversioned directory defaults to git")
	assert.Equal(t, types.VCSSourceDefault, bare.Source)
}

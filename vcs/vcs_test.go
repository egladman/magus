package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// names whatever commit is newest LOCALLY, including your own unpushed work, which
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

	hist, err := res.VCS.History(context.Background(), dir, types.HistoryQuery{Limit: 10})
	require.NoError(t, err, "History")
	require.Len(t, hist, 2)
	assert.Equal(t, c, hist[0], "History's one-pass parse must agree with FindCommit on an empty commit")
	assert.Equal(t, "first", hist[1].Subject, "History order wrong (want newest first)")
}

func TestInstallableAndInstaller(t *testing.T) {
	names := InstallableVCSes()
	// Every backend implements MergeDriverInstaller. Sapling registers the driver in
	// .sl/config, the same [merge-patterns]/[merge-tools] pair hg writes to .hg/hgrc, and
	// jj a merge tool in the repository's config for `jj resolve`.
	want := map[string]bool{"git": true, "hg": true, "sl": true, "jj": true}
	require.Lenf(t, names, len(want), "Installable() = %v, want keys %v", names, want)
	for _, n := range names {
		assert.Truef(t, want[n], "Installable() returned unexpected %q", n)
		_, ok := Installer(n)
		assert.Truef(t, ok, "Installer(%q): got !ok, want an installer", n)
	}

	// Unknown VCS name yields no installer.
	_, ok := Installer("svn")
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
// installed: it validates the detection, not the VCS.
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
// REF, not a name; passing "jj" there silently autodetects instead, which is what
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
// vcsTestOutput runs a backend command and returns its stdout. Unlike vcsTestRun it
// FAILS rather than skips: its callers are reading back something a driver just claimed
// to have written, so a command that will not run is a result, not a missing tool.
func vcsTestOutput(t *testing.T, dir, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoErrorf(t, err, "%s %v", bin, args)
	return string(out)
}

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
// HEAD (which lags jj's working-copy commit until refs sync) as the revision an
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

// ExampleResolve shows how Resolve picks the active VCS and base ref for a
// workspace root. Resolve is typically called once per magus invocation and its
// result is cached in the Workspace.
//
// With no VCS markers on disk and no overrides, Resolve does not error: it falls
// back to the built-in default driver (git) with source "default" and git's
// default base ref. The MAGUS_VCS_* environment variables are cleared first so
// the output does not depend on the caller's environment.
func ExampleResolve() {
	for _, k := range []string{
		"MAGUS_VCS_ENABLED",
		"MAGUS_VCS_NAME",
		"MAGUS_VCS_BASE_REF",
		"MAGUS_VCS_GIT_BASE_REF",
	} {
		_ = os.Unsetenv(k)
	}

	// A path with no .git/.hg/.jj marker, so auto-detection finds nothing and
	// Resolve uses the default driver.
	root := "/nonexistent/path"

	res, err := Resolve(context.Background(), root, "", types.VCSOptions{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("source:", res.Source)
	fmt.Println("vcs:", res.Name)
	fmt.Println("base:", res.Base)

	// Output:
	// source: default
	// vcs: git
	// base: origin/main
}

// The TestParity tests assert the invariants every backend in builtin must share, as one
// table run against all four. They live here, beside the list of backends, because the
// per-backend files cannot hold them: a rule stated only in git_test.go is a rule the next
// backend is free to break, and three of the defects they pin shipped exactly that way:
// each backend was tested against its own behavior rather than against the contract.
//
// A backend whose binary is absent SKIPS rather than failing, so the suite still means
// something on a machine with only git. That is also its weakness: CI installs only git, so
// the other three are pinned by whoever runs this locally.
//
// TestParityCoversEveryBuiltinBackend holds parityBackends to builtin, so a new backend
// cannot ship outside the suite.

// TestParityCoversEveryBuiltinBackend pins parityBackends to builtin: every backend
// Resolve can choose runs the parity table, and the table names none it cannot.
func TestParityCoversEveryBuiltinBackend(t *testing.T) {
	var want, got []string
	for _, v := range builtin {
		want = append(want, v.Name())
	}
	for _, b := range parityBackends() {
		got = append(got, b.drv.Name())
	}
	assert.ElementsMatch(t, want, got)
}

// parityBackend is one driver plus what a test needs to build a repository for it.
type parityBackend struct {
	name string
	bin  string
	drv  types.VCSDriver
	// init creates a repository in dir holding files, with everything committed.
	init func(t *testing.T, dir string, files map[string]string)
	// readback returns what a Preserve handle holds for one path. Nothing in VCSDriver
	// resolves a handle, and without this a Preserve returning a constant would satisfy
	// every other assertion here.
	readback func(t *testing.T, dir, handle, path string) string
	// listPreserved names what MAGUS has minted in dir, in this backend's own store. A
	// pruner that reports the right handles and deletes nothing passes without it.
	listPreserved func(t *testing.T, dir string) []string
	// mints says whether Preserve leaves an object behind at all, and prunes whether magus
	// can then drop it. They are separate fields because sl is the case where they differ:
	// collapse them and sl reads as minting nothing, so every prune assertion compares nil
	// to nil while Preserve mints a hidden commit per call that nothing ever removes.
	mints  bool
	prunes bool
}

func parityBackends() []parityBackend {
	return []parityBackend{
		{"git", "git", gitVCS{}, gitInitRepo, gitReadback, gitListPreserved, true, true},
		{"hg", "hg", hgVCS{}, hgInitRepo, hgReadback, hgListPreserved, true, true},
		{"sl", "sl", saplingVCS{}, slInitRepo, slReadback, slListPreserved, true, false},
		{"jj", "jj", jjVCS{}, jjInitRepo, jjReadback, mintsNothing, false, false},
	}
}

func hgInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	vcsTestRun(t, dir, "hg", "init")
	for name, body := range files {
		writeRepoFile(t, dir, name, body)
		vcsTestRun(t, dir, "hg", "add", name)
	}
	vcsTestRun(t, dir, "hg", "commit", "-m", "init", "-u", "test")
}

func jjInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	vcsTestRun(t, dir, "jj", "git", "init")
	for name, body := range files {
		writeRepoFile(t, dir, name, body)
	}
	// jj's working copy IS a commit, so the writes above land in @. `jj new` closes it and
	// starts an empty one, which is jj's equivalent of a clean tree.
	vcsTestRun(t, dir, "jj", "new")
}

// appendRepoFile adds to a file the backend already wrote, rather than replacing it.
func appendRepoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer fh.Close()
	_, werr := fh.WriteString(body)
	require.NoError(t, werr)
}

func writeRepoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// eachBackend runs fn against every backend whose binary is installed.
func eachBackend(t *testing.T, fn func(t *testing.T, b parityBackend)) {
	t.Helper()
	for _, b := range parityBackends() {
		t.Run(b.name, func(t *testing.T) {
			if _, err := exec.LookPath(b.bin); err != nil {
				t.Skipf("%s not available", b.bin)
			}
			fn(t, b)
		})
	}
}

// Every backend implements RevisionFileReader, and "" means the committed revision in each
// backend's own spelling: HEAD, `.`, `.`, `@`. A caller asking for the committed side has
// no way to name that portably, so the empty default is the portability, and a backend that
// resolved "" to something else would silently hand back the wrong content.
//
// `magus vcs resolve` reads the committed magusfile this way when the working copy's is
// mid-merge, so a backend answering with the CONFLICTED text would defeat the whole point:
// it would parse as badly as the file on disk.
func TestParityReadFileAtReturnsCommittedContent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"magusfile.buzz": "committed\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("working\n"), 0o644))

		got, err := reader.ReadFileAt(t.Context(), dir, "", "magusfile.buzz")
		require.NoErrorf(t, err, "%s ReadFileAt", b.name)
		assert.Equalf(t, "committed\n", got,
			"%s returned the working copy, not the committed revision", b.name)
	})
}

// Content comes back EXACTLY. The shared output helpers trim, which is right for a status
// line and wrong for a file: a magusfile whose trailing newline was eaten is not the file
// the revision holds, and this is the capability whose whole promise is that it is.
func TestParityReadFileAtDoesNotTrim(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader := b.drv

		dir := t.TempDir()
		const body = "  leading\n\ntrailing blank line\n\n"
		b.init(t, dir, map[string]string{"a.txt": body})

		got, err := reader.ReadFileAt(t.Context(), dir, "", "a.txt")
		require.NoErrorf(t, err, "%s ReadFileAt", b.name)
		assert.Equalf(t, body, got, "%s trimmed the content", b.name)
	})
}

// A path the revision does not hold is an ERROR, not empty content. A caller building a
// magusfile overlay would otherwise load an empty magusfile and report a workspace with no
// projects rather than a file it could not read.
func TestParityReadFileAtMissingPathErrors(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		_, err := reader.ReadFileAt(t.Context(), dir, "", "nosuchfile.buzz")
		require.Errorf(t, err, "%s reported no error for a path absent at that revision", b.name)
	})
}

// DirtyFiles returns PATHS, not the backend's status lines. Each backend prints a different
// prefix (git two columns, hg and sl one, jj none), and callers hand the result straight to
// glob matching and staging, so a line that keeps its "M " matches nothing and the file is
// silently treated as undeclared.
func TestParityDirtyFilesReturnsPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))

		got, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		assert.Contains(t, got, "a.txt",
			"%s returned %q; a status prefix left on the line matches no glob", b.name, got)
	})
}

// Every backend reports paths relative to the REPOSITORY ROOT, whatever directory the
// probe runs in. Callers stamp the root as the base (std/vcs.go, std/magus.go), so a
// cwd-relative answer names a different file that frequently exists: there is nothing to
// error on, the wrong file is simply read.
//
// This is the single most valuable assertion in the file: sl and jj BOTH failed it, in
// opposite ways that neither backend's own tests could see. sl needed --root-relative; jj
// has no such flag and had to be run from the root.
func TestParityPathsAreRepositoryRelative(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"root.txt": "r\n", "sub/a.txt": "s\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("changed\n"), 0o644))

		got, err := b.drv.DirtyFiles(t.Context(), filepath.Join(dir, "sub"), nil)
		require.NoError(t, err, "DirtyFiles from a subdirectory")
		assert.Contains(t, got, "sub/a.txt",
			"%s probed from sub/ returned %q, not repo-relative paths", b.name, got)
		for _, p := range got {
			assert.NotContains(t, p, "..",
				"%s returned %q, a path escaping the directory it was asked about", b.name, p)
		}
	})
}

// A commit on linear history has exactly one parent. Reporting none makes it read as a
// root commit at the Buzz boundary, and makes len(Parents) > 1 merge detection permanently
// false. hg failed this: its `parents` template keyword filters through meaningfulparents
// and emits nothing off a merge, while sl's same-named keyword does not, so the two
// backends sharing one template disagreed, and only hg was wrong.
func TestParityLinearCommitHasOneParent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
		commitAll(t, b, dir, "second")

		c, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoError(t, err, "FindCommit")
		require.NotEmpty(t, c.ID)
		assert.Len(t, c.Parents, 1,
			"%s reported %d parents for a linear commit; zero reads as a root commit", b.name, len(c.Parents))
	})
}

// Metadata is what stamps a revision into every output ref. An empty ID degrades every ref
// that backend touches to "unknown revision", and a dirty tree reported clean makes a ref
// claim a reproducibility it cannot deliver.
func TestParityMetadataReportsRevisionAndDirt(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		clean, err := b.drv.Metadata(t.Context(), dir)
		require.NoError(t, err, "Metadata")
		assert.NotEmpty(t, clean.ID, "%s recorded no revision", b.name)
		assert.NotEmpty(t, clean.Short, "%s recorded no short revision", b.name)
		assert.False(t, clean.IsDirty, "%s called a freshly committed tree dirty", b.name)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
		dirty, err := b.drv.Metadata(t.Context(), dir)
		require.NoError(t, err)
		assert.True(t, dirty.IsDirty, "%s did not report an uncommitted edit", b.name)
	})
}

// Ref is Metadata's Ref without the rest, so it must agree with Metadata on the value AND
// on failing: hack/git-hooks/commit-msg.buzz catches vcs\ref raising on git's unborn
// branch, and a Ref that answered there would skip that fallback.
func TestParityRefAgreesWithMetadata(t *testing.T) {
	agree := func(t *testing.T, b parityBackend, dir, state string) string {
		t.Helper()
		meta, metaErr := b.drv.Metadata(t.Context(), dir)
		ref, refErr := b.drv.Ref(t.Context(), dir)
		require.Equalf(t, metaErr != nil, refErr != nil,
			"%s %s: Metadata error %v, Ref error %v", b.name, state, metaErr, refErr)
		assert.Equalf(t, meta.Ref, ref, "%s %s", b.name, state)
		return ref
	}
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		nameRef(t, b, dir, "work")
		assert.Equal(t, "work", agree(t, b, dir, "on a named ref"))

		leaveRef(t, b, dir)
		ref := agree(t, b, dir, "off the named ref")
		if b.name != "hg" {
			assert.Emptyf(t, ref, "%s names a ref the revision no longer carries", b.name)
		}

		// A subdirectory resolves the same repository.
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
		agree(t, b, filepath.Join(dir, "sub"), "from a subdirectory")

		empty := t.TempDir()
		initEmpty(t, b, empty)
		agree(t, b, empty, "in a repository with no commits")
	})
}

// nameRef points the backend's movable name at the working revision: a git branch, an hg
// named branch, a Sapling or jj bookmark.
func nameRef(t *testing.T, b parityBackend, dir, name string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "switch", "-q", "-c", name)
	case "hg":
		vcsTestRun(t, dir, "hg", "branch", "-q", name)
	case "sl":
		vcsTestRun(t, dir, "sl", "bookmark", name)
	case "jj":
		vcsTestRun(t, dir, "jj", "bookmark", "create", name, "-r", "@")
	}
}

// leaveRef moves the working revision off the name nameRef made. hg has no anonymous
// working state: its working directory always carries a named branch.
func leaveRef(t *testing.T, b parityBackend, dir string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "switch", "-q", "--detach")
	case "hg":
		vcsTestRun(t, dir, "hg", "update", "-q", "null")
	case "sl":
		vcsTestRun(t, dir, "sl", "bookmark", "-d", "work")
	case "jj":
		vcsTestRun(t, dir, "jj", "new")
	}
}

// initEmpty makes a repository with nothing committed.
func initEmpty(t *testing.T, b parityBackend, dir string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "init", "-q")
	case "hg":
		vcsTestRun(t, dir, "hg", "init")
	case "sl":
		vcsTestRun(t, dir, "sl", "init", ".")
	case "jj":
		vcsTestRun(t, dir, "jj", "git", "init")
	}
}

// A path outside ASCII survives the round trip. git renders one C-quoted
// ("uni/caf\303\251.md") unless core.quotePath is off, and a quoted name matches no
// project glob, so the project owning that file is never rebuilt, with no diagnostic.
//
// This covers DirtyFiles on every backend. The sibling defect in git's ChangedFiles (the
// one probe in the package that omitted the flag) is pinned by git_test.go's
// TestChangedFilesKeepsNonASCIIPathsRaw, because ChangedFiles needs a base ref and the
// per-backend way to produce one does not belong in this table.
func TestParityNonASCIIPathsSurvive(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		writeRepoFile(t, dir, "café.md", "x\n")
		addPath(t, b, dir, "café.md")

		got, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		assert.Contains(t, got, "café.md",
			"%s returned %q; a C-quoted or escaped name matches no glob", b.name, got)
	})
}

// Dirty and DirtyFiles answer the same question, so they cannot disagree. Dirty is defined
// as len(DirtyFiles) > 0 in every backend, and this pins that rather than trusting four
// copies of the same one-liner to stay in step.
func TestParityDirtyAgreesWithDirtyFiles(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		wasDirty, err := b.drv.Dirty(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.False(t, wasDirty, "%s called a clean tree dirty", b.name)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
		nowDirty, err := b.drv.Dirty(t.Context(), dir, nil)
		require.NoError(t, err)
		files, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.True(t, nowDirty, "%s missed an uncommitted edit", b.name)
		assert.Equal(t, len(files) > 0, nowDirty, "%s: Dirty and DirtyFiles disagree", b.name)
	})
}

// TrackedFiles must answer, not fail, when NONE of the given paths are tracked: that is
// the ordinary answer, and the question the capability exists for. `sl files` exits 1 in
// exactly that case where git's ls-files exits 0, so the driver has to absorb it; because
// the call is batched, getting it wrong fails only for some inputs.
func TestParityTrackedFilesAnswersWhenNoneAreTracked(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter := b.drv
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x\n"), 0o644))

		got, err := reporter.TrackedFiles(t.Context(), dir, []string{"untracked.txt"})
		require.NoError(t, err, "%s failed on a batch containing no tracked path", b.name)
		assert.Empty(t, got)

		mixed, err := reporter.TrackedFiles(t.Context(), dir, []string{"a.txt", "untracked.txt"})
		require.NoError(t, err)
		assert.Equal(t, []string{"a.txt"}, mixed, "%s", b.name)
	})
}

// An ignore reporter echoes the paths it was GIVEN. Expanding a directory argument into
// its contents breaks the set-membership test every caller does against its own input, so
// a directory it asked about is silently reported as not ignored.
func TestParityIgnoredFilesEchoesTheGivenPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		skipUnsupported(t, b, types.CapIgnoredFileReporter)
		reporter := b.drv
		dir := t.TempDir()
		name, body := ignoreRule(b, "build/")
		b.init(t, dir, map[string]string{"a.txt": "one\n", name: body})
		writeRepoFile(t, dir, "build/out.o", "x\n")

		got, err := reporter.IgnoredFiles(t.Context(), dir, []string{"build", "a.txt"})
		require.NoError(t, err, "IgnoredFiles")
		assert.Equal(t, []string{"build"}, got,
			"%s must echo the given path, not expand it into its contents", b.name)
	})
}

// A backend's two ignore reporters must agree. IgnoredFileReporter.IgnoredFiles and
// ConflictResolver.IgnoredPaths are names one letter apart on the same type answering
// nearly the same question in different shapes; git's gave OPPOSITE answers for a path
// that is tracked AND matches an ignore rule, because only one passed --no-index.
// Reaching for the wrong one of two near-identical names is not a compile error, so this is
// the only thing that catches it.
func TestParityIgnoreReportersAgree(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		skipUnsupported(t, b, types.CapIgnoredFileReporter)
		reporter, resolver := b.drv, b.drv
		// keep.log is TRACKED and matches an ignore rule: the case the two disagreed on.
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"keep.log": "x\n"})
		name, body := ignoreRule(b, "*.log")
		writeRepoFile(t, dir, name, body)
		addPath(t, b, dir, name)
		commitAll(t, b, dir, "ignore logs")

		files, err := reporter.IgnoredFiles(t.Context(), dir, []string{"keep.log"})
		require.NoError(t, err, "IgnoredFiles")
		paths, err := resolver.IgnoredPaths(t.Context(), dir, []string{"keep.log"})
		require.NoError(t, err, "IgnoredPaths")

		// Both must say TRUE, not merely agree. Agreement alone is satisfied by
		// false == false, which is what two reporters BOTH returning nothing looks like,
		// and hg's and sl's IgnoredPaths swallow a failed probe into an empty map, so
		// deleting their debugignore call entirely would leave an agreement-only assertion
		// green.
		assert.True(t, paths["keep.log"],
			"%s: IgnoredPaths did not report a tracked path covered by an ignore rule", b.name)
		assert.Equal(t, []string{"keep.log"}, files,
			"%s: IgnoredFiles disagrees with IgnoredPaths on the same tracked-and-ignored path", b.name)
	})
}

// ignoreRule returns the ignore file a backend reads and the content expressing pattern in
// its syntax. Mercurial is the odd one: .hgignore patterns are REGULAR EXPRESSIONS unless
// the file opens with a "syntax: glob" line, so a bare "*.log" there is not merely
// ineffective; hg rejects it as an invalid pattern and every subsequent command aborts.
// git, sl and jj all read a .gitignore of plain globs.
func ignoreRule(b parityBackend, pattern string) (name, body string) {
	if b.name == "hg" {
		return ".hgignore", "syntax: glob\n" + pattern + "\n"
	}
	return ".gitignore", pattern + "\n"
}

// AbortMerge refuses when there is no merge to abort. It is reached on failure paths,
// which is exactly when there may be nothing in progress; sl's implementation is a
// whole-tree revert that exits 0 either way, so without the guard the error path silently
// discards the developer's uncommitted work.
func TestParityAbortMergeRefusesWithNoMergeInProgress(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		starter := b.drv
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("UNCOMMITTED\n"), 0o644))

		assert.Error(t, starter.AbortMerge(t.Context(), dir),
			"%s reported success aborting a merge that was never started", b.name)

		body, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "UNCOMMITTED\n", string(body),
			"%s destroyed uncommitted work while failing to abort a merge", b.name)
	})
}

// operationFork commits a.txt as "main" on one line and as "side" on another from a
// common root, so every operation combining the two conflicts, and leaves the checkout
// on main with an untracked sub/ to ask from.
func operationFork(t *testing.T, b parityBackend) (dir, main, side string) {
	t.Helper()
	dir = t.TempDir()
	b.init(t, dir, map[string]string{"a.txt": "one\n"})
	root := initialCommitID(t, b, dir)
	commitLine := func(body string) string {
		writeRepoFile(t, dir, "a.txt", body)
		commitAll(t, b, dir, body)
		c, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoError(t, err)
		return c.ID
	}
	main = commitLine("main\n")
	checkoutRev(t, b, dir, root)
	side = commitLine("side\n")
	checkoutRev(t, b, dir, main)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	return dir, main, side
}

// tryRun runs one command that is expected to stop on a conflict, so its status is not
// the test's to judge.
func tryRun(dir, bin string, args ...string) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	_ = cmd.Run()
}

// Every backend names the operation a checkout is stopped in, from its root and from a
// directory under it, and nothing once the operation is concluded. jj's operations are
// atomic, so a jj merge or rebase leaves none underway.
func TestParityOperationInProgress(t *testing.T) {
	type step struct {
		bin  string
		args func(main, side string) []string
	}
	fixed := func(bin string, args ...string) step {
		return step{bin, func(string, string) []string { return args }}
	}
	for _, tt := range []struct {
		name  string
		want  string
		steps map[string][]step
		// conclude finishes the operation, for the backends where one stays underway.
		conclude map[string][]step
	}{
		{
			name: "merge", want: types.OperationMerge,
			steps: map[string][]step{
				"git": {{"git", func(_, side string) []string { return []string{"merge", "--no-edit", side} }}},
				"hg":  {{"hg", func(_, side string) []string { return []string{"merge", "-r", side, "--tool", "internal:merge"} }}},
				"sl":  {{"sl", func(_, side string) []string { return []string{"merge", "-r", side, "--tool", "internal:merge"} }}},
				"jj":  {{"jj", func(main, side string) []string { return []string{"new", main, side} }}},
			},
			conclude: map[string][]step{
				"git": {fixed("git", "add", "a.txt"), fixed("git", "commit", "--no-edit")},
				"hg":  {fixed("hg", "resolve", "--mark", "a.txt"), fixed("hg", "commit", "-m", "merged", "-u", "test")},
				"sl":  {fixed("sl", "resolve", "--mark", "a.txt"), fixed("sl", "commit", "-m", "merged")},
			},
		},
		{
			name: "rebase", want: types.OperationRebase,
			steps: map[string][]step{
				"git": {{"git", func(_, side string) []string { return []string{"checkout", "-q", side} }},
					{"git", func(main, _ string) []string { return []string{"rebase", main} }}},
				"hg": {{"hg", func(main, side string) []string {
					return []string{"--config", "extensions.rebase=", "rebase", "-s", side, "-d", main, "--tool", "internal:merge"}
				}}},
				"sl": {{"sl", func(main, side string) []string {
					return []string{"rebase", "-s", side, "-d", main, "--tool", "internal:merge"}
				}}},
				"jj": {{"jj", func(main, side string) []string { return []string{"rebase", "-r", side, "-o", main} }}},
			},
		},
		{
			name: "cherry-pick", want: types.OperationCherryPick,
			steps: map[string][]step{
				"git": {{"git", func(_, side string) []string { return []string{"cherry-pick", side} }}},
				"hg":  {{"hg", func(_, side string) []string { return []string{"graft", "-r", side, "--tool", "internal:merge"} }}},
				"sl":  {{"sl", func(_, side string) []string { return []string{"graft", "-r", side, "--tool", "internal:merge"} }}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eachBackend(t, func(t *testing.T, b parityBackend) {
				steps, ok := tt.steps[b.name]
				if !ok {
					t.Skipf("%s has no %s", b.name, tt.name)
				}
				dir, main, side := operationFork(t, b)
				got, err := b.drv.OperationInProgress(t.Context(), dir)
				require.NoError(t, err)
				require.Empty(t, got, "%s names an operation on a clean checkout", b.name)

				for _, s := range steps {
					tryRun(dir, s.bin, s.args(main, side)...)
				}
				want := tt.want
				if b.name == "jj" {
					want = ""
				}
				for _, at := range []string{dir, filepath.Join(dir, "sub")} {
					got, err := b.drv.OperationInProgress(t.Context(), at)
					require.NoError(t, err)
					assert.Equal(t, want, got, "%s from %s", b.name, at)
				}

				conclude, ok := tt.conclude[b.name]
				if !ok {
					return
				}
				writeRepoFile(t, dir, "a.txt", "resolved\n")
				for _, s := range conclude {
					vcsTestRun(t, dir, s.bin, s.args(main, side)...)
				}
				got, err = b.drv.OperationInProgress(t.Context(), dir)
				require.NoError(t, err)
				assert.Empty(t, got, "%s once the %s is concluded", b.name, tt.name)
			})
		})
	}
}

// commitAll records every pending change in dir.
func commitAll(t *testing.T, b parityBackend, dir, msg string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "commit", "-am", msg)
	case "hg":
		vcsTestRun(t, dir, "hg", "commit", "-m", msg, "-u", "test")
	case "sl":
		vcsTestRun(t, dir, "sl", "commit", "-m", msg)
	case "jj":
		// jj snapshots the working copy automatically, so `describe` is the commit. It
		// deliberately does NOT run `jj new` afterwards: that would leave @ pointing at a
		// fresh EMPTY change, and a test asking about "the commit I just made" via
		// FindCommit(dir, "") would resolve that empty one instead, passing without ever
		// touching the commit it built.
		vcsTestRun(t, dir, "jj", "describe", "-m", msg)
	}
}

// addPath starts tracking one new file. jj tracks on snapshot, so it needs nothing.
func addPath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "add", "--", path)
	case "hg":
		vcsTestRun(t, dir, "hg", "add", path)
	case "sl":
		vcsTestRun(t, dir, "sl", "add", path)
	}
}

// DirtyFiles and DirtyDiff must answer about the SAME change. A gate that names an output
// as drifted and then shows an empty diff sends its reader to reproduce the run to learn
// what the two calls already knew, and it fires in CI, where nobody can look at the tree.
//
// git was the one backend that could disagree: a bare `git diff` is working tree against
// the INDEX, so a STAGED change was reported by DirtyFiles and invisible to DirtyDiff. hg,
// sl and jj have no index and so could not have the bug, which is exactly why only a
// cross-backend test states the rule.
func TestParityDirtyDiffCoversWhatDirtyFilesNames(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"gen.txt": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.txt"), []byte("REGENERATED\n"), 0o644))
		stagePath(t, b, dir, "gen.txt")

		files, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		require.Contains(t, files, "gen.txt", "%s did not report the change at all", b.name)

		diff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyDiff")
		assert.Contains(t, diff, "gen.txt",
			"%s named gen.txt as dirty but its diff does not mention it", b.name)
	})
}

// stagePath stages a path where the backend has an index to stage into. Only git does; for
// the other three this is a no-op, which is the point: they cannot reach the state that
// made git's two probes disagree.
func stagePath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	if b.name == "git" {
		vcsTestRun(t, dir, "git", "add", "--", path)
	}
}

// A repository with no commits still answers. `git diff HEAD` exits 128 there, so the fix
// for the index skew above had to keep the no-HEAD case working rather than trade one
// failure for another.
func TestParityDirtyDiffOnRepoWithNoCommits(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		if b.name != "git" {
			t.Skip("only git distinguishes an unborn HEAD this way")
		}
		dir := t.TempDir()
		vcsTestRun(t, dir, "git", "init", "-q")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644))

		_, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		assert.NoError(t, err, "a repository with no commits has nothing to diff, not an error")
	})
}

// A GLOB pathspec matches, on every backend. This is the assertion the suite was missing,
// and its absence hid the worst defect the VCS work produced: magus.diagnoseDrift hands
// DirtyFiles a project's declared output globs verbatim, and an hg pathspec defaults to a
// LITERAL path, so "gen/**" matched nothing, hg wrote "No such file or directory" to
// stderr, exited 0 with empty stdout, and the generate drift gate reported every project
// clean having checked nothing. In CI, with no diagnostic. sl inherited it; git and jj
// handle the glob natively, which is exactly why a per-backend test would not have found it.
func TestParityGlobPathspecMatches(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"gen/x.json": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "x.json"), []byte("DRIFTED\n"), 0o644))

		files, err := b.drv.DirtyFiles(t.Context(), dir, []string{"gen/**"})
		require.NoError(t, err, "DirtyFiles with a glob pathspec")
		assert.Contains(t, files, "gen/x.json",
			"%s returned %q for glob 'gen/**'; an empty answer here is a drift gate that passes blind", b.name, files)

		diff, err := b.drv.DirtyDiff(t.Context(), dir, []string{"gen/**"})
		require.NoError(t, err, "DirtyDiff with a glob pathspec")
		assert.Contains(t, diff, "x.json",
			"%s produced no diff for glob 'gen/**' while naming the file as dirty", b.name)
	})
}

// Every churn reporter names the files a commit touched AND what it did to each. The status
// half is what lets attribution tell a rename from a delete plus an add, and each backend
// reaches it through a different log format: git tags every path, hg and sl group paths by
// what happened to them, jj spells its statuses as words. Only a shared parser reads all
// three, so a backend whose log stops matching that parser reports a commit with NO files:
// no error, no diagnostic, just a churn heatmap that goes quiet. hg and sl shipped exactly
// that when git's format gained the status column.
func TestParityChangesByCommitReportsStatus(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter := b.drv
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"keep.txt": "one\n", "gone.txt": "two\n"})
		writeRepoFile(t, dir, "keep.txt", "EDITED\n")
		writeRepoFile(t, dir, "new.txt", "three\n")
		addPath(t, b, dir, "new.txt")
		removePath(t, b, dir, "gone.txt")
		commitAll(t, b, dir, "edit one, add one, delete one")

		changes, err := reporter.ChangesByCommit(t.Context(), dir, 1, "")
		require.NoError(t, err, "ChangesByCommit")
		require.Len(t, changes, 1, "%s: the limit must bound the result", b.name)

		got := make(map[string]types.ChangeStatus, len(changes[0].Files))
		for _, f := range changes[0].Files {
			got[f.Path] = f.Status
		}
		assert.Equal(t, map[string]types.ChangeStatus{
			"keep.txt": types.ChangeModified,
			"new.txt":  types.ChangeAdded,
			"gone.txt": types.ChangeDeleted,
		}, got, "%s reported %v", b.name, changes[0].Files)
	})
}

// removePath stops tracking one file. jj snapshots the working copy, so deleting it is the
// whole operation there.
func removePath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "rm", "-q", "--", path)
	case "hg":
		vcsTestRun(t, dir, "hg", "rm", path)
	case "sl":
		vcsTestRun(t, dir, "sl", "rm", path)
	case "jj":
		require.NoError(t, os.Remove(filepath.Join(dir, filepath.FromSlash(path))))
	}
}

// forceColor writes each backend's "colorize even when not a terminal" setting into the
// repository's own config, which is where a real user's would live.
func forceColor(t *testing.T, b parityBackend, dir string) {
	t.Helper()
	switch b.bin {
	case "git":
		vcsTestRun(t, dir, "git", "config", "color.ui", "always")
	case "hg":
		writeRepoFile(t, dir, ".hg/hgrc", "[ui]\ncolor = always\n")
	case "sl":
		// Sapling's repo config is .sl/config, NOT .sl/hgrc, and slInitRepo has already
		// written a username into it, so this appends. Pointing at the hg path instead
		// makes this helper do nothing, and the subtest then passes without ever forcing
		// color, which is exactly how it first went green against a colorizing Sapling.
		appendRepoFile(t, dir, ".sl/config", "\n[ui]\ncolor = always\n")
	case "jj":
		vcsTestRun(t, dir, "jj", "config", "set", "--repo", "ui.color", "always")
	}
}

// A colorized diff is not a cosmetic problem: the escape sequence lands in FRONT of the
// `diff --git` header, so the header no longer begins a line and every reader of the patch
// misses the file entirely. Measured before the fix, with `color.ui = always` in an ordinary
// gitconfig: `magus diff` listed the untracked files (which magus synthesizes itself, and so
// never colorizes) and silently dropped every tracked modification, at exit 0.
//
// Each backend needs a DIFFERENT switch and they are not interchangeable: NO_COLOR loses to
// git's explicit config, and Sapling ignores HGPLAIN even though Mercurial honors it. That is
// what this test is really pinning: one switch per backend, verified against the real binary
// rather than assumed from the family.
func TestParityDirtyDiffIsNeverColorized(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"f.txt": "a\nb\n"})
		forceColor(t, b, dir)
		writeRepoFile(t, dir, "f.txt", "a\nB\n")

		patch, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)
		require.NotEmpty(t, patch, "the tracked edit must show up at all")
		assert.NotContains(t, patch, "\x1b[", "an escape sequence hides the header that follows it")
	})
}

// vcsMove renames a tracked file using the backend's own command, so the backend records it
// as a rename rather than seeing an unrelated delete and add.
func vcsMove(t *testing.T, b parityBackend, dir, from, to string) {
	t.Helper()
	switch b.bin {
	case "git", "hg", "sl":
		vcsTestRun(t, dir, b.bin, "mv", from, to)
	case "jj":
		// jj has no index: the working copy IS the change, so an ordinary move is recorded.
		require.NoError(t, os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)))
	}
}

// A rename must not arrive as a delete plus an add. Mercurial's own diff format renders one
// exactly that way, so a renamed 2000-line file reached this tool as 4000 changed lines whose
// content nobody touched; every consumer treats DirtyDiff as "what a person has to review",
// so that inflates the ranking, the counts, and the hunks a read receipt is keyed by.
//
// Asserted on CONTENT rather than on the word "rename", because the backends spell the header
// differently and the property that matters is the absence of churn, not the spelling.
func TestParityRenameIsNotDeletePlusAdd(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"old.txt": "alpha\nbeta\ngamma\n"})
		vcsMove(t, b, dir, "old.txt", "new.txt")

		patch, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)
		require.NotEmpty(t, patch, "the rename must show up at all")
		assert.NotContains(t, patch, "+alpha", "content re-added means the rename was lost")
		assert.NotContains(t, patch, "-alpha", "content removed means the rename was lost")
	})
}

// Untracked is where a concurrent agent's unfinished work lives, it is in no commit, and a
// checkpoint blind to it answers "same tree?" most confidently about the state it can least
// see. PatchDigest cannot carry it: that one is pinned byte-for-byte to
// internal/changeset.PatchDigest so a checkpoint and a review session stay comparable. Hence a
// second digest, on every backend rather than on git alone.
func TestParityCheckpointSeesUntrackedContent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"tracked.txt": "v1\n"})
		res := types.VCSResolution{VCS: b.drv, Name: b.name}

		require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("one\n"), 0o644))
		first, err := Checkpoint(t.Context(), dir, res, false)
		require.NoError(t, err)
		require.NotEmpty(t, first.UntrackedDigest, "%s: an untracked file left no mark", b.name)

		// CONTENT, not just the path: the file name is unchanged, so a path-only
		// fingerprint would call these two trees identical.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("two\n"), 0o644))
		second, err := Checkpoint(t.Context(), dir, res, false)
		require.NoError(t, err)
		assert.NotEqual(t, first.UntrackedDigest, second.UntrackedDigest,
			"%s: editing an untracked file did not move the digest", b.name)

		// No claim about PatchDigest here: jj snapshots the whole working copy, so a file
		// git calls untracked is one jj already tracks, and the edit moves PatchDigest too.
		// "Untracked" is a git and hg category, not a property of version control.
	})
}

// A tree with nothing untracked reports no digest rather than a hash of emptiness, so ""
// keeps meaning "nothing to measure" the way it does for PatchDigest.
func TestParityCheckpointUntrackedDigestEmptyWhenNoneAreUntracked(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"tracked.txt": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("edited\n"), 0o644))

		cp, err := Checkpoint(t.Context(), dir, types.VCSResolution{VCS: b.drv, Name: b.name}, false)
		require.NoError(t, err)

		assert.Empty(t, cp.UntrackedDigest, "%s: reported an untracked digest with nothing untracked", b.name)
	})
}

// Capturing state must never cost state. One test rather than four because the guarantee is
// one guarantee: the mechanisms differ wildly (git builds a commit through a temporary
// index, hg shelves with --keep, jj has already snapshotted, sl commits and unwinds) and a
// reader choosing a backend should not have to learn which of those leaks.
func TestParityPreserveCostsNoState(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"tracked.txt": "v1\n", "deleted.txt": "gone\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("scratch\n"), 0o644))

		// A DELETED tracked file and a STAGED-equivalent rename each break a different
		// backend: sl turns the deletion into a scheduled removal (R), git leaves the
		// rename's old path in the snapshot tree. One edit plus one new file is the happy
		// path, which is how an invariant test passes while the invariant is false.
		require.NoError(t, os.Remove(filepath.Join(dir, "deleted.txt")))

		beforeFiles, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		beforeDiff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)

		handle, err := b.drv.Preserve(t.Context(), dir)
		require.NoErrorf(t, err, "%s: Preserve failed", b.name)
		require.NotEmptyf(t, handle, "%s: a dirty tree produced no handle", b.name)

		// CONTENT: every byte on disk is what it was.
		tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
		require.NoError(t, err)
		assert.Equalf(t, "v2\n", string(tracked), "%s: Preserve changed a tracked file", b.name)
		untracked, err := os.ReadFile(filepath.Join(dir, "untracked.txt"))
		require.NoErrorf(t, err, "%s: Preserve removed the untracked file, which is the work it exists to protect", b.name)
		assert.Equalf(t, "scratch\n", string(untracked), "%s: Preserve changed an untracked file", b.name)

		// VCS VIEW: the backend still reports the same paths and the same diff. This is
		// what catches sl's uncommit handing untracked files back as added.
		afterFiles, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.ElementsMatchf(t, beforeFiles, afterFiles, "%s: Preserve changed which paths report dirty", b.name)
		afterDiff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.Equalf(t, beforeDiff, afterDiff, "%s: Preserve changed the uncommitted diff", b.name)

		// READ THE HANDLE BACK. Everything above proves the capture cost nothing; only this
		// proves a capture happened. Without it a Preserve returning a constant passes on
		// every backend, and `git stash create`, which silently omits untracked files,
		// would have looked correct.
		assert.Containsf(t, b.readback(t, dir, handle, "untracked.txt"), "scratch",
			"%s: the handle does not hold the untracked file's content", b.name)
	})
}

// The same invariant from the one place every other fixture here avoids: a dir that is NOT
// the repository root. Mercurial's status answers in ROOT-relative paths while its revert
// and forget resolve arguments against the CWD, so from a subdirectory the capture reads
// "sub/deleted.txt" and hands that to a command looking for "sub/sub/deleted.txt", which
// finds nothing and exits ZERO: a tracked file left scheduled for a removal the user never
// asked for, reported as success.
func TestParityPreserveFromASubdirectoryCostsNoState(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"sub/tracked.txt": "v1\n", "sub/deleted.txt": "gone\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "tracked.txt"), []byte("v2\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "untracked.txt"), []byte("scratch\n"), 0o644))
		// The deleted tracked file is the fixture's whole point: it is the only pending
		// state whose restore is a path argument the backend has to resolve.
		require.NoError(t, os.Remove(filepath.Join(dir, "sub", "deleted.txt")))

		// Read from the ROOT on both sides, so one path vocabulary spans the comparison.
		beforeFiles, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		beforeDiff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)

		handle, err := b.drv.Preserve(t.Context(), filepath.Join(dir, "sub"))
		require.NoErrorf(t, err, "%s: Preserve failed from a subdirectory", b.name)
		require.NotEmptyf(t, handle, "%s: a dirty tree produced no handle", b.name)

		afterFiles, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.ElementsMatchf(t, beforeFiles, afterFiles,
			"%s: Preserve from a subdirectory changed which paths report dirty", b.name)
		afterDiff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.Equalf(t, beforeDiff, afterDiff,
			"%s: Preserve from a subdirectory changed the uncommitted diff", b.name)
		untracked, err := os.ReadFile(filepath.Join(dir, "sub", "untracked.txt"))
		require.NoErrorf(t, err, "%s: Preserve removed the untracked file", b.name)
		assert.Equalf(t, "scratch\n", string(untracked), "%s: Preserve changed an untracked file", b.name)
	})
}

// A clean tree has nothing to capture, and says so with "" rather than an error or a handle
// to emptiness, matching how PatchDigest already reports "nothing measured".
func TestParityPreserveOfACleanTreeIsEmpty(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"tracked.txt": "v1\n"})

		handle, err := b.drv.Preserve(t.Context(), dir)

		require.NoErrorf(t, err, "%s: a clean tree is a state, not a failure", b.name)
		assert.Emptyf(t, handle, "%s: a clean tree produced a handle", b.name)
	})
}

// Each readback asks its own backend what the handle holds, in that backend's own spelling,
// which is why a handle is documented as opaque.
func gitReadback(t *testing.T, dir, handle, path string) string {
	t.Helper()
	return vcsTestOutput(t, dir, "git", "show", handle+":"+path)
}

func hgReadback(t *testing.T, dir, handle, path string) string {
	t.Helper()
	// A shelf is not a revision, so its content is read as the patch it stores.
	return vcsTestOutput(t, dir, "hg", "--config", "extensions.shelve=", "shelve", "--patch", handle)
}

func slReadback(t *testing.T, dir, handle, path string) string {
	t.Helper()
	return vcsTestOutput(t, dir, "sl", "cat", "-r", handle, path)
}

func jjReadback(t *testing.T, dir, handle, path string) string {
	t.Helper()
	return vcsTestOutput(t, dir, "jj", "file", "show", "-r", handle, path)
}

// gitListPreserved names the refs Preserve anchored.
func gitListPreserved(t *testing.T, dir string) []string {
	t.Helper()
	out := vcsTestOutput(t, dir, "git", "for-each-ref", "--format=%(refname)", "refs/magus/preserved/")
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		names = append(names, strings.TrimPrefix(line, "refs/magus/preserved/"))
	}
	return names
}

// hgListPreserved names the magus shelves, ignoring any a person made by hand. It parses
// the PLAIN listing rather than the --quiet form the pruner uses: sharing that call would
// make the test agree with the pruner by construction, and a parser that found nothing
// would report an empty mint set every assertion below passes over.
func hgListPreserved(t *testing.T, dir string) []string {
	t.Helper()
	out := vcsTestOutput(t, dir, "hg", "--config", "extensions.shelve=", "shelve", "--list")
	var names []string
	for _, line := range strings.Split(out, "\n") {
		// "name(1s ago)    message", with no space before the "(" when the name overflows
		// the column, so the paren is the cut and whitespace is not.
		name, _, _ := strings.Cut(strings.TrimSpace(line), "(")
		if strings.HasPrefix(name, "magus-") {
			names = append(names, name)
		}
	}
	return names
}

// slListPreserved names the snapshots sl's Preserve left in Sapling's hidden set. They are
// reachable only by their message, which is also the only thing identifying them as
// magus's. Without this the mint set reads empty, every prune assertion compares nil to
// nil, and sl accumulating one hidden commit per capture forever goes unnoticed.
func slListPreserved(t *testing.T, dir string) []string {
	t.Helper()
	out := vcsTestOutput(t, dir, "sl", "log", "--hidden",
		"-r", "desc('"+preserveMessage+"')", "--template", "{node}\n")
	var nodes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			nodes = append(nodes, line)
		}
	}
	return nodes
}

// mintsNothing is jj's store of magus-minted state, empty by construction: jj has already
// snapshotted the working copy, so its Preserve reads a commit id and writes nothing. Named
// rather than inlined so the claim is legible beside the three backends that do mint.
func mintsNothing(*testing.T, string) []string { return nil }

// The other half of Preserve's contract: a handle has a LIFETIME, and what ends it is magus
// deleting its own object and nothing else.
//
// One test over four backends, and the split it exposes is the honest one. git and hg mint
// a named object magus owns and drop it on schedule. jj mints nothing. sl mints a hidden
// commit and CANNOT drop it, because the only Sapling command that removes a commit is
// test-only and aborts on the dirty working copy Preserve always runs against (see
// saplingVCS.PrunePreserved), so sl's assertion is that the capture SURVIVES: the gap
// stated, rather than a nil compared against nil.
func TestParityPrunePreservedDropsWhatMagusMinted(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"tracked.txt": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("scratch\n"), 0o644))

		_, err := b.drv.Preserve(t.Context(), dir)
		require.NoErrorf(t, err, "%s: Preserve failed", b.name)
		minted := b.listPreserved(t, dir)
		if b.mints {
			require.NotEmptyf(t, minted, "%s: Preserve left nothing in the store this test could watch", b.name)
		} else {
			require.Emptyf(t, minted, "%s: a backend documented to mint nothing left an object behind", b.name)
		}

		// A cutoff OLDER than the capture drops nothing. Without this the test passes for a
		// pruner that ignores its argument and deletes everything it finds.
		dropped, err := b.drv.PrunePreserved(t.Context(), dir, time.Now().Add(-time.Hour))
		require.NoErrorf(t, err, "%s: PrunePreserved failed", b.name)
		assert.Emptyf(t, dropped, "%s: dropped a capture newer than the cutoff", b.name)
		assert.ElementsMatchf(t, minted, b.listPreserved(t, dir),
			"%s: the store changed under a cutoff that matched nothing", b.name)

		dropped, err = b.drv.PrunePreserved(t.Context(), dir, time.Now().Add(time.Hour))
		require.NoErrorf(t, err, "%s: PrunePreserved failed", b.name)
		if b.prunes {
			assert.ElementsMatchf(t, minted, dropped, "%s: reported the wrong handles", b.name)
			assert.Emptyf(t, b.listPreserved(t, dir), "%s: reported handles it did not delete", b.name)
		} else {
			assert.Emptyf(t, dropped, "%s: reported dropping what it cannot drop", b.name)
			assert.ElementsMatchf(t, minted, b.listPreserved(t, dir),
				"%s: the store lost a capture nothing here is able to remove", b.name)
		}

		// Pruning is still not allowed to cost working-copy state, for the same reason
		// capturing is not: it runs inside Preserve, on a tree somebody is working in.
		tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
		require.NoError(t, err)
		assert.Equalf(t, "v2\n", string(tracked), "%s: PrunePreserved changed a tracked file", b.name)
		untracked, err := os.ReadFile(filepath.Join(dir, "untracked.txt"))
		require.NoErrorf(t, err, "%s: PrunePreserved removed an untracked file", b.name)
		assert.Equalf(t, "scratch\n", string(untracked), "%s: PrunePreserved changed an untracked file", b.name)
	})
}

// Distinctness is the assertion worth making: a driver returning git's spelling would
// satisfy "non-empty" while telling an hg user to run a command hg does not have.
func TestParityReviewCommandIsTheBackendsOwn(t *testing.T) {
	seen := make(map[string]string, len(parityBackends()))
	for _, b := range parityBackends() {
		cmd := b.drv.ReviewCommand()
		require.NotEmptyf(t, cmd, "%s: no review command", b.name)
		assert.Truef(t, strings.HasPrefix(cmd, b.name+" "), "%s: review command is not this backend's: %q", b.name, cmd)
		if other, dup := seen[cmd]; dup {
			t.Errorf("%s and %s share a review command: %q", other, b.name, cmd)
		}
		seen[cmd] = b.name
	}
}

// RevTime is what cmd/magus/diff.go's impactAdvisorBaseOf reads to date a base ref: a
// backend that satisfies types.RevTimeReporter but reports a bogus or zero time would leave
// the "BASE:" line silently wrong instead of silently absent, which is worse. All four
// backends implement it as of this test.
func TestParityRevTimeReportsCommitDate(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		timer := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		commit, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoErrorf(t, err, "%s FindCommit", b.name)

		got, found, err := timer.RevTime(t.Context(), dir, commit.ID)
		require.NoErrorf(t, err, "%s RevTime", b.name)
		assert.Truef(t, found, "%s reported not-found for a revision it just committed", b.name)
		assert.WithinDurationf(t, time.Now(), got, time.Hour,
			"%s RevTime %v is not close to now", b.name, got)
	})
}

// A revision this clone does not have answers found=false, not an error: the ordinary shape
// a fresh clone gives for a base branch it has never fetched. impactAdvisorBaseOf treats an
// error the same as "no VCS" and drops the BASE: line entirely, so a backend that returned
// one here instead of found=false would make "never fetched" indistinguishable from "this
// backend is broken".
func TestParityRevTimeUnresolvableRevisionIsNotFoundNotError(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		timer := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		_, found, err := timer.RevTime(t.Context(), dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
		require.NoErrorf(t, err, "%s RevTime on an unresolvable revision", b.name)
		assert.Falsef(t, found, "%s reported found=true for a revision that does not exist", b.name)
	})
}

// checkoutRev moves dir's working copy onto rev, so a test can fork a second line of
// history from a known point. Backend-specific like commitAll and addPath above, because
// each speaks its own checkout verb.
func checkoutRev(t *testing.T, b parityBackend, dir, rev string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "checkout", "-q", rev)
	case "hg":
		vcsTestRun(t, dir, "hg", "update", "-r", rev)
	case "sl":
		vcsTestRun(t, dir, "sl", "goto", "-r", rev)
	case "jj":
		// `jj new <rev>` starts a fresh child of rev as the working-copy commit, jj's
		// equivalent of checking it out to build on top of.
		vcsTestRun(t, dir, "jj", "new", rev)
	}
}

// initialCommitID returns the identifier of the commit b.init produced. Every backend
// except jj answers with a plain FindCommit(dir, ""): the working copy IS that commit right
// after init. jj's init additionally runs `jj new` to reach its own equivalent of a clean
// tree (see jjInitRepo), which advances @ past it, so jj is asked about @- instead.
func initialCommitID(t *testing.T, b parityBackend, dir string) string {
	t.Helper()
	rev := ""
	if b.name == "jj" {
		rev = "@-"
	}
	c, err := b.drv.FindCommit(t.Context(), dir, rev)
	require.NoErrorf(t, err, "%s FindCommit(root)", b.name)
	return c.ID
}

// RangeDiff must answer the SYMMETRIC difference: what head added since it diverged from
// base, never base's own changes made after the fork. A naive two-point diff (base vs
// head) charges the reader for both sides; hg's and sl's ancestor()-based revset was
// verified against real binaries specifically because only the merge base, not either
// endpoint alone, gives the right answer.
func TestParityRangeDiffIsSymmetricDifference(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		differ := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"root.txt": "r\n"})
		root := initialCommitID(t, b, dir)

		writeRepoFile(t, dir, "base-only.txt", "base\n")
		addPath(t, b, dir, "base-only.txt")
		commitAll(t, b, dir, "base moves on")
		base, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoErrorf(t, err, "%s FindCommit(base)", b.name)

		checkoutRev(t, b, dir, root)
		writeRepoFile(t, dir, "head-only.txt", "head\n")
		addPath(t, b, dir, "head-only.txt")
		commitAll(t, b, dir, "head diverges")
		head, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoErrorf(t, err, "%s FindCommit(head)", b.name)

		diff, err := differ.RangeDiff(t.Context(), dir, base.ID, head.ID, nil)
		require.NoErrorf(t, err, "%s RangeDiff", b.name)
		assert.Containsf(t, diff, "head-only.txt",
			"%s RangeDiff did not report head's own new file", b.name)
		assert.NotContainsf(t, diff, "base-only.txt",
			"%s RangeDiff reported base's own change since the fork; not the symmetric difference", b.name)
	})
}

// paths, when given, scope RangeDiff to those repo-relative pathspecs the same way
// DirtyFiles and ChangedFiles are scoped: at the source, so a caller never re-filters a
// patch it already asked to be narrowed.
func TestParityRangeDiffScopesToPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		differ := b.drv

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"root.txt": "r\n"})
		base, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoErrorf(t, err, "%s FindCommit(base)", b.name)

		writeRepoFile(t, dir, "keep.txt", "k\n")
		addPath(t, b, dir, "keep.txt")
		writeRepoFile(t, dir, "drop.txt", "d\n")
		addPath(t, b, dir, "drop.txt")
		commitAll(t, b, dir, "two new files")
		head, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoErrorf(t, err, "%s FindCommit(head)", b.name)

		diff, err := differ.RangeDiff(t.Context(), dir, base.ID, head.ID, []string{"keep.txt"})
		require.NoErrorf(t, err, "%s scoped RangeDiff", b.name)
		assert.Containsf(t, diff, "keep.txt", "%s scoped RangeDiff dropped the requested path", b.name)
		assert.NotContainsf(t, diff, "drop.txt", "%s scoped RangeDiff did not narrow to the given path", b.name)
	})
}

// A fresh repository with no remote reports its checkout without error and no remote
// branches, whichever backend answers.
func TestParityCheckoutStateWithoutARemote(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		skipUnsupported(t, b, types.CapCheckoutStateReporter)
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		got, err := b.drv.CheckoutState(t.Context(), dir)
		require.NoErrorf(t, err, "%s CheckoutState", b.name)
		assert.Emptyf(t, got.RemoteBranches, "%s has no remote to report", b.name)
	})
}

// capabilityMatrix is deliberate: adding a backend to a row is an implementation, removing
// one is a regression, and neither happens by accident. Every backend DECLARES every
// capability, so this matrix, not the compiler, is where support is written down.
var capabilityMatrix = map[types.VCSCapability]map[string]bool{
	types.CapBisector:              {"git": true, "hg": true, "sl": true},
	types.CapMergeDriverInstaller:  {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapRefreshHookInstaller:  {"git": true, "hg": true, "sl": true},
	types.CapDriftHookInstaller:    {"git": true, "hg": true, "sl": true},
	types.CapRegenHookInstaller:    {"git": true},
	types.CapRemoteReporter:        {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapRemoteConfigReporter:  {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapDefaultRefReporter:    {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapCheckoutStateReporter: {"git": true},
	types.CapOperationReporter:     {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapPushStatusReporter:    {"git": true, "hg": true, "sl": true},
	types.CapRevTimeReporter:       {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapTrackedFileReporter:   {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapIgnoredFileReporter:   {"git": true, "hg": true, "sl": true},
	types.CapChurnReporter:         {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapBranchChangeReporter:  {"git": true},
	types.CapRangeReporter:         {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapRegionReporter:        {"git": true},
	types.CapAncestryReporter:      {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapConflictResolver:      {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapRevisionFileReader:    {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapRevisionExporter:      {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapMergeStarter:          {"git": true, "hg": true, "sl": true, "jj": true},
	types.CapCommitWriter:          {"git": true},
	types.CapTreeReporter:          {"git": true},
	types.CapTreeMerger:            {"git": true},
	types.CapGeneratedPathReporter: {"git": true},
	types.CapCheckoutProvisioner:   {"git": true},
	types.CapCheckoutLister:        {"git": true},
	types.CapCheckoutReporter:      {"git": true},
	types.CapRevisionFetcher:       {"git": true},
	types.CapPusher:                {"git": true},
	types.CapBundler:               {"git": true},
}

// capabilityProbe calls one method. The arguments make a real implementation fail fast
// and touch nothing outside dir (an invalid revision, remote or path wherever the method
// validates one), so only a decline can come back as a *VCSUnsupportedError.
type capabilityProbe struct {
	capability types.VCSCapability
	method     string
	call       func(d types.VCSDriver, dir string) error
}

func capabilityProbes(t *testing.T) []capabilityProbe {
	ctx := t.Context()
	id := strings.Repeat("a", 40)
	errOf := func(_ any, err error) error { return err }
	return []capabilityProbe{
		{types.CapBisector, "Bisect", func(d types.VCSDriver, dir string) error {
			return errOf(d.Bisect(ctx, dir, types.BisectOptions{Good: "-x"}))
		}},
		{types.CapMergeDriverInstaller, "InstallMergeDriver", func(d types.VCSDriver, dir string) error {
			return d.InstallMergeDriver(ctx, dir, types.MergeDriverGlobs{})
		}},
		{types.CapMergeDriverInstaller, "CheckMergeDriver", func(d types.VCSDriver, dir string) error { return errOf(d.CheckMergeDriver(ctx, dir)) }},
		{types.CapMergeDriverInstaller, "EnsureMergeDriver", func(d types.VCSDriver, dir string) error {
			return errOf(d.EnsureMergeDriver(ctx, dir, types.MergeDriverGlobs{}))
		}},
		{types.CapMergeDriverInstaller, "RunMergeDriver", func(d types.VCSDriver, dir string) error { return d.RunMergeDriver(ctx, dir, nil) }},
		{types.CapMergeDriverInstaller, "MergeDriverCommand", func(d types.VCSDriver, dir string) error { return errOf(d.MergeDriverCommand(ctx, dir)) }},
		{types.CapRefreshHookInstaller, "InstallRefreshHook", func(d types.VCSDriver, dir string) error { return errOf(d.InstallRefreshHook(ctx, dir, "true")) }},
		{types.CapDriftHookInstaller, "InstallDriftHook", func(d types.VCSDriver, dir string) error { return errOf(d.InstallDriftHook(ctx, dir, "true")) }},
		{types.CapRegenHookInstaller, "InstallRegenHook", func(d types.VCSDriver, dir string) error { return errOf(d.InstallRegenHook(ctx, dir, "true")) }},
		{types.CapRemoteReporter, "RemoteURL", func(d types.VCSDriver, dir string) error { return errOf(d.RemoteURL(ctx, dir, "-x")) }},
		{types.CapRemoteConfigReporter, "ConfiguredRemote", func(d types.VCSDriver, dir string) error { return errOf(d.ConfiguredRemote(dir)) }},
		{types.CapDefaultRefReporter, "DefaultRef", func(d types.VCSDriver, dir string) error { return errOf(d.DefaultRef(ctx, dir)) }},
		{types.CapCheckoutStateReporter, "CheckoutState", func(d types.VCSDriver, dir string) error { return errOf(d.CheckoutState(ctx, dir)) }},
		{types.CapOperationReporter, "OperationInProgress", func(d types.VCSDriver, dir string) error { return errOf(d.OperationInProgress(ctx, dir)) }},
		{types.CapPushStatusReporter, "CommitPushed", func(d types.VCSDriver, dir string) error {
			_, _, err := d.CommitPushed(ctx, dir, "-x")
			return err
		}},
		{types.CapRevTimeReporter, "RevTime", func(d types.VCSDriver, dir string) error {
			_, _, err := d.RevTime(ctx, dir, "-x")
			return err
		}},
		{types.CapTrackedFileReporter, "TrackedFiles", func(d types.VCSDriver, dir string) error { return errOf(d.TrackedFiles(ctx, dir, nil)) }},
		{types.CapIgnoredFileReporter, "IgnoredFiles", func(d types.VCSDriver, dir string) error { return errOf(d.IgnoredFiles(ctx, dir, nil)) }},
		{types.CapChurnReporter, "ChangesByCommit", func(d types.VCSDriver, dir string) error { return errOf(d.ChangesByCommit(ctx, dir, 1, "-x")) }},
		{types.CapBranchChangeReporter, "BranchChanges", func(d types.VCSDriver, dir string) error { return errOf(d.BranchChanges(ctx, dir, "-x", 1)) }},
		{types.CapRangeReporter, "RangeDiff", func(d types.VCSDriver, dir string) error { return errOf(d.RangeDiff(ctx, dir, "-x", "-x", nil)) }},
		{types.CapRangeReporter, "RangeFiles", func(d types.VCSDriver, dir string) error { return errOf(d.RangeFiles(ctx, dir, "-x", "-x", nil)) }},
		{types.CapRangeReporter, "RangeCommits", func(d types.VCSDriver, dir string) error { return errOf(d.RangeCommits(ctx, dir, "-x", "-x", nil)) }},
		{types.CapRegionReporter, "Regions", func(d types.VCSDriver, dir string) error {
			return errOf(d.Regions(ctx, dir, "-x", []types.FileChange{{Path: "x"}}))
		}},
		{types.CapAncestryReporter, "IsAncestor", func(d types.VCSDriver, dir string) error { return errOf(d.IsAncestor(ctx, dir, "-x", "-x")) }},
		{types.CapConflictResolver, "Conflicts", func(d types.VCSDriver, dir string) error { return errOf(d.Conflicts(ctx, dir)) }},
		{types.CapConflictResolver, "KeepIncoming", func(d types.VCSDriver, dir string) error { return d.KeepIncoming(ctx, dir, nil) }},
		{types.CapConflictResolver, "MarkResolved", func(d types.VCSDriver, dir string) error { return d.MarkResolved(ctx, dir, nil) }},
		{types.CapConflictResolver, "RemoveConflicts", func(d types.VCSDriver, dir string) error { return d.RemoveConflicts(ctx, dir, nil) }},
		{types.CapConflictResolver, "IgnoredPaths", func(d types.VCSDriver, dir string) error { return errOf(d.IgnoredPaths(ctx, dir, nil)) }},
		{types.CapRevisionFileReader, "ReadFileAt", func(d types.VCSDriver, dir string) error { return errOf(d.ReadFileAt(ctx, dir, "-x", "a")) }},
		{types.CapRevisionExporter, "ExportRevision", func(d types.VCSDriver, dir string) error {
			return d.ExportRevision(ctx, dir, "-x", filepath.Join(dir, "out"))
		}},
		{types.CapMergeStarter, "StartMerge", func(d types.VCSDriver, dir string) error { return d.StartMerge(ctx, dir, "-x", types.Person{}) }},
		{types.CapMergeStarter, "AbortMerge", func(d types.VCSDriver, dir string) error { return d.AbortMerge(ctx, dir) }},
		{types.CapCommitWriter, "Commit", func(d types.VCSDriver, dir string) error { return errOf(d.Commit(ctx, dir, types.CheckoutCommit{})) }},
		{types.CapCommitWriter, "CommitTree", func(d types.VCSDriver, dir string) error { return errOf(d.CommitTree(ctx, dir, types.TreeCommit{})) }},
		{types.CapTreeReporter, "TreeID", func(d types.VCSDriver, dir string) error { return errOf(d.TreeID(ctx, dir, "-x")) }},
		{types.CapTreeReporter, "DiffTrees", func(d types.VCSDriver, dir string) error { return errOf(d.DiffTrees(ctx, dir, "-x", "-x")) }},
		{types.CapTreeMerger, "MergeTrees", func(d types.VCSDriver, dir string) error {
			return errOf(d.MergeTrees(ctx, dir, types.TreeMerge{Ours: "-x", Theirs: "-x"}))
		}},
		{types.CapTreeMerger, "MergeBase", func(d types.VCSDriver, dir string) error {
			_, _, err := d.MergeBase(ctx, dir, "-x", "-x")
			return err
		}},
		{types.CapGeneratedPathReporter, "GeneratedPaths", func(d types.VCSDriver, dir string) error {
			return errOf(d.GeneratedPaths(ctx, dir, "-x", []string{"a"}))
		}},
		{types.CapCheckoutProvisioner, "CreateCheckout", func(d types.VCSDriver, dir string) error { return d.CreateCheckout(ctx, dir, "rel", "-x") }},
		{types.CapCheckoutProvisioner, "RemoveCheckout", func(d types.VCSDriver, dir string) error { return d.RemoveCheckout(ctx, dir, "rel") }},
		{types.CapCheckoutProvisioner, "Checkouts", func(d types.VCSDriver, dir string) error { return errOf(d.Checkouts(ctx, dir)) }},
		{types.CapCheckoutLister, "OtherCheckouts", func(d types.VCSDriver, dir string) error { return errOf(d.OtherCheckouts(dir)) }},
		{types.CapCheckoutReporter, "RegisteredCheckouts", func(d types.VCSDriver, dir string) error { return errOf(d.RegisteredCheckouts(ctx, dir)) }},
		{types.CapCheckoutReporter, "UnpublishedRevisions", func(d types.VCSDriver, dir string) error {
			return errOf(d.UnpublishedRevisions(ctx, dir, "-x"))
		}},
		{types.CapRevisionFetcher, "FetchRef", func(d types.VCSDriver, dir string) error { return errOf(d.FetchRef(ctx, dir, "-x", "refs/heads/main")) }},
		{types.CapRevisionFetcher, "FetchCommit", func(d types.VCSDriver, dir string) error { return d.FetchCommit(ctx, dir, "-x", id) }},
		{types.CapPusher, "Push", func(d types.VCSDriver, dir string) error {
			return d.Push(ctx, dir, types.PushLease{Remote: "-x", Ref: "refs/heads/main", To: id, Expected: id})
		}},
		{types.CapBundler, "Bundle", func(d types.VCSDriver, dir string) error {
			return d.Bundle(ctx, dir, "rel", types.BundleRange{Head: "-x"})
		}},
		{types.CapBundler, "Unbundle", func(d types.VCSDriver, dir string) error { return d.Unbundle(ctx, dir, "rel") }},
	}
}

// Every method of every capability either answers or refuses with a *VCSUnsupportedError
// naming the backend and the capability, matching the matrix. Needs no VCS binary: a stub
// refuses before running anything, and a real implementation's failure to run is not a
// refusal.
func TestParityCapabilityMatrix(t *testing.T) {
	probes := capabilityProbes(t)
	probed := map[types.VCSCapability]bool{}
	for _, p := range probes {
		probed[p.capability] = true
	}
	for capability := range capabilityMatrix {
		assert.Truef(t, probed[capability], "%s is in the matrix but no probe calls it", capability)
	}
	for _, b := range parityBackends() {
		for _, p := range probes {
			t.Run(b.name+"/"+p.method, func(t *testing.T) {
				row, ok := capabilityMatrix[p.capability]
				require.Truef(t, ok, "%s has a probe but no row in the matrix", p.capability)
				err := p.call(b.drv, t.TempDir())
				var declined *types.VCSUnsupportedError
				if !row[b.name] {
					require.ErrorAsf(t, err, &declined, "the matrix says %s declines %s", b.name, p.capability)
					assert.Equal(t, &types.VCSUnsupportedError{VCS: b.name, Capability: p.capability}, declined)
					assert.ErrorIs(t, err, types.ErrVCSUnsupported)
					assert.ErrorIs(t, err, errors.ErrUnsupported)
					return
				}
				assert.Falsef(t, errors.As(err, &declined), "%s implements %s per the matrix, but %s refused: %v", b.name, p.capability, p.method, err)
			})
		}
	}
}

// MergeDriverCommand reads back what InstallMergeDriver registered, and nothing before it.
func TestParityMergeDriverCommandReadsTheRegistration(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		skipUnsupported(t, b, types.CapMergeDriverInstaller)
		isolateGitConfig(t)
		isolateUserConfig(t)
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "a\n"})
		ctx := t.Context()

		got, err := b.drv.MergeDriverCommand(ctx, dir)
		require.NoErrorf(t, err, "%s before install", b.name)
		assert.Emptyf(t, got, "%s reported a driver nothing registered", b.name)

		require.NoError(t, b.drv.InstallMergeDriver(ctx, dir, types.MergeDriverGlobs{Outputs: []string{"gen/**"}}))
		got, err = b.drv.MergeDriverCommand(ctx, dir)
		require.NoErrorf(t, err, "%s after install", b.name)
		assert.Containsf(t, got, "vcs merge-driver", "%s", b.name)
	})
}

// isolateUserConfig moves HOME and XDG_CONFIG_HOME under the test, since jj keeps a
// repository's own config under the user's config directory, not in the working copy.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

// skipUnsupported skips a parity test for a backend the matrix says declines capability.
func skipUnsupported(t *testing.T, b parityBackend, capability types.VCSCapability) {
	t.Helper()
	if !capabilityMatrix[capability][b.name] {
		t.Skipf("%s declines %s", b.name, capability)
	}
}

// forkedRange builds root, then a base line adding base-only.txt, then a head line from
// root that edits root.txt and adds head-only.txt in two commits. It returns root, base
// and head ids.
func forkedRange(t *testing.T, b parityBackend) (dir, root, base, head string) {
	t.Helper()
	dir = t.TempDir()
	b.init(t, dir, map[string]string{"root.txt": "r\n"})
	root = initialCommitID(t, b, dir)

	writeRepoFile(t, dir, "base-only.txt", "base\n")
	addPath(t, b, dir, "base-only.txt")
	commitAll(t, b, dir, "base moves on")
	c, err := b.drv.FindCommit(t.Context(), dir, "")
	require.NoErrorf(t, err, "%s FindCommit(base)", b.name)
	base = c.ID

	checkoutRev(t, b, dir, root)
	writeRepoFile(t, dir, "root.txt", "r\nedited\n")
	commitAll(t, b, dir, "head edits root")
	if b.name == "jj" {
		vcsTestRun(t, dir, "jj", "new")
	}
	writeRepoFile(t, dir, "head-only.txt", "head\n")
	addPath(t, b, dir, "head-only.txt")
	commitAll(t, b, dir, "head adds a file")
	c, err = b.drv.FindCommit(t.Context(), dir, "")
	require.NoErrorf(t, err, "%s FindCommit(head)", b.name)
	return dir, root, base, c.ID
}

// Both refusals come from the check, before the backend runs: an empty base would read as
// the whole history on one backend and as a revset parse error on another.
func TestParityRefusesEmptyAndOptionShapedRevisions(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "a\n"})
		head := initialCommitID(t, b, dir)

		_, err := b.drv.RangeDiff(t.Context(), dir, "", head, nil)
		assert.ErrorContainsf(t, err, "a revision is required", "%s RangeDiff with an empty base", b.name)
		_, _, err = b.drv.RevTime(t.Context(), dir, "-x")
		assert.ErrorContainsf(t, err, "looks like a flag", "%s RevTime with an option-shaped revision", b.name)
	})
}

// RangeFiles is RangeDiff's name list: exactly the files the diff shows, from the merge
// base, so base's own change never appears.
func TestParityRangeFilesMatchesRangeDiff(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir, _, base, head := forkedRange(t, b)

		files, err := b.drv.RangeFiles(t.Context(), dir, base, head, nil)
		require.NoErrorf(t, err, "%s RangeFiles", b.name)
		slices.Sort(files)
		assert.Equalf(t, []string{"head-only.txt", "root.txt"}, files, "%s", b.name)

		diff, err := b.drv.RangeDiff(t.Context(), dir, base, head, nil)
		require.NoError(t, err)
		for _, f := range files {
			assert.Containsf(t, diff, f, "%s RangeFiles named %s, which RangeDiff does not show", b.name, f)
		}

		_, err = b.drv.RangeFiles(t.Context(), dir, base, "no-such-revision", nil)
		assert.Errorf(t, err, "%s: an unresolvable revision is an error, not an empty list", b.name)

		scoped, err := b.drv.RangeFiles(t.Context(), dir, base, head, []string{"root.txt"})
		require.NoErrorf(t, err, "%s scoped RangeFiles", b.name)
		assert.Equalf(t, []string{"root.txt"}, scoped, "%s: paths did not narrow the files", b.name)

		// Empty is not a revision: it would read as the whole history on one backend and as
		// an error on another.
		_, err = b.drv.RangeFiles(t.Context(), dir, "", head, nil)
		assert.Errorf(t, err, "%s: an empty base", b.name)
		_, err = b.drv.RangeCommits(t.Context(), dir, "", head, nil)
		assert.Errorf(t, err, "%s: an empty base", b.name)
		_, _, err = b.drv.RevTime(t.Context(), dir, "-x")
		assert.Errorf(t, err, "%s: an option-shaped revision", b.name)
	})
}

// RangeCommits lists head's own commits newest first, each with its parents, and narrows
// to the commits touching the given paths.
func TestParityRangeCommitsNewestFirstWithParents(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir, _, base, head := forkedRange(t, b)

		commits, err := b.drv.RangeCommits(t.Context(), dir, base, head, nil)
		require.NoErrorf(t, err, "%s RangeCommits", b.name)
		subjects := make([]string, len(commits))
		for i, c := range commits {
			subjects[i] = c.Subject
			assert.Lenf(t, c.Parents, 1, "%s: %q is a linear commit", b.name, c.Subject)
		}
		assert.Equalf(t, []string{"head adds a file", "head edits root"}, subjects, "%s", b.name)
		assert.Equal(t, head, commits[0].ID)

		scoped, err := b.drv.RangeCommits(t.Context(), dir, base, head, []string{"root.txt"})
		require.NoErrorf(t, err, "%s scoped RangeCommits", b.name)
		require.Lenf(t, scoped, 1, "%s: paths did not narrow the commits", b.name)
		assert.Equal(t, "head edits root", scoped[0].Subject)
	})
}

func TestParityIsAncestor(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir, root, base, head := forkedRange(t, b)
		ctx := t.Context()
		for _, c := range []struct {
			ancestor, descendant string
			want                 bool
		}{
			{root, head, true},
			{head, head, true},
			{head, root, false},
			{base, head, false},
		} {
			got, err := b.drv.IsAncestor(ctx, dir, c.ancestor, c.descendant)
			require.NoErrorf(t, err, "%s IsAncestor", b.name)
			assert.Equalf(t, c.want, got, "%s IsAncestor(%s, %s)", b.name, c.ancestor, c.descendant)
		}
		_, err := b.drv.IsAncestor(ctx, dir, "no-such-revision", head)
		assert.Errorf(t, err, "%s: an unresolvable revision is an error, not false", b.name)
	})
}

// RemoteURL reads the default remote under "" and a named one by name, and an unknown
// name is ErrVCSUnsupported, the answer every caller degrades on.
func TestParityRemoteURLByName(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "a\n"})
		const primary, secondary = "https://example.com/primary.git", "https://example.com/upstream.git"
		switch b.name {
		case "git":
			gitRun(t, dir, "remote", "add", "origin", primary)
			gitRun(t, dir, "remote", "add", "upstream", secondary)
		case "hg":
			appendRepoFile(t, dir, ".hg/hgrc", "\n[paths]\ndefault = "+primary+"\nupstream = "+secondary+"\n")
		case "sl":
			appendRepoFile(t, dir, ".sl/config", "\n[paths]\ndefault = "+primary+"\nupstream = "+secondary+"\n")
		case "jj":
			vcsTestRun(t, dir, "jj", "git", "remote", "add", "origin", primary)
			vcsTestRun(t, dir, "jj", "git", "remote", "add", "upstream", secondary)
		}
		ctx := t.Context()

		got, err := b.drv.RemoteURL(ctx, dir, "")
		require.NoErrorf(t, err, "%s default remote", b.name)
		assert.Equal(t, primary, got)
		got, err = b.drv.RemoteURL(ctx, dir, "upstream")
		require.NoErrorf(t, err, "%s named remote", b.name)
		assert.Equal(t, secondary, got)

		_, err = b.drv.RemoteURL(ctx, dir, "nope")
		assert.ErrorIsf(t, err, types.ErrVCSUnsupported, "%s unknown remote", b.name)
		_, err = b.drv.RemoteURL(ctx, dir, "--upload-pack=x")
		require.Error(t, err)
		assert.NotErrorIsf(t, err, types.ErrVCSUnsupported, "%s: an option-shaped name is refused, not reported absent", b.name)

		// A lookup that could not run is not "no remote": callers degrade on the sentinel,
		// and a cancelled read must not turn into a missing link.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = b.drv.RemoteURL(cancelled, dir, "")
		require.Errorf(t, err, "%s cancelled", b.name)
		assert.NotErrorIsf(t, err, types.ErrVCSUnsupported, "%s: a cancelled read reported no remote", b.name)
		if b.name == "hg" {
			_, err = b.drv.DefaultRef(cancelled, dir)
			require.Error(t, err)
			assert.NotErrorIs(t, err, types.ErrVCSUnsupported, "hg: a cancelled read reported no default branch")
		}
	})
}

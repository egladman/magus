package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	t.Run("hg", func(t *testing.T) { assertBuiltinBase(t, "hg", "tip") })
	t.Run("jj", func(t *testing.T) { assertBuiltinBase(t, "jj", "trunk()") })
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
	// git and hg implement MergeDriverInstaller; jj does not.
	want := map[string]bool{"git": true, "hg": true}
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
		_, err := v.Diff(context.Background(), t.TempDir(), "-rf")
		require.Errorf(t, err, "%s.Diff with flag-like base should error", v.Name())
		assert.Containsf(t, err.Error(), "looks like a flag", "%s.Diff error", v.Name())
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

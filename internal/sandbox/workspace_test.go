package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// fromConfig is FromConfig for a test that expects it to succeed.
func fromConfig(t *testing.T, root, cacheDir string, cfg config.SandboxConfig) *Policy {
	t.Helper()
	p, err := FromConfig(root, cacheDir, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(p.TempDir) })
	return p
}

// A sandbox.allow entry grants exactly the mode it spells.
func TestFromConfigFlowsAllowIntoPolicy(t *testing.T) {
	t.Parallel()
	root, cacheDir := t.TempDir(), t.TempDir()
	extra := filesystem.ResolveRulePath(t.TempDir())

	withAllow := fromConfig(t, root, cacheDir, config.SandboxConfig{
		Allow: []config.SandboxAllowPath{{Path: extra, Mode: "rw"}},
	})
	assert.NoError(t, withAllow.CheckWrite(t.Context(), filepath.Join(extra, "out")))
	assert.Error(t, withAllow.CheckExec(t.Context(), filepath.Join(extra, "tool")), "rw grants no exec")
	assert.Error(t, fromConfig(t, root, cacheDir, config.SandboxConfig{}).CheckWrite(t.Context(), filepath.Join(extra, "out")))
}

// The policy carries the mode that decides an unconfinable child, and the roots its
// control files are found under.
func TestFromConfigCarriesTheModeAndTheControlRoots(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	p := fromConfig(t, root, "", config.SandboxConfig{Mode: types.SandboxModeRequired})

	assert.Equal(t, types.SandboxModeRequired, p.Mode)
	assert.Equal(t, filesystem.ResolveRulePath(root), p.Workspace)
	assert.Equal(t, []string{filesystem.ResolveRulePath(filepath.Join(root, ".git"))}, p.GitDirs)
	assert.ErrorIs(t, p.CheckWrite(t.Context(), filepath.Join(root, ".git", "hooks", "pre-commit")), filesystem.ErrDenied)
}

// Misconfiguration is an error naming every bad entry, never an entry skipped with a
// warning: skipped, a typo grants less than was written, or more.
func TestFromConfigRefusesABadAllowOrPassthrough(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAGUS_TEST_UNSET_VAR", "")
	for name, cfg := range map[string]config.SandboxConfig{
		"mode typo":     {Allow: []config.SandboxAllowPath{{Path: "/opt/x", Mode: "RW"}}},
		"unset var":     {Allow: []config.SandboxAllowPath{{Path: "$MAGUS_TEST_UNSET_VAR/", Mode: "rw"}}},
		"relative path": {Allow: []config.SandboxAllowPath{{Path: "build", Mode: "ro"}}},
		"short prefix":  {Env: config.SandboxEnv{Passthrough: []string{"GO*"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FromConfig(root, "", cfg)
			assert.ErrorIs(t, err, types.AllowlistUnresolved)
		})
	}
}

// The private temp dir exists outside the workspace, is the children's TMPDIR, and
// is the same dir for every build of one workspace's policy.
func TestFromConfigGivesChildrenAPrivateTempDir(t *testing.T) {
	root, cacheDir := t.TempDir(), t.TempDir()
	p := fromConfig(t, root, cacheDir, config.SandboxConfig{})

	info, err := os.Stat(p.TempDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.False(t, filesystem.Under(filesystem.ResolveRulePath(p.TempDir), filesystem.ResolveRulePath(root)))
	assert.Contains(t, p.BaseEnv, "TMPDIR="+p.TempDir)
	assert.NoError(t, p.CheckExec(t.Context(), filepath.Join(p.TempDir, "go-build1", "a.test")))
	assert.Equal(t, p.TempDir, fromConfig(t, root, cacheDir, config.SandboxConfig{}).TempDir)
	assert.NotEqual(t, p.TempDir, fromConfig(t, t.TempDir(), cacheDir, config.SandboxConfig{}).TempDir)
}

// A caller's own temp dir replaces the one FromConfig would make, under the same rule
// that it stays out of the workspace.
func TestFromConfigWithTempDirUsesTheCallersTempDir(t *testing.T) {
	root, tmp := t.TempDir(), t.TempDir()
	p, err := FromConfigWithTempDir(root, "", tmp, config.SandboxConfig{})
	require.NoError(t, err)
	assert.Equal(t, tmp, p.TempDir)
	assert.Contains(t, p.BaseEnv, "TMPDIR="+tmp)
	assert.NoError(t, p.CheckWrite(t.Context(), filepath.Join(tmp, "x")))

	_, err = FromConfigWithTempDir(root, "", filepath.Join(root, "tmp"), config.SandboxConfig{})
	assert.ErrorContains(t, err, "inside the workspace")
}

// A temp dir inside the checkout changes what tools see: a repository a test creates
// there nests in the workspace's own, and a VCS command run in it rewrites the
// workspace's history. So a TMPDIR inside the workspace is refused, not used.
func TestFromConfigNeverPutsTheTempDirInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(root, ".magus", "tmp"))
	require.NoError(t, os.MkdirAll(os.Getenv("TMPDIR"), 0o700))

	_, err := FromConfig(root, filepath.Join(root, ".magus"), config.SandboxConfig{})
	assert.ErrorContains(t, err, "inside the workspace")
}

// The shared temp dir is world-writable, so a name another account created first is
// refused rather than used.
func TestPrivateTempDirRefusesAPlantedDirectory(t *testing.T) {
	base := t.TempDir()
	dir, err := privateTempDir(base, "/ws")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o755))
	_, err = privateTempDir(base, "/ws")
	assert.ErrorContains(t, err, "not a private directory")

	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.Symlink(t.TempDir(), dir))
	_, err = privateTempDir(base, "/ws")
	assert.ErrorContains(t, err, "not a private directory")
}

// A linked worktree keeps its git directories outside the checkout, and a subdirectory
// workspace has its .git above it.
func TestGitDirs(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "wt"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub"), 0o755))
	gitDir, common := gitDirs(filepath.Join(repo, "sub"))
	assert.Equal(t, filepath.Join(repo, ".git"), gitDir)
	assert.Equal(t, filepath.Join(repo, ".git"), common)

	wt := t.TempDir()
	linked := filepath.Join(repo, ".git", "worktrees", "wt")
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+linked+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(linked, "commondir"), []byte("../..\n"), 0o644))
	gitDir, common = gitDirs(wt)
	assert.Equal(t, linked, gitDir)
	assert.Equal(t, filepath.Join(repo, ".git"), common)

	gitDir, common = gitDirs(filepath.VolumeName(wt) + string(filepath.Separator))
	assert.Empty(t, gitDir+common, "no checkout, no grant")
}

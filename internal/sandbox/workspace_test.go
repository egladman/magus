package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// fromConfig is FromConfig for a test that expects it to succeed.
func fromConfig(t *testing.T, root, cacheDir string, cfg config.SandboxConfig) *Policy {
	t.Helper()
	p, err := FromConfig(root, cacheDir, cfg, nil)
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
		Allow: []spells.SandboxAllow{{Path: extra, Mode: "rw"}},
	})
	assert.NoError(t, withAllow.CheckWrite(t.Context(), filepath.Join(extra, "out")))
	assert.Error(t, withAllow.CheckExec(t.Context(), filepath.Join(extra, "tool")), "rw grants no exec")
	assert.Error(t, fromConfig(t, root, cacheDir, config.SandboxConfig{}).CheckWrite(t.Context(), filepath.Join(extra, "out")))
}

// magus.yaml takes the declaration a spell makes: an entry's variable, when set, is the
// location, and its base and path otherwise. A spell's declarations are merged in.
func TestFromConfigResolvesTheSpellShape(t *testing.T) {
	root := t.TempDir()
	data := filesystem.ResolveRulePath(t.TempDir())
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("MAGUS_TEST_MISE_DATA_DIR", "")
	cfg := config.SandboxConfig{Allow: []spells.SandboxAllow{
		{Name: "mise", Env: "MAGUS_TEST_MISE_DATA_DIR", Base: "xdgData", Path: "mise", Mode: spells.SandboxAccessRX},
	}}
	tool := filepath.Join(data, "mise", "installs", "go", "bin", "go")
	assert.NoError(t, fromConfig(t, root, "", cfg).CheckExec(t.Context(), tool))

	moved := filesystem.ResolveRulePath(t.TempDir())
	t.Setenv("MAGUS_TEST_MISE_DATA_DIR", moved)
	p := fromConfig(t, root, "", cfg)
	assert.NoError(t, p.CheckExec(t.Context(), filepath.Join(moved, "shims", "go")))
	assert.Error(t, p.CheckExec(t.Context(), tool), "the variable replaces the default")

	cache := filesystem.ResolveRulePath(t.TempDir())
	t.Setenv("MAGUS_TEST_TOOL_CACHE", cache)
	p, err := FromConfig(root, "", config.SandboxConfig{}, map[string]spells.Sandbox{"tool": {
		Allow: []spells.SandboxAllow{{Env: "MAGUS_TEST_TOOL_CACHE", Base: "userCache", Path: "tool", Mode: spells.SandboxAccessRW}},
		Env:   spells.SandboxEnv{Passthrough: []string{"MAGUS_TEST_TOOL_CACHE"}},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(p.TempDir) })
	assert.NoError(t, p.CheckWrite(t.Context(), filepath.Join(cache, "x")))
	assert.Contains(t, p.BaseEnv, "MAGUS_TEST_TOOL_CACHE="+cache)
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
		"mode typo":     {Allow: []spells.SandboxAllow{{Path: "/opt/x", Mode: "RW"}}},
		"unset var":     {Allow: []spells.SandboxAllow{{Path: "$MAGUS_TEST_UNSET_VAR/", Mode: "rw"}}},
		"relative path": {Allow: []spells.SandboxAllow{{Path: "build", Mode: "ro"}}},
		"short prefix":  {Env: config.SandboxEnv{Passthrough: []string{"GO*"}}},
		"unknown base":  {Allow: []spells.SandboxAllow{{Base: "tmp", Path: "x", Mode: "rw"}}},
		"escaping path": {Allow: []spells.SandboxAllow{{Base: "home", Path: "../x", Mode: "rw"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FromConfig(root, "", cfg, nil)
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

// A box's children get its temp dir, under the same rule that it stays out of the
// workspace, and its environment: every writable grant resolves against the box's home,
// every other one against this process's, where the toolchains are installed. A
// literal path is refused strictly against the environment its grant resolves in.
func TestFromConfigBoxedResolvesWritesInTheBoxAndToolsOnTheHost(t *testing.T) {
	root, runner, box := t.TempDir(), filesystem.ResolveRulePath(t.TempDir()), filesystem.ResolveRulePath(t.TempDir())
	home, tmp := filepath.Join(box, "home"), filepath.Join(box, "tmp")
	t.Setenv("MAGUS_TEST_TOOL", filepath.Join(runner, "tool"))
	t.Setenv("MAGUS_TEST_CACHE", filepath.Join(runner, "cache"))
	environ := []string{"PATH=/usr/bin", "HOME=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, ".cache")}
	cfg := config.SandboxConfig{Allow: []spells.SandboxAllow{{Path: "~/notes", Mode: spells.SandboxAccessRW}}}
	grants := map[string]spells.Sandbox{"tool": {Allow: []spells.SandboxAllow{
		{Env: "MAGUS_TEST_TOOL", Mode: spells.SandboxAccessRX},
		{Env: "MAGUS_TEST_CACHE", Base: spells.SandboxBaseXDGCache, Path: "tool", Mode: spells.SandboxAccessRW},
	}}}
	p, err := FromConfigBoxed(root, cfg, grants, Box{Environ: environ, TempDir: tmp})
	require.NoError(t, err)
	assert.Equal(t, tmp, p.TempDir)
	assert.Contains(t, p.BaseEnv, "TMPDIR="+tmp)
	assert.Contains(t, p.BaseEnv, "HOME="+home)
	ctx := t.Context()
	for _, path := range []string{filepath.Join(tmp, "x"), filepath.Join(home, ".cache", "tool", "x"), filepath.Join(home, "notes", "x")} {
		assert.NoError(t, p.CheckWrite(ctx, path), path)
	}
	assert.NoError(t, p.CheckExec(ctx, filepath.Join(runner, "tool", "bin", "tool")))
	assert.ErrorIs(t, p.CheckWrite(ctx, filepath.Join(runner, "tool", "x")), filesystem.ErrDenied)
	assert.ErrorIs(t, p.CheckWrite(ctx, filepath.Join(runner, "cache", "x")), filesystem.ErrDenied)
	assert.Empty(t, p.WritesOutside(root, home, tmp))

	_, err = FromConfigBoxed(root, config.SandboxConfig{}, nil, Box{Environ: environ, TempDir: filepath.Join(root, "tmp")})
	assert.ErrorContains(t, err, "inside the workspace")
	_, err = FromConfigBoxed(root, config.SandboxConfig{Allow: []spells.SandboxAllow{{Path: "$MAGUS_TEST_TOOL/x", Mode: spells.SandboxAccessRW}}}, nil, Box{Environ: environ, TempDir: tmp})
	assert.ErrorIs(t, err, filesystem.ErrUnsetVariable, "a writable grant reads the box's environment, which does not set it")
}

// WritesOutside names each write a policy grants outside the directories given, other
// than the devices and the checkout's git directories every policy grants, and says
// which a link inside them leads out.
func TestWritesOutside(t *testing.T) {
	root := filesystem.ResolveRulePath(t.TempDir())
	box, outside := filepath.Join(root, "box"), filepath.Join(root, "outside")
	for _, d := range []string{box, outside, filepath.Join(root, "repo", ".git", "worktrees", "ws")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.Symlink(outside, filepath.Join(box, "link")))
	p := BuildPolicy(PolicyOptions{
		Workspace:    filepath.Join(box, "ws"),
		TempDir:      filepath.Join(box, "tmp"),
		GitDir:       filepath.Join(root, "repo", ".git", "worktrees", "ws"),
		GitCommonDir: filepath.Join(root, "repo", ".git"),
		Sandbox: spells.Sandbox{Allow: []spells.SandboxAllow{
			{Path: filepath.Join(box, "cache"), Mode: spells.SandboxAccessRW},
			{Path: filepath.Join(box, "link", "cache"), Mode: spells.SandboxAccessRW},
			{Path: filepath.Join(root, "gocache"), Mode: spells.SandboxAccessRWX},
			{Path: filepath.Join(root, "toolchain"), Mode: spells.SandboxAccessRX},
		}},
	})
	assert.Equal(t, []OutsideWrite{
		{Path: filepath.Join(outside, "cache"), Linked: true},
		{Path: filepath.Join(root, "gocache")},
	}, p.WritesOutside(box))
	assert.Nil(t, (*Policy)(nil).WritesOutside(box))
}

// A temp dir inside the checkout changes what tools see: a repository a test creates
// there nests in the workspace's own, and a VCS command run in it rewrites the
// workspace's history. So a TMPDIR inside the workspace is refused, not used.
func TestFromConfigNeverPutsTheTempDirInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(root, ".magus", "tmp"))
	require.NoError(t, os.MkdirAll(os.Getenv("TMPDIR"), 0o700))

	_, err := FromConfig(root, filepath.Join(root, ".magus"), config.SandboxConfig{}, nil)
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

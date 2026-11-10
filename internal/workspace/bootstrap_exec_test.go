package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootstrapFixtureBinaryName is the binary name resolveBootstrapExecTarget
// looks for on this platform.
func bootstrapFixtureBinaryName() string {
	if runtime.GOOS == "windows" {
		return "magus.exe"
	}
	return "magus"
}

// writeBootstrapFixtureWorkspace creates dir/magusfile.buzz and an executable
// dir/magus (or dir/magus.exe), returning the resolved path
// resolveBootstrapExecTarget should report for it.
func writeBootstrapFixtureWorkspace(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("// fixture\n"), 0o644))
	bin := filepath.Join(dir, bootstrapFixtureBinaryName())
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	resolved, err := filepath.EvalSymlinks(bin)
	require.NoError(t, err)
	return resolved
}

func TestResolveBootstrapExecTargetFindsWorkspaceBinary(t *testing.T) {
	dir := t.TempDir()
	want := writeBootstrapFixtureWorkspace(t, dir)

	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	target, ok := resolveBootstrapExecTarget(sub, filepath.Join(dir, "not-the-workspace-binary"))
	require.True(t, ok)
	assert.Equal(t, want, target)
}

// A nested project carries its own magusfile.buzz and no binary. A call
// made from inside it still resolves the workspace root's ./magus.
func TestResolveBootstrapExecTargetFromNestedProjectUsesRootBinary(t *testing.T) {
	root := t.TempDir()
	want := writeBootstrapFixtureWorkspace(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("# fixture\n"), 0o644))
	nested := filepath.Join(root, "console")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "magusfile.buzz"), []byte("// nested\n"), 0o644))

	for _, start := range []string{nested, filepath.Join(nested, "src")} {
		target, ok := resolveBootstrapExecTarget(start, "/nonexistent-self")
		require.True(t, ok, start)
		assert.Equal(t, want, target, start)
	}
}

// A worktree under .claude/worktrees holds its own magus.yaml, so the walk
// stops there. Without its own ./magus it gets no substitution, never the
// parent checkout's binary.
func TestResolveBootstrapExecTargetNeverClimbsIntoParentCheckout(t *testing.T) {
	checkout := t.TempDir()
	writeBootstrapFixtureWorkspace(t, checkout)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "magus.yaml"), []byte("# checkout\n"), 0o644))
	worktree := filepath.Join(checkout, ".claude", "worktrees", "job")
	require.NoError(t, os.MkdirAll(filepath.Join(worktree, "console"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "magus.yaml"), []byte("# worktree\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "magusfile.buzz"), []byte("// worktree\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "console", "magusfile.buzz"), []byte("// nested\n"), 0o644))

	for _, start := range []string{worktree, filepath.Join(worktree, "console")} {
		target, ok := resolveBootstrapExecTarget(start, "/nonexistent-self")
		assert.False(t, ok, "%s resolved %s", start, target)
	}

	own := writeBootstrapFixtureWorkspace(t, worktree)
	target, ok := resolveBootstrapExecTarget(filepath.Join(worktree, "console"), "/nonexistent-self")
	require.True(t, ok)
	assert.Equal(t, own, target)
}

func TestResolveBootstrapExecTargetSkipsSelf(t *testing.T) {
	dir := t.TempDir()
	self := writeBootstrapFixtureWorkspace(t, dir)

	_, ok := resolveBootstrapExecTarget(dir, self)
	assert.False(t, ok, "must not exec into the binary already running")
}

func TestResolveBootstrapExecTargetNoMagusfileAnywhereAbove(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	_, ok := resolveBootstrapExecTarget(sub, "/nonexistent-self")
	assert.False(t, ok)
}

// unsetBootstrapExecEnv clears the env vars BootstrapExecTarget reads. A
// magus that bootstrap-exec'd exports the sentinel to `go test` too, and
// that ambient value would decide the tests below.
func unsetBootstrapExecEnv(t *testing.T) {
	t.Helper()
	t.Setenv(BootstrapExecSentinelVar, "")
	t.Setenv(BootstrapExecOptOutVar, "")
	t.Setenv(PinnedBinaryVar, "")
}

func TestBootstrapExecTargetSentinelPreventsSecondHop(t *testing.T) {
	unsetBootstrapExecEnv(t)
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Setenv(BootstrapExecSentinelVar, "1")

	_, ok := BootstrapExecTarget(dir, filepath.Join(dir, "not-it"))
	assert.False(t, ok, "the sentinel must block a second hop")
}

func TestBootstrapExecTargetOptOut(t *testing.T) {
	unsetBootstrapExecEnv(t)
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Setenv(BootstrapExecOptOutVar, "1")
	t.Chdir(dir)

	_, ok := BootstrapExecTarget("", filepath.Join(dir, "not-it"))
	assert.False(t, ok, "MAGUS_NO_BOOTSTRAP_EXEC must disable the whole mechanism")
}

func TestBootstrapExecTargetGoRunBuildKeepsOwnBinary(t *testing.T) {
	unsetBootstrapExecEnv(t)
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Chdir(dir)

	goRunSelf := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", bootstrapFixtureBinaryName())
	_, ok := BootstrapExecTarget("", goRunSelf)
	assert.False(t, ok, "a go run build must not defer to the placed ./magus")

	installedSelf := filepath.Join(t.TempDir(), "bin", bootstrapFixtureBinaryName())
	_, ok = BootstrapExecTarget("", installedSelf)
	assert.True(t, ok, "an installed binary still defers to the placed ./magus")
}

func TestBootstrapExecTargetCachedGoRunBuildKeepsOwnBinary(t *testing.T) {
	unsetBootstrapExecEnv(t)
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Chdir(dir)

	id := "ab" + strings.Repeat("0", 62)
	cachedSelf := filepath.Join(t.TempDir(), "go-cache", "ab", id+"-d", bootstrapFixtureBinaryName())
	_, ok := BootstrapExecTarget("", cachedSelf)
	assert.False(t, ok, "the escape's go run build runs from the build cache and must not defer to the placed ./magus")
}

func TestBootstrapExecTargetHonorsStartOverCwd(t *testing.T) {
	unsetBootstrapExecEnv(t)
	namedRoot := t.TempDir()
	want := writeBootstrapFixtureWorkspace(t, namedRoot)
	t.Chdir(t.TempDir())

	target, ok := BootstrapExecTarget(namedRoot, filepath.Join(namedRoot, "not-it"))
	require.True(t, ok)
	assert.Equal(t, want, target)
}

func TestBootstrapExecTargetNeverReplacesAPinnedBinary(t *testing.T) {
	unsetBootstrapExecEnv(t)
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Setenv(PinnedBinaryVar, filepath.Join(dir, "magus"))

	_, ok := BootstrapExecTarget(dir, filepath.Join(t.TempDir(), "magus"))
	assert.False(t, ok)
}

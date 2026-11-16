package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/vcs"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestWriteMagusfileStub(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteMagusfileStub(t.Context(), dir, "// starter\n"))
	data, err := os.ReadFile(filepath.Join(dir, "magusfile.buzz"))
	require.NoError(t, err, "expected magusfile.buzz")
	assert.Equal(t, "// starter\n", string(data))
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.buzz"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
		assert.True(t, magusfilePresent(dir), "magusfilePresent should detect %s", name)
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "magusfiles"), 0o755))
	assert.True(t, magusfilePresent(dir), "magusfilePresent should detect magusfiles/ directory")
	assert.False(t, magusfilePresent(t.TempDir()), "magusfilePresent should be false for an empty directory")
}

// An existing magusfile must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(existing, []byte("// mine\n"), 0o644))
	require.NoError(t, WriteMagusfileStub(t.Context(), dir, "// starter\n"))
	data, _ := os.ReadFile(existing)
	assert.Equal(t, "// mine\n", string(data), "existing magusfile.buzz was modified")
}

func TestInstallHooksWritesRefreshAndDriftHooksOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	installer, ok := vcs.Installer("git")
	require.True(t, ok)

	require.NoError(t, installHooks(t.Context(), installer, "git", dir))

	hooks := filepath.Join(dir, ".git", "hooks")
	for file, sections := range map[string][]string{
		"post-checkout": {"# BEGIN magus-refresh"},
		"post-commit":   {"# BEGIN magus-drift-notice"},
		"pre-push":      {"# BEGIN magus-drift-notice"},
	} {
		data, err := os.ReadFile(filepath.Join(hooks, file))
		require.NoError(t, err, file)
		for _, want := range sections {
			assert.Contains(t, string(data), want, file)
		}
		assert.NotContains(t, string(data), "magus-regenerate", file)
	}
	for _, file := range []string{"pre-merge-commit", "pre-commit", "post-applypatch"} {
		assert.NoFileExists(t, filepath.Join(hooks, file), "no hook blocks a commit on a regeneration")
	}
}

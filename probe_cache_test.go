package magus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
)

// probeFixture puts an executable named bin on a PATH of its own and returns a project
// directory beneath a fresh root.
func probeFixture(t *testing.T, bin string, content []byte) string {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, bin), content, 0o755))
	t.Setenv("PATH", binDir)
	dir := filepath.Join(t.TempDir(), "web")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

var pnpmProbe = spells.Command{Bin: "pnpm", Args: []string{"--version"}}

// TestProbeCacheKeyMovesWithEveryInput pins the contract: any input the tool reads to
// pick its version changes the key, and anything else leaves it alone.
func TestProbeCacheKeyMovesWithEveryInput(t *testing.T) {
	dir := probeFixture(t, "pnpm", []byte("\x7fELF binary"))
	key, ok := probeCacheKey(pnpmProbe, dir)
	require.True(t, ok)

	same, _ := probeCacheKey(pnpmProbe, dir)
	assert.Equal(t, key, same)

	t.Setenv("HYPERFINE_RANDOMIZED_ENVIRONMENT_OFFSET", "17")
	unrelated, _ := probeCacheKey(pnpmProbe, dir)
	assert.Equal(t, key, unrelated, "a variable the tool never reads must not miss")

	t.Setenv("npm_config_manage_package_manager_versions", "false")
	configured, _ := probeCacheKey(pnpmProbe, dir)
	assert.NotEqual(t, key, configured)

	// The manifest that pins a version may sit in any ancestor, the workspace root included.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dir), "package.json"), []byte(`{"packageManager":"pnpm@9.0.0"}`), 0o644))
	pinned, _ := probeCacheKey(pnpmProbe, dir)
	assert.NotEqual(t, configured, pinned)

	other, _ := probeCacheKey(pnpmProbe, filepath.Dir(dir))
	assert.NotEqual(t, pinned, other, "a probe answers for its own directory")
}

// TestProbeCacheKeyRefusesWhatItCannotEnumerate: a script wrapper, a tool with no input
// list, and a relative PATH entry all fork every time.
func TestProbeCacheKeyRefusesWhatItCannotEnumerate(t *testing.T) {
	dir := probeFixture(t, "pnpm", []byte("#!/bin/sh\necho 10.0.0\n"))
	_, ok := probeCacheKey(pnpmProbe, dir)
	assert.False(t, ok, "a script decides what runs from inputs nothing here lists")

	dir = probeFixture(t, "go", []byte("\x7fELF binary"))
	_, ok = probeCacheKey(spells.Command{Bin: "go", Args: []string{"version"}}, dir)
	assert.False(t, ok, "go switches toolchains through go.mod and GOTOOLCHAIN")

	dir = probeFixture(t, "node", []byte("\x7fELF binary"))
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"node_modules/.bin")
	_, ok = probeCacheKey(spells.Command{Bin: "node", Args: []string{"--version"}}, dir)
	assert.False(t, ok, "a relative PATH entry resolves per directory")
}

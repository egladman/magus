package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
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

	parent, _ := probeCacheKey(pnpmProbe, filepath.Dir(dir))
	assert.Equal(t, pinned, parent, "two directories that see the same files share an answer")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"packageManager":"pnpm@10.0.0"}`), 0o644))
	own, _ := probeCacheKey(pnpmProbe, dir)
	assert.NotEqual(t, pinned, own, "a manifest created in the directory itself moves its key")
}

// TestProbeCacheKeyRefusesWhatItCannotEnumerate: a script wrapper, a tool with no input
// list, and a relative PATH entry all fork every time.
func TestProbeCacheKeyRefusesWhatItCannotEnumerate(t *testing.T) {
	dir := probeFixture(t, "pnpm", []byte("#!/bin/sh\necho 10.0.0\n"))
	_, ok := probeCacheKey(pnpmProbe, dir)
	assert.False(t, ok, "a script decides what runs from inputs nothing here lists")

	dir = probeFixture(t, "cargo", []byte("\x7fELF binary"))
	_, ok = probeCacheKey(spells.Command{Bin: "cargo", Args: []string{"--version"}}, dir)
	assert.False(t, ok, "a probe with no input list forks")

	dir = probeFixture(t, "govulncheck", []byte("\x7fELF binary"))
	_, ok = probeCacheKey(spells.Command{Bin: "govulncheck", Args: []string{"-version"}}, dir)
	assert.False(t, ok, "govulncheck reports a database that moves on a clock")

	dir = probeFixture(t, "node", []byte("\x7fELF binary"))
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"node_modules/.bin")
	_, ok = probeCacheKey(spells.Command{Bin: "node", Args: []string{"--version"}}, dir)
	assert.False(t, ok, "a relative PATH entry resolves per directory")
}

var goProbe = spells.Command{Bin: "go", Args: []string{"version"}}

// go switches toolchains on the go and toolchain lines of the go.work or go.mod it finds,
// and on GOTOOLCHAIN. Those move its key; the rest of go.mod does not, and two modules
// asking for the same toolchain share one answer.
func TestProbeCacheKeyKeysGoOnItsToolchainLines(t *testing.T) {
	dir := probeFixture(t, "go", []byte("\x7fELF binary"))
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "")
	gomod := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(gomod, []byte("module a\n\ngo 1.25.0\n"), 0o644))
	key, ok := probeCacheKey(goProbe, dir)
	require.True(t, ok)

	require.NoError(t, os.WriteFile(gomod, []byte("module a\n\ngo 1.25.0\n\nrequire example.com/x v1.0.0\n"), 0o644))
	required, _ := probeCacheKey(goProbe, dir)
	assert.Equal(t, key, required, "a require line does not pick a toolchain")

	sibling := filepath.Join(filepath.Dir(dir), "lib")
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "go.mod"), []byte("module b\n\ngo 1.25.0\n"), 0o644))
	shared, _ := probeCacheKey(goProbe, sibling)
	assert.Equal(t, key, shared, "a second module on the same toolchain shares the answer")

	require.NoError(t, os.WriteFile(gomod, []byte("module a\n\ngo 1.25.0\n\ntoolchain go1.26.1\n"), 0o644))
	pinned, _ := probeCacheKey(goProbe, dir)
	assert.NotEqual(t, key, pinned, "a toolchain line switches toolchains")

	t.Setenv("GOTOOLCHAIN", "go1.27.0")
	forced, _ := probeCacheKey(goProbe, dir)
	assert.NotEqual(t, pinned, forced, "GOTOOLCHAIN switches toolchains")

	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dir), "go.work"), []byte("go 1.26.0\n\nuse ./web\n"), 0o644))
	worked, _ := probeCacheKey(goProbe, dir)
	assert.NotEqual(t, forced, worked, "a go.work above the module decides the toolchain")
}

// newProbeMagus is a workspace with a cache directory and nothing else, which is all the
// probe cache reads.
func newProbeMagus(t *testing.T) *Magus {
	t.Helper()
	return &Magus{ws: &types.Workspace{Root: t.TempDir()}}
}

// Seven project directories that see the same inputs ask at once; one fork answers all of
// them, and the next ask is a cache hit.
func TestProbeCachedForksOncePerKeyAcrossDirs(t *testing.T) {
	dir := probeFixture(t, "golangci-lint", []byte("\x7fELF binary"))
	m := newProbeMagus(t)
	probe := spells.Command{Bin: "golangci-lint", Args: []string{"--version"}}

	var forks atomic.Int32
	release := make(chan struct{})
	fork := func() (string, error) {
		forks.Add(1)
		<-release
		return "golangci-lint has version 2.12.2", nil
	}
	var wg sync.WaitGroup
	answers := make([]string, 7)
	for i := range answers {
		project := filepath.Join(filepath.Dir(dir), fmt.Sprintf("p%d", i))
		require.NoError(t, os.MkdirAll(project, 0o755))
		wg.Go(func() {
			out, err := m.probeCached(t.Context(), probe, project, fork)
			assert.NoError(t, err)
			answers[i] = out
		})
	}
	// Long enough for every caller to join the flight before it lands; a caller that
	// arrives after reads the stored answer instead, which the fork count also covers.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), forks.Load())
	for _, out := range answers {
		assert.Equal(t, "golangci-lint has version 2.12.2", out)
	}
	out, err := m.probeCached(t.Context(), probe, dir, fork)
	require.NoError(t, err)
	assert.Equal(t, "golangci-lint has version 2.12.2", out)
	assert.Equal(t, int32(1), forks.Load(), "the answer outlives the flight on disk")
}

// A failed probe is cached under the same key, for probeFailureTTL: an uninstalled
// node_modules fails the same way on every query until an install moves the key.
func TestProbeCachedCachesAFailureForItsTTL(t *testing.T) {
	dir := probeFixture(t, "pnpm", []byte("\x7fELF binary"))
	m := newProbeMagus(t)
	probe := spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--version"}}
	forks := 0
	fork := func() (string, error) {
		forks++
		return "", errors.New("version probe pnpm [exec tsc --version]: exit status 254")
	}

	_, err := m.probeCached(t.Context(), probe, dir, fork)
	require.Error(t, err)
	_, err = m.probeCached(t.Context(), probe, dir, fork)
	require.EqualError(t, err, "version probe pnpm [exec tsc --version]: exit status 254")
	assert.Equal(t, 1, forks, "the second ask replays the failure")

	key, ok := probeCacheKey(probe, dir)
	require.True(t, ok)
	expired := time.Now().Add(-probeFailureTTL - time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(m.CacheDir(), "probes", key+probeFailedSuffix), expired, expired))
	_, err = m.probeCached(t.Context(), probe, dir, fork)
	require.Error(t, err)
	assert.Equal(t, 2, forks, "an expired failure forks again")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", ".bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", ".bin", "tsc"), []byte("#!/bin/sh\n"), 0o755))
	installed, _ := probeCacheKey(probe, dir)
	assert.NotEqual(t, key, installed, "an install moves the key, so a cached failure cannot outlive it")
}

// A cancelled probe says nothing about the tool and is not cached.
func TestProbeCachedDoesNotCacheACancelledProbe(t *testing.T) {
	dir := probeFixture(t, "buf", []byte("\x7fELF binary"))
	m := newProbeMagus(t)
	probe := spells.Command{Bin: "buf", Args: []string{"--version"}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.probeCached(ctx, probe, dir, func() (string, error) { return "", ctx.Err() })
	require.Error(t, err)

	key, _ := probeCacheKey(probe, dir)
	assert.NoFileExists(t, filepath.Join(m.CacheDir(), "probes", key+probeFailedSuffix))
}

package magus

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
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

var tscProbe = spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--version"}}

// tscWorkspace is a pnpm project with a lockfile and no node_modules, on a PATH holding
// only a pnpm binary, opened over a cache in a fresh directory. probe answers every tsc
// probe and counts the spawns it stands in for.
func tscWorkspace(t *testing.T, probe func(dir string) (string, error)) (m *Magus, p *types.Project, calls *int) {
	t.Helper()
	dir := probeFixture(t, "pnpm", []byte("\x7fELF binary"))
	root := filepath.Dir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"devDependencies":{"typescript":"5.8.3"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644))

	install := spells.Install{
		Name: "pnpm-install", Command: spells.Command{Bin: "pnpm", Args: []string{"install"}},
		Dir: "node_modules", Stamps: []string{"node_modules/.modules.yaml"}, Tools: []string{"node", "pnpm"},
	}
	calls = new(int)
	sp := spells.NewSpell("typescript",
		spells.WithTools(map[string]spells.Tool{"tsc": {Probe: tscProbe}}),
		spells.WithOps(map[string]spells.Op{"pnpm-install": {Kind: spells.OpKindInstall, Install: &spells.InstallSpec{
			Spell: "typescript",
			Manifests: []spells.Manifest{{
				Value: "package.json", LockCandidates: []string{"pnpm-lock.yaml"},
				Installs: map[string]spells.Install{"pnpm-lock.yaml": install},
			}},
		}}}),
		spells.WithTargets("pnpm-install"),
		spells.WithVersionProber(func(_ context.Context, _ spells.Command, dir string) (string, error) {
			*calls++
			return probe(dir)
		}),
	)
	p = &types.Project{Path: "web", Dir: dir, ResolvedSpells: []*spells.Spell{sp}, MagusfileTargets: []string{"install"}}
	cacheDir := filepath.Join(root, ".magus")
	c, err := cache.Open(t.Context(), cacheDir)
	require.NoError(t, err)
	m = &Magus{
		cache: c, cfg: config.Config{Cache: config.Cache{Dir: cacheDir}},
		ws: &types.Workspace{Root: root, Projects: map[string]*types.Project{"web": p}},
	}
	return m, p, calls
}

var errTscNotFound = errors.New(`version probe pnpm [exec tsc --version]: exit status 254: ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL Command "tsc" not found`)

// The defect this pins: in a worktree with no node_modules, `pnpm exec tsc --version`
// forked and warned on every magus invocation, because only a successful probe was ever
// cached. A failed one is now recorded, so it costs one spawn and one warning per change
// to the probe's inputs, and installing is such a change.
func TestAnAbsentToolCostsOneProbePerInputChange(t *testing.T) {
	installed := false
	m, p, calls := tscWorkspace(t, func(string) (string, error) {
		if installed {
			return "Version 5.8.3", nil
		}
		return "", errTscNotFound
	})
	var logs bytes.Buffer
	ctx := t.Context()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Each call stands for a separate invocation: a fresh prober, so only the cache
	// on disk carries anything between them.
	for range 3 {
		got, err := m.toolVersionsByProject(ctx, []*types.Project{p})
		require.NoError(t, err, "an absent tool is not an error")
		assert.Equal(t, []string{"typescript:tsc:UNPROBED"}, got["web"])
	}
	assert.Equal(t, 1, *calls, "three invocations, one spawn")
	assert.Equal(t, 1, strings.Count(logs.String(), "level=WARN"), "and one warning")
	assert.Contains(t, logs.String(), "tsc is not installed, so its cache key records UNPROBED: no node_modules in web: run `magus run install web`")
	assert.NotContains(t, logs.String(), "exit status", "the warning names the cause, not a raw exit code")

	require.NoError(t, os.MkdirAll(filepath.Join(p.Dir, "node_modules", ".bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "node_modules", ".modules.yaml"), []byte("layoutVersion: 5\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "node_modules", ".bin", "tsc"), []byte("#!/bin/sh\n"), 0o755))
	installed = true

	for range 2 {
		got, err := m.toolVersionsByProject(ctx, []*types.Project{p})
		require.NoError(t, err)
		assert.Equal(t, []string{"typescript:tsc:Version 5.8.3"}, got["web"], "the install moved the key")
	}
	assert.Equal(t, 2, *calls, "the install is one input change, so one more spawn")
}

// Each way a pnpm-run tool can be missing names its own fix.
func TestAnAbsentToolNamesItsCause(t *testing.T) {
	m, p, _ := tscWorkspace(t, func(string) (string, error) { return "", errTscNotFound })
	sp := p.ResolvedSpells[0]
	cause := func() string {
		got, absent := m.probeAbsence(sp, tscProbe, p.Dir, errTscNotFound)
		require.True(t, absent)
		return got
	}

	assert.Equal(t, "no node_modules in web: run `magus run install web`", cause())

	require.NoError(t, os.MkdirAll(filepath.Join(p.Dir, "node_modules"), 0o755))
	assert.Equal(t, "the install in web never finished (node_modules/.modules.yaml is missing): run `magus run install web`", cause())

	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "node_modules", ".modules.yaml"), nil, 0o644))
	assert.Equal(t, "the install in web provides no tsc: add the package that ships it to package.json", cause())

	p.MagusfileTargets = nil
	require.NoError(t, os.Remove(filepath.Join(p.Dir, "node_modules", ".modules.yaml")))
	assert.Contains(t, cause(), "run `magus run pnpm-install web`", "with no install target, the spell's op")

	require.NoError(t, os.MkdirAll(filepath.Join(p.Dir, "node_modules", ".bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "node_modules", ".bin", "tsc"), []byte("#!/bin/sh\n"), 0o755))
	_, absent := m.probeAbsence(sp, tscProbe, p.Dir, errTscNotFound)
	assert.False(t, absent, "a tsc pnpm exec would run is present, whatever the probe said")
}

// A present tool whose probe fails is MGS3035, and is never recorded: the next run asks
// again, since nothing about a broken tool says the same inputs break it the same way.
func TestAPresentToolThatCannotBeProbedFailsEveryRun(t *testing.T) {
	m, p, calls := tscWorkspace(t, func(string) (string, error) {
		return "", errors.New(`version probe pnpm [exec tsc --version]: exit status 1: Error: Cannot find module 'typescript/lib/tsc.js'`)
	})
	require.NoError(t, os.MkdirAll(filepath.Join(p.Dir, "node_modules", ".bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "node_modules", ".bin", "tsc"), []byte("#!/bin/sh\n"), 0o755))

	for range 2 {
		_, err := m.toolVersionsByProject(t.Context(), []*types.Project{p})
		require.ErrorIs(t, err, types.ToolUnprobeable)
		assert.Contains(t, err.Error(), "Cannot find module 'typescript/lib/tsc.js'")
	}
	assert.Equal(t, 2, *calls)
}

// The tsc probe's key moves with the install, and refuses to exist when pnpm exec could
// fall back to a tsc on PATH, whose inputs nothing here lists.
func TestTscProbeKeyFollowsTheInstall(t *testing.T) {
	dir := probeFixture(t, "pnpm", []byte("\x7fELF binary"))
	bare, ok := probeCacheKey(tscProbe, dir)
	require.True(t, ok)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", ".modules.yaml"), nil, 0o644))
	stamped, _ := probeCacheKey(tscProbe, dir)
	assert.NotEqual(t, bare, stamped, "an install's stamp")

	require.NoError(t, os.Symlink(".pnpm/typescript@5.8.3/node_modules/typescript", filepath.Join(dir, "node_modules", "typescript")))
	linked, _ := probeCacheKey(tscProbe, dir)
	assert.NotEqual(t, stamped, linked, "the link that names the version")

	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("PATH"), "tsc"), []byte("\x7fELF binary"), 0o755))
	_, ok = probeCacheKey(tscProbe, dir)
	assert.False(t, ok, "a tsc on PATH is what pnpm exec falls back to")
}

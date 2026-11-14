package magus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// probeFixture puts an executable named bin on a PATH of its own and returns a project
// directory beneath a fresh root.
func probeFixture(t *testing.T, bin string, content []byte) string {
	t.Helper()
	binDir := testkit.NewWorkspace(t)
	binDir.WriteExecutable(bin, string(content))
	t.Setenv("PATH", binDir.Root())
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
	t.Setenv("PATH", fmt.Sprintf("%s%snode_modules/.bin", os.Getenv("PATH"), string(os.PathListSeparator)))
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

var errTscNotFound = errors.New(`version probe pnpm [exec tsc --version]: ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL Command "tsc" not found: exit status 254`)

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

// A present tool whose probe fails is MGS3035 on every run, not only the one that forked:
// a failure replayed from the cache within probeFailureTTL refuses the same way.
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
	assert.Equal(t, 1, *calls, "the second run replays the recorded failure")
	key, ok := probeCacheKey(tscProbe, p.Dir)
	require.True(t, ok)
	assert.NoFileExists(t, filepath.Join(m.CacheDir(), "probes", key+probeAbsentSuffix), "a broken tool is not recorded as absent")
}

// An absence is recorded without a TTL: its cause is in the key, so it holds until an
// input moves rather than re-forking and re-warning on a clock.
func TestAnAbsenceOutlivesTheFailureTTL(t *testing.T) {
	m, p, calls := tscWorkspace(t, func(string) (string, error) { return "", errTscNotFound })
	_, err := m.toolVersionsByProject(t.Context(), []*types.Project{p})
	require.NoError(t, err)

	key, ok := probeCacheKey(tscProbe, p.Dir)
	require.True(t, ok)
	expired := time.Now().Add(-probeFailureTTL - time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(m.CacheDir(), "probes", key+probeAbsentSuffix), expired, expired))

	got, err := m.toolVersionsByProject(t.Context(), []*types.Project{p})
	require.NoError(t, err)
	assert.Equal(t, []string{"typescript:tsc:UNPROBED"}, got["web"])
	assert.Equal(t, 1, *calls)
}

// The tsc probe's key moves with the install, and with a tsc on PATH, which pnpm exec
// falls back to.
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
	onPath, ok := probeCacheKey(tscProbe, dir)
	require.True(t, ok)
	assert.NotEqual(t, linked, onPath, "a tsc on PATH, which pnpm exec falls back to")
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

// miseState is a mise data, state and config directory set under one root.
type miseState struct {
	trusted, installs string
}

// newMiseState trusts trusted configs and installs tools tools, two versions
// each. Every directory is backdated an hour, as on a machine left alone.
func newMiseState(tb testing.TB, trusted, tools int) miseState {
	tb.Helper()
	root := tb.TempDir()
	st := miseState{trusted: filepath.Join(root, "state", "trusted-configs"), installs: filepath.Join(root, "data", "installs")}
	tb.Setenv("MISE_DATA_DIR", filepath.Join(root, "data"))
	tb.Setenv("MISE_STATE_DIR", filepath.Join(root, "state"))
	tb.Setenv("MISE_CONFIG_DIR", filepath.Join(root, "config"))
	require.NoError(tb, os.MkdirAll(st.trusted, 0o755))
	for i := range trusted {
		require.NoError(tb, os.Symlink(fmt.Sprintf("/work/tree-%d/mise.toml", i), filepath.Join(st.trusted, fmt.Sprintf("tree-%d-mise.toml-%016x", i, i))))
	}
	for i := range tools {
		tool := filepath.Join(st.installs, fmt.Sprintf("tool-%d", i))
		for _, v := range []string{"1.0.0", "1.1.0"} {
			require.NoError(tb, os.MkdirAll(filepath.Join(tool, v), 0o755))
		}
		require.NoError(tb, os.Symlink("./1.1.0", filepath.Join(tool, "1")))
		backdate(tb, tool)
	}
	backdate(tb, st.trusted)
	backdate(tb, st.installs)
	return st
}

// backdate moves dir's modification time an hour back.
func backdate(tb testing.TB, dir string) {
	tb.Helper()
	old := time.Now().Add(-time.Hour)
	require.NoError(tb, os.Chtimes(dir, old, old))
}

// BenchmarkWriteMiseGlobals keys a mise shim's globals on a machine with as
// many trusted configs as a checkout that makes a worktree per job collects.
func BenchmarkWriteMiseGlobals(b *testing.B) {
	newMiseState(b, 830, 40)
	for b.Loop() {
		var key strings.Builder
		if err := writeMiseGlobals(&key, time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}

// ProbeMiseGlobalsSuite pins what moves a mise shim's key: which configs are
// trusted or ignored and which versions are installed.
type ProbeMiseGlobalsSuite struct {
	suite.Suite
	st miseState
}

func TestProbeMiseGlobalsSuite(t *testing.T) { suite.Run(t, new(ProbeMiseGlobalsSuite)) }

func (s *ProbeMiseGlobalsSuite) SetupTest() { s.st = newMiseState(s.T(), 830, 4) }

// key reads the globals an hour on, when every directory has settled.
func (s *ProbeMiseGlobalsSuite) key() string {
	key, err := s.keyAt(time.Now().Add(time.Hour))
	s.Require().NoError(err)
	return key
}

func (s *ProbeMiseGlobalsSuite) keyAt(now time.Time) (string, error) {
	var b strings.Builder
	err := writeMiseGlobals(&b, now)
	return b.String(), err
}

func (s *ProbeMiseGlobalsSuite) stat(dir string) os.FileInfo {
	fi, err := os.Stat(dir)
	s.Require().NoError(err)
	return fi
}

// restore puts back the modification time dir had when before was read.
func (s *ProbeMiseGlobalsSuite) restore(dir string, before os.FileInfo) {
	s.Require().NoError(os.Chtimes(dir, before.ModTime(), before.ModTime()))
}

func (s *ProbeMiseGlobalsSuite) trust(name, config string) {
	s.Require().NoError(os.Symlink(config, filepath.Join(s.st.trusted, name)))
}

func (s *ProbeMiseGlobalsSuite) TestUnchangedDirectoriesKeepTheKey() {
	key := s.key()
	s.Equal(key, s.key())
	s.NotContains(key, " -> ", "a symlink entry costs no readlink")
}

func (s *ProbeMiseGlobalsSuite) TestAnAddedTrustedConfigMovesTheKey() {
	key := s.key()
	s.trust("new-mise.toml-00ff", "/work/new/mise.toml")
	s.NotEqual(key, s.key())
}

func (s *ProbeMiseGlobalsSuite) TestARemovedTrustedConfigMovesTheKey() {
	key := s.key()
	s.Require().NoError(os.Remove(filepath.Join(s.st.trusted, fmt.Sprintf("tree-7-mise.toml-%016x", 7))))
	s.NotEqual(key, s.key())
}

func (s *ProbeMiseGlobalsSuite) TestARetargetedTrustSymlinkMovesTheKey() {
	name := fmt.Sprintf("tree-7-mise.toml-%016x", 7)
	key := s.key()
	s.Require().NoError(os.Remove(filepath.Join(s.st.trusted, name)))
	s.trust(name, "/work/elsewhere/mise.toml")
	s.NotEqual(key, s.key())
}

// A symlink renamed over the old one is how an atomic retarget is done.
func (s *ProbeMiseGlobalsSuite) TestATrustSymlinkReplacedByRenameMovesTheKey() {
	name := fmt.Sprintf("tree-7-mise.toml-%016x", 7)
	key := s.key()
	s.trust("staged", "/work/elsewhere/mise.toml")
	s.Require().NoError(os.Rename(filepath.Join(s.st.trusted, "staged"), filepath.Join(s.st.trusted, name)))
	s.NotEqual(key, s.key())
}

// Paranoid mode rewrites a .hash file in place, which leaves the directory's
// own time alone.
func (s *ProbeMiseGlobalsSuite) TestAHashRewrittenInPlaceMovesTheKey() {
	hash := filepath.Join(s.st.trusted, "tree-7-mise.toml-0007.hash")
	s.Require().NoError(os.WriteFile(hash, []byte("aaaa"), 0o644))
	backdate(s.T(), s.st.trusted)
	before, err := os.Stat(s.st.trusted)
	s.Require().NoError(err)
	key := s.key()

	s.Require().NoError(os.WriteFile(hash, []byte("bbbb"), 0o644))
	after, err := os.Stat(s.st.trusted)
	s.Require().NoError(err)
	s.Require().Equal(before.ModTime(), after.ModTime(), "the directory did not move")
	s.NotEqual(key, s.key())
}

func (s *ProbeMiseGlobalsSuite) TestAnIgnoredConfigMovesTheKey() {
	key := s.key()
	ignored := filepath.Join(filepath.Dir(s.st.trusted), "ignored-configs")
	s.Require().NoError(os.MkdirAll(ignored, 0o755))
	s.Require().NoError(os.Symlink("/work/tree-3/mise.toml", filepath.Join(ignored, "tree-3-mise.toml-0003")))
	s.NotEqual(key, s.key())
}

func (s *ProbeMiseGlobalsSuite) TestANewInstallMovesTheKey() {
	key := s.key()
	s.Require().NoError(os.Mkdir(filepath.Join(s.st.installs, "tool-0", "1.2.0"), 0o755))
	s.NotEqual(key, s.key())
}

func (s *ProbeMiseGlobalsSuite) TestARetargetedRuntimeSymlinkMovesTheKey() {
	link := filepath.Join(s.st.installs, "tool-0", "1")
	key := s.key()
	s.Require().NoError(os.Remove(link))
	s.Require().NoError(os.Symlink("./1.0.0", link))
	s.NotEqual(key, s.key())
}

// tar, rsync and a cache restore put a directory's old time back. So does a
// filesystem whose clock lags the process's.
func (s *ProbeMiseGlobalsSuite) TestATrustReplacedUnderARestoredTimeMovesTheKey() {
	before := s.stat(s.st.trusted)
	key := s.key()
	s.Require().NoError(os.Remove(filepath.Join(s.st.trusted, fmt.Sprintf("tree-7-mise.toml-%016x", 7))))
	s.trust(fmt.Sprintf("tree-7-mise.toml-%016x", 0xff), "/work/tree-new/mise.toml")
	s.restore(s.st.trusted, before)
	s.NotEqual(key, s.key())
}

// A version is a name two levels below installs.
func (s *ProbeMiseGlobalsSuite) TestAnInstallReplacedUnderARestoredTimeMovesTheKey() {
	tool := filepath.Join(s.st.installs, "tool-0")
	before := s.stat(tool)
	key := s.key()
	s.Require().NoError(os.Remove(filepath.Join(tool, "1.0.0")))
	s.Require().NoError(os.Mkdir(filepath.Join(tool, "1.2.0"), 0o755))
	s.restore(tool, before)
	s.NotEqual(key, s.key())
}

// One state keys one way: a directory changed within dirSettleAge keys
// nothing, rather than keying differently until the window passes.
func (s *ProbeMiseGlobalsSuite) TestARecentlyChangedDirectoryKeysNothing() {
	_, err := s.keyAt(s.stat(s.st.trusted).ModTime().Add(time.Second))
	s.ErrorContains(err, "trusted-configs")
}

// The probe that forks uncached for a fresh directory says so at the level
// a person sees by default.
func (s *ProbeMiseGlobalsSuite) TestAProbeForkedForAFreshDirectorySaysSo() {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	s.T().Cleanup(func() { slog.SetDefault(prev) })
	dir := probeFixture(s.T(), "mise", []byte("\x7fELF binary"))
	bin := os.Getenv("PATH")
	s.Require().NoError(os.Symlink(filepath.Join(bin, "mise"), filepath.Join(bin, "node")))
	now := time.Now()
	s.Require().NoError(os.Chtimes(s.st.trusted, now, now))

	_, ok := probeCacheKey(spells.Command{Bin: "node", Args: []string{"--version"}}, dir)
	s.False(ok)
	s.Contains(logs.String(), "level=INFO")
	s.Contains(logs.String(), "trusted-configs")
}

// On a filesystem with one-second times, a change later in the tick leaves
// the directory's time alone. A symlink retargeted under its own name
// changes no name either.
func (s *ProbeMiseGlobalsSuite) TestADirectoryInsideItsTimestampTickKeysNothing() {
	tick := time.Now().Add(-time.Minute).Truncate(time.Second)
	s.Require().NoError(os.Chtimes(s.st.trusted, tick, tick))
	_, err := s.keyAt(tick.Add(time.Second))
	s.Error(err)
}

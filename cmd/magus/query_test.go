package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type indexesBehindSuite struct{ suite.Suite }

func TestIndexesBehindSuite(t *testing.T) { suite.Run(t, new(indexesBehindSuite)) }

func (s *indexesBehindSuite) TestAGapNoBuildClosesGetsNoCause() {
	proto := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("proto", "/ws/proto"), Language: "protobuf", Unindexable: true},
	})
	s.Nil(withIndexCause(s.T().Context(), s.T().TempDir(), proto).IndexCause)
}

func (s *indexesBehindSuite) TestExplainMissDropsAGapHoldingNoTextOfTheName() {
	proto := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("proto", "/ws/proto"), Language: "protobuf", Unindexable: true},
	})
	got := explainMissAnswer(s.T().Context(), s.T().TempDir(), "file:nosuch/go.mod", proto)
	s.Equal(types.VerdictAbsent, got.Verdict)
	s.Empty(got.Gaps)
}

func (s *indexesBehindSuite) TestExplainMissKeepsAGapWhoseProjectHoldsTheName() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "proto"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "proto", "a.proto"), []byte("message Wanted {}\n"), 0o644))
	proto := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("proto", "/ws/proto"), Language: "protobuf", Unindexable: true},
	})
	got := explainMissAnswer(s.T().Context(), root, "Wanted", proto)
	s.Equal(types.VerdictUnknown, got.Verdict)
	s.Len(got.Gaps, 1)
}

func (s *indexesBehindSuite) TestACurrentAnswerIsNotBehind() {
	s.False(types.ClassifyAnswer(true, "", nil).IndexBehind())
}

func (s *indexesBehindSuite) TestAGapNoBuildClosesIsNotBehind() {
	s.False(types.KnowledgeAnswer{Gaps: []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("proto", "/ws/proto"), Language: "protobuf", Unindexable: true},
		{Project: types.NewProjectRef("web", "/ws/web"), Language: "typescript", Hint: "install scip-typescript"},
	}}.IndexBehind())
}

func (s *indexesBehindSuite) TestABuildableGapIsBehind() {
	s.True(types.KnowledgeAnswer{Gaps: []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("libs/a", "/ws/libs/a"), Language: "go"},
	}}.IndexBehind())
}

func (s *indexesBehindSuite) TestAStaleIndexIsBehind() {
	s.True(types.KnowledgeAnswer{
		StaleIndexes:      []string{"libs/a"},
		StaleIndexDetails: []types.KnowledgeStaleIndex{{Project: "libs/a", Language: "go", Op: "scip"}},
	}.IndexBehind())
}

func (s *indexesBehindSuite) TestAnAnswerKeepsTheCauseTheReadGaveIt() {
	mine := &types.KnowledgeIndexCause{Why: "a refresh started", Fix: "ask again"}
	got := withIndexCause(s.T().Context(), s.T().TempDir(), types.KnowledgeAnswer{StaleIndexes: []string{"libs/a"}, IndexCause: mine})
	s.Same(mine, got.IndexCause)
}

// captureStdout is testkit.CaptureStdout. showOutputIdentity prints straight to
// os.Stdout (matching the rest of `magus query`'s text-format rendering), so swapping
// the variable is the only way to observe it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return testkit.CaptureStdout(t, fn)
}

// captureStderr is testkit.CaptureStderr with the default logger's records rendered as
// plain lines into the same stream, so a notice reads as it does on a terminal.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return testkit.CaptureStderr(t, func() {
		prev := slog.Default()
		defer slog.SetDefault(prev)
		slog.SetDefault(slog.New(cache.NewPlainHandler(os.Stderr, slog.LevelInfo)))
		fn()
	})
}

// TestSplitQueryNegations pins the shield that makes the grammar's documented
// negation reachable from the CLI: a dash token the query flag set does not know
// is a search term, never a flag error, while real flags, their values, "="
// spellings, and the "--" tail keep today's parse.
func TestSplitQueryNegations(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	cases := []struct {
		name      string
		args      []string
		kept      []string
		negations []string
	}{
		{"field negation", []string{"docker", "-kind:op"}, []string{"docker"}, []string{"-kind:op"}},
		{"bare negation", []string{"cache", "-remote"}, []string{"cache"}, []string{"-remote"}},
		{"registered flag survives", []string{"docker", "-o", "json"}, []string{"-o", "json", "docker"}, nil},
		{"value flag keeps its value", []string{"--url", "-kind:op", "docker"}, []string{"--url", "-kind:op", "docker"}, nil},
		{"equals spelling stays a flag error", []string{"-kind=op"}, []string{"-kind=op"}, nil},
		{"double dash tail untouched", []string{"a", "--", "-kind:op"}, []string{"a", "--", "-kind:op"}, nil},
		{"short help is a flag", []string{"-h"}, []string{"-h"}, nil},
		{"long help is a flag", []string{"docker", "--help"}, []string{"--help", "docker"}, nil},
		{"single-dash help is a flag", []string{"-help"}, []string{"-help"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, negations := splitQueryNegations(tc.args)
			assert.Equal(t, tc.kept, kept)
			assert.Equal(t, tc.negations, negations)
		})
	}
}

// TestQueryHelpPrintsUsage pins that a help flag prints query's usage and searches
// nothing, rather than reaching the graph as a term.
func TestQueryHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		t.Run(arg, func(t *testing.T) {
			t.Cleanup(snapshotGlobals())
			var err error
			out := captureStderr(t, func() { err = queryCommand(context.Background(), t.TempDir(), []string{arg}) })
			require.ErrorIs(t, err, flag.ErrHelp)
			assert.Contains(t, out, "Usage: magus query <terms> [flags]")
		})
	}
}

// TestReportRefLookupError_NoDoubledConsulted guards the RefNotFoundError rendering
// bug: Error() already names the stores it consulted, so the wrapper must not append a
// second "(consulted: ...)". m is nil here on purpose: this branch exercises
// the not-exist rendering in isolation, and a nil Magus must skip the suggestion rather
// than panic (the txtar coverage exercises the suggestion with a real workspace).
func TestReportRefLookupError_NoDoubledConsulted(t *testing.T) {
	err := &cache.RefNotFoundError{Ref: "outdeadbeef0000", Stores: []string{"local cache"}}

	out := captureStderr(t, func() {
		got := reportRefLookupError(context.Background(), nil, "outdeadbeef0000", err)
		require.Error(t, got)
	})

	assert.Contains(t, out, `no stored output for ref "outdeadbeef0000", consulted local cache`)
	assert.Equal(t, 1, strings.Count(out, "consulted"), "the stores must be named exactly once: %q", out)
}

// newQueryTestWorkspace opens a real (cache-backed) single-project workspace bound to a
// spell providing target "build": the matched-ref suggestion needs IdentifyRef, which
// needs a live cache (ComputeTargetKey returns types.ErrNoCache on an Inspect workspace).
func newQueryTestWorkspace(t *testing.T) *magus.Magus {
	t.Helper()
	spellName := register.Spell(t, "zzz-query-ref-spell", spells.WithTargets("build"))

	w := testkit.NewWorkspace(t)
	w.Magusfile("")

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), w.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestReportRefLookupError_MatchedRefSuggestsRunCommand covers the case the txtar
// scripts cannot express: IdentifyRef needs a real cache-backed workspace and a ref
// that is a live target's PREDICTED key, which a static fixture file cannot pin (the
// key is a content hash). Here the ref is computed via ComputeTargetKey exactly like a
// real run would mint it, then looked up before anything ever produced it: the
// not-exist path must invert it back to a `magus run build` suggestion, with the root
// project omitted since the target lives at ".".
func TestReportRefLookupError_MatchedRefSuggestsRunCommand(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	m := newQueryTestWorkspace(t)
	ctx := context.Background()

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	_, _, lookupErr := m.OutputByRef(ctx, ref)
	require.Error(t, lookupErr, "ref must not already be stored")

	out := captureStderr(t, func() {
		got := reportRefLookupError(ctx, m, ref, lookupErr)
		require.Error(t, got)
	})

	assert.Contains(t, out, "Nothing has produced it here, but this workspace would print it for:")
	assert.Contains(t, out, "magus run build\n", "root project (\".\") must be omitted from the suggested command")
	assert.NotContains(t, out, "magus run build .", "root project must not be spelled out as \".\"")
}

// TestQueryOutputReadsTrailPayloads pins the other prefixes in the shared ref namespace: a
// repeated guard deny cites a grd ref, and `query output` is the command it names, so that
// ref must resolve here rather than be refused as malformed or looked up as a run.
func TestQueryOutputReadsTrailPayloads(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m := newQueryTestWorkspace(t)
	ctx := withMagus(context.Background(), m)
	ref, _ := trail.WriteBlob(ctx, m.CacheDir(), "grd", []byte("the full verdict"))
	require.NotEmpty(t, ref)

	out := captureStdout(t, func() {
		require.NoError(t, queryCommand(ctx, "", []string{"output", ref}))
	})
	assert.Equal(t, "the full verdict\n", out)

	out = captureStdout(t, func() {
		require.NoError(t, queryTrailPayload(ctx, "", ref, output.Options{Format: outputJSON}))
	})
	assert.JSONEq(t, fmt.Sprintf("{\"ref\":\"%s\",\"output\":\"the full verdict\"}", ref), out)

	assert.Error(t, queryCommand(ctx, "", []string{"--attempts", "output", ref}), "run flags are refused on a payload")

	missing := "grd" + strings.Repeat("0", 16)
	stderr := captureStderr(t, func() {
		var silent reportedExit
		require.ErrorAs(t, queryTrailPayload(ctx, "", missing, output.Options{Format: outputText}), &silent)
	})
	assert.Contains(t, stderr, fmt.Sprintf("no stored payload for ref \"%s\"", missing))
}

// TestShowOutputIdentity_RevisionRendering drives showOutputIdentity end to end in a real git
// workspace: a target's descriptor is stamped with the revision HEAD was at when it
// ran (CurrentRevision, resolved once by executeStages), and --identity must render it:
// silently when it still matches HEAD, and with a "recorded at X, you are on Y" line
// once a later commit moves HEAD away from it.
func TestShowOutputIdentity_RevisionRendering(t *testing.T) {
	repo := testkit.NewRepository(t, map[string]string{"magusfile.buzz": ""})
	firstRev := repo.Head()

	spellName := register.Spell(t, "zzz-query-meta-revision-spell", spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) { return nil, nil }))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), repo.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	out := captureStdout(t, func() {
		require.NoError(t, showOutputIdentity(ctx, m, ref, output.Options{}))
	})
	assert.Contains(t, out, "rev:     "+firstRev[:12], "the descriptor records the revision HEAD was at when the target ran")
	assert.NotContains(t, out, "recorded at", "HEAD has not moved yet, so nothing to call out")

	// Move HEAD to a second commit without re-running the target: the stored
	// descriptor still names firstRev, but the workspace is no longer there.
	secondRev := repo.Commit("second", map[string]string{"other.txt": "x"})
	require.NotEqual(t, firstRev, secondRev)

	out = captureStdout(t, func() {
		require.NoError(t, showOutputIdentity(ctx, m, ref, output.Options{}))
	})
	assert.Contains(t, out,
		fmt.Sprintf("recorded at %s, you are on %s.", firstRev[:12], secondRev[:12]),
	)
}

// TestPrintIdentifyRefSuggestion_MultipleMatches covers printIdentifyRefSuggestion's
// default case (len(matches) > 1): "any of:" plus every match's command, not just
// one. A ref match is a content-hash prefix, so forcing a real collision takes many
// distinct targets rather than a hand-picked one: this computes the ACTUAL live
// keys for 32 targets and uses whichever first hex digit two of them really land
// on (a hardcoded prefix would be asserting on a guess, not on printIdentifyRefSuggestion's
// behavior). A collision is CERTAIN rather than likely: a first hex digit has 16
// values and this keys 32 targets, so pigeonhole forces at least one pair. Keep the
// count above 16 if you change it: dropping to a "probably enough" number trades a
// guarantee for a flake.
func TestPrintIdentifyRefSuggestion_MultipleMatches(t *testing.T) {
	targets := make([]string, 32)
	for i := range targets {
		targets[i] = fmt.Sprintf("build%02d", i)
	}
	spellName := register.Spell(t, "zzz-query-multi-spell", spells.WithTargets(targets...))

	w := testkit.NewWorkspace(t)
	w.Magusfile("")

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), w.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	byFirstDigit := map[byte][]string{}
	fullRef := map[string]string{}
	for _, target := range targets {
		key, _, err := m.ComputeTargetKey(ctx, ".", target, nil)
		require.NoErrorf(t, err, "ComputeTargetKey(%s)", target)
		ref := cache.PortableRef(key)
		fullRef[target] = ref
		digit := ref[len(cache.RefPrefix)]
		byFirstDigit[digit] = append(byFirstDigit[digit], target)
	}
	var collidingTargets []string
	for _, group := range byFirstDigit {
		if len(group) >= 2 {
			collidingTargets = group
			break
		}
	}
	require.GreaterOrEqualf(t, len(collidingTargets), 2,
		"test setup: expected at least one same-first-hex-digit pair among %d targets", len(targets))
	prefix := fullRef[collidingTargets[0]][:len(cache.RefPrefix)+1]

	out := captureStderr(t, func() {
		printIdentifyRefSuggestion(ctx, m, prefix)
	})
	assert.Contains(t, out, "Nothing has produced it here, but this workspace would print it for any of:")
	for _, target := range collidingTargets {
		assert.Contains(t, out, "magus run "+target, "expected the colliding target %q in the suggestion", target)
	}
}

// TestPrintIdentifyRefSuggestion_SkipsOnIdentifyRefError covers the other uncovered
// branch: when m.IdentifyRef itself errors (types.ErrNoCache on an Inspect
// workspace, the one case IdentifyRef propagates rather than swallowing; see its
// doc), printIdentifyRefSuggestion must print nothing at all, not even the
// unconditional "share it with --publish" hint that follows every other path. A
// best-effort suggestion must never partially render around an error it hit.
func TestPrintIdentifyRefSuggestion_SkipsOnIdentifyRefError(t *testing.T) {
	spellName := register.Spell(t, "zzz-query-nocache-spell", spells.WithTargets("build"))

	w := testkit.NewWorkspace(t)
	w.Magusfile("")

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	ws, err := magus.Inspect(context.Background(), w.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Inspect")
	m, ok := ws.(*magus.Magus)
	require.True(t, ok, "magus.Inspect returns a *magus.Magus under types.WorkspaceRepository")

	_, identifyErr := m.IdentifyRef(context.Background(), "out123456789012")
	require.Error(t, identifyErr, "test setup: an Inspect (cache-free) workspace must fail to key")

	out := captureStderr(t, func() {
		printIdentifyRefSuggestion(context.Background(), m, "out123456789012")
	})
	assert.Empty(t, out, "an IdentifyRef error must skip the suggestion entirely, including the trailing --publish hint")
}

// readCacheBytes is the knowledge read cache the server keeps, as the tests that compare a
// cached answer with an uncached one size it.
const readCacheBytes = 512 << 20

// graphReadFixture is a workspace holding a magusfile, a doc and a Go source, the
// configuration a CLI run in it reads under, a locally opened handle on it, and an
// server answering reads through the handler `magus server` mounts.
type graphReadFixture struct {
	root  string
	cfg   config.Config
	local *magus.Magus
	host  *server.Host
	addr  string
}

func newGraphReadFixture(t *testing.T) graphReadFixture {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"magusfile.buzz": "",
		"docs/guide.md":  "# Guide\n\nHow searchVerdict decides.\n",
		"pkg/verdict.go": "package pkg\n\nfunc SearchVerdict() {}\n",
	})
	return openGraphReadFixture(t, w.Root())
}

// openGraphReadFixture is graphReadFixture over the workspace at root.
func openGraphReadFixture(tb testing.TB, root string) graphReadFixture {
	tb.Helper()
	tb.Cleanup(snapshotGlobals())
	// privateSockDir, for a benchmark too.
	sockDir, err := os.MkdirTemp("", "mgbrk")
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	tb.Setenv("XDG_RUNTIME_DIR", sockDir)
	tb.Setenv("MAGUS_PROC_SOCKET", "")

	// The configuration startup builds for a CLI run, tier for tier.
	cfg, err := config.LoadWithRoot("", root)
	require.NoError(tb, err)
	require.NoError(tb, configgen.ApplyEnv(&cfg, os.Getenv))
	globalCfg = cfg

	ctx := context.Background()
	local, err := magus.Open(ctx, root, workspace.WithLoadedConfig(cfg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = local.Close() })

	tb.Setenv("MAGUS_BROKER", "off")
	tb.Setenv(proc.SocketEnv, "")
	tb.Setenv(proc.TokenEnv, "")
	host := server.NewHost(server.HostDeps{
		Config:       cfg,
		Version:      version,
		Build:        selfBuild,
		StatusBase:   buildStatusBase,
		Load:         loadMagus,
		Place:        jobPlace,
		BrokerClient: newBrokerClient,
		ServeRead:    serveGraphRead,
		Registry:     server.RegistryDeps{Open: openWorkspace, LeaseOf: boundLease, LeasedBroker: leasedBrokerClient},
	})
	host.Start(tb.Context())
	tb.Cleanup(func() {
		host.Stop()
		<-host.Stopped()
		knowledge.SetReadCacheLimit(0)
	})
	addr := os.Getenv(proc.SocketEnv)
	require.NotEmpty(tb, addr, "the server did not bind its socket")
	return graphReadFixture{root: root, cfg: cfg, local: local, host: host, addr: addr}
}

// answers returns the local answer to read and the server's, each as the JSON the CLI's
// structured output is built from. The server's goes through the same decode the CLI
// runs on it, so a field that does not survive the trip shows up as a difference.
func (f graphReadFixture) answers(t *testing.T, verb string, read knowledge.GraphRead) (local, served string) {
	t.Helper()
	return f.localAnswer(t, verb, read), f.servedAnswer(t, verb, read)
}

func (f graphReadFixture) localAnswer(tb testing.TB, verb string, read knowledge.GraphRead) string {
	tb.Helper()
	ctx := context.Background()
	var res any
	var err error
	switch verb {
	case readQuery:
		res, err = graphReads.SearchGraph(ctx, f.local, f.cfg, read, false, false)
	case readExplain:
		res, err = graphReads.ExplainNode(ctx, f.local, f.cfg, read, false, false)
	case readRefs:
		var g *knowledge.Graph
		g, err = loadRefsGraph(ctx, f.local, f.cfg, false, read.Input)
		if err == nil {
			res = lookupRefs(ctx, f.local, f.cfg, g, read, graphReads.MeasuredCoverage(ctx, f.local, f.cfg))
		}
	}
	require.NoError(tb, err)
	b, err := codec.Marshal(res)
	require.NoError(tb, err)
	return string(b)
}

func (f graphReadFixture) servedAnswer(tb testing.TB, verb string, read knowledge.GraphRead) string {
	tb.Helper()
	read.Config = config.ReadDigest(f.cfg)
	read.Env = config.EnvDigests()
	var res any
	switch verb {
	case readQuery:
		res = &knowledge.QueryResult{}
	case readExplain:
		res = &knowledge.ExplainResult{}
	case readRefs:
		res = &refsResult{}
	}
	require.NoError(tb, proc.Read(context.Background(), f.addr, version, f.root, verb, read, res))
	b, err := codec.Marshal(res)
	require.NoError(tb, err)
	return string(b)
}

// mergedSearch is searchGraph as it answered a symbol-seeded read before ranking from the
// symbol names index: every symbol shard merged into the default graph, then queried.
func mergedSearch(tb testing.TB, ws types.WorkspaceRepository, cfg config.Config, read knowledge.GraphRead) string {
	tb.Helper()
	ctx := context.Background()
	g, err := graphReads.LoadGraph(ctx, ws, cfg, false, false, true)
	require.NoError(tb, err)
	b, err := codec.Marshal(workspacegraph.SearchLoaded(ctx, g, read, embedtable.Cached, graphReads.MeasuredCoverage(ctx, ws, cfg)))
	require.NoError(tb, err)
	return string(b)
}

// memoCoverage measures ws once per kind of read and answers every later read of that
// kind from it, for many reads of a tree and indexes that do not change between them.
// measureSymbolCoverage reads its input only through knowledge.CouldMatchLazyLayer.
func memoCoverage(ctx context.Context, ws workspacegraph.ReadWorkspace, cfg config.Config) workspacegraph.CoverageFunc {
	type kind struct{ couldMatch, seeded bool }
	var mu sync.Mutex
	memo := map[kind]knowledge.Coverage{}
	return func(input string, seeded bool) knowledge.Coverage {
		k := kind{knowledge.CouldMatchLazyLayer(input), seeded}
		mu.Lock()
		defer mu.Unlock()
		cov, ok := memo[k]
		if !ok {
			cov = measureSymbolCoverage(ctx, ws, cfg, input, seeded)
			memo[k] = cov
		}
		return cov
	}
}

// loadedGraphs is the default graph and the default graph with every symbol shard merged,
// each loaded once, with the coverage measured once: what the retrieval eval answers
// every judgment from.
type loadedGraphs struct {
	domain, symbols *knowledge.Graph
	coverage        workspacegraph.CoverageFunc
}

func (f graphReadFixture) loadGraphs(tb testing.TB) loadedGraphs {
	tb.Helper()
	ctx := context.Background()
	domain, err := graphReads.LoadGraph(ctx, f.local, f.cfg, false, false, false)
	require.NoError(tb, err)
	symbols, err := graphReads.LoadGraph(ctx, f.local, f.cfg, false, false, true)
	require.NoError(tb, err)
	return loadedGraphs{domain: domain, symbols: symbols, coverage: memoCoverage(ctx, f.local, f.cfg)}
}

// loadedAnswer is localAnswer from graphs loaded once rather than per read.
func (f graphReadFixture) loadedAnswer(tb testing.TB, gs loadedGraphs, verb string, read knowledge.GraphRead) any {
	tb.Helper()
	ctx := context.Background()
	g := gs.domain
	if knowledge.SeedsLazyLayer(read.Input) {
		g = gs.symbols
	}
	switch verb {
	case readQuery:
		return workspacegraph.SearchLoaded(ctx, g, read, embedtable.Cached, gs.coverage)
	case readExplain:
		return graphReads.ExplainLoaded(ctx, g, read, gs.coverage)
	case readRefs:
		return lookupRefs(ctx, f.local, f.cfg, gs.symbols, read, gs.coverage)
	}
	tb.Fatalf("no read %q", verb)
	return nil
}

// The retrieval eval answers every judgment from one pair of loaded graphs; each answer
// must be the one a read that loads its own graph gives.
func TestLoadedGraphReadsAnswerAsTheLocalReadDoes(t *testing.T) {
	f := newGraphReadFixture(t)
	gs := f.loadGraphs(t)
	for _, tc := range graphReadCases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := codec.Marshal(f.loadedAnswer(t, gs, tc.verb, tc.read))
			require.NoError(t, err)
			assert.JSONEq(t, f.localAnswer(t, tc.verb, tc.read), string(b))
		})
	}
}

// The text renderers print exactly what the CLI verbs do, so the bytes the retrieval eval
// counts from one rendered answer are the bytes a run of the verb prints.
func TestTextRenderersPrintWhatTheVerbPrints(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	f := newGraphReadFixture(t)
	gs := f.loadGraphs(t)
	globalCfg.Server.Enabled = false
	// The gate prints a next step's reason once per session; silent drops it from both.
	global.silent = true
	ctx := context.Background()
	nx := newNextGate(t.Context(), f.root)
	for _, tc := range []struct {
		verb  string
		input string
		cmd   func(context.Context, string, []string) error
	}{
		{readQuery, "guide", queryCommand},
		{readQuery, "SearchVerdict kind=symbol", queryCommand},
		{readQuery, "zzzunmatched", queryCommand},
		{readExplain, "docs", explainCommand},
	} {
		t.Run(fmt.Sprintf("%s %s", tc.verb, tc.input), func(t *testing.T) {
			var cmdErr error
			want := captureStdout(t, func() { cmdErr = tc.cmd(ctx, f.root, []string{tc.input}) })

			read := knowledge.GraphRead{Input: tc.input, Nearest: tc.verb == readQuery}
			var got strings.Builder
			var err error
			switch res := f.loadedAnswer(t, gs, tc.verb, read).(type) {
			case knowledge.QueryResult:
				err = writeQueryText(ctx, &got, nx, nx.served(hint.NextForQuery(res.Out)), res)
			case knowledge.ExplainResult:
				require.True(t, res.Found)
				err = writeExplainText(t.Context(), &got, f.root, tc.input, nx, nx.served(hint.NextForExplain(res.Out)), res)
			}
			assert.Equal(t, want, got.String())
			assert.Equal(t, cmdErr, err, "the same exit status")
		})
	}
}

// A symbol-seeded query ranks from the symbol names index and decodes only the shards its answer
// touches, and answers byte for byte what merging every shard did.
func TestQuerySymbolSeededAnswersAsTheFullMergeDid(t *testing.T) {
	f := newGraphReadFixture(t)
	for _, input := range []string{"SearchVerdict kind=symbol", "kind=symbol", "pkg/verdict.go", "zzzunmatched kind=symbol"} {
		read := knowledge.GraphRead{Input: input, Budget: knowledge.DefaultBudget, Nearest: true}
		assert.Equalf(t, mergedSearch(t, f.local, f.cfg, read), f.localAnswer(t, readQuery, read), "query %q", input)
	}
}

// The server keeps what it decodes; the answers it gives from that are the ones a read with
// nothing kept gives, on the first read and on every one after.
func TestServerGraphReadsAnswerFromTheReadCacheAsWithout(t *testing.T) {
	f := newGraphReadFixture(t)
	want := make([]string, len(graphReadCases))
	for i, tc := range graphReadCases {
		want[i] = f.localAnswer(t, tc.verb, tc.read)
	}
	knowledge.SetReadCacheLimit(readCacheBytes)
	t.Cleanup(func() { knowledge.SetReadCacheLimit(0) })
	for range 2 {
		for i, tc := range graphReadCases {
			assert.JSONEqf(t, want[i], f.servedAnswer(t, tc.verb, tc.read), "%s", tc.name)
		}
	}
}

var graphReadCases = []struct {
	name string
	verb string
	read knowledge.GraphRead
}{
	{"query a domain term", readQuery, knowledge.GraphRead{Input: "guide", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"query a symbol", readQuery, knowledge.GraphRead{Input: "SearchVerdict kind=symbol", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"query nothing", readQuery, knowledge.GraphRead{Input: "zzzunmatched", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"explain a dir", readExplain, knowledge.GraphRead{Input: "docs"}},
	{"explain nothing", readExplain, knowledge.GraphRead{Input: "zzzunmatched"}},
	{"refs with occurrences", readRefs, knowledge.GraphRead{Input: "SearchVerdict", Occurrences: true}},
}

// Accuracy is the edge over a text search, so a server answer is worth having only if
// it is the answer: verdicts, coverage, the nearest suggestion and every fact identical
// to what the same build reads locally.
func TestServerGraphReadsAnswerAsTheLocalReadDoes(t *testing.T) {
	f := newGraphReadFixture(t)
	for _, tc := range graphReadCases {
		t.Run(tc.name, func(t *testing.T) {
			local, served := f.answers(t, tc.verb, tc.read)
			assert.JSONEq(t, local, served)
		})
	}
}

// The server's warm graph trusts a file watcher that ignores .go edits, though the tree
// walk turns every source file into a node. A read must never answer from it: the server
// runs the stamp-checked build, so an edit the watcher let through is still in the answer.
func TestServerGraphReadsSeeAnEditTheWatcherIgnores(t *testing.T) {
	f := newGraphReadFixture(t)
	ctx := context.Background()
	held, release, err := f.host.Hold(f.root)
	require.NoError(t, err)
	stopWatch, err := held.WatchKnowledgeGraph(ctx)
	require.NoError(t, err)
	t.Cleanup(stopWatch)
	_, err = viewInternals(held).KnowledgeGraph(ctx, types.GraphRequest{})
	require.NoError(t, err, "warm the watched graph before the edit")
	release()

	read := knowledge.GraphRead{Input: "kind=dir", Budget: knowledge.DefaultBudget, Nearest: true}
	_, before := f.answers(t, readQuery, read)
	require.NotContains(t, before, `"dir:lib"`)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "lib", "added.go"), []byte("package lib\n\nfunc Added() {}\n"), 0o644))

	local, served := f.answers(t, readQuery, read)
	assert.JSONEq(t, local, served)
	assert.Contains(t, served, `"dir:lib"`, "a directory that exists only since the edit is in the server's answer")
}

func TestServerGraphReadsDeclineAnotherConfiguration(t *testing.T) {
	f := newGraphReadFixture(t)
	read := knowledge.GraphRead{Input: "guide", Config: "a configuration the server does not read under"}
	err := proc.Read(context.Background(), f.addr, version, f.root, readQuery, read, &knowledge.QueryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "a declined read is answered locally, quietly: %v", err)
}

// An index freshness verdict reads the process environment, so a server started from
// another shell can be right about its PATH and wrong about the client's.
func TestServerGraphReadsDeclineAnotherEnvironment(t *testing.T) {
	f := newGraphReadFixture(t)
	env := config.EnvDigests()
	env["PATH"] = config.EnvDigest("/somewhere/else/bin")
	read := knowledge.GraphRead{Input: "guide", Config: config.ReadDigest(f.cfg), Env: env}
	err := proc.Read(context.Background(), f.addr, version, f.root, readQuery, read, &knowledge.QueryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "%v", err)
	assert.Contains(t, err.Error(), "PATH")
}

func TestServerGraphReadsDeclineAnotherBuild(t *testing.T) {
	f := newGraphReadFixture(t)
	read := knowledge.GraphRead{Input: "guide", Config: config.ReadDigest(f.cfg)}
	err := proc.Read(context.Background(), f.addr, "v0.0.0-another-build", f.root, readQuery, read, &knowledge.QueryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "another build's answer is another program's answer: %v", err)
}

func TestAskServerFallsBackWhenTheServerDeclines(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	f := newGraphReadFixture(t)
	t.Setenv(proc.SocketEnv, f.addr)
	globalCfg.Server.Enabled = true

	var res knowledge.QueryResult
	require.True(t, askServer(context.Background(), f.root, readQuery, &knowledge.GraphRead{Input: "guide", Budget: knowledge.DefaultBudget}, &res))
	assert.Positive(t, res.Out.MatchCount)

	globalCfg.Knowledge.Notes.Shared = "elsewhere.md"
	assert.False(t, askServer(context.Background(), f.root, readQuery, &knowledge.GraphRead{Input: "guide"}, &knowledge.QueryResult{}),
		"a client reading under other settings gets no server answer")

	globalCfg.Server.Enabled = false
	assert.False(t, askServer(context.Background(), f.root, readQuery, &knowledge.GraphRead{Input: "guide"}, &knowledge.QueryResult{}),
		"--server-enabled=false never asks")
}

// BenchmarkServerGraphReads times warm reads of this repository answered, one at a time, by
// an in-process server, keeping its reads as `magus server` does and not, and reports each
// verb's median and the heap left in use after it. It first checks the symbol queries it
// times answer byte for byte as the full merge did, on this repository's own index. The
// timings include the index freshness check, so build the graph first: a stale index
// pays a tool-version probe on every read.
func BenchmarkServerGraphReads(b *testing.B) {
	root, err := workspace.FindRoot(".")
	require.NoError(b, err)
	f := openGraphReadFixture(b, root)
	for _, name := range []string{"searchVerdict", "SetReadCacheLimit", "QueryKnowledgeGraph", "ensureAdj"} {
		// -o json, which asks for no nearest suggestion.
		read := knowledge.GraphRead{Input: name + " kind=symbol", Budget: knowledge.DefaultBudget}
		require.Equal(b, mergedSearch(b, f.local, f.cfg, read), f.localAnswer(b, readQuery, read), "query %s --kind symbol -o json", name)
	}
	reads := []struct {
		name string
		verb string
		read knowledge.GraphRead
	}{
		{"query guard", readQuery, knowledge.GraphRead{Input: "guard", Budget: knowledge.DefaultBudget, Nearest: true}},
		{"query searchVerdict --kind symbol", readQuery, knowledge.GraphRead{Input: "searchVerdict kind=symbol", Budget: knowledge.DefaultBudget, Nearest: true}},
		{"refs searchVerdict --occurrences", readRefs, knowledge.GraphRead{Input: "searchVerdict", Occurrences: true}},
		{"explain internal/guard", readExplain, knowledge.GraphRead{Input: "internal/guard"}},
	}
	for _, mode := range []struct {
		name  string
		limit int64
	}{{"kept", readCacheBytes}, {"not kept", 0}} {
		b.Run(mode.name, func(b *testing.B) {
			knowledge.SetReadCacheLimit(mode.limit)
			b.Cleanup(func() { knowledge.SetReadCacheLimit(0) })
			for _, tc := range reads {
				b.Run(tc.name, func(b *testing.B) {
					f.servedAnswer(b, tc.verb, tc.read)
					var took []time.Duration
					for b.Loop() {
						start := time.Now()
						f.servedAnswer(b, tc.verb, tc.read)
						took = append(took, time.Since(start))
					}
					slices.Sort(took)
					b.ReportMetric(float64(took[len(took)/2].Microseconds())/1000, "p50-ms")
					runtime.GC()
					var mem runtime.MemStats
					runtime.ReadMemStats(&mem)
					b.ReportMetric(float64(mem.HeapInuse>>20), "heap-MiB")
				})
			}
		})
	}
}

// The CLI half of the path-normalization mirror; the MCP half is
// TestPathNormalizationIsSharedWithCLI in internal/handler/mcp. `magus query` hands the
// raw terms to the graph and canonicalises nothing of its own, so the CLI and MCP
// cannot resolve one pasted path to different nodes.
func TestPathNormalizationIsSharedWithMCP(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("console/magusfile.buzz", "x")
	root := w.Root()

	g := knowledge.NewGraph()
	g.SetRoot(root)
	g.AddNode(types.KnowledgeNode{ID: "file:console/magusfile.buzz", Kind: types.KindFile, Label: "magusfile.buzz"})

	for _, q := range []string{
		"kind:file ./console/magusfile.buzz",
		"kind:file /console/magusfile.buzz",
		"kind:file " + filepath.Join(root, "console", "magusfile.buzz"),
		`kind:file console\magusfile.buzz`,
	} {
		out := g.Query(q, knowledge.DefaultBudget)
		require.Lenf(t, out.Matches, 1, "%q should resolve", q)
		assert.Equal(t, "file:console/magusfile.buzz", out.Matches[0].ID)
	}
}

// A failed target's result line has to name the project, the target and the ref that reads
// its log: the reader of a failed gate has only this listing, and a result with no ref sent
// them to a second command to find it.
func TestQueryInvocationTextResultLinesCarryProjectTargetAndRef(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m := newQueryTestWorkspace(t)
	ctx := withMagus(context.Background(), m)

	runCtx, finish := m.BeginInvocation(ctx, magus.Command{Arguments: []string{"run", "lint"}, Trigger: journal.TriggerRun}, "v0.test")
	id := journal.InvocationIDFromContext(runCtx)
	journal.Emit(runCtx, journal.Event{Kind: journal.KindResult, Status: journal.StatusFail, Project: "api", Target: "lint", Ref: "out9bddb214656e", Text: "exit status 1\nsecond line"})
	journal.Emit(runCtx, journal.Event{Kind: journal.KindResult, Status: journal.StatusPass, Project: "web", Target: "build", Ref: "out0123456789ab"})
	finish(errors.New("lint failed"))

	out := captureStdout(t, func() {
		require.NoError(t, queryInvocation(ctx, "", id, false, output.Options{Format: outputText}))
	})

	assert.Contains(t, out, "result    fail api lint out9bddb214656e exit status 1\n", "the first line of the text follows the ref")
	assert.Contains(t, out, "result    pass web build out0123456789ab\n")
	assert.NotContains(t, out, "second line")
}

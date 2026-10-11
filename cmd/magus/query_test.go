package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what it wrote.
// showOutputIdentity prints straight to os.Stdout (matching the rest of `magus query`'s
// text-format rendering), so a real fd swap is the only way to observe it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = prev
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	return buf.String()
}

// captureStderr redirects os.Stderr for the duration of fn and returns what it wrote,
// with the default logger's records rendered as plain lines into the same stream, so a
// notice reads as it does on a terminal.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev, prevLog := os.Stderr, slog.Default()
	os.Stderr = w
	slog.SetDefault(slog.New(cache.NewPlainHandler(w, slog.LevelInfo)))
	fn()
	slog.SetDefault(prevLog)
	os.Stderr = prev
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	return buf.String()
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
			out := captureStderr(t, func() { err = queryCmd(context.Background(), t.TempDir(), []string{arg}) })
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
	const spellName = "zzz-query-ref-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg))
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

	_, _, lookupErr := m.OutputByRef(ref)
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
		require.NoError(t, queryCmd(ctx, "", []string{"output", ref}))
	})
	assert.Equal(t, "the full verdict\n", out)

	out = captureStdout(t, func() {
		require.NoError(t, queryTrailPayload(ctx, "", ref, OutputOptions{Format: FormatJSON}))
	})
	assert.JSONEq(t, `{"ref":"`+ref+`","output":"the full verdict"}`, out)

	assert.Error(t, queryCmd(ctx, "", []string{"--attempts", "output", ref}), "run flags are refused on a payload")

	missing := "grd" + strings.Repeat("0", 16)
	stderr := captureStderr(t, func() {
		var silent errSilent
		require.ErrorAs(t, queryTrailPayload(ctx, "", missing, OutputOptions{Format: FormatText}), &silent)
	})
	assert.Contains(t, stderr, `no stored payload for ref "`+missing+`"`)
}

// TestShowOutputIdentity_RevisionRendering drives showOutputIdentity end to end in a real git
// workspace: a target's descriptor is stamped with the revision HEAD was at when it
// ran (CurrentRevision, resolved once by executeStages), and --identity must render it:
// silently when it still matches HEAD, and with a "recorded at X, you are on Y" line
// once a later commit moves HEAD away from it.
func TestShowOutputIdentity_RevisionRendering(t *testing.T) {
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(""), 0o644))
	runGit(t, dir, "add", "magusfile.buzz")
	runGit(t, dir, "commit", "-m", "seed")
	firstRev := strings.TrimSpace(string(mustOutput(t, exec.Command("git", "-C", dir, "rev-parse", "HEAD"))))

	const spellName = "zzz-query-meta-revision-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) { return nil, nil }))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), dir, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	out := captureStdout(t, func() {
		require.NoError(t, showOutputIdentity(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out, "rev:     "+firstRev[:12], "the descriptor records the revision HEAD was at when the target ran")
	assert.NotContains(t, out, "recorded at", "HEAD has not moved yet, so nothing to call out")

	// Move HEAD to a second commit without re-running the target: the stored
	// descriptor still names firstRev, but the workspace is no longer there.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644))
	runGit(t, dir, "add", "other.txt")
	runGit(t, dir, "commit", "-m", "second")
	secondRev := strings.TrimSpace(string(mustOutput(t, exec.Command("git", "-C", dir, "rev-parse", "HEAD"))))
	require.NotEqual(t, firstRev, secondRev)

	out = captureStdout(t, func() {
		require.NoError(t, showOutputIdentity(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out,
		"recorded at "+firstRev[:12]+", you are on "+secondRev[:12]+".",
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
	const spellName = "zzz-query-multi-spell"
	targets := make([]string, 32)
	for i := range targets {
		targets[i] = fmt.Sprintf("build%02d", i)
	}
	s := spells.NewSpell(spellName, spells.WithTargets(targets...))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg))
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
	const spellName = "zzz-query-nocache-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	ws, err := magus.Inspect(context.Background(), root, magus.WithWorkspaceRegistry(reg))
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

// graphReadFixture is a workspace holding a magusfile, a doc and a Go source, the
// configuration a CLI run in it reads under, a locally opened handle on it, and an
// in-process server answering reads through the graphReads handler `magus server` mounts.
type graphReadFixture struct {
	root  string
	cfg   config.Config
	local *magus.Magus
	reg   *wsRegistry
	addr  string
}

func newGraphReadFixture(t *testing.T) graphReadFixture {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"magusfile.buzz": "",
		"docs/guide.md":  "# Guide\n\nHow searchVerdict decides.\n",
		"pkg/verdict.go": "package pkg\n\nfunc SearchVerdict() {}\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	return openGraphReadFixture(t, root)
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
	local, err := magus.Open(ctx, root, magus.WithLoadedConfig(cfg))
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = local.Close() })

	reg := newWSRegistry(ctx, cache.NewLimiter(2), nil, time.Hour, nil)
	tb.Cleanup(reg.close)
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
		Read:    graphReads(reg),
		Version: version,
	})
	require.NoError(tb, err)
	tb.Cleanup(srv.Close)
	require.NoError(tb, srv.Start())
	return graphReadFixture{root: root, cfg: cfg, local: local, reg: reg, addr: srv.Addr()}
}

// answers returns the local answer to read and the server's, each as the JSON the CLI's
// structured output is built from. The server's goes through the same decode the CLI
// runs on it, so a field that does not survive the trip shows up as a difference.
func (f graphReadFixture) answers(t *testing.T, verb string, read graphRead) (local, served string) {
	t.Helper()
	return f.localAnswer(t, verb, read), f.servedAnswer(t, verb, read)
}

func (f graphReadFixture) localAnswer(tb testing.TB, verb string, read graphRead) string {
	tb.Helper()
	ctx := context.Background()
	var res any
	var err error
	switch verb {
	case readQuery:
		res, err = searchGraph(ctx, f.local, f.cfg, read, false, false)
	case readExplain:
		res, err = explainNode(ctx, f.local, f.cfg, read, false, false)
	case readRefs:
		var g *knowledge.Graph
		g, err = loadRefsGraph(ctx, f.local, f.cfg, false, read.Input)
		if err == nil {
			res = lookupRefs(ctx, f.local, f.cfg, g, read)
		}
	}
	require.NoError(tb, err)
	b, err := json.Marshal(res)
	require.NoError(tb, err)
	return string(b)
}

func (f graphReadFixture) servedAnswer(tb testing.TB, verb string, read graphRead) string {
	tb.Helper()
	read.Config = readConfigDigest(f.cfg)
	read.Env = envDigests()
	var res any
	switch verb {
	case readQuery:
		res = &queryResult{}
	case readExplain:
		res = &explainResult{}
	case readRefs:
		res = &refsResult{}
	}
	require.NoError(tb, proc.Read(context.Background(), f.addr, version, f.root, verb, read, res))
	b, err := json.Marshal(res)
	require.NoError(tb, err)
	return string(b)
}

// mergedSearch is searchGraph as it answered a symbol-seeded read before ranking from the
// symbol names index: every symbol shard merged into the default graph, then queried.
func mergedSearch(tb testing.TB, ws types.WorkspaceRepository, cfg config.Config, read graphRead) string {
	tb.Helper()
	ctx := context.Background()
	g, err := loadWorkspaceGraph(ctx, ws, cfg, false, false, true)
	require.NoError(tb, err)
	out := g.Query(read.Input, read.Budget)
	out.Answer = knowledge.Answer(read.Input, out.MatchCount > 0, measureSymbolCoverage(ctx, ws, cfg, read.Input, true))
	res := queryResult{Out: out}
	if out.MatchCount == 0 && read.Nearest {
		res.Nearest = g.NearestNode(read.Input)
	}
	b, err := json.Marshal(res)
	require.NoError(tb, err)
	return string(b)
}

// A symbol-seeded query ranks from the symbol names index and decodes only the shards its answer
// touches, and answers byte for byte what merging every shard did.
func TestQuerySymbolSeededAnswersAsTheFullMergeDid(t *testing.T) {
	f := newGraphReadFixture(t)
	for _, input := range []string{"SearchVerdict kind=symbol", "kind=symbol", "pkg/verdict.go", "zzzunmatched kind=symbol"} {
		read := graphRead{Input: input, Budget: knowledge.DefaultBudget, Nearest: true}
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
	knowledge.SetReadCacheLimit(serverReadCacheBytes)
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
	read graphRead
}{
	{"query a domain term", readQuery, graphRead{Input: "guide", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"query a symbol", readQuery, graphRead{Input: "SearchVerdict kind=symbol", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"query nothing", readQuery, graphRead{Input: "zzzunmatched", Budget: knowledge.DefaultBudget, Nearest: true}},
	{"explain a dir", readExplain, graphRead{Input: "docs"}},
	{"explain nothing", readExplain, graphRead{Input: "zzzunmatched"}},
	{"refs with occurrences", readRefs, graphRead{Input: "SearchVerdict", Occurrences: true}},
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
	e, err := f.reg.acquire(f.root)
	require.NoError(t, err)
	stopWatch, err := e.m.WatchKnowledgeGraph(ctx)
	require.NoError(t, err)
	t.Cleanup(stopWatch)
	_, err = e.m.KnowledgeGraph(ctx, false)
	require.NoError(t, err, "warm the watched graph before the edit")
	f.reg.release(e)

	read := graphRead{Input: "kind=dir", Budget: knowledge.DefaultBudget, Nearest: true}
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
	read := graphRead{Input: "guide", Config: "a configuration the server does not read under"}
	err := proc.Read(context.Background(), f.addr, version, f.root, readQuery, read, &queryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "a declined read is answered locally, quietly: %v", err)
}

// An index freshness verdict reads the process environment, so a server started from
// another shell can be right about its PATH and wrong about the client's.
func TestServerGraphReadsDeclineAnotherEnvironment(t *testing.T) {
	f := newGraphReadFixture(t)
	env := envDigests()
	env["PATH"] = envDigest("/somewhere/else/bin")
	read := graphRead{Input: "guide", Config: readConfigDigest(f.cfg), Env: env}
	err := proc.Read(context.Background(), f.addr, version, f.root, readQuery, read, &queryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "%v", err)
	assert.Contains(t, err.Error(), "PATH")
}

func TestServerGraphReadsDeclineAnotherBuild(t *testing.T) {
	f := newGraphReadFixture(t)
	read := graphRead{Input: "guide", Config: readConfigDigest(f.cfg)}
	err := proc.Read(context.Background(), f.addr, "v0.0.0-another-build", f.root, readQuery, read, &queryResult{})
	require.Error(t, err)
	assert.True(t, proc.NotAdopted(err), "another build's answer is another program's answer: %v", err)
}

func TestAskServerFallsBackWhenTheServerDeclines(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	f := newGraphReadFixture(t)
	t.Setenv(proc.SocketEnv, f.addr)
	globalCfg.Server.Enabled = true

	var res queryResult
	require.True(t, askServer(context.Background(), f.root, readQuery, &graphRead{Input: "guide", Budget: knowledge.DefaultBudget}, &res))
	assert.Positive(t, res.Out.MatchCount)

	globalCfg.Knowledge.Notes.Shared = "elsewhere.md"
	assert.False(t, askServer(context.Background(), f.root, readQuery, &graphRead{Input: "guide"}, &queryResult{}),
		"a client reading under other settings gets no server answer")

	globalCfg.Server.Enabled = false
	assert.False(t, askServer(context.Background(), f.root, readQuery, &graphRead{Input: "guide"}, &queryResult{}),
		"--server-enabled=false never asks")
}

// BenchmarkServerGraphReads times warm reads of this repository answered, one at a time, by
// an in-process server, keeping its reads as `magus server` does and not, and reports each
// verb's median and the heap left in use after it. It first checks the symbol queries it
// times answer byte for byte as the full merge did, on this repository's own index. The
// timings include the index freshness check, so build the graph first: a stale index
// pays a tool-version probe on every read.
func BenchmarkServerGraphReads(b *testing.B) {
	root, err := magus.FindRoot(".")
	require.NoError(b, err)
	f := openGraphReadFixture(b, root)
	for _, name := range []string{"searchVerdict", "SetReadCacheLimit", "QueryKnowledgeGraph", "ensureAdj"} {
		// -o json, which asks for no nearest suggestion.
		read := graphRead{Input: name + " kind=symbol", Budget: knowledge.DefaultBudget}
		require.Equal(b, mergedSearch(b, f.local, f.cfg, read), f.localAnswer(b, readQuery, read), "query %s --kind symbol -o json", name)
	}
	reads := []struct {
		name string
		verb string
		read graphRead
	}{
		{"query guard", readQuery, graphRead{Input: "guard", Budget: knowledge.DefaultBudget, Nearest: true}},
		{"query searchVerdict --kind symbol", readQuery, graphRead{Input: "searchVerdict kind=symbol", Budget: knowledge.DefaultBudget, Nearest: true}},
		{"refs searchVerdict --occurrences", readRefs, graphRead{Input: "searchVerdict", Occurrences: true}},
		{"explain internal/guard", readExplain, graphRead{Input: "internal/guard"}},
	}
	for _, mode := range []struct {
		name  string
		limit int64
	}{{"kept", serverReadCacheBytes}, {"not kept", 0}} {
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

// mustOutput runs cmd and fails the test on error, surfacing combined output.
func mustOutput(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%v: %s", cmd.Args, out)
	return out
}

// The CLI half of the path-normalization mirror; the MCP half is
// TestPathNormalizationIsSharedWithCLI in internal/handler/mcp. `magus query` hands the
// raw terms to the graph and canonicalises nothing of its own, so the CLI and MCP
// cannot resolve one pasted path to different nodes.
func TestPathNormalizationIsSharedWithMCP(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "console"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "console", "magusfile.buzz"), []byte("x"), 0o644))

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

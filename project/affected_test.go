package project

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/depgraph"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
)

// newAffectedWorkspace lays down a workspace with three projects in
// three languages, useful for exercising affected detection.
func newAffectedWorkspace(t *testing.T) *types.Workspace {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"api/magusfile.tl",
		"web/studio/magusfile.tl",
		"extensions/drape/magusfile.tl",
	} {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(""), 0o644))
	}
	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	return ws
}

// TestAffectedFromPathsHappyPath verifies that AffectedFromPaths correctly
// maps file paths to seed projects and computes changed/seed sets.
func TestAffectedFromPathsHappyPath(t *testing.T) {
	ws := newAffectedWorkspace(t)

	r, err := AffectedFromPaths(context.Background(), ws, []string{
		"api/magusfile.tl",
		"web/studio/magusfile.tl",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "web/studio"}, r.Seed)
	assert.NotNil(t, r.FilesBySeed["api"], "FilesBySeed missing api: %v", r.FilesBySeed)
	assert.NotNil(t, r.FilesBySeed["web/studio"], "FilesBySeed missing web/studio: %v", r.FilesBySeed)
}

// TestAffectedDisabledReturnsErrFallback verifies that MAGUS_VCS_ENABLED=false
// causes Affected to return ErrAffectedFallback.
func TestAffectedDisabledReturnsErrFallback(t *testing.T) {
	ws := newAffectedWorkspace(t)
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	_, err := Affected(context.Background(), ws, "")
	assert.ErrorIs(t, err, types.ErrAffectedFallback)
}

// TestAffectedFromPathsOutsideWorkspace verifies that absolute paths outside
// the workspace root are silently skipped.
func TestAffectedFromPathsOutsideWorkspace(t *testing.T) {
	ws := newAffectedWorkspace(t)

	r, err := AffectedFromPaths(context.Background(), ws, []string{
		"api/magusfile.tl",
		"/tmp/outside-workspace/file.go",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, r.Seed)
}

// TestWorkspaceRelative covers the pure prefix-stripping: diff paths come back
// relative to the VCS root, but project attribution needs them relative to the
// workspace root.
func TestWorkspaceRelative(t *testing.T) {
	t.Parallel()

	t.Run("nested workspace strips prefix and drops outsiders", func(t *testing.T) {
		t.Parallel()
		got := workspaceRelative("magus/", []string{
			"magus/gopherbuzz/parser.go",
			"magus/internal/doctor/doctor.go",
			".github/workflows/ci.yaml", // outside the workspace subtree
			"other/x.go",                // outside the workspace subtree
		})
		assert.Equal(t, []string{"gopherbuzz/parser.go", "internal/doctor/doctor.go"}, got)
	})

	t.Run("empty prefix: workspace is the VCS root, unchanged", func(t *testing.T) {
		t.Parallel()
		in := []string{"gopherbuzz/parser.go", "internal/x.go"}
		assert.Equal(t, in, workspaceRelative("", in))
	})
}

// TestVCSRootPrefix verifies the walk-up marker search returns the slash-terminated
// subdir prefix (no subprocess), and "" when the workspace is the VCS root or no
// marker is found.
func TestVCSRootPrefix(t *testing.T) {
	t.Parallel()

	t.Run("workspace nested below the marker", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
		ws := filepath.Join(root, "magus")
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "gopherbuzz"), 0o755))
		assert.Equal(t, "magus/", vcsRootPrefix(ws, []string{".git"}))
		assert.Equal(t, "magus/gopherbuzz/", vcsRootPrefix(filepath.Join(ws, "gopherbuzz"), []string{".git"}))
	})

	t.Run("workspace is the marker dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
		assert.Equal(t, "", vcsRootPrefix(root, []string{".git"}))
	})

	t.Run("marker is a file (worktree/submodule)", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere"), 0o644))
		ws := filepath.Join(root, "sub")
		require.NoError(t, os.Mkdir(ws, 0o755))
		assert.Equal(t, "sub/", vcsRootPrefix(ws, []string{".git"}))
	})

	t.Run("no marker found: empty (best-effort)", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", vcsRootPrefix(t.TempDir(), []string{".magus-no-such-marker"}))
	})
}

// countObserver counts the graph events it receives.
type countObserver struct{ builds, queries, errs int }

func (c *countObserver) OnBuild(types.BuildStats) { c.builds++ }
func (c *countObserver) OnQuery(types.QueryEvent) { c.queries++ }
func (c *countObserver) OnError(error)            { c.errs++ }

// TestAffectedRequestScopedObserver verifies the C4 fix: a graph observer is
// scoped per request via context, not shared workspace state. Two independent
// calls each route to their own observer (exactly one build each), and a call
// with no observer touches neither — so concurrent daemon requests cannot
// clobber each other's observer.
func TestAffectedRequestScopedObserver(t *testing.T) {
	ws := newAffectedWorkspace(t)

	obsA := &countObserver{}
	obsB := &countObserver{}
	_, err := AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsA), ws, []string{"api/x.go"})
	require.NoError(t, err, "call A")
	_, err = AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsB), ws, []string{"api/x.go"})
	require.NoError(t, err, "call B")
	assert.Equal(t, 1, obsA.builds, "observer A builds")
	assert.Equal(t, 1, obsB.builds, "observer B builds")

	_, err = AffectedFromPaths(context.Background(), ws, []string{"api/x.go"})
	require.NoError(t, err, "call C")
	// An observerless call must not leak events into either observer.
	assert.Equal(t, 1, obsA.builds, "an observerless call leaked events into A")
	assert.Equal(t, 1, obsB.builds, "an observerless call leaked events into B")
}

// buildWorkspace constructs a minimal Workspace from a list of projects.
// Each entry is [path, lang, ...deps].
func buildWorkspace(t *testing.T, entries [][]string) *types.Workspace {
	t.Helper()
	ws := &types.Workspace{
		Root:     "/fake",
		Projects: map[string]*types.Project{},
	}
	for _, e := range entries {
		path := e[0]
		lang := e[1]
		deps := e[2:]
		ws.Projects[path] = &types.Project{
			Path:      path,
			Dir:       "/fake/" + path,
			Spell:     lang,
			DependsOn: deps,
		}
	}
	return ws
}

func mustGraph(t *testing.T, ws *types.Workspace) *types.Graph {
	t.Helper()
	g, err := depgraph.Build(ws)
	require.NoError(t, err, "depgraph.Build()")
	return g
}

func renderGraph(t *testing.T, g *types.Graph, opts ...render.RenderOption) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, render.WriteTree(&b, g, opts...), "render.WriteTree()")
	return b.String()
}

// TestRenderSingleProject verifies a lone project with no deps.
func TestRenderSingleProject(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go"},
	})
	g := mustGraph(t, ws)
	assert.Equal(t, "api\n", renderGraph(t, g))
}

// TestRenderLinearChain verifies a simple A → B → C chain.
func TestRenderLinearChain(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db"},
		{"internal/db", "go", "internal/util"},
		{"internal/util", "go"},
	})
	g := mustGraph(t, ws)
	assert.Equal(t, "api\n└── internal/db\n    └── internal/util\n", renderGraph(t, g))
}

// TestRenderDiamondVisited verifies that a shared dep is not re-expanded.
// Children are sorted: "left" is visited before "shared", so "shared" is
// first encountered under "left" and shown as "(visited)" under "root".
//
//	root
//	├── left
//	│   └── shared
//	└── shared (visited)
func TestRenderDiamondVisited(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"root", "go", "left", "shared"},
		{"left", "go", "shared"},
		{"shared", "go"},
	})
	g := mustGraph(t, ws)
	// Only "root" has no predecessors (nothing depends on it).
	// Children are sorted alphabetically: left, shared.
	// "shared" is first visited under "left"; on the second encounter
	// under "root" it shows as "(visited)".
	assert.Equal(t, "root\n├── left\n│   └── shared\n└── shared (visited)\n", renderGraph(t, g))
}

// TestRenderMultipleRoots verifies that multiple top-level roots are
// printed in lexical order.
func TestRenderMultipleRoots(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"app-a", "go", "shared"},
		{"app-b", "go", "shared"},
		{"shared", "go"},
	})
	g := mustGraph(t, ws)
	assert.Equal(t, "app-a\n└── shared\napp-b\n└── shared\n", renderGraph(t, g))
}

// TestRenderWithRoots verifies that WithRoots restricts output to
// specified starting points.
func TestRenderWithRoots(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"app-a", "go", "shared"},
		{"app-b", "go", "shared"},
		{"shared", "go"},
	})
	g := mustGraph(t, ws)
	assert.Equal(t, "app-a\n└── shared\n", renderGraph(t, g, render.WithRoots("app-a")))
}

// TestRenderMaxDepth verifies that WithMaxDepth(1) shows only immediate
// dependencies.
func TestRenderMaxDepth(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db"},
		{"internal/db", "go", "internal/util"},
		{"internal/util", "go"},
	})
	g := mustGraph(t, ws)
	assert.Equal(t, "api\n└── internal/db\n", renderGraph(t, g, render.WithMaxDepth(1)))
}

// TestRenderUpstream verifies that WithDirection(Upstream) shows
// dependents rather than dependencies.
//
//	internal/util is depended on by internal/db which is depended on by api.
//	Upstream view from internal/util: internal/util → internal/db → api.
func TestRenderUpstream(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db"},
		{"internal/db", "go", "internal/util"},
		{"internal/util", "go"},
	})
	g := mustGraph(t, ws)
	got := renderGraph(t, g, render.WithDirection(types.Upstream), render.WithRoots("internal/util"))
	assert.Equal(t, "internal/util\n└── internal/db\n    └── api\n", got)
}

// TestRenderLangFilter verifies that WithSpell skips projects of other
// languages and does not recurse into them.
func TestRenderLangFilter(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"app", "typescript", "api"},
		{"api", "go", "internal/db"},
		{"internal/db", "go"},
	})
	g := mustGraph(t, ws)
	// Only Go projects; "app" is TS so it and its subtree are skipped
	// from display. "api" and "internal/db" have no lang-filtered roots
	// (api's only predecessor is "app" which is TS, so api has no go
	// predecessors → it is a go root).
	assert.Equal(t, "api\n└── internal/db\n", renderGraph(t, g, render.WithSpell("go")))
}

// TestRenderEmpty verifies that an empty workspace produces no output.
func TestRenderEmpty(t *testing.T) {
	t.Parallel()
	ws := &types.Workspace{Root: "/fake", Projects: map[string]*types.Project{}}
	g := mustGraph(t, ws)
	assert.Empty(t, renderGraph(t, g), "empty workspace should produce no output")
}

func pathsFromSeeds(t *testing.T, g *types.Graph, seeds []string, target string) []types.AffectedPath {
	t.Helper()
	return g.PathsFromSeeds(seeds, target)
}

// TestPathsFromSeedsDirect: target is itself the seed → chain length 1.
func TestPathsFromSeedsDirect(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go"},
	})
	g := mustGraph(t, ws)
	got := pathsFromSeeds(t, g, []string{"api"}, "api")
	require.Len(t, got, 1)
	assert.Equal(t, "api", got[0].Seed)
	assert.Equal(t, []string{"api"}, got[0].Chain)
}

// TestPathsFromSeedsTransitive: A depends on B depends on C (seed).
// Target=A → chain [C, B, A].
func TestPathsFromSeedsTransitive(t *testing.T) {
	t.Parallel()
	// A → B → C (C is a dep of B, B is a dep of A)
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go", "c"},
		{"c", "go"},
	})
	g := mustGraph(t, ws)
	got := pathsFromSeeds(t, g, []string{"c"}, "a")
	require.Len(t, got, 1)
	assert.Equal(t, []string{"c", "b", "a"}, got[0].Chain)
}

// TestPathsFromSeedsUnreachable: target not reachable from seeds → empty.
func TestPathsFromSeedsUnreachable(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go"},
		{"web", "typescript"},
	})
	g := mustGraph(t, ws)
	got := pathsFromSeeds(t, g, []string{"web"}, "api")
	assert.Empty(t, got, "api and web are unrelated")
}

// TestPathsFromSeedsMultipleSeeds: two independent seeds both reach target.
func TestPathsFromSeedsMultipleSeeds(t *testing.T) {
	t.Parallel()
	// target depends on both seed-a and seed-b
	ws := buildWorkspace(t, [][]string{
		{"target", "go", "seed-a", "seed-b"},
		{"seed-a", "go"},
		{"seed-b", "go"},
	})
	g := mustGraph(t, ws)
	got := pathsFromSeeds(t, g, []string{"seed-a", "seed-b"}, "target")
	require.Len(t, got, 2)
	// Paths are sorted by seed name.
	assert.Equal(t, "seed-a", got[0].Seed)
	assert.Equal(t, "seed-b", got[1].Seed)
}

// TestPathsFromSeedsThroughIntermediateSeed verifies BFS continues past intermediate seeds.
func TestPathsFromSeedsThroughIntermediateSeed(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go", "c"},
		{"c", "go"},
	})
	g := mustGraph(t, ws)
	got := pathsFromSeeds(t, g, []string{"a", "c"}, "a")
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Seed)
	assert.Equal(t, []string{"a"}, got[0].Chain)
	assert.Equal(t, "c", got[1].Seed)
	assert.Equal(t, []string{"c", "b", "a"}, got[1].Chain)
}

// TestNearCyclesLinearChain: a→b→c→d has no near-cycles because each node
// only depends "forward" — no predecessor can reach a node through a short path.
// Actually a→b, b→c, c→d: predecessors of a = none; predecessors of b = {a};
// predecessors of c = {b,a}; predecessors of d = {c,b,a}.
// For nodeA=d: depth-1 pred={c} → NearCycle(d,c); depth-2 pred={b} → NearCycle(d,b).
// These all represent "adding d→c would close d→c→d" or "adding d→b would close d→b→c→d".
// So a linear chain DOES have near-cycles. This test verifies the count.
func TestNearCyclesLinearChain(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go", "c"},
		{"c", "go", "d"},
		{"d", "go"},
	})
	g := mustGraph(t, ws)
	ncs := g.NearCycles(context.Background(), 3)
	// a's predecessor BFS: nobody depends on a, so no near-cycles from a.
	// b's preds (depth 1): {a} → NearCycle(b,a). depth 2: none (a has no preds).
	// c's preds (depth 1): {b}; depth 2: {a}.
	// d's preds (depth 1): {c}; depth 2: {b}.
	// Total: NearCycles(b,a), (c,b), (c,a), (d,c), (d,b) = 5 pairs.
	assert.Len(t, ncs, 5)
}

// TestNearCyclesDisabled: depth=0 returns nil.
func TestNearCyclesDisabled(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go"},
	})
	g := mustGraph(t, ws)
	assert.Nil(t, g.NearCycles(context.Background(), 0), "depth=0 should return nil")
}

// TestNearCyclesIsolated: a project with no deps and no dependents has
// no near-cycles.
func TestNearCyclesIsolated(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"standalone", "go"},
	})
	g := mustGraph(t, ws)
	assert.Empty(t, g.NearCycles(context.Background(), 3), "isolated project should have no near-cycles")
}

// TestNearCyclesBackPathShape verifies the BackPath content and direction.
// a→b (a depends on b). b's predecessor is a (a depends on b = a is pred of b).
// NearCycle(b, a): From=b, To=a, BackPath=[a, b].
// "Adding b→a would close b→a→b" (a 2-cycle of length 2).
func TestNearCyclesBackPathShape(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go"},
	})
	g := mustGraph(t, ws)
	ncs := g.NearCycles(context.Background(), 3)
	require.Len(t, ncs, 1)
	nc := ncs[0]
	assert.Equal(t, "b", nc.From)
	assert.Equal(t, "a", nc.To)
	assert.Equal(t, []string{"a", "b"}, nc.BackPath)
}

// TestBlastRadiusLinearChain: a→b→c. Changing c affects only c (1).
// Changing b affects b and a (2). Changing a affects only a (1).
func TestBlastRadiusLinearChain(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go", "c"},
		{"c", "go"},
	})
	g := mustGraph(t, ws)
	br := g.BlastRadius()
	assert.Equal(t, 1, br["a"])
	assert.Equal(t, 2, br["b"])
	assert.Equal(t, 3, br["c"])
}

// TestBlastRadiusDiamond: root→left→shared, root→shared.
// shared has blast radius 3 (shared, left, root). left has 2. root has 1.
func TestBlastRadiusDiamond(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"root", "go", "left", "shared"},
		{"left", "go", "shared"},
		{"shared", "go"},
	})
	g := mustGraph(t, ws)
	br := g.BlastRadius()
	assert.Equal(t, 3, br["shared"])
	assert.Equal(t, 2, br["left"])
	assert.Equal(t, 1, br["root"])
}

// TestNCCDSingleNode: a single node has CCD=1, BBT=1, NCCD=1.
func TestNCCDSingleNode(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{{"solo", "go"}})
	g := mustGraph(t, ws)
	nccd := g.NCCD()
	// N=1: CCD=1, BBT=(2*log2(2))-1=1, NCCD=1.
	assert.InDelta(t, 1.0, nccd, 0.01, "NCCD should be ~1.0 for single node")
}

// TestNCCDStarTopology: one central node depended on by N-1 others.
// CCD = 1 (center) + 2*(N-1) (each leaf counts itself+center).
// For N=5: CCD=9. BBT=(6*log2(6))-5≈10.51. NCCD≈0.856 < 1.
// A star topology is well below the balanced-tree baseline.
func TestNCCDStarTopology(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"center", "go"},
		{"leaf1", "go", "center"},
		{"leaf2", "go", "center"},
		{"leaf3", "go", "center"},
		{"leaf4", "go", "center"},
	})
	g := mustGraph(t, ws)
	assert.Less(t, g.NCCD(), 1.0, "NCCD should be < 1.0 for star topology")
}

// TestNCCDEmpty: empty graph returns 0.
func TestNCCDEmpty(t *testing.T) {
	t.Parallel()
	ws := &types.Workspace{Root: "/fake", Projects: map[string]*types.Project{}}
	g := mustGraph(t, ws)
	assert.Zero(t, g.NCCD(), "NCCD should be 0 for empty graph")
}

// captureSlogWarnings installs a text-format slog handler that captures output
// into a buffer. Returns the buffer and a cleanup function that restores the
// previous default logger. These tests must NOT run in parallel because they
// mutate the global default logger.
func captureSlogWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestGraphWarnsOnUnresolvedDep verifies that a dep path not matching any
// registered project emits a slog.Warn with consumer, missing_dep,
// did_you_mean, and fix fields - and that Graph() still succeeds (skip behavior).
func TestGraphWarnsOnUnresolvedDep(t *testing.T) {
	// No t.Parallel() - mutates global slog default.
	buf := captureSlogWarnings(t)

	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db-typo"},
		{"internal/db", "go"},
	})
	g, err := depgraph.Build(ws)
	require.NoError(t, err)
	require.NotNil(t, g)

	got := buf.String()
	for _, want := range []string{
		"consumer=api",
		"missing_dep=internal/db-typo",
		"did_you_mean=internal/db",
		"magus.project.register",
	} {
		assert.Contains(t, got, want, "log output missing %q", want)
	}
}

// TestGraphNoWarnWhenDepsResolve verifies that a workspace where all deps
// resolve produces no warnings.
func TestGraphNoWarnWhenDepsResolve(t *testing.T) {
	// No t.Parallel() - mutates global slog default.
	buf := captureSlogWarnings(t)

	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db"},
		{"internal/db", "go"},
	})
	_, err := depgraph.Build(ws)
	require.NoError(t, err)
	assert.Empty(t, buf.String(), "unexpected warning output")
}

// TestGraphWarnsByDefaultOnUnregisteredDep verifies the default (non-
// strict) behaviour: missing deps are dropped, slog records a warning,
// Graph() still returns a usable graph.
func TestGraphWarnsByDefaultOnUnregisteredDep(t *testing.T) {
	// No t.Parallel() - mutates global slog default.
	buf := captureSlogWarnings(t)

	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db-typo"},
		{"internal/db", "go"},
	})
	// Strict defaults to false; do not set it.
	g, err := depgraph.Build(ws)
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Contains(t, buf.String(), "internal/db-typo", "warning missing dep")
}

// TestGraphStrictFailsOnUnregisteredDep verifies strict mode returns a
// typed error that errors.Is matches against ErrUnregisteredDep.
func TestGraphStrictFailsOnUnregisteredDep(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go", "internal/db-typo"},
		{"internal/db", "go"},
	})
	ws.Strict = true

	_, err := depgraph.Build(ws)
	require.Error(t, err, "expected error in strict mode")
	assert.ErrorIs(t, err, types.ErrUnregisteredDep)
	var ude *types.UnregisteredDepError
	require.ErrorAs(t, err, &ude)
	require.Len(t, ude.Missing, 1)
	assert.Equal(t, "api", ude.Missing[0].Consumer)
	assert.Equal(t, "internal/db-typo", ude.Missing[0].Dep)
}

// TestUnregisteredDepErrorAggregates verifies multiple missing deps
// are collected into a single error, not fail-fast on the first.
func TestUnregisteredDepErrorAggregates(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go", "missing-a", "missing-b"},
		{"svc", "go", "missing-c"},
	})
	ws.Strict = true

	_, err := depgraph.Build(ws)
	require.Error(t, err, "expected aggregated error")
	var ude *types.UnregisteredDepError
	require.ErrorAs(t, err, &ude)
	assert.Len(t, ude.Missing, 3)
}

// TestUnregisteredDepErrorMessage verifies the user-facing message
// includes every missing dep and the fix hint.
func TestUnregisteredDepErrorMessage(t *testing.T) {
	t.Parallel()
	e := &types.UnregisteredDepError{
		Missing: []types.UnregisteredDep{
			{Consumer: "api", Dep: "internal/db-typo", DidYouMean: "internal/db"},
			{Consumer: "svc-b", Dep: "shared/missing"},
		},
	}
	msg := e.Error()
	for _, want := range []string{
		"magus: dependency not registered (2 unresolved)",
		"api -> internal/db-typo",
		"(did you mean: internal/db)",
		"svc-b -> shared/missing",
		"fix: register the missing project(s)",
	} {
		assert.Contains(t, msg, want, "Error() missing %q", want)
	}
}

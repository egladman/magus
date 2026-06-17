package project

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatalf("AffectedFromPaths: %v", err)
	}
	wantSeed := []string{"api", "web/studio"}
	if !equalSlice(r.Seed, wantSeed) {
		t.Errorf("Seed = %v, want %v", r.Seed, wantSeed)
	}
	if r.FilesBySeed["api"] == nil || r.FilesBySeed["web/studio"] == nil {
		t.Errorf("FilesBySeed missing entries: %v", r.FilesBySeed)
	}
}

// TestAffectedDisabledReturnsErrFallback verifies that MAGUS_VCS_ENABLED=false
// causes Affected to return ErrAffectedFallback.
func TestAffectedDisabledReturnsErrFallback(t *testing.T) {
	ws := newAffectedWorkspace(t)
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	_, err := Affected(context.Background(), ws, "")
	if !errors.Is(err, types.ErrAffectedFallback) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAffectedFallback)", err)
	}
}

// TestAffectedFromPathsOutsideWorkspace verifies that absolute paths outside
// the workspace root are silently skipped.
func TestAffectedFromPathsOutsideWorkspace(t *testing.T) {
	ws := newAffectedWorkspace(t)

	r, err := AffectedFromPaths(context.Background(), ws, []string{
		"api/magusfile.tl",
		"/tmp/outside-workspace/file.go",
	})
	if err != nil {
		t.Fatalf("AffectedFromPaths: %v", err)
	}
	wantSeed := []string{"api"}
	if !equalSlice(r.Seed, wantSeed) {
		t.Errorf("Seed = %v, want %v", r.Seed, wantSeed)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if _, err := AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsA), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call A: %v", err)
	}
	if _, err := AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsB), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call B: %v", err)
	}
	if obsA.builds != 1 {
		t.Errorf("observer A builds = %d, want 1", obsA.builds)
	}
	if obsB.builds != 1 {
		t.Errorf("observer B builds = %d, want 1", obsB.builds)
	}

	if _, err := AffectedFromPaths(context.Background(), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call C: %v", err)
	}
	if obsA.builds != 1 || obsB.builds != 1 {
		t.Errorf("an observerless call leaked events: A=%d B=%d", obsA.builds, obsB.builds)
	}
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
	if err != nil {
		t.Fatalf("depgraph.Build(): %v", err)
	}
	return g
}

func renderGraph(t *testing.T, g *types.Graph, opts ...render.RenderOption) string {
	t.Helper()
	var b strings.Builder
	if err := render.WriteTree(&b, g, opts...); err != nil {
		t.Fatalf("render.WriteTree(): %v", err)
	}
	return b.String()
}

// TestRenderSingleProject verifies a lone project with no deps.
func TestRenderSingleProject(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"api", "go"},
	})
	g := mustGraph(t, ws)
	got := renderGraph(t, g)
	want := "api\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g)
	want := "api\n└── internal/db\n    └── internal/util\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g)
	// Only "root" has no predecessors (nothing depends on it).
	// Children are sorted alphabetically: left, shared.
	// "shared" is first visited under "left"; on the second encounter
	// under "root" it shows as "(visited)".
	want := "root\n├── left\n│   └── shared\n└── shared (visited)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g)
	want := "app-a\n└── shared\napp-b\n└── shared\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g, render.WithRoots("app-a"))
	want := "app-a\n└── shared\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g, render.WithMaxDepth(1))
	want := "api\n└── internal/db\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	want := "internal/util\n└── internal/db\n    └── api\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	got := renderGraph(t, g, render.WithSpell("go"))
	want := "api\n└── internal/db\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderEmpty verifies that an empty workspace produces no output.
func TestRenderEmpty(t *testing.T) {
	t.Parallel()
	ws := &types.Workspace{Root: "/fake", Projects: map[string]*types.Project{}}
	g := mustGraph(t, ws)
	got := renderGraph(t, g)
	if got != "" {
		t.Errorf("empty workspace: got %q, want empty", got)
	}
}

// ── PathsFromSeeds tests ──────────────────────────────────────────────

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
	if len(got) != 1 {
		t.Fatalf("got %d paths, want 1", len(got))
	}
	if got[0].Seed != "api" {
		t.Errorf("Seed = %q, want api", got[0].Seed)
	}
	if len(got[0].Chain) != 1 || got[0].Chain[0] != "api" {
		t.Errorf("Chain = %v, want [api]", got[0].Chain)
	}
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
	if len(got) != 1 {
		t.Fatalf("got %d paths, want 1", len(got))
	}
	want := []string{"c", "b", "a"}
	if !equalStringSlice(got[0].Chain, want) {
		t.Errorf("Chain = %v, want %v", got[0].Chain, want)
	}
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
	if len(got) != 0 {
		t.Errorf("got %v, want empty (api and web are unrelated)", got)
	}
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
	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2", len(got))
	}
	// Paths are sorted by seed name.
	if got[0].Seed != "seed-a" || got[1].Seed != "seed-b" {
		t.Errorf("seeds = %q %q, want seed-a seed-b", got[0].Seed, got[1].Seed)
	}
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
	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2; paths=%v", len(got), got)
	}
	if got[0].Seed != "a" || !equalStringSlice(got[0].Chain, []string{"a"}) {
		t.Errorf("seed a: got seed=%q chain=%v", got[0].Seed, got[0].Chain)
	}
	if got[1].Seed != "c" || !equalStringSlice(got[1].Chain, []string{"c", "b", "a"}) {
		t.Errorf("seed c: got seed=%q chain=%v", got[1].Seed, got[1].Chain)
	}
}

// ── NearCycles tests ──────────────────────────────────────────────────

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
	if len(ncs) != 5 {
		t.Errorf("got %d near-cycles, want 5: %v", len(ncs), ncs)
	}
}

// TestNearCyclesDisabled: depth=0 returns nil.
func TestNearCyclesDisabled(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"a", "go", "b"},
		{"b", "go"},
	})
	g := mustGraph(t, ws)
	ncs := g.NearCycles(context.Background(), 0)
	if ncs != nil {
		t.Errorf("depth=0: got %v, want nil", ncs)
	}
}

// TestNearCyclesIsolated: a project with no deps and no dependents has
// no near-cycles.
func TestNearCyclesIsolated(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{
		{"standalone", "go"},
	})
	g := mustGraph(t, ws)
	ncs := g.NearCycles(context.Background(), 3)
	if len(ncs) != 0 {
		t.Errorf("isolated project: got %v, want empty", ncs)
	}
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
	if len(ncs) != 1 {
		t.Fatalf("got %d near-cycles, want 1: %v", len(ncs), ncs)
	}
	nc := ncs[0]
	if nc.From != "b" || nc.To != "a" {
		t.Errorf("From=%q To=%q, want From=b To=a", nc.From, nc.To)
	}
	if !equalStringSlice(nc.BackPath, []string{"a", "b"}) {
		t.Errorf("BackPath=%v, want [a b]", nc.BackPath)
	}
}

// ── BlastRadius tests ─────────────────────────────────────────────────

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
	cases := map[string]int{"a": 1, "b": 2, "c": 3}
	for path, want := range cases {
		if got := br[path]; got != want {
			t.Errorf("BlastRadius[%s]=%d, want %d", path, got, want)
		}
	}
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
	if got := br["shared"]; got != 3 {
		t.Errorf("BlastRadius[shared]=%d, want 3", got)
	}
	if got := br["left"]; got != 2 {
		t.Errorf("BlastRadius[left]=%d, want 2", got)
	}
	if got := br["root"]; got != 1 {
		t.Errorf("BlastRadius[root]=%d, want 1", got)
	}
}

// ── NCCD tests ────────────────────────────────────────────────────────

// TestNCCDSingleNode: a single node has CCD=1, BBT=1, NCCD=1.
func TestNCCDSingleNode(t *testing.T) {
	t.Parallel()
	ws := buildWorkspace(t, [][]string{{"solo", "go"}})
	g := mustGraph(t, ws)
	nccd := g.NCCD()
	// N=1: CCD=1, BBT=(2*log2(2))-1=1, NCCD=1.
	if nccd < 0.99 || nccd > 1.01 {
		t.Errorf("NCCD=%f, want ~1.0 for single node", nccd)
	}
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
	nccd := g.NCCD()
	if nccd >= 1.0 {
		t.Errorf("NCCD=%f, want < 1.0 for star topology", nccd)
	}
}

// TestNCCDEmpty: empty graph returns 0.
func TestNCCDEmpty(t *testing.T) {
	t.Parallel()
	ws := &types.Workspace{Root: "/fake", Projects: map[string]*types.Project{}}
	g := mustGraph(t, ws)
	nccd := g.NCCD()
	if nccd != 0 {
		t.Errorf("NCCD=%f, want 0 for empty graph", nccd)
	}
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
	if err != nil {
		t.Fatalf("depgraph.Build() returned error: %v", err)
	}
	if g == nil {
		t.Fatal("depgraph.Build() returned nil")
	}

	got := buf.String()
	for _, want := range []string{
		"consumer=api",
		"missing_dep=internal/db-typo",
		"did_you_mean=internal/db",
		"magus.project.register",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log output missing %q\nfull output: %s", want, got)
		}
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
	if _, err := depgraph.Build(ws); err != nil {
		t.Fatalf("depgraph.Build(): %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("unexpected warning output: %s", got)
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── Strict mode tests ────────────────────────────────────────────────

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
	if err != nil {
		t.Fatalf("depgraph.Build() returned error: %v", err)
	}
	if g == nil {
		t.Fatal("depgraph.Build() returned nil")
	}
	if got := buf.String(); !strings.Contains(got, "internal/db-typo") {
		t.Errorf("warning missing dep: %s", got)
	}
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
	if err == nil {
		t.Fatal("depgraph.Build(): expected error in strict mode, got nil")
	}
	if !errors.Is(err, types.ErrUnregisteredDep) {
		t.Errorf("err=%v, want errors.Is(err, ErrUnregisteredDep)", err)
	}
	var ude *types.UnregisteredDepError
	if !errors.As(err, &ude) {
		t.Fatalf("err=%v, want errors.As(err, *UnregisteredDepError)", err)
	}
	if len(ude.Missing) != 1 || ude.Missing[0].Consumer != "api" || ude.Missing[0].Dep != "internal/db-typo" {
		t.Errorf("unexpected Missing: %+v", ude.Missing)
	}
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
	if err == nil {
		t.Fatal("depgraph.Build(): expected aggregated error, got nil")
	}
	var ude *types.UnregisteredDepError
	if !errors.As(err, &ude) {
		t.Fatalf("err=%v, want *UnregisteredDepError", err)
	}
	if len(ude.Missing) != 3 {
		t.Errorf("got %d missing deps, want 3: %+v", len(ude.Missing), ude.Missing)
	}
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
		if !strings.Contains(msg, want) {
			t.Errorf("Error() missing %q\nfull: %s", want, msg)
		}
	}
}

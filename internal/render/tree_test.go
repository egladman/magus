package render

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo is a minimal in-memory GraphRepository. WriteTree only reaches for
// Successors/Predecessors/Nodes (plus Graph.Project, which comes from the project
// map handed to NewGraph), so the remaining interface methods are stubs that a
// tree render never calls.
type fakeRepo struct {
	nodes []string
	succ  map[string][]string
	pred  map[string][]string
}

func (r *fakeRepo) Successors(path string) []string   { return r.succ[path] }
func (r *fakeRepo) Predecessors(path string) []string { return r.pred[path] }
func (r *fakeRepo) Nodes() []string                   { return r.nodes }

func (r *fakeRepo) TopoSort() []string                                { return nil }
func (r *fakeRepo) ReverseClosure(seeds []string) []string            { return nil }
func (r *fakeRepo) NearCycles(context.Context, int) []types.NearCycle { return nil }
func (r *fakeRepo) BlastRadius() map[string]int                       { return nil }
func (r *fakeRepo) NCCD() float64                                     { return 0 }
func (r *fakeRepo) PathsFromSeeds([]string, string) []types.AffectedPath {
	return nil
}

// buildGraph derives Predecessors from the given Successors adjacency (so tests
// declare edges once) and wires a project map with a spell per path.
func buildGraph(nodes []string, succ map[string][]string, spellOf map[string]string) *types.Graph {
	pred := map[string][]string{}
	for from, tos := range succ {
		for _, to := range tos {
			pred[to] = append(pred[to], from)
		}
	}
	projects := make(map[string]*types.Project, len(nodes))
	for _, n := range nodes {
		p := &types.Project{Path: n}
		if s := spellOf[n]; s != "" {
			p.Spell = s
		}
		projects[n] = p
	}
	return types.NewGraph(&fakeRepo{nodes: nodes, succ: succ, pred: pred}, projects)
}

// linearGraph: api -> db -> util (downstream = "depends on").
func linearGraph() *types.Graph {
	return buildGraph(
		[]string{"api", "db", "util"},
		map[string][]string{"api": {"db"}, "db": {"util"}},
		map[string]string{"api": "go", "db": "go", "util": "go"},
	)
}

func TestWriteTree_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteTree(&b, linearGraph()))
	// Downstream from the sole root (api) with no predecessors: whole chain, with
	// the box-drawing connectors deepening for each level.
	want := "api\n" +
		"└── db\n" +
		"    └── util\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_SortsRootsAndBranches(t *testing.T) {
	t.Parallel()
	// Two roots (api, cli) each depend on the same leaf; roots emit sorted, and a
	// node with two children uses the tee connector on all but the last child.
	g := buildGraph(
		[]string{"api", "cli", "core", "util"},
		map[string][]string{"api": {"core", "util"}, "cli": {"core"}},
		nil,
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g))
	want := "api\n" +
		"├── core\n" +
		"└── util\n" +
		"cli\n" +
		"└── core\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_WithRootsOverridesResolution(t *testing.T) {
	t.Parallel()
	// WithRoots pins the starting node, bypassing the no-predecessor root scan.
	var b strings.Builder
	require.NoError(t, WriteTree(&b, linearGraph(), WithRoots("db")))
	want := "db\n" +
		"└── util\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_WithMaxDepth(t *testing.T) {
	t.Parallel()
	// Depth 1 prints the root and its immediate children, then stops descending.
	var b strings.Builder
	require.NoError(t, WriteTree(&b, linearGraph(), WithMaxDepth(1)))
	want := "api\n" +
		"└── db\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_UpstreamDirection(t *testing.T) {
	t.Parallel()
	// Upstream walks predecessors, so roots are the sinks (nodes with no
	// successors). For api->db->util that root is util, dependents fanning out.
	var b strings.Builder
	require.NoError(t, WriteTree(&b, linearGraph(), WithDirection(types.Upstream)))
	want := "util\n" +
		"└── db\n" +
		"    └── api\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_VisitedGuard(t *testing.T) {
	t.Parallel()
	// Diamond: api depends on both auth and http, both depend on crypto. The
	// second time crypto is reached it is marked "(visited)" rather than expanded,
	// so a shared dependency is not re-rendered (and any cycle would terminate).
	g := buildGraph(
		[]string{"api", "auth", "http", "crypto"},
		map[string][]string{
			"api":  {"auth", "http"},
			"auth": {"crypto"},
			"http": {"crypto"},
		},
		nil,
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g))
	// Whole-output match (not substring Contains): the diamond renders deterministically,
	// so pin the exact tree - crypto expands under auth (first visit) and is marked
	// "(visited)" under http (second visit) rather than re-expanded.
	want := "api\n" +
		"├── auth\n" +
		"│   └── crypto\n" +
		"└── http\n" +
		"    └── crypto (visited)\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_WithSpellFiltersNodes(t *testing.T) {
	t.Parallel()
	// api(go) -> engine(rust) -> ffi(go). With spell=go the rust node is pruned as
	// a child, severing api from ffi. Root resolution then treats every go node
	// with no go-predecessor as a spell root: api (top) and ffi (its only
	// predecessor engine is rust, not go) both qualify, and each renders as a leaf
	// because the rust node between/under them is filtered out.
	g := buildGraph(
		[]string{"api", "engine", "ffi"},
		map[string][]string{"api": {"engine"}, "engine": {"ffi"}},
		map[string]string{"api": "go", "engine": "rust", "ffi": "go"},
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g, WithSpell("go")))
	// Roots emit sorted: api before ffi.
	require.Equal(t, "api\nffi\n", b.String())
}

func TestWriteTree_WithSpellResolvesSpellRoots(t *testing.T) {
	t.Parallel()
	// Spell filter without explicit roots: a same-spell node whose only predecessor
	// is a different spell is a root of its spell's forest. lib(go) sits under
	// api(rust), so lib is the go-root and prints with its go child.
	g := buildGraph(
		[]string{"api", "lib", "leaf"},
		map[string][]string{"api": {"lib"}, "lib": {"leaf"}},
		map[string]string{"api": "rust", "lib": "go", "leaf": "go"},
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g, WithSpell("go")))
	want := "lib\n" +
		"└── leaf\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_WithSpellSingleForest(t *testing.T) {
	t.Parallel()
	// An all-go chain: top has no predecessor so it is the sole go-root, and the
	// whole chain renders under the spell filter (nothing is pruned).
	g := buildGraph(
		[]string{"top", "mid", "bot"},
		map[string][]string{"top": {"mid"}, "mid": {"bot"}},
		map[string]string{"top": "go", "mid": "go", "bot": "go"},
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g, WithSpell("go")))
	want := "top\n" +
		"└── mid\n" +
		"    └── bot\n"
	require.Equal(t, want, b.String())
}

func TestWriteTree_WithSpellFallsBackToAllWhenNoRoot(t *testing.T) {
	t.Parallel()
	// Fallback branch in resolveRoots: when every same-spell node has a same-spell
	// predecessor, no node qualifies as a spell root, so it falls back to listing
	// all matching nodes. In a DAG that is impossible, but the render is graph-shape
	// agnostic, so a two-node go cycle (a<->b) makes both have a go predecessor.
	// The visited guard keeps recursion finite. Roots then list all go nodes sorted.
	g := buildGraph(
		[]string{"a", "b"},
		map[string][]string{"a": {"b"}, "b": {"a"}},
		map[string]string{"a": "go", "b": "go"},
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g, WithSpell("go")))
	got := b.String()
	// Both a and b are emitted as roots (sorted), each expanding once before the
	// visited guard trims the cycle.
	assert.Contains(t, got, "a\n", "expected node a")
	assert.Contains(t, got, "b\n", "expected node b")
	assert.Contains(t, got, "(visited)", "cycle should terminate via visited marker")
}

func TestWriteTree_SkipsMissingProjectRoots(t *testing.T) {
	t.Parallel()
	// A root path with no backing project entry is skipped (g.Project returns nil).
	// Pin roots to a real node and a phantom; only the real one renders.
	g := buildGraph(
		[]string{"real"},
		map[string][]string{},
		map[string]string{"real": "go"},
	)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g, WithRoots("real", "ghost")))
	require.Equal(t, "real\n", b.String())
}

func TestWriteTree_EmptyGraph(t *testing.T) {
	t.Parallel()
	g := buildGraph(nil, map[string][]string{}, nil)
	var b strings.Builder
	require.NoError(t, WriteTree(&b, g))
	require.Equal(t, "", b.String())
}

func TestHasSpellName(t *testing.T) {
	t.Parallel()
	// Matches the primary Spell field or any entry in the Spells fan-out list.
	primary := &types.Project{Spell: "go"}
	assert.True(t, hasSpellName(primary, "go"))
	assert.False(t, hasSpellName(primary, "rust"))

	multi := &types.Project{Spell: "go", Spells: []string{"go", "docker"}}
	assert.True(t, hasSpellName(multi, "docker"), "should match a secondary spell")
	assert.False(t, hasSpellName(multi, "bash"))
}

func TestSpellColor_UnknownFallsBackToUnspelled(t *testing.T) {
	t.Parallel()
	// A named spell returns its palette entry; anything unmapped falls back to the
	// "unspelled" gray so the switch/default branch in spellColor is exercised.
	fill, text := spellColor("go")
	assert.Equal(t, "#00ADD8", fill)
	assert.Equal(t, "#fff", text)

	unfill, untext := spellColor("cobol")
	wantFill, wantText := spellColor("unspelled")
	assert.Equal(t, wantFill, unfill, "unknown spell should reuse the unspelled fill")
	assert.Equal(t, wantText, untext, "unknown spell should reuse the unspelled text")
}

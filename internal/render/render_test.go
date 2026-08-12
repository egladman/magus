package render

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diamondOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"lib/auth", "lib/http"}},
			{Path: "lib/auth", SpellName: "go", Children: []string{"lib/crypto"}},
			{Path: "lib/http", SpellName: "go", Children: []string{"lib/crypto"}},
			{Path: "lib/crypto", SpellName: "go", Children: []string{}},
		},
	}
}

func linearOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"internal/db"}},
			{Path: "internal/db", SpellName: "go", Children: []string{"internal/util"}},
			{Path: "internal/util", SpellName: "go", Children: []string{}},
		},
	}
}

func singleOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes:     []types.Node{{Path: "api", SpellName: "go", Children: []string{}}},
	}
}

func emptyOutput() types.GraphOutput {
	return types.GraphOutput{Direction: "downstream", Nodes: nil}
}

func TestWriteGraphDOT_Single(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, singleOutput()))
	got := b.String()
	assert.Contains(t, got, `"api";`, "expected node declaration")
	assert.True(t, strings.HasPrefix(got, "digraph magus {"), "expected digraph header; got:\n%s", got)
}

func TestWriteGraphDOT_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, linearOutput()))
	got := b.String()
	assert.Contains(t, got, `"api" -> "internal/db";`, "missing edge api->internal/db")
	assert.Contains(t, got, `"internal/db" -> "internal/util";`, "missing edge internal/db->internal/util")
}

func TestWriteGraphDOT_Diamond(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, diamondOutput()))
	got := b.String()
	assert.Contains(t, got, `"lib/auth" -> "lib/crypto";`, "missing auth->crypto edge")
	assert.Contains(t, got, `"lib/http" -> "lib/crypto";`, "missing http->crypto edge")
}

func TestWriteGraphDOT_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, emptyOutput()))
	got := b.String()
	assert.True(t, strings.HasPrefix(got, "digraph magus {"), "expected digraph header on empty graph; got:\n%s", got)
	assert.NotContains(t, got, "->", "unexpected edge in empty graph")
}

func TestWriteGraphMermaid_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, linearOutput()))
	got := b.String()
	assert.Contains(t, got, "---\ntitle:", "missing frontmatter")
	assert.Contains(t, got, "graph TD", "missing 'graph TD' header")
	assert.Contains(t, got, "subgraph spell_", "missing subgraph")
	for _, label := range []string{`"api"`, `"internal/db"`, `"internal/util"`} {
		assert.Contains(t, got, label, "missing label %s", label)
	}
	assert.Contains(t, got, "-->", "missing edges")
}

func TestWriteGraphMermaid_PathEscaping(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "foo/bar", Children: []string{"baz-qux"}},
			{Path: "baz-qux", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.NotContains(t, got, "foo/bar[", "unsafe characters in Mermaid IDs")
	assert.NotContains(t, got, "baz-qux[", "unsafe characters in Mermaid IDs")
	assert.Contains(t, got, `"foo/bar"`, "label should preserve original path")
}

func TestWriteGraphMermaid_IDCollision(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "foo/bar", Children: []string{}},
			{Path: "foo_bar", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	// Both nodes should appear with distinct IDs (one will be foo_bar, the other foo_bar_1).
	assert.Contains(t, got, `"foo/bar"`, "expected both node labels")
	assert.Contains(t, got, `"foo_bar"`, "expected both node labels")
}

func TestMermaidIDs_ThreeWayCollision(t *testing.T) {
	t.Parallel()
	// "foo-bar", "foo/bar", and "foo_bar" all sanitize to the same base id
	// "foo_bar". Sorted order is "foo-bar" < "foo/bar" < "foo_bar" (by byte
	// value), so the base keeps its bare id and the other two must each get a
	// distinct numeric suffix - not the two colliding on "foo_bar_1".
	ids := mermaidIDs([]string{"foo/bar", "foo_bar", "foo-bar"})
	assert.Len(t, ids, 3, "one id per input path")
	seen := make(map[string]string, 3)
	for path, id := range ids {
		if other, ok := seen[id]; ok {
			t.Fatalf("paths %q and %q both got mermaid id %q", other, path, id)
		}
		seen[id] = path
	}
}

func TestWriteGraphMermaid_Subgraphs(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"engine"}},
			{Path: "engine", SpellName: "rust", Children: []string{}},
			{Path: "web", SpellName: "typescript", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	for _, sg := range []string{"subgraph spell_go", "subgraph spell_rust", "subgraph spell_typescript"} {
		assert.Contains(t, got, sg)
	}
}

func TestWriteGraphMermaid_CrossSpellEdgeLabel(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"engine", "util"}},
			{Path: "engine", SpellName: "rust", Children: []string{}},
			{Path: "util", SpellName: "go", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, `|"rust"|`, `expected cross-spell edge label |"rust"|`)
	// Same-spell edge (api→util) must be a plain -->, not labeled.
	assert.GreaterOrEqual(t, strings.Count(got, " --> "), 1, "expected at least one unlabeled edge")
}

func TestWriteGraphMermaid_RootHighlight(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Roots:     []string{"api"},
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"util"}},
			{Path: "util", SpellName: "go", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "classDef root", "expected 'classDef root'")
	assert.Contains(t, got, "root", "expected root class assignment for api")
	assert.Contains(t, got, "api", "expected root class assignment for api")
}

func TestWriteGraphMermaid_Exclusive(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Exclusive: true, Children: []string{"util"}},
			{Path: "util", SpellName: "go", Exclusive: false, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	// Exclusive node uses hexagon syntax {{...}}
	assert.Contains(t, got, `{{"api"}}`, "expected hexagon shape for exclusive node")
	// Non-exclusive node uses rectangle [...]
	assert.Contains(t, got, `["util"]`, "expected rectangle shape for non-exclusive node")
}

func TestWriteGraphMermaid_ClickHandler(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Dir: "/abs/path/api", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "click ", "expected click handler")
	assert.Contains(t, got, `"file:///abs/path/api"`, "expected file:// URL")
}

func TestWriteGraphMermaid_BlastRadius(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", BlastRadius: 12, Children: []string{"util"}},
			{Path: "util", SpellName: "go", BlastRadius: 0, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "BR=12", "expected BR=12 in label")
	assert.NotContains(t, got, "BR=0", "unexpected BR=0 label")
}

func TestWriteGraphMermaid_Duration(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "fast", SpellName: "go", DurationMs: 450, Children: []string{}},
			{Path: "mid", SpellName: "go", DurationMs: 2300, Children: []string{}},
			{Path: "slow", SpellName: "go", DurationMs: 80000, Children: []string{}},
			{Path: "none", SpellName: "go", DurationMs: 0, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	for _, want := range []string{"~450ms", "~2.3s", "~1m20s"} {
		assert.Contains(t, got, want)
	}
	// The "none" node must not get any duration label.
	assert.NotContains(t, got, `"none<br/>`, "unexpected duration label for DurationMs=0")
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0ms", FormatDuration(0))
	assert.Equal(t, "450ms", FormatDuration(450*time.Millisecond))
	assert.Equal(t, "999ms", FormatDuration(999*time.Millisecond))
	assert.Equal(t, "1s", FormatDuration(1000*time.Millisecond))
	assert.Equal(t, "2.3s", FormatDuration(2300*time.Millisecond))
	assert.Equal(t, "60s", FormatDuration(59999*time.Millisecond))
	assert.Equal(t, "1m", FormatDuration(60000*time.Millisecond))
	assert.Equal(t, "1m20s", FormatDuration(80000*time.Millisecond))
}

func TestWriteGraphMermaid_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, emptyOutput()))
	got := b.String()
	assert.Contains(t, got, "graph TD", "expected 'graph TD' even for empty graph")
}

func TestWriteGraphMermaid_Determinism(t *testing.T) {
	t.Parallel()
	out := diamondOutput()
	var b1, b2 strings.Builder
	require.NoError(t, WriteGraphMermaid(&b1, out))
	require.NoError(t, WriteGraphMermaid(&b2, out))
	assert.Equal(t, b1.String(), b2.String(), "WriteGraphMermaid is not deterministic")
}

// fakeRepo is a minimal in-memory DepGraphRepository. WriteTree only reaches for
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

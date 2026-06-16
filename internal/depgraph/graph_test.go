package depgraph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// build constructs a Graph from a simple spec: each entry is
// [path, dep1, dep2, ...]. Builder IDs are used only during construction.
func build(t *testing.T, entries [][]string) *Graph {
	t.Helper()
	b := New()
	bids := map[string]ID{}
	for _, e := range entries {
		bids[e[0]] = b.AddNode(e[0])
	}
	for _, e := range entries {
		from := bids[e[0]]
		for _, dep := range e[1:] {
			to, ok := bids[dep]
			if !ok {
				t.Fatalf("dep %q not registered", dep)
			}
			if err := b.AddEdge(from, to); err != nil {
				t.Fatalf("AddEdge(%s→%s): %v", e[0], dep, err)
			}
		}
	}
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// pid looks up a Graph ID by path string.
func pid(t *testing.T, g *Graph, path string) ID {
	t.Helper()
	id, ok := g.ID(path)
	if !ok {
		t.Fatalf("pid: %q not in graph", path)
	}
	return id
}

// pathOf returns the path string for id, asserting validity.
func pathOf(t *testing.T, g *Graph, id ID) string {
	t.Helper()
	p, ok := g.Path(id)
	if !ok {
		t.Fatalf("pathOf: id %d out of range", id)
	}
	return p
}

func chainPaths(g *Graph, ids []ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i], _ = g.Path(id)
	}
	return out
}

// ── Build ─────────────────────────────────────────────────────────────

func TestBuildCycleDetected(t *testing.T) {
	t.Parallel()
	b := New()
	a := b.AddNode("a")
	c := b.AddNode("c")
	_ = b.AddEdge(a, c)
	_ = b.AddEdge(c, a)
	_, err := b.Build()
	if err == nil {
		t.Fatal("Build: expected ErrCycle, got nil")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("err=%v, want errors.Is(err, ErrCycle)", err)
	}
}

func TestBuildSelfLoop(t *testing.T) {
	t.Parallel()
	b := New()
	x := b.AddNode("x")
	if err := b.AddEdge(x, x); err == nil {
		t.Fatal("AddEdge self-loop: expected error, got nil")
	}
}

func TestBuildDupEdgeOK(t *testing.T) {
	t.Parallel()
	b := New()
	a := b.AddNode("a")
	c := b.AddNode("c")
	_ = b.AddEdge(a, c)
	if err := b.AddEdge(a, c); err != nil {
		t.Fatalf("duplicate edge must be a no-op, got: %v", err)
	}
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Len() != 2 {
		t.Errorf("Len=%d, want 2", g.Len())
	}
}

// TestObserverOnErrorFiresOnCycle verifies Build calls obs.OnError with
// ErrCycle when cycle detection trips.
func TestObserverOnErrorFiresOnCycle(t *testing.T) {
	t.Parallel()
	var seen []error
	obs := &captureObserver{onErr: func(err error) { seen = append(seen, err) }}

	b := New()
	a := b.AddNode("a")
	c := b.AddNode("c")
	_ = b.AddEdge(a, c)
	_ = b.AddEdge(c, a)
	_, err := b.Build(WithObserver(obs))
	if err == nil {
		t.Fatal("Build: expected ErrCycle, got nil")
	}
	if len(seen) != 1 || !errors.Is(seen[0], ErrCycle) {
		t.Errorf("OnError: got %v, want [ErrCycle]", seen)
	}
}

// ── Path / ID / TopoOrder ─────────────────────────────────────────────

func TestPathOutOfRange(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})
	if p, ok := g.Path(NoID); ok || p != "" {
		t.Errorf("Path(NoID): got (%q,%v), want (\"\",false)", p, ok)
	}
	if p, ok := g.Path(ID(999)); ok || p != "" {
		t.Errorf("Path(999): got (%q,%v), want (\"\",false)", p, ok)
	}
}

// TestTopoOrderIsCloned verifies callers cannot corrupt internal state
// by mutating the returned topo slice.
func TestTopoOrderIsCloned(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	first := g.TopoOrder()
	for i := range first {
		first[i] = -42
	}
	second := g.TopoOrder()
	for _, id := range second {
		if id == -42 {
			t.Fatal("TopoOrder returned mutated cache; expected defensive copy")
		}
	}
}

// ── ReverseClosure ────────────────────────────────────────────────────

func TestReverseClosureLinear(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	c := pid(t, g, "c")
	got := g.ReverseClosure(nil, []ID{c})
	if len(got) != 3 {
		t.Errorf("got %v (len=%d), want 3 nodes", got, len(got))
	}
}

func TestReverseClosureIncludesSeeds(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"solo"}})
	id := pid(t, g, "solo")
	got := g.ReverseClosure(nil, []ID{id})
	if len(got) != 1 || pathOf(t, g, got[0]) != "solo" {
		t.Errorf("seed alone: got %v", got)
	}
}

// TestReverseClosureRejectsBadSeed proves an out-of-range seed does not
// panic; it is silently dropped.
func TestReverseClosureRejectsBadSeed(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})
	a := pid(t, g, "a")
	// One valid + one bogus: result includes only a.
	got := g.ReverseClosure(nil, []ID{ID(999), a, NoID})
	if len(got) != 1 || got[0] != a {
		t.Errorf("got %v, want [%d]", got, a)
	}
}

// ── BlastRadius ───────────────────────────────────────────────────────

func TestBlastRadiusLinear(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	br := g.BlastRadius()
	cases := map[string]int32{"a": 1, "b": 2, "c": 3}
	for path, want := range cases {
		id := pid(t, g, path)
		if br[id] != want {
			t.Errorf("BlastRadius[%s]=%d, want %d", path, br[id], want)
		}
	}
}

func TestBlastRadiusDiamond(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{
		{"root", "left", "shared"},
		{"left", "shared"},
		{"shared"},
	})
	br := g.BlastRadius()
	cases := map[string]int32{"shared": 3, "left": 2, "root": 1}
	for path, want := range cases {
		id := pid(t, g, path)
		if br[id] != want {
			t.Errorf("BlastRadius[%s]=%d, want %d", path, br[id], want)
		}
	}
}

// ── NCCD ─────────────────────────────────────────────────────────────

func TestNCCDSingleNode(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"solo"}})
	v := g.NCCD()
	if v < 0.99 || v > 1.01 {
		t.Errorf("NCCD=%f, want ~1.0 for single node", v)
	}
}

func TestNCCDEmpty(t *testing.T) {
	t.Parallel()
	g := build(t, nil)
	if v := g.NCCD(); v != 0 {
		t.Errorf("NCCD=%f, want 0 for empty graph", v)
	}
}

// TestNCCDEmptyEmitsEvent verifies the empty-graph short-circuit still
// fires the observer (regression guard).
func TestNCCDEmptyEmitsEvent(t *testing.T) {
	t.Parallel()
	var seen []QueryEvent
	obs := &captureObserver{onQuery: func(e QueryEvent) { seen = append(seen, e) }}

	b := New()
	g, err := b.Build(WithObserver(obs))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_ = g.NCCD()

	var ok bool
	for _, e := range seen {
		if e.Op == "nccd" {
			ok = true
			break
		}
	}
	if !ok {
		t.Errorf("NCCD on empty graph did not emit observer event; got %v", seen)
	}
}

func TestNCCDStarTopology(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{
		{"center"},
		{"leaf1", "center"},
		{"leaf2", "center"},
		{"leaf3", "center"},
		{"leaf4", "center"},
	})
	if v := g.NCCD(); v >= 1.0 {
		t.Errorf("NCCD=%f, want < 1.0 for star topology", v)
	}
}

// ── PathsFromSeeds ────────────────────────────────────────────────────

func TestPathsFromSeedsDirect(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"api"}})
	id := pid(t, g, "api")
	paths := g.PathsFromSeeds(id, []ID{id}, nil)
	if len(paths) != 1 || paths[0].Seed != id || len(paths[0].Chain) != 1 {
		t.Errorf("direct: got %v", paths)
	}
}

func TestPathsFromSeedsTransitive(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	a := pid(t, g, "a")
	c := pid(t, g, "c")
	paths := g.PathsFromSeeds(a, []ID{c}, nil)
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	chain := paths[0].Chain
	if len(chain) != 3 || pathOf(t, g, chain[0]) != "c" || pathOf(t, g, chain[2]) != "a" {
		t.Errorf("chain: %v", chainPaths(g, chain))
	}
}

func TestPathsFromSeedsUnreachable(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"api"}, {"web"}})
	api := pid(t, g, "api")
	web := pid(t, g, "web")
	paths := g.PathsFromSeeds(api, []ID{web}, nil)
	if len(paths) != 0 {
		t.Errorf("got %v, want empty", paths)
	}
}

func TestPathsFromSeedsMultiple(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{
		{"target", "seed-a", "seed-b"},
		{"seed-a"},
		{"seed-b"},
	})
	target := pid(t, g, "target")
	sa := pid(t, g, "seed-a")
	sb := pid(t, g, "seed-b")
	paths := g.PathsFromSeeds(target, []ID{sa, sb}, nil)
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	if pathOf(t, g, paths[0].Seed) != "seed-a" || pathOf(t, g, paths[1].Seed) != "seed-b" {
		got0, _ := g.Path(paths[0].Seed)
		got1, _ := g.Path(paths[1].Seed)
		t.Errorf("seed order: %v %v", got0, got1)
	}
}

// TestPathsFromSeedsRejectsBadTarget proves an out-of-range target does
// not panic.
func TestPathsFromSeedsRejectsBadTarget(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})
	a := pid(t, g, "a")
	if got := g.PathsFromSeeds(NoID, []ID{a}, nil); len(got) != 0 {
		t.Errorf("NoID target: got %v, want empty", got)
	}
	if got := g.PathsFromSeeds(ID(999), []ID{a}, nil); len(got) != 0 {
		t.Errorf("OOB target: got %v, want empty", got)
	}
}

// ── NearCycles ────────────────────────────────────────────────────────

func TestNearCyclesDisabled(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b"}})
	if ncs := g.NearCycles(context.Background(), 0); ncs != nil {
		t.Errorf("depth=0: got %v, want nil", ncs)
	}
}

func TestNearCyclesLinearChain(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d"}})
	ncs := g.NearCycles(context.Background(), 3)
	// Expected pairs: (b,a), (c,b), (c,a), (d,c), (d,b) = 5
	if len(ncs) != 5 {
		t.Errorf("got %d near-cycles, want 5: %v", len(ncs), ncs)
	}
}

func TestNearCyclesIsolated(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"standalone"}})
	if ncs := g.NearCycles(context.Background(), 3); len(ncs) != 0 {
		t.Errorf("isolated: got %v, want empty", ncs)
	}
}

func TestNearCyclesBackPath(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b"}})
	ncs := g.NearCycles(context.Background(), 3)
	if len(ncs) != 1 {
		t.Fatalf("got %d near-cycles, want 1", len(ncs))
	}
	nc := ncs[0]
	if pathOf(t, g, nc.From) != "b" || pathOf(t, g, nc.To) != "a" {
		t.Errorf("From=%s To=%s, want From=b To=a", pathOf(t, g, nc.From), pathOf(t, g, nc.To))
	}
	if len(nc.BackPath) != 2 || pathOf(t, g, nc.BackPath[0]) != "a" || pathOf(t, g, nc.BackPath[1]) != "b" {
		t.Errorf("BackPath=%v, want [a b]", chainPaths(g, nc.BackPath))
	}
}

func TestNearCyclesCancellable(t *testing.T) {
	t.Parallel()
	// Build a chain large enough that the loop has work to do.
	entries := make([][]string, 200)
	for i := range entries {
		entries[i] = []string{"n" + iota3(i)}
		if i > 0 {
			entries[i] = append(entries[i], "n"+iota3(i-1))
		}
	}
	g := build(t, entries)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Should short-circuit immediately on cancellation; no panic, returns
	// whatever it's seen so far (possibly empty).
	_ = g.NearCycles(ctx, 5)
}

func iota3(i int) string {
	// trivial 3-digit string
	return string(rune('a'+i/100)) + string(rune('a'+(i/10)%10)) + string(rune('a'+i%10))
}

// ── FanOut aliasing regression ────────────────────────────────────────

func TestFanOutDoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()
	a := NoopObserver{}
	b := NoopObserver{}
	caller := []Observer{a, nil, b}
	_ = FanOut(caller...)
	// Caller's slice must be unchanged after FanOut filters the nil.
	if len(caller) != 3 || caller[0] != a || caller[1] != nil || caller[2] != b {
		t.Errorf("FanOut mutated caller slice: %v", caller)
	}
}

// ── helpers ───────────────────────────────────────────────────────────

type captureObserver struct {
	onBuild func(BuildStats)
	onQuery func(QueryEvent)
	onErr   func(error)
}

func (c *captureObserver) OnBuild(s BuildStats) {
	if c.onBuild != nil {
		c.onBuild(s)
	}
}

func (c *captureObserver) OnQuery(e QueryEvent) {
	if c.onQuery != nil {
		c.onQuery(e)
	}
}

func (c *captureObserver) OnError(err error) {
	if c.onErr != nil {
		c.onErr(err)
	}
}

// (kept to silence unused-import warnings if Render comes back later)
var _ = strings.Builder{}

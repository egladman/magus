package depgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.True(t, ok, "dep %q not registered", dep)
			require.NoError(t, b.AddEdge(from, to), "AddEdge(%s→%s)", e[0], dep)
		}
	}
	g, err := b.Build()
	require.NoError(t, err, "Build")
	return g
}

// pid looks up a Graph ID by path string.
func pid(t *testing.T, g *Graph, path string) ID {
	t.Helper()
	id, ok := g.ID(path)
	require.True(t, ok, "pid: %q not in graph", path)
	return id
}

// pathOf returns the path string for id, asserting validity.
func pathOf(t *testing.T, g *Graph, id ID) string {
	t.Helper()
	p, ok := g.Path(id)
	require.True(t, ok, "pathOf: id %d out of range", id)
	return p
}

func TestBuildCycleDetected(t *testing.T) {
	t.Parallel()
	b := New()
	a := b.AddNode("a")
	c := b.AddNode("c")
	_ = b.AddEdge(a, c)
	_ = b.AddEdge(c, a)
	_, err := b.Build()
	require.Error(t, err, "Build: expected ErrCycle")
	assert.ErrorIs(t, err, ErrCycle)
}

func TestBuildSelfLoop(t *testing.T) {
	t.Parallel()
	b := New()
	x := b.AddNode("x")
	assert.Error(t, b.AddEdge(x, x), "AddEdge self-loop: expected error")
}

func TestBuildDupEdgeOK(t *testing.T) {
	t.Parallel()
	b := New()
	a := b.AddNode("a")
	c := b.AddNode("c")
	_ = b.AddEdge(a, c)
	require.NoError(t, b.AddEdge(a, c), "duplicate edge must be a no-op")
	g, err := b.Build()
	require.NoError(t, err, "Build")
	assert.Equal(t, 2, g.Len())
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
	require.Error(t, err, "Build: expected ErrCycle")
	require.Len(t, seen, 1)
	assert.ErrorIs(t, seen[0], ErrCycle)
}

func TestPathOutOfRange(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})

	p, ok := g.Path(NoID)
	assert.False(t, ok)
	assert.Empty(t, p)

	p, ok = g.Path(ID(999))
	assert.False(t, ok)
	assert.Empty(t, p)
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
	assert.NotContains(t, second, ID(-42), "TopoOrder returned mutated cache; expected defensive copy")
}

func TestReverseClosureLinear(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	c := pid(t, g, "c")
	got := g.ReverseClosure(nil, []ID{c})
	assert.Len(t, got, 3)
}

func TestReverseClosureIncludesSeeds(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"solo"}})
	id := pid(t, g, "solo")
	got := g.ReverseClosure(nil, []ID{id})
	require.Len(t, got, 1)
	assert.Equal(t, "solo", pathOf(t, g, got[0]))
}

// TestReverseClosureRejectsBadSeed proves an out-of-range seed does not
// panic; it is silently dropped.
func TestReverseClosureRejectsBadSeed(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})
	a := pid(t, g, "a")
	// One valid + one bogus: result includes only a.
	got := g.ReverseClosure(nil, []ID{ID(999), a, NoID})
	assert.Equal(t, []ID{a}, got)
}

func TestBlastRadiusLinear(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	br := g.BlastRadius()
	assert.Equal(t, int32(1), br[pid(t, g, "a")])
	assert.Equal(t, int32(2), br[pid(t, g, "b")])
	assert.Equal(t, int32(3), br[pid(t, g, "c")])
}

func TestBlastRadiusDiamond(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{
		{"root", "left", "shared"},
		{"left", "shared"},
		{"shared"},
	})
	br := g.BlastRadius()
	assert.Equal(t, int32(3), br[pid(t, g, "shared")])
	assert.Equal(t, int32(2), br[pid(t, g, "left")])
	assert.Equal(t, int32(1), br[pid(t, g, "root")])
}

func TestNCCDSingleNode(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"solo"}})
	assert.InDelta(t, 1.0, g.NCCD(), 0.01, "want ~1.0 for single node")
}

func TestNCCDEmpty(t *testing.T) {
	t.Parallel()
	g := build(t, nil)
	assert.Zero(t, g.NCCD(), "want 0 for empty graph")
}

// TestNCCDEmptyEmitsEvent verifies the empty-graph short-circuit still
// fires the observer (regression guard).
func TestNCCDEmptyEmitsEvent(t *testing.T) {
	t.Parallel()
	var seen []QueryEvent
	obs := &captureObserver{onQuery: func(e QueryEvent) { seen = append(seen, e) }}

	b := New()
	g, err := b.Build(WithObserver(obs))
	require.NoError(t, err, "Build")
	_ = g.NCCD()

	var ok bool
	for _, e := range seen {
		if e.Op == "nccd" {
			ok = true
			break
		}
	}
	assert.True(t, ok, "NCCD on empty graph did not emit observer event; got %v", seen)
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
	assert.Less(t, g.NCCD(), 1.0, "want < 1.0 for star topology")
}

func TestPathsFromSeedsDirect(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"api"}})
	id := pid(t, g, "api")
	paths := g.PathsFromSeeds(id, []ID{id}, nil)
	require.Len(t, paths, 1)
	assert.Equal(t, id, paths[0].Seed)
	assert.Len(t, paths[0].Chain, 1)
}

func TestPathsFromSeedsTransitive(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c"}})
	a := pid(t, g, "a")
	c := pid(t, g, "c")
	paths := g.PathsFromSeeds(a, []ID{c}, nil)
	require.Len(t, paths, 1)
	chain := paths[0].Chain
	require.Len(t, chain, 3)
	assert.Equal(t, "c", pathOf(t, g, chain[0]))
	assert.Equal(t, "a", pathOf(t, g, chain[2]))
}

func TestPathsFromSeedsUnreachable(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"api"}, {"web"}})
	api := pid(t, g, "api")
	web := pid(t, g, "web")
	paths := g.PathsFromSeeds(api, []ID{web}, nil)
	assert.Empty(t, paths)
}

func TestPathsFromSeedsMultiple(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{
		{"target", "seed-a", "seed-b"},
		{"seed-a"},
		{"seed-b"},
	})
	target := pid(t, g, "target")
	paths := g.PathsFromSeeds(target, []ID{pid(t, g, "seed-a"), pid(t, g, "seed-b")}, nil)
	require.Len(t, paths, 2)
	assert.Equal(t, "seed-a", pathOf(t, g, paths[0].Seed))
	assert.Equal(t, "seed-b", pathOf(t, g, paths[1].Seed))
}

// TestPathsFromSeedsRejectsBadTarget proves an out-of-range target does
// not panic.
func TestPathsFromSeedsRejectsBadTarget(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a"}})
	a := pid(t, g, "a")
	assert.Empty(t, g.PathsFromSeeds(NoID, []ID{a}, nil), "NoID target")
	assert.Empty(t, g.PathsFromSeeds(ID(999), []ID{a}, nil), "OOB target")
}

func TestNearCyclesDisabled(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b"}})
	assert.Nil(t, g.NearCycles(context.Background(), 0), "depth=0")
}

func TestNearCyclesLinearChain(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d"}})
	ncs := g.NearCycles(context.Background(), 3)
	// Expected pairs: (b,a), (c,b), (c,a), (d,c), (d,b) = 5
	assert.Len(t, ncs, 5)
}

func TestNearCyclesIsolated(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"standalone"}})
	assert.Empty(t, g.NearCycles(context.Background(), 3))
}

func TestNearCyclesBackPath(t *testing.T) {
	t.Parallel()
	g := build(t, [][]string{{"a", "b"}, {"b"}})
	ncs := g.NearCycles(context.Background(), 3)
	require.Len(t, ncs, 1)
	nc := ncs[0]
	assert.Equal(t, "b", pathOf(t, g, nc.From))
	assert.Equal(t, "a", pathOf(t, g, nc.To))
	require.Len(t, nc.BackPath, 2)
	assert.Equal(t, "a", pathOf(t, g, nc.BackPath[0]))
	assert.Equal(t, "b", pathOf(t, g, nc.BackPath[1]))
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
	assert.NotPanics(t, func() { _ = g.NearCycles(ctx, 5) })
}

func iota3(i int) string {
	// trivial 3-digit string
	return string(rune('a'+i/100)) + string(rune('a'+(i/10)%10)) + string(rune('a'+i%10))
}

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
